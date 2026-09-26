package errs

import (
	"errors"
	"strings"
	"testing"
)

// The error-first Wrapf signature interpolates its arguments and
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

func TestWrapAsFallsThroughToInnermostError(t *testing.T) {
	cause := New().Code(NewCode("test/as-innermost")).Msg("cause failure")
	wrapped := Wrap(cause, "outer context")

	var target *Error
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As(wrapped, &target) = false, want true")
	}
	if target != cause {
		t.Errorf("errors.As bound %p (%v), want innermost cause %p (%v)", target, target, cause, cause)
	}

	twice := Wrap(wrapped, "second outer context")
	var targetTwice *Error
	if !errors.As(twice, &targetTwice) || targetTwice != cause {
		t.Errorf("errors.As through multiple wraps bound %p, want %p", targetTwice, cause)
	}
}

func TestWrapForeignStacklessErrorCapturesOriginStack(t *testing.T) {
	foreign := errors.New("foreign failure")
	wrapped := wrapOriginForTest(foreign)

	captured := stacks(wrapped)
	if got, want := len(captured), 1; got != want {
		t.Fatalf("stacks(wrapped) len = %d, want %d", got, want)
	}
	frames := captured[0].frames()
	if len(frames) == 0 {
		t.Fatal("captured stack symbolizes to no frames")
	}
	if !strings.Contains(frames[0], "wrapOriginForTest") {
		t.Errorf("first frame = %q, want wrapOriginForTest call site", frames[0])
	}
}

func wrapOriginForTest(err error) error {
	return Wrap(err, "wrapped foreign")
}

func TestWrapErrorWithExistingStackSkipsCapture(t *testing.T) {
	origin := New().Msg("origin with stack")
	if got, want := len(stacks(origin)), 1; got != want {
		t.Fatalf("origin stacks = %d, want %d", got, want)
	}

	wrapped := Wrap(origin, "outer context")
	if got, want := len(stacks(wrapped)), 1; got != want {
		t.Errorf("wrapped error holds %d stacks, want %d", got, want)
	}
	if got := len(wrapped.(*wrapError).stack); got != 0 {
		t.Errorf("wrapper captured %d program counters, want 0", got)
	}

	foreignWrapped := Wrap(errors.New("foreign"), "first wrap")
	if got, want := len(stacks(foreignWrapped)), 1; got != want {
		t.Fatalf("first wrap stacks = %d, want %d", got, want)
	}
	twiceWrapped := Wrap(foreignWrapped, "second wrap")
	if got, want := len(stacks(twiceWrapped)), 1; got != want {
		t.Errorf("twice-wrapped foreign error holds %d stacks, want %d", got, want)
	}
	if got := len(twiceWrapped.(*wrapError).stack); got != 0 {
		t.Errorf("outer wrapper captured %d program counters, want 0", got)
	}
}

func TestWrapRegressionGuards(t *testing.T) {
	// Guard 1: Wrap(nil) returns nil
	if err := Wrap(nil, "context"); err != nil {
		t.Errorf("Wrap(nil, ...) = %v, want nil", err)
	}

	// Guard 2: errors.Is through Wrap holds
	inner := Msg("sentinel failure")
	wrapped := Wrap(inner, "outer context")
	if !errors.Is(wrapped, inner) {
		t.Error("errors.Is(wrapped, inner) = false, want true")
	}

	// Guard 3: message reads "msg: cause" in that order
	if got, want := wrapped.Error(), "outer context: sentinel failure"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestWrapChainWalkersAndWrapf(t *testing.T) {
	code := NewCode("test/chain-walker")
	cause := New().Code(code).Msg("cause failure")

	// errs.CodeOf across Wrap and Wrapf
	wrapped := Wrap(cause, "context")
	if gotCode, ok := CodeOf(wrapped); !ok || gotCode != code {
		t.Errorf("CodeOf(Wrap) = (%v, %v), want (%v, true)", gotCode, ok, code)
	}

	wrappedF := Wrapf(cause, "context %d", 42)
	if gotCode, ok := CodeOf(wrappedF); !ok || gotCode != code {
		t.Errorf("CodeOf(Wrapf) = (%v, %v), want (%v, true)", gotCode, ok, code)
	}

	// errors.Is across Wrap and Wrapf for coded errors
	codedMatch := New().Code(code).Msg("different instance same code")
	if !errors.Is(wrapped, codedMatch) {
		t.Error("errors.Is(Wrap, codedMatch) = false, want true")
	}
	if !errors.Is(wrappedF, codedMatch) {
		t.Error("errors.Is(Wrapf, codedMatch) = false, want true")
	}

	// Wrapf has the same errors.As fallthrough as Wrap
	var target *Error
	if !errors.As(wrappedF, &target) || target != cause {
		t.Errorf("errors.As(wrappedF) bound %p, want cause %p", target, cause)
	}

	// Wrapf captures origin stack for foreign stackless error
	foreignWrappedF := wrapfOriginForTest(errors.New("foreign"))
	captured := stacks(foreignWrappedF)
	if got, want := len(captured), 1; got != want {
		t.Fatalf("stacks(foreignWrappedF) len = %d, want %d", got, want)
	}
	frames := captured[0].frames()
	if len(frames) == 0 {
		t.Fatal("captured stack symbolizes to no frames")
	}
	if !strings.Contains(frames[0], "wrapfOriginForTest") {
		t.Errorf("first frame = %q, want wrapfOriginForTest call site", frames[0])
	}

	// Wrapf of nil is nil
	if err := Wrapf(nil, "dial %s", "host"); err != nil {
		t.Errorf("Wrapf(nil, ...) = %v, want nil", err)
	}

	// Wrapf message ordering "msg: cause"
	if got, want := wrappedF.Error(), "context 42: cause failure"; got != want {
		t.Errorf("Wrapf.Error() = %q, want %q", got, want)
	}
}

func wrapfOriginForTest(err error) error {
	return Wrapf(err, "dial %s", "10.0.0.1:161")
}
