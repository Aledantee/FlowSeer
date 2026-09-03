package snmp

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// session is the [Session] implementation: it builds request PDUs, drives the
// per-session reactor, and translates replies to typed VarBinds. It
// holds no per-call mutex — concurrency safety comes
// from the reactor's request-id demux, so many operations can be in
// flight on one session at once.
//
// walkBulkMaxRepetitions is the MaxRepetitions a BulkWalk PDU requests per
// round; 50 is a common SNMP-client default.
const walkBulkMaxRepetitions = 50

type session struct {
	r         *reactor
	inst      *instruments
	version   Version
	community string
	timeout   time.Duration
	retries   int

	ignoreNonIncreasing bool
	maxWalkVars         int
	maxOIDs             int
}

// Get performs an SNMP Get for the supplied OIDs, returning one VarBind
// per requested OID in request order. Agent-level failures surface as
// [*PDUError]; transport errors are wrapped with the operation name.
func (s *session) Get(ctx context.Context, oids []OID, opts ...CallOption) ([]VarBind, error) {
	return s.simpleOp(ctx, "Get", pduGetRequest, nullVarbinds(oids), opts)
}

// GetNext performs an SNMP GetNext. It mirrors [session.Get] in shape and
// error handling.
func (s *session) GetNext(ctx context.Context, oids []OID, opts ...CallOption) ([]VarBind, error) {
	return s.simpleOp(ctx, "GetNext", pduGetNextRequest, nullVarbinds(oids), opts)
}

// Set performs an SNMP Set. The three SNMPv2 exception variants are
// receive-only on the wire and are rejected before any IO.
func (s *session) Set(ctx context.Context, vbs []VarBind, opts ...CallOption) ([]VarBind, error) {
	for i, vb := range vbs {
		if IsException(vb) {
			return nil, errs.New().Attr("type", fmt.Sprintf("%T", vb)).Attr("index", i).
				Msg("cannot send SNMPv2 exception VarBind")
		}
	}

	return s.simpleOp(ctx, "Set", pduSetRequest, vbs, opts)
}

// simpleOp is the shared single-PDU request/response path for Get,
// GetNext, and Set. It is wrapped in one operation span.
func (s *session) simpleOp(ctx context.Context, op string, typ pduType, vbs []VarBind, opts []CallOption) ([]VarBind, error) {
	callCfg := ApplyCallOptions(opts...)
	ctx, span := s.inst.startOp(ctx, op)
	defer span.End()

	req := s.newRequest(typ, vbs)
	resp, err := s.exchange(ctx, op, req, callCfg)
	if err != nil {
		s.inst.finishOp(ctx, span, op, 0, nil, err)
		return nil, err
	}

	pe := pduError(resp)
	s.inst.finishOp(ctx, span, op, len(resp.pdu.varbinds), pe, asError(pe))
	if pe != nil {
		return nil, pe
	}

	return resp.pdu.varbinds, nil
}

// asError returns pe as an error, or nil — avoiding the typed-nil pitfall
// of returning a nil *PDUError through the error interface.
func asError(pe *PDUError) error {
	if pe == nil {
		return nil
	}
	return pe
}

// GetBulk performs an SNMP GetBulk (v2c only). The public maxRepetitions
// parameter stays uint8 (no API change); it is encoded as a uint32 BER
// INTEGER on the wire. A tooBig agent response triggers automatic
// halve-and-retry down to a floor of 1.
func (s *session) GetBulk(ctx context.Context, nonRepeaters, maxRepetitions uint8, oids []OID, opts ...CallOption) ([]VarBind, error) {
	if s.version == V1 {
		return nil, ErrBulkUnsupported
	}
	callCfg := ApplyCallOptions(opts...)
	ctx, span := s.inst.startOp(ctx, "GetBulk")
	defer span.End()

	reps := int(maxRepetitions)
	for {
		req := s.newBulkRequest(oids, int(nonRepeaters), reps)
		resp, err := s.exchange(ctx, "GetBulk", req, callCfg)
		if err != nil {
			s.inst.finishOp(ctx, span, "GetBulk", 0, nil, err)
			return nil, err
		}

		if pe := pduError(resp); pe != nil {
			if pe.Status == TooBig {
				if n, retry := nextBulkReps(reps); retry {
					reps = n
					continue
				}
			}
			s.inst.finishOp(ctx, span, "GetBulk", 0, pe, pe)
			return nil, pe
		}

		s.inst.finishOp(ctx, span, "GetBulk", len(resp.pdu.varbinds), nil, nil)
		return resp.pdu.varbinds, nil
	}
}

