package snmp

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.aledante.io/as"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// reactor.go is the per-session UDP transport. A single read-loop
// owns the socket and demultiplexes every inbound datagram to the waiting
// caller by request-id (the DNS-stub-resolver pattern), replacing — not
// reimplementing — the gosnmp per-session mutex. The Session is therefore
// safe for concurrent use and many requests can be in flight at once.
//
// The socket is *connected* to the peer by default (net.DialUDP): the kernel
// caches the route so each send is a plain Write rather than a WriteToUDP
// that re-resolves the destination per datagram (a measurable per-op and
// cold-start win — the unconnected path was also the source of an occasional
// first-reply drop), and the kernel drops replies from any other source for
// free. The cost is that a connected socket discards replies whose source
// address differs from the dialed peer, so devices behind an HA VIP or a
// clustered/multi-homed front-end that answer from a non-dialed address need
// the unconnected socket: opt out with [WithMultiHomedPeer], which restores
// net.ListenUDP and matches replies by request-id regardless of source.
// On that unconnected path, optional user-space source validation is still
// available and off by default; see [reactorConfig.validateSrc].

// maxUDPPayload is the largest UDP datagram an SNMP agent can send. The
// receive buffer is sized to it so a large-but-legitimate reply is never
// silently truncated.
const maxUDPPayload = 65507

// defaultMaxInFlight bounds the per-session in-flight registry when the
// caller does not override it. It caps the registry so
// a partition or flood cannot grow it without bound; it pairs with the
// in-flight gauge.
const defaultMaxInFlight = 1024

// Reactor sentinel errors.
var (
	// errAtCapacity is returned by a send when the in-flight registry is
	// already at [reactorConfig.maxInFlight]; it is not retryable.
	errAtCapacity = errs.Msg("in-flight request limit reached")
	// errTimeout is the wire-level per-PDU timeout, returned after all
	// retransmits are exhausted without a reply and without a context
	// deadline firing. A context deadline surfaces as the unwrapped
	// context error instead.
	errTimeout = errs.Msg("request timed out")
)

// ridCounter is the process-wide request-id source. It is seeded from
// crypto/rand at init: a zero-based sequence
// would let an off-path attacker predict the next id and inject a forged
// reply, since source-address validation is off by default. The counter
// only guarantees cross-session id distinctness; routing is per-session
// against each reactor's own registry.
var ridCounter atomic.Uint32

// msgIDCounter is the process-wide v3 msgID source, kept SEPARATE from
// ridCounter: v3 demux keys on msgID, and the inner scoped-PDU
// request-id stays an independent post-decrypt defense-in-depth check.
// Seeding it from crypto/rand is a security requirement, not just hygiene —
// a predictable msgID narrows the off-path guessing space during the
// unauthenticated discovery window, where msgID unpredictability is
// one of only two defenses.
var msgIDCounter atomic.Uint32

func init() {
	var b [4]byte
	if _, err := crand.Read(b[:]); err == nil {
		ridCounter.Store(binary.BigEndian.Uint32(b[:]))
	}
	var b2 [4]byte
	if _, err := crand.Read(b2[:]); err == nil {
		msgIDCounter.Store(binary.BigEndian.Uint32(b2[:]))
	}
	// On the vanishingly unlikely crypto/rand failure the counters stay at
	// zero — a degraded but still-functional fallback, not a panic at
	// package init.
}

// nextRequestIDValue returns the next process-wide request-id masked to
// the positive 31-bit range. SNMP request-id is a signed 32-bit INTEGER,
// so the sign bit is cleared to keep every generated id non-negative even
// across a counter wrap.
func nextRequestIDValue() int32 {
	return int32(ridCounter.Add(1) & 0x7fffffff)
}

// nextMsgIDValue returns a fresh v3 msgID drawn from crypto/rand, masked to
// the positive 31-bit range (RFC 3412 msgID is 0..2^31-1). Each msgID is drawn
// independently rather than from a +1 counter so that observing one msgID does
// not let an off-path attacker predict the next — msgID unpredictability is one
// of only two defenses during the unauthenticated discovery window
// (usm-msgid-predictability). In-flight uniqueness is guaranteed by registerV3's
// collision-retry loop, so a (vanishingly rare) repeat is simply redrawn. On a
// crypto/rand failure it degrades to the seeded counter — uniqueness preserved,
// only the unpredictability weakened — rather than failing the send.
func nextMsgIDValue() int32 {
	var b [4]byte
	if _, err := crand.Read(b[:]); err != nil {
		return int32(msgIDCounter.Add(1) & 0x7fffffff)
	}
	return int32(binary.BigEndian.Uint32(b[:]) & 0x7fffffff)
}

