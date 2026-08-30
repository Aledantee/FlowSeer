package main

import (
	"sort"

	"github.com/dave/jennifer/jen"
	"github.com/sleepinggenius2/gosmi"
	gosmimodels "github.com/sleepinggenius2/gosmi/models"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"
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
// Bit positions come from declaration order, not from the numbers
// written in the MIB. libsmi parses `BITS { other(0), bridge(2) }` into
// members whose numeric payload is dropped (its value getter covers
// Integer and Unsigned only), so gosmi hands us every member as zero.
// Declaration order reproduces the written positions for every MIB that
// numbers its bits consecutively from zero, which is the shape RFC 2578
// §7.1.4 recommends and every module in mibgen.yaml follows. A MIB that
// skips positions would need its numbers recovered from the MIB text —
// surface that as a new override rather than guessing here.
func emitBitsConsts(f *jen.File, mod *gosmi.SmiModule, nodes []gosmi.SmiNode) {
	type bitsDecl struct {
		GoName  string // constant-name prefix
		MIBName string
		Comment string
		Members []string // member names in declaration order
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
	for _, t := range mod.GetTypes() {
		if !isBitsType(&t.Type) || t.Enum == nil || len(t.Enum.Values) == 0 {
			continue
		}
		if t.Name == "" || t.Name == "Enumeration" {
			continue
		}
		add("type:"+t.Name, &bitsDecl{
			GoName:  camelCase(t.Name),
			MIBName: t.Name,
			Comment: t.Description,
			Members: memberNames(t.Enum.Values),
		})
	}

	// Inline `SYNTAX BITS {…}` on a scalar or column. Same shape as the
	// inline-enum case in emitEnums: the declaring node owns the names,
	// suffixed so they cannot collide with the object's own identifier.
	for _, n := range nodes {
		if n.Kind != gosmitypes.NodeScalar && n.Kind != gosmitypes.NodeColumn {
			continue
		}
		if !isBitsType(n.Type) || n.Type.Enum == nil || len(n.Type.Enum.Values) == 0 {
			continue
		}
		if n.Type.Name != "" && n.Type.Name != "Enumeration" {
			continue
		}
		add("node:"+n.Name, &bitsDecl{
			GoName:  camelCase(n.Name) + "Bit",
			MIBName: n.Name + " (inline)",
			Comment: n.Description,
			Members: memberNames(n.Type.Enum.Values),
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
			for i, m := range d.Members {
				g.Id(d.GoName+camelCase(m)).Qual(snmpImport, "BitPos").Op("=").Lit(i)
			}
		})
	}
}

// memberNames lists a BITS type's member names in declaration order —
// the order that, per emitBitsConsts, stands in for the bit positions
// libsmi discards.
func memberNames(values []gosmimodels.NamedNumber) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.Name)
	}
	return out
}