// nextBulkReps halves a GetBulk max-repetitions toward a floor of 1 after a
// tooBig response. It returns the reduced value and whether another bulk
// retry is worthwhile; retry=false means the floor (1) was already reached, so
// the caller stops bulking — GetBulk surfaces the PDUError, while BulkWalk
// diverges and falls back to GetNext-per-OID (walk-toobig-fallback).
//
// "Fallback" here is the tooBig->GetNext degrade within a single walk; it is
// unrelated to watcher.go's BulkWalkFallbackThreshold / enterFallback concept
// ("indicator broken -> full walks").
func nextBulkReps(reps int) (int, bool) {
	if reps > 1 {
		return reps / 2, true // reps>1 ⇒ reps/2 ≥ 1, so the floor is implicit
	}
	return reps, false
}

// Walk performs a GetNext-driven subtree walk rooted at root.
func (s *session) Walk(ctx context.Context, root OID, opts ...CallOption) *Walker {
	return s.walk(ctx, root, opts, false)
}

// BulkWalk performs a GetBulk-driven subtree walk rooted at root. It
// requires SNMPv2c; on a v1 session the returned Walker fails immediately
// with [ErrBulkUnsupported] — this is also what makes a [Watcher]
// over a v1 native session surface a clear error rather than silently
// never ticking.
func (s *session) BulkWalk(ctx context.Context, root OID, opts ...CallOption) *Walker {
	return s.walk(ctx, root, opts, true)
}

// walk constructs the Walker and starts the pump that drives [runWalk].
// One span covers the whole walk; it opens on the pump's (cancellable)
// context so the per-PDU exchanges are children, and ends when the pump
// returns.
func (s *session) walk(ctx context.Context, root OID, opts []CallOption, bulk bool) *Walker {
	callCfg := ApplyCallOptions(opts...)
	op := "Walk"
	if bulk {
		op = "BulkWalk"
	}
	w := NewWalker(ctx, callCfg.RowBuffer)
	w.Pump(func(pctx context.Context) {
		opCtx, span := s.inst.startOp(pctx, op)
		defer span.End()
		s.runWalk(opCtx, w, root, bulk, callCfg, span, op)
	})
	return w
}

