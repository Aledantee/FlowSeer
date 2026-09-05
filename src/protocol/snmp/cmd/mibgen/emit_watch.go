package main

import (
	"sort"
	"strings"

	"github.com/dave/jennifer/jen"
)

// emitWatch writes the per-table Watch-method machinery for a single
// table that has a discovered indicator. When the table has
// no indicator the function is a no-op — indicator discovery has
// already established that no <Table>Indicator var exists, so the wrapper has
// nothing to reference.
//
// The emission produces:
//
//   - decode<TableName>Row(idx OID, vbs []VarBind) (Row, error):
//     package-private decoder shared by the (future) Watch path. The
//     existing IfTableWalker.Iter already inlines this logic; for
//     first ship we emit a parallel package-private helper rather
//     than refactoring the Walker. A future revision can collapse
//     them.
//
//   - equal<TableName>Row(a, b Row) bool:
//     field-by-field equality emitted with the type-appropriate
//     comparator (bytes.Equal for []byte, OID.Equal for OID,
//     time.Time.Equal for time.Time, == for everything else).
//
//   - merge<TableName>Row(dst *Row, vbs []VarBind):
//     best-effort partial-column merge used by [snmp.Watcher] to
//     update a row's state under partial-column fetches (Counter-tier,
//     Static-tier). Fields whose columns are absent from vbs are
//     left unchanged.
//
//   - <TableName>Watcher struct + Iter / Err / Close / Fallback /
//     LastTickErr wrapper methods.
//
//   - func (<table>T) Watch(...) *<TableName>Watcher method.
func emitWatch(f *jen.File, ec *emitCtx, tw tableWalkContext) {
	if !ec.tableHasIndicator(tw.TableOID) {
		return
	}

	decodeFnName := "decode" + tw.TableName + "Row"
	equalFnName := "equal" + tw.TableName + "Row"
	mergeFnName := "merge" + tw.TableName + "Row"
	watcherTypeName := tw.TableName + "Watcher"

	emitWatchDecodeFn(f, ec, tw, decodeFnName)
	emitWatchEqualFn(f, ec, tw, equalFnName)
	emitWatchMergeFn(f, ec, tw, mergeFnName)
	emitWatcherType(f, ec, tw, watcherTypeName)
	emitWatchMethod(f, ec, tw, watcherTypeName, decodeFnName, equalFnName, mergeFnName)
}

// tableWalkContext is the cross-pass handoff for a table being
// emitted. emit_table.go fills this in during column resolution and
// calls emitWatch after the table-level Walker emission completes;
// emit_watch.go consumes it to produce the Watch-side machinery.
type tableWalkContext struct {
	TableName   string // CamelCase, e.g. "IfTable"
	TableOID    string // dotted-decimal table OID, e.g. "1.3.6.1.2.1.2.2"
	EntryPrefix string // dotted-decimal entry OID, e.g. "1.3.6.1.2.1.2.2.1"
	RowTypeName string // generated Row struct name, e.g. "IfTableRow"
	Key         rowKey
	Cols        []watchColInfo
}

// watchColInfo describes one accessible column on a watched table.
// Captured by emit_table.go's column loop and passed through to
// emitWatch.
type watchColInfo struct {
	GoName    string         // e.g. "IfDescr"
	FieldName string         // row-struct field name, e.g. "IfDescr"
	Sub       uint32         // last sub-id of the column OID
	Bit       int            // this column's bit in the row's observed set
	GoType    *jen.Statement // jen.Code for the Go type of this column's field
	Variant   string         // e.g. "OctetStringVar" / "Counter64Var"
}

// tableHasIndicator reports whether the module's discovered indicator
// set contains a binding for the given table OID. Used by emitWatch
// to gate emission per the "no Watch method without an indicator"
// rule.
func (ec *emitCtx) tableHasIndicator(tableOID string) bool {
	_, ok := ec.tableIndicatorsByOID[tableOID]
	return ok
}

