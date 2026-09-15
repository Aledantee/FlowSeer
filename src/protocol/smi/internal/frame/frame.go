// Package frame cuts MIB source into declaration-sized pieces before any
// grammar runs.
//
// Framing is what makes this parser's leniency structural rather than
// hopeful. A declaration parser's ultimate sync set is the end of its own
// frame, so a declaration nobody can parse costs that declaration and
// leaves the ones around it untouched. libsmi, net-snmp and go/parser all
// recover by skipping to a token they hope starts something new, with no
// outer bound on how far a bad guess can run; a frame is that bound.
//
// # Heads, not terminators
//
// The framer recognizes a declaration's head and then applies the
// terminator rule that head implies. Syncing on "::= { … }" alone is the
// obvious design and it mis-frames a large part of the corpus: TRAP-TYPE
// ends at a bare integer with no braces, and textual conventions, type
// assignments and SEQUENCE row types carry their "::=" at the front and
// have no terminator at all, so the next declaration's terminator is the
// first one such a rule sees.
//
// The "<descriptor> OBJECT IDENTIFIER ::= { parent n }" value assignment
// needs a head form of its own because its second token is a type rather
// than a macro keyword and its "::=" is not adjacent to the descriptor,
// so neither general form matches it. It is also the corpus's single most
// common declaration, and it shares a prefix with the "Foo ::= OBJECT
// IDENTIFIER" type assignment that means something else entirely.
//
// # Skipping runs at token level
//
// MACRO, EXPORTS and CHOICE bodies are consumed without producing
// declarations. libsmi does this by matching raw characters, so a quoted
// "END" or a "--" comment inside a macro body ends the skip early. Here
// the skip consumes tokens, which keeps string and comment lexing active
// and makes a quoted END exactly as inert as it should be.
package frame

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/lex"
)

// The declared resource limits. Every one of them is a condition that
// costs the whole file, because a source past any of these bounds is not
// a MIB somebody wrote by hand and continuing would trade bounded work
// for a guess.
//
// The numbers are set far above the corpus rather than near it: the
// largest MIB in spec/mib is a small fraction of the source limit, and
// the deepest nesting is well under an eighth of the depth cap, which
// the corpus sweep asserts.
const (
	// MaxSourceBytes bounds one source file.
	MaxSourceBytes = 16 << 20

	// MaxFrameBytes bounds one declaration.
	MaxFrameBytes = 2 << 20

	// MaxDeclarations bounds the frames one file may yield.
	MaxDeclarations = 65536

	// MaxDepth bounds bracket nesting in any recursive construct.
	MaxDepth = 64

	// MaxDiagnostics bounds what one file may report. The lexer honors
	// the same cap and sits below this package, so the number lives in
	// package diag and this is the framer's spelling of it.
	MaxDiagnostics = diag.MaxDiagnostics
)

// Span is a half-open byte range in the source it was cut from. Its zero
// value is an empty range. Values may be copied and read concurrently.
type Span struct {
	Start int32
	End   int32
}

// Len returns the number of bytes the span covers.
func (s Span) Len() int { return int(s.End - s.Start) }

// Frame is one declaration's extent: what its head was classified as,
// what it names, the bytes it claims, and the tokens inside it.
//
// Tokens aliases the file's token slice rather than copying, so a frame
// costs a header and nothing else. Name is the declared descriptor, the
// empty string for the heads that name nothing, and the offending
// token's spelling for [KindUnrecognized].
// Frames are safe for concurrent reads when their token slices are not
// modified. The zero value holds no declaration.
type Frame struct {
	Kind   Kind
	Name   string
	Span   Span
	Tokens []lex.Token
}

// Module is one DEFINITIONS ::= BEGIN … END block and the frames cut
// from it. Span runs from the module's name to its END, or to the last
// token framed when the END is missing. The zero value holds no module.
// A Module is safe for concurrent reads when its frames are not modified.
type Module struct {
	Name   string
	Span   Span
	Frames []Frame
}

