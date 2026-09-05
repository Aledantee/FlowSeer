package collect

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// Snapshot holds everything one collection cycle read from a device: rows
// per table, keyed by the table's root OID, and one value per scalar.
// Mappers reach it through [Table.Rows], [Table.Err], and [Scalar.Value].
// A snapshot is built by [Read] and never modified afterwards, so it is
// safe for concurrent reads. Rows may share byte slices with the messages
// mappers build from them; callers must synchronize mutations.
type Snapshot struct {
	tables  map[string]walked
	scalars map[string]fetched
}

// walked is the outcome of one table walk: the rows it delivered (a []R
// boxed as any) and the error it ended with, if any.
type walked struct {
	rows any
	err  error
}

type fetched struct {
	value any
	err   error
}

// pendingTable is one table of a cycle's read plan: the first declaration
// seen, whose walk is used, the union of the columns asked for, and the
// conflict recorded when a later declaration named a different row type.
type pendingTable struct {
	read     TableRead
	cols     []snmp.AnyColumn
	conflict error
}

// pendingScalar is one scalar of a cycle's read plan: the first
// declaration seen and the conflict recorded when a later declaration
// named a different value type.
type pendingScalar struct {
	read     ScalarRead
	conflict error
}

// Read walks every table and fetches every scalar the mappers declare,
// without detection: each table once with the union of the columns the
// mappers ask of it, in the order the mappers declare them. Errors are
// recorded per table and per scalar in the snapshot rather than returned,
// so that a mapper can weigh them.
//
// Two mappers may declare one table root only under the same row type,
// and one scalar name only under the same value type. A clash is recorded
// against that key as an error carrying [ErrCodeConflict] and the key is
// not read, so both mappers see the mistake instead of one of them seeing
// an empty table.
//
// Read is the primitive under [Collector.Collect] and the way to map a
// device whose tables are already known to be there.
func Read(ctx context.Context, sess snmp.Session, mappers ...Mapper) *Snapshot {
	var (
		tableOrder  []string
		tables      = make(map[string]*pendingTable)
		scalarOrder []string
		scalars     = make(map[string]*pendingScalar)
	)

	for _, m := range mappers {
		spec := m.Spec()

		for _, t := range append(append([]TableRead(nil), spec.Required...), spec.Optional...) {
			key := t.Descriptor().Root.WireKey()

			p, ok := tables[key]
			if !ok {
				tableOrder = append(tableOrder, key)
				tables[key] = &pendingTable{read: t, cols: t.Columns()}

				continue
			}

			if p.conflict == nil && p.read.rowType() != t.rowType() {
				p.conflict = errs.New().Code(ErrCodeConflict).
					Attr("table", t.Descriptor().Root).
					Attr("row_type", p.read.rowType().String()).
					Attr("other_row_type", t.rowType().String()).
					Msg("table declared under two row types")
			}

			p.cols = unionColumns(p.cols, t.Columns())
		}

		for _, sc := range spec.Scalars {
			p, ok := scalars[sc.Name()]
			if !ok {
				scalarOrder = append(scalarOrder, sc.Name())
				scalars[sc.Name()] = &pendingScalar{read: sc}

				continue
			}

			if p.conflict == nil && p.read.valueType() != sc.valueType() {
				p.conflict = errs.New().Code(ErrCodeConflict).
					Attr("scalar", sc.Name()).
					Attr("value_type", p.read.valueType().String()).
					Attr("other_value_type", sc.valueType().String()).
					Msg("scalar declared under two value types")
			}
		}
	}

	snap := &Snapshot{
		tables:  make(map[string]walked, len(tableOrder)),
		scalars: make(map[string]fetched, len(scalarOrder)),
	}

	for _, key := range tableOrder {
		p := tables[key]
		if p.conflict != nil {
			snap.tables[key] = walked{err: p.conflict}

			continue
		}

		rows, err := p.read.walk(ctx, sess, p.cols)
		snap.tables[key] = walked{rows: rows, err: err}
	}

	for _, name := range scalarOrder {
		p := scalars[name]
		if p.conflict != nil {
			snap.scalars[name] = fetched{err: p.conflict}

			continue
		}

		value, err := p.read.read(ctx, sess)
		snap.scalars[name] = fetched{value: value, err: err}
	}

	return snap
}

// unionColumns appends the columns of more that base does not already
// hold, keeping base's order so a shared walk requests columns in a
// stable order.
func unionColumns(base, more []snmp.AnyColumn) []snmp.AnyColumn {
	have := make(map[string]bool, len(base))
	for _, c := range base {
		have[c.Key()] = true
	}

	out := append([]snmp.AnyColumn(nil), base...)

	for _, c := range more {
		if !have[c.Key()] {
			have[c.Key()] = true
			out = append(out, c)
		}
	}

	return out
}
