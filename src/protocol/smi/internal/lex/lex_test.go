package lex

import (
	"bytes"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
)

// shape is a token reduced to what a test cares about. Offsets are
// checked by the tiling helper rather than restated in every table.
type shape struct {
	kind Kind
	text string
}

func shapes(r *Result) []shape {
	out := make([]shape, 0, len(r.Tokens))
	for _, t := range r.Tokens {
		out = append(out, shape{kind: t.Kind, text: r.Text(t)})
	}

	return out
}

func codes(r *Result) []errs.Code {
	out := make([]errs.Code, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		out = append(out, d.Code())
	}

	return out
}

func wantShapes(t *testing.T, r *Result, want []shape) {
	t.Helper()

	got := shapes(r)
	if len(got) != len(want) {
		t.Fatalf("got %d tokens %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func wantCodes(t *testing.T, r *Result, want ...errs.Code) {
	t.Helper()

	got := codes(r)
	if len(got) != len(want) {
		t.Fatalf("got diagnostics %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("diagnostic %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// wantTiling checks the property the whole in-memory shape rests on:
// every byte of the source belongs either to a token or to the gap
// before one, exactly once. Reassembling the file from the tokens and
// their implied gaps has to reproduce it byte for byte.
func wantTiling(t *testing.T, src []byte, r *Result) {
	t.Helper()

	var rebuilt []byte
	prevEnd := int32(0)

	for i, tok := range r.Tokens {
		switch {
		case tok.PrevEnd != prevEnd:
			t.Fatalf("token %d: PrevEnd %d, want %d", i, tok.PrevEnd, prevEnd)
		case tok.Offset < tok.PrevEnd:
			t.Fatalf("token %d: offset %d precedes gap start %d", i, tok.Offset, tok.PrevEnd)
		case tok.Length <= 0:
			t.Fatalf("token %d: length %d", i, tok.Length)
		case int(tok.End()) > len(src):
			t.Fatalf("token %d: ends at %d past source length %d", i, tok.End(), len(src))
		}

		rebuilt = append(rebuilt, src[tok.PrevEnd:tok.Offset]...)
		rebuilt = append(rebuilt, src[tok.Offset:tok.End()]...)
		prevEnd = tok.End()
	}

	rebuilt = append(rebuilt, src[prevEnd:]...)
	if !bytes.Equal(rebuilt, src) {
		t.Errorf("tokens plus gaps rebuild %q, want %q", rebuilt, src)
	}
}

func lexString(t *testing.T, src string, mode CommentMode) *Result {
	t.Helper()

	b := []byte(src)
	r := Lex(b, Options{File: "test.mib", Comments: mode})
	wantTiling(t, b, r)

	return r
}

func TestByteOrderMarkIsInvisible(t *testing.T) {
	const body = "sysDescr OBJECT-TYPE\n  SYNTAX OCTET STRING\n"

	plain := lexString(t, body, CommentEndOfLine)
	marked := lexString(t, "\ufeff"+body, CommentEndOfLine)

	wantShapes(t, marked, shapes(plain))
	wantCodes(t, marked)

	if got, want := marked.Tokens[0].Offset, plain.Tokens[0].Offset+3; got != want {
		t.Errorf("first token offset with mark: got %d, want %d", got, want)
	}
	if got := marked.Tokens[0].PrevEnd; got != 0 {
		t.Errorf("first token PrevEnd: got %d, want 0 so the mark lands in the leading gap", got)
	}
}

func TestByteLevelDeviationsAreSilent(t *testing.T) {
	src := "a\tb\x0cc\x0bd\x00e\x1ff\r\ngéh"

	r := lexString(t, src, CommentEndOfLine)

	wantCodes(t, r)
	wantShapes(t, r, []shape{
		{KindIdentifier, "a"},
		{KindIdentifier, "b"},
		{KindIdentifier, "c"},
		{KindIdentifier, "d"},
		{KindIdentifier, "e"},
		{KindIdentifier, "f"},
		{KindIdentifier, "g"},
		{KindIdentifier, "h"},
	})
}

func TestQuotedStringKeepsNonASCIIBytes(t *testing.T) {
	// A Latin-1 "Zürich" followed by a well-formed UTF-8 sequence: the
	// first byte cannot be decoded and the second must survive.
	src := []byte("\"Z\xfcrich é\"")

	r := Lex(src, Options{File: "test.mib"})
	wantTiling(t, src, r)
	wantCodes(t, r)

	if len(r.Tokens) != 1 || r.Tokens[0].Kind != KindQuotedString {
		t.Fatalf("got %v, want one quoted string", shapes(r))
	}

	if got, want := r.Content(r.Tokens[0]), []byte("Z\xfcrich é"); !bytes.Equal(got, want) {
		t.Errorf("content: got %q, want the source bytes %q", got, want)
	}
	if got, want := r.StringValue(r.Tokens[0]), "Z�rich é"; got != want {
		t.Errorf("decoded: got %q, want %q", got, want)
	}
}

func TestCommentTerminationModes(t *testing.T) {
	const src = "-- comment -- trailing\n"

	eol := lexString(t, src, CommentEndOfLine)
	wantCodes(t, eol)
	wantShapes(t, eol, nil)

	paired := lexString(t, src, CommentPaired)
	wantCodes(t, paired)
	wantShapes(t, paired, []shape{{KindIdentifier, "trailing"}})
}

func TestLineTerminatorsEndComment(t *testing.T) {
	for _, term := range []string{"\n", "\r", "\r\n", "\n\r"} {
		t.Run(strings.NewReplacer("\r", "CR", "\n", "LF").Replace(term), func(t *testing.T) {
			r := lexString(t, "a -- swallowed"+term+"b", CommentEndOfLine)

			wantCodes(t, r)
			wantShapes(t, r, []shape{{KindIdentifier, "a"}, {KindIdentifier, "b"}})

			if got := r.Lines.Lines(); got != 2 {
				t.Errorf("line count: got %d, want 2", got)
			}
		})
	}
}

func TestHyphenSeparatorLine(t *testing.T) {
	for _, mode := range []CommentMode{CommentEndOfLine, CommentPaired} {
		t.Run(map[CommentMode]string{CommentEndOfLine: "eol", CommentPaired: "paired"}[mode], func(t *testing.T) {
			r := lexString(t, "a\n---------\nb\n", mode)

			wantCodes(t, r, diag.ErrCodeHyphenSeparator)
			wantShapes(t, r, []shape{{KindIdentifier, "a"}, {KindIdentifier, "b"}})

			if got, want := r.Diagnostics[0].Message(), "separator line of 9 hyphens is not a well-formed comment"; got != want {
				t.Errorf("message: got %q, want %q", got, want)
			}
		})
	}
}

func TestEvenHyphenRunIsJustAComment(t *testing.T) {
	r := lexString(t, "a\n--------\nb\n", CommentPaired)

	wantCodes(t, r)
	wantShapes(t, r, []shape{{KindIdentifier, "a"}, {KindIdentifier, "b"}})
}

func TestRadixStrings(t *testing.T) {
	r := lexString(t, "'0F0F'H '1010'B", CommentEndOfLine)

	wantCodes(t, r)
	wantShapes(t, r, []shape{
		{KindHexString, "'0F0F'H"},
		{KindBinaryString, "'1010'B"},
	})

	if got, want := string(r.Content(r.Tokens[0])), "0F0F"; got != want {
		t.Errorf("hex content: got %q, want %q", got, want)
	}

	odd := lexString(t, "'0F0'H", CommentEndOfLine)
	wantCodes(t, odd, diag.ErrCodeOddHexString)
	if got, want := odd.Diagnostics[0].Message(), "hexadecimal string has 3 digits, an odd count that leaves a half byte"; got != want {
		t.Errorf("message: got %q, want %q", got, want)
	}
}

func TestDotDisambiguation(t *testing.T) {
	r := lexString(t, "1..4 sysDescr.0 { iso 3 6 1 }", CommentEndOfLine)

	wantCodes(t, r)
	wantShapes(t, r, []shape{
		{KindNumber, "1"},
		{KindRange, ".."},
		{KindNumber, "4"},
		{KindIdentifier, "sysDescr"},
		{KindDot, "."},
		{KindNumber, "0"},
		{KindLeftBrace, "{"},
		{KindIdentifier, "iso"},
		{KindNumber, "3"},
		{KindNumber, "6"},
		{KindNumber, "1"},
		{KindRightBrace, "}"},
	})
}

func TestUnterminatedStringIsFatal(t *testing.T) {
	r := lexString(t, "DESCRIPTION \"runs off the end", CommentEndOfLine)

	wantCodes(t, r, diag.ErrCodeUnterminatedString)
	if got, want := r.Diagnostics[0].Severity(), diag.SeverityFatal; got != want {
		t.Errorf("severity: got %v, want %v", got, want)
	}

	last := r.Tokens[len(r.Tokens)-1]
	if last.Kind != KindQuotedString {
		t.Fatalf("last token: got %v, want a quoted string", last.Kind)
	}
	if got, want := string(r.Content(last)), "runs off the end"; got != want {
		t.Errorf("content: got %q, want %q", got, want)
	}
}

func TestUnterminatedPairedCommentIsFatal(t *testing.T) {
	r := lexString(t, "a -- opens and never closes\nb\n", CommentPaired)

	wantCodes(t, r, diag.ErrCodeUnterminatedComment)
	wantShapes(t, r, []shape{{KindIdentifier, "a"}})
}

func TestTrailingHyphenIdentifier(t *testing.T) {
	r := lexString(t, "foo- ", CommentEndOfLine)

	wantCodes(t, r, diag.ErrCodeTrailingHyphenIdentifier)
	wantShapes(t, r, []shape{{KindIdentifier, "foo-"}})
	if got, want := r.Diagnostics[0].Message(), `identifier "foo-" ends in a hyphen`; got != want {
		t.Errorf("message: got %q, want %q", got, want)
	}
}

func TestDoubledHyphenEndsIdentifier(t *testing.T) {
	r := lexString(t, "foo--bar\nbaz", CommentEndOfLine)

	wantCodes(t, r)
	wantShapes(t, r, []shape{{KindIdentifier, "foo"}, {KindIdentifier, "baz"}})
}

func TestInternedIdentifierTextIsAllocatedOnce(t *testing.T) {
	const repeats = 1000

	r := lexString(t, strings.Repeat("ifIndex ", repeats), CommentEndOfLine)
	if len(r.Tokens) != repeats {
		t.Fatalf("got %d tokens, want %d", len(r.Tokens), repeats)
	}

	// AllocsPerRun discards a warm-up run, so the interner is populated
	// before measurement and a second copy of the text would show up.
	allocs := testing.AllocsPerRun(3, func() {
		for _, tok := range r.Tokens {
			_ = r.Text(tok)
		}
	})
	if allocs != 0 {
		t.Errorf("got %.0f allocations reading %d identifiers, want 0", allocs, repeats)
	}
	if got := len(r.names.table); got != 1 {
		t.Errorf("interner holds %d entries, want 1", got)
	}
}

func TestKeywordsAreRecognized(t *testing.T) {
	r := lexString(t, "OBJECT-TYPE Counter32 sysUpTime SnmpAdminString", CommentEndOfLine)

	wantCodes(t, r)
	wantShapes(t, r, []shape{
		{KindKeyword, "OBJECT-TYPE"},
		{KindKeyword, "Counter32"},
		{KindIdentifier, "sysUpTime"},
		{KindTypeReference, "SnmpAdminString"},
	})

	if got := r.Tokens[0].Keyword; got != KeywordObjectType {
		t.Errorf("keyword: got %v, want %v", got, KeywordObjectType)
	}
	if got := r.Tokens[2].Keyword; got != KeywordNone {
		t.Errorf("identifier carries keyword %v, want none", got)
	}
}

func TestKeywordTableIsComplete(t *testing.T) {
	if len(keywords) != len(keywordText)-1 {
		t.Errorf("%d spellings map back, want %d", len(keywords), len(keywordText)-1)
	}

	for k := 1; k < len(keywordText); k++ {
		text := keywordText[k]
		if text == "" {
			t.Errorf("keyword %d has no spelling", k)

			continue
		}
		if got, ok := lookupKeyword([]byte(text)); !ok || got != Keyword(k) {
			t.Errorf("%q resolves to %v (%v), want keyword %d", text, got, ok, k)
		}
	}
}

func TestKindNamesAreComplete(t *testing.T) {
	for k := Kind(0); k <= KindAssign; k++ {
		if kindNames[k] == "" {
			t.Errorf("kind %d has no name", k)
		}
	}
	if got := Kind(len(kindNames) + 1).String(); got == "" {
		t.Error("off-table kind renders as an empty string")
	}
}

func TestLineTableTracksMixedTerminators(t *testing.T) {
	r := lexString(t, "a\nb\r\nc\rd\n\re", CommentEndOfLine)

	if got, want := r.Lines.Lines(), 5; got != want {
		t.Fatalf("line count: got %d, want %d", got, want)
	}

	for i, tok := range r.Tokens {
		line, column := r.Lines.LineColumn(int(tok.Offset))
		if line != i+1 || column != 1 {
			t.Errorf("token %d at line %d column %d, want line %d column 1", i, line, column, i+1)
		}
	}
}

func TestAssignAndStrayColon(t *testing.T) {
	r := lexString(t, "x ::= : y", CommentEndOfLine)

	wantCodes(t, r)
	wantShapes(t, r, []shape{
		{KindIdentifier, "x"},
		{KindAssign, "::="},
		{KindUnknown, ":"},
		{KindIdentifier, "y"},
	})
}

func TestUnsuffixedSingleQuoteStaysUnknown(t *testing.T) {
	r := lexString(t, "'1010'", CommentEndOfLine)

	wantCodes(t, r)
	wantShapes(t, r, []shape{{KindUnknown, "'1010'"}})
}

func TestUnderscoreInDescriptorLexesAsOneName(t *testing.T) {
	r := lexString(t, "tls_ecdhe_rsa_with_aes_128_cbc_sha(0)", CommentEndOfLine)

	wantCodes(t, r, diag.ErrCodeUnderscoreInDescriptor)
	wantShapes(t, r, []shape{
		{KindIdentifier, "tls_ecdhe_rsa_with_aes_128_cbc_sha"},
		{KindLeftParen, "("},
		{KindNumber, "0"},
		{KindRightParen, ")"},
	})

	if got, want := r.Diagnostics[0].Message(),
		`descriptor "tls_ecdhe_rsa_with_aes_128_cbc_sha" holds an underscore, which RFC 2578 does not permit`; got != want {
		t.Errorf("message: got %q, want %q", got, want)
	}
	if got, want := r.Diagnostics[0].Severity(), diag.SeverityMinor; got != want {
		t.Errorf("severity: got %v, want %v", got, want)
	}
}

// RFC 2578 §3.1 leaves the underscore out of a descriptor and five
// vendor MIBs write it anyway. Ending the name at the underscore is
// what costs those files their enumeration members, so the looseness is
// the behavior wanted here: a lexer tightened back to the RFC reads one
// descriptor as a run of fragments again.
func TestUnderscoreDescriptorKeepsItsMembersThoughRFC2578ForbidsIt(t *testing.T) {
	r := lexString(t, "{ a_one(1), a_two(2) }", CommentEndOfLine)

	names := 0
	for _, tok := range r.Tokens {
		switch tok.Kind {
		case KindIdentifier:
			names++
		case KindUnknown:
			t.Errorf("an underscore surfaced as an unknown token in %v", shapes(r))
		}
	}
	if names != 2 {
		t.Errorf("got %d identifiers in %v, want one per member", names, shapes(r))
	}
}

func TestCurlyQuotesDelimitAString(t *testing.T) {
	src := []byte("DESCRIPTION \x93A port\x92s state.\x94")

	r := Lex(src, Options{File: "test.mib"})
	wantTiling(t, src, r)
	wantCodes(t, r, diag.ErrCodeCurlyQuotedString)

	if len(r.Tokens) != 2 || r.Tokens[1].Kind != KindQuotedString {
		t.Fatalf("got %v, want a keyword and a quoted string", shapes(r))
	}
	if got, want := r.Content(r.Tokens[1]), []byte("A port\x92s state."); !bytes.Equal(got, want) {
		t.Errorf("content: got %q, want the source bytes %q", got, want)
	}
}

// A word processor re-quotes a DESCRIPTION into Windows-1252 without
// being asked, and one IEEE MIB in the corpus arrives that way
// throughout. Reading those two bytes as delimiters is what keeps that
// file's hundred declarations, so a lexer put back to ASCII-only quotes
// loses all of them.
func TestCurlyQuotedFileKeepsItsDeclarationsThoughSMIWritesASCIIQuotes(t *testing.T) {
	src := []byte("STATUS current DESCRIPTION \x93first\x94 REFERENCE \x93second\x94 ::=")

	r := Lex(src, Options{File: "test.mib"})
	wantTiling(t, src, r)

	quoted := 0
	for _, tok := range r.Tokens {
		if tok.Kind == KindQuotedString {
			quoted++
		}
	}
	if quoted != 2 {
		t.Errorf("got %d strings in %v, want one per curly-quoted clause", quoted, shapes(r))
	}
	if last := r.Tokens[len(r.Tokens)-1]; last.Kind != KindAssign {
		t.Errorf("the assignment after the last string was swallowed: %v", shapes(r))
	}
}

func TestCurlyApostropheInsideAnASCIIStringIsOrdinaryText(t *testing.T) {
	src := []byte("\"it\x92s a plain string\"")

	r := Lex(src, Options{File: "test.mib"})
	wantTiling(t, src, r)
	wantCodes(t, r)

	if len(r.Tokens) != 1 || r.Tokens[0].Kind != KindQuotedString {
		t.Fatalf("got %v, want one quoted string", shapes(r))
	}
	if got, want := r.Content(r.Tokens[0]), []byte("it\x92s a plain string"); !bytes.Equal(got, want) {
		t.Errorf("content: got %q, want %q", got, want)
	}
}

func TestCurlyQuoteByteInsideAUTF8SequenceIsNotADelimiter(t *testing.T) {
	// U+2013 EN DASH is 0xe2 0x80 0x93, and one Cisco MIB writes it
	// between two identifiers outside any string.
	src := []byte("first \xe2\x80\x93 second")

	r := Lex(src, Options{File: "test.mib"})
	wantTiling(t, src, r)
	wantCodes(t, r)
	wantShapes(t, r, []shape{{KindIdentifier, "first"}, {KindIdentifier, "second"}})
}

func TestCurlyClosingQuoteByteInsideUTF8PreservesString(t *testing.T) {
	src := "DESCRIPTION \x93before — after\x94 ::= "
	r := lexString(t, src, CommentEndOfLine)

	wantCodes(t, r, diag.ErrCodeCurlyQuotedString)
	wantShapes(t, r, []shape{
		{KindKeyword, "DESCRIPTION"},
		{KindQuotedString, "\x93before — after\x94"},
		{KindAssign, "::="},
	})
	if got, want := string(r.Content(r.Tokens[1])), "before — after"; got != want {
		t.Errorf("content: got %q, want %q", got, want)
	}
}

func TestUnterminatedCurlyStringPreservesTrailingUTF8(t *testing.T) {
	if got, want := string(Unquote([]byte("\x93before —"))), "before —"; got != want {
		t.Errorf("unquoted: got %q, want %q", got, want)
	}

	src := "DESCRIPTION \x93before —"
	r := lexString(t, src, CommentEndOfLine)

	wantCodes(t, r, diag.ErrCodeCurlyQuotedString, diag.ErrCodeUnterminatedString)
	if got, want := string(r.Content(r.Tokens[1])), "before —"; got != want {
		t.Errorf("content: got %q, want %q", got, want)
	}
}

func TestUnterminatedCurlyQuotedStringIsFatal(t *testing.T) {
	src := []byte("DESCRIPTION \x93runs off the end")

	r := Lex(src, Options{File: "test.mib"})
	wantTiling(t, src, r)
	wantCodes(t, r, diag.ErrCodeCurlyQuotedString, diag.ErrCodeUnterminatedString)

	if got, want := r.Diagnostics[1].Severity(), diag.SeverityFatal; got != want {
		t.Errorf("severity: got %v, want %v", got, want)
	}
	if got, want := string(r.Content(r.Tokens[len(r.Tokens)-1])), "runs off the end"; got != want {
		t.Errorf("content: got %q, want %q", got, want)
	}
}

// The framer and the parser both stop at the diagnostic cap, and the
// lexer honors the same one. It cannot stop scanning the way they stop —
// its contract is that every byte slice yields a token stream — so the
// cap closes the diagnostic list and the tokens keep coming.
func TestDiagnosticsAreCapped(t *testing.T) {
	// An identifier ending in a hyphen is one diagnostic each.
	src := []byte(strings.Repeat("bad- ", diag.MaxDiagnostics+500))

	r := Lex(src, Options{File: "test.mib"})

	if got, want := len(r.Diagnostics), diag.MaxDiagnostics+1; got != want {
		t.Fatalf("got %d diagnostics, want %d: the cap plus the one that reports it", got, want)
	}
	if got := r.Diagnostics[len(r.Diagnostics)-1].Code(); got != diag.ErrCodeLimitExceeded {
		t.Errorf("got %v, want %v as the last diagnostic", got, diag.ErrCodeLimitExceeded)
	}
	wantTiling(t, src, r)
}