// runWalk is the pump body: it issues GetNext (or GetBulk) PDUs, yields
// each in-subtree VarBind, and enforces the walk-termination guards.
//
// Termination, in priority order: context cancellation; transport/decode
// error; agent PDU error (v1 end-of-MIB via NoSuchName is a clean exit);
// EndOfMibView marker; an OID leaving the root subtree; a non-increasing
// or repeated OID (cycle); the max-varbind budget.
func (s *session) runWalk(ctx context.Context, w *Walker, root OID, bulk bool, callCfg *CallConfig, span trace.Span, op string) {
	var (
		count   int
		walkErr error
		walkPE  *PDUError
	)
	// One span covers the whole walk; record its outcome on return.
	defer func() { s.inst.finishOp(ctx, span, op, count, walkPE, walkErr) }()

	if bulk && s.version == V1 {
		walkErr = ErrBulkUnsupported
		w.Fail(ErrBulkUnsupported)
		return
	}

	ignoreNonIncreasing := s.ignoreNonIncreasing
	if callCfg.IgnoreNonIncreasingSet {
		ignoreNonIncreasing = callCfg.IgnoreNonIncreasing
	}
	maxVars := s.maxWalkVars
	if callCfg.MaxWalkVars > 0 {
		maxVars = callCfg.MaxWalkVars
	}

	next := root
	var prev OID
	havePrev := false

	// useBulk starts as the requested mode and may degrade to GetNext within
	// this walk if the agent rejects even a single-repetition GetBulk with
	// tooBig (walk-toobig-fallback). reps is the per-walk GetBulk
	// max-repetitions, halved toward 1 on each tooBig before the GetNext
	// fallback engages.
	useBulk := bulk
	reps := walkBulkMaxRepetitions

	for {
		if err := ctx.Err(); err != nil {
			walkErr = err
			w.Fail(err)
			return
		}

		var req *message
		if useBulk {
			req = s.newBulkRequest([]OID{next}, 0, reps)
		} else {
			req = s.newRequest(pduGetNextRequest, nullVarbinds([]OID{next}))
		}
		resp, err := s.exchange(ctx, op, req, callCfg)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				walkErr = ctxErr
				w.Fail(ctxErr)
				return
			}
			walkErr = err
			w.Fail(err)
			return
		}
		if pe := pduError(resp); pe != nil {
			// tooBig: halve max-repetitions and retry the same cursor; once at
			// the floor, fall back to GetNext-per-OID rather than aborting the
			// walk (walk-toobig-fallback; MikroTik >50, Cisco/Nokia/F5). The
			// GetNext path reuses the same per-varbind loop below, so the
			// fallback inherits all exception/cycle handling.
			if useBulk && pe.Status == TooBig {
				if n, retry := nextBulkReps(reps); retry {
					reps = n
				} else {
					useBulk = false
				}
				continue
			}
			// v1 agents terminate a walk with NoSuchName once the cursor
			// steps past the last accessible OID (RFC 1157 §4.1.3) — a
			// clean exit, not a failure.
			if s.version == V1 && pe.Status == NoSuchName {
				return
			}
			walkPE, walkErr = pe, pe
			w.Fail(pe)
			return
		}
		if len(resp.pdu.varbinds) == 0 {
			return // natural end
		}

		var lastOID OID
		progressed := false
		for _, vb := range resp.pdu.varbinds {
			h := vb.GetHeader()

			if _, isEnd := vb.(EndOfMibViewVar); isEnd {
				// EndOfMibView is a value; yield it then stop. For a
				// single-OID GETBULK chain any preceding value varbinds in this
				// PDU were already yielded above, so no data is lost
				// (walk-mid-pdu-eomv).
				w.Send(h.OID, vb)
				count++
				return
			}
			// noSuch* exceptions are classified here, BEFORE the subtree-prefix
			// and cycle guards, so an exception OID never poisons the cycle
			// detector (prev) — mirroring the EndOfMibView handling above
			// (walk-nosuch-semantics).
			if _, isNoObj := vb.(NoSuchObjectVar); isNoObj {
				// noSuchObject: the object does not exist in the agent's MIB
				// view. In a single-subtree walk this terminates cleanly — the
				// subtree is absent past this point.
				return
			}
			if _, isNoInst := vb.(NoSuchInstanceVar); isNoInst {
				// noSuchInstance: the object exists but no instance is present
				// here. Skip it (do not yield) and advance the cursor past it so
				// the walk progresses. Advance only for an in-subtree OID that
				// moves forward of the request cursor; otherwise leave progress
				// unchanged so the no-progress guard below terminates a stuck
				// agent rather than looping. The skipped instance still charges
				// the walk budget — otherwise an agent streaming endless
				// increasing in-subtree noSuchInstance varbinds would advance
				// forever without ever tripping maxVars.
				if h.OID.HasPrefix(root) && h.OID.Compare(next) > 0 {
					count++
					if maxVars > 0 && count > maxVars {
						walkErr = errs.Wrapf(ErrMaxWalkVars, "budget %d exceeded", maxVars)
						w.Fail(walkErr)
						return
					}
					if !progressed || h.OID.Compare(lastOID) > 0 {
						lastOID = h.OID
					}
					progressed = true
				}
				continue
			}
			if !h.OID.HasPrefix(root) {
				return // stepped outside the requested subtree
			}
			if havePrev {
				switch cmp := h.OID.Compare(prev); {
				case cmp == 0:
					// Exact-repeat OID is an unambiguous cycle — always
					// abort, even in skip mode.
					walkErr = errs.Wrapf(ErrOIDNotIncreasing, "repeated OID %s", h.OID)
					w.Fail(walkErr)
					return
				case cmp < 0 && !ignoreNonIncreasing:
					walkErr = errs.Wrapf(ErrOIDNotIncreasing, "OID %s <= previous %s", h.OID, prev)
					w.Fail(walkErr)
					return
				case cmp < 0 && ignoreNonIncreasing:
					// Skip mode: drop the non-increasing varbind entirely —
					// do not yield it, do not count it, and leave the cursor
					// (prev/lastOID/progressed) at the last in-order OID so
					// the next GetNext resumes forward rather than regressing
					// to the offending OID.
					continue
				}
			}

			count++
			if maxVars > 0 && count > maxVars {
				walkErr = errs.Wrapf(ErrMaxWalkVars, "budget %d exceeded", maxVars)
				w.Fail(walkErr)
				return
			}
			if !w.Send(h.OID, vb) {
				return // consumer signaled stop
			}
			prev = h.OID
			havePrev = true
			lastOID = h.OID
			progressed = true
		}
		if progressed {
			next = lastOID
		} else {
			// Every varbind in the PDU was out-of-subtree or otherwise did
			// not advance the cursor; stop to avoid re-requesting the same
			// OID forever.
			return
		}
	}
}

