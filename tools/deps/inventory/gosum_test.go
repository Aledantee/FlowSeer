package inventory

import (
	"path/filepath"
	"testing"
)

func TestParseGoSumKeepsZipHashesAndSkipsRepositoryModules(t *testing.T) {
	entries, err := parseGoSum(filepath.Join("testdata", "gosum", "go.sum"), "go.mod", "example.com/root", map[string]bool{
		"example.com/keep": true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if got := entries[0]; got.Name != "example.com/keep" || got.Version != "v1.2.3" || got.Hash != "h1:keep" || !got.Direct {
		t.Fatalf("first entry = %#v", got)
	}
	if got := entries[1]; got.Name != "example.com/other" || got.Version != "v1.0.0" || got.Hash != "h1:other" || got.Direct {
		t.Fatalf("second entry = %#v", got)
	}
}
