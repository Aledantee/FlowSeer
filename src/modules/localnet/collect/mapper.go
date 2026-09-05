// Package collect runs one SNMP collection cycle against a device: it
// resolves the device's identity, decides which mappers apply, walks every
// table those mappers read exactly once, and hands the rows to the mappers
// as an immutable [Snapshot].
//
// A mapper is pure. It declares what it reads through a [Spec] and maps a
// Snapshot to its output; it never touches the session. That is what lets
// two mappers share a table walk, and what keeps a mapper testable from a
// handful of rows.
//
// # Detection
//
// A mapper applies to a device when every table in [Spec.Required] answers
// [snmp.TableDescriptor.Present] and, when the spec names sysObjectID
// prefixes, one of them is a prefix of the device's sysObjectID. Detection
// never reads sysDescr: its text is free-form and differs between firmware
// releases of the same model, so a match on it is a guess dressed up as a
// fact.
//
// Present reports instances, not support. A table the agent implements but
// currently holds no rows in reads as absent, so a mapper whose required
// table is empty on this cycle is skipped, and picked up again on a later
// cycle when rows appear.
//
// # Failure isolation
//
// A table walk that fails is recorded against that table alone; the other
// tables of the cycle are still walked and every applicable mapper still
// runs. The mapper decides what the failure costs: a required table's
// failure declines its whole output, an optional table's failure degrades
// it. The walker's own semantics are unchanged underneath: rows a failed
// walk delivered before it stopped are in the snapshot beside the error.
package collect

