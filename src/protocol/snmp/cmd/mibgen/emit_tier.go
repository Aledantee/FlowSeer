package main

import (
	"sort"
	"strings"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// tierEntry binds one column's OID string to its emitted tier
// classification. emit_tier.go renders these as the per-package
// <prefix>ColumnTiers map entries.
type tierEntry struct {
	OID  string
	Tier string // jen-renderable identifier, e.g. "snmp.TierIndicator"
	// Name is the column's Go identifier — emitted as a trailing
	// line-comment on the map entry to keep the generated file
	// readable.
	Name string
}

// classifyTier resolves a column's [snmp.Tier]. The classification is
// purely codegen-time:
//
//  1. Counter32 / Counter64 wire variant → TierCounter.
//  2. TC name "TimeStamp" → TierIndicator (every TimeStamp-typed
//     object is, by SMIv2 semantics, a change indicator).
//  3. Object name matches the indicator-suffix heuristic
//     ([matchesIndicatorNameSuffix]) → TierIndicator. This is the
//     load-bearing rule for IF-MIB's ifLastChange, which is a raw
//     TimeTicks and so carries no convention name for rule 2 to read.
//  4. Otherwise → TierState. TierStatic is never auto-classified;
//     callers opt in via [snmp.WithColumnTier].
//
// Rules 2 and 3 are why the classifier reads a type's name and its base
// separately: TimeStamp and TimeTicks stand on the same base and mean
// different things, and only one of them says that the value marks when
// something changed.
//
// variant is the concrete snmp.VarBind variant name from [resolved]
// (e.g. "Counter32", "TimeTicks", "OctetString"). The function returns
// the rendered tier identifier (e.g., "snmp.TierIndicator") for use in
// the emitted map.
func classifyTier(node *smi.Node, variant string) string {
	switch variant {
	case "Counter32Var", "Counter64Var":
		return "snmp.TierCounter"
	}
	if node.Type != nil && node.Type.Name == "TimeStamp" {
		return "snmp.TierIndicator"
	}
	if matchesIndicatorNameSuffix(node.Name) {
		return "snmp.TierIndicator"
	}
	return "snmp.TierState"
}

// emitTierMap writes the per-package <prefix>ColumnTiers map and its
// ColumnTier(col) accessor. Emission is gated on the module containing
// at least one Watch-eligible table (signaled by
// ec.tableIndicators being non-empty), matching the conservative
// "no Watch method without an indicator" rule and avoiding
// generated-code bloat across vendor MIB packages that nothing will
// consume.
//
// The map identifier is module-prefixed (`<prefix>ColumnTiers`)
// mirroring the existing dispatch map's collision-avoidance shape.
// The exported accessor stays as `ColumnTier(col)`; if two generated
// files actually land in the same Go package, the developer is
// expected to merge them by hand.
func emitTierMap(f *jen.File, ec *emitCtx) {
	if !ec.hasIndicator {
		return
	}
	if len(ec.tiers) == 0 {
		return
	}
	entries := make([]tierEntry, len(ec.tiers))
	copy(entries, ec.tiers)
	sort.Slice(entries, func(i, j int) bool { return entries[i].OID < entries[j].OID })

	mapName := tierMapName(ec.mod.Name)

	f.Comment(mapName + " maps column wire keys ([snmp.OID.WireKey]) to their")
	f.Comment("snmp.Tier classification.")
	f.Comment("Tier classification drives the Watcher's scheduler — counters fire")
	f.Comment("on their own cadence, state columns refresh on indicator advance,")
	f.Comment("and indicator columns drive the change-detection probe itself.")
	f.Comment("See snmp.Tier and snmp.Watcher for the consumption contract.")
	f.Var().Id(mapName).Op("=").Map(jen.String()).Qual(snmpImport, "Tier").Values(jen.DictFunc(func(d jen.Dict) {
		for _, e := range entries {
			// Keys are wire keys: computed at init via WireKey()
			// so raw-walk consumers can look up with raw OID bytes,
			// allocation-free. The tier identifier renders as a
			// qualified reference; jennifer's Qual handles the import.
			d[newOIDCall(e.OID).Dot("WireKey").Call()] = qualTier(e.Tier)
		}
	}))

	f.Comment("ColumnTier returns the codegen-classified [snmp.Tier] for col, or")
	f.Comment("snmp.TierUnknown when col's OID is not known to this package. The")
	f.Comment("generated Watch methods install this function via")
	f.Comment("[snmp.WithTierLookup]; returning TierUnknown lets the runtime fall")
	f.Comment("back to its wire-Kind heuristic for columns this package does not")
	f.Comment("classify. Caller overrides via [snmp.WithColumnTier] still win.")
	f.Func().Id("ColumnTier").Params(
		jen.Id("col").Qual(snmpImport, "AnyColumn"),
	).Qual(snmpImport, "Tier").Block(
		jen.If(jen.Id("col").Op("==").Nil()).Block(
			jen.Return(jen.Qual(snmpImport, "TierUnknown")),
		),
		jen.Id("t").Op(",").Id("ok").Op(":=").Id(mapName).Index(jen.Id("col").Dot("Key").Call()),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Qual(snmpImport, "TierUnknown")),
		),
		jen.Return(jen.Id("t")),
	)
}

// qualTier converts a fully-qualified tier identifier (e.g.
// "snmp.TierIndicator") into a jen.Code that renders to the same
// reference via the snmp import.
func qualTier(tierStr string) *jen.Statement {
	// Strip the "snmp." prefix so jen.Qual can route through the
	// managed import; the input is always one of TierCounter /
	// TierIndicator / TierState / TierStatic.
	if name, ok := strings.CutPrefix(tierStr, "snmp."); ok && name != "" {
		return jen.Qual(snmpImport, name)
	}
	return jen.Qual(snmpImport, tierStr)
}

// tierMapName derives the Go identifier for a per-module tier map
// from the MIB module's name. Mirrors [dispatchMapName] but with the
// "ColumnTiers" suffix — e.g. "IF-MIB" yields "ifMIBColumnTiers".
func tierMapName(modName string) string {
	return moduleScopedName(modName, "ColumnTiers", "columnTiers")
}