// BulkWalkRaw performs a GetBulk-driven subtree walk that yields
// [RawVarBind]s — the fast path for generated MIB bindings, which
// match columns by byte prefix and decode known Kinds without the
// per-varbind OID materialization and interface boxing of [BulkWalk].
//
// Semantics mirror [Session.BulkWalk] exactly: same termination guards,
// same tooBig → GetNext degrade, same v1 [ErrBulkUnsupported] failure.
// The guard logic runs on canonical BER bytes (order-equivalent to arc
// order; pinned by the byte-order property test). Responses that cannot
// take the raw wire path — v3/USM sessions, or off-spec responses the
// mirror validation refuses — degrade transparently: the yielded
// RawVarBinds then carry a pre-decoded VB and consumers fall back to
// their generic decode arm.
func (s *session) BulkWalkRaw(ctx context.Context, root OID, opts ...CallOption) *RawWalker {
	callCfg := ApplyCallOptions(opts...)
	w := NewRawWalker(ctx, callCfg.RowBuffer)
	w.Pump(func(pctx context.Context) {
		opCtx, span := s.inst.startOp(pctx, "BulkWalkRaw")
		defer span.End()
		s.runWalkRaw(opCtx, w, root, callCfg, span, "BulkWalkRaw")
	})
	return w
}

// rawWalkItem is one response varbind in the raw walk engine's working
// form: the canonical BER name-content octets plus either the raw value
// TLV (wire fast path) or the pre-decoded VarBind (v3 / degraded path).
type rawWalkItem struct {
	oidC []byte
	tag  byte
	val  []byte
	vb   VarBind
}

func (it rawWalkItem) rv() RawVarBind {
	return RawVarBind{OID: it.oidC, Tag: it.tag, Value: it.val, VB: it.vb}
}

// rawItemsOf projects a response PDU into rawWalkItems. A rawVBL
// response (already mirror-validated by the read loop) is re-framed in
// place without decoding; a pre-decoded response (v3/USM, or an eager
// fallback) materializes each varbind's canonical name octets so the
// byte-level guards stay uniform across both sources.
func rawItemsOf(p *pdu) ([]rawWalkItem, error) {
	return rawItemsOfLimit(p, 0)
}

func rawItemsOfLimit(p *pdu, limit int) ([]rawWalkItem, error) {
	if p.rawVBL != nil {
		listContent, _, err := parseSequence(p.rawVBL, tagSequence, 1)
		if err != nil {
			return nil, errs.Wrap(err, "decode varbind list")
		}
		capacity := max(1, len(listContent)/10)
		if limit > 0 {
			capacity = min(capacity, limit)
		}
		items := make([]rawWalkItem, 0, capacity)
		for len(listContent) > 0 {
			if limit > 0 && len(items) == limit {
				return nil, errs.Msg("table walk response exceeds requested varbind count")
			}
			vbContent, consumed, err := parseSequence(listContent, tagSequence, 2)
			if err != nil {
				return nil, errs.Wrap(err, "decode varbind")
			}
			_, nameContent, nameUsed, err := parseTLV(vbContent)
			if err != nil {
				return nil, errs.Wrap(err, "decode varbind name")
			}
			valTag, valContent, _, err := parseTLV(vbContent[nameUsed:])
			if err != nil {
				return nil, errs.Wrap(err, "decode varbind value")
			}
			items = append(items, rawWalkItem{oidC: nameContent, tag: valTag, val: valContent})
			listContent = listContent[consumed:]
		}
		return items, nil
	}
	if limit > 0 && len(p.varbinds) > limit {
		return nil, errs.Msg("table walk response exceeds requested varbind count")
	}
	items := make([]rawWalkItem, 0, len(p.varbinds))
	for _, vb := range p.varbinds {
		items = append(items, rawWalkItem{oidC: encodeOIDContent(vb.GetHeader().OID), vb: vb})
	}
	return items, nil
}

