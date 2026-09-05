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

// writeMIB writes a minimal module declaring name under dir at file and
// returns the path.
func writeMIB(t *testing.T, dir, file, name string) string {
	t.Helper()

	path := filepath.Join(dir, file)
	src := name + " DEFINITIONS ::= BEGIN\n" +
		"IMPORTS enterprises FROM SNMPv2-SMI;\n" +
		strings.ToLower(strings.ReplaceAll(name, "-", "")) + " OBJECT IDENTIFIER ::= { enterprises 4711 }\n" +
		"END\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return path
}

// TestLoadModules_FilePin: a pinned module is read from its file even
// though a sibling file spelled after the module name sits on the search
// path, and an unpinned module in the same config still resolves by
// name.
func TestLoadModules_FilePin(t *testing.T) {
	dir := t.TempDir()
	// The decoy is what name-based lookup would pick.
	writeMIB(t, dir, "PINNED-MIB", "PINNED-MIB")
	pinnedFile := writeMIB(t, dir, "PINNED-10-94.mib", "PINNED-MIB")
	freeFile := writeMIB(t, dir, "FREE-MIB", "FREE-MIB")

	cfg := &Config{
		SearchPaths: []string{dir},
		Modules: []Module{
			{Name: "PINNED-MIB", Package: "pinned", File: pinnedFile},
			{Name: "FREE-MIB", Package: "free"},
		},
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("LoadModules: %v", err)
	}
	if mod, ok := set.Module("PINNED-MIB"); !ok || mod.File != pinnedFile {
		t.Errorf("PINNED-MIB came from %q; want the pinned %q", mod.File, pinnedFile)
	}
	if mod, ok := set.Module("FREE-MIB"); !ok || mod.File != freeFile {
		t.Errorf("FREE-MIB came from %q; want %q by name", mod.File, freeFile)
	}
}

// TestLoadModules_FilePinWrongModule: a pin to a file that declares some
// other module fails, naming the pin and where the name actually came
// from, rather than quietly loading the search-path copy.
func TestLoadModules_FilePinWrongModule(t *testing.T) {
	dir := t.TempDir()
	byName := writeMIB(t, dir, "PINNED-MIB", "PINNED-MIB")
	wrong := writeMIB(t, dir, "SOMETHING-ELSE.mib", "SOMETHING-ELSE-MIB")

	cfg := &Config{
		SearchPaths: []string{dir},
		Modules: []Module{
			// The importer is what drags PINNED-MIB in from the search
			// path, which is the case the post-load check exists for.
			{Name: "IMPORTER-MIB", Package: "importer"},
			{Name: "PINNED-MIB", Package: "pinned", File: wrong},
		},
	}
	importer := "IMPORTER-MIB DEFINITIONS ::= BEGIN\n" +
		"IMPORTS pinnedmib FROM PINNED-MIB;\n" +
		"importermib OBJECT IDENTIFIER ::= { pinnedmib 1 }\n" +
		"END\n"
	if err := os.WriteFile(filepath.Join(dir, "IMPORTER-MIB"), []byte(importer), 0o644); err != nil {
		t.Fatalf("write importer: %v", err)
	}

	_, err := LoadModules(cfg)
	if err == nil {
		t.Fatal("LoadModules succeeded; want a wrong-file error")
	}
	for _, want := range []string{"PINNED-MIB", "is pinned to " + wrong, "it came from " + byName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestLoadModules_FilePinMissingFile: a pin to a file that is not there
// fails the load like a missing named module does.
func TestLoadModules_FilePinMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		SearchPaths: []string{dir},
		Modules:     []Module{{Name: "GONE-MIB", Package: "gone", File: filepath.Join(dir, "GONE.mib")}},
	}
	if _, err := LoadModules(cfg); err == nil {
		t.Fatal("LoadModules succeeded; want an error for the missing pinned file")
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