// emitWatchDecodeFn renders the decode<TableName>Row helper. The
// function consumes the row index and the VarBinds collected for
// that row (one entry per requested column) and produces a Row
// struct with the matching fields populated. Absent VarBinds leave
// their corresponding field at its zero value — the same shape
// inline-Walker decoding already produces today.
func emitWatchDecodeFn(f *jen.File, _ *emitCtx, tw tableWalkContext, fnName string) {
	f.Comment(fnName + " decodes one row of " + tw.TableName + " from the supplied")
	f.Comment("VarBinds. Each VarBind's OID determines which row field it populates")
	f.Comment("(via the column's last sub-id). VarBinds with unknown column-ids are")
	f.Comment("ignored. Absent columns leave their field at its zero value.")
	f.Func().Id(fnName).Params(
		jen.Id("idx").Qual(snmpImport, "OID"),
		jen.Id("vbs").Index().Qual(snmpImport, "VarBind"),
	).Params(jen.Id(tw.RowTypeName), jen.Error()).BlockFunc(func(g *jen.Group) {
		tw.Key.declareRow(g, tw.RowTypeName)

		sortedCols := make([]watchColInfo, len(tw.Cols))
		copy(sortedCols, tw.Cols)
		sort.Slice(sortedCols, func(i, j int) bool { return sortedCols[i].Sub < sortedCols[j].Sub })

		g.Line()
		g.For(jen.List(jen.Id("_"), jen.Id("vb")).Op(":=").Range().Id("vbs")).BlockFunc(func(lg *jen.Group) {
			lg.Id("o").Op(":=").Id("vb").Dot("GetHeader").Call().Dot("OID")
			lg.If(jen.Id("o").Dot("Len").Call().Op("==").Lit(0)).Block(jen.Continue())
			// The column OID's last sub-id sits two positions before
			// the OID's tail when the OID has shape
			// `<entry>.<col>.<idx...>`. Defensive: require the OID
			// to be at least 2 sub-ids past the entry prefix.
			lg.Id("entryLen").Op(":=").Add(newOIDCall(tw.EntryPrefix)).Dot("Len").Call()
			lg.If(jen.Id("o").Dot("Len").Call().Op("<=").Id("entryLen")).Block(jen.Continue())
			lg.Id("colID").Op(":=").Id("o").Dot("At").Call(jen.Id("entryLen"))
			lg.Switch(jen.Id("colID")).BlockFunc(func(sg *jen.Group) {
				for _, c := range sortedCols {
					sg.Case(jen.Lit(int(c.Sub))).BlockFunc(func(cg *jen.Group) {
						cg.List(jen.Id("dv"), jen.Id("derr")).Op(":=").Id(c.GoName).Dot("Decode").Call(jen.Id("vb"))
						cg.If(jen.Id("derr").Op("!=").Nil()).Block(
							jen.Return(jen.Id("row"), jen.Id("derr")),
						)
						cg.Id("row").Dot(c.FieldName).Op("=").Id("dv")
						cg.Add(observedMark(jen.Id("row"), c.Bit))
					})
				}
			})
		})
		g.Line()

		g.Return(jen.Id("row"), jen.Nil())
	})
}

