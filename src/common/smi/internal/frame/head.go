package frame

import (
	"bytes"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

// Kind classifies a declaration head. It is the head, not the
// terminator, that names a declaration: several forms end at "::= { … }",
// one ends at a bare integer, one at a semicolon, and three end nowhere
// at all, so a framer that keys on the terminator mis-cuts most of the
// corpus. Values may be copied and read concurrently.
type Kind uint8

// The head kinds. [KindUnrecognized] is the zero value and covers a run
// of tokens no form claimed, which is kept as a frame so the file still
// tiles and so a corpus sweep can count what the taxonomy missed.
const (
	// KindUnrecognized preserves tokens whose declaration head is unknown.
	KindUnrecognized Kind = iota
	// KindImports names an IMPORTS list terminated by a semicolon.
	KindImports
	// KindExports names a skipped EXPORTS list terminated by a semicolon.
	KindExports
	// KindMacroDefinition names a skipped ASN.1 MACRO body ending at END.
	KindMacroDefinition
	// KindChoice names a skipped CHOICE type with a brace-delimited body.
	KindChoice
	// KindMacroInvocation names an SMI macro invocation with an assigned OID.
	KindMacroInvocation
	// KindTrapType names a TRAP-TYPE invocation with an assigned trap number.
	KindTrapType
	// KindValueAssignment names an OBJECT IDENTIFIER value assignment.
	KindValueAssignment
	// KindTypeAssignment names a type definition ending at the next head.
	KindTypeAssignment
)

// kindNames are the strings a kind renders as. They reach the committed
// head histograms, so they are as stable as those files are.
var kindNames = [...]string{
	KindUnrecognized:    "unrecognized",
	KindImports:         "imports",
	KindExports:         "exports",
	KindMacroDefinition: "macro-definition",
	KindChoice:          "choice",
	KindMacroInvocation: "macro-invocation",
	KindTrapType:        "trap-type",
	KindValueAssignment: "value-assignment",
	KindTypeAssignment:  "type-assignment",
}

// String returns the kind's stable name, or "kind(N)" for a value off
// the set.
func (k Kind) String() string {
	if int(k) >= len(kindNames) {
		return "kind(" + strconv.Itoa(int(k)) + ")"
	}

	return kindNames[k]
}

// Skipped reports whether the kind's frame carries no declaration. A
// pasted macro definition, an EXPORTS list and a CHOICE type all have to
// be consumed so the frames after them land correctly, and none of them
// defines anything the object model holds.
func (k Kind) Skipped() bool {
	return k == KindMacroDefinition || k == KindExports || k == KindChoice
}

// terminator is the shape that ends a frame, chosen by the head.
type terminator uint8

const (
	// termNextHead ends the frame where the next head begins. Type
	// assignments carry their "::=" at the front and have no terminator
	// of their own, so the following declaration is the only boundary
	// there is.
	termNextHead terminator = iota

	// termAssignValue ends the frame after the value that follows the
	// first "::=" at brace depth zero: a balanced brace group for macro
	// invocations and OBJECT IDENTIFIER value assignments, a bare integer
	// for TRAP-TYPE.
	termAssignValue

	// termBraceGroup ends the frame after the next balanced brace group,
	// which is how a CHOICE body is consumed.
	termBraceGroup

	// termSemicolon ends the frame after the first semicolon at brace
	// depth zero.
	termSemicolon

	// termEnd ends the frame after the END that closes a macro
	// definition.
	termEnd
)

// head is one recognized declaration head.
type head struct {
	kind Kind
	term terminator

	// name is the token index of the declared descriptor, or -1 for the
	// heads that name nothing.
	name int

	// body is the token index where the scan for the terminator starts.
	body int

	// module marks the module header, which opens a module rather than a
	// declaration and so is never a frame.
	module bool

	// strong marks a head unambiguous enough to bound another frame's
	// scan. A declaration whose terminator never arrives ends at the next
	// strong head instead of swallowing the rest of the module.
	strong bool
}

// macroWord is the one reserved word the lexer does not know: MACRO
// introduces a definition this parser skips wholesale, so teaching the
// keyword table about it would buy nothing and would reclassify the word
// everywhere it appears as ordinary text.
var macroWord = []byte("MACRO")

// moduleHeaderSpan bounds how far past DEFINITIONS a BEGIN may sit. The
// tag-default clauses that can intervene are three tokens at most
// ("IMPLICIT TAGS ::="); the allowance is loose because a header this
// parser fails to recognize costs the whole file.
const moduleHeaderSpan = 16

// recognize classifies the head starting at token i.
//
// The order of the tests is the grammar's, not an optimization: the
// second token decides almost everything, and the two forms that share
// the words OBJECT IDENTIFIER are told apart by whether that second
// token is the type or the assignment operator.
func (c *cutter) recognize(i int) (head, bool) {
	if i >= len(c.toks) {
		return head{}, false
	}

	switch c.keyword(i) {
	case lex.KeywordImports:
		return head{kind: KindImports, term: termSemicolon, name: -1, body: i + 1, strong: true}, true
	case lex.KeywordExports:
		return head{kind: KindExports, term: termSemicolon, name: -1, body: i + 1, strong: true}, true
	}

	// A module may redefine one of the RFC macros, in which case the name
	// in front of MACRO is a reserved word rather than a type reference.
	if c.isWord(i+1, macroWord) && (c.isName(i) || c.tokenKind(i) == lex.KindKeyword) {
		return head{kind: KindMacroDefinition, term: termEnd, name: i, body: i + 2, strong: true}, true
	}

	// SNMPv2-SMI defines Counter32, Integer32 and IpAddress, and RFC1155
	// defines IpAddress, so the name in front of a type assignment's
	// "::=" is sometimes one of the words the lexer already resolved to a
	// reserved word. Those four declarations are the only heads the whole
	// corpus has that a name-only rule misses.
	if c.tokenKind(i+1) == lex.KindAssign && (c.isName(i) || c.tokenKind(i) == lex.KindKeyword) {
		if c.keyword(i+2) == lex.KeywordChoice {
			return head{kind: KindChoice, term: termBraceGroup, name: i, body: i + 2}, true
		}

		return head{kind: KindTypeAssignment, term: termNextHead, name: i, body: i + 2}, true
	}

	if !c.isName(i) {
		return head{}, false
	}

	if c.keyword(i+1) == lex.KeywordDefinitions {
		body, ok := c.moduleBody(i + 2)
		if !ok {
			return head{}, false
		}

		return head{kind: KindUnrecognized, name: i, body: body, module: true, strong: true}, true
	}

	if kw := c.keyword(i + 1); macroKeyword(kw) {
		kind := KindMacroInvocation
		if kw == lex.KeywordTrapType {
			kind = KindTrapType
		}

		return head{kind: kind, term: termAssignValue, name: i, body: i + 2, strong: true}, true
	}

	if c.keyword(i+1) == lex.KeywordObject && c.keyword(i+2) == lex.KeywordIdentifier && c.tokenKind(i+3) == lex.KindAssign {
		return head{kind: KindValueAssignment, term: termAssignValue, name: i, body: i + 3, strong: true}, true
	}

	return head{}, false
}

// moduleBody returns the token index just past the BEGIN that opens a
// module. Reporting false rejects the header, which keeps a stray
// "DEFINITIONS" inside a description or an IMPORTS list from opening a
// module that never closes.
func (c *cutter) moduleBody(from int) (int, bool) {
	for i := from; i < len(c.toks) && i < from+moduleHeaderSpan; i++ {
		if c.keyword(i) == lex.KeywordBegin {
			return i + 1, true
		}
	}

	return 0, false
}

// macroKeyword reports whether kw is one of the SMI macros a declaration
// invokes. These are the words that make "<descriptor> <MACRO>" a head;
// every other reserved word in that position is part of some clause.
func macroKeyword(kw lex.Keyword) bool {
	switch kw {
	case lex.KeywordObjectType,
		lex.KeywordModuleIdentity,
		lex.KeywordObjectIdentity,
		lex.KeywordNotificationType,
		lex.KeywordTrapType,
		lex.KeywordObjectGroup,
		lex.KeywordNotificationGroup,
		lex.KeywordModuleCompliance,
		lex.KeywordAgentCapabilities:
		return true
	default:
		return false
	}
}

func (c *cutter) tokenKind(i int) lex.Kind {
	if i < 0 || i >= len(c.toks) {
		return lex.KindInvalid
	}

	return c.toks[i].Kind
}

func (c *cutter) keyword(i int) lex.Keyword {
	if i < 0 || i >= len(c.toks) || c.toks[i].Kind != lex.KindKeyword {
		return lex.KeywordNone
	}

	return c.toks[i].Keyword
}

func (c *cutter) isName(i int) bool {
	k := c.tokenKind(i)

	return k == lex.KindIdentifier || k == lex.KindTypeReference
}

// isWord reports whether the token at i is spelled exactly word. It
// compares the source bytes rather than interning, since the only word
// asked about is MACRO and interning it would put a string in the file's
// table for a token nothing later reads.
func (c *cutter) isWord(i int, word []byte) bool {
	if i < 0 || i >= len(c.toks) {
		return false
	}

	return bytes.Equal(c.res.Content(c.toks[i]), word)
}
