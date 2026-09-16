package snmp

import (
	"context"
	"iter"
	"math"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/pump"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// rawwalk.go — the raw varbind fast path consumed by mibgen-generated
// table walkers. A [RawVarBind] carries the undecoded BER name and
// value of one response varbind; generated code matches columns by byte
// prefix and decodes known-Kind values straight to typed Go values,
// skipping the per-varbind OID materialization and interface boxing of
// the generic [VarBind] path. Every fused primitive is a guarded fast
// path: anything unusual — an off-spec wire tag, an out-of-range value,
// a pre-decoded varbind — declines (ok=false) so the caller falls back
// to [RawVarBind.Decode] + the generic column decoder, reproducing the
// tolerant coercion semantics of decode.go exactly.

// RawVarBind is one undecoded response varbind yielded by a
// [RawWalker].
//
// OID holds the BER content octets of the name (no tag/length header);
// Tag and Value hold the value TLV's identifier octet and content.
// The slices alias the response buffer owned by the walk — they remain
// valid for the lifetime of the iteration step and are safe to retain
// (retention pins the response buffer; copy when holding many).
//
// When VB is non-nil the varbind was already decoded upstream — a v3
// (USM) session, or a response whose wire form could not take the raw
// path (e.g. non-canonical OID arc encoding). Tag and Value are then
// zero and consumers must use VB via [RawVarBind.Decode]; the fused
// primitives decline automatically.
type RawVarBind struct {
	OID   []byte
	Tag   byte
	Value []byte
	VB    VarBind
}

// Decode returns the fully-decoded [VarBind] for rv: VB when the
// varbind was pre-decoded, otherwise the same decode the generic walk
// path would have produced.
func (rv RawVarBind) Decode() (VarBind, error) {
	if rv.VB != nil {
		return rv.VB, nil
	}
	oid, err := decodeOID(rv.OID)
	if err != nil {
		return nil, err
	}
	return decodeValue(oid, rv.Tag, rv.Value)
}

// exceptionTag classifies rv as one of the SNMPv2 exception markers,
// returning the exception's wire tag or 0. Works for both raw and
// pre-decoded varbinds so the walk guards treat them identically.
func (rv RawVarBind) exceptionTag() byte {
	if rv.VB != nil {
		switch rv.VB.(type) {
		case EndOfMibViewVar:
			return tagEndOfMibView
		case NoSuchObjectVar:
			return tagNoSuchObject
		case NoSuchInstanceVar:
			return tagNoSuchInstance
		}
		return 0
	}
	switch rv.Tag {
	case tagEndOfMibView, tagNoSuchObject, tagNoSuchInstance:
		return rv.Tag
	}
	return 0
}

// Fused raw decoders return (value, true) only when rv carries exactly the expected
// wire tag with a cleanly-decodable in-range value. Every other case —
// off-spec tag, exception, overflow, pre-decoded VB — returns ok=false,
// and the caller must fall back to Decode() + the column's generic
// decoder so error text and coercion semantics stay identical to the
// generic path.

// RawInteger32 decodes an INTEGER value varbind.
func RawInteger32(rv RawVarBind) (int32, bool) {
	if rv.VB != nil || rv.Tag != tagInteger {
		return 0, false
	}
	v, err := decodeSignedInt(rv.Value)
	if err != nil || v < math.MinInt32 || v > math.MaxInt32 {
		return 0, false
	}
	return int32(v), true
}

// RawCounter32 decodes a Counter32 value varbind.
func RawCounter32(rv RawVarBind) (uint32, bool) { return rawUint32(rv, tagCounter32) }

// RawGauge32 decodes a Gauge32/Unsigned32 (APPLICATION 2) value varbind.
func RawGauge32(rv RawVarBind) (uint32, bool) { return rawUint32(rv, tagGauge32) }

// RawUnsigned32 decodes a Unsigned32 (APPLICATION 7, gosnmp-compatible)
// value varbind.
func RawUnsigned32(rv RawVarBind) (uint32, bool) { return rawUint32(rv, tagUinteger32) }

// RawTimeTicks decodes a TimeTicks value varbind, masking to the low 32
// bits exactly as the generic decoder does (enc-timeticks-range).
func RawTimeTicks(rv RawVarBind) (uint32, bool) {
	if rv.VB != nil || rv.Tag != tagTimeTicks {
		return 0, false
	}
	v, err := decodeUnsigned(rv.Value)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// RawCounter64 decodes a Counter64 value varbind.
func RawCounter64(rv RawVarBind) (uint64, bool) {
	if rv.VB != nil || rv.Tag != tagCounter64 {
		return 0, false
	}
	v, err := decodeUnsigned(rv.Value)
	if err != nil {
		return 0, false
	}
	return v, true
}

func rawUint32(rv RawVarBind, tag byte) (uint32, bool) {
	if rv.VB != nil || rv.Tag != tag {
		return 0, false
	}
	v, err := decodeUnsigned(rv.Value)
	if err != nil || v > math.MaxUint32 {
		return 0, false
	}
	return uint32(v), true
}

// RawFirstArc reads the first sub-identifier from a mid-OID byte suffix
// (no X.690 first-octet folding — that applies only at the start of a
// full OID). Returns the arc value, the number of octets consumed, and
// ok=false on a truncated or overflowing arc.
func RawFirstArc(b []byte) (arc uint32, n int, ok bool) {
	v, adv, err := readBase128(b, 0)
	if err != nil {
		return 0, 0, false
	}
	return v, adv, true
}

// DecodeIndexArcs decodes a mid-OID byte suffix (the row-index portion
// of a table instance OID) into an [OID]. Unlike a full OID, an index
// suffix carries no root arcs, so no first-octet folding and no SMIv2
// root-rule validation apply — an ifTable index of 7 decodes to the
// single-arc OID 7, matching the index OIDs the generic table walkers
// construct via [OID.Append].
func DecodeIndexArcs(b []byte) (OID, error) {
	if len(b) == 0 {
		return OID{}, nil
	}
	subs := make([]uint32, 0, len(b))
	for i := 0; i < len(b); {
		v, adv, err := readBase128(b, i)
		if err != nil {
			return OID{}, err
		}
		subs = append(subs, v)
		i += adv
	}
	if len(subs) > maxOIDComponents {
		return OID{}, errs.New().Attr("count", len(subs)).Attr("max", maxOIDComponents).
			Msg("sub-identifier count exceeds SMIv2 maximum")
	}
	return OID{subs: subs}, nil
}

// cmpOIDWire compares two canonical BER OID content encodings in arc
// order without decoding. Plain bytes.Compare is NOT arc order: base-128
// groups of different lengths compare by first octet, so 16383 (0xFF7F)
// would sort above 16384 (0x818000). Instead each arc's encoded group is
// compared length-first (a minimally-encoded longer group is the larger
// value), then bytewise. The folded first octet-group orders correctly
// too: the (arc0, arc1) fold is monotone in lexicographic arc order.
// Correct only for canonical (minimal) encodings — which the read-loop
// mirror validation guarantees on the raw path. Pinned against
// [OID.Compare] by the byte-order property test.
func cmpOIDWire(a, b []byte) int {
	for len(a) > 0 && len(b) > 0 {
		la, lb := base128Len(a), base128Len(b)
		if la != lb {
			if la < lb {
				return -1
			}
			return 1
		}
		for i := 0; i < la; i++ {
			if a[i] != b[i] {
				if a[i] < b[i] {
					return -1
				}
				return 1
			}
		}
		a, b = a[la:], b[lb:]
	}
	switch {
	case len(a) > 0:
		return 1
	case len(b) > 0:
		return -1
	}
	return 0
}

// base128Len returns the octet length of the base-128 group at the
// front of b (1 past the last continuation octet, capped at len(b) for
// a truncated tail — callers only see validated encodings).
func base128Len(b []byte) int {
	for i := 0; i < len(b); i++ {
		if b[i]&0x80 == 0 {
			return i + 1
		}
	}
	return len(b)
}

// RawWalker is the raw-varbind counterpart of [Walker]: the streaming
// result of [Session.BulkWalkRaw]. It shares the channel-pump state
// machine; consumers iterate via [RawWalker.Iter] and must check
// [RawWalker.Err] after the loop, exactly as with [Walker].
type RawWalker struct {
	pump *pump.Pump[RawVarBind]
}

// NewRawWalker constructs a RawWalker tied to ctx with the given buffer
// size (<= 0 defaults to the same row buffer as [NewWalker]). Backends
// populate it via [RawWalker.Pump].
func NewRawWalker(ctx context.Context, bufferSize int) *RawWalker {
	if bufferSize <= 0 {
		bufferSize = defaultRowBuffer
	}
	return &RawWalker{pump: pump.New[RawVarBind](ctx, bufferSize)}
}

// Pump runs fn in a new goroutine with the same contract as
// [Walker.Pump]: fn produces items via Send, calls Fail on terminal
// errors, and returns on natural completion. A panic in fn is recovered by
// [spawn.Go] and reported through [spawn.ReportTo](w.Fail), which records
// the error before it closes the channel. Done is not deferred, for the
// reason [Walker.Pump] gives.
func (w *RawWalker) Pump(fn func(ctx context.Context)) {
	spawn.Go(w.pump.Context(), "RawWalker.Pump", func() {
		fn(w.pump.Context())
		w.Done()
	}, spawn.ReportTo(w.Fail))
}

// Send delivers one raw varbind to the consumer; false means the
// consumer has signaled termination. Same contract as [Walker.Send].
func (w *RawWalker) Send(rv RawVarBind) bool { return w.pump.Send(rv) }

// Fail records err as the terminal error and closes the data channel.
// Same contract as [Walker.Fail].
func (w *RawWalker) Fail(err error) { w.pump.Fail(err) }

// Done signals normal completion. Same contract as [Walker.Done].
func (w *RawWalker) Done() { w.pump.Done() }

// Err returns the first terminal error recorded via [RawWalker.Fail],
// or nil if the walk completed naturally or is still running. Same
// contract as [Walker.Err].
func (w *RawWalker) Err() error { return w.pump.Err() }

// Iter returns the range-over-function form of the raw walk. Breaking
// out of the loop terminates the pump; check [RawWalker.Err] after the
// loop.
func (w *RawWalker) Iter() iter.Seq[RawVarBind] {
	return func(yield func(RawVarBind) bool) {
		for rv := range w.pump.Data() {
			if !yield(rv) {
				w.pump.SignalStop()
				for range w.pump.Data() {
				}
				return
			}
		}
	}
}

// Close signals the pump to terminate early. Same contract as
// [Walker.Close].
func (w *RawWalker) Close() error {
	w.pump.SignalStop()
	w.pump.Cancel()
	return nil
}

// ErrForeignColumn reports a column handed to a generated table's Walk
// that is not a column of that table. Column sub-ids repeat across
// tables, so accepting one would either decode the local column that
// happens to share the foreign column's last sub-id or match nothing at
// all and return an empty table; both are silent. A generated Walk
// therefore refuses the walk outright and surfaces this through Err.
var ErrForeignColumn = errs.Msg("column does not belong to this table")

// ForeignColumnWalk builds the failed [RawWalker] a generated Walk
// returns when it is handed a foreign column: no request reaches the
// device, the iterator yields nothing, and Err wraps
// [ErrForeignColumn] naming the table and the offending column.
// Generated code calls this; there is no reason to call it by hand.
func ForeignColumnWalk(ctx context.Context, table string, col AnyColumn) *RawWalker {
	rw := NewRawWalker(ctx, 0)
	rw.Fail(errs.From(ErrForeignColumn).
		Attr("table", table).
		Attr("column", col.OID().String()).
		Msgf("%s.Walk: column %s is not a column of %s", table, col.OID(), table))
	return rw
}

// RawWalkerFromWalker adapts a [Walker] into a [RawWalker]: each
// (OID, VarBind) pair is yielded as a pre-decoded [RawVarBind] (VB set,
// OID re-encoded to canonical wire octets), and the source walker's
// terminal error propagates. It lets [Session] implementations that
// cannot produce wire bytes — fakes, recorders, middleware — satisfy
// BulkWalkRaw by delegating to their BulkWalk; consumers then take
// their generic fallback arm for every varbind.
func RawWalkerFromWalker(ctx context.Context, w *Walker) *RawWalker {
	rw := NewRawWalker(ctx, 0)
	rw.Pump(func(context.Context) {
		for idx, vb := range w.Iter() {
			if !rw.Send(RawVarBind{OID: encodeOIDContent(idx), VB: vb}) {
				_ = w.Close()
				return
			}
		}
		if err := w.Err(); err != nil {
			rw.Fail(err)
		}
	})
	return rw
}