// emitWatchMergeFn renders the merge<TableName>Row helper that
// updates a row's fields in-place from a partial-column VarBind
// slice. Used by [snmp.Watcher] to maintain per-row state across
// Counter-tier and Static-tier ticks without zeroing the fields the
// tier did not fetch.
//
// Unlike the decode helper, mergeFn silently skips VarBinds whose
// individual Decode fails — the partial-fetch contract is best-effort
// merge, not atomic apply. The Watcher's transient-error surface is
// LastTickErr, populated by the per-tier call sites in watcher.go.
func emitWatchMergeFn(f *jen.File, _ *emitCtx, tw tableWalkContext, fnName string) {
	f.Comment(fnName + " merges the values decoded from vbs into dst, leaving fields")
	f.Comment("whose columns are not present in vbs unchanged. Used by [snmp.Watcher]")
	f.Comment("to maintain per-row state under partial-column fetches (Counter-tier,")
	f.Comment("Static-tier). Best-effort: individual VarBind decode failures are")
	f.Comment("silently skipped rather than propagated, because partial-fetch ticks")
	f.Comment("surface transient errors through [snmp.Watcher.LastTickErr] at the")
	f.Comment("call-site granularity rather than per-VarBind.")
	f.Func().Id(fnName).Params(
		jen.Id("dst").Op("*").Id(tw.RowTypeName),
		jen.Id("vbs").Index().Qual(snmpImport, "VarBind"),
	).BlockFunc(func(g *jen.Group) {
		sortedCols := make([]watchColInfo, len(tw.Cols))
		copy(sortedCols, tw.Cols)
		sort.Slice(sortedCols, func(i, j int) bool { return sortedCols[i].Sub < sortedCols[j].Sub })

		g.For(jen.List(jen.Id("_"), jen.Id("vb")).Op(":=").Range().Id("vbs")).BlockFunc(func(lg *jen.Group) {
			lg.Id("o").Op(":=").Id("vb").Dot("GetHeader").Call().Dot("OID")
			lg.If(jen.Id("o").Dot("Len").Call().Op("==").Lit(0)).Block(jen.Continue())
			lg.Id("entryLen").Op(":=").Add(newOIDCall(tw.EntryPrefix)).Dot("Len").Call()
			lg.If(jen.Id("o").Dot("Len").Call().Op("<=").Id("entryLen")).Block(jen.Continue())
			lg.Id("colID").Op(":=").Id("o").Dot("At").Call(jen.Id("entryLen"))
			lg.Switch(jen.Id("colID")).BlockFunc(func(sg *jen.Group) {
				for _, c := range sortedCols {
					sg.Case(jen.Lit(int(c.Sub))).BlockFunc(func(cg *jen.Group) {
						cg.List(jen.Id("dv"), jen.Id("derr")).Op(":=").Id(c.GoName).Dot("Decode").Call(jen.Id("vb"))
						cg.If(jen.Id("derr").Op("==").Nil()).Block(
							jen.Id("dst").Dot(c.FieldName).Op("=").Id("dv"),
							observedMark(jen.Id("dst"), c.Bit),
						)
					})
				}
			})
		})
	})
}

// emitWatchEqualFn renders the equal<TableName>Row helper that
// compares two Row values field-by-field with the type-appropriate
// comparator. Required because the Row type contains slice fields
// where `==` does not compile (so the generic Watcher cannot use
// `comparable`).
func emitWatchEqualFn(f *jen.File, _ *emitCtx, tw tableWalkContext, fnName string) {
	f.Comment(fnName + " compares two " + tw.RowTypeName + " values for equality.")
	f.Comment("Used by [snmp.Watcher] to compute ChangeKindModified emits. Field-by-")
	f.Comment("field with the type-appropriate comparator (bytes.Equal for []byte,")
	f.Comment("OID.Equal for OID, time.Time.Equal for time.Time, == for everything else).")
	f.Func().Id(fnName).Params(
		jen.Id("a").Id(tw.RowTypeName),
		jen.Id("b").Id(tw.RowTypeName),
	).Bool().BlockFunc(func(g *jen.Group) {
		// Begin the boolean expression with the key comparison so
		// the && chain stays one-per-line readable.
		exprs := make([]*jen.Statement, 0, len(tw.Cols)+2)
		exprs = append(exprs, tw.Key.equalExpr())
		// An absent value becoming a reported zero changes the row
		// even though every data field still compares equal.
		exprs = append(exprs, jen.Id("a").Dot("observed").Op("==").Id("b").Dot("observed"))
		for _, c := range tw.Cols {
			exprs = append(exprs, equalExprForField(c))
		}
		// AND them together.
		ret := exprs[0]
		for _, e := range exprs[1:] {
			ret = jen.Add(ret).Op("&&").Add(e)
		}
		g.Return(ret)
	})
}

