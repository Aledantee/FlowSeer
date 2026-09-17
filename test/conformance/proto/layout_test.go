// Package conformance holds repository gates for protobuf schemas.
package conformance

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// TestProtoReadmeCoverage ensures every directory under spec/proto/flowseer
// that is not a bare version holder contains a README.md.
func TestProtoReadmeCoverage(t *testing.T) {
	protoRoot := filepath.Join(repoRoot(t), "spec", "proto")
	flowseerDir := filepath.Join(protoRoot, "flowseer")

	for _, missing := range missingProtoReadmes(protoRoot, flowseerDir) {
		t.Errorf("%s: missing README.md", missing)
	}

	t.Run("synthetic", func(t *testing.T) {
		tmp := t.TempDir()
		xV1 := filepath.Join(tmp, "x", "v1")
		if err := os.MkdirAll(xV1, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(xV1, "a.proto"), []byte("syntax = \"proto3\";\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		missing := missingProtoReadmes(tmp, tmp)
		if !slices.Equal(missing, []string{"x/v1"}) {
			t.Errorf("got %v, want [x/v1]", missing)
		}
	})
}

// missingProtoReadmes returns the slash-separated paths relative to baseDir of
// directories under startDir that lack a README.md and are not bare version holders.
func missingProtoReadmes(baseDir, startDir string) []string {
	var missing []string
	err := filepath.WalkDir(startDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if path == baseDir {
			return nil
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}

		var subdirs []os.DirEntry
		hasReadme := false
		for _, e := range entries {
			if e.IsDir() {
				if !strings.HasPrefix(e.Name(), ".") {
					subdirs = append(subdirs, e)
				}
			} else if e.Name() == "README.md" {
				hasReadme = true
			}
		}

		// A bare version holder is a directory whose children are all version directories.
		isBareVersionHolder := len(subdirs) > 0
		for _, s := range subdirs {
			if !versionSegment.MatchString(s.Name()) {
				isBareVersionHolder = false
				break
			}
		}

		if !isBareVersionHolder && !hasReadme {
			rel, err := filepath.Rel(baseDir, path)
			if err != nil {
				return err
			}
			missing = append(missing, filepath.ToSlash(rel))
		}

		return nil
	})
	if err != nil {
		return nil
	}
	slices.Sort(missing)
	return missing
}

// TestProtoReadmeImports checks that every versioned package's README has
// Imports: and Imported by: lines that match the dependencies declared by
// the schemas in the tree.
func TestProtoReadmeImports(t *testing.T) {
	protoRoot := filepath.Join(repoRoot(t), "spec", "proto")
	flowseerDir := filepath.Join(protoRoot, "flowseer")

	violations, err := checkProtoReadmeImports(protoRoot, flowseerDir)
	if err != nil {
		t.Fatalf("checking README imports: %v", err)
	}
	for _, v := range violations {
		t.Errorf("%s: %s", v.readme, v.message)
	}

	t.Run("synthetic", func(t *testing.T) {
		tmp := t.TempDir()
		aDir := filepath.Join(tmp, "flowseer", "a", "v1")
		if err := os.MkdirAll(aDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(aDir, "a.proto"), []byte("edition = \"2024\";\npackage flowseer.a.v1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// a's README names one package too many on Imports: and one too few on Imported by:.
		aReadme := `# A
The flowseer.a.v1 package holds a.

## Boundaries

Imports: extra

Imported by: nothing

Deliberately absent:
- none
`
		if err := os.WriteFile(filepath.Join(aDir, "README.md"), []byte(aReadme), 0o644); err != nil {
			t.Fatal(err)
		}

		bDir := filepath.Join(tmp, "flowseer", "b", "v1")
		if err := os.MkdirAll(bDir, 0o755); err != nil {
			t.Fatal(err)
		}
		bProto := `edition = "2024";
package flowseer.b.v1;
import "flowseer/a/v1/a.proto";
`
		if err := os.WriteFile(filepath.Join(bDir, "b.proto"), []byte(bProto), 0o644); err != nil {
			t.Fatal(err)
		}
		bReadme := `# B
The flowseer.b.v1 package holds b.

## Boundaries

Imports: a

Imported by: nothing

Deliberately absent:
- none
`
		if err := os.WriteFile(filepath.Join(bDir, "README.md"), []byte(bReadme), 0o644); err != nil {
			t.Fatal(err)
		}

		violations, err := checkProtoReadmeImports(tmp, filepath.Join(tmp, "flowseer"))
		if err != nil {
			t.Fatalf("synthetic check failed: %v", err)
		}

		var hasImportsViolation, hasImportedByViolation bool
		for _, v := range violations {
			if v.pkg == "a" {
				if v.field == "Imports" {
					hasImportsViolation = true
				}
				if v.field == "Imported by" {
					hasImportedByViolation = true
				}
			}
		}
		if !hasImportsViolation {
			t.Errorf("synthetic test expected Imports violation on package a, got %v", violations)
		}
		if !hasImportedByViolation {
			t.Errorf("synthetic test expected Imported by violation on package a, got %v", violations)
		}
	})
}

type readmeViolation struct {
	pkg     string
	readme  string
	field   string
	message string
}

func checkProtoReadmeImports(protoRoot, scanDir string) ([]readmeViolation, error) {
	pkgDirs := map[string]string{}
	actualImports := map[string]map[string]bool{}
	actualImportedBy := map[string]map[string]bool{}

	err := filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".proto" {
			return nil
		}

		rel, err := filepath.Rel(protoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		pkg := protoPackage(rel)
		if pkg == "" {
			return nil
		}
		pkgDirs[pkg] = filepath.Dir(path)

		imports, err := protoImports(path)
		if err != nil {
			return fmt.Errorf("reading imports of %s: %w", path, err)
		}

		if actualImports[pkg] == nil {
			actualImports[pkg] = map[string]bool{}
		}
		for _, imp := range imports {
			if strings.HasPrefix(imp, "flowseer/") {
				impPkg := protoPackage(imp)
				if impPkg != "" && impPkg != pkg {
					actualImports[pkg][impPkg] = true
					if actualImportedBy[impPkg] == nil {
						actualImportedBy[impPkg] = map[string]bool{}
					}
					actualImportedBy[impPkg][pkg] = true
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	var violations []readmeViolation
	for _, pkg := range slices.Sorted(maps.Keys(pkgDirs)) {
		dir := pkgDirs[pkg]
		readmePath := filepath.Join(dir, "README.md")
		content, err := os.ReadFile(readmePath)
		if err != nil {
			violations = append(violations, readmeViolation{
				pkg:     pkg,
				readme:  readmePath,
				message: fmt.Sprintf("cannot read README.md: %v", err),
			})
			continue
		}

		gotImports, gotImportedBy, err := parseReadmeBoundaries(string(content))
		if err != nil {
			violations = append(violations, readmeViolation{
				pkg:     pkg,
				readme:  readmePath,
				field:   "Boundaries",
				message: err.Error(),
			})
			continue
		}

		var wantImports []string
		if len(actualImports[pkg]) > 0 {
			wantImports = slices.Sorted(maps.Keys(actualImports[pkg]))
		}
		var wantImportedBy []string
		if len(actualImportedBy[pkg]) > 0 {
			wantImportedBy = slices.Sorted(maps.Keys(actualImportedBy[pkg]))
		}

		if !equalPackageLists(gotImports, wantImports) {
			violations = append(violations, readmeViolation{
				pkg:     pkg,
				readme:  readmePath,
				field:   "Imports",
				message: fmt.Sprintf("Imports: got %v, want %v", formatPkgList(gotImports), formatPkgList(wantImports)),
			})
		}

		if !equalPackageLists(gotImportedBy, wantImportedBy) {
			violations = append(violations, readmeViolation{
				pkg:     pkg,
				readme:  readmePath,
				field:   "Imported by",
				message: fmt.Sprintf("Imported by: got %v, want %v", formatPkgList(gotImportedBy), formatPkgList(wantImportedBy)),
			})
		}
	}

	return violations, nil
}

func parseReadmeBoundaries(content string) (imports, importedBy []string, err error) {
	idx := strings.Index(content, "## Boundaries")
	if idx == -1 {
		return nil, nil, fmt.Errorf("missing ## Boundaries section")
	}
	section := content[idx:]

	importsStr, ok := extractBoundaryField(section, "Imports:")
	if !ok {
		return nil, nil, fmt.Errorf("missing Imports: line under ## Boundaries")
	}

	importedByStr, ok := extractBoundaryField(section, "Imported by:")
	if !ok {
		return nil, nil, fmt.Errorf("missing Imported by: line under ## Boundaries")
	}

	imports = parsePackageList(importsStr)
	importedBy = parsePackageList(importedByStr)
	return imports, importedBy, nil
}

func extractBoundaryField(section, field string) (string, bool) {
	lines := strings.Split(section, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, field) {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, field))
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" || strings.HasPrefix(next, "Imports:") || strings.HasPrefix(next, "Imported by:") || strings.HasPrefix(next, "Deliberately absent:") || strings.HasPrefix(next, "#") {
					break
				}
				val += " " + next
			}
			return val, true
		}
		if i > 0 && strings.HasPrefix(trimmed, "## ") {
			break
		}
	}
	return "", false
}

func parsePackageList(val string) []string {
	val = strings.TrimSpace(val)
	if val == "" || val == "nothing" || val == "nothing FlowSeer-owned" {
		return nil
	}
	parts := strings.Split(val, ",")
	var result []string
	for _, p := range parts {
		item := strings.TrimSpace(p)
		if item != "" {
			result = append(result, item)
		}
	}
	slices.Sort(result)
	return result
}

func equalPackageLists(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return slices.Equal(a, b)
}

func formatPkgList(pkgs []string) string {
	if len(pkgs) == 0 {
		return "nothing"
	}
	return strings.Join(pkgs, ", ")
}
