package parse

import (
	"strconv"

	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

// reader walks the tokens of one clause's payload.
//
// The clause parsers hand it the exact token range they consumed, which
// is what keeps the value grammar out of the clause-boundary logic: a
// reader cannot run past the clause it was given, so nothing here needs
// to know what ends one. Diagnostics go through the parser, so the
// per-line throttle and the diagnostic limit cover the value grammar
// without it knowing they exist.
type reader struct {
	p    *parser
	toks []lex.Token
	pos  int
}

func (r *reader) more() bool { return r.pos < len(r.toks) }

func (r *reader) tok() lex.Token {
	if !r.more() {
		return lex.Token{}
	}

	return r.toks[r.pos]
}

func (r *reader) next() { r.pos++ }

func (r *reader) at(k lex.Kind) bool { return r.more() && r.toks[r.pos].Kind == k }

func (r *reader) keyword() lex.Keyword {
	t := r.tok()
	if t.Kind != lex.KindKeyword {
		return lex.KeywordNone
	}

	return t.Keyword
}

func (r *reader) isName() bool {
	k := r.tok().Kind

	return k == lex.KindIdentifier || k == lex.KindTypeReference
}

func (r *reader) span() Span {
	t := r.tok()

	return Span{Start: t.Offset, End: t.End()}
}

// integer reads an optionally signed decimal literal. A literal too
// large for an int64 saturates rather than failing, so the rule that
// checks a value against its base type is the one that reports it.
func (r *reader) integer() (int64, Span, bool) {
	span := r.span()
	negative := false

	if r.at(lex.KindMinus) {
		negative = true
		r.next()
		if !r.at(lex.KindNumber) {
			return 0, span, false
		}
		extend(&span, r.tok().End())
	}
	if !r.at(lex.KindNumber) {
		return 0, span, false
	}

	text := r.p.res.Text(r.tok())
	span = Span{Start: span.Start, End: r.tok().End()}
	r.next()

	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		n = maxInt64
	}
	if negative {
		n = -n
	}

	return n, span, true
}

// maxInt64 is what an over-long decimal literal saturates to.
const maxInt64 = 1<<63 - 1

// skipGroup consumes one balanced bracket group, positioned at its
// opening bracket. It is how the type reader steps over a construct it
// has no use for, such as an ASN.1 tag or a SEQUENCE body.
func (r *reader) skipGroup() {
	depth := 0

	for r.more() {
		switch r.tok().Kind {
		case lex.KindLeftBrace, lex.KindLeftParen, lex.KindLeftBracket:
			depth++
		case lex.KindRightBrace, lex.KindRightParen, lex.KindRightBracket:
			depth--
		}
		r.next()

		if depth == 0 {
			return
		}
	}
}

// ValueKind tells one shape of default value from another. It is the
// shape the source wrote, not the type the value belongs to: which of
// RFC 2578 §7.9's rows a default satisfies is a judgement that needs the
// SYNTAX clause, and the SYNTAX clause is on the declaration.
type ValueKind uint8

// The value shapes RFC 2578 §7.9 admits.
const (
	// ValueNone is a DEFVAL whose payload read as nothing usable.
	ValueNone ValueKind = iota

	// ValueInteger is a signed decimal literal.
	ValueInteger

	// ValueLabel is a bare descriptor: an enumeration's label, or the
	// name of an OBJECT IDENTIFIER this pass does not resolve.
	ValueLabel

	// ValueString is a quoted string.
	ValueString

	// ValueOctets is a hexadecimal or binary literal.
	ValueOctets

	// ValueOID is an object identifier written as a sub-identifier list.
	ValueOID

	// ValueBits is a set of bit names, which is empty for the "{ { } }"
	// the RFC spells out for a default of no bits at all.
	ValueBits
)

// valueKindNames are what a value shape renders as in a test failure.
var valueKindNames = [...]string{
	ValueNone:    "none",
	ValueInteger: "integer",
	ValueLabel:   "label",
	ValueString:  "string",
	ValueOctets:  "octets",
	ValueOID:     "oid",
	ValueBits:    "bits",
}

// String returns the shape's name, or "value(N)" off the set.
func (k ValueKind) String() string {
	if int(k) >= len(valueKindNames) {
		return "value(" + strconv.Itoa(int(k)) + ")"
	}

	return valueKindNames[k]
}

// Value is a DEFVAL or a VARIATION default read per RFC 2578 §7.9. The
// two clauses share this type because they share the production.
//
// Only the fields Kind names are filled in. Octets carries the decoded
// bytes of a string, hexadecimal or binary default, since that is the
// value the object takes rather than the spelling it was written with;
// the spelling is still in Span.
type Value struct {
	Kind ValueKind
	Span Span

	// Number is the value of a [ValueInteger].
	Number int64

	// Name is the descriptor of a [ValueLabel].
	Name Span

	// Octets are the bytes of a [ValueString] or a [ValueOctets].
	Octets []byte

	// Subs are the sub-identifiers of a [ValueOID].
	Subs []int64

	// Bits are the member names of a [ValueBits], in source order.
	Bits []Span
}

// parseDefault reads the tokens a DEFVAL or VARIATION default covers.
//
// base is the SYNTAX clause's base type, which decides how a nested
// brace group reads: BITS writes its default as a list of names and an
// OBJECT IDENTIFIER is sometimes written as a list of numbers, and the
// two are told apart by the type when it is known and by their contents
// when it is not.
func (p *parser) parseDefault(toks []lex.Token, base BaseType) Value {
	r := reader{p: p, toks: toks}

	return r.defaultValue(base)
}

