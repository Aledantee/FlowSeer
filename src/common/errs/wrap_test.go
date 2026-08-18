package errs

import (
	"errors"
	"testing"
)

// Covers AE4: the error-first Wrapf signature interpolates its arguments and
// keeps the wrapped error matchable.
func TestWrapfInterpolatesAndPreservesMatching(t *testing.T) {
	dialErr := Msg("connection refused")
	target := "10.0.0.1:161"

	err := Wrapf(dialErr, "dial %s", target)

	if got, want := err.Error(), "dial 10.0.0.1:161: connection refused"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, dialErr) {
		t.Error("wrapped error does not match the error it wrapped")
	}
}

func TestWrapAddsContext(t *testing.T) {
	inner := Msg("connection refused")

	err := Wrap(inner, "open session")

	if got, want := err.Error(), "open session: connection refused"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, inner) {
		t.Error("wrapped error does not match the error it wrapped")
	}
}

func TestWrapOfNilIsNil(t *testing.T) {
	if err := Wrap(nil, "open session"); err != nil {
		t.Errorf("Wrap(nil, …) = %v, want nil", err)
	}
	if err := Wrapf(nil, "dial %s", "host"); err != nil {
		t.Errorf("Wrapf(nil, …) = %v, want nil", err)
	}
}

func TestWrapCarriesForeignErrors(t *testing.T) {
	err := Wrap(errors.New("foreign"), "open session")

	if got, want := err.Error(), "open session: foreign"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