// waiter is one outstanding request's delivery slot. The channel is
// buffered (cap 1) so the read-loop's delivery never blocks even if the
// caller has already moved on.
type waiter struct {
	ch chan *message
	// raw marks a waiter whose reply should be delivered with the
	// varbind list undecoded (pdu.rawVBL) when the wire form permits —
	// the raw-delivery fast path. The read loop falls back to eager decode when
	// [validateRawVarBindList] refuses the response.
	raw bool
}

// v3Waiter is the v3 analog of [waiter], keyed by msgID instead of the
// scoped-PDU request-id. Its fields are immutable once registered, so the
// read-loop can read expectRID/level/isDiscovery lock-free while it runs the
// expensive crypto gate, taking r.mu only for the final deliver.
type v3Waiter struct {
	ch chan *v3Result
	// expectRID is the inner scoped-PDU request-id the read-loop checks
	// post-decrypt (defense-in-depth, independent of msgID).
	expectRID int32
	// level is the session's requested security level (the downgrade check).
	level SecurityLevel
	// isDiscovery marks the unauthenticated discovery probe whose reply is
	// accepted under the discovery carve-out only.
	isDiscovery bool
}

// v3Result is what the read-loop delivers to a v3 waiter: either a verified
// scoped PDU (a data reply) or a classified Report (discovery / time sync).
type v3Result struct {
	scoped *scopedPDU

	isReport       bool
	report         reportOutcome
	reportEngineID []byte
	reportBoots    int32
	reportTime     int32
}

// reactorConfig is the transport-level configuration the Session derives
// from its [SessionConfig]. It is deliberately decoupled from
// SessionConfig so the reactor is independently testable. Per-PDU timeout
// and retry count are not held here: they are passed to [reactor.roundTrip]
// per call so a [CallOption] can override the session defaults.
type reactorConfig struct {
	peer        *net.UDPAddr
	validateSrc bool
	maxInFlight int
	// multiHomed selects the unconnected socket so
	// replies arriving from a source address other than the dialed peer — HA
	// VIPs, clustered or multi-homed devices — are still accepted and matched
	// by request-id. The default (false) connects the socket; see the package
	// comment on socket connectedness.
	multiHomed bool

	// version and community are the session's expected values. The
	// read-loop drops any datagram whose version/community disagree before
	// it can satisfy (or fail) an in-flight request, so a forged reply on a
	// guessed request-id cannot displace the genuine one.
	version   Version
	community string

	// usm is non-nil for a v3 session; the read-loop then routes datagrams
	// through the USM gate and demuxes by msgID instead of request-id.
	usm *usmContext
}

// reactor owns one UDP socket, its read-loop, and the per-session
// in-flight registry.
type reactor struct {
	conn *net.UDPConn
	peer *net.UDPAddr

	// rxBuf is the single receive buffer (maxUDPPayload), reused by readLoop
	// for the reactor's whole lifetime — one datagram is decoded before the
	// next read, so no per-datagram buffer is needed. It is an explicit field
	// (not a readLoop local) so the 64 KiB allocation is intentional and
	// heap-stable rather than left to escape analysis, which otherwise flips
	// the buffer between stack and heap as unrelated package code shifts the
	// inlining budget. Only readLoop touches it, so it needs no lock.
	rxBuf []byte

	// connected reports whether the UDP socket is connected to the peer
	// (the default). A connected socket lets sends use Write (the kernel
	// caches the route, avoiding a per-datagram destination resolution) and
	// makes the kernel drop replies from any other source for free. When
	// false (WithMultiHomedPeer) the socket is unconnected and sends use
	// WriteToUDP so HA/clustered devices answering from a different address
	// are still received.
	connected bool

	validateSrc bool
	maxInFlight int

	// version and community are the session's expected reply values; a
	// datagram that does not match both is dropped in the read-loop.
	version   Version
	community string

	// inst is the shared instrumentation; nil when the reactor is used
	// outside Dial (e.g. unit tests), in which case recording is skipped.
	// Set once by Dial before any traffic, so reads need no lock.
	inst *instruments

	// logCtx carries the dial-time logger attributes for read-loop debug
	// logging, detached from the dial context's cancellation so logging
	// survives after Dial returns.
	logCtx context.Context

	// usm is non-nil for a v3 session. baseline holds the discovered
	// authoritative engine's boots/time. discoMu serializes the lazy,
	// retryable single-flight discovery.
	usm      *usmContext
	baseline *engineBaseline
	discoMu  sync.Mutex

	mu         sync.Mutex
	inflight   map[int32]*waiter
	v3inflight map[int32]*v3Waiter
	closed     bool

	closeOnce sync.Once
	closeCh   chan struct{}
	loopDone  chan struct{}
	closeErr  error // socket-close result; set exactly once under closeOnce

	dropped atomic.Uint64
}

