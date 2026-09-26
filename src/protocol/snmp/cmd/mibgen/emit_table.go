package main

import (
	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// emitTable expands one MIB table node into a suite of declarations:
//
//   - `var <Column> = snmp.NewTableColumn[T](...)` or `snmp.NewFusedTableColumn[T](...)`
//     for every accessible column. The column is registered with [emitCtx.dispatch]
//     so emitDispatch can later wire the per-package OID map.
//   - `var <table>Columns = []snmp.AnyColumn{...}` in observed-bit order.
//   - `type <Table>Key struct { … }` with one field per INDEX part,
//     plus the shapes and decode helper that read it from the instance
//     suffix (see [emitTableKey]).
//   - `type <Table>Row struct { Key <Table>Key; … }` with one field
//     per column (named after the column). A table whose key does not
//     resolve carries the raw suffix as `Index snmp.OID` instead.
//   - `type <Table>Walker struct{ snmp.TableWalker[<TableRow>] }`.
//   - `type <table>T struct{ snmp.Table[<TableRow>, *<TableWalker>] }`
//     plus `var <Table> = <table>T{ Table: snmp.NewTable(...) }`.
//
// Table walks and iteration are coordinated by the shared runtime types
// [snmp.Table] and [snmp.TableWalker].
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
	descriptorTypeName := unexported(tableName) + "T"

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
		// An index-only table has no row to walk, but a table that
		// AUGMENTS it keys its rows by this table's struct, so the
		// struct is declared all the same.
		emitTableKey(f, ec, t, tableName)

		return nil
	}

	for _, c := range cols {
		f.Comment(c.GoName + " is the column " + c.Node.Name + " of table " + table.Name + ".")
		for _, line := range splitDoc(c.Node.Description) {
			f.Comment(line)
		}
		emitDeprecationParagraph(f, c.Node)
		if c.Res.RawFuse == "" {
			f.Var().Id(c.GoName).Op("=").Qual(snmpImport, "NewTableColumn").Types(c.Res.GoType.Clone()).Call(
				newOIDCall(c.ColumnOID),
				c.Res.Kind.Clone(),
				c.Res.DecodeFunc(),
				jen.Lit(c.Bit),
			)
		} else {
			_, result, ok := decoderHelperFor(c.Res.Variant)
			var fused jen.Code
			if !ok || renderGoType(c.Res.GoType) == renderGoType(result) {
				fused = jen.Qual(snmpImport, c.Res.RawFuse)
			} else {
				fused = jen.Qual(snmpImport, c.Res.RawFuse+"As").Types(c.Res.GoType.Clone())
			}
			f.Var().Id(c.GoName).Op("=").Qual(snmpImport, "NewFusedTableColumn").Types(c.Res.GoType.Clone()).Call(
				newOIDCall(c.ColumnOID),
				c.Res.Kind.Clone(),
				c.Res.DecodeFunc(),
				fused,
				jen.Lit(c.Bit),
			)
		}
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

	colsVar := unexported(tableName) + "Columns"
	f.Var().Id(colsVar).Op("=").Index().Qual(snmpImport, "AnyColumn").ValuesFunc(func(g *jen.Group) {
		for _, c := range cols {
			g.Id(c.GoName)
		}
	})

	key := emitTableKey(f, ec, t, tableName)

	// A field left at zero is ambiguous on its own — the agent may have
	// reported a genuine zero or may not have reported the column at
	// all — so the row carries one bit per column recording which ones
	// actually landed, read back through the Observed method.
	observedWords := (len(cols) + 63) / 64
	if key.raw() {
		f.Comment(rowTypeName + " is one row of " + table.Name + ". Index carries the OID")
		f.Comment("suffix beyond the table-entry prefix; the remaining fields are")
	} else {
		f.Comment(rowTypeName + " is one row of " + table.Name + ". Key is the decoded INDEX; a")
		f.Comment("suffix that does not match the declared INDEX leaves it zero, and")
		f.Comment("[" + rowTypeName + ".KeyValid] reports which. The remaining fields are")
	}
	f.Comment("populated only for columns the caller passed to Walk(). Use")
	f.Comment("[" + rowTypeName + ".Observed] to tell a reported zero from a column the")
	f.Comment("agent never answered.")
	f.Comment("The zero value has no observed columns. Concurrent reads are safe;")
	f.Comment("callers must synchronize mutation of the row or its referenced data.")
	f.Type().Id(rowTypeName).StructFunc(func(g *jen.Group) {
		if key.raw() {
			g.Id("Index").Qual(snmpImport, "OID")
		} else {
			g.Id("Key").Add(key.Type.Clone())
			g.Id("keyValid").Bool()
		}
		for _, c := range cols {
			g.Id(c.FieldName).Add(c.Res.GoType.Clone())
		}
		g.Line()
		g.Comment("observed carries one bit per column of this table, in")
		g.Comment("column-OID order, set when the walk decoded a value for")
		g.Comment("that column on this row.")
		g.Id("observed").Index(jen.Lit(observedWords)).Uint64()
	})

	if !key.raw() {
		f.Comment("KeyValid reports whether the row's instance suffix decoded as the declared")
		f.Comment("INDEX. A false result means Key is zero and the agent's suffix did not")
		f.Comment("have the declared shape; the row's columns are still populated.")
		f.Func().Params(jen.Id("r").Id(rowTypeName)).Id("KeyValid").Params().Bool().Block(
			jen.Return(jen.Id("r").Dot("keyValid")),
		)
	}

	f.Comment("Observed reports whether col returned a value for this row. A column")
	f.Comment("the agent answered reads true even when the answer was zero or empty;")
	f.Comment("a column that was requested but never landed, one that was not passed")
	f.Comment("to Walk, and any column of another table all read false.")
	f.Func().Params(jen.Id("r").Id(rowTypeName)).Id("Observed").Params(
		jen.Id("col").Qual(snmpImport, "AnyColumn"),
	).Bool().Block(
		jen.Return(jen.Qual(snmpImport, "ColumnObserved").Call(
			jen.Id("r").Dot("observed").Index(jen.Op(":")),
			jen.Id(colsVar),
			jen.Id("col"),
		)),
	)

	f.Comment(walkerTypeName + " streams selected columns of " + table.Name + ".")
	f.Comment("The zero value is not usable; construct via " + tableName + ".Walk(ctx, sess, cols...).")
	f.Comment("Iteration is single-use and single-consumer; Close and Err are safe concurrently.")
	f.Type().Id(walkerTypeName).Struct(
		jen.Qual(snmpImport, "TableWalker").Types(jen.Id(rowTypeName)),
	)

	f.Comment(descriptorTypeName + " is the singleton type of " + tableName + ".")
	f.Type().Id(descriptorTypeName).Struct(
		jen.Qual(snmpImport, "Table").Types(jen.Id(rowTypeName), jen.Op("*").Id(walkerTypeName)),
	)
	f.Comment(tableName + " is the descriptor for the " + table.Name + " table.")
	emitDeprecationParagraph(f, table)
	f.Var().Id(tableName).Op("=").Id(descriptorTypeName).Values(jen.Dict{
		jen.Id("Table"): jen.Qual(snmpImport, "NewTable").Call(
			jen.Lit(table.Name),
			jen.Id(colsVar),
			jen.Func().Params(
				jen.Id("idx").Qual(snmpImport, "OID"),
				jen.Id("row").Op("*").Id(rowTypeName),
			).BlockFunc(func(g *jen.Group) {
				if key.raw() {
					g.Id("row").Dot("Index").Op("=").Id("idx")
				} else {
					g.List(jen.Id("row").Dot("Key"), jen.Id("row").Dot("keyValid")).Op("=").Id(key.DecodeFn).Call(jen.Id("idx"))
				}
			}),
			jen.Func().Params(
				jen.Id("row").Op("*").Id(rowTypeName),
				jen.Id("ordinal").Int(),
				jen.Id("rv").Qual(snmpImport, "RawVarBind"),
			).Error().BlockFunc(func(g *jen.Group) {
				g.Switch(jen.Id("ordinal")).BlockFunc(func(sg *jen.Group) {
					for _, c := range cols {
						sg.Case(jen.Lit(c.Bit)).Block(
							jen.Return(jen.Qual(snmpImport, "DecodeColumn").Call(
								jen.Id("rv"),
								jen.Id(c.GoName),
								jen.Op("&").Id("row").Dot(c.FieldName),
								jen.Id("row").Dot("observed").Index(jen.Op(":")),
							)),
						)
					}
				})
				g.Return(jen.Nil())
			}),
			jen.Func().Params(
				jen.Id("tw").Qual(snmpImport, "TableWalker").Types(jen.Id(rowTypeName)),
			).Op("*").Id(walkerTypeName).Block(
				jen.Return(jen.Op("&").Id(walkerTypeName).Values(jen.Dict{
					jen.Id("TableWalker"): jen.Id("tw"),
				})),
			),
		),
	})

	// The descriptor names the indicator var emitIndicators writes at
	// the end of the file; a method body may refer to a package-level
	// var declared after it, so emission order does not matter here.
	f.Comment("Descriptor returns the table as a [snmp.TableDescriptor]: its root OID, its")
	f.Comment("change indicator when the MIB declares one, and the Go type of its row key.")
	f.Comment("The descriptor is a value; hold it without the row or walker types to probe")
	f.Comment("for the table or declare it as a dependency.")
	f.Func().Params(jen.Id(descriptorTypeName)).Id("Descriptor").Params().Qual(snmpImport, "TableDescriptor").Block(
		jen.Return(jen.Qual(snmpImport, "TableDescriptor").Values(jen.DictFunc(func(d jen.Dict) {
			d[jen.Id("Root")] = newOIDCall(tablePrefix)
			if _, ok := ec.tableIndicatorsByOID[tablePrefix]; ok {
				d[jen.Id("Indicator")] = jen.Id(tableName + "Indicator")
			}
			d[jen.Id("KeyType")] = jen.Lit(key.descriptorKeyType())
		}))),
	)

	// The Watch machinery is emitted only for tables that have a
	// discovered or declared indicator; the emit_watch.go gate consults
	// ec.tableIndicators.
	emitWatch(f, ec, tableWalkContext{
		TableName:   tableName,
		TableOID:    tablePrefix,
		EntryPrefix: entryPrefix,
		RowTypeName: rowTypeName,
		Key:         key,
		Cols:        cols,
	})

	return nil
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
