// Package lex turns MIB source bytes into tokens.
//
// Its contract is total: any byte slice at all yields a token stream and
// a diagnostic list, never a panic and never a refusal to proceed. That
// is not defensive coding, it is the property the rest of the parser is
// built on. Vendor MIBs arrive with CRLF line endings, tabs and form
// feeds inside declarations, stray control bytes, byte-order marks, and
// non-UTF-8 text inside DESCRIPTION clauses. None of those is a fault
// worth a diagnostic, so the lexer absorbs them silently and keeps
// scanning.
//
// The deviations that do earn a diagnostic are the ones that change what
// the text means: a string or a comment that runs off the end of the
// file, a hexadecimal literal with a half byte left over, an identifier
// ending in a hyphen, and a hyphen separator line whose length makes it
// pair off into comments and leave a stray minus behind.
//
// # Spellings the RFCs do not define
//
// Two of them are read anyway, because refusing them costs whole files
// rather than the byte that was wrong. An underscore inside a descriptor
// stays part of the name, and a string delimited by the Windows-1252
// curly quotes is read as a string. Both are reported, and both are
// pinned by a test named for the behavior, because each looks from the
// outside like a rule somebody forgot.
//
// # Comment termination
//
// ASN.1 ends a comment at the next "--" or at the end of the line,
// whichever comes first. A large part of the vendor corpus was written
// against tools that only honor the end of the line, and reading such a
// file under the paired rule swallows whole declarations into a comment
// that was never meant to open. Neither rule is right for every file, so
// the mode is an option: [CommentEndOfLine] is the default because it is
// the one that cannot silently eat a declaration, and [CommentPaired]
// exists so a caller that finds evidence of paired comments can lex the
// file again under the other rule.
//
// # Allocation
//
// Only significant tokens are emitted; whitespace and comments are
// implied by the gap between one token's end and the next token's start.
// A token is five small fields with no pointer in it. Identifier text is
// interned per file and produced on demand, and quoted-string content is
// materialized only when a caller asks for it. The result is that lexing
// a file allocates on the order of the tokens it contains, not on the
// order of the bytes it read, which is what makes a 1,695-file corpus
// sweep affordable.
package lex

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
)

// CommentMode selects how a comment ends. See the package overview for
// why the choice is a per-file option rather than a fixed rule.
// Values may be copied and read concurrently.
type CommentMode uint8

const (
	// CommentEndOfLine ends a comment at the end of its line. A "--"
	// inside the comment is ordinary text.
	CommentEndOfLine CommentMode = iota

	// CommentPaired ends a comment at the next "--", crossing lines to
	// find it. A comment that finds no closing "--" before the end of the
	// file costs the file, since no declaration boundary after it can be
	// trusted.
	CommentPaired
)

// Options configure one call to [Lex]. The zero value lexes an unnamed
// file with end-of-line comments. Values may be copied and read concurrently.
type Options struct {
	// File is the name diagnostics are reported against. It is never
	// opened or interpreted; the caller has already read the bytes.
	File string

	// Comments selects the comment-termination rule.
	Comments CommentMode
}

// Result is one file's tokens, the diagnostics raised while producing
// them, and the line table that turns an offset in them into a line and
// column.
//
// A Result holds the source slice it was lexed from and does not copy
// it, so the caller must not modify those bytes afterwards. It is safe
// for concurrent reads of its fields, but [Result.Text] fills the
// interner as it goes and so is not safe to call concurrently.
type Result struct {
	// Tokens are the file's significant tokens in source order.
	Tokens []Token

	// Diagnostics are the conditions found while scanning, in the order
	// they were found. A non-empty list does not mean the token stream is
	// unusable; a fatal one means the scan stopped early.
	Diagnostics []diag.Diagnostic

	// Lines maps an offset in this file onto a line and column.
	Lines *diag.LineTable

	src   []byte
	names interner
}

