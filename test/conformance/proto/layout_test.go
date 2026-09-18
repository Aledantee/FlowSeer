// Package conformance holds repository gates for protobuf schemas.
package conformance

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProtoSourceTreeLayout(t *testing.T) {
	root := repoRoot(t)
	protoRoot := filepath.Join(root, "spec", "proto")

	err := filepath.WalkDir(protoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(protoRoot, p)
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

	missing, err := missingProtoReadmes(protoRoot, flowseerDir)
	if err != nil {
		t.Fatalf("walking %s: %v", flowseerDir, err)
	}
	for _, dir := range missing {
		t.Errorf("%s: missing README.md", dir)
	}

	t.Run("synthetic", func(t *testing.T) {
		tmp := t.TempDir()
		writeFixture(t, filepath.Join(tmp, "x", "v1", "a.proto"), "syntax = \"proto3\";\n")
		// A version directory holding only a placeholder has no schema to
		// describe, and TestProtoPathPolicy blesses the placeholder.
		writeFixture(t, filepath.Join(tmp, "y", "v1", ".gitkeep"), "")

		got, err := missingProtoReadmes(tmp, tmp)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"x/v1"}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	// A walk that cannot read the tree has found nothing, not nothing to find,
	// and the caller has to be able to tell the two apart.
	t.Run("unreadable tree", func(t *testing.T) {
		tmp := t.TempDir()
		if _, err := missingProtoReadmes(tmp, filepath.Join(tmp, "absent")); err == nil {
			t.Error("got no error walking a directory that does not exist, want one")
		}
	})
}

// missingProtoReadmes returns the slash-separated paths relative to baseDir of
// directories under startDir that lack a README.md and are expected to carry one.
func missingProtoReadmes(baseDir, startDir string) ([]string, error) {
	var missing []string
	err := filepath.WalkDir(startDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if p == baseDir {
			return nil
		}

		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}

		var subdirs []os.DirEntry
		hasReadme, hasSchema := false, false
		for _, e := range entries {
			switch {
			case e.IsDir():
				if !strings.HasPrefix(e.Name(), ".") {
					subdirs = append(subdirs, e)
				}
			case e.Name() == "README.md":
				hasReadme = true
			case filepath.Ext(e.Name()) == ".proto":
				hasSchema = true
			}
		}
		if hasReadme {
			return nil
		}

		// A bare version holder is a directory whose children are all version
		// directories; the packages under it state their own identity. A
		// version directory holding no schema has no identity to state either:
		// TestProtoPathPolicy blesses a placeholder there, and demanding a
		// README of a directory whose only file is a .gitkeep would contradict
		// it.
		if bareVersionHolder(subdirs) || (versionSegment.MatchString(d.Name()) && !hasSchema) {
			return nil
		}

		rel, err := filepath.Rel(baseDir, p)
		if err != nil {
			return err
		}
		missing = append(missing, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.Sort(missing)

	return missing, nil
}

// bareVersionHolder reports whether subdirs are all version directories.
func bareVersionHolder(subdirs []os.DirEntry) bool {
	if len(subdirs) == 0 {
		return false
	}
	for _, subdir := range subdirs {
		if !versionSegment.MatchString(subdir.Name()) {
			return false
		}
	}

	return true
}

// TestProtoReadmeImports checks every README under spec/proto/flowseer that
// claims a boundary against the imports the tree declares: a versioned
// package's own imports and importers, and, for a root or intermediate
// directory, the packages crossing its edge either way.
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
		flowseer := filepath.Join(tmp, "flowseer")

		writeFixture(t, filepath.Join(flowseer, "a", "v1", "a.proto"), "edition = \"2024\";\npackage flowseer.a.v1;\n")
		// a/v1 imports nothing and is imported by b/v1, so its README names one
		// package too many on Imports: and one too few on Imported by:.
		writeFixture(t, filepath.Join(flowseer, "a", "v1", "README.md"), boundariesReadme("A", "extra", "nothing"))

		writeFixture(t, filepath.Join(flowseer, "b", "v1", "b.proto"),
			"edition = \"2024\";\npackage flowseer.b.v1;\nimport \"flowseer/a/v1/a.proto\";\n")
		writeFixture(t, filepath.Join(flowseer, "b", "v1", "README.md"), boundariesReadme("B", "a", "c"))
		// The b root's README denies both the import the package under it makes
		// and the one made into it.
		writeFixture(t, filepath.Join(flowseer, "b", "README.md"), boundariesReadme("B root", "nothing FlowSeer-owned", "nothing"))

		// Two versions of c, each importing a different package. Keyed on the
		// package rather than the directory, the two versions' imports would
		// merge and one version would go unchecked; c/v1's README is wrong about
		// its own imports, so a gate that skips it reports nothing for it.
		writeFixture(t, filepath.Join(flowseer, "c", "v1", "c.proto"),
			"edition = \"2024\";\npackage flowseer.c.v1;\nimport \"flowseer/a/v1/a.proto\";\n")
		writeFixture(t, filepath.Join(flowseer, "c", "v1", "README.md"), boundariesReadme("C v1", "b", "nothing"))
		writeFixture(t, filepath.Join(flowseer, "c", "v2", "c.proto"),
			"edition = \"2024\";\npackage flowseer.c.v2;\nimport \"flowseer/b/v1/b.proto\";\n")
		writeFixture(t, filepath.Join(flowseer, "c", "v2", "README.md"), boundariesReadme("C v2", "b", "nothing"))

		violations, err := checkProtoReadmeImports(tmp, flowseer)
		if err != nil {
			t.Fatalf("synthetic check failed: %v", err)
		}

		var got []string
		for _, v := range violations {
			got = append(got, v.readme+" "+v.field)
		}
		slices.Sort(got)

		want := []string{
			"flowseer/a/v1/README.md Imported by",
			"flowseer/a/v1/README.md Imports",
			"flowseer/b/README.md Imported by",
			"flowseer/b/README.md Imports",
			"flowseer/c/v1/README.md Imports",
		}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

type readmeViolation struct {
	// readme is the file's path relative to spec/proto, in slash form.
	readme  string
	field   string
	message string
}

// schemaFile is one schema's FlowSeer-owned imports. Both the file and the
// imports are paths relative to spec/proto, in slash form.
type schemaFile struct {
	rel     string
	imports []string
}

func checkProtoReadmeImports(protoRoot, scanDir string) ([]readmeViolation, error) {
	files, err := collectSchemaImports(protoRoot, scanDir)
	if err != nil {
		return nil, err
	}

	roots, err := boundaryReadmeRoots(protoRoot, scanDir)
	if err != nil {
		return nil, err
	}

	violations := packageReadmeViolations(protoRoot, files)
	for _, root := range roots {
		violations = append(violations, rootReadmeViolations(protoRoot, root, files)...)
	}

	return violations, nil
}

func collectSchemaImports(protoRoot, scanDir string) ([]schemaFile, error) {
	var files []schemaFile
	err := filepath.WalkDir(scanDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}

			return nil
		}
		if filepath.Ext(p) != ".proto" {
			return nil
		}

		rel, err := filepath.Rel(protoRoot, p)
		if err != nil {
			return err
		}

		source, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading imports of %s: %w", filepath.ToSlash(rel), err)
		}

		var owned []string
		for _, imported := range scanImports(string(source)) {
			if strings.HasPrefix(imported, "flowseer/") {
				owned = append(owned, imported)
			}
		}

		files = append(files, schemaFile{rel: filepath.ToSlash(rel), imports: owned})

		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// packageReadmeViolations checks one README per versioned package directory.
// The directory is the key rather than the version-stripped package name: two
// version directories under one package each carry their own README and their
// own imports, and keying on the name would check whichever was walked last
// against both versions' imports merged.
func packageReadmeViolations(protoRoot string, files []schemaFile) []readmeViolation {
	dirs := map[string]bool{}
	imports := map[string]map[string]bool{}
	importedBy := map[string]map[string]bool{}

	for _, file := range files {
		dir := path.Dir(file.rel)
		dirs[dir] = true

		for _, imported := range file.imports {
			importedDir := path.Dir(imported)
			if importedDir == dir {
				continue
			}
			add(imports, dir, protoPackage(imported))
			add(importedBy, importedDir, protoPackage(file.rel))
		}
	}

	var violations []readmeViolation
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		violations = append(violations, readmeViolations(protoRoot, dir, imports[dir], importedBy[dir])...)
	}

	return violations
}

// rootReadmeViolations checks the README of a root or intermediate directory.
// Its Imports: are the packages its schemas reach outside it and its Imported
// by: the packages outside it that reach in, so the claim a root README makes
// about its edge is the same kind of claim a package README makes and is worth
// the same amount.
func rootReadmeViolations(protoRoot, root string, files []schemaFile) []readmeViolation {
	imports := map[string]bool{}
	importedBy := map[string]bool{}

	for _, file := range files {
		inside := withinDir(root, file.rel)
		for _, imported := range file.imports {
			switch {
			case inside && !withinDir(root, imported):
				imports[protoPackage(imported)] = true
			case !inside && withinDir(root, imported):
				importedBy[protoPackage(file.rel)] = true
			}
		}
	}

	return readmeViolations(protoRoot, root, imports, importedBy)
}

// boundaryReadmeRoots returns the directories under scanDir whose README states
// a boundary: every directory that holds one except the version directories,
// whose READMEs packageReadmeViolations checks, and scanDir itself, because no
// import crosses the edge of the tree that holds every FlowSeer package. A
// directory that should carry a README and does not is TestProtoReadmeCoverage's
// to report.
func boundaryReadmeRoots(protoRoot, scanDir string) ([]string, error) {
	var roots []string
	err := filepath.WalkDir(scanDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if p == scanDir || versionSegment.MatchString(d.Name()) {
			return nil
		}

		switch _, err := os.Stat(filepath.Join(p, "README.md")); {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		}

		rel, err := filepath.Rel(protoRoot, p)
		if err != nil {
			return err
		}
		roots = append(roots, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, err
	}

	return roots, nil
}

// readmeViolations compares the ## Boundaries lines of the README in dir, a
// directory relative to spec/proto, against the dependencies the tree declares.
func readmeViolations(protoRoot, dir string, imports, importedBy map[string]bool) []readmeViolation {
	readme := dir + "/README.md"

	content, err := os.ReadFile(filepath.Join(protoRoot, filepath.FromSlash(readme)))
	if err != nil {
		return []readmeViolation{{readme: readme, message: fmt.Sprintf("cannot read README.md: %v", err)}}
	}

	gotImports, gotImportedBy, err := parseReadmeBoundaries(string(content))
	if err != nil {
		return []readmeViolation{{readme: readme, field: "Boundaries", message: err.Error()}}
	}

	var violations []readmeViolation
	if want := slices.Sorted(maps.Keys(imports)); !equalPackageLists(gotImports, want) {
		violations = append(violations, readmeViolation{
			readme:  readme,
			field:   "Imports",
			message: fmt.Sprintf("Imports: got %s, want %s", formatPkgList(gotImports), formatPkgList(want)),
		})
	}
	if want := slices.Sorted(maps.Keys(importedBy)); !equalPackageLists(gotImportedBy, want) {
		violations = append(violations, readmeViolation{
			readme:  readme,
			field:   "Imported by",
			message: fmt.Sprintf("Imported by: got %s, want %s", formatPkgList(gotImportedBy), formatPkgList(want)),
		})
	}

	return violations
}

// withinDir reports whether rel names a file under dir.
func withinDir(dir, rel string) bool {
	return strings.HasPrefix(rel, dir+"/")
}

func add(index map[string]map[string]bool, key, value string) {
	if index[key] == nil {
		index[key] = map[string]bool{}
	}
	index[key][value] = true
}

func writeFixture(t *testing.T, p, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func boundariesReadme(title, imports, importedBy string) string {
	return fmt.Sprintf("# %s\n\n## Boundaries\n\nImports: %s\n\nImported by: %s\n", title, imports, importedBy)
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