// File is one source cut into modules.
//
// Cut always returns a usable File. Modules is empty when a fatal condition
// cost the file, and Diagnostics then says which one; every other condition
// leaves the modules that were framed in place. Comments records which
// comment-termination rule produced this result, which matters because
// the two rules disagree about where several declarations in the corpus
// begin.
//
// The zero value has no modules or source. File methods are safe for
// concurrent reads when its fields and the source bytes are not modified.
// Calling [lex.Result.Text] through Source requires exclusive access
// because it updates the token text cache.
type File struct {
	Name        string
	Modules     []Module
	Diagnostics []diag.Diagnostic
	Comments    lex.CommentMode

	// Source is the token stream the frames index into. A later pass
	// reads token text through it.
	Source *lex.Result

	src      []byte
	maxDepth int
}

// Text returns the source bytes the span covers, as a string. The span
// must have come from this File.
func (f *File) Text(s Span) string { return string(f.src[s.Start:s.End]) }

// Frames returns every frame in the file, across all its modules. It is
// for reporting and for corpus sweeps; a pass that cares which module a
// declaration belongs to walks Modules instead. The returned slice is
// independent, but each frame's Tokens still aliases the source tokens.
func (f *File) Frames() []Frame {
	var out []Frame
	for _, m := range f.Modules {
		out = append(out, m.Frames...)
	}

	return out
}

// Options configure one call to [Cut]. The zero value uses an empty file
// name in diagnostics. Values may be copied and read concurrently.
type Options struct {
	// File is the name diagnostics are reported against.
	File string
}

// Cut splits src into modules and declaration frames.
//
// It reads the file with end-of-line comment termination first. If that
// yields conditions the paired "--" rule resolves, the file is read again
// in the paired mode and the result carries
// [diag.ErrCodePairedCommentMode] to record it. That happens at most once
// and is decided for the whole file: a source that frames badly under
// both rules keeps the end-of-line reading, which is the rule that cannot
// silently swallow a declaration.
//
// The returned File aliases src, which the caller must not modify
// afterwards.
func Cut(src []byte, opts Options) *File {
	if len(src) > MaxSourceBytes {
		f := &File{Name: opts.File, src: src, Source: lex.Lex(nil, lex.Options{File: opts.File})}
		f.Diagnostics = append(f.Diagnostics, diag.MustRaise(
			diag.Position{File: opts.File},
			diag.ErrCodeLimitExceeded,
			diag.ArgString("source bytes"), diag.ArgInt(MaxSourceBytes),
		))

		return f
	}

	byLine := cutIn(src, opts, lex.CommentEndOfLine)
	broken := countSevere(byLine)
	if broken == 0 {
		return byLine
	}

	paired := cutIn(src, opts, lex.CommentPaired)

	if resolved := broken - countSevere(paired); resolved > 0 {
		c := cutter{file: opts.File, out: paired}
		c.raise(0, diag.ErrCodePairedCommentMode, diag.ArgInt(resolved))
		if c.stopped {
			paired.Modules = nil
		}

		// An unmatched "--" can hide the rest of an end-of-line file
		// under the paired rule. Fewer diagnostics cannot justify losing
		// a file the first reading kept, including when the mode notice
		// itself exceeds the diagnostic cap.
		if fatal(paired) && !fatal(byLine) {
			return byLine
		}

		return paired
	}

	return byLine
}

// countSevere counts the diagnostics that mean something was lost. Those
// are the ones a change of comment rule could plausibly resolve; a
// warning about a hyphen run is noise either way, and letting it vote
// would flip files between modes for no gain.
func countSevere(f *File) int {
	n := 0
	for _, d := range f.Diagnostics {
		if d.Severity().NeedsBaselineReason() {
			n++
		}
	}

	return n
}

// fatal reports whether a reading cost the file rather than a
// declaration.
func fatal(f *File) bool {
	for _, d := range f.Diagnostics {
		if d.Severity() == diag.SeverityFatal {
			return true
		}
	}

	return false
}