// Text returns the token's source spelling. A reserved word comes from
// the fixed keyword table and an identifier from the file's interner, so
// neither allocates on a repeat occurrence; every other kind
// materializes a fresh string, which is why a parser that only needs to
// compare a token should compare its kind instead.
//
// The token must have come from this Result.
func (r *Result) Text(t Token) string {
	if t.Kind == KindKeyword {
		return t.Keyword.String()
	}

	b := r.src[t.Offset:t.End()]
	if t.Kind == KindIdentifier || t.Kind == KindTypeReference {
		return r.names.intern(b)
	}

	return string(b)
}

// Content returns the bytes a token delimits: the text between the
// quotes of a string, or between the quote and the radix suffix of a
// hexadecimal or binary literal. For every other kind it is the token's
// own bytes.
//
// The result aliases the source, so the bytes of a DESCRIPTION written
// in some vendor's local encoding survive byte for byte. Use
// [Result.StringValue] when a Go string is wanted instead.
// The token must have come from this Result; the caller must not modify
// the returned bytes.
func (r *Result) Content(t Token) []byte {
	b := r.src[t.Offset:t.End()]

	switch t.Kind {
	case KindQuotedString:
		b = Unquote(b)
	case KindHexString, KindBinaryString:
		// Both delimiters and the radix letter are guaranteed present,
		// since the lexer gives these kinds to nothing else.
		b = b[1 : len(b)-2]
	}

	return b
}

// Bytes returns the source in [start, end). It is what lets a later pass
// keep byte offsets in its nodes and still render text: the node carries
// two integers and asks for the bytes when somebody reads it. A range
// outside the file yields nil rather than panicking, since an offset in
// a node is only as trustworthy as the pass that put it there.
//
// The result aliases the source, which the caller must not modify.
func (r *Result) Bytes(start, end int32) []byte {
	if start < 0 || start > end || int(end) > len(r.src) {
		return nil
	}

	return r.src[start:end]
}

// StringValue returns [Result.Content] as a Go string with invalid UTF-8
// replaced by U+FFFD. MIB text is nominally ASCII and routinely is not:
// registered vendor names and contact addresses arrive in Latin-1 and
// several older code pages. Rejecting those files would cost far more
// than a replacement character does, so the bytes are kept and the
// decode is lossy.
func (r *Result) StringValue(t Token) string {
	return strings.ToValidUTF8(string(r.Content(t)), "�")
}

// Lex scans src into tokens under opts. It always returns a usable
// Result: a fatal diagnostic stops the scan, leaving the tokens found so
// far, and every other condition is recorded and scanning continues.
//
// The Result aliases src, which the caller must not modify afterwards.
func Lex(src []byte, opts Options) *Result {
	r := &Result{
		Lines: &diag.LineTable{},
		src:   src,
	}
	l := lexer{
		src:  src,
		file: opts.File,
		mode: opts.Comments,
		out:  r,
	}
	l.run()

	return r
}

// lexer is the scan in progress over one file.
type lexer struct {
	src     []byte
	pos     int
	prevEnd int32
	file    string
	mode    CommentMode
	out     *Result
}

// byteOrderMark is the UTF-8 encoding of U+FEFF. Editors on Windows add
// it to MIB files routinely, and it is not an error in any sense the
// reader would recognize, so it is skipped without comment.
var byteOrderMark = []byte{0xef, 0xbb, 0xbf}

func (l *lexer) run() {
	// A token is a handful of bytes of source on average, so this sizes
	// the slice close enough to avoid most of the growth copies without
	// over-reserving on a large file.
	l.out.Tokens = make([]Token, 0, len(l.src)/8+8)

	if bytes.HasPrefix(l.src, byteOrderMark) {
		l.pos = len(byteOrderMark)
	}

	for l.pos < len(l.src) {
		b := l.src[l.pos]

		switch {
		case b == '\n' || b == '\r':
			l.consumeLineBreak()
		case l.opensCurlyString(b):
			if !l.scanToken() {
				return
			}
		case isTrivia(b):
			l.pos++
		case b == '-' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '-':
			if !l.skipComment() {
				return
			}
		default:
			if !l.scanToken() {
				return
			}
		}
	}
}

