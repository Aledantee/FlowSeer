package main

import (
	"strings"
	"testing"
)

// TestSplitDocKeepsDeprecatedOffLineStart proves the wrapper never opens a
// line with "deprecated": gocritic's deprecatedComment check reads such a
// line of copied MIB prose as a malformed deprecation notice. The words and
// their order must survive the reflow unchanged.
func TestSplitDocKeepsDeprecatedOffLineStart(t *testing.T) {
	cases := []string{
		// The lower-case form, as copied MIB prose uses it: a plain wrap
		// puts "deprecated in" at the start of the third line.
		"The number of packets received on this interface since the counter " +
			"was last reset by the management station of the site. This object " +
			"is deprecated in the current revision.",
		// The colon form, which a plain wrap also puts at a line start.
		"This sentence is long enough that a plain wrap moves the next word down. Deprecated: use the other object.",
	}
	for _, in := range cases {
		lines := splitDoc(in)
		for i, l := range lines {
			if strings.HasPrefix(strings.ToLower(l), "deprecat") {
				t.Errorf("splitDoc line %d starts with a deprecation marker: %q", i, l)
			}
		}
		if got, want := strings.Join(lines, " "), strings.Join(strings.Fields(in), " "); got != want {
			t.Errorf("splitDoc changed the words:\n got %q\nwant %q", got, want)
		}
	}
}
