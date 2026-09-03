package yang

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TypeKind identifies a YANG base type. The zero value is invalid by
// design so an unset Type is caught loudly rather than decoded as
// something arbitrary.
type TypeKind int

// The YANG base types (RFC 7950 §9). Leafref does not appear: a
// leafref is resolved at generation time to its target's type, and
// enumeration/identityref/bits carry their value spaces in the
// generated code, not here.
const (
	typeInvalid TypeKind = iota
	TypeInt8
	TypeInt16
	TypeInt32
	TypeInt64
	TypeUint8
	TypeUint16
	TypeUint32
	TypeUint64
	TypeBool
	TypeString
	TypeEnum
	TypeBits
	TypeBinary
	TypeDecimal64
	TypeEmpty
	TypeIdentityRef
	TypeInstanceID
	TypeUnion
)

var typeKindNames = map[TypeKind]string{
	TypeInt8:        "int8",
	TypeInt16:       "int16",
	TypeInt32:       "int32",
	TypeInt64:       "int64",
	TypeUint8:       "uint8",
	TypeUint16:      "uint16",
	TypeUint32:      "uint32",
	TypeUint64:      "uint64",
	TypeBool:        "boolean",
	TypeString:      "string",
	TypeEnum:        "enumeration",
	TypeBits:        "bits",
	TypeBinary:      "binary",
	TypeDecimal64:   "decimal64",
	TypeEmpty:       "empty",
	TypeIdentityRef: "identityref",
	TypeInstanceID:  "instance-identifier",
	TypeUnion:       "union",
}

// String returns the YANG keyword for the kind.
func (k TypeKind) String() string {
	if s, ok := typeKindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("TypeKind(%d)", int(k))
}

// Type describes a leaf's resolved YANG type: the base-type kind plus
// the two parameters the wire codecs need. Generated bindings embed
// Type values in their leaf descriptors; restrictions that only
// matter for device-side validation (ranges, patterns, lengths) are
// deliberately absent per the client-side-validation non-goal.
type Type struct {
	Kind TypeKind
	// FractionDigits is decimal64's scale (1–18). Zero elsewhere.
	FractionDigits int
	// Members are a union's member types in YANG definition order.
	// Resolution is strictly first-match over this order.
	Members []Type
}

// Shared Type singletons for the parameterless kinds. Generated
// schemas reference these instead of repeating literals — at the
// full-surface scale (~1,200 modules) the sharing is a material
// repo-size and link-time win. Decimal64 and unions carry parameters
// and stay per-leaf literals.
var (
	TInt8       = &Type{Kind: TypeInt8}
	TInt16      = &Type{Kind: TypeInt16}
	TInt32      = &Type{Kind: TypeInt32}
	TInt64      = &Type{Kind: TypeInt64}
	TUint8      = &Type{Kind: TypeUint8}
	TUint16     = &Type{Kind: TypeUint16}
	TUint32     = &Type{Kind: TypeUint32}
	TUint64     = &Type{Kind: TypeUint64}
	TBool       = &Type{Kind: TypeBool}
	TString     = &Type{Kind: TypeString}
	TEnum       = &Type{Kind: TypeEnum}
	TBits       = &Type{Kind: TypeBits}
	TBinary     = &Type{Kind: TypeBinary}
	TEmpty      = &Type{Kind: TypeEmpty}
	TIdentity   = &Type{Kind: TypeIdentityRef}
	TInstanceID = &Type{Kind: TypeInstanceID}
)

// Identity is a resolved identityref value: the identity's name
// qualified by the name of the module that defines it.
type Identity struct {
	Module string
	Name   string
}

// String returns "module:name", or bare "name" when Module is empty.
func (id Identity) String() string {
	if id.Module == "" {
		return id.Name
	}
	return id.Module + ":" + id.Name
}