// newReactor opens the UDP socket and starts the read-loop. ctx supplies
// logger attributes only; the reactor's lifetime is bounded by
// [reactor.close], not by ctx.
func newReactor(ctx context.Context, cfg reactorConfig) (*reactor, error) {
	if cfg.peer == nil {
		return nil, errs.Msg("reactor requires a peer address")
	}
	// Connected socket by default (fast send, kernel source-filtering);
	// unconnected only for multi-homed/HA peers that answer from a different
	// address. See the package comment on socket connectedness.
	connected := !cfg.multiHomed
	var (
		conn *net.UDPConn
		err  error
	)
	if connected {
		conn, err = net.DialUDP("udp", nil, cfg.peer)
	} else {
		conn, err = net.ListenUDP("udp", nil)
	}
	if err != nil {
		return nil, errs.Wrap(err, "snmp: open udp socket")
	}
	maxInFlight := cfg.maxInFlight
	if maxInFlight <= 0 {
		maxInFlight = defaultMaxInFlight
	}
	r := &reactor{
		conn:        conn,
		peer:        cfg.peer,
		connected:   connected,
		rxBuf:       make([]byte, maxUDPPayload),
		validateSrc: cfg.validateSrc,
		maxInFlight: maxInFlight,
		version:     cfg.version,
		community:   cfg.community,
		usm:         cfg.usm,
		logCtx:      context.WithoutCancel(ctx),
		inflight:    make(map[int32]*waiter),
		v3inflight:  make(map[int32]*v3Waiter),
		closeCh:     make(chan struct{}),
		loopDone:    make(chan struct{}),
	}
	if cfg.usm != nil {
		r.baseline = &engineBaseline{}
	}
	go r.readLoop()
	return r, nil
}

// register allocates a request-id unique among the session's in-flight
// ids and installs a waiter for it. It returns [errAtCapacity] when the
// registry is full and [ErrSessionClosed] once the reactor is
// closed.
func (r *reactor) register(raw bool) (int32, *waiter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, nil, ErrSessionClosed
	}
	if len(r.inflight) >= r.maxInFlight {
		return 0, nil, errAtCapacity
	}
	for {
		id := nextRequestIDValue()
		if _, exists := r.inflight[id]; exists {
			// Wrap-collision with a still-in-flight id: skip to the next.
			continue
		}
		w := &waiter{ch: make(chan *message, 1), raw: raw}
		r.inflight[id] = w
		return id, w, nil
	}
}

// rawWanted reports whether the waiter registered under id (if any)
// asked for raw varbind delivery. The read loop consults it after
// decoding a reply's header, before deciding how to handle the varbind
// list; the registered/absent race with a concurrent deregister is
// harmless — deliver re-checks and an absent waiter counts as a drop
// either way.
func (r *reactor) rawWanted(id int32) (raw, found bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.inflight[id]
	if !ok {
		return false, false
	}
	return w.raw, true
}

// deregister removes id from the registry. Idempotent: a delivered id has
// already been removed, so the per-attempt deferred deregister is a no-op
// in that case (every retry id is removed exactly once).
func (r *reactor) deregister(id int32) {
	r.mu.Lock()
	delete(r.inflight, id)
	r.mu.Unlock()
}

