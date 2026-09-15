package lex

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/catalog"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
)

// TestRaiseForwardsCodeAndArgsUnchanged is the executed half of the
// proof described at spreadFinding in internal/diag's arity scan: every
// catalog row goes through raise and comes out as the diagnostic
// MustRaise builds from the same position, code and arguments.
func TestRaiseForwardsCodeAndArgsUnchanged(t *testing.T) {
	for _, row := range catalog.Entries() {
		t.Run(row.Code, func(t *testing.T) {
			code := errs.Code(row.Code)
			args := make([]diag.Arg, row.Arity)
			for i := range args {
				args[i] = diag.ArgInt(i + 1)
			}

			// A fresh lexer per row keeps the diagnostic cap from swallowing
			// a later row, which the count check below would otherwise miss.
			l := lexer{file: "test.mib", out: &Result{}}
			l.raise(3, code, args...)

			if got := len(l.out.Diagnostics); got != 1 {
				t.Fatalf("got %d diagnostics, want 1", got)
			}
			d := l.out.Diagnostics[0]
			if got := d.Code(); got != code {
				t.Errorf("code = %q, want %q", got, code)
			}
			if want := diag.MustRaise(diag.Position{File: "test.mib", Offset: 3}, code, args...); d != want {
				t.Errorf("diagnostic = %+v, want %+v", d, want)
			}
		})
	}
}
