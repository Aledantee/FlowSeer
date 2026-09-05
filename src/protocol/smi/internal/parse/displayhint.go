package parse

import (
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
)

// HintKind tells RFC 2579 §3.1's two display-hint grammars apart. They
// share no syntax at all: an integer hint is a format letter, and an
// octet-string hint is a sequence of octet counts.
type HintKind uint8

// The hint kinds.
const (
	// HintNone is a hint that read as neither grammar.
	HintNone HintKind = iota

	// HintInteger renders a whole number in one base, optionally with an
	// implied decimal point.
	HintInteger

	// HintOctets renders a string by walking it one specification at a
	// time.
	HintOctets
)

// HintSpec is one specification of an octet-string display hint: how
// many octets it consumes, how they are rendered, and what is printed
// after them.
//
// Separator and Terminator are zero when the hint wrote none.
// Terminator is only meaningful when Repeat is set, since it is what
// closes a repeated group.
type HintSpec struct {
	// Repeat marks the "*" form, where the first octet is a count of how
	// many times the rest of the specification applies.
	Repeat bool

	// Length is how many octets the specification consumes.
	Length int

	// Format is the rendering: 'a' ASCII, 't' UTF-8, 'b' binary, 'd'
	// decimal, 'o' octal, 'x' hexadecimal.
	Format byte

	Separator  byte
	Terminator byte
}

// DisplayHint is a DISPLAY-HINT clause read per RFC 2579 §3.1.
//
// A hint whose Kind is [HintNone] did not parse; the clause's span is
// still on the declaration, so the source text survives whatever this
// made of it.
type DisplayHint struct {
	Kind HintKind
	Span Span

	// Format is an integer hint's base letter: 'b', 'd', 'o' or 'x'.
	Format byte

	// Decimals is how many of a decimal integer hint's digits fall after
	// an implied decimal point, which is the "d-2" form.
	Decimals int

	// Specs are an octet-string hint's specifications, in the order they
	// apply.
	Specs []HintSpec
}

// hintFormats are the octet-string rendering letters RFC 2579 §3.1
// defines. UTF-8 is among them because RFC 2579 added it alongside the
// four ASN.1 ones.
const hintFormats = "abdotx"

// integerHintFormats are the bases an integer hint may name.
const integerHintFormats = "bdox"

// parseDisplayHint reads a DISPLAY-HINT clause's quoted text.
//
// Which of the two grammars applies is decided by the text rather than
// by the SYNTAX clause, because a textual convention writes its hint
// before its syntax and the two grammars share no first character: an
// octet-string hint starts with an octet count or a repeat indicator,
// and an integer hint starts with its base letter.
func (p *parser) parseDisplayHint(span Span, text string) DisplayHint {
	h := DisplayHint{Span: span}
	if text == "" {
		p.raise(span.Start, diag.ErrCodeDisplayHintMalformed, diag.ArgString(text))

		return h
	}

	if isHintDigit(text[0]) || text[0] == '*' {
		return p.octetHint(h, text)
	}

	return p.integerHint(h, text)
}

// integerHint reads the "d", "d-2", "x", "o" and "b" forms.
func (p *parser) integerHint(h DisplayHint, text string) DisplayHint {
	if strings.IndexByte(integerHintFormats, text[0]) < 0 {
		p.raise(h.Span.Start, diag.ErrCodeDisplayHintMalformed, diag.ArgString(text))

		return h
	}

	rest := text[1:]
	if rest == "" {
		h.Kind = HintInteger
		h.Format = text[0]

		return h
	}

	// Only the decimal form carries implied decimal places, and it
	// writes them as a hyphen and a count.
	if text[0] != 'd' || rest[0] != '-' || !allDigits(rest[1:]) {
		p.raise(h.Span.Start, diag.ErrCodeDisplayHintMalformed, diag.ArgString(text))

		return h
	}

	places, err := strconv.Atoi(rest[1:])
	if err != nil {
		p.raise(h.Span.Start, diag.ErrCodeDisplayHintMalformed, diag.ArgString(text))

		return h
	}

	h.Kind = HintInteger
	h.Format = 'd'
	h.Decimals = places

	return h
}

// octetHint reads a sequence of octet specifications.
//
// After a specification's format letter the next character is a
// separator unless it opens the specification after it, which is what
// keeps the "1a1d" in DateAndTime's hint from reading the "1" as a
// separator. A character in a separator's place that is neither a
// separator nor the start of a specification is reported: RFC 2579 bars
// a decimal digit and "*" there precisely because neither can be told
// apart from what follows a separator.
func (p *parser) octetHint(h DisplayHint, text string) DisplayHint {
	rest := text

	for rest != "" {
		spec, after, ok := readHintSpec(rest)
		if !ok {
			p.raise(h.Span.Start, diag.ErrCodeDisplayHintMalformed, diag.ArgString(text))

			return DisplayHint{Span: h.Span}
		}
		rest = after

		spec.Separator, rest = p.hintSeparator(h.Span, rest)
		if spec.Repeat && spec.Separator != 0 {
			spec.Terminator, rest = p.hintSeparator(h.Span, rest)
		}

		h.Specs = append(h.Specs, spec)
	}

	h.Kind = HintOctets

	return h
}

// hintSeparator takes the next character as a separator when one is
// there to take. A character that starts another specification is not
// one, and a character RFC 2579 bars from the position is reported and
// dropped.
func (p *parser) hintSeparator(span Span, rest string) (byte, string) {
	if rest == "" || startsHintSpec(rest) {
		return 0, rest
	}

	c := rest[0]
	if isHintDigit(c) || c == '*' {
		p.raise(span.Start, diag.ErrCodeDisplayHintSeparator, diag.ArgString(string(c)))

		return 0, rest[1:]
	}

	return c, rest[1:]
}

// readHintSpec reads one "[*]<count><format>" specification.
func readHintSpec(s string) (HintSpec, string, bool) {
	var spec HintSpec

	if s != "" && s[0] == '*' {
		spec.Repeat = true
		s = s[1:]
	}

	digits := 0
	for digits < len(s) && isHintDigit(s[digits]) {
		digits++
	}
	if digits == 0 || digits >= len(s) {
		return spec, s, false
	}

	length, err := strconv.Atoi(s[:digits])
	if err != nil || length == 0 {
		return spec, s, false
	}
	if strings.IndexByte(hintFormats, s[digits]) < 0 {
		return spec, s, false
	}

	spec.Length = length
	spec.Format = s[digits]

	return spec, s[digits+1:], true
}

// startsHintSpec reports whether s opens another specification, which is
// what decides that the character at hand is not a separator.
func startsHintSpec(s string) bool {
	_, _, ok := readHintSpec(s)

	return ok
}

// displayHintAllowed reports whether RFC 2579 §3.1 permits a hint on
// this syntax, and whether this pass knows enough to say.
//
// The RFC permits one on a non-enumerated INTEGER, Integer32 or
// Unsigned32, and on an OCTET STRING. A type this pass could not
// classify draws nothing: what base a textual convention stands on is
// resolution's answer, and reporting one here would be a guess.
func displayHintAllowed(t Type) (allowed, known bool) {
	switch t.Base {
	case BaseNamed:
		return false, false
	case BaseInteger, BaseInteger32, BaseUnsigned32:
		return !t.Enumerated(), true
	case BaseOctetString, BaseOpaque:
		return true, true
	default:
		return false, true
	}
}

func isHintDigit(b byte) bool { return b >= '0' && b <= '9' }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isHintDigit(s[i]) {
			return false
		}
	}

	return true
}