// deliver routes a decoded reply to its waiter and removes the id so a
// late duplicate cannot reach a future same-id request. It reports
// whether a waiter was found; an unmatched id is the caller's signal to
// count a drop.
func (r *reactor) deliver(id int32, m *message) bool {
	r.mu.Lock()
	w, ok := r.inflight[id]
	if ok {
		delete(r.inflight, id)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	// Buffered cap-1 channel: never blocks, and the id is already removed
	// so no second value can be delivered here.
	w.ch <- m
	return true
}

// registerV3 allocates a msgID unique among the v3 in-flight ids and
// installs a v3 waiter. It mirrors [reactor.register] (capacity + closed
// guards, wrap-collision loop) but on the msgID-keyed registry.
func (r *reactor) registerV3(expectRID int32, level SecurityLevel, isDiscovery bool) (int32, *v3Waiter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, nil, ErrSessionClosed
	}
	if len(r.v3inflight) >= r.maxInFlight {
		return 0, nil, errAtCapacity
	}
	for {
		id := nextMsgIDValue()
		if _, exists := r.v3inflight[id]; exists {
			continue
		}
		w := &v3Waiter{ch: make(chan *v3Result, 1), expectRID: expectRID, level: level, isDiscovery: isDiscovery}
		r.v3inflight[id] = w
		return id, w, nil
	}
}

// deregisterV3 removes a v3 msgID from the registry (idempotent).
func (r *reactor) deregisterV3(id int32) {
	r.mu.Lock()
	delete(r.v3inflight, id)
	r.mu.Unlock()
}

// peekV3 returns the v3 waiter for id WITHOUT removing it, so the read-loop
// can run the crypto gate against the waiter's immutable fields before
// committing. The final consume happens in [reactor.deliverV3] under the
// lock, closing the timeout-race TOCTOU.
func (r *reactor) peekV3(id int32) *v3Waiter {
	r.mu.Lock()
	w := r.v3inflight[id]
	r.mu.Unlock()
	return w
}

