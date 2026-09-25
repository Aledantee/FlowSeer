package main

import (
	"regexp"
	"testing"
)

// TestNameScopeClash forces a clash between "a-b" + "c" and "a" + "b-c"
// (both join to "ABC"), asserting the second claim gets the X-hex suffix
// and that the result is identical on repeated runs.
func TestNameScopeClash(t *testing.T) {
	s := newNameScope()
	first := s.claim("ABC", "/a-b/c")
	if first != "ABC" {
		t.Fatalf("first claim got %q, want %q", first, "ABC")
	}

	second := s.claim("ABC", "/a/b-c")
	hexPattern := regexp.MustCompile(`^ABCX[0-9a-f]{6}$`)
	if !hexPattern.MatchString(second) {
		t.Errorf("second claim got %q, want ABCX<6-hex-digits>", second)
	}

	// Repeated run with a fresh scope must yield the identical result.
	s2 := newNameScope()
	s2.claim("ABC", "/a-b/c")
	second2 := s2.claim("ABC", "/a/b-c")
	if second2 != second {
		t.Errorf("repeated run got %q, want %q", second2, second)
	}
}