func cutIn(src []byte, opts Options, mode lex.CommentMode) *File {
	res := lex.Lex(src, lex.Options{File: opts.File, Comments: mode})
	f := &File{
		Name:        opts.File,
		Diagnostics: res.Diagnostics,
		Comments:    mode,
		Source:      res,
		src:         src,
	}

	c := cutter{res: res, toks: res.Tokens, file: opts.File, out: f}
	for _, d := range res.Diagnostics {
		if d.Severity() == diag.SeverityFatal {
			c.stopped = true
		}
	}
	c.run()
	f.maxDepth = c.maxDepth

	return f
}

// cutter is one pass over one file's tokens.
type cutter struct {
	res  *lex.Result
	toks []lex.Token
	file string
	out  *File

	frames   int
	maxDepth int
	stopped  bool
}

func (c *cutter) run() {
	if c.stopped {
		return
	}

	i := 0
	for i < len(c.toks) {
		start, ok := c.findModuleHeader(i)
		if !ok {
			c.reportTrailing(i)

			break
		}

		i = c.cutModule(start)
		if c.stopped {
			break
		}
	}

	// A source with no significant tokens never entered the loop, so it
	// never reached the report above. Empty files and files holding
	// nothing but comments both land here, and both are as much "not a
	// MIB" as a file full of prose is. Saying nothing about them would
	// break the one promise a read makes: every file answers with a
	// module or with the reason there is none.
	if !c.stopped && len(c.out.Modules) == 0 {
		c.reportTrailing(len(c.toks))
	}

	if c.stopped {
		c.out.Modules = nil
	}
}

// reportTrailing accounts for the tokens after the last module. Text
// before the first module is a file's preamble — an RFC excerpt, a
// changelog, a copyright block — and is silently ignored; text after the
// final END is a trailer somebody left behind, which is worth saying and
// is not worth the file.
func (c *cutter) reportTrailing(i int) {
	switch {
	case len(c.out.Modules) == 0:
		c.raise(0, diag.ErrCodeMissingModuleHeader)
		c.stopped = true
	case i < len(c.toks):
		c.raise(int(c.toks[i].Offset), diag.ErrCodeContentAfterEnd)
	}
}

// findModuleHeader returns the token index of the next module's name.
func (c *cutter) findModuleHeader(from int) (int, bool) {
	for i := from; i < len(c.toks); i++ {
		if h, ok := c.recognize(i); ok && h.module {
			return i, true
		}
	}

	return 0, false
}

// cutModule frames one module and returns the token index after it.
func (c *cutter) cutModule(nameTok int) int {
	h, _ := c.recognize(nameTok)
	mod := Module{
		Name: c.res.Text(c.toks[nameTok]),
		Span: Span{Start: c.toks[nameTok].Offset, End: c.toks[h.body-1].End()},
	}

	i := h.body
	for i < len(c.toks) {
		if c.keyword(i) == lex.KeywordEnd {
			mod.Span.End = c.toks[i].End()
			i++

			break
		}

		next, ok := c.recognize(i)
		if ok && next.module {
			// The module never closed. Ending it here rather than at the
			// end of the file keeps the damage inside one module.
			break
		}

		var fr Frame
		if ok {
			fr, i = c.cutFrame(next, i)
		} else {
			fr, i = c.cutUnrecognized(i)
		}

		mod.Frames = append(mod.Frames, fr)
		mod.Span.End = fr.Span.End
		if c.stopped {
			return i
		}
	}

	c.out.Modules = append(c.out.Modules, mod)

	return i
}

// cutFrame consumes one declaration and returns it with the token index
// after it.
func (c *cutter) cutFrame(h head, start int) (Frame, int) {
	end := c.scan(h)
	if end <= start {
		end = start + 1
	}

	fr := Frame{
		Kind:   h.kind,
		Span:   Span{Start: c.toks[start].Offset, End: c.toks[end-1].End()},
		Tokens: c.toks[start:end],
	}
	if h.name >= 0 {
		fr.Name = c.res.Text(c.toks[h.name])
	}

	c.account(fr)

	return fr, end
}