// deliverV3 routes a verified result to its waiter and removes the id, the
// same single-use discipline as [reactor.deliver]. It re-checks the entry
// under the lock so a waiter that already timed out is not delivered to.
func (r *reactor) deliverV3(id int32, res *v3Result) bool {
	r.mu.Lock()
	w, ok := r.v3inflight[id]
	if ok {
		delete(r.v3inflight, id)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	w.ch <- res
	return true
}

// handleInboundV3 decodes a v3 datagram and runs the read-loop USM gate:
// every identity/integrity check happens before the waiter is
// consumed, and every reject is a drop-and-count, never a panic. The
// expensive crypto runs outside r.mu (peekV3 + immutable waiter fields).
func (r *reactor) handleInboundV3(buf []byte, src *net.UDPAddr) {
	_, dec, err := decodeAnyMessage(buf)
	if err != nil || dec == nil {
		r.dropped.Add(1)
		return
	}
	w := r.peekV3(dec.msg.msgID)
	if w == nil {
		r.dropped.Add(1) // unknown / late / duplicate msgID
		return
	}

	if w.isDiscovery {
		r.handleDiscoveryReply(dec, src)
		return
	}

	// A secure session legitimately receives only authenticated replies. The
	// one exception is an unauthenticated unknownEngineID Report from the
	// dialed peer — the "engine restarted with a new engineID" signal that
	// drives one re-discovery. It is bounded to the peer address
	// (like the discovery carve-out) so an off-path sender cannot force a
	// re-discovery.
	if r.usm.level >= SecurityLevelAuthNoPriv && !dec.msg.flags.auth {
		if sameUDPAddr(src, r.peer) && dec.msg.scoped != nil {
			if o, isR := classifyReport(&dec.msg.scoped.pdu); isR && o == reportUnknownEngineID {
				res := &v3Result{
					isReport:       true,
					report:         o,
					reportEngineID: dec.msg.sec.engineID,
					reportBoots:    dec.msg.sec.engineBoots,
					reportTime:     dec.msg.sec.engineTime,
				}
				if !r.deliverV3(dec.msg.msgID, res) {
					r.dropped.Add(1)
				}
				return
			}
		}
		r.dropped.Add(1)
		return
	}

	sp, isReport, gerr := r.usm.inboundGate(dec)
	if gerr != nil {
		r.dropped.Add(1)
		return
	}
	res := &v3Result{}
	if isReport {
		res.isReport = true
		res.report, _ = classifyReport(&sp.pdu)
		res.reportEngineID = dec.msg.sec.engineID
		res.reportBoots = dec.msg.sec.engineBoots
		res.reportTime = dec.msg.sec.engineTime
	} else {
		// Inner request-id check (defense-in-depth): a forged
		// packet on a guessed msgID that somehow passed HMAC must still match
		// the request-id sealed inside the request.
		if sp.pdu.requestID != w.expectRID {
			r.dropped.Add(1)
			return
		}
		res.scoped = sp
	}
	if !r.deliverV3(dec.msg.msgID, res) {
		r.dropped.Add(1)
	}
}

// handleDiscoveryReply applies the discovery carve-out: an unauthenticated
// engine-discovery Report is accepted ONLY against a live outstanding
// discovery msgID (already matched by the caller), ONLY from the session's
// dialed peer (unconditional, regardless of validateSrc), and ONLY when it
// is an unauthenticated Report. Anything else is dropped.
func (r *reactor) handleDiscoveryReply(dec *v3Decoded, src *net.UDPAddr) {
	if !sameUDPAddr(src, r.peer) {
		r.dropped.Add(1)
		return
	}
	m := dec.msg
	if m.flags.auth || m.scoped == nil {
		r.dropped.Add(1)
		return
	}
	outcome, isReport := classifyReport(&m.scoped.pdu)
	if !isReport {
		r.dropped.Add(1)
		return
	}
	res := &v3Result{
		isReport:       true,
		report:         outcome,
		reportEngineID: m.sec.engineID,
		reportBoots:    m.sec.engineBoots,
		reportTime:     m.sec.engineTime,
	}
	if !r.deliverV3(m.msgID, res) {
		r.dropped.Add(1)
	}
}

// roundTrip sends req and waits for the matching reply, retransmitting
// with a fresh request-id on each per-PDU timeout up to retries. timeout
// and retries are passed per call so a [CallOption] can override the
// session defaults. The loop is guarded by ctx: the context error
// is checked before every attempt and returned unwrapped on
// cancellation/deadline. req.pdu's request-id is overwritten on each
// attempt.
func (r *reactor) roundTrip(ctx context.Context, req *message, timeout time.Duration, retries int) (*message, error) {
	attempts := retries + 1
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 && r.inst != nil {
			r.inst.recordRetry(ctx)
			trace.SpanFromContext(ctx).SetAttributes(attribute.Int(attrRetryCount, attempt))
		}
		resp, retry, err := r.singleAttempt(ctx, req, timeout)
		if err == nil {
			return resp, nil
		}
		if !retry {
			return nil, err
		}
	}
	return nil, errTimeout
}

// send writes one datagram to the peer. On the connected default it is a
// plain Write (the kernel already holds the destination); on the unconnected
// multi-homed path it is a WriteToUDP to the dialed peer. WriteToUDP on a
// connected socket is an error in the net package, so the two paths must not
// be mixed — r.connected is the single source of truth.
func (r *reactor) send(datagram []byte) (int, error) {
	if r.connected {
		return r.conn.Write(datagram)
	}
	return r.conn.WriteToUDP(datagram, r.peer)
}

// singleAttempt performs one request/response exchange. The boolean
// reports whether the error is retryable (a per-PDU timeout); every other
// error — context cancellation, close, capacity, write failure — is
// terminal. The request-id is deregistered on every exit path.
func (r *reactor) singleAttempt(ctx context.Context, req *message, timeout time.Duration) (resp *message, retry bool, err error) {
	id, w, err := r.register(req.wantRaw)
	if err != nil {
		return nil, false, err
	}
	defer r.deregister(id)

	req.pdu.requestID = id
	datagram, err := encodeMessage(req)
	if err != nil {
		return nil, false, errs.Wrap(err, "snmp: encode request")
	}
	if _, err := r.send(datagram); err != nil {
		if r.isClosed() {
			return nil, false, ErrSessionClosed
		}
		return nil, false, errs.Wrap(err, "snmp: write request")
	}
	if r.inst != nil {
		r.inst.recordPDUSize(ctx, "tx", len(datagram))
		trace.SpanFromContext(ctx).SetAttributes(attribute.Int(attrRequestID, int(id)))
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case m := <-w.ch:
		return m, false, nil
	case <-timer.C:
		return nil, true, errTimeout
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-r.closeCh:
		return nil, false, ErrSessionClosed
	}
}

