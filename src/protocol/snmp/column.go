package snmp

import "iter"

// AnyColumn is the type-erased view of a [Column] that table-level
// variadic Walk signatures accept. The interface is exported so generated
// MIB packages and third-party code can declare it; the unexported
// sealedColumn method keeps the concrete implementation set closed to
// [Column].
type AnyColumn interface {
	// OID returns the column's object identifier (the table's column OID,
	// not any instance OID).
	OID() OID
	// Key returns the column OID's [OID.WireKey], precomputed at
	// construction so per-lookup callers pay no formatting allocation.
	// Generated dispatch and tier maps are keyed on it.
	Key() string
	// Kind returns the column's wire-level [Kind].
	Kind() Kind
	sealedColumn()
}

// Column binds a column's OID and wire [Kind] to a decoder that produces
// a typed Go value from a [VarBind]. Construct via [NewColumn]; fields
// are unexported so that generated code routes through the constructor
// and so that the type's invariants stay encapsulated.
//
// Pre-Go-1.27 generic methods, this exported-struct + exported-constructor
// shape is the cleanest way to combine "type-safe per-column decoders"
// with "variadic table Walk that accepts heterogeneous columns".
type Column[T any] struct {
	oid    OID
	key    string
	kind   Kind
	decode func(VarBind) (T, error)
}

// NewColumn constructs a [Column] bound to the given OID, wire Kind, and
// decoder function. It exists as an exported constructor (rather than a
// composite literal) so that callers in foreign packages — including
// mibgen-generated code — can build columns without reaching into the
// type's unexported fields.
func NewColumn[T any](oid OID, kind Kind, decode func(VarBind) (T, error)) Column[T] {
	return Column[T]{oid: oid, key: oid.WireKey(), kind: kind, decode: decode}
}

// OID returns the column's OID.
func (c Column[T]) OID() OID { return c.oid }

// Key returns the column OID's precomputed [OID.WireKey].
func (c Column[T]) Key() string { return c.key }

// Kind returns the column's wire [Kind].
func (c Column[T]) Kind() Kind { return c.kind }

func (c Column[T]) sealedColumn() {}

// Decode applies the column's decoder to vb. It is exported so generated
// code that receives a VarBind from a [Walker] can resolve it to a typed
// value via the same column value it passed to Walk.
func (c Column[T]) Decode(vb VarBind) (T, error) {
	if c.decode == nil {
		var zero T
		return zero, ErrTypeMismatch
	}
	return c.decode(vb)
}

// BindRow lifts a core [Walker] yielding (OID, VarBind) pairs into a
// table-row iterator: for each VarBind drawn from the walker, bind fills
// the caller-supplied row struct and the returned [iter.Seq2] yields
// (index, row).
//
// BindRow ships the simplest correct semantics: bind is invoked once per
// VarBind drawn from the Walker and a (idx, row) pair is yielded each
// call, with row reset to its zero value between yields. Generated
// table walkers build the richer "group VarBinds by row index
// across multiple columns" semantics on top of this primitive rather
// than rebuilding the channel-pump from scratch.
//
// If bind returns an error, BindRow records it on the Walker via
// [Walker.Fail] so that [Walker.Err] surfaces the cause to the caller,
// and BindRow stops yielding. The pump is canceled in the same step so
// it does not leak a goroutine.
//
// A nil Walker yields zero items. The caller should always check
// [Walker.Err] after the loop.
func BindRow[R any](w *Walker, bind func(idx OID, vb VarBind, row *R) error) iter.Seq2[OID, R] {
	return func(yield func(OID, R) bool) {
		if w == nil {
			return
		}
		var row R
		for idx, vb := range w.Iter() {
			if err := bind(idx, vb, &row); err != nil {
				// Surface the bind failure on the Walker so callers
				// that check Err() after the loop see it. Fail also
				// closes the data channel under the sendMu write
				// lock; the pump (still ranging) will observe stop
				// on its next Send and exit.
				w.Fail(err)
				return
			}
			if !yield(idx, row) {
				// The outer caller broke out. Returning here causes
				// Iter (which is feeding us) to see its own yield
				// return false on its next iteration; Iter then
				// signals stop and drains. Calling signalStop here
				// makes the intent explicit and shortens the wakeup
				// path by one channel send.
				w.pump.SignalStop()
				return
			}
			var zero R
			row = zero
		}
	}
}
