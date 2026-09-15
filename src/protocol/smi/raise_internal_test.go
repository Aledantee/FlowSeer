package smi

import "testing"

// The arity scan in internal/diag checks each caller of a forwarder
// against the catalog row of the code that caller passes, on the
// assumption that the forwarder hands diag.MustRaise the same code and
// the same argument list. A forwarder that rebound the code or appended
// to args would make every caller pass the scan while MustRaise panicked
// at run time; these two tests pin the assumption for this package's
// forwarders by executing them.
func TestRaiseForwardsCodeAndArgsUnchanged(t *testing.T) {
	r := &resolver{}

	r.raise("test.mib", 3, ErrCodeLimitExceeded, ArgString("frames"), ArgInt(7))

	if got := len(r.diags); got != 1 {
		t.Fatalf("got %d diagnostics, want 1", got)
	}
	wantForwarded(t, r.diags[0])
}

func TestMustRaiseForwardsCodeAndArgsUnchanged(t *testing.T) {
	d := MustRaise(Position{File: "test.mib", Offset: 3}, ErrCodeLimitExceeded, ArgString("frames"), ArgInt(7))

	wantForwarded(t, d)
}

func wantForwarded(t *testing.T, d Diagnostic) {
	t.Helper()

	if got, want := d.Code(), ErrCodeLimitExceeded; got != want {
		t.Errorf("code = %q, want %q", got, want)
	}
	if got, want := d.Message(), "frames limit of 7 exceeded"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
