package smi

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
)

// The diagnostic types are declared in internal/diag and re-exported
// here as aliases.
//
// They have to be declared below package smi because the lexer, framer
// and parser raise them and package smi loads what those packages
// produce: a diagnostic type at the top would put package smi on both
// ends of its own import graph. They are re-exported because the
// obligation that reporting a diagnostic takes one import and one call
// applies just as much to reading one, and because an alias is the same
// type rather than a conversion — a [Diagnostic] the parser raised and
// one a caller constructs are indistinguishable.

// Position is where in a source file something was found: the file's
// name as the caller gave it, and a byte offset from the start of that
// file. There is no line or column here on purpose; see [LineTable].
type Position = diag.Position

// LineTable maps byte offsets in one file onto line and column. The
// zero value is a usable table for a file with one line. A LineTable is
// read-only, and safe to share, once the file it describes has been
// read.
type LineTable = diag.LineTable

// Arg is one format argument for a diagnostic's message. It is a closed
// set of an integer and a string rather than an any, because an any
// boxes its value onto the heap and raising a diagnostic is required to
// allocate nothing. Build one with [ArgInt] or [ArgString].
type Arg = diag.Arg

// Diagnostic is one thing the parser found wrong with a source file.
//
// It is deliberately not an error and does not implement the error
// interface: a parse that raises a thousand diagnostics has not failed a
// thousand times, and giving them the error shape would invite callers
// to return the first and stop. Its identity is [Diagnostic.Code], which
// is what a baseline pins; severity, position and message text are not.
type Diagnostic = diag.Diagnostic

// Rendered is a diagnostic with its text and its place in the file
// worked out. It is what a report, a snapshot or a log line is built
// from.
type Rendered = diag.Rendered

// Severity is how badly a diagnostic reflects on the source, on libsmi's
// 0-6 scale where zero is the most severe. The scale is kept backwards
// from most severity types because every MIB author who has run libsmi
// with -l already reads these numbers.
type Severity = diag.Severity

// The severity scale. Each level says what a caller should conclude, not
// how the parser behaves, because the parser behaves the same way at
// every level: it grades and continues.
const (
	SeverityInternal = diag.SeverityInternal
	SeverityFatal    = diag.SeverityFatal
	SeverityError    = diag.SeverityError
	SeverityMinor    = diag.SeverityMinor
	SeverityChange   = diag.SeverityChange
	SeverityWarning  = diag.SeverityWarning
	SeverityInfo     = diag.SeverityInfo
)

// NewLineTable builds the table for src in one pass. It is for callers
// that have the bytes and no lexer, such as a tool rendering
// diagnostics somebody else produced; a load fills its own tables in as
// it reads.
func NewLineTable(src []byte) *LineTable { return diag.NewLineTable(src) }

// ArgInt returns an argument for a %d verb.
func ArgInt(n int) Arg { return diag.ArgInt(n) }

// ArgString returns an argument for a %s or %q verb. Passing a string
// that already exists costs nothing; materializing one from source bytes
// costs an allocation at the call site.
func ArgString(s string) Arg { return diag.ArgString(s) }

// MustRaise records that the condition identified by code was found at
// pos. Severity comes from the catalog, so a caller cannot grade the same
// condition two ways in two places.
//
// MustRaise allocates nothing and formats no text: the message is built
// by [Diagnostic.Render], which runs once per diagnostic somebody reads.
//
// It panics if code is not cataloged or if len(args) disagrees with the
// code's arity, both of which are bugs in the raising code rather than
// anything a MIB can provoke. code and args are not closed here, so the
// panic travels to the caller; the invariant is proven by the arity scan
// in internal/diag, which resolves every first-party call reaching this
// function to a catalog row.
func MustRaise(pos Position, code errs.Code, args ...Arg) Diagnostic {
	return diag.MustRaise(pos, code, args...)
}

// Severities returns the scale from most to least severe, every level
// present, so a snapshot that groups by severity has a shape that does
// not change with its content.
func Severities() []Severity { return diag.Severities() }

// ParseSeverity returns the severity written as tag, the inverse of
// [Severity.String]. It exists so a baseline file committed by one
// release is still readable by the next.
func ParseSeverity(tag string) (Severity, error) { return diag.ParseSeverity(tag) }
