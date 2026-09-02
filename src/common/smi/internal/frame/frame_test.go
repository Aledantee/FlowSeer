package frame

import (
	"fmt"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

// shape is a frame reduced to what a framing test cares about: what the
// head was classified as, what it named, and the exact source the frame
// claims. The source text is in the table on purpose — a boundary that
// moves by one token is the whole failure mode this package exists to
// prevent, and only the text makes that visible in a diff.
type shape struct {
	kind Kind
	name string
	text string
}

func shapes(t *testing.T, f *File, module int) []shape {
	t.Helper()

	if module >= len(f.Modules) {
		t.Fatalf("no module %d; the file has %d", module, len(f.Modules))
	}

	frames := f.Modules[module].Frames
	out := make([]shape, 0, len(frames))
	for _, fr := range frames {
		out = append(out, shape{kind: fr.Kind, name: fr.Name, text: squeeze(f.Text(fr.Span))})
	}

	return out
}

// squeeze collapses runs of whitespace so a table entry can be written on
// one line while the fixture keeps its real line breaks.
func squeeze(s string) string { return strings.Join(strings.Fields(s), " ") }

func codes(f *File) []errs.Code {
	out := make([]errs.Code, 0, len(f.Diagnostics))
	for _, d := range f.Diagnostics {
		out = append(out, d.Code())
	}

	return out
}

func wantShapes(t *testing.T, f *File, module int, want ...shape) {
	t.Helper()

	got := shapes(t, f, module)
	if len(got) != len(want) {
		t.Fatalf("module %d: got %d frames %v, want %d %v", module, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("module %d frame %d:\n got %+v\nwant %+v", module, i, got[i], want[i])
		}
	}
}

func wantCodes(t *testing.T, f *File, want ...errs.Code) {
	t.Helper()

	got := codes(f)
	if len(got) != len(want) {
		t.Fatalf("got diagnostics %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("diagnostic %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// checkTiling checks the property every later pass depends on: frames
// sit inside their module in source order, never overlap, and never
// reach outside the file. A parser recovering to the end of its own
// frame is only bounded if the frames really are disjoint.
//
// It returns an error rather than failing a test so the corpus sweep can
// run it from a worker goroutine.
func checkTiling(size int, f *File) error {
	prevModuleEnd := int32(0)
	for m, mod := range f.Modules {
		switch {
		case mod.Span.Start < prevModuleEnd:
			return fmt.Errorf("module %d starts at %d, inside the previous module", m, mod.Span.Start)
		case mod.Span.End > int32(size):
			return fmt.Errorf("module %d ends at %d past source length %d", m, mod.Span.End, size)
		case mod.Span.Start >= mod.Span.End:
			return fmt.Errorf("module %d spans %d..%d", m, mod.Span.Start, mod.Span.End)
		}
		prevModuleEnd = mod.Span.End

		prevFrameEnd := mod.Span.Start
		for i, fr := range mod.Frames {
			switch {
			case fr.Span.Start < prevFrameEnd:
				return fmt.Errorf("module %d frame %d starts at %d, inside the previous frame", m, i, fr.Span.Start)
			case fr.Span.End > mod.Span.End:
				return fmt.Errorf("module %d frame %d ends at %d past its module", m, i, fr.Span.End)
			case fr.Span.Start >= fr.Span.End:
				return fmt.Errorf("module %d frame %d spans %d..%d", m, i, fr.Span.Start, fr.Span.End)
			case len(fr.Tokens) == 0:
				return fmt.Errorf("module %d frame %d carries no tokens", m, i)
			case fr.Tokens[0].Offset != fr.Span.Start:
				return fmt.Errorf("module %d frame %d: first token at %d, span starts at %d", m, i, fr.Tokens[0].Offset, fr.Span.Start)
			case fr.Tokens[len(fr.Tokens)-1].End() != fr.Span.End:
				return fmt.Errorf("module %d frame %d: last token ends at %d, span ends at %d", m, i, fr.Tokens[len(fr.Tokens)-1].End(), fr.Span.End)
			}
			prevFrameEnd = fr.Span.End
		}
	}

	return nil
}

func wantTiling(t *testing.T, src string, f *File) {
	t.Helper()

	if err := checkTiling(len(src), f); err != nil {
		t.Fatal(err)
	}
}

func cut(t *testing.T, src string) *File {
	t.Helper()

	f := Cut([]byte(src), Options{File: "test.mib"})
	wantTiling(t, src, f)

	return f
}

// inModule wraps a body in the smallest module header the framer accepts,
// so a table of declarations reads as a table of declarations.
func inModule(body string) string {
	return "TEST-MIB DEFINITIONS ::= BEGIN\n" + body + "\nEND\n"
}

// A TRAP-TYPE ends at a bare integer with no braces anywhere in sight, so
// a framer that syncs on "::= { ... }" swallows whatever follows it.
func TestTrapTypeEndsAtBareInteger(t *testing.T) {
	src := inModule(`linkDown TRAP-TYPE
	ENTERPRISE snmp
	VARIABLES { ifIndex }
	DESCRIPTION "A linkDown trap."
	::= 2

linkUp TRAP-TYPE
	ENTERPRISE snmp
	::= 3`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindTrapType, "linkDown", `linkDown TRAP-TYPE ENTERPRISE snmp VARIABLES { ifIndex } DESCRIPTION "A linkDown trap." ::= 2`},
		shape{KindTrapType, "linkUp", `linkUp TRAP-TYPE ENTERPRISE snmp ::= 3`},
	)
}

// The two forms share the prefix "OBJECT IDENTIFIER" and mean opposite
// things: one assigns a node in the tree, the other names a type. The
// second token is what tells them apart.
func TestObjectIdentifierValueAndTypeAssignmentsFrameApart(t *testing.T) {
	src := inModule(`org OBJECT IDENTIFIER ::= { iso 3 }
dod OBJECT IDENTIFIER ::= { org 6 }
ObjectName ::= OBJECT IDENTIFIER`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindValueAssignment, "org", "org OBJECT IDENTIFIER ::= { iso 3 }"},
		shape{KindValueAssignment, "dod", "dod OBJECT IDENTIFIER ::= { org 6 }"},
		shape{KindTypeAssignment, "ObjectName", "ObjectName ::= OBJECT IDENTIFIER"},
	)
}

// A type assignment has no terminator at all, so its frame can only end
// where the next head begins.
func TestTypeAssignmentEndsBeforeNextHead(t *testing.T) {
	src := inModule(`DisplayString ::= OCTET STRING (SIZE (0..255))
sysDescr OBJECT-TYPE
	SYNTAX DisplayString
	::= { system 1 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindTypeAssignment, "DisplayString", "DisplayString ::= OCTET STRING (SIZE (0..255))"},
		shape{KindMacroInvocation, "sysDescr", "sysDescr OBJECT-TYPE SYNTAX DisplayString ::= { system 1 }"},
	)
}

func TestSequenceBracesDoNotSplitAFrame(t *testing.T) {
	src := inModule(`FooEntry ::= SEQUENCE { a INTEGER, b OCTET STRING }
BarEntry ::= SEQUENCE { c INTEGER }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindTypeAssignment, "FooEntry", "FooEntry ::= SEQUENCE { a INTEGER, b OCTET STRING }"},
		shape{KindTypeAssignment, "BarEntry", "BarEntry ::= SEQUENCE { c INTEGER }"},
	)
}

// A textual convention carries "::=" at the front, so the "::=" that ends
// the OBJECT-TYPE after it is the first one a terminator-only rule sees.
func TestTextualConventionThenObjectType(t *testing.T) {
	src := inModule(`RowStatus ::= TEXTUAL-CONVENTION
	STATUS current
	DESCRIPTION "Row status."
	SYNTAX INTEGER { active(1), notInService(2) }

ifAdminStatus OBJECT-TYPE
	SYNTAX INTEGER { up(1), down(2) }
	MAX-ACCESS read-write
	::= { ifEntry 7 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindTypeAssignment, "RowStatus", `RowStatus ::= TEXTUAL-CONVENTION STATUS current DESCRIPTION "Row status." SYNTAX INTEGER { active(1), notInService(2) }`},
		shape{KindMacroInvocation, "ifAdminStatus", "ifAdminStatus OBJECT-TYPE SYNTAX INTEGER { up(1), down(2) } MAX-ACCESS read-write ::= { ifEntry 7 }"},
	)
}

// Modules that paste the RFC 2578 macro definitions in verbatim are
// common in the corpus. The body is full of "::=" and ends with its own
// END, either of which derails a framer that reads them as structure.
func TestPastedMacroIsSkipped(t *testing.T) {
	src := inModule(`OBJECT-TYPE MACRO ::=
BEGIN
	TYPE NOTATION ::= "SYNTAX" type(ObjectSyntax)
		UnitsPart
		"MAX-ACCESS" Access
	VALUE NOTATION ::= value(VALUE ObjectName)
	UnitsPart ::= "UNITS" Text | empty
END

sysUpTime OBJECT-TYPE
	SYNTAX TimeTicks
	::= { system 3 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindMacroDefinition, "OBJECT-TYPE", `OBJECT-TYPE MACRO ::= BEGIN TYPE NOTATION ::= "SYNTAX" type(ObjectSyntax) UnitsPart "MAX-ACCESS" Access VALUE NOTATION ::= value(VALUE ObjectName) UnitsPart ::= "UNITS" Text | empty END`},
		shape{KindMacroInvocation, "sysUpTime", "sysUpTime OBJECT-TYPE SYNTAX TimeTicks ::= { system 3 }"},
	)

	if !f.Modules[0].Frames[0].Kind.Skipped() {
		t.Errorf("macro definition frame is not marked skipped")
	}
	if f.Modules[0].Frames[1].Kind.Skipped() {
		t.Errorf("macro invocation frame is marked skipped")
	}
}

// libsmi skips a macro body by matching raw characters, which ends the
// skip at any "END" it sees. Skipping at token level cannot: a quoted
// END is one string token and a commented one is no token at all.
func TestMacroSkipIgnoresEndInStringAndComment(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"quoted", `	VALUE NOTATION ::= "END"`},
		{"commented", `	-- END of the notation`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := inModule("Foo MACRO ::=\nBEGIN\n" + tc.body + "\nEND\n\nsysUpTime OBJECT-TYPE\n\tSYNTAX TimeTicks\n\t::= { system 3 }")

			f := cut(t, src)

			wantCodes(t, f)
			got := shapes(t, f, 0)
			if len(got) != 2 {
				t.Fatalf("got %d frames %v, want 2", len(got), got)
			}
			if got[1].name != "sysUpTime" {
				t.Errorf("frame after the macro is %+v, want the sysUpTime declaration", got[1])
			}
		})
	}
}

