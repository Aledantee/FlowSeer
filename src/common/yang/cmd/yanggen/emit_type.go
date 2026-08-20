package main

import (
	"github.com/dave/jennifer/jen"
	goyang "github.com/openconfig/goyang/pkg/yang"
)

// emit_type.go maps resolved goyang leaf types onto the generated Go
// representation and the runtime yang.Type descriptor. Leafrefs are
// chased to their target type here (generation-time resolution per
// KTD4); an unresolvable leafref degrades to string.

// yangPkg is the runtime package every generated file imports — the
// only permitted import (R7).
const yangPkg = "go.aledante.io/FlowSeer/src/common/yang"

// leafType is the emission plan for one leaf: the Go scalar type, how
// it embeds in a struct (pointer vs bare slice), the runtime type
// descriptor, and the comparable key-struct representation.
type leafType struct {
	// goType renders the scalar's Go type (without pointer).
	goType func() *jen.Statement
	// pointer wraps the field type in a pointer for presence
	// tracking; false for the slice-backed kinds (bits, binary).
	pointer bool
	// typeExpr renders a *yang.Type expression for Field.Type —
	// a shared singleton for the parameterless kinds, a literal for
	// decimal64 and unions.
	typeExpr func() *jen.Statement
	// valueExpr renders a yang.Type value literal (union members).
	valueExpr func() *jen.Statement
	// keyString marks kinds whose key-struct field is the canonical
	// string rather than the scalar itself (identityref, decimal64,
	// union, bits, binary — the non-comparable or compound kinds).
	keyString bool
}

// mapLeafType resolves e's type (chasing leafrefs) into a leafType.
func mapLeafType(e *goyang.Entry) leafType {
	t := resolveLeafref(e, e.Type, 0)
	return mapYangType(t)
}

// resolveLeafref follows leafref targets up to a fixed depth,
// degrading to string on failure — client-side validation is a
// non-goal, so the value space matters, not the reference.
func resolveLeafref(e *goyang.Entry, t *goyang.YangType, depth int) *goyang.YangType {
	if t == nil || (t.Kind == goyang.Yleafref && e == nil) {
		return &goyang.YangType{Kind: goyang.Ystring}
	}
	if t.Kind != goyang.Yleafref || depth > 8 {
		if t.Kind == goyang.Yleafref {
			return &goyang.YangType{Kind: goyang.Ystring}
		}
		return t
	}
	target := e.Find(t.Path)
	if target == nil || target.Type == nil {
		return &goyang.YangType{Kind: goyang.Ystring}
	}
	return resolveLeafref(target, target.Type, depth+1)
}

// kindVar maps a runtime TypeKind name to its shared *Type singleton.
var kindVar = map[string]string{
	"TypeInt8": "TInt8", "TypeInt16": "TInt16", "TypeInt32": "TInt32", "TypeInt64": "TInt64",
	"TypeUint8": "TUint8", "TypeUint16": "TUint16", "TypeUint32": "TUint32", "TypeUint64": "TUint64",
	"TypeBool": "TBool", "TypeString": "TString", "TypeEnum": "TEnum", "TypeBits": "TBits",
	"TypeBinary": "TBinary", "TypeEmpty": "TEmpty", "TypeIdentityRef": "TIdentity",
	"TypeInstanceID": "TInstanceID",
}

// simpleKind builds a leafType for kinds whose Go scalar is a basic
// type and whose descriptor is just a kind.
func simpleKind(goKind string, runtimeKind string) leafType {
	return leafType{
		goType:  func() *jen.Statement { return jen.Id(goKind) },
		pointer: true,
		typeExpr: func() *jen.Statement {
			return jen.Qual(yangPkg, kindVar[runtimeKind])
		},
		valueExpr: func() *jen.Statement {
			return jen.Qual(yangPkg, "Type").Values(jen.Dict{jen.Id("Kind"): jen.Qual(yangPkg, runtimeKind)})
		},
	}
}

// mapYangType maps one resolved goyang type.
func mapYangType(t *goyang.YangType) leafType {
	switch t.Kind {
	case goyang.Yint8:
		return simpleKind("int8", "TypeInt8")
	case goyang.Yint16:
		return simpleKind("int16", "TypeInt16")
	case goyang.Yint32:
		return simpleKind("int32", "TypeInt32")
	case goyang.Yint64:
		return simpleKind("int64", "TypeInt64")
	case goyang.Yuint8:
		return simpleKind("uint8", "TypeUint8")
	case goyang.Yuint16:
		return simpleKind("uint16", "TypeUint16")
	case goyang.Yuint32:
		return simpleKind("uint32", "TypeUint32")
	case goyang.Yuint64:
		return simpleKind("uint64", "TypeUint64")
	case goyang.Ybool:
		return simpleKind("bool", "TypeBool")
	case goyang.Yempty:
		return simpleKind("bool", "TypeEmpty")
	case goyang.Ystring:
		return simpleKind("string", "TypeString")
	case goyang.YinstanceIdentifier:
		return simpleKind("string", "TypeInstanceID")
	case goyang.Yenum:
		return simpleKind("string", "TypeEnum")
	case goyang.Ybits:
		lt := simpleKind("", "TypeBits")
		lt.goType = func() *jen.Statement { return jen.Index().String() }
		lt.pointer = false
		lt.keyString = true
		return lt
	case goyang.Ybinary:
		lt := simpleKind("", "TypeBinary")
		lt.goType = func() *jen.Statement { return jen.Index().Byte() }
		lt.pointer = false
		lt.keyString = true
		return lt
	case goyang.Ydecimal64:
		fd := t.FractionDigits
		if fd == 0 {
			fd = 1
		}
		literal := func() *jen.Statement {
			return jen.Qual(yangPkg, "Type").Values(jen.Dict{
				jen.Id("Kind"):           jen.Qual(yangPkg, "TypeDecimal64"),
				jen.Id("FractionDigits"): jen.Lit(fd),
			})
		}
		return leafType{
			goType:    func() *jen.Statement { return jen.Qual(yangPkg, "Value") },
			pointer:   true,
			typeExpr:  func() *jen.Statement { return jen.Op("&").Add(literal()) },
			valueExpr: literal,
			keyString: true,
		}
	case goyang.Yidentityref:
		lt := simpleKind("", "TypeIdentityRef")
		lt.goType = func() *jen.Statement { return jen.Qual(yangPkg, "Identity") }
		lt.keyString = true
		return lt
	case goyang.Yunion:
		members := t.Type
		literal := func() *jen.Statement {
			memberExprs := make([]jen.Code, 0, len(members))
			for _, m := range members {
				mt := mapYangType(resolveLeafref(nil, m, 0))
				memberExprs = append(memberExprs, mt.valueExpr())
			}
			return jen.Qual(yangPkg, "Type").Values(jen.Dict{
				jen.Id("Kind"):    jen.Qual(yangPkg, "TypeUnion"),
				jen.Id("Members"): jen.Index().Qual(yangPkg, "Type").Values(memberExprs...),
			})
		}
		return leafType{
			goType:    func() *jen.Statement { return jen.Qual(yangPkg, "Value") },
			pointer:   true,
			typeExpr:  func() *jen.Statement { return jen.Op("&").Add(literal()) },
			valueExpr: literal,
			keyString: true,
		}
	default:
		// Unhandled corner kinds degrade to string rather than
		// failing the whole surface.
		return simpleKind("string", "TypeString")
	}
}
