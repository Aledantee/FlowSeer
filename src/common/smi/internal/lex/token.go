package lex

import "strconv"

// Kind classifies a token. Reserved words all share [KindKeyword] and
// are told apart by [Token.Keyword], so this set stays small enough to
// switch on in a parser without a hundred-arm statement.
type Kind uint8

// The token kinds. [KindInvalid] is the zero value and the lexer never
// produces it, so a Token read out of an empty slot is recognizable as
// one. [KindUnknown] is different: it is a byte the lexer read and could
// not classify, kept as a token so the input still tiles and so a later
// pass can report it in context.
const (
	KindInvalid Kind = iota
	KindUnknown
	KindIdentifier
	KindTypeReference
	KindKeyword
	KindNumber
	KindQuotedString
	KindHexString
	KindBinaryString
	KindLeftBrace
	KindRightBrace
	KindLeftParen
	KindRightParen
	KindLeftBracket
	KindRightBracket
	KindComma
	KindSemicolon
	KindBar
	KindMinus
	KindDot
	KindRange
	KindAssign
)

// kindNames are the strings a kind renders as in test failures and
// diagnostics. They are not a wire contract and may be reworded.
var kindNames = [...]string{
	KindInvalid:       "invalid",
	KindUnknown:       "unknown",
	KindIdentifier:    "identifier",
	KindTypeReference: "typereference",
	KindKeyword:       "keyword",
	KindNumber:        "number",
	KindQuotedString:  "string",
	KindHexString:     "hexstring",
	KindBinaryString:  "binstring",
	KindLeftBrace:     "{",
	KindRightBrace:    "}",
	KindLeftParen:     "(",
	KindRightParen:    ")",
	KindLeftBracket:   "[",
	KindRightBracket:  "]",
	KindComma:         ",",
	KindSemicolon:     ";",
	KindBar:           "|",
	KindMinus:         "-",
	KindDot:           ".",
	KindRange:         "..",
	KindAssign:        "::=",
}

// String returns the kind's name, or "kind(N)" for a value off the set.
func (k Kind) String() string {
	if int(k) >= len(kindNames) {
		return "kind(" + strconv.Itoa(int(k)) + ")"
	}

	return kindNames[k]
}

// Token is one significant piece of a source file.
//
// A token carries byte offsets and nothing else: no string, no pointer,
// no reference to the file it came from. A vendor corpus produces tokens
// by the ten million, so the type is sized to sit in a slab, and its
// text is materialized from the source only when somebody asks for it
// through [Result.Text] or [Result.Content]. Line and column are not
// stored either; they come from the file's line table at the moment a
// diagnostic is rendered.
//
// Whitespace and comments produce no tokens. PrevEnd is where the
// preceding token ended, so the bytes in [PrevEnd, Offset) are exactly
// the trivia before this token and the tokens plus those gaps tile the
// file with nothing left over. The first token's PrevEnd is 0, which
// puts a leading byte-order mark or comment in its gap.
//
// Keyword is meaningful only when Kind is [KindKeyword]; it is
// [KeywordNone] otherwise.
type Token struct {
	Kind    Kind
	Keyword Keyword
	Offset  int32
	Length  int32
	PrevEnd int32
}

// End returns the offset one past the token's last byte.
func (t Token) End() int32 { return t.Offset + t.Length }