// equalExprForField returns the jen expression comparing the named
// field on two Row values. The comparator depends on the field's
// rendered Go type. The rendered type is consulted as a string
// because jen does not surface its type information at this layer.
func equalExprForField(c watchColInfo) *jen.Statement {
	rendered := renderGoType(c.GoType)
	left := jen.Id("a").Dot(c.FieldName)
	right := jen.Id("b").Dot(c.FieldName)

	switch rendered {
	case "[]byte":
		return jen.Qual("bytes", "Equal").Call(left, right)
	case "snmp.OID":
		return jen.Add(left).Dot("Equal").Call(right)
	case "snmp.BitSet":
		// BitSet holds a slice, so == does not compile; Equal compares
		// the sets regardless of how wide the agent padded them.
		return jen.Add(left).Dot("Equal").Call(right)
	case "net.HardwareAddr":
		// HardwareAddr is a []byte under the hood; use bytes.Equal.
		return jen.Qual("bytes", "Equal").Call(left, right)
	case "net.IP":
		return jen.Add(left).Dot("Equal").Call(right)
	case "time.Time":
		return jen.Add(left).Dot("Equal").Call(right)
	default:
		// Plain numerics, strings, enums, and named types over
		// primitives are comparable with ==.
		return jen.Add(left).Op("==").Add(right)
	}
}

// renderGoType returns the rendered Go type string for a jen.Statement
// representing a type. The rendered form (e.g., "snmp.OID",
// "net.HardwareAddr") drives equalExprForField's comparator choice.
func renderGoType(s *jen.Statement) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	if err := s.Render(&b); err != nil {
		return ""
	}
	return b.String()
}

// emitWatcherType renders the typed `*<TableName>Watcher` wrapper and
// its passthrough methods.
func emitWatcherType(f *jen.File, _ *emitCtx, tw tableWalkContext, watcherTypeName string) {
	f.Comment(watcherTypeName + " is a table-aware Watcher over " + tw.TableName + ".")
	f.Comment("The zero value is not usable; construct via " + tw.TableName + ".Watch(ctx, sess, cols, opts...).")
	f.Comment("Use a single iterator. The other methods may be called concurrently.")
	f.Type().Id(watcherTypeName).Struct(
		jen.Id("w").Op("*").Qual(snmpImport, "Watcher").Types(jen.Id(tw.RowTypeName)),
	)

	f.Comment("Iter returns the range-over-func view of the Watcher's event stream.")
	f.Comment("See [snmp.Watcher.Iter] for the contract.")
	f.Func().Params(jen.Id("tw").Op("*").Id(watcherTypeName)).Id("Iter").Params().
		Qual("iter", "Seq2").Types(
		jen.Qual(snmpImport, "OID"),
		jen.Qual(snmpImport, "WatchEvent").Types(jen.Id(tw.RowTypeName)),
	).Block(jen.Return(jen.Id("tw").Dot("w").Dot("Iter").Call()))

	f.Comment("Err returns the underlying Watcher's terminal error, or nil if it")
	f.Comment("completed naturally. See [snmp.Watcher.Err].")
	f.Func().Params(jen.Id("tw").Op("*").Id(watcherTypeName)).Id("Err").Params().Error().Block(
		jen.Return(jen.Id("tw").Dot("w").Dot("Err").Call()),
	)

	f.Comment("Close signals the Watcher's tick goroutine to terminate. Idempotent.")
	f.Comment("See [snmp.Watcher.Close].")
	f.Func().Params(jen.Id("tw").Op("*").Id(watcherTypeName)).Id("Close").Params().Error().Block(
		jen.Return(jen.Id("tw").Dot("w").Dot("Close").Call()),
	)

	f.Comment("Fallback reports whether the Watcher has transitioned to fallback")
	f.Comment("mode. See [snmp.Watcher.Fallback].")
	f.Func().Params(jen.Id("tw").Op("*").Id(watcherTypeName)).Id("Fallback").Params().Bool().Block(
		jen.Return(jen.Id("tw").Dot("w").Dot("Fallback").Call()),
	)

	f.Comment("LastTickErr returns the most-recent transient per-tick error.")
	f.Comment("See [snmp.Watcher.LastTickErr].")
	f.Func().Params(jen.Id("tw").Op("*").Id(watcherTypeName)).Id("LastTickErr").Params().Error().Block(
		jen.Return(jen.Id("tw").Dot("w").Dot("LastTickErr").Call()),
	)

	f.Comment("TableRoot returns the OID of the table this Watcher operates over.")
	f.Func().Params(jen.Id("tw").Op("*").Id(watcherTypeName)).Id("TableRoot").Params().Qual(snmpImport, "OID").Block(
		jen.Return(jen.Id("tw").Dot("w").Dot("TableRoot").Call()),
	)
}

