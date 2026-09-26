package main

import (
	"strings"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// emitScalar writes the Get-accessor for a single read-accessible
// SMIv2 scalar. The emitted function is:
//
//	func <Scalar>Get(ctx context.Context, sess snmp.Session) (T, error) {
//	    vbs, err := sess.Get(ctx, []snmp.OID{snmp.MustOID(<subs>...)})
//	    if err != nil { return zero, err }
//	    if len(vbs) == 0 { return zero, errs.Msg("empty Get response") }
//	    return decode(vbs[0])
//	}
//
// The trailing ".0" is the SMIv2 instance suffix for a scalar — the
// generator emits it eagerly so callers don't have to remember.
//
// It returns an error when the scalar's type cannot be resolved,
// leaving nothing written for it.
func emitScalar(f *jen.File, ec *emitCtx, n *smi.Node) error {
	r, err := resolveType(ec, n)
	if err != nil {
		return err
	}

	goName := camelCase(n.Name)
	oidStr := n.OID.String() + ".0"

	// Doc comment block: short headline + the MIB DESCRIPTION
	// reflown to 76-col-ish lines so the rendered file stays
	// readable.
	f.Comment(goName + "Get reads the SMIv2 scalar " + n.Name + ".")
	f.Comment("It returns the session or decode error, or an error if the response is empty.")
	f.Comment("")
	for _, line := range splitDoc(n.Description) {
		f.Comment(line)
	}
	emitDeprecationParagraph(f, n)

	f.Func().Id(goName+"Get").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("sess").Qual(snmpImport, "Session"),
	).Params(r.GoType.Clone(), jen.Error()).BlockFunc(func(g *jen.Group) {
		g.List(jen.Id("vbs"), jen.Err()).Op(":=").Id("sess").Dot("Get").Call(
			jen.Id("ctx"),
			jen.Index().Qual(snmpImport, "OID").Values(newOIDCall(oidStr)),
		)
		g.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(r.ZeroExpr(), jen.Err()),
		)
		g.Line()

		g.If(jen.Len(jen.Id("vbs")).Op("==").Lit(0)).Block(
			jen.Return(r.ZeroExpr(), jen.Qual(errsImport, "Msg").Call(jen.Lit("empty Get response for "+n.Name))),
		)
		g.Line()

		g.Return(r.DecodeFunc().Call(jen.Id("vbs").Index(jen.Lit(0))))
	})

	return nil
}

func emitDeprecationParagraph(f *jen.File, n *smi.Node) {
	if n.Status != smi.StatusDeprecated && n.Status != smi.StatusObsolete {
		return
	}

	f.Comment("")
	f.Comment("Deprecated: " + n.Name + " is STATUS " + n.Status.String() + " in " + n.Module + ".")
}

// splitDoc reflows an SMI DESCRIPTION clause into 1-line comment
// fragments. The input is whitespace-collapsed first (SMI descriptions preserve
// newlines and large indents from the source MIB which would otherwise
// produce noisy comments).
//
// A word starting with "deprecat" in any case is never moved to the start
// of a line; it stays on the line before, past the target width. gocritic's
// deprecatedComment check reads a comment line opening with that word as a
// malformed deprecation notice, and the copied prose must not be reworded.
func splitDoc(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Replace runs of whitespace (including newlines) with a single
	// space. The result is one logical paragraph; we then wrap.
	fields := strings.Fields(s)
	const target = 72
	var lines []string
	var cur strings.Builder
	for _, w := range fields {
		if cur.Len() == 0 {
			cur.WriteString(w)
			continue
		}
		if cur.Len()+1+len(w) > target && !strings.HasPrefix(strings.ToLower(w), "deprecat") {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
			continue
		}
		cur.WriteByte(' ')
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
