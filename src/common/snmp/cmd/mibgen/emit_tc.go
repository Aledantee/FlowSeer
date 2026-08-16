package main

import (
	"strings"
	"unicode"

	"github.com/dave/jennifer/jen"
	"github.com/sleepinggenius2/gosmi"
	gosmimodels "github.com/sleepinggenius2/gosmi/models"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"
)

// emitCtx threads per-module state through the emission passes: which
// imports are needed for cross-MIB references, which OIDs have been
// emitted (so the dispatch map can be built), and the per-MIB override
// lookup table.
//
// emitCtx values are not reusable across modules; construct one per
// EmitModule call via [newEmitCtx].
type emitCtx struct {
	// mod is the gosmi module currently being emitted.
	mod *gosmi.SmiModule
	// cm is the configured Module entry for [emitCtx.mod].
	cm Module
	// cfgByName maps every loaded module's MIB name to its Module entry
	// so cross-MIB qualified references resolve to the configured Go
	// package. Optional; nil falls back to a lowercased MIB name.
	cfgByName map[string]Module
	// pkgPrefix is the import-path prefix declared on the CLI.
	pkgPrefix string
	// overrides indexes [Module.Overrides] by OID for O(1) lookup.
	overrides map[string]Override
	// dispatch records column OIDs that should appear in the per-package
	// oidDispatch table; the key is the dotted OID and the value is the
	// generated Go identifier of the Column[T] value.
	dispatch []dispatchEntry
	// enumNames remembers the Go type names emitted by emitEnums so
	// scalars/columns can refer to them by name and the second-pass
	// "is this an enum?" check is cheap.
	enumNames map[string]string // SmiType.Name -> Go identifier

	// tiers records per-column tier classifications observed during
	// the table-emission pass. emitTierMap renders these as the
	// per-package ColumnTiers map entries, gated on hasIndicator.
	tiers []tierEntry

	// tableIndicators is the discovery output: zero or more
	// (table, indicator) bindings produced by [discoverIndicators].
	// emitTierMap consults its length as the emission gate;
	// emit_indicator.go materializes one ChangeIndicator var
	// per entry.
	tableIndicators []tableIndicator

	// tableIndicatorsByOID indexes tableIndicators by the dotted-decimal
	// table OID so tableHasIndicator runs in O(1). Built once after
	// discoverIndicators populates tableIndicators.
	tableIndicatorsByOID map[string]struct{}

	// hasIndicator caches whether the module has any Watch-eligible
	// table. Set by [discoverIndicators] running before the table
	// emission pass so tier-map emission can gate cleanly.
	hasIndicator bool
}

// dispatchEntry binds one column's OID string to its emitted Go
// identifier; the dispatch pass renders these as map entries.
type dispatchEntry struct {
	OID  string
	Name string
}

// newEmitCtx builds an emitCtx with overrides indexed and enumNames
// pre-allocated. The caller fills [emitCtx.enumNames] during the enum
// pass.
func newEmitCtx(mod *gosmi.SmiModule, cm Module, cfgByName map[string]Module, pkgPrefix string) *emitCtx {
	ovs := make(map[string]Override, len(cm.Overrides))
	for _, o := range cm.Overrides {
		ovs[o.OID] = o
	}
	return &emitCtx{
		mod:       mod,
		cm:        cm,
		cfgByName: cfgByName,
		pkgPrefix: pkgPrefix,
		overrides: ovs,
		enumNames: make(map[string]string),
	}
}

// resolved describes how a node's value should be represented in
// generated Go: the Go-type code to render, the wire [snmp.Kind] of the
// VarBind variant the agent emits, and a decoder builder that returns
// a [jen.Code] producing `func(snmp.VarBind) (T, error)`.
type resolved struct {
	// GoType is the jen.Code for the Go type the field/return value
	// should have (e.g. `int32`, `string`, `net.HardwareAddr`,
	// `IfOperStatus`).
	GoType *jen.Statement
	// Kind is the jen.Code referring to the [snmp.Kind] constant — for
	// example `snmp.KindOctetString`.
	Kind *jen.Statement
	// DecodeFunc returns a jen.Code that evaluates to the
	// `func(snmp.VarBind) (T, error)` closure used by [snmp.NewColumn]
	// or returned to a scalar accessor.
	DecodeFunc func() *jen.Statement
	// Variant is the snmp.<Variant>Var concrete type the natural
	// decoder type-asserts against. Empty when DecodeFunc delegates
	// fully to a TC helper (e.g. snmp.DecodeMacAddress).
	Variant string
	// ZeroExpr is the Go expression for the zero value of GoType used
	// when the decoder returns an error.
	ZeroExpr func() *jen.Statement
	// RawFuse names the snmp.Raw* fused decoder whose strict-tag fast
	// path decodes this column's expected wire form directly to the
	// numeric base of GoType. Empty when no fused arm applies —
	// TC-mediated, byte-valued, OID-valued, and override resolutions
	// always take the generic Decode path. The fused arm is a strict
	// subset of the generic decoder: any tag or range the primitive
	// declines falls back to Decode, so semantics never diverge.
	RawFuse string
}