// Value is one typed YANG leaf value: a Type plus exactly one
// populated payload field, selected by Type.Kind (Int for the signed
// widths and the scaled decimal64 digits, Uint for the unsigned
// widths, Bool, String for string/enumeration/instance-identifier,
// Bytes for binary, Bits, Identity). TypeEmpty carries no payload.
//
// A Value inside a union carries the resolved member Type, not the
// union itself, so the wire encoders never re-resolve.
type Value struct {
	Type Type

	Int      int64
	Uint     uint64
	Bool     bool
	String   string
	Bytes    []byte
	Bits     []string
	Identity Identity
}

// Decimal64 assembles a decimal64 Value from its scaled integer
// representation: digits is the value multiplied by 10^fractionDigits
// (RFC 7950 §9.3.1's i × 10^-n form).
func Decimal64(digits int64, fractionDigits int) Value {
	return Value{
		Type: Type{Kind: TypeDecimal64, FractionDigits: fractionDigits},
		Int:  digits,
	}
}

// Canonical renders the value's canonical lexical form (RFC 7950's
// canonical representations): the text content of a NETCONF XML leaf
// element, also the form list-key predicates carry. TypeEmpty renders
// as the empty string (an empty XML element).
func (v Value) Canonical() (string, error) {
	switch v.Type.Kind {
	case TypeInt8, TypeInt16, TypeInt32, TypeInt64:
		return strconv.FormatInt(v.Int, 10), nil
	case TypeUint8, TypeUint16, TypeUint32, TypeUint64:
		return strconv.FormatUint(v.Uint, 10), nil
	case TypeBool:
		return strconv.FormatBool(v.Bool), nil
	case TypeString, TypeEnum, TypeInstanceID:
		return v.String, nil
	case TypeBits:
		return strings.Join(v.Bits, " "), nil
	case TypeBinary:
		return base64.StdEncoding.EncodeToString(v.Bytes), nil
	case TypeDecimal64:
		return formatDecimal64(v.Int, v.Type.FractionDigits)
	case TypeEmpty:
		return "", nil
	case TypeIdentityRef:
		return v.Identity.String(), nil
	default:
		return "", errs.New().Code(ErrCodeValueParse).Msgf("cannot render a %s value canonically", v.Type.Kind)
	}
}

// formatDecimal64 renders digits × 10^-fd in the RFC 7950 canonical
// form: no unnecessary leading zeros, a decimal point, and trailing
// fraction zeros trimmed down to at least one digit.
func formatDecimal64(digits int64, fd int) (string, error) {
	if fd < 1 || fd > 18 {
		return "", errs.New().Code(ErrCodeValueParse).Msgf("decimal64 fraction-digits %d outside 1..18", fd)
	}
	neg := digits < 0
	abs := strconv.FormatUint(absInt64(digits), 10)
	if len(abs) <= fd {
		abs = strings.Repeat("0", fd-len(abs)+1) + abs
	}
	intPart, fracPart := abs[:len(abs)-fd], abs[len(abs)-fd:]
	fracPart = strings.TrimRight(fracPart, "0")
	if fracPart == "" {
		fracPart = "0"
	}
	if neg {
		intPart = "-" + intPart
	}
	return intPart + "." + fracPart, nil
}

// absInt64 returns |v| as a uint64, exact for math.MinInt64.
func absInt64(v int64) uint64 {
	if v < 0 {
		return -uint64(v)
	}
	return uint64(v)
}

// intRanges maps each signed kind to its inclusive bounds.
var intRanges = map[TypeKind][2]int64{
	TypeInt8:  {-1 << 7, 1<<7 - 1},
	TypeInt16: {-1 << 15, 1<<15 - 1},
	TypeInt32: {-1 << 31, 1<<31 - 1},
	TypeInt64: {-1 << 63, 1<<63 - 1},
}

