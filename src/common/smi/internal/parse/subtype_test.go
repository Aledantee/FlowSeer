package parse

import (
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi"
)

// syntaxOf parses one object with the given SYNTAX clause and returns
// the type it read.
func syntaxOf(t *testing.T, syntax string, want ...errs.Code) Type {
	t.Helper()

	r := parseSource(t, wrap(`
testObject OBJECT-TYPE
    SYNTAX      `+syntax+`
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "the object under test"
    ::= { testMIB 1 }
`))
	wantCodes(t, r, want...)
	m := module(t, r)

	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}

	return m.ObjectTypes[0].SyntaxType
}

func wantRanges(t *testing.T, got []Range, want ...[2]int64) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d ranges, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Min != w[0] || got[i].Max != w[1] {
			t.Errorf("range %d is %d..%d, want %d..%d", i, got[i].Min, got[i].Max, w[0], w[1])
		}
	}
}

// RFC 2578 §11.1 lets range alternatives touch but not overlap, so the
// pair the rule bites on and the pair a MIB writes routinely are only
// one value apart.
func TestOverlappingRangesAreReported(t *testing.T) {
	overlapping := syntaxOf(t, "Integer32 (1..4 | 4..9)", smi.ErrCodeOverlappingRange)
	wantRanges(t, overlapping.Ranges, [2]int64{1, 4}, [2]int64{4, 9})

	touching := syntaxOf(t, "Integer32 (1..4 | 5..9)")
	wantRanges(t, touching.Ranges, [2]int64{1, 4}, [2]int64{5, 9})
}

func TestDescendingRangeIsReported(t *testing.T) {
	syntaxOf(t, "Integer32 (9..1)", smi.ErrCodeRangeNotAscending)
}

func TestNegativeSizeIsReported(t *testing.T) {
	syn := syntaxOf(t, "OCTET STRING (SIZE (-1..4))", smi.ErrCodeNegativeSize)
	wantRanges(t, syn.Sizes, [2]int64{-1, 4})
}

// A bound the base type could never take leaves the constraint
// describing values the object cannot carry, whether it is a range on an
// integer or a length on a string.
func TestRangeOutsideTheBaseTypeIsReported(t *testing.T) {
	value := syntaxOf(t, "Integer32 (0..5000000000)", smi.ErrCodeRangeOutsideBaseType)
	wantRanges(t, value.Ranges, [2]int64{0, 5000000000})

	length := syntaxOf(t, "OCTET STRING (SIZE (0..70000))", smi.ErrCodeRangeOutsideBaseType)
	wantRanges(t, length.Sizes, [2]int64{0, 70000})
}

// A type this pass cannot classify draws no containment diagnostic: what
// base a textual convention stands on is resolution's answer.
func TestNamedTypeIsNotGradedForContainment(t *testing.T) {
	syn := syntaxOf(t, "DisplayString (SIZE (0..255))")

	if syn.Base != BaseNamed {
		t.Errorf("SYNTAX reads as %v, want %v", syn.Base, BaseNamed)
	}
	wantRanges(t, syn.Sizes, [2]int64{0, 255})
}

// A member with no number is dropped rather than numbered by where it
// sits, which is the whole point of reading the declared number. The
// corpus reaches this through descriptors written with an underscore,
// which RFC 2578 §3.1 does not allow in one and which the lexer
// therefore reads as several tokens: only the fragment the number
// follows would be a member, and every fragment before it would
// otherwise contribute one member too many at the wrong number.
func TestMemberWithoutANumberIsDropped(t *testing.T) {
	syn := syntaxOf(t, "BITS { alpha, gamma(4) }", smi.ErrCodeUnexpectedToken)

	if len(syn.Members) != 1 {
		t.Fatalf("got %d members, want 1", len(syn.Members))
	}
	if syn.Members[0].Number != 4 {
		t.Errorf("the surviving member carries %d, want 4", syn.Members[0].Number)
	}
}

// enumeration builds a named-number list of n members, which is how the
// corpus's largest enumeration and the one member past the limit are
// both written without a fixture file.
func enumeration(n int) string {
	var b strings.Builder

	b.WriteString("INTEGER {")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("m")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("(")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(")")
	}
	b.WriteString(" }")

	return b.String()
}

// The corpus's largest enumeration has 5,965 members, in
// spec/mib/huawei/HUAWEI-TC-MIB, so the limit has to sit well above it.
func TestCorpusSizedEnumerationParses(t *testing.T) {
	syn := syntaxOf(t, enumeration(5965))

	if len(syn.Members) != 5965 {
		t.Fatalf("got %d members, want 5965", len(syn.Members))
	}
	if last := syn.Members[5964]; last.Number != 5964 {
		t.Errorf("the last member carries %d, want 5964", last.Number)
	}
}

// A list past the member limit is not something anybody wrote, so it
// costs the file rather than trading bounded work for a guess.
func TestEnumerationPastTheLimitCostsTheFile(t *testing.T) {
	r := parseSource(t, wrap(`
testObject OBJECT-TYPE
    SYNTAX      `+enumeration(MaxMembers+1)+`
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "too many"
    ::= { testMIB 1 }
`))
	wantCodes(t, r, smi.ErrCodeLimitExceeded)

	if len(r.Modules) != 0 {
		t.Errorf("got %d modules, want none", len(r.Modules))
	}
	if got := rendered(r)[0].Message; got != "enumeration members limit of 65536 exceeded" {
		t.Errorf("got message %q", got)
	}
}

// The number in a BITS member is the bit position a device reports, and
// declaration order is not that number whenever the list has a gap. A
// parser that discards the declared number decodes bit 4 as whichever
// member happens to sit fifth in the file.
func TestBitsMembersCarryTheDeclaredNumber(t *testing.T) {
	r := parseSource(t, wrap(`
alarmState OBJECT-TYPE
    SYNTAX      BITS { alpha(0), gamma(4), delta(7) }
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "which alarms are raised"
    ::= { alarms 1 }
`))
	wantCodes(t, r)
	m := module(t, r)

	if len(m.ObjectTypes) != 1 {
		t.Fatalf("got %d object types, want 1", len(m.ObjectTypes))
	}
	syntax := m.ObjectTypes[0].SyntaxType

	if syntax.Base != BaseBits {
		t.Errorf("SYNTAX reads as %v, want %v", syntax.Base, BaseBits)
	}
	if len(syntax.Members) != 3 {
		t.Fatalf("got %d members, want 3", len(syntax.Members))
	}

	want := []struct {
		name   string
		number int64
	}{{"alpha", 0}, {"gamma", 4}, {"delta", 7}}
	for i, w := range want {
		got := syntax.Members[i]
		if name := r.Text(got.Name); name != w.name {
			t.Errorf("member %d is %q, want %q", i, name, w.name)
		}
		if got.Number != w.number {
			t.Errorf("member %q carries bit %d, want %d", w.name, got.Number, w.number)
		}
	}
}
