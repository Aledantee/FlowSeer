package inventory

import (
	"path/filepath"
	"testing"
)

func TestReadUsesRepositoryRootForGoSum(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/root\n\ngo 1.27\n")
	writeTestFile(t, filepath.Join(root, "go.sum"), "example.test/dependency v1.0.0 h1:dependency\n")

	result, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "example.test/dependency" {
		t.Fatalf("entries = %#v, want repository go.sum entry", result.Entries)
	}
}
