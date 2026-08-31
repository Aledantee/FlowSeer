package main

import (
	"github.com/dave/jennifer/jen"
)

// emitIndicators writes one `var <Table>Indicator = snmp.MustChange
// Indicator(...)` per table that has a discovered indicator.
//
// Per-row indicator emission:
//
//	var IfTableIndicator = snmp.MustChangeIndicator(snmp.NewPerRowIndicator(
//	    IfLastChange,
//	    snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2),
//	))
//
// Scalar indicator emission (single Coverage entry per emitted var:
// each Watcher fires its own probe, so the per-table Watcher only needs
// to know that its table is covered, not which other tables share the
// scalar):
//
//	var EntPhysicalTableIndicator = snmp.MustChangeIndicator(snmp.NewScalarIndicator(
//	    snmp.MustOID(1, 3, 6, 1, 2, 1, 47, 1, 4, 1),
//	    snmp.KindUinteger32,
//	    []snmp.OID{snmp.MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1)},
//	))
//
// The var name is the table's CamelCase name + "Indicator" — mirrors
// the existing `<Table> <table>T` descriptor singleton.
func emitIndicators(f *jen.File, ec *emitCtx) {
	for _, ti := range ec.tableIndicators {
		emitOneIndicator(f, ec, ti)
	}
}

// emitOneIndicator renders a single ChangeIndicator var per the rules
// in the package-level [emitIndicators] comment.
func emitOneIndicator(f *jen.File, ec *emitCtx, ti tableIndicator) {
	tableName := camelCase(ti.Table.Name)
	varName := tableName + "Indicator"

	switch ti.Kind {
	case indicatorPerRow:
		emitPerRowIndicator(f, ec, ti, varName, tableName)
	case indicatorScalar:
		emitScalarIndicator(f, ec, ti, varName, tableName)
	}
}

// emitPerRowIndicator emits the per-row form. The indicator column
// must already have been emitted as a Column[T] var (it lives inside
// the table); we reference it by its package-local Go identifier.
func emitPerRowIndicator(f *jen.File, _ *emitCtx, ti tableIndicator, varName, _ string) {
	colName := camelCase(ti.IndicatorNode.Name)
	tableRoot := ti.Table.OID.String()

	f.Comment(varName + " is the per-row change indicator for " + ti.Table.Name + ".")
	f.Comment("The Watcher probes the indicator column on each tick and only")
	f.Comment("re-fetches a row's other state-tier columns when the row's")
	f.Comment("indicator value advances. See [snmp.NewPerRowIndicator] and")
	f.Comment("[snmp.Watcher] for the consumption contract.")
	emitIndicatorSourceComment(f, ti.Source)
	f.Var().Id(varName).Op("=").Qual(snmpImport, "MustChangeIndicator").Call(
		jen.Qual(snmpImport, "NewPerRowIndicator").Call(
			jen.Id(colName),
			newOIDCall(tableRoot),
		),
	)
}

// emitScalarIndicator emits the scalar form. The scalar's wire Kind
// is resolved via [resolveType] over the scalar's node.
func emitScalarIndicator(f *jen.File, ec *emitCtx, ti tableIndicator, varName, _ string) {
	scalarOID := ti.IndicatorNode.OID.String()
	tableRoot := ti.Table.OID.String()

	res := resolveType(ec, ti.IndicatorNode)

	f.Comment(varName + " is the scalar change indicator for " + ti.Table.Name + ".")
	f.Comment("The Watcher Gets " + ti.IndicatorNode.Name + " on each tick; when the value")
	f.Comment("advances the full table is walked and diffed against the snapshot.")
	f.Comment("See [snmp.NewScalarIndicator] and [snmp.Watcher] for the contract.")
	emitIndicatorSourceComment(f, ti.Source)
	f.Var().Id(varName).Op("=").Qual(snmpImport, "MustChangeIndicator").Call(
		jen.Qual(snmpImport, "NewScalarIndicator").Call(
			newOIDCall(scalarOID),
			res.Kind.Clone(),
			jen.Index().Qual(snmpImport, "OID").Values(newOIDCall(tableRoot)),
		),
	)
}

// emitIndicatorSourceComment writes a single-line provenance comment
// next to the emitted var so a future reader can trace why mibgen
// produced this binding without grepping the discovery code.
func emitIndicatorSourceComment(f *jen.File, src indicatorSource) {
	switch src {
	case indicatorFromStructuralPerRow:
		f.Comment("// Discovered by mibgen structural rule: per-row column name matches indicator-suffix heuristic.")
	case indicatorFromStructuralNamePrefix:
		f.Comment("// Discovered by mibgen structural rule: scalar named after the table plus an indicator suffix.")
	case indicatorFromConfig:
		f.Comment("// Declared in mibgen.yaml under modules.<name>.indicators.")
	}
}
