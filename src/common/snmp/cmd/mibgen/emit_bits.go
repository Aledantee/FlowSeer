package main

import (
	"sort"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/common/smi"
)

// emitBitsConsts writes one named [snmp.BitPos] constant per member of
// every BITS type the module declares:
//
//	const (
//	    LldpSystemCapabilitiesMapOther  snmp.BitPos = 0
//	    LldpSystemCapabilitiesMapBridge snmp.BitPos = 2
//	    …
//	)
//
// The values themselves decode to [snmp.BitSet] (see resolvedBitSet),
// so these constants are what a caller passes to BitSet.Has. Positions
// the MIB names are the only ones that get a constant; a bit the MIB
// left unnamed still round-trips through the set.
//
// A constant carries the number the MIB wrote rather than the member's
// place in the declaration, because a set bit an agent reports is a
// position: a type numbering its members 0, 2, 4 has nothing to say
// about position 1.
//
// An inline `SYNTAX BITS {…}` on a scalar or column is named too. The
// declaring object owns those names, suffixed so they cannot collide
// with the object's own identifier, which is the same shape the inline
// enum case uses.
func emitBitsConsts(f *jen.File, mod *smi.Module) {
	type bitsDecl struct {
		GoName  string // constant-name prefix
		MIBName string
		Comment string
		Members []smi.Member
	}

	decls := make(map[string]*bitsDecl)
	var order []string

	add := func(key string, d *bitsDecl) {
		if _, ok := decls[key]; ok {
			return
		}
		decls[key] = d
		order = append(order, key)
	}

	// Named BITS textual conventions.
	for _, t := range mod.Types {
		if !isBitsType(t) || t.Name == "" || len(t.Members) == 0 {
			continue
		}
		add("type:"+t.Name, &bitsDecl{
			GoName:  camelCase(t.Name),
			MIBName: t.Name,
			Comment: t.Description,
			Members: t.Members,
		})
	}

	// Inline `SYNTAX BITS {…}` on a scalar or column. The type has no
	// name of its own, so the declaring object supplies one.
	for _, n := range mod.Nodes {
		if n.Kind != smi.NodeScalar && n.Kind != smi.NodeColumn {
			continue
		}
		if !isBitsType(n.Type) || n.Type.Name != "" || len(n.Type.Members) == 0 {
			continue
		}
		add("node:"+n.Name, &bitsDecl{
			GoName:  camelCase(n.Name) + "Bit",
			MIBName: n.Name + " (inline)",
			Comment: n.Description,
			Members: n.Type.Members,
		})
	}

	if len(order) == 0 {
		return
	}
	sort.SliceStable(order, func(i, j int) bool { return decls[order[i]].GoName < decls[order[j]].GoName })

	for _, k := range order {
		d := decls[k]
		f.Comment(d.GoName + " names the bit positions of the SMI BITS type " + d.MIBName + ".")
		f.Comment("Pass one to [snmp.BitSet.Has] on a value of this type.")
		for _, line := range splitDoc(d.Comment) {
			f.Comment(line)
		}
		f.Const().DefsFunc(func(g *jen.Group) {
			for _, m := range d.Members {
				g.Id(d.GoName+camelCase(m.Name)).Qual(snmpImport, "BitPos").Op("=").Lit(int(m.Number))
			}
		})
	}
}
