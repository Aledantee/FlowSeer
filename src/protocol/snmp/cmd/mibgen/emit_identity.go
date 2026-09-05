package main

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// identityPackage is the Go package name and output directory of the
// sysObjectID identity table. It is fixed rather than configured because
// there is exactly one table per generated tree.
const identityPackage = "sysobjectid"

// identityDescriptionCap bounds the description carried per entry so a
// vendor MIB with a paragraph of prose under every product node does not
// make the table a multi-megabyte file.
const identityDescriptionCap = 240

// enterprisesOID is 1.3.6.1.4.1, the subtree every vendor registers
// product identities under. Nodes strictly below it are collected; the
// node itself is not an identity.
var enterprisesOID = smi.NewOID(1, 3, 6, 1, 4, 1)

// identityEntry is one naming node of the identity table before
// rendering.
type identityEntry struct {
	OID         smi.OID
	Name        string
	Module      string
	Description string
}

// identityReport is what the identity pass emitted, for the run summary.
type identityReport struct {
	// Nodes is the number of entries in the table.
	Nodes int
	// Modules is the number of configured modules that contributed at
	// least one entry.
	Modules int
}

// collectIdentity gathers every naming node under the enterprises subtree
// from every configured module, sorted by OID, with one entry per OID.
//
// When two modules declare the same OID the entry from the module first
// in name order is kept. Name order rather than config order because a
// reordering of mibgen.yaml, which changes nothing else in the output,
// should not change which vendor a device resolves to.
func collectIdentity(cfg *Config, set *smi.ModuleSet) []identityEntry {
	names := make([]string, 0, len(cfg.Modules))
	for _, cm := range cfg.Modules {
		names = append(names, cm.Name)
	}
	sort.Strings(names)

	byOID := make(map[string]identityEntry)
	for _, name := range names {
		mod, ok := set.Module(name)
		if !ok {
			continue
		}
		for _, n := range mod.Nodes {
			if n.Kind != smi.NodeNode || n.OID.Len() <= enterprisesOID.Len() || !n.OID.HasPrefix(enterprisesOID) {
				continue
			}
			key := n.OID.String()
			if _, taken := byOID[key]; taken {
				continue
			}
			byOID[key] = identityEntry{
				OID:         n.OID,
				Name:        n.Name,
				Module:      mod.Name,
				Description: trimIdentityDescription(n.Description),
			}
		}
	}

	entries := make([]identityEntry, 0, len(byOID))
	for _, e := range byOID {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].OID.Compare(entries[j].OID) < 0 })

	return entries
}

// paragraphBreak matches the first blank line of a DESCRIPTION clause. A
// continuation line in a MIB is indented, so a blank line is one that
// holds only whitespace.
var paragraphBreak = regexp.MustCompile(`\n[ \t\r]*\n`)

// trimIdentityDescription keeps the first paragraph of a DESCRIPTION,
// collapses its whitespace, and cuts it to [identityDescriptionCap] at a
// word boundary. A single word longer than the cap is cut mid-word so
// the cap holds regardless of input.
func trimIdentityDescription(s string) string {
	if loc := paragraphBreak.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= identityDescriptionCap {
		return s
	}
	cut := strings.LastIndexByte(s[:identityDescriptionCap+1], ' ')
	if cut <= 0 {
		cut = identityDescriptionCap
	}

	return strings.TrimRight(s[:cut], " ")
}

// emitIdentity renders the identity package and writes it under
// outDir/sysobjectid/mib.go.
func emitIdentity(cfg *Config, set *smi.ModuleSet, outDir, pkgPrefix string) (identityReport, error) {
	out, report, err := renderIdentity(cfg, set, pkgPrefix)
	if err != nil {
		return identityReport{}, err
	}

	if err := writeGeneratedPackage(outDir, identityPackage, out); err != nil {
		return identityReport{}, err
	}

	return report, nil
}