// cutUnrecognized consumes the run of tokens no head form claimed. The
// run ends where the next head begins, so an unclassifiable declaration
// costs itself and nothing after it.
func (c *cutter) cutUnrecognized(start int) (Frame, int) {
	c.raise(int(c.toks[start].Offset), diag.ErrCodeUnrecognizedDeclaration, diag.ArgString(c.res.Text(c.toks[start])))

	end := c.scanNextHead(start + 1)
	fr := Frame{
		Kind:   KindUnrecognized,
		Name:   c.res.Text(c.toks[start]),
		Span:   Span{Start: c.toks[start].Offset, End: c.toks[end-1].End()},
		Tokens: c.toks[start:end],
	}

	c.account(fr)

	return fr, end
}

// account enforces the per-file limits a frame can push past.
func (c *cutter) account(fr Frame) {
	c.frames++
	if c.frames > MaxDeclarations {
		c.limit("declarations", MaxDeclarations, int(fr.Span.Start))

		return
	}
	if fr.Span.Len() > MaxFrameBytes {
		c.limit("declaration bytes", MaxFrameBytes, int(fr.Span.Start))
	}
}

// scan finds where the frame h opened ends, returning the token index
// one past its last token.
func (c *cutter) scan(h head) int {
	switch h.term {
	case termAssignValue:
		return c.scanAssignValue(h.body)
	case termBraceGroup:
		return c.scanBraceGroup(h.body)
	case termSemicolon:
		return c.scanSemicolon(h.body)
	case termEnd:
		return c.scanMacroEnd(h.body)
	case termNextHead:
		return c.scanNextHead(h.body)
	default:
		return c.scanNextHead(h.body)
	}
}

// scanAssignValue ends the frame after the value that follows the first
// "::=" at depth zero. A brace group and a bare integer are both
// accepted wherever one of them is expected: a macro invocation written
// with a bare number and a TRAP-TYPE written with braces are both
// deviations the declaration parser can grade, and mis-framing them here
// would cost the declarations around them too.
func (c *cutter) scanAssignValue(from int) int {
	depth := 0

	for i := from; i < len(c.toks); i++ {
		if depth == 0 {
			if c.toks[i].Kind == lex.KindAssign {
				return c.scanValue(i + 1)
			}
			if c.isBoundary(i) {
				return i
			}
		}

		depth = c.step(depth, i)
		if c.stopped {
			return i + 1
		}
	}

	return len(c.toks)
}

func (c *cutter) scanValue(at int) int {
	switch {
	case at >= len(c.toks):
		return len(c.toks)
	case c.toks[at].Kind == lex.KindLeftBrace:
		return c.scanBraceGroup(at)
	case c.toks[at].Kind == lex.KindNumber:
		return at + 1
	case c.toks[at].Kind == lex.KindMinus && c.tokenKind(at+1) == lex.KindNumber:
		return at + 2
	default:
		return c.scanNextHead(at)
	}
}

// scanBraceGroup ends the frame after the balanced brace group at or
// after from. Depth is what keeps a DEFVAL { { … } }, a SIZE clause and
// an enumeration inside the frame that owns them.
func (c *cutter) scanBraceGroup(from int) int {
	open := from
	for open < len(c.toks) && c.toks[open].Kind != lex.KindLeftBrace {
		if c.isBoundary(open) {
			return open
		}
		open++
	}
	if open >= len(c.toks) {
		return len(c.toks)
	}

	depth := 0
	for i := open; i < len(c.toks); i++ {
		depth = c.step(depth, i)
		if c.stopped || depth == 0 {
			return i + 1
		}
	}

	return len(c.toks)
}

// scanSemicolon ends the frame after the first semicolon at depth zero.
//
// Its boundary set is narrower than every other scan's, because an
// IMPORTS list puts a module name directly in front of the next symbol
// it imports: "FROM SNMPv2-SMI MODULE-COMPLIANCE" reads as the head
// "<descriptor> <MACRO-KEYWORD>" and would cut the list in half.
func (c *cutter) scanSemicolon(from int) int {
	depth := 0

	for i := from; i < len(c.toks); i++ {
		if depth == 0 {
			if c.toks[i].Kind == lex.KindSemicolon {
				return i + 1
			}
			if c.isOuterBoundary(i) {
				return i
			}
		}

		depth = c.step(depth, i)
		if c.stopped {
			return i + 1
		}
	}

	return len(c.toks)
}

