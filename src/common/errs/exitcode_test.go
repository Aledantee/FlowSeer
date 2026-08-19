package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	coded := New().ExitCode(3).Msg("inner")

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil is success", err: nil, want: 0},
		{name: "unset defaults to failure", err: Msg("boom"), want: 1},
		{name: "own code", err: coded, want: 3},
		{name: "inherited through wrap", err: Wrap(coded, "outer"), want: 3},
		{
			name: "outermost wins over a higher cause",
			err:  From(coded).ExitCode(2).Msg("outer"),
			want: 2,
		},
		{
			name: "through a foreign node",
			err:  Wrap(fmt.Errorf("foreign: %w", coded), "outer"),
			want: 3,
		},
		{
			name: "joined branches left to right",
			err:  Wrap(errors.Join(coded, New().ExitCode(9).Msg("right")), "outer"),
			want: 3,
		},
		{name: "foreign error alone", err: errors.New("plain"), want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Errorf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

// Zero and negative would report success, so the builder ignores them rather
// than storing a status that ends a failed process with exit 0.
func TestExitCodeIgnoresNonPositive(t *testing.T) {
	for _, code := range []int{0, -1} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			err := New().ExitCode(code).Msg("boom")

			if got := ExitCode(err); got != 1 {
				t.Errorf("ExitCode() = %d, want 1", got)
			}
			if _, ok := exitCodeOf(err); ok {
				t.Error("exitCodeOf reports a code was set")
			}
		})
	}
}