// resolveType maps a gosmi node's [SmiType] to a [resolved] descriptor
// that the scalar/column emitters consume. Overrides for the node's
// OID short-circuit the natural resolution and force a caller-supplied
// Go type with a fall-through decoder.
//
// The well-known textual conventions (MacAddress, DateAndTime,
// TruthValue, RowStatus, DisplayString, PhysAddress, BITS) route
// through the per-TC helper in `common/snmp/tc.go` so the generated
// closure is just a thin wrapper.
//
// Enum types defined by `INTEGER { name(value), ... }` resolve to the
// generated Go enum type registered in [emitCtx.enumNames].
func resolveType(ec *emitCtx, n gosmi.SmiNode) resolved {
	oid := oidString(n.Oid)
	if ov, ok := ec.overrides[oid]; ok {
		return resolveOverride(ec, n, ov)
	}

	t := n.Type
	if t == nil {
		// No type info — fall back to OctetString-ish bytes. Real
		// MIB nodes always have a type; emitting bytes here makes
		// the generator robust against ill-formed input rather
		// than panicking.
		return resolvedBytes()
	}

	// Well-known TC dispatch. SMIv2 textual conventions are matched
	// by name (case-sensitive — RFC 2579 spells them as below).
	if dec, ok := wellKnownTC(t.Name); ok {
		return dec
	}

	// SMI base-type aliases. libsmi normalises Counter32 / Counter64 /
	// Gauge32 / TimeTicks / IpAddress / Opaque to their integer base
	// types but loses the wire-Kind distinction. We pick the right
	// snmp.Kind here so generated callers see the right exception
	// variant in diagnostics and the dispatch table records the right
	// VarBind variant for downstream decoders.
	if dec, ok := smiBaseAlias(t.Name); ok {
		return dec
	}

	// Inline-INTEGER enums on an OBJECT-TYPE leave the node's Type.Name
	// empty (libsmi flattens them); a generated enum is keyed off the
	// node name and registered in enumNames during the enum pass.
	if t.BaseType == gosmitypes.BaseTypeEnum {
		if id, ok := ec.enumNames[enumKey(n, t)]; ok {
			return enumResolved(jen.Id(id), id)
		}
		// Cross-module enum: the type lives in another loaded MIB
		// module. We look up the owning module on the underlying
		// SmiType and emit a Qual reference into its generated Go
		// package.
		if t.Name != "" && t.Name != "Enumeration" {
			if qual := ec.crossModuleQual(n, t.Name); qual != nil {
				return enumResolved(qual, camelCase(t.Name))
			}
		}
		// Enum wasn't registered (unlikely): degrade to int32 so the
		// emitted file still compiles.
		return resolveBase(gosmitypes.BaseTypeInteger32)
	}

	return resolveBase(t.BaseType)
}

// enumResolved is the canonical resolved descriptor for any enum-like
// type — the type's Integer32 wire kind, a decoder that casts the
// inner value to the enum, and a zero expression of `T(0)`.
func enumResolved(goType *jen.Statement, _ string) resolved {
	return resolved{
		GoType:     goType.Clone(),
		Kind:       jen.Qual(snmpImport, "KindInteger32"),
		Variant:    "Integer32Var",
		DecodeFunc: func() *jen.Statement { return decodeIntCast("Integer32Var", goType.Clone()) },
		ZeroExpr:   func() *jen.Statement { return goType.Clone().Call(jen.Lit(0)) },
		RawFuse:    "RawInteger32",
	}
}