// v3Attempts is the v3 timeout-retransmit loop, the analog of
// [reactor.roundTrip]'s inner loop. Each attempt allocates a fresh msgID and
// inner request-id. It is wrapped by [reactor.v3RoundTrip], which layers the
// discovery and §3.2 resync budgets around it.
func (r *reactor) v3Attempts(ctx context.Context, p pdu, boots, etime int32, timeout time.Duration, retries int) (*v3Result, error) {
	attempts := retries + 1
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 && r.inst != nil {
			r.inst.recordRetry(ctx)
			trace.SpanFromContext(ctx).SetAttributes(attribute.Int(attrRetryCount, attempt))
		}
		res, retry, err := r.v3SingleAttempt(ctx, p, boots, etime, timeout)
		if err == nil {
			return res, nil
		}
		if !retry {
			return nil, err
		}
	}
	return nil, errTimeout
}

// v3SingleAttempt performs one authenticated v3 exchange: allocate a fresh
// inner request-id and msgID, build the authenticated/encrypted datagram via
// the USM processor, send, and wait for the gated reply by msgID. The
// boolean reports a retryable per-PDU timeout.
func (r *reactor) v3SingleAttempt(ctx context.Context, p pdu, boots, etime int32, timeout time.Duration) (*v3Result, bool, error) {
	rid := nextRequestIDValue()
	p.requestID = rid
	msgID, w, err := r.registerV3(rid, r.usm.level, false)
	if err != nil {
		return nil, false, err
	}
	defer r.deregisterV3(msgID)

	datagram, err := r.usm.buildOutbound(msgID, p, boots, etime, true)
	if err != nil {
		return nil, false, errs.Wrap(err, "snmp: encode v3 request")
	}
	if r.inst != nil {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.Int(attrRequestID, int(rid)), attribute.Int(attrMsgID, int(msgID)))
		if eid := r.usm.currentEngineID(); len(eid) != 0 {
			span.SetAttributes(attribute.String(attrEngineID, hexString(eid)))
		}
	}
	return r.sendWaitV3(ctx, datagram, w, timeout)
}

// sendWaitV3 writes datagram and waits on the waiter's channel, mirroring
// the select in [reactor.singleAttempt]. A per-PDU timeout is retryable; a
// context/close error is terminal.
func (r *reactor) sendWaitV3(ctx context.Context, datagram []byte, w *v3Waiter, timeout time.Duration) (*v3Result, bool, error) {
	if _, err := r.send(datagram); err != nil {
		if r.isClosed() {
			return nil, false, ErrSessionClosed
		}
		return nil, false, errs.Wrap(err, "snmp: write request")
	}
	if r.inst != nil {
		r.inst.recordPDUSize(ctx, "tx", len(datagram))
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-w.ch:
		return res, false, nil
	case <-timer.C:
		return nil, true, errTimeout
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-r.closeCh:
		return nil, false, ErrSessionClosed
	}
}

