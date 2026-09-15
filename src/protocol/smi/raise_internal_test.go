package smi

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/catalog"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/diag"
)

// TestRaiseForwardsCodeAndArgsUnchanged and
// TestMustRaiseForwardsCodeAndArgsUnchanged are the executed half of the
// proof described at spreadFinding in internal/diag's arity scan, one per
// forwarder this package declares: every catalog row goes through the
// forwarder and comes out as the diagnostic diag.MustRaise builds from
// the same position, code and arguments.
func TestRaiseForwardsCodeAndArgsUnchanged(t *testing.T) {
	forEachRow(t, func(t *testing.T, code errs.Code, args []Arg) {
		r := &resolver{}
		r.raise("test.mib", 3, code, args...)

		if got := len(r.diags); got != 1 {
			t.Fatalf("got %d diagnostics, want 1", got)
		}
		wantForwarded(t, r.diags[0], code, args)
	})
}

func TestMustRaiseForwardsCodeAndArgsUnchanged(t *testing.T) {
	forEachRow(t, func(t *testing.T, code errs.Code, args []Arg) {
		wantForwarded(t, MustRaise(Position{File: "test.mib", Offset: 3}, code, args...), code, args)
	})
}

// forEachRow runs check once per catalog row with that row's code and
// its arity's worth of distinct arguments.
func forEachRow(t *testing.T, check func(t *testing.T, code errs.Code, args []Arg)) {
	t.Helper()

	for _, row := range catalog.Entries() {
		t.Run(row.Code, func(t *testing.T) {
			args := make([]Arg, row.Arity)
			for i := range args {
				args[i] = ArgInt(i + 1)
			}

			check(t, errs.Code(row.Code), args)
		})
	}
}

func wantForwarded(t *testing.T, d Diagnostic, code errs.Code, args []Arg) {
	t.Helper()

	if got := d.Code(); got != code {
		t.Errorf("code = %q, want %q", got, code)
	}
	if want := diag.MustRaise(diag.Position{File: "test.mib", Offset: 3}, code, args...); d != want {
		t.Errorf("diagnostic = %+v, want %+v", d, want)
	}
}