// RFC 2578 §7.9 spells DEFVAL for a BITS value as a brace group inside a
// brace group, so the first closing brace is not the frame's.
func TestNestedDefvalBracesStayInOneFrame(t *testing.T) {
	src := inModule(`fooBits OBJECT-TYPE
	SYNTAX BITS { primary(0), secondary(1) }
	DEFVAL { { primary, secondary } }
	::= { foo 1 }

barBits OBJECT-TYPE
	SYNTAX BITS { none(0) }
	DEFVAL { { } }
	::= { foo 2 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindMacroInvocation, "fooBits", "fooBits OBJECT-TYPE SYNTAX BITS { primary(0), secondary(1) } DEFVAL { { primary, secondary } } ::= { foo 1 }"},
		shape{KindMacroInvocation, "barBits", "barBits OBJECT-TYPE SYNTAX BITS { none(0) } DEFVAL { { } } ::= { foo 2 }"},
	)
}

// Braces inside a DESCRIPTION are prose, and vendor prose is not
// balanced. Counting them would move every boundary after the first one.
func TestUnbalancedBraceInDescriptionIsInert(t *testing.T) {
	src := inModule(`fooCount OBJECT-TYPE
	DESCRIPTION "Written as { name value, with no closing brace."
	::= { foo 1 }

barCount OBJECT-TYPE
	::= { foo 2 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindMacroInvocation, "fooCount", `fooCount OBJECT-TYPE DESCRIPTION "Written as { name value, with no closing brace." ::= { foo 1 }`},
		shape{KindMacroInvocation, "barCount", "barCount OBJECT-TYPE ::= { foo 2 }"},
	)
}

