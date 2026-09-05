// Package conformance holds repository gates for protobuf schemas.
package conformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtoSourceTreeLayout(t *testing.T) {
	root := repoRoot(t)
	protoRoot := filepath.Join(root, "spec", "proto")

	err := filepath.WalkDir(protoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(protoRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		if reason := protoPathViolation(rel, d.IsDir()); reason != "" {
			t.Errorf("%s: %s", filepath.ToSlash(rel), reason)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", protoRoot, err)
	}
}

func TestProtoPathPolicy(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		isDir bool
		valid bool
	}{
		{name: "schema", path: "flowseer/net/addr/v1/ip.proto", valid: true},
		{name: "package readme", path: "flowseer/net/addr/v1/README.md", valid: true},
		{name: "dotfile placeholder", path: "flowseer/api/switching/v1/.gitkeep", valid: true},
		{name: "Go test", path: "addr_rules_test.go"},
		{name: "source inventory", path: "flowseer/SOURCES.md"},
		{name: "fixture directory", path: "flowseer/net/addr/_test_fixtures", isDir: true},
		{name: "fixture schema", path: "flowseer/net/addr/_test_fixtures/invalid.proto"},
		{name: "testdata schema", path: "flowseer/net/addr/testdata/invalid.proto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protoPathViolation(filepath.FromSlash(tt.path), tt.isDir) == ""
			if got != tt.valid {
				t.Errorf("got valid=%t, want %t", got, tt.valid)
			}
		})
	}
}

func protoPathViolation(path string, isDir bool) string {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		switch part {
		case "_test_fixtures", "fixtures", "testdata":
			return "test artifacts belong under test/conformance/proto/testdata, outside the Buf source tree"
		}
	}

	if isDir {
		return ""
	}
	base := filepath.Base(path)
	// Dotfiles cover .gitkeep placeholders and the .DS_Store files Finder
	// leaves behind; both are ignored by git and neither is schema content.
	if base == "README.md" || strings.HasPrefix(base, ".") || filepath.Ext(path) == ".proto" {
		return ""
	}

	return "spec/proto contains only .proto schemas, package-boundary README.md files, and dotfiles"
}

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
			t.Fatal("repo root not found above the conformance package")
		}

		dir = parent
	}
}