// uintMax maps each unsigned kind to its inclusive upper bound.
var uintMax = map[TypeKind]uint64{
	TypeUint8:  1<<8 - 1,
	TypeUint16: 1<<16 - 1,
	TypeUint32: 1<<32 - 1,
	TypeUint64: 1<<64 - 1,
}

// ParseCanonical parses a canonical lexical form (XML leaf text, list
// key value) under t. A union resolves strictly first-match over
// t.Members, and the returned Value carries the matching member type.
func ParseCanonical(t Type, text string) (Value, error) {
	switch t.Kind {
	case TypeInt8, TypeInt16, TypeInt32, TypeInt64:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return Value{}, errs.From(err).Code(ErrCodeValueParse).Msgf("%q is not a valid %s", text, t.Kind)
		}
		r := intRanges[t.Kind]
		if n < r[0] || n > r[1] {
			return Value{}, errs.New().Code(ErrCodeValueRange).Msgf("%q overflows %s", text, t.Kind)
		}
		return Value{Type: t, Int: n}, nil
	case TypeUint8, TypeUint16, TypeUint32, TypeUint64:
		n, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return Value{}, errs.From(err).Code(ErrCodeValueParse).Msgf("%q is not a valid %s", text, t.Kind)
		}
		if n > uintMax[t.Kind] {
			return Value{}, errs.New().Code(ErrCodeValueRange).Msgf("%q overflows %s", text, t.Kind)
		}
		return Value{Type: t, Uint: n}, nil
	case TypeBool:
		switch text {
		case "true":
			return Value{Type: t, Bool: true}, nil
		case "false":
			return Value{Type: t}, nil
		}
		return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("%q is not a boolean", text)
	case TypeString, TypeEnum, TypeInstanceID:
		return Value{Type: t, String: text}, nil
	case TypeBits:
		return Value{Type: t, Bits: strings.Fields(text)}, nil
	case TypeBinary:
		raw, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return Value{}, errs.From(err).Code(ErrCodeValueParse).Msg("invalid base64 in binary value")
		}
		return Value{Type: t, Bytes: raw}, nil
	case TypeDecimal64:
		return parseDecimal64(t, text)
	case TypeEmpty:
		if text != "" {
			return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("empty leaf carries content %q", text)
		}
		return Value{Type: t}, nil
	case TypeIdentityRef:
		id := Identity{Name: text}
		if mod, name, ok := strings.Cut(text, ":"); ok {
			id = Identity{Module: mod, Name: name}
		}
		if id.Name == "" {
			return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("%q is not an identity name", text)
		}
		return Value{Type: t, Identity: id}, nil
	case TypeUnion:
		return parseUnion(t, text, ParseCanonical)
	default:
		return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("cannot parse a %s value", t.Kind)
	}
}

// parseUnion resolves a union by trying each member in YANG
// definition order with parse and returning the first success —
// deterministic first-match, per the runtime contract.
func parseUnion[In string | []byte](t Type, in In, parse func(Type, In) (Value, error)) (Value, error) {
	for _, member := range t.Members {
		if v, err := parse(member, in); err == nil {
			return v, nil
		}
	}
	return Value{}, errs.New().Code(ErrCodeUnionNoMatch).Msgf("no union member type accepts %q", string(in))
}

// parseDecimal64 parses the lexical decimal form into the scaled
// integer representation, rejecting more fraction digits than the
// type's scale allows.
func parseDecimal64(t Type, text string) (Value, error) {
	fd := t.FractionDigits
	if fd < 1 || fd > 18 {
		return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("decimal64 fraction-digits %d outside 1..18", fd)
	}
	s := text
	neg := false
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		neg, s = true, rest
	} else if rest, ok := strings.CutPrefix(s, "+"); ok {
		s = rest
	}
	intPart, fracPart, _ := strings.Cut(s, ".")
	if intPart == "" || len(fracPart) > fd || !allDigits(intPart) || !allDigits(fracPart) {
		return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("%q is not a valid decimal64 with %d fraction digits", text, fd)
	}
	fracPart += strings.Repeat("0", fd-len(fracPart))
	scaled := intPart + fracPart
	if neg {
		scaled = "-" + scaled
	}
	digits, err := strconv.ParseInt(scaled, 10, 64)
	if err != nil {
		return Value{}, errs.From(err).Code(ErrCodeValueRange).Msgf("%q overflows decimal64", text)
	}
	return Value{Type: t, Int: digits}, nil
}

