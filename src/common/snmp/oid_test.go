package snmp

import (
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/ae"
)

func TestParseOID_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		subs  []uint32
	}{
		{"no leading dot", "1.3.6.1.2.1", "1.3.6.1.2.1", []uint32{1, 3, 6, 1, 2, 1}},
		{"leading dot", ".1.3.6.1.2.1", "1.3.6.1.2.1", []uint32{1, 3, 6, 1, 2, 1}},
		{"single sub", "0", "0", []uint32{0}},
		// Stress the uint32 width in a position that respects the SMIv2
		// root constraints: first sub = 2, then a max-uint32 component.
		{"max uint32", "2.4294967295", "2.4294967295", []uint32{2, 4294967295}},
		{
			"long", "1.3.6.1.4.1.9.9.276.1.1.1.1.6.1234", "1.3.6.1.4.1.9.9.276.1.1.1.1.6.1234",
			[]uint32{1, 3, 6, 1, 4, 1, 9, 9, 276, 1, 1, 1, 1, 6, 1234},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oid, err := ParseOID(tc.input)
			if err != nil {
				t.Fatalf("ParseOID(%q) error: %v", tc.input, err)
			}
			if got := oid.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
			if oid.Len() != len(tc.subs) {
				t.Fatalf("Len() = %d, want %d", oid.Len(), len(tc.subs))
			}
			for i, want := range tc.subs {
				if got := oid.At(i); got != want {
					t.Errorf("At(%d) = %d, want %d", i, got, want)
				}
			}
		})
	}
}

func TestParseOID_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"only dot", "."},
		{"only dots", "..."},
		{"non numeric", "1.3.abc.1"},
		{"trailing dot", "1.3.6."},
		{"empty component", "1..3"},
		{"overflow uint32", "1.3.6.1.2.1.999999999999"},
		{"negative", "1.3.-1.4"},
		{"whitespace", "1.3 .6"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseOID(tc.input); err == nil {
				t.Errorf("ParseOID(%q) = nil error, want error", tc.input)
			}
		})
	}
}

// TestParseOID_SMIv2RootConstraints enforces the SMIv2 / RFC 2578 root
// arc constraints: the first sub-identifier must be 0, 1, or 2; when
// first is 0 or 1, the second must be in [0, 39]; first=2 has no such
// constraint on the second sub.
func TestParseOID_SMIv2RootConstraints(t *testing.T) {
	rejects := []struct {
		name  string
		input string
		want  string // substring expected in error message
	}{
		{"first sub-id out of range", "3.6.1.2.1", "first sub-identifier"},
		{"second sub-id too large for root 0", "0.40.1", "second sub-identifier"},
		{"second sub-id too large for root 1", "1.40.1", "second sub-identifier"},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseOID(tc.input)
			if err == nil {
				t.Fatalf("ParseOID(%q) = nil error, want error", tc.input)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ParseOID(%q) error %q does not mention %q",
					tc.input, err.Error(), tc.want)
			}
		})
	}

	// first=2 escapes the second-sub-id constraint.
	if _, err := ParseOID("2.999.1"); err != nil {
		t.Errorf("ParseOID(%q) error: %v (first=2 should permit any second sub)", "2.999.1", err)
	}
	// first=2 with a second still within [0, 39] is also accepted.
	if _, err := ParseOID("2.0.1"); err != nil {
		t.Errorf("ParseOID(%q) error: %v", "2.0.1", err)
	}
}

// TestParseOID_LengthLimit rejects OIDs with more than 128 sub-identifiers.
func TestParseOID_LengthLimit(t *testing.T) {
	parts := make([]string, 129)
	for i := range parts {
		switch i {
		case 0:
			parts[i] = "1"
		case 1:
			parts[i] = "3"
		default:
			parts[i] = "1"
		}
	}
	input := strings.Join(parts, ".")
	_, err := ParseOID(input)
	if err == nil {
		t.Fatal("expected error for 129-component OID")
	}
	if !strings.Contains(err.Error(), "128") && !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error %q should reference the length cap", err.Error())
	}

	// Exactly 128 components is still accepted.
	parts128 := make([]string, 128)
	for i := range parts128 {
		switch i {
		case 0:
			parts128[i] = "1"
		case 1:
			parts128[i] = "3"
		default:
			parts128[i] = "1"
		}
	}
	if _, err := ParseOID(strings.Join(parts128, ".")); err != nil {
		t.Errorf("128-component OID rejected: %v", err)
	}
}

