package secretguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoRawSecretFields asserts that no exported field under src/ carries
// credential material in a type that renders it. The carrier is
// src/common/secret; a field this test names either changes to a
// secret.Value or, if the name is misleading, gets a name that does not
// read as credential material.
func TestNoRawSecretFields(t *testing.T) {
	root := filepath.Join(repoRoot(t), "src")
	found, err := scanRawSecretFields(root)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	for _, f := range found {
		t.Errorf("raw secret field in src/%s", f)
	}
}

// repoRoot finds the repository root by walking up from the working directory
// until it finds a go.mod declaring the main module path. It mirrors the
// repoRoot pattern from src/common/errs/code_test.go.
func repoRoot(t *testing.T) string {
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
			t.Fatal("repo root not found above the secretguard package")
		}

		dir = parent
	}
}