// allDigits reports whether s is entirely ASCII digits. The empty
// string qualifies (a decimal64 may omit the fraction part).
func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// MarshalJSON7951 renders the value in RFC 7951 JSON: the 32-bit-and-
// narrower integers as JSON numbers; int64, uint64, and decimal64 as
// JSON strings; empty as [null]; identityref with its defining
// module's name prefix; bits as a space-separated string.
func (v Value) MarshalJSON7951() ([]byte, error) {
	switch v.Type.Kind {
	case TypeInt8, TypeInt16, TypeInt32:
		return strconv.AppendInt(nil, v.Int, 10), nil
	case TypeUint8, TypeUint16, TypeUint32:
		return strconv.AppendUint(nil, v.Uint, 10), nil
	case TypeBool:
		return strconv.AppendBool(nil, v.Bool), nil
	case TypeEmpty:
		return []byte("[null]"), nil
	case TypeInt64, TypeUint64, TypeDecimal64, TypeString, TypeEnum, TypeInstanceID,
		TypeBits, TypeBinary, TypeIdentityRef:
		s, err := v.Canonical()
		if err != nil {
			return nil, err
		}
		return json.Marshal(s)
	default:
		return nil, errs.New().Code(ErrCodeValueParse).Msgf("cannot encode a %s value as RFC 7951 JSON", v.Type.Kind)
	}
}

// ParseJSON7951 parses one RFC 7951 JSON leaf value under t. Encoding
// is strict RFC 7951 on output; on input the numeric kinds accept both
// the JSON number and JSON string spellings, since real devices mix
// them — the conformance corpus records which peers need the leniency.
func ParseJSON7951(t Type, raw []byte) (Value, error) {
	raw = bytes.Trim(raw, " \t\r\n")
	if t.Kind != TypeUnion && (!json.Valid(raw) || bytes.Equal(raw, []byte("null"))) {
		return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("%s leaf is not a non-null JSON value", t.Kind)
	}
	switch t.Kind {
	case TypeInt8, TypeInt16, TypeInt32, TypeInt64,
		TypeUint8, TypeUint16, TypeUint32, TypeUint64, TypeDecimal64:
		return ParseCanonical(t, jsonScalarText(raw))
	case TypeBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return Value{}, errs.From(err).Code(ErrCodeValueParse).Msg("boolean leaf is not JSON true/false")
		}
		return Value{Type: t, Bool: b}, nil
	case TypeString, TypeEnum, TypeInstanceID, TypeBits, TypeBinary, TypeIdentityRef:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return Value{}, errs.From(err).Code(ErrCodeValueParse).Msgf("%s leaf is not a JSON string", t.Kind)
		}
		return ParseCanonical(t, s)
	case TypeEmpty:
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil || len(arr) != 1 || string(arr[0]) != "null" {
			return Value{}, errs.New().Code(ErrCodeValueParse).Msg("empty leaf is not the RFC 7951 [null] form")
		}
		return Value{Type: t}, nil
	case TypeUnion:
		return parseUnion(t, raw, ParseJSON7951)
	default:
		return Value{}, errs.New().Code(ErrCodeValueParse).Msgf("cannot parse a %s value from RFC 7951 JSON", t.Kind)
	}
}

// jsonScalarText strips one level of quoting from a JSON scalar so
// the numeric kinds accept both spellings. Malformed input passes
// through and fails in the canonical parser with a proper error.
func jsonScalarText(raw []byte) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
