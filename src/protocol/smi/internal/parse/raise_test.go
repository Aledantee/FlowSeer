package parse

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/lex"
)

// The arity scan in internal/diag checks each caller of raise against
// the catalog row of the code that caller passes, on the assumption
// that raise hands MustRaise the same code and the same argument list.
// A raise that rebound the code or appended to args would make every
// caller pass the scan while MustRaise panicked at run time; this pins
// the assumption by executing it.
func TestRaiseForwardsCodeAndArgsUnchanged(t *testing.T) {
	p := newParser(&lex.Result{}, testFile)

	p.raise(3, diag.ErrCodeLimitExceeded, diag.ArgString("frames"), diag.ArgInt(7))

	if got := len(p.diags); got != 1 {
		t.Fatalf("got %d diagnostics, want 1", got)
	}
	d := p.diags[0]
	if got, want := d.Code(), diag.ErrCodeLimitExceeded; got != want {
		t.Errorf("code = %q, want %q", got, want)
	}
	if got, want := d.Message(), "frames limit of 7 exceeded"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
