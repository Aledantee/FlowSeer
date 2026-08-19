package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestRetryable(t *testing.T) {
	transient := New().Retryable().Msg("dial timed out")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unset is not retryable", err: Msg("boom"), want: false},
		{name: "foreign error", err: errors.New("plain"), want: false},
		{name: "own disposition", err: transient, want: true},
		{name: "inherited through wrap", err: Wrap(transient, "collect"), want: true},
		{
			name: "outer Fatal overrules a transient cause",
			err:  From(transient).Fatal().Msg("retry budget exhausted"),
			want: false,
		},
		{
			name: "outer Retryable overrules a fatal cause",
			err:  From(New().Fatal().Msg("inner")).Retryable().Msg("outer"),
			want: true,
		},
		{
			name: "undecided wrapper defers to its cause",
			err:  Wrap(Wrap(transient, "middle"), "outer"),
			want: true,
		},
		{
			name: "through a foreign node",
			err:  Wrap(fmt.Errorf("foreign: %w", transient), "outer"),
			want: true,
		},
		{
			name: "joined branches left to right",
			err:  Wrap(errors.Join(New().Fatal().Msg("left"), transient), "outer"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Retryable(tc.err); got != tc.want {
				t.Errorf("Retryable() = %v, want %v", got, tc.want)
			}
		})
	}
}