// crossModuleQual builds a jen.Qual referring to a type defined in
// another loaded MIB module. Returns nil when the type cannot be
// resolved (no module found, type's home module is not in our config)
// so the caller can fall back to a less-precise resolution.
//
// Cross-module enum references are only emitted when the home module
// is explicitly listed in mibgen.yaml. Modules that gosmi auto-loaded
// via transitive IMPORTS but that the config doesn't name (e.g. an
// IANA-* registry MIB) are out of scope for binding emission, so the
// referring scalar/column falls back to its natural base type plus a
// generator comment.
func (ec *emitCtx) crossModuleQual(_ gosmi.SmiNode, typeName string) *jen.Statement {
	t, err := gosmi.GetType(typeName)
	if err != nil {
		return nil
	}
	mod := t.GetModule()
	if mod.Name == "" || mod.Name == ec.mod.Name {
		return nil
	}
	if ec.cfgByName == nil {
		return nil
	}
	cm, ok := ec.cfgByName[mod.Name]
	if !ok || cm.Package == "" {
		return nil
	}
	importPath := ec.pkgPrefix + "/" + cm.Package
	return jen.Qual(importPath, camelCase(typeName))
}

// smiBaseAlias maps SMIv2 base-type names that share an underlying
// libsmi BaseType but carry distinct wire kinds (and therefore distinct
// VarBind variants) to their generated Go type + decoder.
func smiBaseAlias(name string) (resolved, bool) {
	switch name {
	case "Counter32":
		zero := func() *jen.Statement { return jen.Lit(0) }
		return resolved{
			GoType:     jen.Uint32(),
			Kind:       jen.Qual(snmpImport, "KindCounter32"),
			Variant:    "Counter32Var",
			DecodeFunc: func() *jen.Statement { return decodeNatural("Counter32Var", jen.Uint32()) },
			ZeroExpr:   zero,
			RawFuse:    "RawCounter32",
		}, true
	case "Counter64":
		zero := func() *jen.Statement { return jen.Lit(0) }
		return resolved{
			GoType:     jen.Uint64(),
			Kind:       jen.Qual(snmpImport, "KindCounter64"),
			Variant:    "Counter64Var",
			DecodeFunc: func() *jen.Statement { return decodeNatural("Counter64Var", jen.Uint64()) },
			ZeroExpr:   zero,
			RawFuse:    "RawCounter64",
		}, true
	case "Gauge32":
		zero := func() *jen.Statement { return jen.Lit(0) }
		return resolved{
			GoType:     jen.Uint32(),
			Kind:       jen.Qual(snmpImport, "KindGauge32"),
			Variant:    "Gauge32Var",
			DecodeFunc: func() *jen.Statement { return decodeNatural("Gauge32Var", jen.Uint32()) },
			ZeroExpr:   zero,
			RawFuse:    "RawGauge32",
		}, true
	case "TimeTicks":
		zero := func() *jen.Statement { return jen.Lit(0) }
		return resolved{
			GoType:     jen.Uint32(),
			Kind:       jen.Qual(snmpImport, "KindTimeTicks"),
			Variant:    "TimeTicksVar",
			DecodeFunc: func() *jen.Statement { return decodeNatural("TimeTicksVar", jen.Uint32()) },
			ZeroExpr:   zero,
			RawFuse:    "RawTimeTicks",
		}, true
	case "IpAddress":
		zero := jen.Nil
		return resolved{
			GoType:     jen.Qual("net", "IP"),
			Kind:       jen.Qual(snmpImport, "KindIPAddress"),
			Variant:    "IPAddressVar",
			DecodeFunc: func() *jen.Statement { return decodeNatural("IPAddressVar", jen.Qual("net", "IP")) },
			ZeroExpr:   zero,
		}, true
	case "Opaque":
		zero := jen.Nil
		return resolved{
			GoType:     jen.Index().Byte(),
			Kind:       jen.Qual(snmpImport, "KindOpaque"),
			Variant:    "OpaqueVar",
			DecodeFunc: func() *jen.Statement { return decodeNatural("OpaqueVar", jen.Index().Byte()) },
			ZeroExpr:   zero,
		}, true
	}
	return resolved{}, false
}

