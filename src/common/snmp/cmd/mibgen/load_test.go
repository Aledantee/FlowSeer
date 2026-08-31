package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// indexOf is a small slice-position helper used in topo-order assertions.
// Returns -1 if name is absent.
func indexOf(slice []string, name string) int {
	for i, s := range slice {
		if s == name {
			return i
		}
	}
	return -1
}

// TestTopoSort_LinearChain pins the canonical case: A → B → C produces
// [A, B, C].
func TestTopoSort_LinearChain(t *testing.T) {
	mods := []Module{
		{Name: "C", Package: "c", DependsOn: []string{"B"}},
		{Name: "B", Package: "b", DependsOn: []string{"A"}},
		{Name: "A", Package: "a"},
	}
	got, err := topoSort(mods)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if want := []string{"A", "B", "C"}; !equalStrings(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

// TestTopoSort_NoEdges preserves input order when there are no deps.
func TestTopoSort_NoEdges(t *testing.T) {
	mods := []Module{
		{Name: "ALPHA", Package: "alpha"},
		{Name: "BETA", Package: "beta"},
		{Name: "GAMMA", Package: "gamma"},
	}
	got, err := topoSort(mods)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if want := []string{"ALPHA", "BETA", "GAMMA"}; !equalStrings(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

// TestTopoSort_Diamond verifies that every depends_on edge u→v has u
// before v in the output, without pinning a specific permutation.
func TestTopoSort_Diamond(t *testing.T) {
	mods := []Module{
		{Name: "D", Package: "d", DependsOn: []string{"B", "C"}},
		{Name: "B", Package: "b", DependsOn: []string{"A"}},
		{Name: "C", Package: "c", DependsOn: []string{"A"}},
		{Name: "A", Package: "a"},
	}
	got, err := topoSort(mods)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %v; want 4 modules", got)
	}
	// Every edge must be respected.
	for _, m := range mods {
		for _, dep := range m.DependsOn {
			if indexOf(got, dep) >= indexOf(got, m.Name) {
				t.Errorf("edge %s -> %s violated in result %v", dep, m.Name, got)
			}
		}
	}
}

// TestTopoSort_Determinism runs the diamond twice and verifies the
// output is identical, guarding against accidental dependence on Go
// map iteration order.
func TestTopoSort_Determinism(t *testing.T) {
	mods := []Module{
		{Name: "D", Package: "d", DependsOn: []string{"B", "C"}},
		{Name: "B", Package: "b", DependsOn: []string{"A"}},
		{Name: "C", Package: "c", DependsOn: []string{"A"}},
		{Name: "A", Package: "a"},
	}
	for i := 0; i < 20; i++ {
		got1, err1 := topoSort(mods)
		got2, err2 := topoSort(mods)
		if err1 != nil || err2 != nil {
			t.Fatalf("topoSort errors: %v / %v", err1, err2)
		}
		if !equalStrings(got1, got2) {
			t.Fatalf("iteration %d non-deterministic: %v vs %v", i, got1, got2)
		}
	}
}

// TestTopoSort_Cycle: A → B → A returns a *CycleError naming both
// participants.
func TestTopoSort_Cycle(t *testing.T) {
	mods := []Module{
		{Name: "A", Package: "a", DependsOn: []string{"B"}},
		{Name: "B", Package: "b", DependsOn: []string{"A"}},
	}
	_, err := topoSort(mods)
	if err == nil {
		t.Fatal("topoSort: expected cycle error, got nil")
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err is %T (%v); want *CycleError", err, err)
	}
	if indexOf(ce.Modules, "A") < 0 || indexOf(ce.Modules, "B") < 0 {
		t.Errorf("CycleError.Modules = %v; should mention both A and B", ce.Modules)
	}
	if !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), "B") {
		t.Errorf("err message %q should reference both cycle modules", err.Error())
	}
}

// TestTopoSort_SelfLoop: A → A is treated as a cycle. (validateConfig
// allows it through because depends_on referencing a known module name
// is structurally legal; the cycle is detected by topoSort.)
func TestTopoSort_SelfLoop(t *testing.T) {
	mods := []Module{
		{Name: "A", Package: "a", DependsOn: []string{"A"}},
	}
	_, err := topoSort(mods)
	if err == nil {
		t.Fatal("topoSort: expected cycle error for self-loop")
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err is %T; want *CycleError", err)
	}
	if indexOf(ce.Modules, "A") < 0 {
		t.Errorf("CycleError.Modules = %v; should contain A", ce.Modules)
	}
}

// TestLoadModules_Real is an integration test against spec/mib/ietf/.
// Skipped if the directory is missing (e.g. detached worktree). Loads
// SNMPv2-SMI alone — every IMPORTS clause is followed from the search
// path, so the set comes back holding more than the module named.
func TestLoadModules_Real(t *testing.T) {
	mibDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(mibDir); statErr != nil {
		t.Skipf("spec/mib/ietf not available: %v", statErr)
	}

	cfg := &Config{
		SearchPaths: []string{mibDir},
		Modules: []Module{
			{Name: "SNMPv2-SMI", Package: "snmpv2smi"},
		},
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("LoadModules: %v", err)
	}
	mod, ok := set.Module("SNMPv2-SMI")
	if !ok {
		t.Fatal("SNMPv2-SMI missing from the resolved set")
	}
	if mod.File == "" {
		t.Error("SNMPv2-SMI resolved to no file")
	}
}

// TestLoadModules_NilConfig is a contract guard.
func TestLoadModules_NilConfig(t *testing.T) {
	_, err := LoadModules(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

// equalStrings is a tiny helper to avoid pulling in reflect.DeepEqual
// just for slice comparison.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