// consumeLineBreak advances past one line terminator and records where
// the next line starts. All four orderings of CR and LF appear in the
// corpus, often mixed within one file, and getting this wrong would move
// every comment's end in end-of-line mode.
func (l *lexer) consumeLineBreak() {
	first := l.src[l.pos]
	l.pos++

	if l.pos < len(l.src) {
		second := l.src[l.pos]
		if (first == '\r' && second == '\n') || (first == '\n' && second == '\r') {
			l.pos++
		}
	}

	l.out.Lines.AddLine(l.pos)
}

// skipComment consumes one comment. It reports false when the comment
// ran to the end of the file under the paired rule, which is fatal and
// ends the scan.
func (l *lexer) skipComment() bool {
	start := l.pos

	// A run of 4n+1 hyphens is a separator line drawn by hand. Under the
	// paired rule it pairs off into empty comments and leaves one hyphen
	// behind as a minus token in the middle of a declaration, so it is
	// reported and swallowed whole under both rules rather than left to
	// surface as a syntax error somewhere else.
	if run := l.hyphenRun(); run >= 5 && run%4 == 1 {
		l.raise(start, diag.ErrCodeHyphenSeparator, diag.ArgInt(run))
		l.skipToLineBreak()

		return true
	}

	l.pos += 2

	if l.mode == CommentEndOfLine {
		l.skipToLineBreak()

		return true
	}

	for l.pos < len(l.src) {
		b := l.src[l.pos]
		switch {
		case b == '-' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '-':
			l.pos += 2

			return true
		case b == '\n' || b == '\r':
			l.consumeLineBreak()
		default:
			l.pos++
		}
	}

	l.raise(start, diag.ErrCodeUnterminatedComment)

	return false
}

// hyphenRun returns the length of the hyphen run starting at the
// scanner's position.
func (l *lexer) hyphenRun() int {
	n := 0
	for l.pos+n < len(l.src) && l.src[l.pos+n] == '-' {
		n++
	}

	return n
}

// skipToLineBreak advances to the line terminator, leaving the
// terminator itself for [lexer.consumeLineBreak] so that line accounting
// happens in exactly one place.
func (l *lexer) skipToLineBreak() {
	for l.pos < len(l.src) {
		if b := l.src[l.pos]; b == '\n' || b == '\r' {
			return
		}

		l.pos++
	}
}

// scanToken scans one significant token and appends it. It reports false
// when the token ended the file fatally.
func (l *lexer) scanToken() bool {
	start := l.pos
	b := l.src[l.pos]

	switch {
	case isLetter(b):
		return l.emit(start, l.scanName(start))
	case isDigit(b):
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}

		return l.emit(start, Token{Kind: KindNumber})
	case b == '"':
		return l.scanQuoted(start, '"')
	case l.opensCurlyString(b):
		l.raise(start, diag.ErrCodeCurlyQuotedString)

		return l.scanQuoted(start, curlyCloseQuote)
	case b == '\'':
		return l.scanRadixString(start)
	}

	kind := KindUnknown
	l.pos++

	switch b {
	case '{':
		kind = KindLeftBrace
	case '}':
		kind = KindRightBrace
	case '(':
		kind = KindLeftParen
	case ')':
		kind = KindRightParen
	case '[':
		kind = KindLeftBracket
	case ']':
		kind = KindRightBracket
	case ',':
		kind = KindComma
	case ';':
		kind = KindSemicolon
	case '|':
		kind = KindBar
	case '-':
		kind = KindMinus
	case '.':
		// "1..4" is a range and "sysDescr.0" is a qualified reference, so
		// the doubled dot has to win over the single one here rather than
		// be reassembled by the parser.
		kind = KindDot
		if l.pos < len(l.src) && l.src[l.pos] == '.' {
			l.pos++
			kind = KindRange
		}
	case ':':
		if l.pos+1 < len(l.src) && l.src[l.pos] == ':' && l.src[l.pos+1] == '=' {
			l.pos += 2
			kind = KindAssign
		}
	}

	return l.emit(start, Token{Kind: kind})
}