import (
	"context"
	"errors"
	"iter"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

var (
	// ErrCodePresence identifies a presence probe that failed for a
	// required table, which leaves the mapper's applicability unknown for
	// the cycle.
	ErrCodePresence = errs.NewCode("collect/presence")
	// ErrCodeNotRead identifies a table or scalar a mapper asked the
	// snapshot for without having declared it, so it was never read.
	ErrCodeNotRead = errs.NewCode("collect/not-read")
)

// Row is what every generated table row provides: whether its instance
// suffix decoded as the INDEX the MIB declares.
type Row interface {
	KeyValid() bool
}

// Walker is the part of a generated table walker the collector drives.
// Every generated walker satisfies it.
type Walker[R Row] interface {
	Iter() iter.Seq2[snmp.OID, R]
	Err() error
}

// TableRead is one table read a mapper declares, seen without its row
// type so a [Spec] can list tables of different row types together.
// Only [Table] implements it.
type TableRead interface {
	// Descriptor identifies the table; the collector keys walks on its Root.
	Descriptor() snmp.TableDescriptor
	// Columns are the columns the mapper reads. The collector unions the
	// columns of every mapper that declares the same table.
	Columns() []snmp.AnyColumn

	walk(ctx context.Context, sess snmp.Session, cols []snmp.AnyColumn) (any, error)
}

// Table is a table read with typed access to its rows. Build it with
// [NewTable]; the zero value is not usable.
type Table[R Row] struct {
	desc snmp.TableDescriptor
	cols []snmp.AnyColumn
	run  func(ctx context.Context, sess snmp.Session, cols []snmp.AnyColumn) ([]R, error)
}

// NewTable declares a table read through its generated walker. The row
// type R is stated explicitly because a walker's method set does not let
// the compiler infer it; W is inferred from walk:
//
//	collect.NewTable[ifmib.IfTableRow](ifmib.IfTable.Descriptor(), ifmib.IfTable.Walk, ifmib.IfDescr, ifmib.IfType)
//
// Rows whose key did not decode are dropped during the walk: they name
// nothing the model can hold, and every mapper skipped them anyway.
func NewTable[R Row, W Walker[R]](
	desc snmp.TableDescriptor,
	walk func(context.Context, snmp.Session, ...snmp.AnyColumn) W,
	cols ...snmp.AnyColumn,
) Table[R] {
	return Table[R]{
		desc: desc,
		cols: cols,
		run: func(ctx context.Context, sess snmp.Session, cols []snmp.AnyColumn) ([]R, error) {
			var rows []R

			w := walk(ctx, sess, cols...)
			for _, row := range w.Iter() {
				if row.KeyValid() {
					rows = append(rows, row)
				}
			}

			return rows, w.Err()
		},
	}
}

// Descriptor returns the table's descriptor.
func (t Table[R]) Descriptor() snmp.TableDescriptor { return t.desc }

// Columns returns the columns this declaration reads.
func (t Table[R]) Columns() []snmp.AnyColumn { return t.cols }

func (t Table[R]) walk(ctx context.Context, sess snmp.Session, cols []snmp.AnyColumn) (any, error) {
	return t.run(ctx, sess, cols)
}

// Rows returns the rows the cycle walked for this table, or nil when the
// cycle never walked it or it delivered no row. A walk that failed
// partway still contributes the rows it delivered; check [Table.Err] to
// learn whether the set is complete.
func (t Table[R]) Rows(snap *Snapshot) []R {
	w, ok := snap.tables[t.desc.Root.WireKey()]
	if !ok {
		return nil
	}

	rows, _ := w.rows.([]R)

	return rows
}

// Err returns the error the walk of this table ended with, nil when the
// walk completed, and an error carrying [ErrCodeNotRead] when the cycle
// never walked the table.
func (t Table[R]) Err(snap *Snapshot) error {
	w, ok := snap.tables[t.desc.Root.WireKey()]
	if !ok {
		return errs.New().Code(ErrCodeNotRead).Attr("table", t.desc.Root).Msg("table not read")
	}

	return w.err
}

// ScalarRead is one scalar read a mapper declares, seen without its value
// type. Only [Scalar] implements it.
type ScalarRead interface {
	// Name identifies the scalar within a cycle and in diagnostics. Two
	// declarations with the same name are one read.
	Name() string

	read(ctx context.Context, sess snmp.Session) (any, error)
}

// Scalar is a scalar read with typed access to its value, fetched once per
// cycle through its generated getter. Build it with [NewScalar]; the zero
// value is not usable.
//
// Scalars are fetched one request each rather than batched. An SNMPv1
// agent answers a Get that names one object it does not implement with
// noSuchName for the whole PDU, so a batch would lose every scalar beside
// the unsupported one; a request per scalar keeps each failure to itself.
type Scalar[T any] struct {
	name string
	get  func(context.Context, snmp.Session) (T, error)
}

// NewScalar declares a scalar read through its generated getter:
//
//	collect.NewScalar("lldpLocSysName", lldpmib.LldpLocSysNameGet)
func NewScalar[T any](name string, get func(context.Context, snmp.Session) (T, error)) Scalar[T] {
	return Scalar[T]{name: name, get: get}
}

// Name returns the scalar's name.
func (s Scalar[T]) Name() string { return s.name }

func (s Scalar[T]) read(ctx context.Context, sess snmp.Session) (any, error) {
	return s.get(ctx, sess)
}

// Value returns the value the cycle fetched for this scalar, or the error
// its getter returned. A scalar the cycle never fetched returns an error
// carrying [ErrCodeNotRead].
func (s Scalar[T]) Value(snap *Snapshot) (T, error) {
	var zero T

	f, ok := snap.scalars[s.name]
	if !ok {
		return zero, errs.New().Code(ErrCodeNotRead).Attr("scalar", s.name).Msg("scalar not read")
	}

	if f.err != nil {
		return zero, f.err
	}

	v, _ := f.value.(T)

	return v, nil
}

// Spec is what a mapper declares about itself. The collector reads it once
// per cycle; a mapper must return the same Spec every time.
type Spec struct {
	// Name identifies the mapper in results and diagnostics. Must be unique
	// among the mappers of one collector.
	Name string
	// Required are the tables the mapper cannot map without. All must be
	// present for the mapper to apply.
	Required []TableRead
	// Optional are tables that enrich the mapper's output. Their absence or
	// failure degrades the output rather than declining it.
	Optional []TableRead
	// Scalars are the scalar objects the mapper reads.
	Scalars []ScalarRead
	// SysObjectIDPrefixes restricts the mapper to devices whose sysObjectID
	// has one of them as a prefix. Empty means any device, which is what a
	// mapper of a standard MIB declares.
	SysObjectIDPrefixes []snmp.OID
}

// Mapper turns a snapshot into a mapped output. Map must not touch the
// session or any state outside the snapshot; it may return a partial
// output beside its error. Implementations must be safe for concurrent
// use.
type Mapper interface {
	Spec() Spec
	Map(snap *Snapshot) (any, error)
}

// Applies reports whether spec applies to the device behind sess, whose
// sysObjectID is sysObjectID (the zero OID when it could not be read). A
// spec with prefixes never applies to an unknown sysObjectID. A presence
// probe that fails is returned as an error carrying [ErrCodePresence], and
// the spec does not apply for this cycle.
func Applies(ctx context.Context, sess snmp.Session, spec Spec, sysObjectID snmp.OID) (bool, error) {
	if len(spec.SysObjectIDPrefixes) > 0 && !hasAnyPrefix(sysObjectID, spec.SysObjectIDPrefixes) {
		return false, nil
	}

	for _, t := range spec.Required {
		present, err := t.Descriptor().Present(ctx, sess)
		if err != nil {
			return false, errs.From(err).
				Code(ErrCodePresence).
				Attr("mapper", spec.Name).
				Attr("table", t.Descriptor().Root).
				Msg("probe required table")
		}

		if !present {
			return false, nil
		}
	}

	return true, nil
}

func hasAnyPrefix(oid snmp.OID, prefixes []snmp.OID) bool {
	if oid.Len() == 0 {
		return false
	}

	for _, p := range prefixes {
		if oid.HasPrefix(p) {
			return true
		}
	}

	return false
}

// detect filters mappers down to those that apply, joining the probe
// errors of the ones whose applicability could not be decided.
func detect(ctx context.Context, sess snmp.Session, mappers []Mapper, sysObjectID snmp.OID) ([]Mapper, error) {
	var (
		applicable []Mapper
		errList    []error
	)

	for _, m := range mappers {
		ok, err := Applies(ctx, sess, m.Spec(), sysObjectID)
		if err != nil {
			errList = append(errList, err)

			continue
		}

		if ok {
			applicable = append(applicable, m)
		}
	}

	return applicable, errors.Join(errList...)
}
