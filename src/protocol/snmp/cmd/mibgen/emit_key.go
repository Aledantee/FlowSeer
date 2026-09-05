package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dave/jennifer/jen"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// typeKey identifies a named type by the module that declares it, since
// two modules may declare conventions of the same name.
type typeKey struct {
	Module string
	Name   string
}

// keyBase is the Go representation a keyed convention is emitted over.
type keyBase uint8

const (
	keyBaseSigned keyBase = iota + 1
	keyBaseUnsigned
	keyBaseString
)

// keyedConvention is a textual convention that solely indexes at least
// one table of its declaring module. Home is the table a value of the
// convention identifies a row of.
type keyedConvention struct {
	Type *smi.Type
	Base keyBase
	Home *smi.Table
}

// degradedRef is a reference the emitter had to render without its key
// type, either because the package that would declare the type is not
// configured or because the module never imported it (see
// [emitCtx.typeAvailable]). Object is the column, index column, or row
// that carries the reference; Convention is the key type it names.
type degradedRef struct {
	Module          string
	Object          string
	Convention      string
	DeclaringModule string
	Reason          string
}

const (
	degradedNotConfigured = "which is not configured"
	degradedNotImported   = "which the module does not import"
)

func (d degradedRef) String() string {
	return fmt.Sprintf("degraded reference: %s %s uses %s declared by %s, %s; emitted without the key type",
		d.Module, d.Object, d.Convention, d.DeclaringModule, d.Reason)
}

// conventionKeyBase reports whether t may become a key type, and over
// which Go base.
//
// The well-known conventions already have a Go shape of their own, an
// enumeration is a type of its own, and BITS is a set; none of them
// identifies a row the way an index number or name does, so a table
// indexed solely by one keeps a plain base-typed key.
func conventionKeyBase(t *smi.Type) (keyBase, bool) {
	if t == nil || t.Name == "" || t.Module == "" || t.Unresolved {
		return 0, false
	}
	if _, wellKnown := wellKnownTC(t.Name); wellKnown {
		return 0, false
	}
	if t.Enumerated() || isBitsType(t) {
		return 0, false
	}
	switch t.Base {
	case smi.BaseInteger, smi.BaseInteger32:
		if dispatchBase(t) == baseUnsigned32 {
			return keyBaseUnsigned, true
		}
		return keyBaseSigned, true
	case smi.BaseUnsigned32:
		return keyBaseUnsigned, true
	case smi.BaseOctetString:
		return keyBaseString, true
	}
	return 0, false
}

// keyedConventions finds every keyed convention in the loaded set, not
// only in the configured modules, so a module referencing a convention
// keyed elsewhere can tell that it is a reference even when the
// declaring package is not generated.
//
// When several tables of the module are solely indexed by the same
// convention, the home table is the one whose index column has the
// shortest name, then the one with the lowest OID. The table that
// describes the keyed entity names its index plainly (ifIndex,
// lldpLocPortNum) while the tables hanging configuration or statistics
// off it qualify theirs (lldpPortConfigPortNum, lldpStatsRxPortNum), and
// a consumer following a key wants the describing table.
func keyedConventions(set *smi.ModuleSet) map[typeKey]keyedConvention {
	out := make(map[typeKey]keyedConvention)
	for _, mod := range set.Modules() {
		for _, t := range mod.Tables {
			if t.Node == nil || t.Augments != "" || len(t.Index) != 1 || t.Index[0].Unresolved {
				continue
			}
			ty := t.Index[0].Type
			base, ok := conventionKeyBase(ty)
			if !ok || ty.Module != mod.Name {
				continue
			}
			k := typeKey{Module: ty.Module, Name: ty.Name}
			if cur, seen := out[k]; seen && !preferHome(t, cur.Home) {
				continue
			}
			out[k] = keyedConvention{Type: ty, Base: base, Home: t}
		}
	}

	return out
}

func preferHome(candidate, current *smi.Table) bool {
	cn, xn := len(candidate.Index[0].Name), len(current.Index[0].Name)
	if cn != xn {
		return cn < xn
	}

	return candidate.Node.OID.Compare(current.Node.OID) < 0
}

