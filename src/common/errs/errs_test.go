package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestMsgRendersMessage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "plain", err: Msg("request timed out"), want: "request timed out"},
		{name: "formatted", err: Msgf("dial %s", "10.0.0.1:161"), want: "dial 10.0.0.1:161"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMsgSentinelsAreDistinctIdentities(t *testing.T) {
	first := Msg("same text")
	second := Msg("same text")

	if errors.Is(first, second) {
		t.Error("sentinels with equal text match, want distinct identities")
	}
	if !errors.Is(first, first) {
		t.Error("sentinel does not match itself")
	}
}

func TestErrorRendersCauses(t *testing.T) {
	inner := Msg("connection refused")
	other := Msg("no route to host")

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "single cause",
			err:  New().Cause(inner).Msg("dial failed"),
			want: "dial failed: connection refused",
		},
		{
			name: "multiple causes",
			err:  New().Cause(inner, other).Msg("dial failed"),
			want: "dial failed: [connection refused; no route to host]",
		},
		{
			name: "nested",
			err:  Wrap(New().Cause(inner).Msg("dial failed"), "open session"),
			want: "open session: dial failed: connection refused",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnwrapExposesCauses(t *testing.T) {
	inner := Msg("inner")
	err := New().Cause(inner).Msg("outer")

	causes := err.(*Error).Unwrap()
	if len(causes) != 1 || !errors.Is(causes[0], inner) {
		t.Errorf("Unwrap() = %v, want [inner]", causes)
	}

	if got := Msg("leaf").(*Error).Unwrap(); got != nil {
		t.Errorf("Unwrap() of causeless error = %v, want nil", got)
	}
}

func TestErrorsAsExtractsConcreteType(t *testing.T) {
	origin := New().Attr("port", 161).Msg("origin")
	err := fmt.Errorf("stdlib layer: %w", Wrap(origin, "middle"))

	var target *Error
	if !errors.As(err, &target) {
		t.Fatal("errors.As did not extract *Error through the chain")
	}
	if target.msg != "middle" {
		t.Errorf("extracted msg = %q, want %q", target.msg, "middle")
	}
}