func TestImportsFramesToItsSemicolon(t *testing.T) {
	src := inModule(`IMPORTS
	MODULE-IDENTITY, OBJECT-TYPE
		FROM SNMPv2-SMI;

sysUpTime OBJECT-TYPE ::= { system 3 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindImports, "", "IMPORTS MODULE-IDENTITY, OBJECT-TYPE FROM SNMPv2-SMI;"},
		shape{KindMacroInvocation, "sysUpTime", "sysUpTime OBJECT-TYPE ::= { system 3 }"},
	)
}

func TestExportsAndChoiceAreSkipped(t *testing.T) {
	src := inModule(`EXPORTS sysDescr, sysUpTime;

Syntax ::= CHOICE {
	number INTEGER,
	text OCTET STRING
}

sysUpTime OBJECT-TYPE ::= { system 3 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindExports, "", "EXPORTS sysDescr, sysUpTime;"},
		shape{KindChoice, "Syntax", "Syntax ::= CHOICE { number INTEGER, text OCTET STRING }"},
		shape{KindMacroInvocation, "sysUpTime", "sysUpTime OBJECT-TYPE ::= { system 3 }"},
	)

	if !f.Modules[0].Frames[0].Kind.Skipped() || !f.Modules[0].Frames[1].Kind.Skipped() {
		t.Errorf("EXPORTS and CHOICE frames are not both marked skipped")
	}
}