// keyedResolved is the decode bundle of a keyed convention: the enum
// bundle for an integer base, and a cast of the decoded octets for a
// string base.
func keyedResolved(goType *jen.Statement, base keyBase) resolved {
	switch base {
	case keyBaseUnsigned:
		return resolved{
			GoType:     goType.Clone(),
			Kind:       jen.Qual(snmpImport, "KindUinteger32"),
			Variant:    "Uinteger32Var",
			DecodeFunc: func() *jen.Statement { return decodeIntCast("Uinteger32Var", goType.Clone()) },
			ZeroExpr:   func() *jen.Statement { return goType.Clone().Call(jen.Lit(0)) },
			RawFuse:    "RawGauge32",
		}
	case keyBaseString:
		zero := func() *jen.Statement { return goType.Clone().Call(jen.Lit("")) }
		return resolved{
			GoType:     goType.Clone(),
			Kind:       jen.Qual(snmpImport, "KindOctetString"),
			Variant:    "OctetStringVar",
			DecodeFunc: func() *jen.Statement { return decodeCast("OctetStringVar", goType.Clone(), zero()) },
			ZeroExpr:   zero,
		}
	default:
		return enumResolved(goType)
	}
}

// keyTypeRef returns the Go type expression of the keyed convention t
// as seen from the module being emitted, or nil when the declaring
// module is not configured, in which case the reference from object is
// recorded as degraded.
func (ec *emitCtx) keyTypeRef(t *smi.Type, object string) *jen.Statement {
	if t.Module == ec.mod.Name {
		return jen.Id(camelCase(t.Name))
	}
	if q := ec.moduleQual(t.Module, camelCase(t.Name)); q != nil {
		return q
	}
	ec.recordDegraded(object, t.Name, t.Module, degradedNotConfigured)

	return nil
}

// recordDegraded notes one degraded reference once, however many passes
// resolve the same object.
func (ec *emitCtx) recordDegraded(object, convention, declaringModule, reason string) {
	ref := degradedRef{Module: ec.mod.Name, Object: object, Convention: convention, DeclaringModule: declaringModule, Reason: reason}
	for _, d := range ec.degraded {
		if d == ref {
			return
		}
	}
	ec.degraded = append(ec.degraded, ref)
}

