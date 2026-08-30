package main

import (
	"sort"
	"strings"

	"github.com/dave/jennifer/jen"
	"github.com/sleepinggenius2/gosmi"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"
)

// emitTable expands one MIB table node into a suite of declarations:
//
//   - `var <Column> = snmp.NewColumn[T](...)` for every accessible
//     column. The column is registered with [emitCtx.dispatch]
//     so emitDispatch can later wire the per-package OID map.
//   - `type <Table>Row struct { Index snmp.OID; … }` with one field
//     per column (named after the column).
//   - `type <Table>Walker struct{...}` and its `Iter` / `Err` methods.
//   - `type <table>T struct{}` plus `var <Table> <table>T` and a
//     `Walk(ctx, sess, cols ...snmp.AnyColumn) *<Table>Walker` method.
//
// The Iter implementation groups VarBinds by row-index suffix; a row
// is yielded when the next-row threshold is crossed (next VarBind has
// a different suffix) or when the underlying Walker is exhausted.
//
// emitTable assumes [emitEnums] has already run so [emitCtx.enumNames]
// is populated.
func emitTable(f *jen.File, ec *emitCtx, table gosmi.SmiNode) {
	t := table.AsTable()
	if len(t.ColumnOrder) == 0 {
		// Table with no defined columns is a generator anomaly (no
		// SEQUENCE body). Skip silently rather than emit invalid
		// code; libsmi would normally have already complained.
		return
	}

	// Row OID is the table OID with .1 appended (the entry node's
	// last sub-id is conventionally 1; we read it from the gosmi row
	// node directly so vendor MIBs with non-standard entry indices
	// still work).
	row := t.GetRow()
	tablePrefix := oidString(table.Oid)
	entryPrefix := oidString(row.Oid) // the table-entry OID

	tableName := camelCase(table.Name)
	rowTypeName := tableName + "Row"
	walkerTypeName := tableName + "Walker"
	descriptorTypeName := strings.ToLower(tableName[:1]) + tableName[1:] + "T"

	// Per-column data: name, OID, resolved type. Built up here so
	// the column-var pass and the row-struct pass agree on every
	// column's Go name and type.
	type colInfo struct {
		Node      gosmi.SmiNode
		GoName    string
		FieldName string
		ColumnOID string
		Sub       uint32 // last sub-id of the column OID
		Bit       int    // this column's bit in the row's observed set
		Res       resolved
	}
	cols := make([]colInfo, 0, len(t.ColumnOrder))
	for _, name := range t.ColumnOrder {
		cn := t.Columns[name]
		// not-accessible index columns still appear in ColumnOrder
		// because they are real OBJECT-TYPEs; we skip them on the
		// generated row struct since they are encoded in the index
		// suffix, not in a VarBind value.
		if cn.Access == gosmitypes.AccessNotAccessible {
			continue
		}
		cols = append(cols, colInfo{
			Node:      cn,
			GoName:    camelCase(cn.Name),
			FieldName: camelCase(cn.Name),
			ColumnOID: oidString(cn.Oid),
			Sub:       uint32(cn.Oid[len(cn.Oid)-1]),
			Bit:       len(cols),
			Res:       resolveType(ec, cn),
		})
	}
	if len(cols) == 0 {
		return
	}

	// 1) Emit one Column[T] value per column.
	for _, c := range cols {
		f.Comment(c.GoName + " is the column " + c.Node.Name + " of table " + table.Name + ".")
		for _, line := range splitDoc(c.Node.Description) {
			f.Comment(line)
		}
		f.Var().Id(c.GoName).Op("=").Qual(snmpImport, "NewColumn").Types(c.Res.GoType.Clone()).Call(
			newOIDCall(c.ColumnOID),
			c.Res.Kind.Clone(),
			c.Res.DecodeFunc(),
		)
		ec.dispatch = append(ec.dispatch, dispatchEntry{OID: c.ColumnOID, Name: c.GoName})

		// Record the column's tier classification for the per-
		// package ColumnTiers map. classifyTier
		// consults the resolved VarBind variant and the object
		// name's indicator-suffix heuristic; emission is gated in
		// emitTierMap on the module having at least one
		// Watch-eligible table.
		ec.tiers = append(ec.tiers, tierEntry{
			OID:  c.ColumnOID,
			Tier: classifyTier(c.Node, c.Res.Variant),
			Name: c.GoName,
		})
	}

	// 2) Row struct plus its per-column observation set. A field left
	// at zero is ambiguous on its own — the agent may have reported a
	// genuine zero or may not have reported the column at all — so the
	// row carries one bit per column recording which ones actually
	// landed, read back through the Observed method.
	observedWords := (len(cols) + 63) / 64
	f.Comment(rowTypeName + " is one row of " + table.Name + ". Index carries the OID")
	f.Comment("suffix beyond the table-entry prefix; the remaining fields are")
	f.Comment("populated only for columns the caller passed to Walk(). Use")
	f.Comment(rowTypeName + ".Observed to tell a reported zero from a column the")
	f.Comment("agent never answered.")
	f.Type().Id(rowTypeName).StructFunc(func(g *jen.Group) {
		g.Id("Index").Qual(snmpImport, "OID")
		for _, c := range cols {
			g.Id(c.FieldName).Add(c.Res.GoType.Clone())
		}
		g.Line()
		g.Comment("observed carries one bit per column of this table, in")
		g.Comment("column-OID order, set when the walk decoded a value for")
		g.Comment("that column on this row.")
		g.Id("observed").Index(jen.Lit(observedWords)).Uint64()
	})

	f.Comment("Observed reports whether col returned a value for this row. A column")
	f.Comment("the agent answered reads true even when the answer was zero or empty;")
	f.Comment("a column that was requested but never landed, one that was not passed")
	f.Comment("to Walk, and any column of another table all read false.")
	f.Func().Params(jen.Id("r").Id(rowTypeName)).Id("Observed").Params(
		jen.Id("col").Qual(snmpImport, "AnyColumn"),
	).Bool().Block(
		// Keyed on the column's OID wire key rather than its last
		// sub-id: sub-ids collide across tables constantly, and a
		// caller passing another table's column must read false.
		jen.Switch(jen.Id("col").Dot("Key").Call()).BlockFunc(func(sg *jen.Group) {
			for _, c := range cols {
				sg.Case(jen.Id(c.GoName).Dot("Key").Call()).Block(
					jen.Return(observedTest(jen.Id("r"), c.Bit)),
				)
			}
		}),
		jen.Line(),
		jen.Return(jen.False()),
	)

	// 3) Walker wrapper. We embed an indexed map column-id → AnyColumn so
	// the hot path can look up the decoder by the last sub-id of each
	// varbind's OID without scanning the cols slice every time. The
	// walker consumes the raw fast path: varbinds arrive as
	// undecoded [snmp.RawVarBind]s, columns match by byte prefix, and
	// known-Kind values decode fused — the generic decode runs only as
	// the per-varbind fallback.
	f.Comment(walkerTypeName + " is a table-aware walker over " + table.Name + ".")
	f.Comment("Construct via " + tableName + ".Walk(ctx, sess, cols...).")
	f.Type().Id(walkerTypeName).Struct(
		jen.Id("rw").Op("*").Qual(snmpImport, "RawWalker"),
		jen.Id("cols").Index().Qual(snmpImport, "AnyColumn"),
		jen.Id("byCol").Map(jen.Uint32()).Qual(snmpImport, "AnyColumn"),
	)

	// 4) Iter method. The bulk of the work lives here.
	iterCols := make([]colInfoLike, len(cols))
	for i, c := range cols {
		iterCols[i] = colInfoLike{GoName: c.GoName, FieldName: c.FieldName, Sub: c.Sub, Bit: c.Bit, RawFuse: c.Res.RawFuse, GoType: c.Res.GoType}
	}
	emitTableIter(f, walkerTypeName, rowTypeName, entryPrefix, iterCols)

	// 5) Err passthrough.
	f.Comment("Err returns the underlying walker's terminal error, or nil if")
	f.Comment("the walk completed naturally.")
	f.Func().Params(jen.Id("tw").Op("*").Id(walkerTypeName)).Id("Err").Params().Error().Block(
		jen.Return(jen.Id("tw").Dot("rw").Dot("Err").Call()),
	)

	// 6) Descriptor singleton + Walk method.
	f.Comment(descriptorTypeName + " is the singleton type of " + tableName + ".")
	f.Type().Id(descriptorTypeName).Struct()
	f.Comment(tableName + " is the descriptor for the " + table.Name + " table.")
	f.Var().Id(tableName).Id(descriptorTypeName)

	f.Comment("Walk launches a BulkWalk over " + table.Name + " and returns a")
	f.Comment("table-aware iterator. Only the columns listed in cols are")
	f.Comment("decoded; varbinds for unlisted columns are skipped. The walk")
	f.Comment("rides the raw fast path (BulkWalkRaw); sessions or responses")
	f.Comment("that cannot deliver raw bytes degrade transparently to the")
	f.Comment("generic per-varbind decode.")
	f.Func().Params(jen.Id(descriptorTypeName)).Id("Walk").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("sess").Qual(snmpImport, "Session"),
		jen.Id("cols").Op("...").Qual(snmpImport, "AnyColumn"),
	).Op("*").Id(walkerTypeName).Block(
		jen.Id("w").Op(":=").Id("sess").Dot("BulkWalkRaw").Call(jen.Id("ctx"), newOIDCall(tablePrefix)),
		jen.Id("byCol").Op(":=").Make(jen.Map(jen.Uint32()).Qual(snmpImport, "AnyColumn"), jen.Len(jen.Id("cols"))),
		jen.Line(),
		jen.For(jen.List(jen.Id("_"), jen.Id("c")).Op(":=").Range().Id("cols")).Block(
			jen.Id("o").Op(":=").Id("c").Dot("OID").Call(),
			jen.If(jen.Id("o").Dot("Len").Call().Op("==").Lit(0)).Block(jen.Continue()),
			jen.Id("byCol").Index(jen.Id("o").Dot("At").Call(jen.Id("o").Dot("Len").Call().Op("-").Lit(1))).Op("=").Id("c"),
		),
		jen.Line(),
		jen.Return(jen.Op("&").Id(walkerTypeName).Values(jen.Dict{
			jen.Id("rw"):    jen.Id("w"),
			jen.Id("cols"):  jen.Id("cols"),
			jen.Id("byCol"): jen.Id("byCol"),
		})),
	)

	// 7) Watch method + companion helpers. Emitted only for
	// tables that have a discovered or declared indicator; the
	// emit_watch.go gate consults ec.tableIndicators.
	twctx := tableWalkContext{
		TableName:   tableName,
		TableOID:    tablePrefix,
		EntryPrefix: entryPrefix,
		RowTypeName: rowTypeName,
		Cols:        make([]watchColInfo, 0, len(cols)),
	}
	for _, c := range cols {
		twctx.Cols = append(twctx.Cols, watchColInfo{
			GoName:    c.GoName,
			FieldName: c.FieldName,
			Sub:       c.Sub,
			Bit:       c.Bit,
			GoType:    c.Res.GoType,
			Variant:   c.Res.Variant,
		})
	}
	emitWatch(f, ec, twctx)
}