// resolveOverride applies a per-OID Go-type override from the YAML
// config. The override's Go type is used verbatim; the wire decoding
// stays based on the natural base type so the closure can still type-
// assert the right VarBind variant.
func resolveOverride(_ *emitCtx, n gosmi.SmiNode, ov Override) resolved {
	// Natural variant resolution mirrors resolveType: smiBaseAlias
	// (Counter32/64, Gauge32, TimeTicks, IpAddress, Opaque — names
	// libsmi normalises to a shared integer BaseType) takes precedence
	// over the bare BaseType dispatch. Without this chain, an override
	// on a Counter64 / Gauge32 / TimeTicks / IpAddress / Opaque column
	// would fall through to BaseTypeInteger32 and emit DecodeInt32 —
	// silently rejecting the agent's natural emission.
	var nat resolved
	if n.Type != nil {
		if alias, ok := smiBaseAlias(n.Type.Name); ok {
			nat = alias
		} else {
			nat = resolveBase(n.Type.BaseType)
		}
	} else {
		nat = resolveBase(gosmitypes.BaseTypeUnknown)
	}

	// Overrides currently only support integer-castable target types:
	// the emitted decoder is `return Target(v), nil` where v is the
	// helper's success value. For Integer32 / Uinteger32 / Counter32 /
	// Counter64 / Gauge32 / TimeTicks natural variants, v is a numeric
	// type and `Target(v)` is well-defined for any numeric override
	// alias. For structural variants (OctetString / ObjectID / IP /
	// Opaque), v is a non-numeric type and `Target(v)` only compiles
	// when Target is a matching alias (`type MyBytes []byte`); the
	// `Target(0)` zero expr emitted by decodeIntCast does not compile
	// at all. Surface the limitation as a generator-time panic instead
	// of emitting a file that won't compile.
	if !isIntegerLikeVariant(nat.Variant) {
		panic("resolveOverride: override on non-integer column " + n.Name +
			" (natural variant " + nat.Variant + ") is not supported; " +
			"add a wellKnownTC entry or extend the override emitter")
	}

	// goTypeCode emits either a bare Id (same package) or a Qual when
	// Import is non-empty.
	goTypeCode := func() *jen.Statement {
		if ov.Import != "" {
			// Strip package selector if present in GoType: "pkg.Name"
			// resolves the selector portion against ov.Import.
			name := ov.GoType
			if idx := strings.LastIndex(name, "."); idx >= 0 {
				name = name[idx+1:]
			}
			return jen.Qual(ov.Import, name)
		}
		return jen.Id(ov.GoType)
	}

	return resolved{
		GoType:     goTypeCode(),
		Kind:       nat.Kind,
		Variant:    nat.Variant,
		DecodeFunc: func() *jen.Statement { return decodeIntCast(nat.Variant, goTypeCode()) },
		ZeroExpr:   func() *jen.Statement { return goTypeCode().Call(jen.Lit(0)) },
	}
}

// isIntegerLikeVariant reports whether variant carries a numeric Value
// field that can be cast to any numeric Go type via Target(v). This is
// the support boundary for [resolveOverride] today.
func isIntegerLikeVariant(variant string) bool {
	switch variant {
	case "Integer32Var", "Uinteger32Var", "Counter32Var", "Counter64Var",
		"Gauge32Var", "TimeTicksVar":
		return true
	}
	return false
}

// resolveBase covers the plain SMIv2 base types (no TC, no enum).
func resolveBase(bt gosmitypes.BaseType) resolved {
	switch bt {
	case gosmitypes.BaseTypeInteger32:
		zero := func() *jen.Statement { return jen.Lit(0) }
		return resolved{
			GoType:     jen.Int32(),
			Kind:       jen.Qual(snmpImport, "KindInteger32"),
			Variant:    "Integer32Var",
			DecodeFunc: func() *jen.Statement { return decodeNatural("Integer32Var", jen.Int32()) },
			ZeroExpr:   zero,
			RawFuse:    "RawInteger32",
		}
	case gosmitypes.BaseTypeUnsigned32:
		zero := func() *jen.Statement { return jen.Lit(0) }
		return resolved{
			GoType:     jen.Uint32(),
			Kind:       jen.Qual(snmpImport, "KindUinteger32"),
			Variant:    "Uinteger32Var",
			DecodeFunc: func() *jen.Statement { return decodeNatural("Uinteger32Var", jen.Uint32()) },
			ZeroExpr:   zero,
			RawFuse:    "RawGauge32",
		}
	case gosmitypes.BaseTypeOctetString, gosmitypes.BaseTypeBits:
		return resolvedBytes()
	case gosmitypes.BaseTypeObjectIdentifier:
		zero := func() *jen.Statement { return jen.Qual(snmpImport, "OID").Values() }
		return resolved{
			GoType:     jen.Qual(snmpImport, "OID"),
			Kind:       jen.Qual(snmpImport, "KindObjectID"),
			Variant:    "ObjectIDVar",
			DecodeFunc: func() *jen.Statement { return decodeNatural("ObjectIDVar", jen.Qual(snmpImport, "OID")) },
			ZeroExpr:   zero,
		}
	}
	// Default — treat as Integer32 so the file still compiles. The
	// fallback is documented behavior, not silent loss: the generator
	// emits the natural Integer32Var decoder which will surface a
	// mismatched-kind error at runtime if the wire type differs.
	return resolveBase(gosmitypes.BaseTypeInteger32)
}

