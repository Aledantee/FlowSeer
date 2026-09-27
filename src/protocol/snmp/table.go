package snmp

import (
	"context"
	"iter"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TableDescriptor identifies one MIB table without reference to its row
// or walker types, so a consumer can hold tables from many generated
// packages in one slice, probe them, and declare them as dependencies.
// Generated bindings populate it; callers treat it as a value.
type TableDescriptor struct {
	// Root is the table's OID (the node above the entry).
	Root OID
	// Indicator is the change indicator a Watcher would poll for the
	// table, or the zero value when the MIB declares none; see
	// [TableDescriptor.HasIndicator].
	Indicator ChangeIndicator
	// KeyType is the Go type name of the row key in the generated
	// package, for diagnostics and dependency reporting.
	KeyType string
}

// HasIndicator reports whether the table has a change indicator.
func (d TableDescriptor) HasIndicator() bool { return !d.Indicator.isZero() }

// Present issues one GetNext at the table root and reports whether the
// agent answered with an instance under it. This is an instance probe,
// not a support probe: an agent that implements the table but currently
// holds no rows reads as absent, as does one that answers outside the
// root or with an end-of-MIB marker. A transport failure or a reply
// without a VarBind is returned as an error.
func (d TableDescriptor) Present(ctx context.Context, sess Session) (bool, error) {
	vbs, err := sess.GetNext(ctx, []OID{d.Root})
	if err != nil {
		return false, err
	}
	if len(vbs) == 0 || vbs[0] == nil {
		return false, errs.New().Attr("root", d.Root).Msg("table presence probe received no varbind")
	}
	if IsException(vbs[0]) {
		return false, nil
	}
	oid := vbs[0].GetHeader().OID
	return oid.Len() > d.Root.Len() && oid.HasPrefix(d.Root), nil
}

// TableWalker merges selected columns by numeric index suffix into typed rows.
// Construct it through [Table.Walk]; the zero value yields no rows and no error.
// Iteration is single-use and single-consumer; Close and Err are safe
// concurrently with it.
type TableWalker[Row any] struct {
	cw       *ColumnWalker
	ordinals []int
	bindKey  func(OID, *Row)
	decode   func(*Row, int, RawVarBind) error
	row      Row
}

// Err returns the walk's terminal error, or nil when it completed or the
// consumer stopped it. A foreign column, a decode error, or parent cancellation
// during the walk is reported here.
func (w *TableWalker[Row]) Err() error {
	if w == nil || w.cw == nil {
		return nil
	}
	return w.cw.Err()
}

// Close cancels in-flight retrieval and prevents further requests. It is
// idempotent and safe during iteration.
func (w *TableWalker[Row]) Close() {
	if w == nil || w.cw == nil {
		return
	}
	w.cw.Close()
}

// Iter yields complete selected-column rows in numeric OID suffix order
// (192.168.0.2 precedes 192.168.0.10), retaining one batch per selected column.
// Each row is a fresh value: a row kept across iterations is never changed by
// later ones. Breaking iteration stops retrieval. A row whose suffix does not
// decode as the table's INDEX is still yielded, with the raw suffix as its OID.
// A decode error omits the failing row and every later row; rows already
// yielded remain valid. Check [TableWalker.Err] after iteration.
func (w *TableWalker[Row]) Iter() iter.Seq2[OID, Row] {
	return func(yield func(OID, Row) bool) {
		if w == nil || w.cw == nil {
			return
		}
		var zero Row
		for idx, cells := range w.cw.Iter() {
			w.row = zero
			if w.bindKey != nil {
				w.bindKey(idx, &w.row)
			}
			var derr error
			for _, cell := range cells {
				if cell.Column < 0 || cell.Column >= len(w.ordinals) {
					continue
				}
				ordinal := w.ordinals[cell.Column]
				if w.decode != nil {
					if err := w.decode(&w.row, ordinal, cell.Value); err != nil {
						derr = err
						break
					}
				}
			}
			if derr != nil {
				w.cw.Fail(derr)
				return
			}
			if !yield(idx, w.row) {
				return
			}
		}
	}
}

// Table coordinates selected-column validation, deduplication, and execution for
// generated table singletons. The generated table singleton embeds [Table], and
// its walker embeds [TableWalker] by value. A Table is immutable after
// [NewTable] and safe for concurrent use; its zero value walks nothing and
// returns the zero Walk.
type Table[Row any, Walk any] struct {
	name    string
	cols    []AnyColumn
	bindKey func(OID, *Row)
	decode  func(*Row, int, RawVarBind) error
	wrap    func(TableWalker[Row]) Walk
}

// NewTable constructs a [Table] descriptor with callbacks for key binding, cell
// decoding, and named walker wrapping.
func NewTable[Row any, Walk any](
	name string,
	cols []AnyColumn,
	bindKey func(OID, *Row),
	decode func(*Row, int, RawVarBind) error,
	wrap func(TableWalker[Row]) Walk,
) Table[Row, Walk] {
	return Table[Row, Walk]{
		name:    name,
		cols:    cols,
		bindKey: bindKey,
		decode:  decode,
		wrap:    wrap,
	}
}

// Walk lazily retrieves only the selected columns with bounded defaults. Rows
// are the union of selected values in numeric OID index order. No columns means
// no rows or requests, and duplicate selections are ignored. A nil column or a
// column of another table fails before I/O with [ErrForeignColumn], reported by
// the walker's Err.
func (t Table[Row, Walk]) Walk(ctx context.Context, sess Session, cols ...AnyColumn) Walk {
	return t.WalkWithOptions(ctx, sess, TableWalkOptions{}, cols...)
}

// WalkWithOptions is [Table.Walk] with request sizing and per-call controls.
// SNMPv1 is unsupported. Parent cancellation is an error; stopping iteration is
// successful.
func (t Table[Row, Walk]) WalkWithOptions(ctx context.Context, sess Session, options TableWalkOptions, cols ...AnyColumn) Walk {
	if t.wrap == nil {
		var zero Walk
		return zero
	}

	seen := make(map[string]bool)
	var selectedRoots []OID
	var selectedOrdinals []int

	for _, c := range cols {
		if c == nil {
			cw := WalkColumns(ctx, sess, nil, options)
			cw.Fail(errs.Wrapf(ErrForeignColumn, "%s.Walk: nil column", t.name))
			return t.wrap(TableWalker[Row]{cw: cw})
		}
		matchIdx := -1
		for i, tc := range t.cols {
			if tc != nil && tc.Key() == c.Key() {
				matchIdx = i
				break
			}
		}
		if matchIdx < 0 {
			cw := WalkColumns(ctx, sess, nil, options)
			cw.Fail(errs.Wrapf(ErrForeignColumn, "%s.Walk: column %s", t.name, c.OID()))
			return t.wrap(TableWalker[Row]{cw: cw})
		}
		k := c.Key()
		if seen[k] {
			continue
		}
		seen[k] = true
		ordinal := matchIdx
		if tb, ok := t.cols[matchIdx].(tableColBit); ok {
			if bit := tb.observedBit(); bit >= 0 {
				ordinal = bit
			}
		}
		selectedRoots = append(selectedRoots, c.OID())
		selectedOrdinals = append(selectedOrdinals, ordinal)
	}

	cw := WalkColumns(ctx, sess, selectedRoots, options)
	return t.wrap(TableWalker[Row]{
		cw:       cw,
		ordinals: selectedOrdinals,
		bindKey:  t.bindKey,
		decode:   t.decode,
	})
}
