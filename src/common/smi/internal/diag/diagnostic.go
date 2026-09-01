// Package diag holds the diagnostic value type, the severity scale and
// the generated diagnostic codes.
//
// It sits below package smi because the lexer, framer and parser raise
// diagnostics and package smi loads what those produce: declaring the
// type at the top would put package smi on both ends of its own import
// graph. Package smi re-exports everything here as aliases, so a caller
// still reports and matches a diagnostic with one import and never sees
// the split, and this package's names are not part of the public API.
package diag

import (
	"fmt"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/catalog"
)

// MaxDiagnostics bounds what one file may report.
//
// It lives here rather than beside the framer's other limits because
// every pass that raises diagnostics has to honor the same cap, and the
// lexer sits below the framer in the import graph. This package is the
// one they all already depend on.
const MaxDiagnostics = 10000

// Position is where in a source file something was found: the file's name
// as the caller gave it, and a byte offset from the start of that file.
//
// There is no line or column here on purpose. A parse raises a diagnostic
// per malformed construct and a vendor corpus raises them by the
// thousand, so the raise path stays as cheap as an integer; line and
// column are worked out from a [LineTable] when somebody actually looks
// at the diagnostic.
type Position struct {
	File   string
	Offset int
}

// LineTable maps byte offsets in one file onto line and column. The lexer
// fills it in as it walks the source, since it is already looking at
// every newline, and rendering reads it afterwards.
//
// The zero value is a usable table for a file with one line. A LineTable
// is not safe for concurrent use while it is being filled in; once the
// lexer is done with a file it is read-only and safe to share.
type LineTable struct {
	// starts holds the offset of each line's first byte. Line 1 starts at
	// offset 0 and is implied, so an empty slice describes a single-line
	// file rather than a file with no lines.
	starts []int32
}

// NewLineTable builds the table for src in one pass. The lexer uses
// [LineTable.AddLine] instead; this constructor is for callers that have
// the bytes and no lexer, such as a test or a tool that renders
// diagnostics somebody else produced.
func NewLineTable(src []byte) *LineTable {
	t := &LineTable{}
	for i, b := range src {
		if b == '\n' {
			t.AddLine(i + 1)
		}
	}

	return t
}

// AddLine records that a new line begins at offset. Offsets must arrive
// in increasing order; an out-of-order or duplicate offset is ignored,
// because a lexer that double-counts a CRLF should produce a slightly
// wrong column rather than a corrupt table.
func (t *LineTable) AddLine(offset int) {
	if offset <= 0 {
		return
	}
	if n := len(t.starts); n > 0 && offset <= int(t.starts[n-1]) {
		return
	}

	t.starts = append(t.starts, int32(offset))
}

// Lines returns the number of lines the table describes.
func (t *LineTable) Lines() int {
	if t == nil {
		return 1
	}

	return len(t.starts) + 1
}

// LineColumn returns the 1-based line and column of offset. The column
// counts bytes rather than runes or display cells: a MIB's non-ASCII
// bytes live in quoted text, and a byte column is what an editor's "go to
// offset" and a hex dump agree on.
//
// A negative offset reports line 1, column 1. An offset past the end of
// the file reports the last line the table knows about, so rendering a
// diagnostic against a truncated table degrades rather than failing.
func (t *LineTable) LineColumn(offset int) (line, column int) {
	if offset < 0 {
		offset = 0
	}
	if t == nil || len(t.starts) == 0 {
		return 1, offset + 1
	}

	// The first line start strictly greater than offset begins the line
	// after the one offset sits on, which makes the search index the
	// 0-based line number.
	idx, _ := slices.BinarySearch(t.starts, int32(offset)+1)

	start := 0
	if idx > 0 {
		start = int(t.starts[idx-1])
	}

	return idx + 1, offset - start + 1
}

// argKind tags which of [Arg]'s payload fields carries the value.
type argKind uint8

const (
	argUnset argKind = iota
	argInt
	argString
)

// Arg is one format argument for a diagnostic's catalog row. It is a
// closed set of two payload types rather than an any, because an any
// boxes its value onto the heap and the raise path is required to
// allocate nothing at all.
//
// Build one with [ArgInt] or [ArgString]. The zero Arg renders as
// "<missing>".
type Arg struct {
	str  string
	num  int64
	kind argKind
}

// ArgInt returns an argument for a %d verb.
func ArgInt(n int) Arg {
	return Arg{num: int64(n), kind: argInt}
}

// ArgString returns an argument for a %s or %q verb. Passing a string
// that already exists costs nothing; materializing one from source bytes
// costs an allocation at the call site, so prefer an interned identifier
// or a keyword literal over a fresh slice-to-string conversion.
func ArgString(s string) Arg {
	return Arg{str: s, kind: argString}
}