// TestNewOID_HappyPath pins zero-arg and basic round-trip behavior of
// the now-fallible NewOID.
func TestNewOID_HappyPath(t *testing.T) {
	// Empty arg list yields the zero OID and a nil error.
	got, err := NewOID()
	if err != nil {
		t.Fatalf("NewOID() error: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("NewOID() Len() = %d, want 0", got.Len())
	}
	if got.String() != "" {
		t.Errorf("NewOID() String() = %q, want %q", got.String(), "")
	}

	// Round-trip through String().
	oid, err := NewOID(1, 3, 6, 1, 2, 1)
	if err != nil {
		t.Fatalf("NewOID(1, 3, 6, 1, 2, 1) error: %v", err)
	}
	if g, want := oid.String(), "1.3.6.1.2.1"; g != want {
		t.Errorf("NewOID(...).String() = %q, want %q", g, want)
	}

	// Each single-root sub-id is valid on its own.
	for _, r := range []uint32{0, 1, 2} {
		if _, err := NewOID(r); err != nil {
			t.Errorf("NewOID(%d) error: %v", r, err)
		}
	}

	// first=2 escapes the second-sub-id constraint (matches ParseOID("2.999.1")).
	if _, err := NewOID(2, 999, 1); err != nil {
		t.Errorf("NewOID(2, 999, 1) error: %v (first=2 should permit any second sub)", err)
	}
}

// TestNewOID_DefensiveCopy asserts that mutating the caller's slice
// after construction does not affect the OID's backing storage.
func TestNewOID_DefensiveCopy(t *testing.T) {
	subs := []uint32{1, 3, 6}
	oid, err := NewOID(subs...)
	if err != nil {
		t.Fatalf("NewOID(1, 3, 6) error: %v", err)
	}
	subs[1] = 99
	if got, want := oid.String(), "1.3.6"; got != want {
		t.Errorf("NewOID mutated by caller slice mutation: got %q, want %q", got, want)
	}
}

// TestNewOID_ValidatesSMIv2Rules pins that NewOID rejects the same
// inputs ParseOID rejects: first sub-id > 2, second sub-id > 39 when
// first ≤ 1, and any input exceeding the 128-sub-id cap. Error messages
// name the offending sub-id index and value.
func TestNewOID_ValidatesSMIv2Rules(t *testing.T) {
	rejects := []struct {
		name    string
		subs    []uint32
		wantMsg []string // substrings expected in the static message
		wantGot uint32   // offending value, now carried on the "got" attribute
	}{
		{"first sub > 2 (single)", []uint32{3}, []string{"first sub-identifier", "index 0"}, 3},
		{"first sub > 2 (multi)", []uint32{99, 1}, []string{"first sub-identifier", "index 0"}, 99},
		{"second sub > 39 with root 0", []uint32{0, 40}, []string{"second sub-identifier", "index 1"}, 40},
		{"second sub > 39 with root 1", []uint32{1, 40}, []string{"second sub-identifier", "index 1"}, 40},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewOID(tc.subs...)
			if err == nil {
				t.Fatalf("NewOID(%v) = nil error, want error", tc.subs)
			}
			msg := err.Error()
			for _, want := range tc.wantMsg {
				if !strings.Contains(msg, want) {
					t.Errorf("NewOID(%v) error %q does not mention %q", tc.subs, msg, want)
				}
			}
			// The offending value moved from the message into the "got"
			// structured attribute.
			if got := ae.Attributes(err)["got"]; got != tc.wantGot {
				t.Errorf("NewOID(%v) got attr = %v, want %d", tc.subs, got, tc.wantGot)
			}
		})
	}
}

