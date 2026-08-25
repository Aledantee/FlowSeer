package output

import (
	"testing"
)

func TestResolveMode(t *testing.T) {
	cases := []struct {
		name         string
		explicitFlag string
		stdoutIsTTY  bool
		want         Mode
	}{
		// Explicit flag wins in both tty states.
		{"explicit flag on tty", "true", true, ModeJSON},
		{"explicit flag off tty", "1", false, ModeJSON},
		{"explicit flag any value", "yes", true, ModeJSON},

		// No flag: tty selects TUI, non-tty selects JSON.
		{"no flag on tty selects TUI", "", true, ModeTUI},
		{"no flag off tty selects JSON", "", false, ModeJSON},

		// Explicit flag with empty value is treated as unset (the flag
		// was not passed). This mirrors the CLI convention where an
		// empty string means the flag is absent.
		{"empty flag on tty selects TUI", "", true, ModeTUI},
		{"empty flag off tty selects JSON", "", false, ModeJSON},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveMode(tc.explicitFlag, tc.stdoutIsTTY)
			if got != tc.want {
				t.Errorf("ResolveMode(%q, %v) = %v, want %v",
					tc.explicitFlag, tc.stdoutIsTTY, got, tc.want)
			}
		})
	}
}