// readLoop is the single goroutine that reads every datagram, validates
// the source when configured, decodes it, and routes it by
// request-id. Unmatched, malformed, and disallowed-source datagrams are
// dropped and counted. The loop exits when the socket is closed.
func (r *reactor) readLoop() {
	defer close(r.loopDone)
	buf := r.rxBuf
	for {
		n, src, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			// A closed socket is the orchestrated shutdown path. Any other
			// read error means the socket is dead: mark the reactor closed
			// and unblock every parked/future caller with ErrSessionClosed
			// so they fail fast instead of hanging until their timeout.
			if !r.isClosed() {
				r.shutdown()
			}
			return
		}
		if r.validateSrc && !sameUDPAddr(src, r.peer) {
			r.dropped.Add(1)
			as.Logger(r.logCtx).DebugContext(r.logCtx,
				"snmp: dropping reply from unexpected source",
				"expected", r.peer.String(), "got", src.String())
			continue
		}
		if r.inst != nil {
			r.inst.recordPDUSize(r.logCtx, "rx", n)
		}
		// v3 sessions route through the USM gate and demux by msgID. The
		// v1/v2c path below is left exactly as it was (the protected
		// invariant behind the validate-reply-identity-before-demux learning):
		// it never reads the v3 waiter fields.
		if r.usm != nil {
			r.handleInboundV3(buf[:n], src)
			continue
		}
		m, rawVBL, derr := decodeMessageHeader(buf[:n])
		if derr != nil {
			r.dropped.Add(1)
			continue
		}
		// Validate version/community before delivery: a datagram
		// whose version or community does not match the session's expected
		// values is dropped+counted, so a forged reply on a guessed
		// request-id cannot displace the genuine one. The community string
		// is never logged or embedded in an error.
		if m.version != r.version || m.community != r.community {
			r.dropped.Add(1)
			as.Logger(r.logCtx).DebugContext(r.logCtx,
				"snmp: dropping reply with mismatched version/community",
				"expected_version", r.version.String(), "got_version", m.version.String())
			continue
		}
		// Varbind handling splits on the waiter's raw flag. Raw delivery
		// requires the response to pass the mirror validation — anything
		// the eager decode would reject (plus non-canonical name-OID
		// encodings, which would corrupt byte-order walk guards) falls
		// back to the eager path so drop semantics stay identical.
		raw, found := r.rawWanted(m.pdu.requestID)
		if !found {
			// Unknown / late / duplicate id. Counted without paying
			// the varbind decode; deliver below re-checks under the lock.
			r.dropped.Add(1)
			continue
		}
		if m.pdu.trapV1 == nil {
			if raw && validateRawVarBindList(rawVBL, 1) == nil {
				m.pdu.rawVBL = cloneBytes(rawVBL)
			} else {
				vbs, verr := decodeVarBindList(rawVBL, 1)
				if verr != nil {
					r.dropped.Add(1)
					continue
				}
				m.pdu.varbinds = vbs
				m.warnings = scanCounter64Warnings(m.version, vbs)
			}
		}
		if !r.deliver(m.pdu.requestID, m) {
			// Unknown / late / duplicate id.
			r.dropped.Add(1)
			continue
		}
		// Surface tolerated decode warnings (e.g. Counter64-in-v1, RFC 2576 §3):
		// the value is preserved and delivered, but the spec breach is logged
		// rather than silently swallowed (enc-counter64-v1). Logged only for a
		// delivered reply (post-demux) so a flood of unmatched datagrams cannot
		// amplify into unbounded warning logs.
		for _, w := range m.warnings {
			as.Logger(r.logCtx).WarnContext(r.logCtx,
				"snmp: tolerated decode warning", "warning", w.Error())
		}
	}
}

// isClosed reports whether close has been initiated.
func (r *reactor) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// inFlight returns the current number of registered waiters, for the
// in-flight gauge.
func (r *reactor) inFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	// A session uses exactly one registry (v1/v2c -> inflight, v3 -> v3inflight),
	// so the sum is the true concurrency count for either; without the v3 term
	// the in-flight gauge reads zero for every v3 session.
	return len(r.inflight) + len(r.v3inflight)
}

// droppedCount returns the monotonic count of unmatched/malformed/
// disallowed-source datagrams, for the dropped/unmatched metric.
func (r *reactor) droppedCount() uint64 {
	return r.dropped.Load()
}

// shutdown performs the idempotent close-side effects shared by the
// orchestrated [reactor.close] path and the read-loop's unexpected-error
// path: mark the reactor closed, unblock every parked/future caller via
// closeCh ([ErrSessionClosed]), and close the socket. The closeOnce
// guard means the two paths cannot double-close. It does not wait on
// loopDone, so it is safe to call from the read-loop goroutine itself.
func (r *reactor) shutdown() {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		close(r.closeCh)
		r.closeErr = r.conn.Close()
	})
}

// close shuts down the reactor (via [reactor.shutdown]) and waits for the
// read-loop to exit so no goroutine leaks. Idempotent.
func (r *reactor) close() error {
	r.shutdown()
	<-r.loopDone
	return r.closeErr
}

// sameUDPAddr reports whether two UDP addresses have the same IP and port,
// comparing IPs in their canonical form so a v4 and v4-in-v6 spelling of
// the same address match.
func sameUDPAddr(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.IP.Equal(b.IP) && a.Port == b.Port
}