// scanMacroEnd discards a macro definition's body, which is ASN.1 macro
// notation this parser has no use for. The body is full of "::=" and
// ends with its own END, so nothing but a token-level discard survives
// it. Nested BEGIN blocks are counted so a macro that opens one does not
// hand the module's END to the wrong reader.
func (c *cutter) scanMacroEnd(from int) int {
	depth := 0

	for i := from; i < len(c.toks); i++ {
		switch c.keyword(i) {
		case lex.KeywordBegin:
			depth++
		case lex.KeywordEnd:
			if depth <= 1 {
				return i + 1
			}
			depth--
		}
	}

	return len(c.toks)
}

// scanNextHead ends the frame where the next head or the module's END
// begins. It is the only terminator available to the front-"::=" forms,
// which have none of their own.
func (c *cutter) scanNextHead(from int) int {
	depth := 0

	for i := from; i < len(c.toks); i++ {
		if depth == 0 {
			if c.keyword(i) == lex.KeywordEnd {
				return i
			}
			if _, ok := c.recognize(i); ok {
				return i
			}
		}

		depth = c.step(depth, i)
		if c.stopped {
			return i + 1
		}
	}

	return len(c.toks)
}

// isBoundary reports whether token i begins something that cannot be
// inside a declaration, which is where a declaration missing its
// terminator has to stop.
func (c *cutter) isBoundary(i int) bool {
	if c.keyword(i) == lex.KeywordEnd {
		return true
	}

	h, ok := c.recognize(i)

	return ok && (h.module || h.strong)
}

// isOuterBoundary is isBoundary restricted to the heads that cannot
// appear inside an IMPORTS or EXPORTS list.
func (c *cutter) isOuterBoundary(i int) bool {
	if c.keyword(i) == lex.KeywordEnd {
		return true
	}

	h, ok := c.recognize(i)
	if !ok {
		return false
	}

	switch {
	case h.module, h.kind == KindValueAssignment, h.kind == KindImports, h.kind == KindExports:
		return true
	default:
		return false
	}
}

// step applies token i's effect on bracket nesting. A closing bracket
// with nothing open is clamped rather than counted, since vendor prose
// outside a quoted string does occasionally carry one and a negative
// depth would let the next real terminator close two frames.
func (c *cutter) step(depth, i int) int {
	switch c.toks[i].Kind {
	case lex.KindLeftBrace, lex.KindLeftParen, lex.KindLeftBracket:
		depth++
	case lex.KindRightBrace, lex.KindRightParen, lex.KindRightBracket:
		if depth > 0 {
			depth--
		}
	}

	if depth > c.maxDepth {
		c.maxDepth = depth
	}
	if depth > MaxDepth {
		c.limit("nesting depth", MaxDepth, int(c.toks[i].Offset))
	}

	return depth
}

func (c *cutter) limit(what string, bound, offset int) {
	c.out.Diagnostics = append(c.out.Diagnostics, diag.MustRaise(
		diag.Position{File: c.file, Offset: offset},
		diag.ErrCodeLimitExceeded,
		diag.ArgString(what), diag.ArgInt(bound),
	))
	c.stopped = true
}

// raise records a condition at offset, up to the diagnostic cap.
//
// It inherits [diag.MustRaise]'s panic on an uncataloged code or an
// argument count the code's catalog row does not declare. code and args
// come from the cutter's own call sites, so the invariant is proven by
// the arity scan in internal/diag, not left to the caller.
func (c *cutter) raise(offset int, code errs.Code, args ...diag.Arg) {
	if len(c.out.Diagnostics) >= MaxDiagnostics {
		if len(c.out.Diagnostics) == MaxDiagnostics {
			c.limit("diagnostics", MaxDiagnostics, offset)
		}
		c.stopped = true

		return
	}

	c.out.Diagnostics = append(c.out.Diagnostics, diag.MustRaise(diag.Position{File: c.file, Offset: offset}, code, args...))
}

// MaxObservedDepth returns the deepest bracket nesting the last cut of
// this file reached. It is what the corpus sweep measures the depth cap
// against.
func (f *File) MaxObservedDepth() int { return f.maxDepth }
