package parse

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi"
)

// hintOf parses one textual convention and returns the hint it read.
func hintOf(t *testing.T, syntax, hint string, want ...errs.Code) DisplayHint {
	t.Helper()

	r := parseSource(t, wrap(`
TestConvention ::= TEXTUAL-CONVENTION
    DISPLAY-HINT "`+hint+`"
    STATUS       current
    DESCRIPTION  "the convention under test"
    SYNTAX       `+syntax+`
`))
	wantCodes(t, r, want...)
	m := module(t, r)

	if len(m.TextualConventions) != 1 {
		t.Fatalf("got %d textual conventions, want 1", len(m.TextualConventions))
	}

	return m.TextualConventions[0].Hint
}

func wantSpecs(t *testing.T, got []HintSpec, want ...HintSpec) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d specifications, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("specification %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The hints below are the ones RFC 2579 uses as its own examples, plus
// DateAndTime's, which is the one that exercises every part of the
// grammar at once.
func TestOctetStringHints(t *testing.T) {
	t.Run("plain ASCII run", func(t *testing.T) {
		h := hintOf(t, "OCTET STRING (SIZE (0..255))", "255a")
		if h.Kind != HintOctets {
			t.Fatalf("hint reads as %v, want %v", h.Kind, HintOctets)
		}
		wantSpecs(t, h.Specs, HintSpec{Length: 255, Format: 'a'})
	})

	t.Run("separated octets", func(t *testing.T) {
		h := hintOf(t, "OCTET STRING (SIZE (6))", "1x:")
		wantSpecs(t, h.Specs, HintSpec{Length: 1, Format: 'x', Separator: ':'})
	})

	t.Run("DateAndTime", func(t *testing.T) {
		h := hintOf(t, "OCTET STRING (SIZE (8 | 11))", "2d-1d-1d,1d:1d:1d.1d,1a1d:1d")
		wantSpecs(t, h.Specs,
			HintSpec{Length: 2, Format: 'd', Separator: '-'},
			HintSpec{Length: 1, Format: 'd', Separator: '-'},
			HintSpec{Length: 1, Format: 'd', Separator: ','},
			HintSpec{Length: 1, Format: 'd', Separator: ':'},
			HintSpec{Length: 1, Format: 'd', Separator: ':'},
			HintSpec{Length: 1, Format: 'd', Separator: '.'},
			HintSpec{Length: 1, Format: 'd', Separator: ','},
			HintSpec{Length: 1, Format: 'a'},
			HintSpec{Length: 1, Format: 'd', Separator: ':'},
			HintSpec{Length: 1, Format: 'd'},
		)
	})

	// The repeat form takes its count from the first octet and closes
	// the group with a terminator, which only this form may carry.
	t.Run("repeated group", func(t *testing.T) {
		h := hintOf(t, "OCTET STRING", "*2d.;")
		wantSpecs(t, h.Specs, HintSpec{Repeat: true, Length: 2, Format: 'd', Separator: '.', Terminator: ';'})
	})
}

func TestIntegerHints(t *testing.T) {
	t.Run("implied decimal places", func(t *testing.T) {
		h := hintOf(t, "Integer32", "d-2")
		if h.Kind != HintInteger {
			t.Fatalf("hint reads as %v, want %v", h.Kind, HintInteger)
		}
		if h.Format != 'd' || h.Decimals != 2 {
			t.Errorf("hint renders as %q with %d decimal places, want 'd' with 2", h.Format, h.Decimals)
		}
	})

	t.Run("plain base", func(t *testing.T) {
		h := hintOf(t, "Integer32", "x")
		if h.Kind != HintInteger || h.Format != 'x' || h.Decimals != 0 {
			t.Errorf("hint reads as %v %q with %d decimal places, want an integer 'x' with none", h.Kind, h.Format, h.Decimals)
		}
	})

	t.Run("no such base", func(t *testing.T) {
		h := hintOf(t, "Integer32", "z", smi.ErrCodeDisplayHintMalformed)
		if h.Kind != HintNone {
			t.Errorf("hint reads as %v, want %v", h.Kind, HintNone)
		}
	})
}

// A separator has to be told apart from the octet count or the repeat
// indicator of whatever follows it, which is why RFC 2579 bars a decimal
// digit and "*" from the position.
func TestSeparatorMustNotBeADigitOrRepeatIndicator(t *testing.T) {
	t.Run("digit", func(t *testing.T) {
		h := hintOf(t, "OCTET STRING", "1d5", smi.ErrCodeDisplayHintSeparator)
		wantSpecs(t, h.Specs, HintSpec{Length: 1, Format: 'd'})
	})

	t.Run("repeat indicator", func(t *testing.T) {
		h := hintOf(t, "OCTET STRING", "1d*", smi.ErrCodeDisplayHintSeparator)
		wantSpecs(t, h.Specs, HintSpec{Length: 1, Format: 'd'})
	})
}

// RFC 2579 §3.1 gives hint forms only for a non-enumerated INTEGER,
// Integer32 or Unsigned32 and for an OCTET STRING. On anything else
// there is nothing for the hint to act on.
func TestDisplayHintNotPermitted(t *testing.T) {
	t.Run("enumerated", func(t *testing.T) {
		hintOf(t, "INTEGER { up(1), down(2) }", "d", smi.ErrCodeDisplayHintNotPermitted)
	})

	t.Run("counter", func(t *testing.T) {
		hintOf(t, "Counter32", "d", smi.ErrCodeDisplayHintNotPermitted)
	})
}

// A textual convention whose syntax is another convention draws nothing:
// what base that one stands on is resolution's answer.
func TestDisplayHintOnANamedSyntaxIsNotGraded(t *testing.T) {
	h := hintOf(t, "DisplayString", "255a")

	if h.Kind != HintOctets {
		t.Errorf("hint reads as %v, want %v", h.Kind, HintOctets)
	}
}
