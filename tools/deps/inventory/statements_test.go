package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckStatementsFixture(t *testing.T) {
	root := copyStatementsFixture(t)
	findings, err := CheckStatements(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestCheckStatementsRefusesOneInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		message string
		mutate  func(t *testing.T, root string)
	}{
		{
			name:    "missing statement",
			message: "direct dependency has no statement",
			mutate: func(t *testing.T, root string) {
				removeStatement(t, root, "go/example.test/dep.md")
			},
		},
		{
			name:    "orphan statement",
			message: "statement has no direct dependency",
			mutate: func(t *testing.T, root string) {
				writeStatement(t, root, "go/example.test/orphan.md", validStatement("example.test/orphan"))
			},
		},
		{
			name:    "empty section",
			message: "section Why it is safe is empty",
			mutate: func(t *testing.T, root string) {
				path := filepath.Join(root, statementRoot, "go", "example.test", "dep.md")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = []byte(strings.Replace(string(data), "The pinned version is `v0.1.0`. The dependency is used by the shipping package.\n", "", 1))
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "invalid verdict",
			message: "verdict is invalid",
			mutate: func(t *testing.T, root string) {
				replaceMetadata(t, root, "verdict: keep", "verdict: maybe")
			},
		},
		{
			name:    "invalid criteria",
			message: "criteria is invalid",
			mutate: func(t *testing.T, root string) {
				replaceMetadata(t, root, "criteria: deploy", "criteria: inspect")
			},
		},
		{
			name:    "invalid approved",
			message: "approved must be empty or an ISO date",
			mutate: func(t *testing.T, root string) {
				replaceMetadata(t, root, "approved: \"\"", "approved: soon")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := copyStatementsFixture(t)
			test.mutate(t, root)
			findings, err := CheckStatements(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || findings[0].Message != test.message {
				t.Fatalf("findings = %#v, want one %q", findings, test.message)
			}
		})
	}
}

func copyStatementsFixture(t *testing.T) string {
	t.Helper()
	source := filepath.Join("testdata", "statements")
	root := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if filepath.Base(target) == "go.mod.fixture" {
			target = filepath.Join(filepath.Dir(target), "go.mod")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func removeStatement(t *testing.T, root, relative string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, statementRoot, relative)); err != nil {
		t.Fatal(err)
	}
}

func writeStatement(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, statementRoot, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceMetadata(t *testing.T, root, old, replacement string) {
	t.Helper()
	path := filepath.Join(root, statementRoot, "go", "example.test", "dep.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), old, replacement, 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func validStatement(name string) string {
	return "---\nname: " + name + "\necosystem: go\nrequired_by:\n  - go.mod\ncriteria: run\nverdict: keep\napproved: \"\"\n---\n\n## Why it is required\n\nThe fixture requires it.\n\n## Why it is safe\n\nThe fixture pins it.\n\n## Why not owned code\n\nThe fixture does not own it.\n"
}