// renderIdentity produces the identity package source: an Entry type, the
// sorted table, and a longest-prefix Lookup over [snmp.OIDSet]. The
// package imports the SNMP library and nothing else, so it can be linked
// by a consumer that has no use for any per-module package.
func renderIdentity(cfg *Config, set *smi.ModuleSet, pkgPrefix string) ([]byte, identityReport, error) {
	entries := collectIdentity(cfg, set)

	contributing := map[string]struct{}{}
	for _, e := range entries {
		contributing[e.Module] = struct{}{}
	}
	sources := make([]string, 0, len(contributing))
	for name := range contributing {
		sources = append(sources, name)
	}
	sort.Strings(sources)
	report := identityReport{Nodes: len(entries), Modules: len(sources)}

	f := jen.NewFilePathName(pkgPrefix+"/"+identityPackage, identityPackage)
	writeIdentityHeader(f, sources)
	f.PackageComment("Package " + identityPackage + " resolves a sysObjectID to the deepest naming node the")
	f.PackageComment("configured MIB modules declare under the enterprises subtree.")

	f.Comment("Entry is one naming node of the identity table.")
	f.Type().Id("Entry").Struct(
		jen.Comment("OID is the node's object identifier."),
		jen.Id("OID").Qual(snmpImport, "OID"),
		jen.Comment("Name is the descriptor the MIB declares the node under."),
		jen.Id("Name").String(),
		jen.Comment("Module is the MIB module that declares the node."),
		jen.Id("Module").String(),
		jen.Comment("Description is the first paragraph of the node's DESCRIPTION,"),
		jen.Comment("capped in length, or empty for a plain OID assignment."),
		jen.Id("Description").String(),
	)

	f.Comment("entries is the table in OID order, one entry per OID.")
	f.Var().Id("entries").Op("=").Index().Id("Entry").ValuesFunc(func(g *jen.Group) {
		for _, e := range entries {
			fields := []jen.Code{
				jen.Id("OID").Op(":").Add(newOIDCall(e.OID.String())),
				jen.Id("Name").Op(":").Lit(e.Name),
				jen.Id("Module").Op(":").Lit(e.Module),
			}
			if e.Description != "" {
				fields = append(fields, jen.Id("Description").Op(":").Lit(e.Description))
			}
			g.Line().Values(fields...)
		}
		if len(entries) > 0 {
			g.Line()
		}
	})

	f.Comment("oids is the set the longest-prefix lookup searches.")
	f.Var().Id("oids").Op("=").Func().Params().Qual(snmpImport, "OIDSet").Block(
		jen.Id("list").Op(":=").Make(jen.Index().Qual(snmpImport, "OID"), jen.Len(jen.Id("entries"))),
		jen.For(jen.List(jen.Id("i"), jen.Id("e")).Op(":=").Range().Id("entries")).Block(
			jen.Id("list").Index(jen.Id("i")).Op("=").Id("e").Dot("OID"),
		),
		jen.Return(jen.Qual(snmpImport, "NewOIDSet").Call(jen.Id("list"))),
	).Call()

	f.Comment("byKey indexes entries by [snmp.OID.WireKey] for exact lookup.")
	f.Var().Id("byKey").Op("=").Func().Params().Map(jen.String()).Id("Entry").Block(
		jen.Id("m").Op(":=").Make(jen.Map(jen.String()).Id("Entry"), jen.Len(jen.Id("entries"))),
		jen.For(jen.List(jen.Id("_"), jen.Id("e")).Op(":=").Range().Id("entries")).Block(
			jen.Id("m").Index(jen.Id("e").Dot("OID").Dot("WireKey").Call()).Op("=").Id("e"),
		),
		jen.Return(jen.Id("m")),
	).Call()

	f.Comment("Lookup returns the deepest entry whose OID is a prefix of oid, which")
	f.Comment("is the entry at oid itself when one is declared, and false when no")
	f.Comment("configured module declares a node above oid.")
	f.Func().Id("Lookup").Params(jen.Id("oid").Qual(snmpImport, "OID")).Params(jen.Id("Entry"), jen.Bool()).Block(
		jen.List(jen.Id("member"), jen.Id("ok")).Op(":=").Id("oids").Dot("Longest").Call(jen.Id("oid")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Id("Entry").Values(), jen.False()),
		),
		jen.Return(jen.Id("byKey").Index(jen.Id("member").Dot("WireKey").Call()), jen.True()),
	)

	f.Comment("Exact returns the entry declared at oid itself, and false when none is.")
	f.Func().Id("Exact").Params(jen.Id("oid").Qual(snmpImport, "OID")).Params(jen.Id("Entry"), jen.Bool()).Block(
		jen.List(jen.Id("e"), jen.Id("ok")).Op(":=").Id("byKey").Index(jen.Id("oid").Dot("WireKey").Call()),
		jen.Return(jen.Id("e"), jen.Id("ok")),
	)

	f.Comment("Entries returns a copy of the table in OID order.")
	f.Func().Id("Entries").Params().Index().Id("Entry").Block(
		jen.Id("out").Op(":=").Make(jen.Index().Id("Entry"), jen.Len(jen.Id("entries"))),
		jen.Copy(jen.Id("out"), jen.Id("entries")),
		jen.Return(jen.Id("out")),
	)

	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		return nil, identityReport{}, errs.Wrap(err, "render identity")
	}
	formatted, err := formatGenerated(identityPackage+"/mib.go", buf.Bytes())
	if err != nil {
		return nil, identityReport{}, err
	}

	return formatted, report, nil
}

// writeIdentityHeader writes the `Code generated` banner for the identity
// package. Unlike the per-module banner it names every contributing
// module rather than one source path and hash: the table is a projection
// of many files, and the per-module packages already pin each hash.
func writeIdentityHeader(f *jen.File, sources []string) {
	list := strings.Join(sources, ", ")
	if list == "" {
		list = "(none)"
	}
	f.HeaderComment("// Code generated by mibgen; DO NOT EDIT.\n" +
		"//\n" +
		"// Source MIBs:   " + list + "\n" +
		"//\n" +
		"// Regenerate with `go generate .` at the repository root.")
}
