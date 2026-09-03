package main

import (
	"strings"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// tableIndicator binds one table to the column or scalar that drives
// its change indicator. The pass that produces these values
// (discoverIndicators) is shared by the tier-classification emitter
// (which gates emission on tableIndicators being non-empty) and the
// indicator-metadata emitter (which materializes one
// snmp.ChangeIndicator value per entry).
type tableIndicator struct {
	// Table is the resolved node for the table this indicator
	// describes.
	Table *smi.Node

	// Kind distinguishes the indicator's shape: indicatorPerRow means
	// the indicator lives as a column inside the table's row;
	// indicatorScalar means the indicator is a separate scalar OBJECT
	// that covers this table.
	Kind indicatorKind

	// IndicatorNode is the column (PerRow) or scalar (Scalar) node
	// that supplies the indicator's OID.
	IndicatorNode *smi.Node

	// Source records how the indicator was bound to the table — useful
	// for diagnostic comments emitted next to the generated var.
	Source indicatorSource
}

type indicatorKind uint8

const (
	indicatorPerRow indicatorKind = iota
	indicatorScalar
)

type indicatorSource uint8

const (
	indicatorFromStructuralPerRow     indicatorSource = iota // column inside the row matches name heuristic
	indicatorFromStructuralNamePrefix                        // scalar name is the table's name plus an indicator suffix
	indicatorFromConfig                                      // user-declared in mibgen.yaml
)

// indicatorSuffixes lists the object-name patterns that classify a
// column or scalar as an indicator. Lower-case for case-insensitive
// matching; matched as a substring after lowering the candidate name.
//
// The patterns intentionally subsume one another (LastChangeTime
// contains LastChange) so a single substring check against the longest
// pattern is sufficient — but enumerating them keeps the rule
// inspectable.
var indicatorSuffixes = []string{
	"lastchangetime",
	"lastupdated",
	"lastchange",
}

// matchesIndicatorNameSuffix reports whether name fits the
// indicator-name-suffix heuristic. The check is
// case-insensitive substring match against the patterns listed in
// [indicatorSuffixes]. "Suffix" here refers to
// the conventional SMIv2 naming pattern (`<table>LastChange`,
// `<table>LastChangeTime`); this implementation treats the matcher as
// substring-anywhere for robustness against edge spellings.
func matchesIndicatorNameSuffix(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range indicatorSuffixes {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// discoverIndicators scans every table in the module for a change
// indicator. When a config-declared and a structural indicator both
// bind the same table, precedence is:
//
//  1. Structural per-row (most precise — one probe per row).
//  2. Config-declared (whether scalar or column override).
//  3. Structural name-prefix scalar (scalar named after the table plus
//     an indicator suffix, e.g. ifStackLastChange for ifStackTable).
//
// The result is the source of truth for whether a package contains
// any Watch-eligible tables — emit_tier.go's emission gate consults
// it, and emit_indicator.go renders one ChangeIndicator var per entry.
func discoverIndicators(ec *emitCtx, mod *smi.Module) []tableIndicator {
	tables := make([]*smi.Node, 0)
	scalars := make([]*smi.Node, 0)
	for _, n := range mod.Nodes {
		switch n.Kind {
		case smi.NodeTable:
			tables = append(tables, n)
		case smi.NodeScalar:
			scalars = append(scalars, n)
		}
	}

	var out []tableIndicator

	for _, tbl := range tables {
		// 1) Structural per-row (most precise).
		if ind, ok := discoverPerRowIndicator(ec, tbl); ok {
			out = append(out, ind)
			continue
		}
		// 2) Config-declared.
		if ind, ok := discoverConfigIndicator(ec, ec.cm, tbl, scalars); ok {
			out = append(out, ind)
			continue
		}
		// 3) Structural name-prefix scalar.
		if ind, ok := discoverNamePrefixScalarIndicator(tbl, scalars); ok {
			out = append(out, ind)
		}
	}

	return out
}

// discoverConfigIndicator consults the module's configured
// IndicatorDecl entries and returns a binding if one covers `tbl`.
// Both shapes (scalar with covers_tables; column with table_oid) are
// considered.
//
// scalars is the module's list of scalar nodes — required so the
// scalar declaration's OID can be resolved to a node and the
// scalar's wire Kind read for the emitted snmp.NewScalarIndicator
// call.
func discoverConfigIndicator(
	ec *emitCtx, cm Module, tbl *smi.Node, scalars []*smi.Node,
) (tableIndicator, bool) {
	tblOID := tbl.OID.String()
	for _, decl := range cm.Indicators {
		switch {
		case decl.ScalarOID != "":
			// Scalar-with-covers-tables form.
			covers := false
			for _, t := range decl.CoversTables {
				if t == tblOID {
					covers = true
					break
				}
			}
			if !covers {
				continue
			}
			scalar, ok := findNodeByOID(scalars, decl.ScalarOID)
			if !ok {
				// Validated at config-load time only that the OID
				// parses; runtime check ensures the scalar exists
				// in the module. Skip silently — a clear diagnostic
				// would come from a separate pass; for now the
				// caller will see no indicator emission for this
				// table.
				continue
			}
			return tableIndicator{
				Table:         tbl,
				Kind:          indicatorScalar,
				IndicatorNode: scalar,
				Source:        indicatorFromConfig,
			}, true
		case decl.ColumnOID != "":
			if decl.TableOID != tblOID {
				continue
			}
			col := findColumnByOID(ec, tbl, decl.ColumnOID)
			if col == nil {
				continue
			}
			return tableIndicator{
				Table:         tbl,
				Kind:          indicatorPerRow,
				IndicatorNode: col,
				Source:        indicatorFromConfig,
			}, true
		}
	}
	return tableIndicator{}, false
}

// findNodeByOID locates a node by dotted-decimal OID within a slice.
// Returns (node, true) on hit, (nil, false) on miss.
func findNodeByOID(nodes []*smi.Node, dotted string) (*smi.Node, bool) {
	for _, n := range nodes {
		if n.OID.String() == dotted {
			return n, true
		}
	}

	return nil, false
}

// findColumnByOID locates a column node within the supplied
// table by dotted-decimal OID. Returns nil on miss.
func findColumnByOID(ec *emitCtx, tbl *smi.Node, dotted string) *smi.Node {
	t := ec.table(tbl.OID.String())
	if t == nil {
		return nil
	}
	for _, col := range t.Columns {
		if col.OID.String() == dotted {
			return col
		}
	}

	return nil
}

// discoverPerRowIndicator looks for a column inside table's row whose
// object name matches the indicator-suffix heuristic. Returns
// (entry, true) when found, ({} , false) otherwise.
func discoverPerRowIndicator(ec *emitCtx, table *smi.Node) (tableIndicator, bool) {
	t := ec.table(table.OID.String())
	if t == nil {
		return tableIndicator{}, false
	}
	for _, col := range t.Columns {
		if col.Access == smi.AccessNotAccessible {
			continue
		}
		if !matchesIndicatorNameSuffix(col.Name) {
			continue
		}
		return tableIndicator{
			Table:         table,
			Kind:          indicatorPerRow,
			IndicatorNode: col,
			Source:        indicatorFromStructuralPerRow,
		}, true
	}
	return tableIndicator{}, false
}

// discoverNamePrefixScalarIndicator looks for a module-level scalar
// whose object name is the table's name plus an indicator suffix — the
// conventional SMIv2 spelling for a table-covering change scalar. Two
// name shapes are accepted, checked in this order across all scalars:
//
//   - `<tableName><suffix>`: ifTableLastChange for ifTable.
//   - `<base><suffix>` where base is the table name with a trailing
//     "Table" stripped: ifStackLastChange for ifStackTable.
//
// The full-name form is tried first so a module that (pathologically)
// declares both spellings binds the more explicit one. Matching is
// case-insensitive. The rule needs no subtree relationship between
// scalar and table: the name correlation alone is specific enough that
// a false binding would require two unrelated objects sharing an exact
// `<table><suffix>` spelling within one module.
func discoverNamePrefixScalarIndicator(
	table *smi.Node,
	scalars []*smi.Node,
) (tableIndicator, bool) {
	tblLower := strings.ToLower(table.Name)
	base := strings.TrimSuffix(tblLower, "table")
	prefixes := []string{tblLower}
	if base != tblLower && base != "" {
		prefixes = append(prefixes, base)
	}
	for _, prefix := range prefixes {
		for _, sc := range scalars {
			// A not-accessible scalar cannot be polled, so binding it
			// would leave the Watcher probing a dead OID forever. Same
			// guard discoverPerRowIndicator applies to columns.
			if sc.Access == smi.AccessNotAccessible {
				continue
			}
			scLower := strings.ToLower(sc.Name)
			for _, suffix := range indicatorSuffixes {
				if scLower == prefix+suffix {
					return tableIndicator{
						Table:         table,
						Kind:          indicatorScalar,
						IndicatorNode: sc,
						Source:        indicatorFromStructuralNamePrefix,
					}, true
				}
			}
		}
	}
	return tableIndicator{}, false
}