// rawOIDString renders canonical name octets for an error message,
// falling back to hex if the octets do not decode.
func rawOIDString(oidC []byte) string {
	o, err := decodeOID(oidC)
	if err != nil {
		return fmt.Sprintf("%x", oidC)
	}
	return o.String()
}

// runWalkRaw is the raw twin of [session.runWalk]: identical guard
// logic and termination priority, expressed over canonical BER name
// octets instead of decoded OIDs. Any change to runWalk's semantics
// must be mirrored here; the walk-engine differential test pins the two
// engines to identical behavior over the same responses.
func (s *session) runWalkRaw(ctx context.Context, w *RawWalker, root OID, callCfg *CallConfig, span trace.Span, op string) {
	var (
		count   int
		walkErr error
		walkPE  *PDUError
	)
	defer func() { s.inst.finishOp(ctx, span, op, count, walkPE, walkErr) }()

	if s.version == V1 {
		walkErr = ErrBulkUnsupported
		w.Fail(ErrBulkUnsupported)
		return
	}

	ignoreNonIncreasing := s.ignoreNonIncreasing
	if callCfg.IgnoreNonIncreasingSet {
		ignoreNonIncreasing = callCfg.IgnoreNonIncreasing
	}
	maxVars := s.maxWalkVars
	if callCfg.MaxWalkVars > 0 {
		maxVars = callCfg.MaxWalkVars
	}

	rootC := encodeOIDContent(root)
	nextC := rootC
	var prevC []byte
	havePrev := false

	useBulk := true
	reps := walkBulkMaxRepetitions

	for {
		if err := ctx.Err(); err != nil {
			walkErr = err
			w.Fail(err)
			return
		}

		nextOID, err := decodeOID(nextC)
		if err != nil {
			walkErr = err
			w.Fail(err)
			return
		}
		var req *message
		if useBulk {
			req = s.newBulkRequest([]OID{nextOID}, 0, reps)
		} else {
			req = s.newRequest(pduGetNextRequest, nullVarbinds([]OID{nextOID}))
		}
		req.wantRaw = true
		resp, err := s.exchange(ctx, op, req, callCfg)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				walkErr = ctxErr
				w.Fail(ctxErr)
				return
			}
			walkErr = err
			w.Fail(err)
			return
		}
		// pduError enriches from the varbind at error-index; a raw
		// response defers that decode to here, on the error path only.
		if resp.pdu.errorStatus != NoError && resp.pdu.rawVBL != nil {
			if vbs, verr := decodeVarBindList(resp.pdu.rawVBL, 1); verr == nil {
				resp.pdu.varbinds = vbs
			}
		}
		if pe := pduError(resp); pe != nil {
			// tooBig: halve max-repetitions and retry the same cursor;
			// at the floor, degrade to GetNext-per-OID
			// (walk-toobig-fallback) — identical to runWalk.
			if useBulk && pe.Status == TooBig {
				if n, retry := nextBulkReps(reps); retry {
					reps = n
				} else {
					useBulk = false
				}
				continue
			}
			walkPE, walkErr = pe, pe
			w.Fail(pe)
			return
		}
		items, err := rawItemsOf(&resp.pdu)
		if err != nil {
			walkErr = err
			w.Fail(err)
			return
		}
		if len(items) == 0 {
			return // natural end
		}

		var lastC []byte
		progressed := false
		for _, it := range items {
			switch it.rv().exceptionTag() {
			case tagEndOfMibView:
				// Yield the marker then stop (walk-mid-pdu-eomv),
				// exactly as runWalk yields EndOfMibViewVar.
				w.Send(it.rv())
				count++
				return
			case tagNoSuchObject:
				// Subtree absent past this point — clean exit.
				return
			case tagNoSuchInstance:
				// Skip (no yield) but advance the cursor for an
				// in-subtree, forward-moving OID; charge the budget so a
				// stream of these cannot walk forever
				// (walk-nosuch-semantics).
				if bytes.HasPrefix(it.oidC, rootC) && cmpOIDWire(it.oidC, nextC) > 0 {
					count++
					if maxVars > 0 && count > maxVars {
						walkErr = errs.Wrapf(ErrMaxWalkVars, "budget %d exceeded", maxVars)
						w.Fail(walkErr)
						return
					}
					if !progressed || cmpOIDWire(it.oidC, lastC) > 0 {
						lastC = it.oidC
					}
					progressed = true
				}
				continue
			}
			if !bytes.HasPrefix(it.oidC, rootC) {
				return // stepped outside the requested subtree
			}
			if havePrev {
				switch cmp := cmpOIDWire(it.oidC, prevC); {
				case cmp == 0:
					walkErr = errs.Wrapf(ErrOIDNotIncreasing, "repeated OID %s", rawOIDString(it.oidC))
					w.Fail(walkErr)
					return
				case cmp < 0 && !ignoreNonIncreasing:
					walkErr = errs.Wrapf(ErrOIDNotIncreasing, "OID %s <= previous %s", rawOIDString(it.oidC), rawOIDString(prevC))
					w.Fail(walkErr)
					return
				case cmp < 0 && ignoreNonIncreasing:
					// Skip mode: drop the varbind, keep the cursor at the
					// last in-order OID.
					continue
				}
			}

			count++
			if maxVars > 0 && count > maxVars {
				walkErr = errs.Wrapf(ErrMaxWalkVars, "budget %d exceeded", maxVars)
				w.Fail(walkErr)
				return
			}
			if !w.Send(it.rv()) {
				return // consumer signaled stop
			}
			prevC = it.oidC
			havePrev = true
			lastC = it.oidC
			progressed = true
		}
		if progressed {
			nextC = lastC
		} else {
			// No varbind advanced the cursor; stop to avoid re-requesting
			// the same OID forever.
			return
		}
	}
}