// emitTableIter writes the Iter method body. The semantics:
//
//  1. The walker is consumed fully into an index-keyed buffer + an
//     ordered index list before any row is yielded. This is the only
//     correct shape over real-world BulkWalk output, which agents
//     stream column-major ((col1,row1)(col1,row2)…(col2,row1)…) — a
//     row-boundary-on-index-change emitter inflates 14 ports into
//     308 rows on that shape.
//  2. The first time an index appears under the entry prefix, it is
//     appended to `order` and a zero-valued row is established in
//     `buffer`. This includes indexes whose only VarBind in this walk
//     belongs to a column the caller did not request — row presence
//     is recorded for every index observed, matching today's
//     streaming row-presence behavior.
//  3. On a per-column decode error, rows for indexes strictly before
//     the failing index (in `order` position) are flushed via yield;
//     the failing row and anything after it in `order` are not
//     yielded. Walker.Fail is then called with the decode error and
//     iteration returns.
//  4. At the natural end of the walk, every index in `order` is
//     yielded in first-appearance order — which for a well-behaved
//     agent equals lexicographic OID order over the index suffix.
//     This is NOT numerical order for composite-index tables
//     (ipAddrTable indexed by IP-as-OID: 192.168.0.10 sorts before
//     192.168.0.2). The contract is pinned in the generated
//     docstring.
//
// We can't dynamically dispatch on Column[T] at runtime (Go generics
// are not reflectable), so the per-column dispatch falls back on a
// switch over the column's last-sub-id: each case calls the
// corresponding Column[T]'s Decode method directly.
//
// Memory profile: full table buffered before first yield —
// O(rows × requested columns × column-type size). The current shipping
// package set tops out at hrSWRunTable (thousands of rows) and
// ipNetToMediaTable (tens of thousands of rows on edge routers);
// streaming-with-column-major-detection is a documented follow-up
// path if a real workload pushes against this.
func emitTableIter(f *jen.File, walkerTypeName, rowTypeName, entryPrefix string, cols []colInfoLike) {
	// Sort cols by sub-id for stable switch case ordering.
	sortedCols := make([]colInfoLike, len(cols))
	copy(sortedCols, cols)
	sort.Slice(sortedCols, func(i, j int) bool { return sortedCols[i].Sub < sortedCols[j].Sub })

	// The contract block is duplicated into every generated Iter so
	// callers reading the file (not just the emitter) see the four
	// guarantees inline. Numbered list — each entry is a separate
	// f.Comment call so it formats as one bullet per line.
	f.Comment("Iter yields one (Index, Row) pair per row of the table walk. The")
	f.Comment("full BulkWalk is buffered before any row is yielded, so the")
	f.Comment("generated walker is correct over both column-major and row-major")
	f.Comment("agent emission. Contracts:")
	f.Comment("")
	f.Comment("  1. Ordering: rows yield in the index's first-appearance position")
	f.Comment("     in the agent's BulkWalk response — which for a well-behaved")
	f.Comment("     agent equals lexicographic OID order over the index suffix.")
	f.Comment("     This is NOT numerical order for composite-index tables")
	f.Comment("     (e.g. ipAddrTable indexed by IP-as-OID: 192.168.0.10 sorts")
	f.Comment("     before 192.168.0.2). Integer-keyed tables (ifTable,")
	f.Comment("     hrProcessorTable) get numeric order for free.")
	f.Comment("")
	f.Comment("  2. Row presence: every index observed under the entry prefix")
	f.Comment("     yields a row, even when only unrequested columns landed on")
	f.Comment("     that index. The row's requested-column fields stay at zero")
	f.Comment("     and Observed reports every column of that row as unobserved.")
	f.Comment("")
	f.Comment("  3. Decode error: rows for indexes strictly before the failing")
	f.Comment("     index in appearance order flush before Walker.Fail is set,")
	f.Comment("     preserving partial-progress visibility for the operator.")
	f.Comment("     The failing row and anything after it are not yielded.")
	f.Comment("     Check Err() afterwards for the terminal cause.")
	f.Comment("")
	f.Comment("  4. Memory profile: O(rows × requested columns) buffered before")
	f.Comment("     the first yield. Bounded by table size, not walk position —")
	f.Comment("     callers that broke out early via 'for row := range Iter()'")
	f.Comment("     still pay the full-walk buffer cost.")
	f.Func().Params(jen.Id("tw").Op("*").Id(walkerTypeName)).Id("Iter").Params().Qual("iter", "Seq2").Types(jen.Qual(snmpImport, "OID"), jen.Id(rowTypeName)).Block(
		jen.Return(jen.Func().Params(jen.Id("yield").Func().Params(jen.Qual(snmpImport, "OID"), jen.Id(rowTypeName)).Bool()).BlockFunc(func(g *jen.Group) {
			// entryWire is the entry OID's BER content octets: raw
			// varbind names match by byte prefix (canonical encoding is
			// bijective, so byte prefix == arc prefix).
			g.Id("entryWire").Op(":=").Add(newOIDCall(entryPrefix)).Dot("WireBytes").Call()

			// buffer is keyed by the index suffix's raw wire octets —
			// stable, hashable, and unique per index; lookups with
			// string(bytes) are allocation-free, so a walk allocates
			// one key per distinct row, not per varbind. Values are
			// pointers so per-column decodes mutate in place.
			g.Id("buffer").Op(":=").Make(jen.Map(jen.String()).Op("*").Id(rowTypeName))
			// Parallel slices record appearance order: orderIdx carries
			// the snmp.OID handed to yield(), orderKey the map key.
			g.Var().Id("orderIdx").Index().Qual(snmpImport, "OID")
			g.Var().Id("orderKey").Index().String()
			g.Line()

			g.For(jen.Id("rv").Op(":=").Range().Id("tw").Dot("rw").Dot("Iter").Call()).BlockFunc(func(lg *jen.Group) {
				// Defensive: ignore varbinds outside the entry subtree.
				lg.If(jen.Op("!").Qual("bytes", "HasPrefix").Call(jen.Id("rv").Dot("OID"), jen.Id("entryWire"))).Block(jen.Continue())
				// Column id is the arc right after the entry prefix;
				// the remainder is the index suffix. A varbind landing
				// at entry.colID with no index arcs is malformed and
				// would otherwise collide on the empty map key,
				// synthesizing a phantom row — skip it.
				lg.Id("suffix").Op(":=").Id("rv").Dot("OID").Index(jen.Len(jen.Id("entryWire")).Op(":"))
				lg.List(jen.Id("colID"), jen.Id("colLen"), jen.Id("okArc")).Op(":=").Qual(snmpImport, "RawFirstArc").Call(jen.Id("suffix"))
				lg.If(jen.Op("!").Id("okArc").Op("||").Id("colLen").Op(">=").Len(jen.Id("suffix"))).Block(jen.Continue())
				lg.Id("idxWire").Op(":=").Id("suffix").Index(jen.Id("colLen").Op(":"))

				// Establish (or look up) the row for this index. First
				// observation — including via an unrequested-column
				// varbind — records appearance order and
				// zero-initializes the row. The index OID materializes
				// once per distinct row, not per varbind.
				lg.List(jen.Id("row"), jen.Id("exists")).Op(":=").Id("buffer").Index(jen.String().Call(jen.Id("idxWire")))
				lg.If(jen.Op("!").Id("exists")).BlockFunc(func(ng *jen.Group) {
					ng.List(jen.Id("idx"), jen.Id("idxErr")).Op(":=").Qual(snmpImport, "DecodeIndexArcs").Call(jen.Id("idxWire"))
					ng.If(jen.Id("idxErr").Op("!=").Nil()).Block(jen.Continue())
					ng.Id("key").Op(":=").String().Call(jen.Id("idxWire"))
					ng.Id("row").Op("=").Op("&").Id(rowTypeName).Values()
					ng.Id("buffer").Index(jen.Id("key")).Op("=").Id("row")
					ng.Id("orderIdx").Op("=").Append(jen.Id("orderIdx"), jen.Id("idx"))
					ng.Id("orderKey").Op("=").Append(jen.Id("orderKey"), jen.Id("key"))
				})

				// Unrequested column: row presence is already recorded;
				// skip the decode.
				lg.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("tw").Dot("byCol").Index(jen.Id("colID"))
				lg.If(jen.Op("!").Id("ok")).Block(jen.Continue())

				// Per-column decode dispatch. Columns with a fused
				// primitive decode straight from the wire when
				// the tag matches the declared Kind; every other case —
				// off-spec tag, exception marker, pre-decoded varbind —
				// falls back to the generic column decoder, preserving
				// its coercion semantics and error text exactly. derr
				// is hoisted above the switch so the error-handling
				// flush path lives once.
				lg.Var().Id("derr").Error()
				lg.Switch(jen.Id("colID")).BlockFunc(func(sg *jen.Group) {
					for _, c := range sortedCols {
						sg.Case(jen.Lit(int(c.Sub))).BlockFunc(func(cg *jen.Group) {
							genericArm := func(ag *jen.Group) {
								ag.List(jen.Id("vb"), jen.Id("vbErr")).Op(":=").Id("rv").Dot("Decode").Call()
								ag.If(jen.Id("vbErr").Op("!=").Nil()).Block(
									jen.Id("derr").Op("=").Id("vbErr"),
								).Else().BlockFunc(func(bg *jen.Group) {
									bg.List(jen.Id("dv"), jen.Id("dErr")).Op(":=").Id(c.GoName).Dot("Decode").Call(jen.Id("vb"))
									bg.If(jen.Id("dErr").Op("!=").Nil()).Block(
										jen.Id("derr").Op("=").Id("dErr"),
									).Else().Block(
										jen.Id("row").Dot(c.FieldName).Op("=").Id("dv"),
										observedMark(jen.Id("row"), c.Bit),
									)
								})
							}
							if c.RawFuse != "" {
								cg.If(jen.List(jen.Id("v"), jen.Id("okRaw")).Op(":=").Qual(snmpImport, c.RawFuse).Call(jen.Id("rv")), jen.Id("okRaw")).Block(
									jen.Id("row").Dot(c.FieldName).Op("=").Add(c.GoType.Clone()).Call(jen.Id("v")),
									observedMark(jen.Id("row"), c.Bit),
								).Else().BlockFunc(genericArm)
							} else {
								genericArm(cg)
							}
						})
					}
				})

				// Decode error: flush rows strictly before this index in
				// appearance order, then surface the error and stop.
				// Caller break-out during the partial flush still
				// terminates with Fail recorded — the failure is real
				// regardless of whether the caller wanted more rows.
				lg.If(jen.Id("derr").Op("!=").Nil()).BlockFunc(func(eg *jen.Group) {
					eg.For(jen.Id("i").Op(":=").Lit(0).Op(";").Id("i").Op("<").Len(jen.Id("orderKey")).Op(";").Id("i").Op("++")).BlockFunc(func(fg *jen.Group) {
						fg.If(jen.Id("orderKey").Index(jen.Id("i")).Op("==").String().Call(jen.Id("idxWire"))).Block(jen.Break())
						fg.If(jen.Op("!").Id("yield").Call(jen.Id("orderIdx").Index(jen.Id("i")), jen.Op("*").Id("buffer").Index(jen.Id("orderKey").Index(jen.Id("i"))))).Block(
							jen.Id("tw").Dot("rw").Dot("Fail").Call(jen.Id("derr")),
							jen.Return(),
						)
					})
					eg.Id("tw").Dot("rw").Dot("Fail").Call(jen.Id("derr"))
					eg.Return()
				})
			})

			// Natural-end flush: yield every buffered row in
			// first-appearance order. Caller break-out terminates
			// cleanly — the underlying RawWalker.Iter observes the
			// false return on its next yield and drains the pump.
			g.Line()
			g.For(jen.Id("i").Op(":=").Lit(0).Op(";").Id("i").Op("<").Len(jen.Id("orderIdx")).Op(";").Id("i").Op("++")).Block(
				jen.If(jen.Op("!").Id("yield").Call(jen.Id("orderIdx").Index(jen.Id("i")), jen.Op("*").Id("buffer").Index(jen.Id("orderKey").Index(jen.Id("i"))))).Block(
					jen.Return(),
				),
			)
		})),
	)
}