// emitKeyTypes writes one named type per keyed convention the module
// declares, each with a HomeTable method returning the descriptor of
// the table it keys.
func emitKeyTypes(f *jen.File, ec *emitCtx) {
	var names []string
	for k := range ec.keyed {
		if k.Module == ec.mod.Name {
			names = append(names, k.Name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		kc := ec.keyed[typeKey{Module: ec.mod.Name, Name: name}]
		goName := camelCase(name)
		home := kc.Home.Node.Name

		f.Comment(goName + " is the textual convention " + name + ". A value identifies one row of")
		f.Comment(home + ", and a column of this type in any table refers to that row.")
		for _, line := range splitDoc(kc.Type.Description) {
			f.Comment(line)
		}
		switch kc.Base {
		case keyBaseUnsigned:
			f.Type().Id(goName).Uint32()
		case keyBaseString:
			f.Type().Id(goName).String()
		default:
			f.Type().Id(goName).Int32()
		}

		f.Comment("HomeTable returns the descriptor of " + home + ", the table a " + goName + " value")
		f.Comment("identifies a row of.")
		f.Func().Params(jen.Id(goName)).Id("HomeTable").Params().Qual(snmpImport, "TableDescriptor").Block(
			jen.Return(homeTableDescriptor(kc.Home, goName)),
		)
	}
}

// homeTableDescriptor is the expression HomeTable returns. The home
// table is by construction a table of the module declaring the
// convention (see [keyedConventions]), so its singleton is in the same
// package and the method forwards to its Descriptor rather than
// spelling a second literal that could drift from it. A home table the
// emitter skips, one with no accessible column, has no singleton, and
// the literal names the convention as the key.
func homeTableDescriptor(home *smi.Table, keyName string) *jen.Statement {
	if tableBound(home) {
		return jen.Id(camelCase(home.Node.Name)).Dot("Descriptor").Call()
	}

	return jen.Qual(snmpImport, "TableDescriptor").Values(jen.Dict{
		jen.Id("Root"):    newOIDCall(home.Node.OID.String()),
		jen.Id("KeyType"): jen.Lit(keyName),
	})
}

// tableBound reports whether emitTable writes bindings for t: a row
// with at least one accessible column.
func tableBound(t *smi.Table) bool {
	if t == nil || t.Node == nil || t.Row == nil {
		return false
	}
	for _, c := range t.Columns {
		if c.Access != smi.AccessNotAccessible {
			return true
		}
	}

	return false
}

// rowKey says how a table's row is keyed: by a typed key struct decoded
// from the instance suffix, or, when Type is nil, by the raw suffix.
// TypeName is the key type as the descriptor reports it: the struct's
// name, qualified by package when an augmenting table borrows it from
// another module, or "snmp.OID" for the raw suffix.
type rowKey struct {
	Type     *jen.Statement
	TypeName string
	DecodeFn string
}

func (k rowKey) raw() bool { return k.Type == nil }

// descriptorKeyType is the KeyType a table descriptor carries for k.
func (k rowKey) descriptorKeyType() string {
	if k.raw() {
		return "snmp.OID"
	}

	return k.TypeName
}

// declareRow writes into g the statements that start a row from its
// index suffix, held in idx.
func (k rowKey) declareRow(g *jen.Group, rowTypeName string) {
	if k.raw() {
		g.Id("row").Op(":=").Id(rowTypeName).Values(jen.Dict{jen.Id("Index"): jen.Id("idx")})
		return
	}
	g.Var().Id("row").Id(rowTypeName)
	g.List(jen.Id("row").Dot("Key"), jen.Id("row").Dot("keyValid")).Op("=").Id(k.DecodeFn).Call(jen.Id("idx"))
}

// equalExpr renders the key comparison between rows a and b.
func (k rowKey) equalExpr() *jen.Statement {
	if k.raw() {
		return jen.Id("a").Dot("Index").Dot("Equal").Call(jen.Id("b").Dot("Index"))
	}
	return jen.Id("a").Dot("Key").Op("==").Id("b").Dot("Key").Op("&&").
		Id("a").Dot("keyValid").Op("==").Id("b").Dot("keyValid")
}

// keyPart is one field of a key struct: its Go type, the
// [snmp.IndexShape] that reads it from the suffix, and the conversion
// from the decoded [snmp.IndexValue] to the field.
type keyPart struct {
	Field string
	Type  *jen.Statement
	Shape *jen.Statement
	Value func(part *jen.Statement) *jen.Statement
}

// keyPartFor maps one resolved index part onto its key field.
//
// Every field stays comparable so the struct can be a map key: octets
// become string, an OID its dotted form, and an IpAddress a
// [netip.Addr]. The column of the same object keeps its own type.
//
// A keyed convention the module never imported is held to the same
// IMPORTS rule as a column (see [emitCtx.typeAvailable]): the field
// carries no key type and the reference is reported. Unlike the column,
// which takes the untyped byte fallback, the field keeps the base
// type, because the shape has to match the suffix arcs for any row's
// key to decode.
func (ec *emitCtx) keyPartFor(part smi.IndexPart) keyPart {
	t := part.Type
	kp := keyPart{Field: camelCase(part.Name)}
	shape := func(kind string) *jen.Statement {
		return jen.Values(jen.Dict{jen.Id("Kind"): jen.Qual(snmpImport, "Index"+kind)})
	}

	if kc, ok := ec.keyed[typeKey{Module: t.Module, Name: t.Name}]; ok {
		if !ec.typeAvailable(t) {
			ec.recordDegraded(part.Name, t.Name, t.Module, degradedNotImported)
		} else if goType := ec.keyTypeRef(t, part.Name); goType != nil {
			kp.Type = goType
			if kc.Base == keyBaseString {
				kp.Shape = octetShape(t, part.Implied)
				kp.Value = func(p *jen.Statement) *jen.Statement { return goType.Clone().Call(p.Dot("Octets")) }
			} else {
				kp.Shape = shape("Integer")
				kp.Value = func(p *jen.Statement) *jen.Statement { return goType.Clone().Call(p.Dot("Integer")) }
			}
			return kp
		}
	}

	switch {
	case t.Base == smi.BaseIPAddress:
		kp.Type = jen.Qual("net/netip", "Addr")
		kp.Shape = shape("IPv4")
		kp.Value = func(p *jen.Statement) *jen.Statement { return p.Dot("Addr") }
	case t.Base == smi.BaseObjectIdentifier:
		kp.Type = jen.String()
		if part.Implied {
			kp.Shape = shape("ImpliedOID")
		} else {
			kp.Shape = shape("LengthPrefixedOID")
		}
		kp.Value = func(p *jen.Statement) *jen.Statement { return p.Dot("OID").Dot("String").Call() }
	case dispatchBase(t) == baseBytes:
		kp.Type = jen.String()
		kp.Shape = octetShape(t, part.Implied)
		kp.Value = func(p *jen.Statement) *jen.Statement { return jen.String().Call(p.Dot("Octets")) }
	case dispatchBase(t) == baseUnsigned32:
		kp.Type = jen.Uint32()
		kp.Shape = shape("Integer")
		kp.Value = func(p *jen.Statement) *jen.Statement { return p.Dot("Integer") }
	default:
		kp.Type = jen.Int32()
		kp.Shape = shape("Integer")
		kp.Value = func(p *jen.Statement) *jen.Statement { return jen.Int32().Call(p.Dot("Integer")) }
	}

	return kp
}

// octetShape is the suffix shape of an OCTET STRING part: fixed when the
// type allows exactly one size, since RFC 2578 §7.7 then omits the
// length arc, IMPLIED when the clause says so, and length-prefixed
// otherwise.
func octetShape(t *smi.Type, implied bool) *jen.Statement {
	if len(t.Sizes) == 1 && t.Sizes[0].Min == t.Sizes[0].Max {
		return jen.Values(jen.Dict{
			jen.Id("Kind"):   jen.Qual(snmpImport, "IndexFixedOctets"),
			jen.Id("Length"): jen.Lit(int(t.Sizes[0].Min)),
		})
	}
	kind := "IndexLengthPrefixedOctets"
	if implied {
		kind = "IndexImpliedOctets"
	}

	return jen.Values(jen.Dict{jen.Id("Kind"): jen.Qual(snmpImport, kind)})
}

// emitTableKey writes the key struct, suffix shapes, and decode helper
// of one table and returns how its rows are keyed.
//
// An augmenting table reuses the augmented table's struct, local or in
// its configured package, and decodes it with a helper of its own since
// the parts are the same. A table with an unresolved part, or one
// augmenting a table whose package is not configured, keeps the raw
// suffix as its key.
func emitTableKey(f *jen.File, ec *emitCtx, t *smi.Table, tableName string) rowKey {
	if len(t.Index) == 0 {
		return rowKey{}
	}
	for _, part := range t.Index {
		if part.Unresolved || part.Type == nil {
			return rowKey{}
		}
	}

	parts := make([]keyPart, 0, len(t.Index))
	for _, part := range t.Index {
		parts = append(parts, ec.keyPartFor(part))
	}

	keyTypeName := tableName + "Key"
	key := rowKey{Type: jen.Id(keyTypeName), TypeName: keyTypeName, DecodeFn: "decode" + keyTypeName}
	switch {
	case t.AugmentsTable == nil:
		f.Comment(keyTypeName + " is the decoded INDEX of one " + t.Node.Name + " row, one field per")
		f.Comment("part in INDEX order. It is comparable and usable as a map key.")
		f.Type().Id(keyTypeName).StructFunc(func(g *jen.Group) {
			for _, p := range parts {
				g.Id(p.Field).Add(p.Type.Clone())
			}
		})
	case t.AugmentsTable.Node.Module == ec.mod.Name:
		key.TypeName = camelCase(t.AugmentsTable.Node.Name) + "Key"
		key.Type = jen.Id(key.TypeName)
	default:
		augName := camelCase(t.AugmentsTable.Node.Name) + "Key"
		q := ec.moduleQual(t.AugmentsTable.Node.Module, augName)
		if q == nil {
			ec.recordDegraded(t.Row.Name, augName, t.AugmentsTable.Node.Module, degradedNotConfigured)
			return rowKey{}
		}
		key.Type = q
		key.TypeName = ec.cfgByName[t.AugmentsTable.Node.Module].Package + "." + augName
	}

	shapesName := unexported(tableName) + "IndexShapes"
	f.Var().Id(shapesName).Op("=").Index().Qual(snmpImport, "IndexShape").ValuesFunc(func(g *jen.Group) {
		for _, p := range parts {
			g.Add(p.Shape)
		}
	})

	f.Comment(key.DecodeFn + " decodes the instance suffix of one " + t.Node.Name + " row. ok is false")
	f.Comment("when the suffix does not match the declared INDEX; the key is then zero.")
	f.Func().Id(key.DecodeFn).Params(jen.Id("idx").Qual(snmpImport, "OID")).Params(key.Type.Clone(), jen.Bool()).Block(
		jen.List(jen.Id("parts"), jen.Id("ok")).Op(":=").Qual(snmpImport, "DecodeIndex").Call(jen.Id("idx"), jen.Id(shapesName)),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(key.Type.Clone().Values(), jen.False()),
		),
		jen.Return(key.Type.Clone().ValuesFunc(func(g *jen.Group) {
			for i, p := range parts {
				g.Id(p.Field).Op(":").Add(p.Value(jen.Id("parts").Index(jen.Lit(i))))
			}
		}), jen.True()),
	)

	return key
}

// unexported turns an emitted UpperCamelCase name into the
// package-private spelling of its companion identifier.
func unexported(s string) string {
	return strings.ToLower(s[:1]) + s[1:]
}