// Close releases the session's socket and stops the reactor. Idempotent
// and safe under concurrent invocation; subsequent operations return
// [ErrSessionClosed] (enforced by the reactor's registry).
func (s *session) Close() error {
	return s.r.close()
}

// exchange runs one request/response round-trip, applying per-call timeout
// and retry overrides, then validates the reply's version/community.
// It returns the decoded response message; PDU-error inspection is
// the caller's job (so GetBulk can branch on tooBig).
func (s *session) exchange(ctx context.Context, op string, req *message, callCfg *CallConfig) (*message, error) {
	timeout := s.timeout
	if callCfg.TimeoutSet && callCfg.Timeout > 0 {
		timeout = callCfg.Timeout
	}
	retries := s.retries
	if callCfg.RetriesSet {
		retries = callCfg.Retries
	}

	start := time.Now()

	// v3 routes through the USM path: the scoped PDU is wrapped, discovered,
	// authenticated/encrypted, and the reply is gated by the reactor's USM
	// read-loop. The decoded scoped PDU is re-wrapped into a message
	// so the PDU-error / varbind handling above this layer is identical to
	// v1/v2c. Identity validation already happened in the gate, so the
	// v1/v2c validateResponse community/version check is skipped.
	if s.version == V3 {
		res, err := s.r.v3RoundTrip(ctx, req.pdu, timeout, retries)
		s.inst.recordRequest(ctx, op, time.Since(start))
		if err != nil {
			return nil, errs.Wrap(err, op)
		}
		if res.isReport || res.scoped == nil {
			return nil, errs.Wrap(ErrReportUnexpected, op)
		}
		return &message{version: V3, pdu: res.scoped.pdu}, nil
	}

	resp, err := s.r.roundTrip(ctx, req, timeout, retries)
	s.inst.recordRequest(ctx, op, time.Since(start))
	if err != nil {
		return nil, errs.Wrap(err, op)
	}

	if err := validateResponse(resp, s.version, s.community); err != nil {
		return nil, errs.Wrap(err, op)
	}

	return resp, nil
}

// newRequest builds a request message with the session's version and
// community and the given PDU type and varbinds. The request-id is left
// zero; the reactor assigns a unique one per transmit.
func (s *session) newRequest(typ pduType, vbs []VarBind) *message {
	return &message{
		version:   s.version,
		community: s.community,
		pdu:       pdu{typ: typ, varbinds: vbs},
	}
}

// newBulkRequest builds a GetBulkRequest message with the given
// non-repeaters and max-repetitions.
func (s *session) newBulkRequest(oids []OID, nonRepeaters, maxRepetitions int) *message {
	return &message{
		version:   s.version,
		community: s.community,
		pdu: pdu{
			typ:            pduGetBulkRequest,
			nonRepeaters:   nonRepeaters,
			maxRepetitions: maxRepetitions,
			varbinds:       nullVarbinds(oids),
		},
	}
}
