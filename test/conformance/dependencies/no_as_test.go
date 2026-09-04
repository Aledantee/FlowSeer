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

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "testdata":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, module := range forbiddenModules {
			if importsModule(string(data), module) {
				t.Errorf("%s imports %s", path, module)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning production Go source: %v", err)
	}
}

func importsModule(source, module string) bool {
	return strings.Contains(source, `"`+module+`"`) || strings.Contains(source, `"`+module+`/`)
}

func TestImportsModule(t *testing.T) {
	for _, source := range []string{
		`import "go.aledante.io/as"`,
		`import "go.aledante.io/as/logging"`,
	} {
		if !importsModule(source, "go.aledante.io/as") {
			t.Fatalf("forbidden import was not detected: %s", source)
		}
	}
	if importsModule(`import "go.aledante.io/assert"`, "go.aledante.io/as") {
		t.Fatal("module prefix without a path boundary was rejected")
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
