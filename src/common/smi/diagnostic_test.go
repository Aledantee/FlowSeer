package smi_test

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/smi"
)

func TestLineColumn(t *testing.T) {
	// Offsets 0-5 are "alpha\n", so 6 is the first byte of line 2.
	src := []byte("alpha\nbeta\n\ngamma")
	table := smi.NewLineTable(src)

	tests := []struct {
		name      string
		offset    int
		line, col int
	}{
		{name: "start of file", offset: 0, line: 1, col: 1},
		{name: "inside the first line", offset: 3, line: 1, col: 4},
		{name: "the first line's newline", offset: 5, line: 1, col: 6},
		{name: "first byte of the second line", offset: 6, line: 2, col: 1},
		{name: "last byte of the second line", offset: 9, line: 2, col: 4},
		{name: "an empty line", offset: 11, line: 3, col: 1},
		{name: "first byte of the last line", offset: 12, line: 4, col: 1},
		{name: "end of file", offset: len(src), line: 4, col: 6},
		{name: "past end of file", offset: len(src) + 10, line: 4, col: 16},
		{name: "a negative offset", offset: -1, line: 1, col: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line, col := table.LineColumn(tc.offset)
			if line != tc.line || col != tc.col {
				t.Errorf("LineColumn(%d) = %d:%d, want %d:%d", tc.offset, line, col, tc.line, tc.col)
			}
		})
	}

	if got := table.Lines(); got != 4 {
		t.Errorf("Lines() = %d, want 4", got)
	}
}

func TestLineTableZeroValueIsOneLine(t *testing.T) {
	var table smi.LineTable

	line, col := table.LineColumn(7)
	if line != 1 || col != 8 {
		t.Errorf("LineColumn(7) on the zero table = %d:%d, want 1:8", line, col)
	}
}

// Rendering must survive a caller that has no table for the file, since
// a diagnostic nobody can print is worse than one printed on line 1.
func TestRenderWithoutATable(t *testing.T) {
	d := smi.Raise(smi.Position{File: "X.mib", Offset: 40}, smi.ErrCodeUnterminatedString)

	got := d.Render(nil)
	if got.Line != 1 || got.Column != 41 {
		t.Errorf("Render(nil) = %d:%d, want 1:41", got.Line, got.Column)
	}
}

// AddLine takes offsets in increasing order. A lexer that double-counts a
// CRLF should cost a column, not corrupt the table.
func TestAddLineIgnoresOutOfOrderOffsets(t *testing.T) {
	var table smi.LineTable
	table.AddLine(10)
	table.AddLine(10)
	table.AddLine(4)
	table.AddLine(0)
	table.AddLine(20)

	if got := table.Lines(); got != 3 {
		t.Fatalf("Lines() = %d, want 3", got)
	}
	if line, col := table.LineColumn(12); line != 2 || col != 3 {
		t.Errorf("LineColumn(12) = %d:%d, want 2:3", line, col)
	}
}

func TestRender(t *testing.T) {
	src := []byte("FOO-MIB DEFINITIONS ::= BEGIN\nfoo OBJECT-TYPE\n")
	table := smi.NewLineTable(src)

	d := smi.Raise(
		smi.Position{File: "FOO-MIB.mib", Offset: 30},
		smi.ErrCodeUnrecognizedDeclaration,
		smi.ArgString("foo"),
	)

	got := d.Render(table)

	want := smi.Rendered{
		File:     "FOO-MIB.mib",
		Line:     2,
		Column:   1,
		Code:     smi.ErrCodeUnrecognizedDeclaration,
		Severity: smi.SeverityError,
		Message:  `declaration beginning with "foo" is not a recognized declaration head`,
	}
	if got != want {
		t.Errorf("Render() =\n %+v\nwant\n %+v", got, want)
	}

	wantLine := `FOO-MIB.mib:2:1: error: declaration beginning with "foo" is not a recognized declaration head [smi/unrecognized-declaration]`
	if got.String() != wantLine {
		t.Errorf("String() = %q, want %q", got.String(), wantLine)
	}
}

func TestRenderMultipleArguments(t *testing.T) {
	d := smi.Raise(
		smi.Position{File: "BIG-MIB.mib", Offset: 0},
		smi.ErrCodeLimitExceeded,
		smi.ArgString("declarations per file"),
		smi.ArgInt(65536),
	)

	want := "declarations per file limit of 65536 exceeded"
	if got := d.Message(); got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}
}

func TestRaiseCarriesTheCatalogedSeverity(t *testing.T) {
	d := smi.Raise(smi.Position{File: "X.mib"}, smi.ErrCodeHyphenSeparator, smi.ArgInt(5))

	if got := d.Severity(); got != smi.SeverityWarning {
		t.Errorf("Severity() = %v, want %v", got, smi.SeverityWarning)
	}
	if got := d.Code(); got != smi.ErrCodeHyphenSeparator {
		t.Errorf("Code() = %v, want %v", got, smi.ErrCodeHyphenSeparator)
	}
	if got := d.Position().File; got != "X.mib" {
		t.Errorf("Position().File = %q, want %q", got, "X.mib")
	}
}

// Raise panics on a caller bug rather than producing a diagnostic that
// renders as %!d(MISSING) somewhere far from the mistake.
func TestRaisePanics(t *testing.T) {
	tests := []struct {
		name string
		call func()
		want string
	}{
		{
			name: "uncataloged code",
			call: func() { smi.Raise(smi.Position{}, "smi/no-such-thing") },
			want: "not a cataloged diagnostic code",
		},
		{
			name: "too few arguments",
			call: func() { smi.Raise(smi.Position{}, smi.ErrCodeLimitExceeded, smi.ArgInt(1)) },
			want: "takes 2 arguments, given 1",
		},
		{
			name: "too many arguments",
			call: func() {
				smi.Raise(smi.Position{}, smi.ErrCodeUnterminatedString, smi.ArgInt(1))
			},
			want: "takes 0 arguments, given 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("Raise did not panic on %s", tc.name)
				}
				if msg, _ := r.(string); !strings.Contains(msg, tc.want) {
					t.Errorf("panic = %v, want it to mention %q", r, tc.want)
				}
			}()

			tc.call()
		})
	}
}

// A vendor corpus raises far more diagnostics than it renders, so the
// raise path is required to cost nothing beyond the returned value.
func TestRaiseAllocatesNothing(t *testing.T) {
	pos := smi.Position{File: "BIG-MIB.mib", Offset: 4096}
	limit := "declarations per file"

	allocs := testing.AllocsPerRun(200, func() {
		sink = smi.Raise(pos, smi.ErrCodeLimitExceeded, smi.ArgString(limit), smi.ArgInt(65536))
	})
	if allocs != 0 {
		t.Errorf("Raise allocated %.1f times per call, want 0", allocs)
	}
}

// sink keeps the compiler from optimizing the raise away, and is written
// rather than read for the same reason.
var sink smi.Diagnostic