func (r *reader) defaultValue(base BaseType) Value {
	if !r.at(lex.KindLeftBrace) || len(r.toks) == 0 {
		return Value{}
	}

	v := Value{Span: Span{Start: r.toks[0].Offset, End: r.toks[len(r.toks)-1].End()}}
	r.next()

	switch {
	case r.at(lex.KindLeftBrace):
		r.nestedDefault(&v, base)
	case r.at(lex.KindQuotedString):
		v.Kind = ValueString
		v.Octets = r.p.res.Content(r.tok())
		r.next()
	case r.at(lex.KindHexString):
		v.Kind = ValueOctets
		v.Octets = r.hexOctets()
	case r.at(lex.KindBinaryString):
		v.Kind = ValueOctets
		v.Octets = r.binaryOctets()
	case r.at(lex.KindNumber), r.at(lex.KindMinus):
		if n, _, ok := r.integer(); ok {
			v.Kind = ValueInteger
			v.Number = n
		}
	case r.isName():
		v.Kind = ValueLabel
		v.Name = r.span()
		r.next()
	}

	return v
}

// nestedDefault reads the "{ { ... } }" form, positioned at the inner
// brace.
//
// The inner group is a BITS default's bit list, or the sub-identifier
// list some MIBs write an OBJECT IDENTIFIER default as. RFC 2578 §7.9
// defines only the first; the second is common enough that refusing it
// would cost more objects than it would catch, so it is read and
// reported. An empty group is the "{ { } }" the RFC spells out for a
// BITS value with no bits set.
func (r *reader) nestedDefault(v *Value, base BaseType) {
	r.next()

	var (
		names []Span
		subs  []int64
	)

	for r.more() && !r.at(lex.KindRightBrace) {
		switch {
		case r.isName():
			names = append(names, r.span())
			r.next()
		case r.at(lex.KindNumber), r.at(lex.KindMinus):
			n, _, ok := r.integer()
			if ok {
				subs = append(subs, n)
			}
		default:
			r.next()
		}
	}
	if r.at(lex.KindRightBrace) {
		r.next()
	}

	if writesSubIdentifiers(base, names, subs) {
		v.Kind = ValueOID
		v.Subs = subs
		r.p.raise(v.Span.Start, diag.ErrCodeNonConformingOIDDefault)

		return
	}

	v.Kind = ValueBits
	v.Bits = names
}

// writesSubIdentifiers reports whether the inner group is an OID written
// as bare numbers. The declared type decides when it says anything, and
// the contents decide otherwise: a group of numbers is no bit list, and
// an empty group is the BITS form the RFC defines.
func writesSubIdentifiers(base BaseType, names []Span, subs []int64) bool {
	switch base {
	case BaseObjectIdentifier:
		return true
	case BaseBits:
		return false
	default:
		return len(subs) > 0 && len(names) == 0
	}
}

// hexOctets decodes a hexadecimal literal into the bytes it denotes. An
// odd digit count leaves a nibble with no byte to sit in; the lexer has
// already reported it, so the trailing nibble is dropped here rather
// than reported twice.
func (r *reader) hexOctets() []byte {
	digits := radixDigits(r.p.res.Content(r.tok()), true)
	r.next()

	out := make([]byte, 0, len(digits)/2)
	for i := 0; i+1 < len(digits); i += 2 {
		out = append(out, byte(digits[i]<<4|digits[i+1]))
	}

	return out
}

// binaryOctets decodes a binary literal into the bytes it denotes.
//
// A digit count that is not a multiple of eight leaves a short final
// octet. The bits that were written keep their places and the octet is
// filled out with zeros, which is where the same bits land in a BER
// encoding, and the shortfall is reported.
func (r *reader) binaryOctets() []byte {
	digits := radixDigits(r.p.res.Content(r.tok()), false)
	if len(digits)%8 != 0 {
		r.p.raise(r.tok().Offset, diag.ErrCodeBinaryStringNotOctets, diag.ArgInt(len(digits)))
	}
	r.next()

	out := make([]byte, (len(digits)+7)/8)
	for i, d := range digits {
		if d != 0 {
			out[i/8] |= 1 << (7 - i%8)
		}
	}

	return out
}

// radixDigits returns the literal's digits with the whitespace ASN.1
// allows between them removed.
func radixDigits(content []byte, hex bool) []int {
	out := make([]int, 0, len(content))
	for _, b := range content {
		if d, ok := radixDigit(b, hex); ok {
			out = append(out, d)
		}
	}

	return out
}

// permitsDefault reports whether RFC 2578 §7.9 defines a default for
// this base type. A counter's value only ever means the difference
// between two readings, so there is nothing for a default to say.
func (b BaseType) permitsDefault() bool {
	switch b {
	case BaseCounter32, BaseCounter64:
		return false
	default:
		return true
	}
}

// gradeDeclaration grades the clause values that only make sense read
// against each other.
//
// It runs once the clause loop is done rather than inside it because
// both rules here compare a clause with the SYNTAX clause, and a
// declaration that writes its clauses out of order still has to be
// graded on what it says rather than on the order it said it in.
func (p *parser) gradeDeclaration() {
	syntax := p.d.syntax

	if p.d.present.Has(ClauseDefval) && !syntax.Base.permitsDefault() {
		p.raise(p.d.text[ClauseDefval].Start, diag.ErrCodeDefaultNotPermitted,
			diag.ArgString(syntax.Base.String()))
	}

	if p.d.present.Has(ClauseDisplayHint) {
		if allowed, known := displayHintAllowed(syntax); known && !allowed {
			p.raise(p.d.text[ClauseDisplayHint].Start, diag.ErrCodeDisplayHintNotPermitted,
				diag.ArgString(syntax.describe()))
		}
	}
}
