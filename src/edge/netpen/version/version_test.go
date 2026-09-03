package version

import (
	"strings"
	"testing"
)

// TestStringDefault checks the tool name and labels in development output.
func TestStringDefault(t *testing.T) {
	s := String()
	if !strings.HasPrefix(s, "netpen ") {
		t.Errorf("String(): %q does not start with 'netpen '", s)
	}
	for _, want := range []string{"commit", "built"} {
		if !strings.Contains(s, want) {
			t.Errorf("String(): %q missing %q", s, want)
		}
	}
}

// TestStringInjected checks formatting after assigning release metadata.
// Linker injection is a separate build-level contract.
func TestStringInjected(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, BuildDate
	defer func() {
		Version, Commit, BuildDate = oldV, oldC, oldD
	}()
	Version = "v1.2.3"
	Commit = "abcdef1234567890"
	BuildDate = "2026-08-24T12:00:00Z"
	s := String()
	want := "netpen v1.2.3 (commit abcdef12, built 2026-08-24T12:00:00Z)"
	if s != want {
		t.Errorf("String(): got %q, want %q", s, want)
	}
}

// TestStringShortCommit checks that a hash shorter than eight bytes is retained.
func TestStringShortCommit(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, BuildDate
	defer func() {
		Version, Commit, BuildDate = oldV, oldC, oldD
	}()
	Version = "dev"
	Commit = "short"
	BuildDate = "x"
	s := String()
	// "short" is already <= 8 chars, so it stays as-is.
	if !strings.Contains(s, "commit short") {
		t.Errorf("String(): %q does not contain 'commit short'", s)
	}
}
