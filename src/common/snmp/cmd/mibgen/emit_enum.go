package main

import (
	"sort"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/common/smi"
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
func emitEnums(f *jen.File, ec *emitCtx, mod *smi.Module, nodes []*smi.Node) {
	type enumDecl struct {
		Key     string // dedup key (enumKey)
		GoName  string
		MIBName string // diagnostic-only
		Comment string
		Values  []enumMember
	}

	enums := make(map[string]*enumDecl)
	var order []string

	// Named types first — the textual conventions and type assignments
	// the module declares. Well-known TCs are excluded because they
	// have a dedicated decoder path that decides their Go shape.
	for _, t := range mod.Types {
		if !t.Enumerated() {
			continue
		}
		if _, wellKnown := wellKnownTC(t.Name); wellKnown {
			continue
		}
		if t.Name == "" || len(t.Members) == 0 {
			continue
		}
		key := "type:" + t.Name
		if _, ok := enums[key]; ok {
			continue
		}
		decl := &enumDecl{
			Key:     key,
			GoName:  camelCase(t.Name),
			MIBName: t.Name,
			Comment: t.Description,
			Values:  collectEnumMembers(t.Members, camelCase(t.Name)),
		}
		enums[key] = decl
		order = append(order, key)
		ec.enumNames[key] = decl.GoName
	}

	// Enumerations written inline in an object's SYNTAX clause. The MIB
	// gave them no name, so the declaring object supplies one and each
	// object owns its own Go type. A named type an object merely refers
	// to was picked up by the pass above, or is resolved cross-module by
	// resolveType.
	for _, n := range nodes {
		if n.Kind != smi.NodeScalar && n.Kind != smi.NodeColumn {
			continue
		}
		if n.Type == nil || n.Type.Name != "" || !n.Type.Enumerated() {
			continue
		}
		if len(n.Type.Members) == 0 {
			continue
		}
		key := "node:" + n.Name
		if _, ok := enums[key]; ok {
			continue
		}
		decl := &enumDecl{
			Key:     key,
			GoName:  camelCase(n.Name) + "Value",
			MIBName: n.Name + " (inline)",
			Comment: n.Description,
			Values:  collectEnumMembers(n.Type.Members, camelCase(n.Name)+"Value"),
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

// collectEnumMembers builds the enumMember slice for one type, sorted
// by value so the constant block reads in numeric order whatever order
// the MIB declared the members in.
//
// The numbers are the ones the MIB wrote. That matters most for a BITS
// type: a device reporting bit 5 of a gapped BITS means the member
// declared as 5, and inferring a member's number from its position in
// the declaration would decode that as whichever member happens to sit
// fifth.
func collectEnumMembers(in []smi.Member, prefix string) []enumMember {
	out := make([]enumMember, 0, len(in))
	for _, v := range in {
		out = append(out, enumMember{
			GoName:  prefix + camelCase(v.Name),
			MIBName: v.Name,
			Value:   v.Number,
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
	f.Comment("")
	f.Comment("Values outside the named constants are preserved. Concurrent reads are safe;")
	f.Comment("callers must synchronize writes to a shared value.")
	f.Type().Id(goName).Int32()

	f.Const().DefsFunc(func(g *jen.Group) {
		for _, m := range values {
			g.Comment(m.GoName + " represents the SMI value " + m.MIBName + ".")
			g.Id(m.GoName).Id(goName).Op("=").Lit(int(m.Value))
		}
	})

	f.Comment("String returns the SMI label, or " + goName + "(n) for an unrecognized value n.")
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
