package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreeCountsSharedAndExclusiveVersions(t *testing.T) {
	got := treeStats(ModuleGraph{
		Direct: []string{"a", "b"},
		Edges: map[string][]string{
			"a": {"shared", "only-a"},
			"b": {"shared", "only-b"},
		},
	})
	if len(got) != 2 {
		t.Fatalf("got %d stats, want 2", len(got))
	}
	if got[0].Name != "a" || got[0].Versions != 3 || got[0].Only != 1 {
		t.Fatalf("a = %#v", got[0])
	}
	if got[1].Name != "b" || got[1].Versions != 3 || got[1].Only != 1 {
		t.Fatalf("b = %#v", got[1])
	}
}

func TestTreeStatsRetainsManifest(t *testing.T) {
	got := treeStatsForManifest(ModuleGraph{Direct: []string{"a@v1"}}, "src/edge/netpen/go.mod")
	if len(got) != 1 || got[0].Manifest != "src/edge/netpen/go.mod" {
		t.Fatalf("tree stats = %#v, want manifest", got)
	}
}

func TestTreeCountsSelectedModuleVersions(t *testing.T) {
	root, _ := mvsFixture(t)
	stats, err := Tree(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, stat := range stats {
		if stat.Manifest != "go.mod" || stat.Name != "example.test/direct" {
			continue
		}
		if stat.Versions != 3 || stat.Only != 2 {
			t.Fatalf("selected direct tree = %#v, want 3 versions and 2 only", stat)
		}
		return
	}
	t.Fatal("selected direct tree entry not found")
}

func TestTreeCountsNpmSnapshotClosureAndOptionalDependencies(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, "frontend", "web", "pnpm-lock.yaml")
	data, err := os.ReadFile(filepath.Join("testdata", "pnpm", "tree-lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, lockPath, string(data))

	stats, err := Tree(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, stat := range stats {
		if stat.Name != "app" {
			continue
		}
		if stat.Manifest != "frontend/web/pnpm-lock.yaml" || stat.Versions != 3 || stat.Only != 2 {
			t.Fatalf("app tree = %#v, want npm manifest and optional closure", stat)
		}
		return
	}
	t.Fatal("npm app tree entry not found")
}