func TestTwoModulesInOneFile(t *testing.T) {
	src := "FIRST-MIB DEFINITIONS ::= BEGIN\n" +
		"a OBJECT IDENTIFIER ::= { iso 1 }\n" +
		"END\n\n" +
		"SECOND-MIB DEFINITIONS ::= BEGIN\n" +
		"b OBJECT IDENTIFIER ::= { iso 2 }\n" +
		"END\n"

	f := cut(t, src)

	wantCodes(t, f)
	if len(f.Modules) != 2 {
		t.Fatalf("got %d modules, want 2", len(f.Modules))
	}
	if got, want := f.Modules[0].Name, "FIRST-MIB"; got != want {
		t.Errorf("first module name %q, want %q", got, want)
	}
	if got, want := f.Modules[1].Name, "SECOND-MIB"; got != want {
		t.Errorf("second module name %q, want %q", got, want)
	}
	wantShapes(t, f, 0, shape{KindValueAssignment, "a", "a OBJECT IDENTIFIER ::= { iso 1 }"})
	wantShapes(t, f, 1, shape{KindValueAssignment, "b", "b OBJECT IDENTIFIER ::= { iso 2 }"})
}

func TestContentAfterFinalEndIsDiagnosed(t *testing.T) {
	src := inModule("a OBJECT IDENTIFIER ::= { iso 1 }") + "\nleftover text nobody closed\n"

	f := cut(t, src)

	wantCodes(t, f, diag.ErrCodeContentAfterEnd)
	if len(f.Modules) != 1 {
		t.Fatalf("got %d modules, want 1", len(f.Modules))
	}
	wantShapes(t, f, 0, shape{KindValueAssignment, "a", "a OBJECT IDENTIFIER ::= { iso 1 }"})
}