// TestNewOID_LengthLimit pins the same 128-sub-id cap that ParseOID
// enforces. Exactly 128 succeeds; 129 fails.
func TestNewOID_LengthLimit(t *testing.T) {
	// 129 sub-ids -> error mentioning 128.
	subs129 := make([]uint32, 129)
	subs129[0] = 1
	subs129[1] = 3
	for i := 2; i < 129; i++ {
		subs129[i] = 1
	}
	_, err := NewOID(subs129...)
	if err == nil {
		t.Fatal("NewOID with 129 sub-ids: expected error")
	}
	if got := ae.Attributes(err)["max"]; got != maxOIDComponents {
		t.Errorf("max attr = %v, want %d (the 128-cap)", got, maxOIDComponents)
	}

	// 128 sub-ids -> success (pins the same boundary as TestParseOID_LengthLimit).
	subs128 := make([]uint32, 128)
	subs128[0] = 1
	subs128[1] = 3
	for i := 2; i < 128; i++ {
		subs128[i] = 1
	}
	if _, err := NewOID(subs128...); err != nil {
		t.Errorf("NewOID with 128 sub-ids: %v", err)
	}
}

// TestNewOID_AgreesWithParseOID cross-checks that NewOID and ParseOID
// reject the same inputs and accept the same inputs. Pins that the two
// construction paths cannot drift apart structurally.
func TestNewOID_AgreesWithParseOID(t *testing.T) {
	cases := [][]uint32{
		// Valid.
		{1, 3, 6, 1, 2, 1},
		{0, 0},
		{1, 39},
		{2, 999, 1},
		// Invalid.
		{3},
		{99, 1},
		{0, 40},
		{1, 40},
	}
	for _, subs := range cases {
		parts := make([]string, len(subs))
		for i, v := range subs {
			parts[i] = strconv.FormatUint(uint64(v), 10)
		}
		s := strings.Join(parts, ".")

		_, newErr := NewOID(subs...)
		_, parseErr := ParseOID(s)
		if (newErr == nil) != (parseErr == nil) {
			t.Errorf("disagree on %v / %q: NewOID err=%v, ParseOID err=%v",
				subs, s, newErr, parseErr)
		}
	}
}

// TestMustOID_HappyPath asserts MustOID returns the same OID NewOID
// would have on valid input, without panicking. The zero-arg form
// returns the zero OID.
func TestMustOID_HappyPath(t *testing.T) {
	if got := MustOID(1, 3, 6, 1, 2, 1); got.String() != "1.3.6.1.2.1" {
		t.Errorf("MustOID(...).String() = %q, want %q", got.String(), "1.3.6.1.2.1")
	}
	if got := MustOID(); got.Len() != 0 {
		t.Errorf("MustOID() Len() = %d, want 0", got.Len())
	}
}

// TestMustOID_PanicOnInvalid asserts MustOID panics on invalid input
// and the panic value carries an error matching what NewOID would have
// returned.
func TestMustOID_PanicOnInvalid(t *testing.T) {
	cases := []struct {
		name string
		subs []uint32
		want string // substring expected in panic message
	}{
		{"bad first sub-id", []uint32{99, 1}, "first sub-identifier"},
		{"bad second sub-id", []uint32{0, 40}, "second sub-identifier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("MustOID(%v) did not panic", tc.subs)
				}
				err, ok := r.(error)
				if !ok {
					t.Fatalf("MustOID(%v) panic value %v is not an error", tc.subs, r)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("panic error %q does not mention %q", err.Error(), tc.want)
				}
			}()
			_ = MustOID(tc.subs...)
		})
	}
}

// TestMustOID_PanicOnLengthCap asserts MustOID panics with a 128-cap
// error on a 129-sub-id call.
func TestMustOID_PanicOnLengthCap(t *testing.T) {
	subs := make([]uint32, 129)
	subs[0] = 1
	subs[1] = 3
	for i := 2; i < 129; i++ {
		subs[i] = 1
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustOID with 129 sub-ids did not panic")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("panic value %v is not an error", r)
		}
		if got := ae.Attributes(err)["max"]; got != maxOIDComponents {
			t.Errorf("panic error max attr = %v, want %d (the 128-cap)", got, maxOIDComponents)
		}
	}()
	_ = MustOID(subs...)
}

func TestOID_Equal(t *testing.T) {
	a, _ := ParseOID("1.3.6.1.2.1")
	b, _ := ParseOID("1.3.6.1.2.1")
	c, _ := ParseOID("1.3.6.1.2.2")
	d, _ := ParseOID("1.3.6.1.2")

	if !a.Equal(b) {
		t.Error("equal OIDs reported as unequal")
	}
	if a.Equal(c) {
		t.Error("unequal OIDs reported as equal (sibling)")
	}
	if a.Equal(d) {
		t.Error("OIDs of different length reported as equal")
	}
	empty1 := OID{}
	empty2 := OID{}
	if !empty1.Equal(empty2) {
		t.Error("two empty OIDs reported as unequal")
	}
}

