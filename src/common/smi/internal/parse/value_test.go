package parse

import (
	"bytes"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
)

// object wraps one OBJECT-TYPE with the given SYNTAX and DEFVAL, which
// is the shape every rule about a default value is stated over.
func object(syntax, defval string) string {
	return wrap(`
testObject OBJECT-TYPE
    SYNTAX      ` + syntax + `
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "the object under test"
    DEFVAL      ` + defval + `
    ::= { testMIB 1 }
`)
}

// defaultOf parses one object and returns its default value.
func defaultOf(t *testing.T, syntax, defval string, want ...errs.Code) (*Result, Value) {
	t.Helper()

	r := parseSource(t, object(syntax, defval))
	wantCodes(t, r, want...)
	m := module(t, r)

	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}

	return r, m.ObjectTypes[0].DefaultValue
}

// Every row of RFC 2578 §7.9's table, read as the shape the RFC gives
// it. What the parser owes each row is the value, not a judgement about
// it: whether a label names a member of the enumeration is a question
// only resolution can answer.
func TestDefaultsCoverTheRFCTable(t *testing.T) {
	cases := []struct {
		name   string
		syntax string
		defval string
		kind   ValueKind
		check  func(t *testing.T, r *Result, v Value)
	}{
		{
			name: "Integer32", syntax: "Integer32", defval: "{ -1 }", kind: ValueInteger,
			check: func(t *testing.T, _ *Result, v Value) {
				if v.Number != -1 {
					t.Errorf("got %d, want -1", v.Number)
				}
			},
		},
		{
			name: "enumerated INTEGER", syntax: "INTEGER { up(1), down(2) }", defval: "{ down }", kind: ValueLabel,
			check: func(t *testing.T, r *Result, v Value) {
				if name := r.Text(v.Name); name != "down" {
					t.Errorf("got label %q, want %q", name, "down")
				}
			},
		},
		{
			name: "Unsigned32", syntax: "Unsigned32", defval: "{ 4294967295 }", kind: ValueInteger,
			check: func(t *testing.T, _ *Result, v Value) {
				if v.Number != 4294967295 {
					t.Errorf("got %d, want 4294967295", v.Number)
				}
			},
		},
		{
			name: "Gauge32", syntax: "Gauge32", defval: "{ 7 }", kind: ValueInteger,
		},
		{
			name: "TimeTicks", syntax: "TimeTicks", defval: "{ 100 }", kind: ValueInteger,
		},
		{
			name: "OCTET STRING as text", syntax: "OCTET STRING", defval: `{ "flow" }`, kind: ValueString,
			check: func(t *testing.T, _ *Result, v Value) {
				if !bytes.Equal(v.Octets, []byte("flow")) {
					t.Errorf("got octets %q, want %q", v.Octets, "flow")
				}
			},
		},
		{
			name: "OCTET STRING as binary", syntax: "OCTET STRING", defval: "{ '1010101011110000'B }", kind: ValueOctets,
			check: func(t *testing.T, _ *Result, v Value) {
				if !bytes.Equal(v.Octets, []byte{0xaa, 0xf0}) {
					t.Errorf("got octets % x, want aa f0", v.Octets)
				}
			},
		},
		{
			name: "OBJECT IDENTIFIER", syntax: "OBJECT IDENTIFIER", defval: "{ zeroDotZero }", kind: ValueLabel,
			check: func(t *testing.T, r *Result, v Value) {
				if name := r.Text(v.Name); name != "zeroDotZero" {
					t.Errorf("got descriptor %q, want %q", name, "zeroDotZero")
				}
			},
		},
		{
			name: "IpAddress", syntax: "IpAddress", defval: "{ 'c0a80101'H }", kind: ValueOctets,
			check: func(t *testing.T, _ *Result, v Value) {
				if !bytes.Equal(v.Octets, []byte{192, 168, 1, 1}) {
					t.Errorf("got octets % x, want c0 a8 01 01", v.Octets)
				}
			},
		},
		{
			name: "Opaque", syntax: "Opaque", defval: "{ '00'H }", kind: ValueOctets,
		},
		{
			name: "BITS", syntax: "BITS { primary(0), secondary(1) }", defval: "{ { primary, secondary } }", kind: ValueBits,
			check: func(t *testing.T, r *Result, v Value) {
				if len(v.Bits) != 2 {
					t.Fatalf("got %d bits, want 2", len(v.Bits))
				}
				if got := r.Text(v.Bits[0]); got != "primary" {
					t.Errorf("got first bit %q, want %q", got, "primary")
				}
				if got := r.Text(v.Bits[1]); got != "secondary" {
					t.Errorf("got second bit %q, want %q", got, "secondary")
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, v := defaultOf(t, c.syntax, c.defval)
			if v.Kind != c.kind {
				t.Fatalf("default reads as %v, want %v", v.Kind, c.kind)
			}
			if c.check != nil {
				c.check(t, r, v)
			}
		})
	}
}

// The empty bit set is the form RFC 2578 §7.9 spells out and the one
// that panics the parser this package replaces. It appears in 29 files
// of the shipped corpus.
func TestEmptyBitsDefault(t *testing.T) {
	_, v := defaultOf(t, "BITS { primary(0) }", "{ { } }")

	if v.Kind != ValueBits {
		t.Fatalf("default reads as %v, want %v", v.Kind, ValueBits)
	}
	if len(v.Bits) != 0 {
		t.Errorf("got %d bits, want none", len(v.Bits))
	}
}

func TestHexDefault(t *testing.T) {
	_, v := defaultOf(t, "OCTET STRING (SIZE (6))", "{ 'ffffffffffff'H }")

	if v.Kind != ValueOctets {
		t.Fatalf("default reads as %v, want %v", v.Kind, ValueOctets)
	}
	if want := bytes.Repeat([]byte{0xff}, 6); !bytes.Equal(v.Octets, want) {
		t.Errorf("got octets % x, want % x", v.Octets, want)
	}
}

// A hexadecimal literal with an odd digit count is the lexer's finding,
// so the default keeps the octets that were whole and the condition is
// reported once rather than twice.
func TestOddHexDefaultIsReported(t *testing.T) {
	_, v := defaultOf(t, "OCTET STRING", "{ 'fff'H }", diag.ErrCodeOddHexString)

	if !bytes.Equal(v.Octets, []byte{0xff}) {
		t.Errorf("got octets % x, want ff", v.Octets)
	}
}

func TestBinaryDefaultShortOfAWholeOctet(t *testing.T) {
	_, v := defaultOf(t, "OCTET STRING", "{ '10101'B }", diag.ErrCodeBinaryStringNotOctets)

	if !bytes.Equal(v.Octets, []byte{0xa8}) {
		t.Errorf("got octets % x, want a8", v.Octets)
	}
}

// A counter's value only ever means the difference between two
// readings, so RFC 2578 §7.9 gives it no default form.
func TestDefaultNotPermittedOnACounter(t *testing.T) {
	r, _ := defaultOf(t, "Counter32", "{ 0 }", diag.ErrCodeDefaultNotPermitted)

	if got := rendered(r)[0].Message; got != "DEFVAL is not permitted on a Counter32 object" {
		t.Errorf("got message %q", got)
	}
}

// Some MIBs write an OID default as bare sub-identifiers. The RFC does
// not define the form, but what it means is plain, so it is read and
// reported rather than refused.
func TestNonConformingOIDDefault(t *testing.T) {
	r, v := defaultOf(t, "OBJECT IDENTIFIER", "{ { 1 3 6 1 4 1 } }", diag.ErrCodeNonConformingOIDDefault)

	if v.Kind != ValueOID {
		t.Fatalf("default reads as %v, want %v", v.Kind, ValueOID)
	}
	want := []int64{1, 3, 6, 1, 4, 1}
	if len(v.Subs) != len(want) {
		t.Fatalf("got %d sub-identifiers, want %d", len(v.Subs), len(want))
	}
	for i := range want {
		if v.Subs[i] != want[i] {
			t.Errorf("sub-identifier %d is %d, want %d", i, v.Subs[i], want[i])
		}
	}
	wantText(t, r, "DEFVAL", v.Span, "{ { 1 3 6 1 4 1 } }")
}

// The same list at one brace level, where DEFVAL's own braces are the
// only ones there are. It has to be graded and read as an OID for the
// same reason: taking the first number for an integer default would
// answer 1 where the source said 1.3.6.1, silently.
func TestNonConformingOIDDefaultAtOneBraceLevel(t *testing.T) {
	_, v := defaultOf(t, "OBJECT IDENTIFIER", "{ 1 3 6 1 }", diag.ErrCodeNonConformingOIDDefault)

	if v.Kind != ValueOID {
		t.Fatalf("default reads as %v, want %v", v.Kind, ValueOID)
	}
	if want := []int64{1, 3, 6, 1}; !slices.Equal(v.Subs, want) {
		t.Errorf("got sub-identifiers %v, want %v", v.Subs, want)
	}
}

// A single number stays the integer default RFC 2578 §7.9 defines
// wherever the declared type leaves room for one. Only a type that can
// hold no integer at all, or a second number, makes the list form.
func TestSingleNumberDefaultStaysAnInteger(t *testing.T) {
	for _, syntax := range []string{"Integer32", "INTEGER { up(1), down(2) }", "DisplayString"} {
		t.Run(syntax, func(t *testing.T) {
			_, v := defaultOf(t, syntax, "{ 1 }")
			if v.Kind != ValueInteger || v.Number != 1 {
				t.Errorf("default reads as %v %d, want %v 1", v.Kind, v.Number, ValueInteger)
			}
		})
	}
}

// RFC 2580's VARIATION writes its default with the same production
// DEFVAL uses, and reads it with the same code.
func TestVariationDefaultSharesTheProduction(t *testing.T) {
	r := parseSource(t, wrap(`
testCaps AGENT-CAPABILITIES
    PRODUCT-RELEASE "release 1"
    STATUS          current
    DESCRIPTION     "what the agent does"
    SUPPORTS        TEST-MIB
        INCLUDES    { testGroup }
        VARIATION   testObject
            SYNTAX      BITS { primary(0), secondary(1) }
            DEFVAL      { { secondary } }
            DESCRIPTION "only the second bit"
    ::= { testMIB 2 }
`))
	wantCodes(t, r)
	m := module(t, r)

	if len(m.AgentCapabilities) != 1 {
		t.Fatalf("got %d capabilities, want 1", len(m.AgentCapabilities))
	}
	supports := m.AgentCapabilities[0].Supports
	if len(supports) != 1 || len(supports[0].Variations) != 1 {
		t.Fatalf("got %d supports statements, want one with one variation", len(supports))
	}

	v := supports[0].Variations[0].DefaultValue
	if v.Kind != ValueBits {
		t.Fatalf("the variation default reads as %v, want %v", v.Kind, ValueBits)
	}
	if len(v.Bits) != 1 || r.Text(v.Bits[0]) != "secondary" {
		t.Errorf("got bits %v, want one named secondary", v.Bits)
	}
}