// END is a token, not a substring. ENDPOINT and endOfMibView are one
// identifier each, so nothing but a real END can close a module.
func TestEndPrefixedIdentifiersDoNotCloseTheModule(t *testing.T) {
	src := inModule(`ENDPOINT ::= INTEGER
endOfMibView OBJECT IDENTIFIER ::= { foo 1 }`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindTypeAssignment, "ENDPOINT", "ENDPOINT ::= INTEGER"},
		shape{KindValueAssignment, "endOfMibView", "endOfMibView OBJECT IDENTIFIER ::= { foo 1 }"},
	)
}

// SNMPv2-SMI and RFC1155-SMI define the application types the lexer
// already knows as reserved words, so the descriptor in front of a type
// assignment's "::=" is sometimes a keyword token. The corpus sweep is
// what found this; a name-only head rule leaves four declarations in the
// two files every other MIB imports from unclassified.
func TestReservedWordNamesATypeAssignment(t *testing.T) {
	src := inModule(`IpAddress ::= [APPLICATION 0] IMPLICIT OCTET STRING (SIZE (4))
Counter32 ::= [APPLICATION 1] IMPLICIT INTEGER (0..4294967295)`)

	f := cut(t, src)

	wantCodes(t, f)
	wantShapes(t, f, 0,
		shape{KindTypeAssignment, "IpAddress", "IpAddress ::= [APPLICATION 0] IMPLICIT OCTET STRING (SIZE (4))"},
		shape{KindTypeAssignment, "Counter32", "Counter32 ::= [APPLICATION 1] IMPLICIT INTEGER (0..4294967295)"},
	)
}

func TestMissingModuleHeaderIsFatal(t *testing.T) {
	f := cut(t, "This is a plain text file about MIBs.\nIt has no module in it.\n")

	wantCodes(t, f, diag.ErrCodeMissingModuleHeader)
	if len(f.Modules) != 0 {
		t.Errorf("got %d modules, want none", len(f.Modules))
	}
}

// A source can hold no significant tokens at all, and a vendor tree has
// both shapes: a zero-byte placeholder, and a file that is nothing but a
// comment banner. Neither is a MIB, and neither may pass in silence --
// a caller that gets back no module and no reason cannot tell a refusal
// from a success.
func TestSourceWithNoTokensIsStillNotAMIB(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"empty", ""},
		{"only a comment", "-- please ignore this mib\n-- Copyright (c) 2010\n"},
		{"only whitespace", "\n\t \r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := cut(t, tc.src)

			wantCodes(t, f, diag.ErrCodeMissingModuleHeader)
			if len(f.Modules) != 0 {
				t.Errorf("got %d modules, want none", len(f.Modules))
			}
		})
	}
}

func TestUnrecognizedDeclarationCostsOnlyItself(t *testing.T) {
	src := inModule(`%%% junk nobody can classify
sysUpTime OBJECT-TYPE ::= { system 3 }`)

	f := cut(t, src)

	wantCodes(t, f, diag.ErrCodeUnrecognizedDeclaration)
	got := shapes(t, f, 0)
	if len(got) != 2 {
		t.Fatalf("got %d frames %v, want 2", len(got), got)
	}
	if got[0].kind != KindUnrecognized {
		t.Errorf("first frame is %+v, want an unrecognized frame", got[0])
	}
	if got[1].name != "sysUpTime" {
		t.Errorf("second frame is %+v, want the sysUpTime declaration", got[1])
	}
}