// emitWatchMethod renders the per-descriptor Watch method on the
// table's descriptor singleton type. The method forwards to
// snmp.NewWatcher with the package's emitted <Table>Indicator,
// decode function, equal function, and merge function.
func emitWatchMethod(f *jen.File, _ *emitCtx, tw tableWalkContext, watcherTypeName, decodeFn, equalFn, mergeFn string) {
	descriptorTypeName := unexported(tw.TableName) + "T"
	indicatorVarName := tw.TableName + "Indicator"

	f.Comment("Watch opens a long-lived watch on " + tw.TableName + ".")
	f.Comment("")
	f.Comment("cols selects the columns whose values are reported on every emitted")
	f.Comment("event. opts override cadence bounds, per-column tier classifications,")
	f.Comment("fallback behavior, and other policy — see [snmp.WatchOption] for the")
	f.Comment("full set.")
	f.Comment("")
	f.Comment("cols is a slice rather than variadic because opts is variadic and")
	f.Comment("Go forbids two variadic parameters.")
	f.Comment("")
	f.Comment("Events emitted on this stream:")
	f.Comment("  - ChangeKindAdded    — Row populated, Prev always nil.")
	f.Comment("  - ChangeKindModified — Row is current state. Prev is nil unless")
	f.Comment("                          the caller passed [snmp.WithPrevRow]; when")
	f.Comment("                          set, Prev carries the previous Row.")
	f.Comment("  - ChangeKindRemoved  — Row carries the last-known state at the")
	f.Comment("                          time the row disappeared. Prev is nil.")
	f.Comment("")
	f.Comment("The returned Watcher must be Closed when the caller is done; iteration")
	f.Comment("exit alone does not free the underlying goroutine until Close.")
	f.Comment("")
	f.Comment("On validation failure (invalid cadence bounds, tier override of the")
	f.Comment("indicator column, etc.) Watch returns a Watcher whose Err() returns")
	f.Comment("the cause immediately; range loops exit without emitting events and")
	f.Comment("Close is a no-op.")
	f.Func().Params(jen.Id(descriptorTypeName)).Id("Watch").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("sess").Qual(snmpImport, "Session"),
		jen.Id("cols").Index().Qual(snmpImport, "AnyColumn"),
		jen.Id("opts").Op("...").Qual(snmpImport, "WatchOption"),
	).Op("*").Id(watcherTypeName).Block(
		// Prepend WithTierLookup(ColumnTier) so the package's
		// codegen-classified tiers apply by default. A caller-supplied
		// WithTierLookup in opts still wins because later WatchOption
		// values overwrite earlier ones on TierLookup (last-wins on a
		// single field).
		jen.Id("allOpts").Op(":=").Append(
			jen.Index().Qual(snmpImport, "WatchOption").Values(
				jen.Qual(snmpImport, "WithTierLookup").Call(jen.Id("ColumnTier")),
			),
			jen.Id("opts").Op("..."),
		),
		jen.List(jen.Id("w"), jen.Id("_")).Op(":=").Qual(snmpImport, "NewWatcher").Types(jen.Id(tw.RowTypeName)).Call(
			jen.Id("ctx"),
			jen.Id("sess"),
			jen.Id(indicatorVarName),
			jen.Id("cols"),
			jen.Id(decodeFn),
			jen.Id(equalFn),
			jen.Id(mergeFn),
			jen.Id("allOpts").Op("..."),
		),
		jen.Line(),
		jen.Return(jen.Op("&").Id(watcherTypeName).Values(jen.Dict{
			jen.Id("w"): jen.Id("w"),
		})),
	)
}
