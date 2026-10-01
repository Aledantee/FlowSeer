package inventory

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyUsesTestAndWindowsClosures(t *testing.T) {
	root, modules := classifyFixture(t)
	entries := []Entry{
		{Ecosystem: "go", Name: "example.test/non_test", Version: "v0.0.0", Manifests: []string{"go.sum"}},
		{Ecosystem: "go", Name: "example.test/test_only", Version: "v0.0.0", Manifests: []string{"go.sum"}},
		{Ecosystem: "go", Name: "example.test/transitive", Version: "v0.0.0", Manifests: []string{"go.sum"}},
		{Ecosystem: "go", Name: "example.test/windows_only", Version: "v0.0.0", Manifests: []string{"go.sum"}},
	}

	classified, err := Classify(root, modules, entries)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"example.test/non_test":     "deploy",
		"example.test/test_only":    "run",
		"example.test/transitive":   "deploy",
		"example.test/windows_only": "deploy",
	}
	for _, entry := range classified {
		if got := entry.Criteria; got != want[entry.Name] {
			t.Errorf("%s criteria = %q, want %q", entry.Name, got, want[entry.Name])
		}
		if entry.Name == "example.test/non_test" && !containsString(entry.Via, "example.test/non_test") {
			t.Errorf("%s via = %#v, want its direct importer", entry.Name, entry.Via)
		}
		if entry.Name == "example.test/transitive" && !containsString(entry.Via, "example.test/non_test") {
			t.Errorf("%s via = %#v, want its direct importer", entry.Name, entry.Via)
		}
	}
}

func TestClassifyUsesEveryModuleGraph(t *testing.T) {
	root, modules := classifyFixture(t)

	entries := make([]Entry, 0, len(modules))
	for _, module := range modules {
		entries = append(entries, Entry{
			Ecosystem: "go",
			Name:      "example.test/transitive",
			Version:   "v0.0.0",
			Manifests: []string{strings.TrimSuffix(module.Manifest, "go.mod") + "go.sum"},
		})
	}

	classified, err := Classify(root, modules, entries)
	if err != nil {
		t.Fatal(err)
	}
	wantCriteria := map[string]string{
		"go.sum":              "deploy",
		"non_test/go.sum":     "run",
		"test_only/go.sum":    "run",
		"transitive/go.sum":   "run",
		"windows_only/go.sum": "run",
	}
	wantVia := map[string][]string{
		"go.sum":              {"example.test/non_test", "example.test/test_only", "example.test/windows_only"},
		"non_test/go.sum":     {"example.test/transitive"},
		"test_only/go.sum":    {"example.test/transitive"},
		"transitive/go.sum":   nil,
		"windows_only/go.sum": {"example.test/transitive"},
	}
	for _, entry := range classified {
		manifest := entry.Manifests[0]
		if got := entry.Criteria; got != wantCriteria[manifest] {
			t.Errorf("%s criteria = %q, want %q", manifest, got, wantCriteria[manifest])
		}
		if got := entry.Via; !equalStrings(got, wantVia[manifest]) {
			t.Errorf("%s via = %#v, want %#v", manifest, got, wantVia[manifest])
		}
	}
}

