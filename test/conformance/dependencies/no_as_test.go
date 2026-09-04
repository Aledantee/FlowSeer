package dependencies_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var forbiddenModules = []string{
	"go.aledante.io/ae",
	"go.aledante.io/as",
}

func TestNoASDependency(t *testing.T) {
	root := repositoryRoot(t)

	for _, path := range []string{
		filepath.Join(root, "go.mod"),
		filepath.Join(root, "src", "common", "snmp", "bench", "go.mod"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for _, module := range forbiddenModules {
			if strings.Contains(string(data), module) {
				t.Errorf("%s references %s", path, module)
			}
		}
	}

	err := filepath.WalkDir(filepath.Join(root, "src"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, module := range forbiddenModules {
			if strings.Contains(string(data), `"`+module+`"`) {
				t.Errorf("%s imports %s", path, module)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning production Go source: %v", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating dependency conformance test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}
