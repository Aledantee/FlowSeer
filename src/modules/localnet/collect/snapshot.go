package collect

import (
	"context"

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
// seen, whose walk is used, and the union of the columns asked for.
type pendingTable struct {
	read TableRead
	cols []snmp.AnyColumn
}

// Read walks every table and fetches every scalar the mappers declare,
// without detection: each table once with the union of the columns the
// mappers ask of it, in declaration order with required tables first.
// Errors are recorded per table and per scalar in the snapshot rather than
// returned, so that a mapper can weigh them.
//
// Read is the primitive under [Collector.Collect] and the way to map a
// device whose tables are already known to be there.
func Read(ctx context.Context, sess snmp.Session, mappers ...Mapper) *Snapshot {
	var (
		order   []string
		tables  = make(map[string]*pendingTable)
		scalars []ScalarRead
		seen    = make(map[string]bool)
	)

	for _, m := range mappers {
		spec := m.Spec()

		for _, t := range append(append([]TableRead(nil), spec.Required...), spec.Optional...) {
			key := t.Descriptor().Root.WireKey()

			p, ok := tables[key]
			if !ok {
				order = append(order, key)
				tables[key] = &pendingTable{read: t, cols: t.Columns()}

				continue
			}

			p.cols = unionColumns(p.cols, t.Columns())
		}

		for _, sc := range spec.Scalars {
			if !seen[sc.Name()] {
				seen[sc.Name()] = true
				scalars = append(scalars, sc)
			}
		}
	}

	snap := &Snapshot{
		tables:  make(map[string]walked, len(order)),
		scalars: make(map[string]fetched, len(scalars)),
	}

	for _, key := range order {
		p := tables[key]
		rows, err := p.read.walk(ctx, sess, p.cols)
		snap.tables[key] = walked{rows: rows, err: err}
	}

	for _, sc := range scalars {
		value, err := sc.read(ctx, sess)
		snap.scalars[sc.Name()] = fetched{value: value, err: err}
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