// resolvedBytes returns the canonical resolution for OCTET STRING-ish
// types: []byte payload, OctetString wire kind, copy-on-decode. The
// defensive-copy semantic now lives inside snmp.DecodeBytes so it is
// guaranteed for every caller, not just the column decoders the
// generator emits.
func resolvedBytes() resolved {
	zero := jen.Nil
	return resolved{
		GoType:     jen.Index().Byte(),
		Kind:       jen.Qual(snmpImport, "KindOctetString"),
		Variant:    "OctetStringVar",
		DecodeFunc: func() *jen.Statement { return decodeNatural("OctetStringVar", jen.Index().Byte()) },
		ZeroExpr:   zero,
	}
}

// wellKnownTC implements the well-known textual-convention dispatch.
func wellKnownTC(name string) (resolved, bool) {
	switch name {
	case "MacAddress":
		return tcDelegate(jen.Qual("net", "HardwareAddr"), "DecodeMacAddress", "OctetStringVar", "KindOctetString", jen.Nil), true
	case "PhysAddress":
		return tcDelegate(jen.Index().Byte(), "DecodePhysAddress", "OctetStringVar", "KindOctetString", jen.Nil), true
	case "DateAndTime":
		return tcDelegate(jen.Qual("time", "Time"), "DecodeDateAndTime", "OctetStringVar", "KindOctetString", func() *jen.Statement { return jen.Qual("time", "Time").Values() }), true
	case "TruthValue":
		return tcDelegate(jen.Bool(), "DecodeTruthValue", "Integer32Var", "KindInteger32", jen.False), true
	case "RowStatus":
		return tcDelegate(jen.Qual(snmpImport, "RowStatus"), "DecodeRowStatus", "Integer32Var", "KindInteger32", func() *jen.Statement { return jen.Qual(snmpImport, "RowStatus").Call(jen.Lit(0)) }), true
	case "DisplayString":
		return tcDelegate(jen.String(), "DecodeDisplayString", "OctetStringVar", "KindOctetString", func() *jen.Statement { return jen.Lit("") }), true
	case "BITS":
		return tcDelegate(jen.Index().Byte(), "DecodeBITS", "OctetStringVar", "KindOctetString", jen.Nil), true
	}
	return resolved{}, false
}

// tcDelegate builds a resolved that calls snmp.<helper> from the
// emitted closure. The closure body is a single-line call.
func tcDelegate(goType *jen.Statement, helper, variant, kindName string, zero func() *jen.Statement) resolved {
	return resolved{
		GoType:  goType.Clone(),
		Kind:    jen.Qual(snmpImport, kindName),
		Variant: variant,
		DecodeFunc: func() *jen.Statement {
			return jen.Func().Params(jen.Id("vb").Qual(snmpImport, "VarBind")).Params(goType.Clone(), jen.Error()).Block(
				jen.Return(jen.Qual(snmpImport, helper).Call(jen.Id("vb"))),
			)
		},
		ZeroExpr: zero,
	}
}

// decoderHelperFor maps a wire-variant short name (e.g. "Counter32Var")
// onto the snmp package leniency helper that decodes any variant in its
// accept set into the target Go type. The mapping is one-to-one with
// the per-SMI-base resolutions emitted above, so the generator picks
// the right helper at codegen time and the runtime gets a single
// one-line `snmp.Decode<T>(vb)` call instead of an inlined type-switch.
//
// The override dispatch in [resolveOverride] consults this table on the
// natural-resolved variant (nat.Variant) — so an override on a Gauge32
// column emits snmp.DecodeUint32, not snmp.DecodeInt32, preserving
// the column's wire semantics regardless of the Go-side override type.
func decoderHelperFor(variant string) (helper string, ok bool) {
	switch variant {
	case "Integer32Var":
		return "DecodeInt32", true
	case "Uinteger32Var", "Counter32Var", "Gauge32Var", "TimeTicksVar":
		return "DecodeUint32", true
	case "Counter64Var":
		return "DecodeUint64", true
	case "OctetStringVar", "OpaqueVar":
		return "DecodeBytes", true
	case "ObjectIDVar":
		return "DecodeOID", true
	case "IPAddressVar":
		return "DecodeIP", true
	}
	return "", false
}

