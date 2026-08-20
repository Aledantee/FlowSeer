package yang_test

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// TestValueSymmetry drives every YANG base type through both wire
// forms: canonical (XML text) and RFC 7951 JSON, asserting the exact
// encodings — including the 7951 quirks — and that each decodes back
// to the identical Value.
func TestValueSymmetry(t *testing.T) {
	tests := []struct {
		name      string
		value     yang.Value
		canonical string
		json      string
	}{
		{
			name:      "int8 number",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeInt8}, Int: -128},
			canonical: "-128",
			json:      `-128`,
		},
		{
			name:      "int32 number",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeInt32}, Int: -2147483648},
			canonical: "-2147483648",
			json:      `-2147483648`,
		},
		{
			name:      "int64 is a JSON string",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeInt64}, Int: -9223372036854775808},
			canonical: "-9223372036854775808",
			json:      `"-9223372036854775808"`,
		},
		{
			name:      "uint16 number",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeUint16}, Uint: 65535},
			canonical: "65535",
			json:      `65535`,
		},
		{
			name:      "uint64 is a JSON string",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeUint64}, Uint: 18446744073709551615},
			canonical: "18446744073709551615",
			json:      `"18446744073709551615"`,
		},
		{
			name:      "boolean",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeBool}, Bool: true},
			canonical: "true",
			json:      `true`,
		},
		{
			name:      "string",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeString}, String: `quote " and slash /`},
			canonical: `quote " and slash /`,
			json:      `"quote \" and slash /"`,
		},
		{
			name:      "enumeration",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeEnum}, String: "if-mib"},
			canonical: "if-mib",
			json:      `"if-mib"`,
		},
		{
			name:      "bits space-joined",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeBits}, Bits: []string{"sync", "auto-sense"}},
			canonical: "sync auto-sense",
			json:      `"sync auto-sense"`,
		},
		{
			name:      "binary base64",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeBinary}, Bytes: []byte{0xde, 0xad, 0xbe, 0xef}},
			canonical: "3q2+7w==",
			json:      `"3q2+7w=="`,
		},
		{
			name:      "decimal64 as a JSON string",
			value:     yang.Decimal64(-12345, 3),
			canonical: "-12.345",
			json:      `"-12.345"`,
		},
		{
			name:      "decimal64 trims trailing zeros to one digit",
			value:     yang.Decimal64(5000, 3),
			canonical: "5.0",
			json:      `"5.0"`,
		},
		{
			name:      "empty is [null]",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeEmpty}},
			canonical: "",
			json:      `[null]`,
		},
		{
			name: "identityref module-prefixed",
			value: yang.Value{
				Type:     yang.Type{Kind: yang.TypeIdentityRef},
				Identity: yang.Identity{Module: "iana-if-type", Name: "ethernetCsmacd"},
			},
			canonical: "iana-if-type:ethernetCsmacd",
			json:      `"iana-if-type:ethernetCsmacd"`,
		},
		{
			name:      "instance-identifier",
			value:     yang.Value{Type: yang.Type{Kind: yang.TypeInstanceID}, String: "/ietf-interfaces:interfaces/interface[name='eth0']"},
			canonical: "/ietf-interfaces:interfaces/interface[name='eth0']",
			json:      `"/ietf-interfaces:interfaces/interface[name='eth0']"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			canon, err := tc.value.Canonical()
			if err != nil {
				t.Fatalf("Canonical(): %v", err)
			}
			if canon != tc.canonical {
				t.Errorf("Canonical() = %q, want %q", canon, tc.canonical)
			}
			decoded, err := yang.ParseCanonical(tc.value.Type, canon)
			if err != nil {
				t.Fatalf("ParseCanonical(%q): %v", canon, err)
			}
			// Decimal64 canonical form trims trailing zeros, so
			// compare re-encoded forms, not raw representations.
			assertValueEqual(t, "canonical", decoded, tc.value)

			j, err := tc.value.MarshalJSON7951()
			if err != nil {
				t.Fatalf("MarshalJSON7951(): %v", err)
			}
			if string(j) != tc.json {
				t.Errorf("MarshalJSON7951() = %s, want %s", j, tc.json)
			}
			decoded, err = yang.ParseJSON7951(tc.value.Type, j)
			if err != nil {
				t.Fatalf("ParseJSON7951(%s): %v", j, err)
			}
			assertValueEqual(t, "json", decoded, tc.value)
		})
	}
}

// assertValueEqual compares decoded against want structurally. Bits
// decode from "" as a nil slice; normalize before comparing.
func assertValueEqual(t *testing.T, form string, got, want yang.Value) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s round-trip = %+v, want %+v", form, got, want)
	}
}

func TestParseJSON7951AcceptsLenientNumberSpellings(t *testing.T) {
	// Strict RFC 7951 puts int64 in a string and int32 in a number;
	// real devices mix them, so decode accepts both spellings.
	tests := []struct {
		name string
		kind yang.TypeKind
		raw  string
		want int64
	}{
		{name: "int64 bare number", kind: yang.TypeInt64, raw: `42`, want: 42},
		{name: "int32 quoted", kind: yang.TypeInt32, raw: `"42"`, want: 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := yang.ParseJSON7951(yang.Type{Kind: tc.kind}, []byte(tc.raw))
			if err != nil {
				t.Fatalf("ParseJSON7951(%s): %v", tc.raw, err)
			}
			if v.Int != tc.want {
				t.Errorf("Int = %d, want %d", v.Int, tc.want)
			}
		})
	}
}

func TestParseCanonicalRangeChecks(t *testing.T) {
	tests := []struct {
		name string
		kind yang.TypeKind
		in   string
	}{
		{name: "int8 overflow", kind: yang.TypeInt8, in: "128"},
		{name: "uint8 overflow", kind: yang.TypeUint8, in: "256"},
		{name: "uint32 overflow", kind: yang.TypeUint32, in: "4294967296"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := yang.ParseCanonical(yang.Type{Kind: tc.kind}, tc.in)
			if err == nil {
				t.Fatalf("ParseCanonical(%s, %q) succeeded, want range error", tc.kind, tc.in)
			}
			if code, ok := errs.CodeOf(err); !ok || code != yang.ErrCodeValueRange {
				t.Errorf("error code = %v, want %v", code, yang.ErrCodeValueRange)
			}
		})
	}
}

func TestDecimal64RejectsExcessFractionDigits(t *testing.T) {
	_, err := yang.ParseCanonical(yang.Type{Kind: yang.TypeDecimal64, FractionDigits: 2}, "1.234")
	if err == nil {
		t.Fatal("three fraction digits parsed under fraction-digits 2")
	}
}

// TestUnionFirstMatchDeterministic covers the union contract: members
// resolve strictly in YANG definition order, so an input two members
// accept always lands on the earlier one.
func TestUnionFirstMatchDeterministic(t *testing.T) {
	union := yang.Type{Kind: yang.TypeUnion, Members: []yang.Type{
		{Kind: yang.TypeUint16},
		{Kind: yang.TypeString},
	}}

	v, err := yang.ParseCanonical(union, "8080")
	if err != nil {
		t.Fatalf("ParseCanonical: %v", err)
	}
	if v.Type.Kind != yang.TypeUint16 || v.Uint != 8080 {
		t.Errorf("resolved to %v (%+v), want the uint16 member", v.Type.Kind, v)
	}

	v, err = yang.ParseCanonical(union, "http-alt")
	if err != nil {
		t.Fatalf("ParseCanonical: %v", err)
	}
	if v.Type.Kind != yang.TypeString || v.String != "http-alt" {
		t.Errorf("resolved to %v (%+v), want the string member", v.Type.Kind, v)
	}

	// Same rule over JSON, where "8080" arrives quoted: numeric
	// leniency still picks the earlier uint16 member.
	v, err = yang.ParseJSON7951(union, []byte(`"8080"`))
	if err != nil {
		t.Fatalf("ParseJSON7951: %v", err)
	}
	if v.Type.Kind != yang.TypeUint16 {
		t.Errorf("JSON union resolved to %v, want uint16", v.Type.Kind)
	}

	_, err = yang.ParseJSON7951(yang.Type{Kind: yang.TypeUnion, Members: []yang.Type{{Kind: yang.TypeUint8}}}, []byte(`"not-a-number"`))
	if err == nil {
		t.Fatal("union with no matching member parsed")
	}
	if code, ok := errs.CodeOf(err); !ok || code != yang.ErrCodeUnionNoMatch {
		t.Errorf("error code = %v, want %v", code, yang.ErrCodeUnionNoMatch)
	}
}

func TestParseCanonicalEmptyRejectsContent(t *testing.T) {
	if _, err := yang.ParseCanonical(yang.Type{Kind: yang.TypeEmpty}, "x"); err == nil {
		t.Fatal("empty leaf with content parsed")
	}
}

func TestParseJSON7951EmptyRejectsWrongShape(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `[null, null]`, `["null"]`} {
		if _, err := yang.ParseJSON7951(yang.Type{Kind: yang.TypeEmpty}, []byte(raw)); err == nil {
			t.Errorf("ParseJSON7951(empty, %s) succeeded, want error", raw)
		}
	}
}