// scanName scans an identifier, a type reference or a reserved word.
func (l *lexer) scanName(start int) Token {
	underscore := false

	for l.pos < len(l.src) {
		b := l.src[l.pos]
		if isLetter(b) || isDigit(b) {
			l.pos++

			continue
		}
		if b == '_' {
			// RFC 2578 §3.1 leaves the underscore out of a descriptor's
			// character set, and five vendor MIBs in the corpus write it
			// anyway, mostly in TLS cipher-suite names. Ending the name
			// here is what turns one descriptor into a run of fragments
			// and costs the declaration its members, so the byte is taken
			// as part of the name and the deviation is reported. A
			// descriptor still may not begin with one, since that is the
			// caller's dispatch rather than this loop.
			underscore = true
			l.pos++

			continue
		}
		if b != '-' {
			break
		}

		// A doubled hyphen opens a comment, so "foo--bar" is the name
		// "foo" and a comment, never a name with hyphens in it.
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '-' {
			break
		}
		if l.pos+1 < len(l.src) && (isLetter(l.src[l.pos+1]) || isDigit(l.src[l.pos+1])) {
			l.pos += 2

			continue
		}

		l.pos++
		l.raise(start, diag.ErrCodeTrailingHyphenIdentifier, diag.ArgString(l.out.names.intern(l.src[start:l.pos])))

		break
	}

	text := l.src[start:l.pos]
	if underscore {
		l.raise(start, diag.ErrCodeUnderscoreInDescriptor, diag.ArgString(l.out.names.intern(text)))
	}
	if kw, ok := lookupKeyword(text); ok {
		return Token{Kind: KindKeyword, Keyword: kw}
	}
	if isUpper(text[0]) {
		return Token{Kind: KindTypeReference}
	}

	return Token{Kind: KindIdentifier}
}

// scanQuoted scans a string opened at start and ended by closing. SMI
// has no escape mechanism, so the first closing delimiter ends the
// string and a "--" inside one is ordinary text.
func (l *lexer) scanQuoted(start int, closing byte) bool {
	l.pos++

	for l.pos < len(l.src) {
		switch b := l.src[l.pos]; b {
		case closing:
			if closing == curlyCloseQuote && continuesRune(l.src, l.pos) {
				l.pos++

				continue
			}
			l.pos++

			return l.emit(start, Token{Kind: KindQuotedString})
		case '\n', '\r':
			l.consumeLineBreak()
		default:
			l.pos++
		}
	}

	l.raise(start, diag.ErrCodeUnterminatedString)
	l.emit(start, Token{Kind: KindQuotedString})

	return false
}

// scanRadixString scans a single-quoted literal and its radix suffix.
// A literal with no recognized suffix is kept as an unknown token rather
// than guessed at, since reading '10' as either binary or hexadecimal
// would silently change a value.
func (l *lexer) scanRadixString(start int) bool {
	l.pos++

	digits := 0
	closed := false

	for l.pos < len(l.src) {
		switch b := l.src[l.pos]; b {
		case '\'':
			l.pos++
			closed = true
		case '\n', '\r':
			l.consumeLineBreak()
		default:
			// ASN.1 allows whitespace to group digits inside these
			// literals, so only the digits count toward the length.
			if !isTrivia(b) {
				digits++
			}
			l.pos++
		}
		if closed {
			break
		}
	}

	if !closed {
		l.raise(start, diag.ErrCodeUnterminatedString)
		l.emit(start, Token{Kind: KindUnknown})

		return false
	}

	kind := KindUnknown
	if l.pos < len(l.src) {
		switch l.src[l.pos] {
		case 'h', 'H':
			kind = KindHexString
			l.pos++
		case 'b', 'B':
			kind = KindBinaryString
			l.pos++
		}
	}

	if kind == KindHexString && digits%2 == 1 {
		l.raise(start, diag.ErrCodeOddHexString, diag.ArgInt(digits))
	}

	return l.emit(start, Token{Kind: kind})
}

// emit records a token spanning start to the scanner's position. It
// always reports true so a scan function can end with it.
func (l *lexer) emit(start int, t Token) bool {
	t.Offset = int32(start)
	t.Length = int32(l.pos - start)
	t.PrevEnd = l.prevEnd
	l.prevEnd = t.End()

	l.out.Tokens = append(l.out.Tokens, t)

	return true
}