// value returns the argument in the form fmt wants. This is the one place
// the value is boxed, and it happens at render time.
func (a Arg) value() any {
	switch a.kind {
	case argInt:
		return a.num
	case argString:
		return a.str
	case argUnset:
		return "<missing>"
	default:
		return "<missing>"
	}
}

// Diagnostic is one thing the parser found wrong with a source file.
//
// It is deliberately not an error and does not implement the error
// interface. A parse that raises a thousand diagnostics has not failed a
// thousand times; leniency is the point, and a file that yields a partial
// module plus diagnostics is a success. Treating a diagnostic as an error
// would invite callers to return the first one and stop, which is exactly
// the behavior this parser exists to avoid.
//
// Its identity is [Diagnostic.Code], an append-only [errs.Code] in the
// smi namespace. Severity, position and message text are not identity: a
// caller that pins a diagnostic in a baseline pins the code, and a later
// release may reword the message or move the offset without breaking it.
//
// A Diagnostic is a value. Copy it freely; it holds no pointers.
type Diagnostic struct {
	pos      Position
	code     errs.Code
	severity Severity
	nargs    uint8
	args     [catalog.MaxArgs]Arg
}

// Raise records that the condition identified by code was found at pos.
// Severity comes from the catalog, so a caller cannot grade the same
// condition two ways in two places.
//
// Raise allocates nothing: it copies args into the returned value and
// formats no text. The message is built by [Diagnostic.Render], which
// runs once per diagnostic a human or a snapshot actually reads.
//
// Raise panics if code is not in the catalog or if len(args) disagrees
// with the row's arity. Both are programming errors in the parser rather
// than anything a MIB can provoke, and both would otherwise surface as a
// mangled message far from the call that caused them.
func Raise(pos Position, code errs.Code, args ...Arg) Diagnostic {
	row, ok := lookup(code)
	if !ok {
		panic(fmt.Sprintf("smi: %q is not a cataloged diagnostic code", code))
	}
	if len(args) != row.Arity {
		panic(fmt.Sprintf("smi: %q takes %d arguments, given %d", code, row.Arity, len(args)))
	}

	d := Diagnostic{
		pos:      pos,
		code:     code,
		severity: Severity(row.Severity),
		nargs:    uint8(len(args)),
	}
	copy(d.args[:], args)

	return d
}

// Position returns where the condition was found.
func (d Diagnostic) Position() Position { return d.pos }

// Code returns the diagnostic's stable identity.
func (d Diagnostic) Code() errs.Code { return d.code }

// Severity returns the grade the catalog gives this code.
func (d Diagnostic) Severity() Severity { return d.severity }

// Rendered is a diagnostic with its text and its place in the file worked
// out. It is what a report, a snapshot, or a log line is built from.
type Rendered struct {
	File     string
	Line     int
	Column   int
	Code     errs.Code
	Severity Severity
	Message  string
}

// String returns the one-line form, "file:line:column: severity: message
// [code]". The code is present because it, not the prose, is what a
// baseline entry matches on.
func (r Rendered) String() string {
	var b strings.Builder

	b.WriteString(r.File)
	fmt.Fprintf(&b, ":%d:%d: ", r.Line, r.Column)
	b.WriteString(r.Severity.String())
	b.WriteString(": ")
	b.WriteString(r.Message)
	b.WriteString(" [")
	b.WriteString(r.Code.String())
	b.WriteString("]")

	return b.String()
}

// Render works out the diagnostic's line, column and message text. lines
// is the table for the file the diagnostic came from; passing nil, or a
// table for a different file, yields a position on line 1 rather than an
// error, because a diagnostic that cannot be printed is worse than one
// printed with a poor position.
func (d Diagnostic) Render(lines *LineTable) Rendered {
	line, column := lines.LineColumn(d.pos.Offset)

	return Rendered{
		File:     d.pos.File,
		Line:     line,
		Column:   column,
		Code:     d.code,
		Severity: d.severity,
		Message:  d.Message(),
	}
}

// Message formats the diagnostic's text from its catalog row. An
// uncataloged code, which [Raise] cannot produce, renders as the code
// itself.
func (d Diagnostic) Message() string {
	row, ok := lookup(d.code)
	if !ok {
		return d.code.String()
	}
	if d.nargs == 0 {
		return row.Format
	}

	values := make([]any, d.nargs)
	for i := range values {
		values[i] = d.args[i].value()
	}

	return fmt.Sprintf(row.Format, values...)
}

// index is the catalog keyed by code. It is built once at init and read
// concurrently thereafter, which is why nothing writes to it later.
var index = func() map[errs.Code]catalog.Entry {
	rows := catalog.Entries()
	m := make(map[errs.Code]catalog.Entry, len(rows))
	for _, r := range rows {
		m[errs.Code(r.Code)] = r
	}

	return m
}()

func lookup(code errs.Code) (catalog.Entry, bool) {
	row, ok := index[code]

	return row, ok
}