// decodeNatural builds the generated decoder closure for the simple
// base types (Integer32, Counter32, …). It delegates to the matching
// snmp.Decode<T> leniency helper picked by [decoderHelperFor], so the
// emitted body is a single `return snmp.Decode<T>(vb)` line. The
// helper enforces the variant's coercion rules at runtime — see
// common/snmp/decode.go for the policy table.
func decodeNatural(variant string, goType *jen.Statement) *jen.Statement {
	helper, ok := decoderHelperFor(variant)
	if !ok {
		// The emitter only invokes decodeNatural for variants we
		// resolve from SMI base types — an unknown variant here is a
		// codegen bug, not user input. Surface it at generator time.
		panic("decodeNatural: no leniency helper registered for variant " + variant)
	}
	return jen.Func().Params(jen.Id("vb").Qual(snmpImport, "VarBind")).Params(goType.Clone(), jen.Error()).Block(
		jen.Return(jen.Qual(snmpImport, helper).Call(jen.Id("vb"))),
	)
}

// decodeIntCast is decodeNatural specialised for cast-to-target types
// (enums, RowStatus, override-aliases). The emitted body calls the
// matching leniency helper, propagates the helper's error verbatim,
// and casts the helper's success value to the target Go type:
//
//	v, err := snmp.Decode<T>(vb)
//	if err != nil { return Target(0), err }
//	return Target(v), nil
//
// variant drives the helper selection — enums always pass
// "Integer32Var" (so DecodeInt32 fires), but [resolveOverride] passes
// whatever variant the natural-resolved SMI base type produced. That
// matters: an override on a Gauge32 column routes through
// DecodeUint32, preserving the column's wire semantics. A naive
// single-helper dispatch would emit Integer32 semantics for unsigned
// columns and reject the agent's natural Gauge32 emission as a type
// mismatch.
func decodeIntCast(variant string, target *jen.Statement) *jen.Statement {
	helper, ok := decoderHelperFor(variant)
	if !ok {
		panic("decodeIntCast: no leniency helper registered for variant " + variant)
	}
	return jen.Func().Params(jen.Id("vb").Qual(snmpImport, "VarBind")).Params(target.Clone(), jen.Error()).Block(
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Qual(snmpImport, helper).Call(jen.Id("vb")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(target.Clone().Call(jen.Lit(0)), jen.Id("err")),
		),
		jen.Return(target.Clone().Call(jen.Id("v")), jen.Nil()),
	)
}

// camelCase converts an SMI identifier to UpperCamelCase for use as a
// Go identifier. SMI identifiers are conventionally lowerCamelCase per
// RFC 2578 §3.1, but real-world MIBs (including the IANA registries)
// occasionally include hyphens and digits adjacent to letters. We
// strip hyphens / underscores and up-case the following character so
// that names like "if-gsn" emerge as "IfGsn". Digits are preserved in
// place and never start the identifier — if the first rune is a digit
// the result is prefixed with "_" to remain a valid Go identifier.
func camelCase(name string) string {
	if name == "" {
		return ""
	}
	var out []rune
	upperNext := true
	for _, r := range name {
		switch {
		case r == '-' || r == '_' || r == ' ':
			upperNext = true
			continue
		case upperNext:
			out = append(out, unicode.ToUpper(r))
			upperNext = false
		default:
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "_"
	}
	if unicode.IsDigit(out[0]) {
		out = append([]rune{'_'}, out...)
	}
	return string(out)
}

// enumKey is the de-duplication key used by [emitCtx.enumNames]. Named
// SMI types share their declaration across modules; inline anonymous
// enums on a node are unique per node. libsmi gives inline enums the
// synthetic name "Enumeration"; treat that the same as anonymous so
// each declaring node owns its own Go type.
func enumKey(n gosmi.SmiNode, t *gosmimodels.Type) string {
	if t != nil && t.Name != "" && t.Name != "Enumeration" {
		return "type:" + t.Name
	}
	return "node:" + n.Name
}