func TestOID_HasPrefix(t *testing.T) {
	parent, _ := ParseOID("1.3.6.1.2.1")
	child, _ := ParseOID("1.3.6.1.2.1.2.2.1.1.42")
	sibling, _ := ParseOID("1.3.6.1.2.2")
	shorter, _ := ParseOID("1.3.6.1")

	if !child.HasPrefix(parent) {
		t.Error("child should have parent as prefix")
	}
	if !parent.HasPrefix(parent) {
		t.Error("OID should have itself as prefix")
	}
	if !parent.HasPrefix(shorter) {
		t.Error("longer should have shorter prefix")
	}
	if parent.HasPrefix(child) {
		t.Error("parent should not have child as prefix")
	}
	if child.HasPrefix(sibling) {
		t.Error("child should not have unrelated sibling as prefix")
	}
	empty := OID{}
	if !parent.HasPrefix(empty) {
		t.Error("every OID should have empty OID as prefix")
	}
}

func TestOID_Parent(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1")
	parent, ok := oid.Parent()
	if !ok {
		t.Fatal("Parent() ok = false on multi-element OID")
	}
	if parent.String() != "1.3.6.1.2" {
		t.Errorf("Parent() = %q, want %q", parent.String(), "1.3.6.1.2")
	}

	// Mutating parent must not affect original (defensive copy contract).
	_ = parent.Child(99)
	if oid.String() != "1.3.6.1.2.1" {
		t.Errorf("original mutated after Parent()+Child(): %q", oid.String())
	}

	single, _ := ParseOID("1")
	if _, ok := single.Parent(); ok {
		t.Error("Parent() of single-element OID returned ok=true")
	}

	empty := OID{}
	if _, ok := empty.Parent(); ok {
		t.Error("Parent() of empty OID returned ok=true")
	}
}

func TestOID_Child(t *testing.T) {
	parent, _ := ParseOID("1.3.6.1.2.1")
	child := parent.Child(42)
	if child.String() != "1.3.6.1.2.1.42" {
		t.Errorf("Child(42) = %q, want %q", child.String(), "1.3.6.1.2.1.42")
	}
	// Parent must be unchanged.
	if parent.String() != "1.3.6.1.2.1" {
		t.Errorf("parent mutated by Child(): %q", parent.String())
	}

	// From empty.
	root := OID{}.Child(1)
	if root.String() != "1" {
		t.Errorf("Child from empty = %q, want %q", root.String(), "1")
	}
}

func TestOID_Append(t *testing.T) {
	base, _ := ParseOID("1.3.6")
	got := base.Append(1, 4, 1, 9)
	want := "1.3.6.1.4.1.9"
	if got.String() != want {
		t.Errorf("Append = %q, want %q", got.String(), want)
	}
	if base.String() != "1.3.6" {
		t.Errorf("base mutated by Append: %q", base.String())
	}
	// Zero-arg Append returns a value equal to the base.
	same := base.Append()
	if !same.Equal(base) {
		t.Errorf("Append() with no args = %q, want %q", same.String(), base.String())
	}
}

func TestOID_Clone(t *testing.T) {
	a, _ := ParseOID("1.3.6.1")
	b := a.Clone()
	if !a.Equal(b) {
		t.Error("Clone produced unequal OID")
	}
	// Mutate b's backing slice via Child; original must be untouched.
	_ = b.Child(9)
	if a.String() != "1.3.6.1" {
		t.Errorf("original mutated after Clone+Child: %q", a.String())
	}
}

func TestOID_String_Empty(t *testing.T) {
	var empty OID
	if got := empty.String(); got != "" {
		t.Errorf("empty OID String() = %q, want empty", got)
	}
}

func TestParseOID_ErrorMessage(t *testing.T) {
	_, err := ParseOID("1.x.3")
	if err == nil {
		t.Fatal("expected error")
	}
	// Message should mention the offending input or the word "OID" for ergonomics.
	if msg := err.Error(); !strings.Contains(msg, "OID") && !strings.Contains(msg, "oid") {
		t.Errorf("error message %q should reference OID", msg)
	}
}
