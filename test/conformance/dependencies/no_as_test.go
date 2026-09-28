package dependencies_test

import (
	"io/fs"
	"os"
	"path/filepath"
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
		filepath.Join(root, "src", "protocol", "snmp", "bench", "go.mod"),
		filepath.Join(root, "generated", "go", "yang", "go.mod"),
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
			t.Fatalf("got forbidden import undetected for %s, want detected", source)
		}
	}
	if importsModule(`import "go.aledante.io/assert"`, "go.aledante.io/as") {
		t.Fatal("module prefix without a path boundary was rejected")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module go.aledante.io/FlowSeer\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found above the conformance package")
		}

		dir = parent
	}
}
