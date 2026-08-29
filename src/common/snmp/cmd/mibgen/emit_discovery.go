package main

import (
	"strings"

	"github.com/sleepinggenius2/gosmi"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"
)

// tableIndicator binds one table to the column or scalar that drives
// its change indicator. The pass that produces these values
// (discoverIndicators) is shared by the tier-classification emitter
// (which gates emission on tableIndicators being non-empty) and the
// indicator-metadata emitter (which materializes one
// snmp.ChangeIndicator value per entry).
type tableIndicator struct {
	// Table is the gosmi node for the table that this indicator
	// describes.
	Table gosmi.SmiNode

	// Kind distinguishes the indicator's shape: indicatorPerRow means
	// the indicator lives as a column inside the table's row;
	// indicatorScalar means the indicator is a separate scalar OBJECT
	// that covers this table.
	Kind indicatorKind

	// IndicatorNode is the column (PerRow) or scalar (Scalar) node
	// that supplies the indicator's OID.
	IndicatorNode gosmi.SmiNode

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
// [indicatorSuffixes]. The "suffix" terminology in the plan refers to
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
func discoverIndicators(ec *emitCtx, mod *gosmi.SmiModule) []tableIndicator {
	nodes := mod.GetNodes()
	tables := make([]gosmi.SmiNode, 0)
	scalars := make([]gosmi.SmiNode, 0)
	for _, n := range nodes {
		switch n.Kind {
		case gosmitypes.NodeTable:
			tables = append(tables, n)
		case gosmitypes.NodeScalar:
			scalars = append(scalars, n)
		}
	}

	var out []tableIndicator

	for _, tbl := range tables {
		// 1) Structural per-row (most precise).
		if ind, ok := discoverPerRowIndicator(tbl); ok {
			out = append(out, ind)
			continue
		}
		// 2) Config-declared.
		if ind, ok := discoverConfigIndicator(ec.cm, tbl, scalars); ok {
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
// scalar declaration's OID can be resolved to a gosmi node and the
// scalar's wire Kind read for the emitted snmp.NewScalarIndicator
// call.
func discoverConfigIndicator(cm Module, tbl gosmi.SmiNode, scalars []gosmi.SmiNode) (tableIndicator, bool) {
	tblOID := oidString(tbl.Oid)
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
			col := findColumnByOID(tbl, decl.ColumnOID)
			if col == nil {
				continue
			}
			return tableIndicator{
				Table:         tbl,
				Kind:          indicatorPerRow,
				IndicatorNode: *col,
				Source:        indicatorFromConfig,
			}, true
		}
	}
	return tableIndicator{}, false
}

// findNodeByOID locates a gosmi node by dotted-decimal OID within a
// slice. Returns (node, true) on hit, (zero, false) on miss.
func findNodeByOID(nodes []gosmi.SmiNode, dotted string) (gosmi.SmiNode, bool) {
	for _, n := range nodes {
		if oidString(n.Oid) == dotted {
			return n, true
		}
	}
	return gosmi.SmiNode{}, false
}

// findColumnByOID locates a column gosmi node within the supplied
// table by dotted-decimal OID. Returns nil on miss.
func findColumnByOID(tbl gosmi.SmiNode, dotted string) *gosmi.SmiNode {
	t := tbl.AsTable()
	for _, name := range t.ColumnOrder {
		col := t.Columns[name]
		if oidString(col.Oid) == dotted {
			cp := col
			return &cp
		}
	}
	return nil
}

// discoverPerRowIndicator looks for a column inside table's row whose
// object name matches the indicator-suffix heuristic. Returns
// (entry, true) when found, ({} , false) otherwise.
func discoverPerRowIndicator(table gosmi.SmiNode) (tableIndicator, bool) {
	t := table.AsTable()
	for _, name := range t.ColumnOrder {
		col := t.Columns[name]
		if col.Access == gosmitypes.AccessNotAccessible {
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
	table gosmi.SmiNode,
	scalars []gosmi.SmiNode,
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
			if sc.Access == gosmitypes.AccessNotAccessible {
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
