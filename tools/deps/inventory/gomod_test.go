package inventory

import (
	"path/filepath"
	"testing"
)

func TestDiscoverModulesSkipsRepositoryModuleFromNestedDirects(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/root\n\ngo 1.27\n")
	writeTestFile(t, filepath.Join(root, "nested", "go.mod"), "module example.test/nested\n\ngo 1.27\n\nrequire example.test/root v0.0.0\n")

	modules, err := DiscoverModules(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if module.Manifest != "nested/go.mod" {
			continue
		}
		if len(module.Requires) != 0 || module.Direct["example.test/root"] {
			t.Fatalf("nested direct requirements = %#v, want repository module excluded", module)
		}
		return
	}
	t.Fatal("nested module not found")
}