func TestLimitsAreFatal(t *testing.T) {
	tests := []struct {
		name string
		src  func() string
	}{
		{
			name: "source bytes",
			src: func() string {
				return inModule("-- " + strings.Repeat("x", MaxSourceBytes))
			},
		},
		{
			name: "declaration bytes",
			src: func() string {
				return inModule(`big OBJECT-TYPE
	DESCRIPTION "` + strings.Repeat("y", MaxFrameBytes) + `"
	::= { foo 1 }`)
			},
		},
		{
			name: "declarations",
			src: func() string {
				var b strings.Builder
				b.WriteString("TEST-MIB DEFINITIONS ::= BEGIN\n")
				for i := range MaxDeclarations + 1 {
					b.WriteString("n")
					b.WriteString(strings.Repeat("0", i%3))
					b.WriteString(" OBJECT IDENTIFIER ::= { foo 1 }\n")
				}
				b.WriteString("END\n")

				return b.String()
			},
		},
		{
			name: "nesting depth",
			src: func() string {
				return inModule("Deep ::= INTEGER " +
					strings.Repeat("(", MaxDepth+1) + "0" + strings.Repeat(")", MaxDepth+1))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := cut(t, tc.src())

			wantCodes(t, f, diag.ErrCodeLimitExceeded)
			if len(f.Modules) != 0 {
				t.Errorf("got %d modules, want none: a fatal file yields nothing usable", len(f.Modules))
			}
		})
	}
}

// The paired rule folds the two "--" runs below into one comment and the
// declaration reads normally. The end-of-line rule ends the comment at
// the newline, which strips the "::=" out of the middle of a head.
const pairedOnlySource = `TEST-MIB DEFINITIONS ::= BEGIN
bar OBJECT IDENTIFIER -- opens here
	-- ::= { iso 3 }
END
`

func TestPairedCommentModeReparse(t *testing.T) {
	f := cut(t, pairedOnlySource)

	if f.Comments != lex.CommentPaired {
		t.Fatalf("file read in mode %v, want the paired rule", f.Comments)
	}
	wantCodes(t, f, diag.ErrCodePairedCommentMode)
	// The span covers raw source, so the comment the paired rule folded
	// away is still inside the frame's bytes.
	wantShapes(t, f, 0, shape{KindValueAssignment, "bar", "bar OBJECT IDENTIFIER -- opens here -- ::= { iso 3 }"})
}

// The end-of-line rule is the one that cannot silently eat a
// declaration, so a file that frames badly under both keeps its result
// rather than trading one set of errors for another.
func TestNoReparseWhenPairedIsNoBetter(t *testing.T) {
	f := cut(t, inModule("%%% junk nobody can classify"))

	if f.Comments != lex.CommentEndOfLine {
		t.Fatalf("file read in mode %v, want the end-of-line rule", f.Comments)
	}
	wantCodes(t, f, diag.ErrCodeUnrecognizedDeclaration)
}

// Reading a file written for the end-of-line rule under the paired rule
// usually folds the tail of the file into one comment, which looks like
// an improvement by diagnostic count and is a total loss in fact. Two
// vendor copies of SNMPv2-SMI switched modes on exactly this before the
// count learned to discount a reading that costs the file.
func TestPairedReadingThatCostsTheFileNeverWins(t *testing.T) {
	src := inModule(`%%% junk nobody can classify
x OBJECT IDENTIFIER ::= { iso 1 }
&&& more junk
-- a comment the paired rule never closes`)

	f := cut(t, src)

	if f.Comments != lex.CommentEndOfLine {
		t.Fatalf("file read in mode %v, want the end-of-line rule", f.Comments)
	}
	wantCodes(t, f, diag.ErrCodeUnrecognizedDeclaration, diag.ErrCodeUnrecognizedDeclaration)
	if len(f.Modules) != 1 {
		t.Fatalf("got %d modules, want the one the end-of-line reading found", len(f.Modules))
	}
}

func TestKindNamesAreStable(t *testing.T) {
	seen := make(map[string]Kind, len(kindNames))
	for k, name := range kindNames {
		if name == "" {
			t.Errorf("kind %d has no name", k)

			continue
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("kinds %d and %d both render as %q", prev, k, name)
		}
		seen[name] = Kind(k)
	}
}