func TestClassifyUsesSelectedVersionsForVia(t *testing.T) {
	root, modules := mvsFixture(t)
	entries := []Entry{
		{
			Ecosystem: "go",
			Name:      "example.test/upgraded",
			Version:   "v1.1.0",
			Manifests: []string{"go.sum"},
		},
		{
			Ecosystem: "go",
			Name:      "example.test/leaf",
			Version:   "v1.0.0",
			Manifests: []string{"go.sum"},
		},
		{
			Ecosystem: "go",
			Name:      "example.test/upgraded",
			Version:   "v1.1.0",
			Manifests: []string{"generated/go/yang/go.sum"},
		},
	}

	classified, err := Classify(root, modules, entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range classified {
		if !containsString(entry.Via, "example.test/direct") {
			t.Errorf("%s via = %#v, want selected graph's direct importer", entry.Name, entry.Via)
		}
		if entry.Manifests[0] == "generated/go/yang/go.sum" && entry.Criteria != "deploy" {
			t.Errorf("%s criteria = %q, want deploy", entry.Manifests[0], entry.Criteria)
		}
	}
}

func TestSelectModuleGraphUsesSelectedSourcesAndTargets(t *testing.T) {
	graph := ModuleGraph{
		Direct: []string{"example.test/direct@v1.0.0"},
		Edges: map[string][]string{
			"example.test/root":            {"example.test/direct@v1.0.0"},
			"example.test/direct@v1.0.0":   {"example.test/upgraded@v1.0.0"},
			"example.test/upgraded@v1.0.0": {"example.test/old-only@v1.0.0"},
			"example.test/upgraded@v1.1.0": {"example.test/selected-only@v1.0.0"},
		},
	}
	selected := map[string]string{
		"example.test/root":          "",
		"example.test/direct":        "v1.0.0",
		"example.test/upgraded":      "v1.1.0",
		"example.test/old-only":      "v1.0.0",
		"example.test/selected-only": "v1.0.0",
	}

	got, err := selectModuleGraph(graph, selected, "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(got.Edges["example.test/direct@v1.0.0"], []string{"example.test/upgraded@v1.1.0"}) {
		t.Errorf("direct edges = %#v, want selected upgraded target", got.Edges["example.test/direct@v1.0.0"])
	}
	if _, ok := got.Edges["example.test/upgraded@v1.0.0"]; ok {
		t.Errorf("retained old source version: %#v", got.Edges)
	}
	selectedEdges := got.Edges["example.test/upgraded@v1.1.0"]
	if containsString(selectedEdges, "example.test/old-only@v1.0.0") {
		t.Errorf("selected source retained old-only edge: %#v", selectedEdges)
	}
	if !containsString(selectedEdges, "example.test/selected-only@v1.0.0") {
		t.Errorf("selected source edges = %#v, want selected-only dependency", selectedEdges)
	}
}

func classifyFixture(t *testing.T) (string, []Module) {
	t.Helper()
	root := copyFixtureTree(t, filepath.Join("testdata", "classify"))
	if err := os.Rename(filepath.Join(root, "go.mod.fixture"), filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"non_test", "test_only", "transitive", "windows_only"} {
		path := filepath.Join(root, name, "go.mod.fixture")
		if err := os.Rename(path, filepath.Join(filepath.Dir(path), "go.mod")); err != nil {
			t.Fatal(err)
		}
	}

	modules, err := DiscoverModules(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, modules
}

func mvsFixture(t *testing.T) (string, []Module) {
	t.Helper()
	root := copyFixtureTree(t, filepath.Join("testdata", "mvs"))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "go.mod.fixture" {
			return nil
		}
		return os.Rename(path, filepath.Join(filepath.Dir(path), "go.mod"))
	})
	if err != nil {
		t.Fatal(err)
	}

	modules, err := DiscoverModules(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, modules
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestDiscoversEveryModule(t *testing.T) {
	root := findRepositoryRoot(t)
	modules, err := DiscoverModules(root)
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]bool, len(modules))
	for _, module := range modules {
		got[module.Manifest] = true
	}
	files := gitFiles(t, root)
	want := make(map[string]bool, len(files))
	for _, file := range files {
		want[file] = true
	}
	if len(got) != len(want) {
		t.Fatalf("discovered %d modules, git lists %d: got %#v want %#v", len(got), len(want), got, want)
	}
	for file := range want {
		if !got[file] {
			t.Errorf("did not discover %s", file)
		}
	}
}

func copyFixtureTree(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
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

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("repository root not found")
		}
		root = parent
	}
}

func gitFiles(t *testing.T, root string) []string {
	t.Helper()
	output, err := exec.Command("git", "-C", root, "ls-files", "*go.mod").Output()
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}