// observedTest renders the expression that reads column bit from a
// row value's observation set, e.g. `r.observed[0]&(1<<3) != 0`.
func observedTest(rowExpr *jen.Statement, bit int) *jen.Statement {
	return rowExpr.Clone().Dot("observed").Index(jen.Lit(bit / 64)).
		Op("&").Parens(jen.Lit(1).Op("<<").Lit(bit % 64)).Op("!=").Lit(0)
}

// observedMark renders the statement that records column bit as
// observed on a row, e.g. `row.observed[0] |= 1 << 3`.
func observedMark(rowExpr *jen.Statement, bit int) *jen.Statement {
	return rowExpr.Clone().Dot("observed").Index(jen.Lit(bit / 64)).
		Op("|=").Lit(1).Op("<<").Lit(bit % 64)
}

// colInfoLike is the table-emitter's interface for column-info; the
// helper exists so emit_table.go can pass either the local colInfo
// struct or any equivalent to emitTableIter. Concrete type for cols
// passed into emitTableIter.
type colInfoLike struct {
	GoName    string
	FieldName string
	Sub       uint32
	// Bit is the column's position in the row's observed set.
	Bit int
	// RawFuse names the snmp.Raw* fused decoder for the column's Kind
	// (empty → generic decode only); GoType is the row field's type,
	// used to cast the fused primitive's numeric result.
	RawFuse string
	GoType  *jen.Statement
}
