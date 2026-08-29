package main

import (
	"sort"

	"github.com/dave/jennifer/jen"
)

// emitDispatch writes the per-package wire-OID → AnyColumn map. The map keys are column wire keys
// ([snmp.OID.WireKey] — the BER content octets as a string), so a
// caller holding a raw wire OID can look the column up with
// map[string(rawBytes)] allocation-free; the values reference the
// package-local Column[T] values emitted by [emitTable].
//
// The unexported map identifier is module-prefixed (e.g.
// `ifMIBOIDDispatch`) so two generated files dropped into the same Go
// package directory do not collide on a shared `oidDispatch` symbol.
// The exported accessor stays as `OIDDispatch` — each generated file
// emits a per-package method, and if two files actually do land in
// the same package the developer is expected to merge them by hand;
// the rename here removes the *guaranteed* collision the previous
// shared name created.
//
// If the module emitted no columns the function emits nothing — the
// empty map would be valid Go but would mask the fact that the
// package is data-light. Downstream consumers that need a universal
// "give me the dispatch map for every package" knob can call the per-
// package OIDDispatch() accessor (also emitted below) and union the
// results.
func emitDispatch(f *jen.File, ec *emitCtx) {
	if len(ec.dispatch) == 0 {
		return
	}

	entries := make([]dispatchEntry, len(ec.dispatch))
	copy(entries, ec.dispatch)
	sort.Slice(entries, func(i, j int) bool { return entries[i].OID < entries[j].OID })

	mapName := dispatchMapName(ec.mod.Name)

	f.Comment(mapName + " maps column wire keys ([snmp.OID.WireKey]) to their")
	f.Comment("typed AnyColumn for fast")
	f.Comment("lookup during table-walk decoding. Per-MIB-module — no global")
	f.Comment("registry; cross-package callers should consult OIDDispatch().")
	f.Var().Id(mapName).Op("=").Map(jen.String()).Qual(snmpImport, "AnyColumn").Values(jen.DictFunc(func(d jen.Dict) {
		for _, e := range entries {
			d[newOIDCall(e.OID).Dot("WireKey").Call()] = jen.Id(e.Name)
		}
	}))

	f.Comment("OIDDispatch returns a shallow copy of the package's wire-key →")
	f.Comment("AnyColumn map. The copy isolates callers from accidental mutation")
	f.Comment("of the package-internal map.")
	f.Func().Id("OIDDispatch").Params().Map(jen.String()).Qual(snmpImport, "AnyColumn").Block(
		jen.Id("out").Op(":=").Make(
			jen.Map(jen.String()).Qual(snmpImport, "AnyColumn"),
			jen.Len(jen.Id(mapName)),
		),
		jen.For(jen.List(jen.Id("k"), jen.Id("v")).Op(":=").Range().Id(mapName)).Block(
			jen.Id("out").Index(jen.Id("k")).Op("=").Id("v"),
		),
		jen.Return(jen.Id("out")),
	)
}

// dispatchMapName derives a Go identifier for the per-module dispatch
// map from the MIB module's name. The result is a lowerCamelCase
// identifier with the "OIDDispatch" suffix — e.g. "IF-MIB" yields
// "ifMIBOIDDispatch", "SNMPv2-MIB" yields "snmpv2MIBOIDDispatch".
//
// We keep the identifier unexported so that the public surface of the
// generated package is the OIDDispatch() accessor; the map itself is a
// private implementation detail.
func dispatchMapName(modName string) string {
	upper := camelCase(modName)
	if upper == "" {
		return "oidDispatch"
	}
	// Lower-case the first rune to make the identifier package-private.
	runes := []rune(upper)
	runes[0] = lowerFirst(runes[0])
	return string(runes) + "OIDDispatch"
}

// lowerFirst lower-cases an ASCII rune; non-ASCII is returned
// unchanged because the camelCase helper has already validated the
// identifier's leading rune.
func lowerFirst(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
