package parse

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/common/smi/internal/lex"
)

// MaxMembers bounds the members of one braced list, which is the shape
// an enumeration, a BITS type and an OBJECTS clause all share. A list
// past this bound is not something somebody wrote, so it costs the file
// rather than trading bounded work for a guess.
//
// The nesting and diagnostic bounds are the framer's constants, because
// they bound the same file the framer already measured and two numbers
// for one limit is one number too many.
const MaxMembers = 65536

// maxSyncAttempts is how often recovery may stop at the same token
// before it consumes one anyway. Ten is go/parser's number and the
// reason is the same: a clause reader that reports an error without
// consuming leaves the loop where it started, and only a forced
// consumption guarantees the loop ends.
const maxSyncAttempts = 10

// bailout unwinds a declaration whose parse cannot continue. It is a
// distinct type rather than an error value so that the frame boundary
// can tell its own unwinding from a genuine bug and re-panic the second
// kind rather than swallowing it.
type bailout struct{}

// parser is one pass over one file's frames.
//
// The scanner state — toks, pos, end — is reset for every frame, which
// is what makes the end of a frame the ultimate sync set: a declaration
// nobody can parse runs out of tokens before it can reach the next
// declaration's. The diagnostic and recovery state is per file, since a
// cascade is a property of the file rather than of one declaration.
type parser struct {
	res  *lex.Result
	file string

	toks []lex.Token
	pos  int
	end  int32

	diags    []diag.Diagnostic
	lastLine int
	fatal    bool

	syncOffset int32
	syncCount  int
	depth      int

	dialect Dialect

	d pending
}

func newParser(res *lex.Result, file string) *parser {
	return &parser{res: res, file: file}
}

// more reports whether the frame has tokens left.
func (p *parser) more() bool { return p.pos < len(p.toks) }

// tok returns the current token, or the zero token past the end of the
// frame. A zero token has [lex.KindInvalid], which matches no test the
// parser makes, so a reader that runs off the end simply stops matching.
func (p *parser) tok() lex.Token {
	if !p.more() {
		return lex.Token{}
	}

	return p.toks[p.pos]
}

func (p *parser) next() { p.pos++ }

// offset is where the current token starts, or the end of the frame once
// there are none left, so a diagnostic about a missing tail still points
// inside the declaration that lacks it.
func (p *parser) offset() int32 {
	if !p.more() {
		return p.end
	}

	return p.toks[p.pos].Offset
}

// spanText materializes the source a span covers. It is for the short
// clause values the dialect pass grades, never for a DESCRIPTION.
func (p *parser) spanText(s Span) string {
	return string(p.res.Bytes(s.Start, s.End))
}

func (p *parser) span() Span {
	t := p.tok()

	return Span{Start: t.Offset, End: t.End()}
}

func (p *parser) at(k lex.Kind) bool { return p.more() && p.toks[p.pos].Kind == k }

func (p *parser) keyword() lex.Keyword {
	t := p.tok()
	if t.Kind != lex.KindKeyword {
		return lex.KeywordNone
	}

	return t.Keyword
}

func (p *parser) isName() bool {
	k := p.tok().Kind

	return k == lex.KindIdentifier || k == lex.KindTypeReference
}

func (p *parser) opener() bool {
	switch p.tok().Kind {
	case lex.KindLeftBrace, lex.KindLeftParen, lex.KindLeftBracket:
		return true
	default:
		return false
	}
}

// text materializes the current token's spelling for a diagnostic. It is
// only ever called on the error path, which is why it may allocate.
func (p *parser) text() string {
	if !p.more() {
		return "end of declaration"
	}

	return p.res.Text(p.toks[p.pos])
}

// raise records a condition at offset, at most one per source line.
//
// The throttle is go/parser's and it earns its place for the same
// reason: one wrong token puts the reader out of step with the source
// and every clause after it reports the same confusion. The first report
// on a line is the one worth reading, and a vendor corpus that produced
// the rest would drown the ones that matter. What the parser knows is
// not lost by the throttle — a declaration's missing clauses are on the
// node whether or not each one drew a diagnostic.
func (p *parser) raise(offset int32, code errs.Code, args ...diag.Arg) {
	line, _ := p.res.Lines.LineColumn(int(offset))
	if line == p.lastLine {
		return
	}
	p.lastLine = line

	if len(p.diags) >= frame.MaxDiagnostics {
		p.limit("diagnostics", frame.MaxDiagnostics, offset)

		return
	}

	p.diags = append(p.diags, diag.Raise(diag.Position{File: p.file, Offset: int(offset)}, code, args...))
}

// limit reports a resource bound being reached and unwinds. It bypasses
// the per-line throttle because a limit is the one diagnostic that
// explains why everything after it is missing.
func (p *parser) limit(what string, bound int, offset int32) {
	p.diags = append(p.diags, diag.Raise(
		diag.Position{File: p.file, Offset: int(offset)},
		diag.ErrCodeLimitExceeded,
		diag.ArgString(what), diag.ArgInt(bound),
	))
	p.fatal = true

	panic(bailout{})
}

// enter counts one level of a recursive construct. The cap is what turns
// input designed to nest forever into a diagnostic instead of a stack
// overflow, and it is checked on the way in so no frame is ever deeper
// than the bound by more than one.
func (p *parser) enter(offset int32) {
	p.depth++
	if p.depth > frame.MaxDepth {
		p.limit("nesting depth", frame.MaxDepth, offset)
	}
}

func (p *parser) leave() {
	if p.depth > 0 {
		p.depth--
	}
}

// advance skips to the next token that starts a clause in sync, or to
// the end of the frame.
//
// The frame is the outer bound and the reason this parser recovers
// better than the ones it replaces: libsmi, net-snmp and go/parser all
// skip to a token they hope begins something new, with nothing to stop a
// bad guess from eating the rest of the file. Here the worst case is
// that the rest of one declaration is skipped.
//
// The same-position counter guards the other failure: a clause reader
// that reports an error without consuming leaves the loop exactly where
// it was, and sync would return immediately forever. After ten stops at
// one token, this consumes it.
func (p *parser) advance(sync ClauseSet) {
	for ; p.more(); p.next() {
		if !p.startsClause(sync) && !p.at(lex.KindAssign) {
			continue
		}

		at := p.offset()
		if at > p.syncOffset {
			p.syncOffset = at
			p.syncCount = 0

			return
		}
		if p.syncCount < maxSyncAttempts {
			p.syncCount++

			return
		}
	}
}

// startsClause reports whether the current token opens a clause in set.
func (p *parser) startsClause(set ClauseSet) bool {
	c := clauseOf(p.keyword())

	return c != ClauseNone && set.Has(c)
}
