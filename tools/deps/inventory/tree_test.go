package inventory

import "testing"

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
