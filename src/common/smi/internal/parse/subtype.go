package parse

import (
	"math"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/smi"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

// BaseType is the SMI type a SYNTAX clause is built on, as far as one
// declaration can tell.
//
// [BaseNamed] is the zero value and covers every type this pass cannot
// classify: a textual convention, a row type, anything a MIB imported
// from elsewhere. Which base such a name eventually stands on is
// resolution's answer, and guessing it here would put a wrong base
// behind every semantic rule below.
type BaseType uint8

// The base types RFC 2578 §7.1 defines, plus the two ASN.1 constructs
// the corpus writes and the catch-all for a name.
const (
	BaseNamed BaseType = iota
	BaseInteger
	BaseInteger32
	BaseUnsigned32
	BaseGauge32
	BaseCounter32
	BaseCounter64
	BaseTimeTicks
	BaseOctetString
	BaseObjectIdentifier
	BaseBits
	BaseIPAddress
	BaseOpaque
	BaseNull
	BaseSequence
)

// baseTypeNames are what a base type is called in a diagnostic, which is
// the word the source writes it with.
var baseTypeNames = [...]string{
	BaseNamed:            "a named type",
	BaseInteger:          "INTEGER",
	BaseInteger32:        "Integer32",
	BaseUnsigned32:       "Unsigned32",
	BaseGauge32:          "Gauge32",
	BaseCounter32:        "Counter32",
	BaseCounter64:        "Counter64",
	BaseTimeTicks:        "TimeTicks",
	BaseOctetString:      "OCTET STRING",
	BaseObjectIdentifier: "OBJECT IDENTIFIER",
	BaseBits:             "BITS",
	BaseIPAddress:        "IpAddress",
	BaseOpaque:           "Opaque",
	BaseNull:             "NULL",
	BaseSequence:         "SEQUENCE",
}

// String returns the base type's name, or "base(N)" off the set.
func (b BaseType) String() string {
	if int(b) >= len(baseTypeNames) {
		return "base(" + strconv.Itoa(int(b)) + ")"
	}

	return baseTypeNames[b]
}

// Member is one named number: an entry of an enumeration, or one bit of
// a BITS type.
//
// Number is the number the MIB declared, never the member's position in
// the list. The two agree for every type numbered consecutively from
// zero and part company at the first gap, and a device reporting bit 5
// of a gapped BITS type means the member declared as 5.
type Member struct {
	Span   Span
	Name   Span
	Number int64
}

// Range is one alternative of a range or SIZE constraint. A single
// value written without "..", such as the 8 in "(SIZE (0 | 8))", has
// Min and Max equal.
type Range struct {
	Span Span
	Min  int64
	Max  int64
}

// Type is a SYNTAX clause read as a type rather than as source text.
//
// The clause's span is still on the declaration, so a diagnostic that
// quotes the file quotes the file. This is the same text read for what
// it says: which base type it stands on, the members it enumerates with
// the numbers the source gave them, and its range and SIZE constraints.
//
// Nothing here is resolved. A Name that this pass could not classify
// leaves Base as [BaseNamed], and every semantic rule below stays quiet
// for it rather than grading a constraint against a base type it
// guessed.
type Type struct {
	Span Span

	// Base is the type this one is built on, or [BaseNamed] when the
	// source names a type this pass does not define.
	Base BaseType

	// Name is the type name as written, which is what a reader of a
	// [BaseNamed] type has to work with.
	Name Span

	// Members are the enumeration entries or BITS members, in source
	// order.
	Members []Member

	// Ranges are the alternatives of a range constraint, and Sizes those
	// of a SIZE constraint. Both are in source order, so a constraint
	// that broke one of RFC 2578 §11.1's rules is still readable as
	// written.
	Ranges []Range
	Sizes  []Range
}

// Enumerated reports whether the type is an INTEGER with named numbers,
// which several RFC rules treat as a type of its own.
func (t Type) Enumerated() bool {
	switch t.Base {
	case BaseInteger, BaseInteger32, BaseUnsigned32:
		return len(t.Members) > 0
	default:
		return false
	}
}

// describe names the type in a diagnostic. An enumeration is called one,
// because "INTEGER" would send a reader looking for the rule that
// actually bit.
func (t Type) describe() string {
	if t.Enumerated() {
		return "enumerated " + t.Base.String()
	}

	return t.Base.String()
}

// limits returns the values the base type can take, and whether it has
// bounds this pass knows. Counter64 has none here on purpose: RFC 2578
// §7.1.10 forbids subtyping it at all, and its upper bound does not fit
// in the int64 a bound is read into.
func (b BaseType) limits() (low, high int64, known bool) {
	switch b {
	case BaseInteger, BaseInteger32:
		return math.MinInt32, math.MaxInt32, true
	case BaseUnsigned32, BaseGauge32, BaseCounter32, BaseTimeTicks:
		return 0, math.MaxUint32, true
	default:
		return 0, 0, false
	}
}

// maxOctetStringLength is the longest OCTET STRING RFC 2578 §7.1.2
// allows, which is what a SIZE constraint on one has to fit inside.
const maxOctetStringLength = 65535

// parseType reads the tokens a SYNTAX clause covers.
//
// The clause reader hands over the exact tokens it consumed, so nothing
// here can run past the clause and no clause-boundary logic has to know
// the value grammar exists.
func (p *parser) parseType(toks []lex.Token) Type {
	r := reader{p: p, toks: toks}

	return r.typeDescription()
}

func (r *reader) typeDescription() Type {
	if !r.more() {
		return Type{}
	}

	t := Type{Span: Span{Start: r.toks[0].Offset, End: r.toks[len(r.toks)-1].End()}}
	t.Base, t.Name = r.baseType()

	if r.at(lex.KindLeftBrace) {
		if namesMembers(t.Base) {
			t.Members = r.members()
		} else {
			r.skipGroup()
		}
	}

	if r.at(lex.KindLeftParen) {
		r.constraint(&t)
	}

	return t
}

// namesMembers reports whether a brace group after this base type is a
// named-number list. A SEQUENCE's brace group holds column definitions
// and reading those as members would invent a member per column.
func namesMembers(b BaseType) bool {
	switch b {
	case BaseInteger, BaseInteger32, BaseUnsigned32, BaseBits:
		return true
	default:
		return false
	}
}

// baseType reads the leading type name and returns which base type it
// is and the span it covers. ASN.1 tagging and the IMPLICIT and EXPLICIT
// keywords are skipped: SMI ignores them and the corpus writes them on
// the SMIv1 application types.
func (r *reader) baseType() (BaseType, Span) {
	for r.at(lex.KindLeftBracket) {
		r.skipGroup()
	}
	if kw := r.keyword(); kw == lex.KeywordImplicit || kw == lex.KeywordExplicit {
		r.next()

		return r.baseType()
	}

	name := r.span()

	switch r.keyword() {
	case lex.KeywordInteger:
		r.next()

		return BaseInteger, name
	case lex.KeywordInteger32:
		r.next()

		return BaseInteger32, name
	case lex.KeywordUnsigned32:
		r.next()

		return BaseUnsigned32, name
	case lex.KeywordGauge32:
		r.next()

		return BaseGauge32, name
	case lex.KeywordCounter32:
		r.next()

		return BaseCounter32, name
	case lex.KeywordCounter64:
		r.next()

		return BaseCounter64, name
	case lex.KeywordTimeTicks:
		r.next()

		return BaseTimeTicks, name
	case lex.KeywordIPAddress:
		r.next()

		return BaseIPAddress, name
	case lex.KeywordOpaque:
		r.next()

		return BaseOpaque, name
	case lex.KeywordNull:
		r.next()

		return BaseNull, name
	case lex.KeywordBits:
		r.next()

		return BaseBits, name
	case lex.KeywordOctet:
		return BaseOctetString, r.twoWordType(name, lex.KeywordString)
	case lex.KeywordObject:
		return BaseObjectIdentifier, r.twoWordType(name, lex.KeywordIdentifier)
	case lex.KeywordBit:
		// "BIT STRING" is what SMIv1 modules write where SMIv2 writes
		// BITS, and RFC 3584 §2.1.1 says to read the two the same way.
		return BaseBits, r.twoWordType(name, lex.KeywordString)
	case lex.KeywordSequence:
		r.next()
		if r.keyword() == lex.KeywordOf {
			r.next()
		}
		if r.isName() {
			r.next()
		}

		return BaseSequence, name
	}

	if !r.isName() {
		return BaseNamed, Span{}
	}

	text := r.p.res.Text(r.tok())
	r.next()

	// An SMIv1 module's Counter and Gauge are the same types SMIv2
	// spells Counter32 and Gauge32, so the rules below bite on both
	// spellings rather than only the modern one.
	if v2, ok := MapType(text); ok {
		switch v2 {
		case "Counter32":
			return BaseCounter32, name
		case "Gauge32":
			return BaseGauge32, name
		case "IpAddress":
			return BaseIPAddress, name
		}
	}

	return BaseNamed, name
}

// twoWordType consumes the second word of a two-word type name and
// returns the span both words cover. A source that wrote only the first
// word keeps the span it has.
func (r *reader) twoWordType(first Span, second lex.Keyword) Span {
	r.next()
	if r.keyword() != second {
		return first
	}

	span := first
	extend(&span, r.tok().End())
	r.next()

	return span
}

// members reads a named-number list, positioned at its "{".
//
// The declared number is what a member carries. Nothing here counts
// positions, because a list with a gap in it is exactly the case the
// position would be wrong for.
func (r *reader) members() []Member {
	r.next()

	var out []Member
	for r.more() && !r.at(lex.KindRightBrace) {
		switch {
		case r.at(lex.KindComma):
			r.next()
		case r.isName():
			m, ok := r.member()
			if !ok {
				continue
			}

			out = append(out, m)
			if len(out) > MaxMembers {
				r.p.limit("enumeration members", MaxMembers, m.Span.Start)
			}
		default:
			r.next()
		}
	}

	if r.at(lex.KindRightBrace) {
		r.next()
	}

	return out
}

// member reads one "name(number)" entry, positioned at the name.
//
// A name with no number after it is not a member. The number is the
// whole point of the list, and taking the name's position for it is the
// defect this parser exists to remove — so the name is reported and
// dropped instead. That also keeps a descriptor written with a character
// RFC 2578 §3.1 does not allow in one, which the lexer reads as several
// tokens, from contributing a member per fragment.
func (r *reader) member() (Member, bool) {
	m := Member{Span: r.span(), Name: r.span()}
	r.next()

	if !r.at(lex.KindLeftParen) {
		r.p.raise(m.Span.Start, smi.ErrCodeUnexpectedToken,
			smi.ArgString(r.p.spanText(m.Name)), smi.ArgString("a member number"))

		return Member{}, false
	}
	r.next()

	n, span, ok := r.integer()
	if !ok {
		r.p.raise(m.Span.Start, smi.ErrCodeUnexpectedToken,
			smi.ArgString(r.p.spanText(m.Name)), smi.ArgString("a member number"))

		return Member{}, false
	}

	m.Number = n
	extend(&m.Span, span.End)

	if r.at(lex.KindRightParen) {
		extend(&m.Span, r.tok().End())
		r.next()
	}

	return m, true
}

// constraint reads one parenthesized subtype constraint, positioned at
// its "(", and grades it against RFC 2578 §11.1.
func (r *reader) constraint(t *Type) {
	r.next()

	if r.keyword() == lex.KeywordSize {
		r.next()
		if r.at(lex.KindLeftParen) {
			r.next()
			t.Sizes = r.alternatives(t, true)
			r.closeParen()
		}
	} else {
		t.Ranges = r.alternatives(t, false)
	}

	r.closeParen()
}

func (r *reader) closeParen() {
	if r.at(lex.KindRightParen) {
		r.next()
	}
}

// alternatives reads the "a..b | c..d" list inside a constraint and
// grades each alternative as it arrives.
//
// Every alternative that read as a range is kept even when it broke a
// rule, because a constraint the RFC forbids still says what its author
// wrote and dropping it would leave a type looking unconstrained.
func (r *reader) alternatives(t *Type, size bool) []Range {
	var out []Range

	for r.more() && !r.at(lex.KindRightParen) {
		if r.at(lex.KindBar) {
			r.next()

			continue
		}

		rg, ok := r.rangeItem()
		if !ok {
			r.next()

			continue
		}

		var prev *Range
		if len(out) > 0 {
			prev = &out[len(out)-1]
		}
		r.gradeRange(t, rg, prev, size)

		out = append(out, rg)
	}

	return out
}

// rangeItem reads one bound or one "low..high" pair.
func (r *reader) rangeItem() (Range, bool) {
	low, span, ok := r.bound()
	if !ok {
		return Range{}, false
	}

	rg := Range{Span: span, Min: low, Max: low}
	if !r.at(lex.KindRange) {
		return rg, true
	}
	r.next()

	high, highSpan, ok := r.bound()
	if !ok {
		return rg, true
	}
	rg.Max = high
	extend(&rg.Span, highSpan.End)

	return rg, true
}

// bound reads one constraint bound: a signed integer, a hexadecimal or
// binary literal, or the MIN and MAX keywords ASN.1 allows in place of
// the base type's own limits.
func (r *reader) bound() (int64, Span, bool) {
	switch r.keyword() {
	case lex.KeywordMin:
		span := r.span()
		r.next()

		return math.MinInt64, span, true
	case lex.KeywordMax:
		span := r.span()
		r.next()

		return math.MaxInt64, span, true
	}

	if r.at(lex.KindHexString) || r.at(lex.KindBinaryString) {
		span := r.span()
		n := radixValue(r.p.res.Content(r.tok()), r.at(lex.KindHexString))
		r.next()

		return n, span, true
	}

	return r.integer()
}

// radixValue reads a hexadecimal or binary literal as the number a
// constraint bound means by it. Digits past what an int64 holds
// saturate, which leaves the containment rule below to report a bound no
// base type could take rather than silently wrapping it.
func radixValue(digits []byte, hex bool) int64 {
	base := int64(2)
	if hex {
		base = 16
	}

	var n int64
	for _, b := range digits {
		d, ok := radixDigit(b, hex)
		if !ok {
			continue
		}
		if n > (math.MaxInt64-int64(d))/base {
			return math.MaxInt64
		}
		n = n*base + int64(d)
	}

	return n
}

func radixDigit(b byte, hex bool) (int, bool) {
	switch {
	case b >= '0' && b <= '1':
		return int(b - '0'), true
	case b >= '2' && b <= '9':
		return int(b - '0'), hex
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, hex
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, hex
	default:
		return 0, false
	}
}

// gradeRange applies the four rules RFC 2578 §11.1 states about a
// constraint: a size is never negative, a range ascends, alternatives do
// not overlap though they may touch, and every bound is one the base
// type could take.
func (r *reader) gradeRange(t *Type, rg Range, prev *Range, size bool) {
	if size && (rg.Min < 0 || rg.Max < 0) {
		negative := rg.Min
		if negative >= 0 {
			negative = rg.Max
		}
		r.p.raise(rg.Span.Start, smi.ErrCodeNegativeSize, smi.ArgInt(int(negative)))
	}

	if rg.Min > rg.Max {
		r.p.raise(rg.Span.Start, smi.ErrCodeRangeNotAscending, smi.ArgInt(int(rg.Min)), smi.ArgInt(int(rg.Max)))
	}

	if prev != nil && rg.Min <= prev.Max {
		r.p.raise(rg.Span.Start, smi.ErrCodeOverlappingRange,
			smi.ArgInt(int(rg.Min)), smi.ArgInt(int(rg.Max)),
			smi.ArgInt(int(prev.Min)), smi.ArgInt(int(prev.Max)))
	}

	low, high, known := containment(t.Base, size)
	if !known || rg.Min == math.MinInt64 || rg.Max == math.MaxInt64 {
		return
	}
	if rg.Min < low || rg.Max > high {
		r.p.raise(rg.Span.Start, smi.ErrCodeRangeOutsideBaseType,
			smi.ArgString(squeezeSpaces(r.p.spanText(rg.Span))), smi.ArgString(t.Base.String()))
	}
}

// containment returns the values a constraint on this base type may
// name. A SIZE names a length rather than a value, so it is bounded by
// how long the base type's strings may be.
func containment(b BaseType, size bool) (low, high int64, known bool) {
	if !size {
		return b.limits()
	}
	if b == BaseOctetString {
		return 0, maxOctetStringLength, true
	}

	return 0, 0, false
}

// squeezeSpaces collapses the whitespace inside a span's text so a
// constraint written across two lines still renders on one.
func squeezeSpaces(s string) string {
	out := make([]byte, 0, len(s))
	space := false

	for i := range len(s) {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
			space = len(out) > 0

			continue
		}
		if space {
			out = append(out, ' ')
			space = false
		}
		out = append(out, s[i])
	}

	return string(out)
}