// raise records a condition at offset, up to the diagnostic cap.
//
// The lexer cannot stop scanning the way the framer and the parser stop
// at their limits — its contract is that every byte slice yields a token
// stream — so the cap only closes the diagnostic list. A file that
// reaches it gets one [diag.ErrCodeLimitExceeded] saying so and nothing
// after, which is the same shape the other two passes produce.
func (l *lexer) raise(offset int, code errs.Code, args ...diag.Arg) {
	if len(l.out.Diagnostics) >= diag.MaxDiagnostics {
		if len(l.out.Diagnostics) == diag.MaxDiagnostics {
			l.out.Diagnostics = append(l.out.Diagnostics, diag.Raise(
				diag.Position{File: l.file, Offset: offset},
				diag.ErrCodeLimitExceeded,
				diag.ArgString("diagnostics"), diag.ArgInt(diag.MaxDiagnostics),
			))
		}

		return
	}

	pos := diag.Position{File: l.file, Offset: offset}
	l.out.Diagnostics = append(l.out.Diagnostics, diag.Raise(pos, code, args...))
}

// The Windows-1252 curly quotation marks, which a word processor
// substitutes for the ASCII pair without being asked.
const (
	curlyOpenQuote  = 0x93
	curlyCloseQuote = 0x94
)

// Unquote returns the text a quoted string's source bytes delimit. The
// opening delimiter says which closing one to look for, and the closing
// one is absent when the string ran to the end of the file.
//
// It is exported because a later pass keeps spans rather than tokens
// and still has to strip the same pair, the curly one included.
// The result aliases b; concurrent calls require that b remain unchanged.
func Unquote(b []byte) []byte {
	if len(b) == 0 {
		return b
	}

	closing := byte('"')
	switch b[0] {
	case '"':
	case curlyOpenQuote:
		closing = curlyCloseQuote
	default:
		return b
	}

	b = b[1:]
	if n := len(b); n > 0 && b[n-1] == closing &&
		(closing != curlyCloseQuote || !continuesRune(b, n-1)) {
		b = b[:n-1]
	}

	return b
}

// opensCurlyString reports whether b begins a Windows-1252 curly-quoted
// string here.
//
// SMI delimits a string with the ASCII quotation mark and nothing else,
// but one IEEE MIB in the corpus went through a word processor that
// replaced every pair with curly quotes, and reading those as ordinary
// bytes leaves that file without one parsable declaration. So the pair
// is accepted as a delimiter and the substitution is reported. A
// stricter lexer costs the file.
//
// The rune guard is what keeps well-formed UTF-8 out of it: U+2013 EN
// DASH ends in 0x93, one Cisco MIB writes it between identifiers, and
// treating that byte as an opening quote would swallow the rest of the
// file into a string.
func (l *lexer) opensCurlyString(b byte) bool {
	return b == curlyOpenQuote && !continuesRune(l.src, l.pos)
}

// continuesRune reports whether the byte at pos is a continuation byte
// of a well-formed multi-byte sequence that began earlier in src. A
// sequence is at most four bytes, so at most three positions can start
// one that reaches pos.
func continuesRune(src []byte, pos int) bool {
	for back := 1; back <= 3 && back <= pos; back++ {
		start := pos - back
		if src[start] < 0xc0 {
			continue
		}

		r, size := utf8.DecodeRune(src[start:])
		if r != utf8.RuneError && start+size > pos {
			return true
		}
	}

	return false
}

// isTrivia reports whether b is a byte to skip between tokens. Every
// control byte counts, not just the whitespace ones: form feeds and
// vertical tabs are used as page separators in older MIBs, and a stray
// NUL or an escape sequence left by an editor is not worth a diagnostic.
// Bytes above ASCII count too, since text outside a quoted string has no
// meaning the parser could attach to a non-ASCII byte.
func isTrivia(b byte) bool {
	return b <= ' ' || b >= 0x7f
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isUpper(b byte) bool {
	return b >= 'A' && b <= 'Z'
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
