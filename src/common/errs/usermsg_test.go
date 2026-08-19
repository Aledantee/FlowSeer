package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestUserMessageResolvesOutermostFirst(t *testing.T) {
	inner := New().UserMsg("inner text").Msg("inner")

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "unset", err: Msg("boom"), want: ""},
		{name: "own", err: inner, want: "inner text"},
		{name: "inherited through wrap", err: Wrap(inner, "outer"), want: "inner text"},
		{
			name: "outermost wins",
			err:  From(inner).UserMsg("outer text").Msg("outer"),
			want: "outer text",
		},
		{
			name: "through a foreign node",
			err:  Wrap(fmt.Errorf("foreign: %w", inner), "outer"),
			want: "inner text",
		},
		{
			name: "joined branches left to right",
			err:  Wrap(errors.Join(inner, New().UserMsg("right text").Msg("right")), "outer"),
			want: "inner text",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := UserMessage(tc.err); got != tc.want {
				t.Errorf("UserMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHintResolvesOutermostFirst(t *testing.T) {
	inner := New().Hint("check the credentials").Msg("inner")

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "unset", err: Msg("boom"), want: ""},
		{name: "inherited through wrap", err: Wrap(inner, "outer"), want: "check the credentials"},
		{
			name: "outermost wins",
			err:  From(inner).Hint("raise the timeout").Msg("outer"),
			want: "raise the timeout",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Hint(tc.err); got != tc.want {
				t.Errorf("Hint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The user message and hint are independent of the internal message, which
// keeps naming what actually failed.
func TestUserMsgLeavesInternalMessageIntact(t *testing.T) {
	err := From(Msg("dial 10.0.0.1:161: refused")).
		UserMsg("could not read the device").
		Hint("check that the device is reachable").
		Msg("open session")

	const want = "open session: dial 10.0.0.1:161: refused"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := UserMessage(err); got != "could not read the device" {
		t.Errorf("UserMessage() = %q", got)
	}
}
