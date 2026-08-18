package errs

import (
	"errors"
	"testing"
)

func TestBuilderChainCarriesCauseAndMessage(t *testing.T) {
	sentinel := Msg("sentinel")
	err := New().Attr("port", 161).Cause(sentinel).Msg("x")

	if !errors.Is(err, sentinel) {
		t.Error("built error does not match its cause")
	}
	if got, want := err.(*Error).msg, "x"; got != want {
		t.Errorf("msg = %q, want %q", got, want)
	}
	if got := Attributes(err)["port"]; got != 161 {
		t.Errorf("port attr = %v, want 161", got)
	}
}

func TestBuilderCauseIsNilSafe(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "no arguments", err: New().Cause().Msg("m")},
		{name: "nil argument", err: New().Cause(nil).Msg("m")},
		{name: "nil among causes", err: New().Cause(nil, nil).Msg("m")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != "m" {
				t.Errorf("Error() = %q, want %q", got, "m")
			}
			if got := tc.err.(*Error).Unwrap(); got != nil {
				t.Errorf("Unwrap() = %v, want nil", got)
			}
		})
	}
}

func TestFromPreservesMatching(t *testing.T) {
	sentinel := Msg("sentinel")

	built := From(sentinel).Msg("ctx")
	if !errors.Is(built, sentinel) {
		t.Error("From(err).Msg does not preserve errors.Is against err")
	}

	if got := From(nil).Msg("ctx").Error(); got != "ctx" {
		t.Errorf("From(nil).Msg() = %q, want %q", got, "ctx")
	}
}

// A partially built Builder is reusable: two errors derived from one base
// must not share the base's attribute or cause backing arrays.
func TestBuilderReuseDoesNotAlias(t *testing.T) {
	first := Msg("first")
	second := Msg("second")
	base := New().Attr("shared", true)

	a := base.Attr("only", "a").Cause(first).Msg("a")
	b := base.Attr("only", "b").Cause(second).Msg("b")

	if got := Attributes(a)["only"]; got != "a" {
		t.Errorf("first error only attr = %v, want a", got)
	}
	if got := Attributes(b)["only"]; got != "b" {
		t.Errorf("second error only attr = %v, want b", got)
	}
	if errors.Is(a, second) || errors.Is(b, first) {
		t.Error("reused builder leaked causes between derived errors")
	}
}

func TestMsgfFormats(t *testing.T) {
	err := New().Msgf("walk %s stalled after %d rows", "ifTable", 3)

	if got, want := err.Error(), "walk ifTable stalled after 3 rows"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
