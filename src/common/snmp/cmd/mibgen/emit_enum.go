package main

import (
	"sort"

	"github.com/dave/jennifer/jen"
	"github.com/sleepinggenius2/gosmi"
	gosmimodels "github.com/sleepinggenius2/gosmi/models"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"
)

// emitEnums writes one Go enum declaration per distinct SMI enum
// encountered in the module's types and nodes. Each enum becomes
//
//	type <Name> int32
//	const (
//	    <Name><Member1> <Name> = <value>
//	    ...
//	)
//	func (v <Name>) String() string { ... }
//
// The deduplication key is the SMI type name when present;
// inline anonymous enums declared directly on an OBJECT-TYPE are
// keyed off the node name.
//
// The function records each emitted enum's Go identifier in
// [emitCtx.enumNames] so the second pass (resolveType) can reference
// the enum type by name.
func emitEnums(f *jen.File, ec *emitCtx, mod *gosmi.SmiModule, nodes []gosmi.SmiNode) {
	type enumDecl struct {
		Key      string // dedup key (enumKey)
		GoName   string
		MIBName  string // diagnostic-only
		Comment  string
		BaseKind gosmitypes.BaseType
		Values   []enumMember
	}

	enums := make(map[string]*enumDecl)
	var order []string

	// Named TC enums first — those are the reusable types listed by
	// SmiModule.GetTypes(). We exclude well-known TCs that have a
	// dedicated TC decoder path.
	for _, t := range mod.GetTypes() {
		if t.BaseType != gosmitypes.BaseTypeEnum {
			continue
		}
		if _, wellKnown := wellKnownTC(t.Name); wellKnown {
			// e.g. an extension MIB might re-import RowStatus; the
			// TC handler decides the Go shape.
			continue
		}
		if t.Name == "" || t.Name == "Enumeration" {
			// Inline INTEGER {...} declarations show up in the
			// module's type list with libsmi's "Enumeration"
			// placeholder name; those are handled below per node.
			continue
		}
		if t.Enum == nil || len(t.Enum.Values) == 0 {
			continue
		}
		key := "type:" + t.Name
		if _, ok := enums[key]; ok {
			continue
		}
		decl := &enumDecl{
			Key:      key,
			GoName:   camelCase(t.Name),
			MIBName:  t.Name,
			Comment:  t.Description,
			BaseKind: t.BaseType,
			Values:   collectEnumMembers(t.Enum.Values, camelCase(t.Name)),
		}
		enums[key] = decl
		order = append(order, key)
		ec.enumNames[key] = decl.GoName
	}

	// Inline enums declared directly on a node (the node's Type has no
	// Name). Use the node name as the de-dup key and the enum's Go
	// type name.
	for _, n := range nodes {
		if n.Kind != gosmitypes.NodeScalar && n.Kind != gosmitypes.NodeColumn {
			continue
		}
		if n.Type == nil || n.Type.BaseType != gosmitypes.BaseTypeEnum {
			continue
		}
		// libsmi flattens inline INTEGER {...} enums by giving them the
		// synthetic type name "Enumeration"; treat that as anonymous
		// so we generate one enum per declaring node rather than
		// trying to share a single Go type for every inline enum in
		// the MIB. Genuinely-named TC enums are picked up by the
		// types pass above (when the type's home module is this
		// module) or resolved cross-module via resolveType.
		if n.Type.Name != "" && n.Type.Name != "Enumeration" {
			continue
		}
		if n.Type.Enum == nil || len(n.Type.Enum.Values) == 0 {
			continue
		}
		key := "node:" + n.Name
		if _, ok := enums[key]; ok {
			continue
		}
		decl := &enumDecl{
			Key:      key,
			GoName:   camelCase(n.Name) + "Value",
			MIBName:  n.Name + " (inline)",
			Comment:  n.Description,
			BaseKind: n.Type.BaseType,
			Values:   collectEnumMembers(n.Type.Enum.Values, camelCase(n.Name)+"Value"),
		}
		enums[key] = decl
		order = append(order, key)
		ec.enumNames[key] = decl.GoName
	}

	// Stable output ordering: alphabetical Go name. Iteration order
	// of the map is non-deterministic; the `order` slice was filled
	// in node-OID order which is good enough but two passes contribute
	// to it, so we re-sort to keep the by-name top-of-file shape.
	sort.SliceStable(order, func(i, j int) bool {
		return enums[order[i]].GoName < enums[order[j]].GoName
	})

	for _, k := range order {
		decl := enums[k]
		writeEnumDecl(f, decl.GoName, decl.MIBName, decl.Comment, decl.Values)
	}
}

// enumMember is the rendered shape of one INTEGER {name(value)} pair.
type enumMember struct {
	GoName  string // generated Go identifier (e.g. IfOperStatusUp)
	MIBName string // SMI name as written in the MIB (e.g. "up")
	Value   int64
}

// collectEnumMembers builds enumMember slice from a gosmi Enum, sorted
// by value so the constant block is in numeric order regardless of
// libsmi's parse order.
//
// libsmi exposes BITS members as SmiValues with no underlying int
// payload (its internal/type.go GetValue switch covers Integer/Unsigned
// only); gosmi's convertValue then returns 0 for every member. We
// detect that "all-zero" shape and assign sequential 0..N bit
// positions by declaration order so the generated constants don't
// collide. The fallback is harmless for genuinely-numeric enums
// because they exit the all-zero check immediately.
func collectEnumMembers(in []gosmimodels.NamedNumber, prefix string) []enumMember {
	allZero := true
	for _, v := range in {
		if v.Value != 0 {
			allZero = false
			break
		}
	}
	out := make([]enumMember, 0, len(in))
	for i, v := range in {
		val := v.Value
		if allZero && len(in) > 1 {
			val = int64(i)
		}
		out = append(out, enumMember{
			GoName:  prefix + camelCase(v.Name),
			MIBName: v.Name,
			Value:   val,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// writeEnumDecl renders the type + const block + String method for one
// enum. Inputs are already canonicalised by [emitEnums].
func writeEnumDecl(f *jen.File, goName, mibName, comment string, values []enumMember) {
	if comment != "" {
		f.Comment(goName + " is the SMI enum " + mibName + ".")
		for _, line := range splitDoc(comment) {
			f.Comment(line)
		}
	} else {
		f.Comment(goName + " is the SMI enum " + mibName + ".")
	}
	f.Type().Id(goName).Int32()

	f.Const().DefsFunc(func(g *jen.Group) {
		for _, m := range values {
			g.Id(goName + camelCase(m.MIBName)).Id(goName).Op("=").Lit(int(m.Value))
		}
	})

	f.Func().Params(jen.Id("v").Id(goName)).Id("String").Params().String().BlockFunc(func(g *jen.Group) {
		g.Switch(jen.Id("v")).BlockFunc(func(cg *jen.Group) {
			for _, m := range values {
				cg.Case(jen.Id(goName + camelCase(m.MIBName))).Block(
					jen.Return(jen.Lit(m.MIBName)),
				)
			}
		})
		g.Line()

		g.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit(goName+"(%d)"), jen.Id("v")))
	})
}
