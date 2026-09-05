package main

import (
	"sort"
	"strings"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/protocol/smi"
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
// Iter decodes rows assembled by the shared bounded selected-column merge.
//
// emitTable assumes [emitEnums] has already run so [emitCtx.enumNames]
// is populated. It returns an error when a column's type cannot be
// resolved, leaving the table unwritten.
func emitTable(f *jen.File, ec *emitCtx, table *smi.Node) error {
	t := ec.table(table.OID.String())
	if t == nil || t.Row == nil || len(t.Columns) == 0 {
		// A table with no row or no columns has nothing to bind. Skip
		// it rather than emit a walker over nothing; the resolver has
		// already diagnosed whatever went missing.
		return nil
	}

	// The entry node's last sub-id is conventionally 1, but it is read
	// off the resolved row rather than assumed, so a vendor MIB with a
	// non-standard entry arc still binds.
	tablePrefix := table.OID.String()
	entryPrefix := t.Row.OID.String()

	tableName := camelCase(table.Name)
	rowTypeName := tableName + "Row"
	walkerTypeName := tableName + "Walker"
	descriptorTypeName := strings.ToLower(tableName[:1]) + tableName[1:] + "T"

	// Per-column data: name, OID, resolved type. Built up here so
	// the column-var pass and the row-struct pass agree on every
	// column's Go name and type.
	cols := make([]colInfo, 0, len(t.Columns))
	for _, cn := range t.Columns {
		// Not-accessible index columns are real OBJECT-TYPEs and so are
		// listed here; they are left off the generated row struct
		// because they travel in the index suffix rather than in a
		// VarBind value.
		if cn.Access == smi.AccessNotAccessible {
			continue
		}
		res, err := resolveType(ec, cn)
		if err != nil {
			return err
		}
		cols = append(cols, colInfo{
			Node:      cn,
			GoName:    camelCase(cn.Name),
			FieldName: camelCase(cn.Name),
			ColumnOID: cn.OID.String(),
			Sub:       cn.OID.At(cn.OID.Len() - 1),
			Bit:       len(cols),
			Res:       res,
		})
	}
	if len(cols) == 0 {
		return nil
	}

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

	// A field left at zero is ambiguous on its own — the agent may have
	// reported a genuine zero or may not have reported the column at
	// all — so the row carries one bit per column recording which ones
	// actually landed, read back through the Observed method.
	observedWords := (len(cols) + 63) / 64
	f.Comment(rowTypeName + " is one row of " + table.Name + ". Index carries the OID")
	f.Comment("suffix beyond the table-entry prefix; the remaining fields are")
	f.Comment("populated only for columns the caller passed to Walk(). Use")
	f.Comment("[" + rowTypeName + ".Observed] to tell a reported zero from a column the")
	f.Comment("agent never answered.")
	f.Comment("The zero value has no observed columns. Concurrent reads are safe;")
	f.Comment("callers must synchronize mutation of the row or its referenced data.")
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

	f.Comment(walkerTypeName + " streams selected columns of " + table.Name + ".")
	f.Comment("The zero value is not usable; construct via " + tableName + ".Walk(ctx, sess, cols...).")
	f.Comment("Iteration is single-use and single-consumer; Close and Err are safe concurrently.")
	f.Type().Id(walkerTypeName).Struct(
		jen.Id("rw").Op("*").Qual(snmpImport, "ColumnWalker"),
		jen.Id("cols").Index().Qual(snmpImport, "AnyColumn"),
	)

	emitTableIter(f, walkerTypeName, rowTypeName, cols)

	f.Comment("Err returns the underlying walker's terminal error, or nil if")
	f.Comment("the walk completed naturally.")
	f.Func().Params(jen.Id("tw").Op("*").Id(walkerTypeName)).Id("Err").Params().Error().Block(
		jen.Return(jen.Id("tw").Dot("rw").Dot("Err").Call()),
	)

	f.Comment(descriptorTypeName + " is the singleton type of " + tableName + ".")
	f.Type().Id(descriptorTypeName).Struct()
	f.Comment(tableName + " is the descriptor for the " + table.Name + " table.")
	f.Var().Id(tableName).Id(descriptorTypeName)

	f.Comment("Close stops retrieval. It is idempotent and safe during iteration.")
	f.Func().Params(jen.Id("tw").Op("*").Id(walkerTypeName)).Id("Close").Params().Block(
		jen.Id("tw").Dot("rw").Dot("Close").Call(),
	)
	f.Comment("Walk lazily retrieves only selected columns with bounded defaults.")
	f.Comment("Rows are the union of selected values in numeric OID index order.")
	f.Comment("No columns means no rows or requests. Duplicate selections are ignored.")
	f.Comment("Unknown or foreign columns fail before I/O with [snmp.ErrForeignColumn].")
	f.Func().Params(jen.Id("t").Id(descriptorTypeName)).Id("Walk").Params(
		jen.Id("ctx").Qual("context", "Context"), jen.Id("sess").Qual(snmpImport, "Session"),
		jen.Id("cols").Op("...").Qual(snmpImport, "AnyColumn"),
	).Op("*").Id(walkerTypeName).Block(
		jen.Return(jen.Id("t").Dot("WalkWithOptions").Call(jen.Id("ctx"), jen.Id("sess"), jen.Qual(snmpImport, "TableWalkOptions").Values(), jen.Id("cols").Op("..."))),
	)
	f.Comment("WalkWithOptions is Walk with request sizing and per-call controls.")
	f.Comment("SNMPv1 remains unsupported. Parent cancellation is an error; stopping iteration is successful.")
	f.Func().Params(jen.Id(descriptorTypeName)).Id("WalkWithOptions").Params(
		jen.Id("ctx").Qual("context", "Context"), jen.Id("sess").Qual(snmpImport, "Session"),
		jen.Id("options").Qual(snmpImport, "TableWalkOptions"), jen.Id("cols").Op("...").Qual(snmpImport, "AnyColumn"),
	).Op("*").Id(walkerTypeName).BlockFunc(func(g *jen.Group) {
		g.Id("seen").Op(":=").Make(jen.Map(jen.String()).Bool())
		g.Var().Id("selected").Index().Qual(snmpImport, "AnyColumn")
		g.Var().Id("roots").Index().Qual(snmpImport, "OID")
		g.For(jen.List(jen.Id("_"), jen.Id("c")).Op(":=").Range().Id("cols")).BlockFunc(func(cg *jen.Group) {
			cg.Switch(jen.Id("c").Dot("Key").Call()).BlockFunc(func(sg *jen.Group) {
				cases := make([]jen.Code, 0, len(cols))
				for _, c := range cols {
					cases = append(cases, jen.Id(c.GoName).Dot("Key").Call())
				}
				sg.Case(cases...)
				sg.Default().Block(
					jen.Id("w").Op(":=").Qual(snmpImport, "WalkColumns").Call(jen.Id("ctx"), jen.Id("sess"), jen.Nil(), jen.Id("options")),
					jen.Id("w").Dot("Fail").Call(jen.Qual(errsImport, "Wrapf").Call(jen.Qual(snmpImport, "ErrForeignColumn"), jen.Lit(table.Name+".Walk: column %s"), jen.Id("c").Dot("OID").Call())),
					jen.Return(jen.Op("&").Id(walkerTypeName).Values(jen.Dict{jen.Id("rw"): jen.Id("w")})),
				)
			})
			cg.If(jen.Id("seen").Index(jen.Id("c").Dot("Key").Call())).Block(jen.Continue())
			cg.Id("seen").Index(jen.Id("c").Dot("Key").Call()).Op("=").True()
			cg.Id("selected").Op("=").Append(jen.Id("selected"), jen.Id("c"))
			cg.Id("roots").Op("=").Append(jen.Id("roots"), jen.Id("c").Dot("OID").Call())
		})
		g.Return(jen.Op("&").Id(walkerTypeName).Values(jen.Dict{
			jen.Id("rw"):   jen.Qual(snmpImport, "WalkColumns").Call(jen.Id("ctx"), jen.Id("sess"), jen.Id("roots"), jen.Id("options")),
			jen.Id("cols"): jen.Id("selected"),
		}))
	})

	// The Watch machinery is emitted only for tables that have a
	// discovered or declared indicator; the emit_watch.go gate consults
	// ec.tableIndicators.
	emitWatch(f, ec, tableWalkContext{
		TableName:   tableName,
		TableOID:    tablePrefix,
		EntryPrefix: entryPrefix,
		RowTypeName: rowTypeName,
		Cols:        cols,
	})

	return nil
}

// emitTableIter leaves protocol ordering and buffering to the runtime. Decoding
// happens only for the row about to be delivered, preserving the error prefix.
func emitTableIter(f *jen.File, walkerTypeName, rowTypeName string, cols []colInfo) {
	sortedCols := append([]colInfo(nil), cols...)
	sort.Slice(sortedCols, func(i, j int) bool { return sortedCols[i].Sub < sortedCols[j].Sub })
	f.Comment("Iter yields complete selected-column rows in numeric OID suffix order")
	f.Comment("(192.168.0.2 precedes 192.168.0.10). It retains one batch per selected")
	f.Comment("column. Breaking iteration stops retrieval. A decode error omits the")
	f.Comment("failing row and later rows; already delivered rows remain valid. Check Err.")
	f.Func().Params(jen.Id("tw").Op("*").Id(walkerTypeName)).Id("Iter").Params().Qual("iter", "Seq2").Types(jen.Qual(snmpImport, "OID"), jen.Id(rowTypeName)).Block(
		jen.Return(jen.Func().Params(jen.Id("yield").Func().Params(jen.Qual(snmpImport, "OID"), jen.Id(rowTypeName)).Bool()).BlockFunc(func(g *jen.Group) {
			g.For(jen.List(jen.Id("idx"), jen.Id("cells")).Op(":=").Range().Id("tw").Dot("rw").Dot("Iter").Call()).BlockFunc(func(rg *jen.Group) {
				rg.Id("row").Op(":=").Id(rowTypeName).Values(jen.Dict{jen.Id("Index"): jen.Id("idx")})
				rg.For(jen.List(jen.Id("_"), jen.Id("cell")).Op(":=").Range().Id("cells")).BlockFunc(func(lg *jen.Group) {
					lg.Id("rv").Op(":=").Id("cell").Dot("Value")
					lg.Var().Id("derr").Error()
					lg.Switch(jen.Id("tw").Dot("cols").Index(jen.Id("cell").Dot("Column")).Dot("Key").Call()).BlockFunc(func(sg *jen.Group) {
						for _, c := range sortedCols {
							sg.Case(jen.Id(c.GoName).Dot("Key").Call()).BlockFunc(func(cg *jen.Group) {
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
								if c.Res.RawFuse != "" {
									cg.If(jen.List(jen.Id("v"), jen.Id("okRaw")).Op(":=").Qual(snmpImport, c.Res.RawFuse).Call(jen.Id("rv")), jen.Id("okRaw")).Block(
										jen.Id("row").Dot(c.FieldName).Op("=").Add(c.Res.GoType.Clone()).Call(jen.Id("v")),
										observedMark(jen.Id("row"), c.Bit),
									).Else().BlockFunc(genericArm)
								} else {
									genericArm(cg)
								}
							})
						}
					})

					lg.If(jen.Id("derr").Op("!=").Nil()).Block(
						jen.Id("tw").Dot("rw").Dot("Fail").Call(jen.Id("derr")), jen.Return(),
					)
				})
				rg.If(jen.Op("!").Id("yield").Call(jen.Id("idx"), jen.Id("row"))).Block(jen.Return())
			})
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

// colInfo is one accessible column of a table being emitted. The
// column-var, row-struct, Iter and Watch passes all read it, so the
// Go identifiers and the resolved type a column renders under are
// decided once.
type colInfo struct {
	Node      *smi.Node
	GoName    string
	FieldName string
	ColumnOID string
	// Sub is the last sub-id of the column OID — the column-id a
	// VarBind's OID carries just past the table's entry prefix.
	Sub uint32
	// Bit is the column's position in the row's observed set.
	Bit int
	// Res carries the column's Go type, wire kind, decoder and
	// (when the kind has one) fused raw decoder.
	Res resolved
}
