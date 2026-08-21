package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureVendor loads the testdata fixture tree, optionally with skip
// entries.
func fixtureVendor(t *testing.T, skip ...Skip) *VendorSet {
	t.Helper()
	abs, err := filepath.Abs("testdata/modules")
	if err != nil {
		t.Fatal(err)
	}
	vs, err := LoadVendor(&Vendor{Name: "fixture", Paths: []string{abs}, Skip: skip})
	if err != nil {
		t.Fatalf("LoadVendor: %v", err)
	}
	return vs
}

func moduleByName(t *testing.T, vs *VendorSet, name string) *LoadedModule {
	t.Helper()
	for _, m := range vs.Modules {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("module %q not loaded; have %d modules", name, len(vs.Modules))
	return nil
}

func TestLoadVendorFixtureTree(t *testing.T) {
	vs := fixtureVendor(t)

	wantModules := []string{"fixture-aug", "fixture-dev", "fixture-main", "fixture-types"}
	var got []string
	for _, m := range vs.Modules {
		got = append(got, m.Name)
	}
	if len(got) != len(wantModules) {
		t.Fatalf("loaded modules %v, want %v", got, wantModules)
	}
	for i, name := range wantModules {
		if got[i] != name {
			t.Fatalf("loaded modules %v, want %v", got, wantModules)
		}
	}

	main := moduleByName(t, vs, "fixture-main")
	if main.Revision != "2026-01-02" {
		t.Errorf("fixture-main revision = %q, want 2026-01-02", main.Revision)
	}

	servers := main.Entry.Dir["servers"]
	if servers == nil {
		t.Fatal("fixture-main entry has no servers container")
	}
	server := servers.Dir["server"]
	if server == nil {
		t.Fatal("servers has no server list")
	}
	// Augment applied: fixture-aug's owner leaf landed in the list.
	if server.Dir["owner"] == nil {
		t.Error("augment not applied: server has no owner leaf")
	}
	// Deviation applied: legacy-flag removed by deviate not-supported.
	if server.Dir["legacy-flag"] != nil {
		t.Error("deviation not applied: legacy-flag still present")
	}
	// Submodule flattened: from-submodule container present.
	if main.Entry.Dir["from-submodule"] == nil {
		t.Error("submodule content missing: no from-submodule container")
	}

	// The flattener makes submodule typedefs visible to importers —
	// fixture-aug's sub-typed leaf resolves against fixture-main's
	// submodule typedef.
	aug := moduleByName(t, vs, "fixture-aug")
	subTyped := aug.Entry.Dir["sub-typed"]
	if subTyped == nil {
		t.Fatal("fixture-aug entry has no sub-typed leaf")
	}
	if subTyped.Type == nil || subTyped.Type.Kind.String() != "string" {
		t.Errorf("sub-typed resolved to %v, want the submodule's string typedef", subTyped.Type)
	}
}

func TestLoadVendorSkipDisablesDeviation(t *testing.T) {
	vs := fixtureVendor(t, Skip{Module: "fixture-dev", Reason: "test: keep the deviated leaf"})
	main := moduleByName(t, vs, "fixture-main")
	server := main.Entry.Dir["servers"].Dir["server"]
	if server.Dir["legacy-flag"] == nil {
		t.Error("legacy-flag missing although the deviating module was skipped")
	}
}

func TestLoadVendorStaleSkipFails(t *testing.T) {
	abs, _ := filepath.Abs("testdata/modules")
	_, err := LoadVendor(&Vendor{
		Name: "fixture", Paths: []string{abs},
		Skip: []Skip{{Module: "no-such-module", Reason: "stale"}},
	})
	if err == nil {
		t.Fatal("stale skip entry accepted")
	}
	_, err = LoadVendor(&Vendor{
		Name: "fixture", Paths: []string{abs},
		Skip: []Skip{{Pattern: "zzz-*", Reason: "stale"}},
	})
	if err == nil {
		t.Fatal("stale skip pattern accepted")
	}
}

// TestClosureHashesFollowTheDependencyGraph covers KTD7: a source
// change anywhere in a module's closure — including reverse
// augment/deviation contributors — changes its closure hash, and
// modules outside the closure keep theirs.
func TestClosureHashesFollowTheDependencyGraph(t *testing.T) {
	before := fixtureVendor(t)

	// Copy the fixture tree and append a byte to fixture-dev.
	dir := t.TempDir()
	entries, err := os.ReadDir("testdata/modules")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("testdata/modules", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == "fixture-dev.yang" {
			data = append(data, []byte("// touched\n")...)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	after, err := LoadVendor(&Vendor{Name: "fixture", Paths: []string{dir}})
	if err != nil {
		t.Fatalf("LoadVendor on modified copy: %v", err)
	}

	changed := map[string]bool{
		// fixture-dev deviates fixture-main, so its source is in the
		// closure of fixture-main and of everything that imports
		// fixture-main (fixture-aug), plus itself.
		"fixture-dev":  true,
		"fixture-main": true,
		"fixture-aug":  true,
		// fixture-types imports nothing and nothing deviates it.
		"fixture-types": false,
	}
	for name, wantChanged := range changed {
		b := moduleByName(t, before, name).ClosureSHA
		a := moduleByName(t, after, name).ClosureSHA
		if wantChanged && a == b {
			t.Errorf("%s closure hash unchanged despite a closure-member edit", name)
		}
		if !wantChanged && a != b {
			t.Errorf("%s closure hash changed although its closure is untouched", name)
		}
	}

	// Source hashes move only for the edited file.
	for _, name := range []string{"fixture-main", "fixture-types", "fixture-aug"} {
		if moduleByName(t, before, name).SourceSHA != moduleByName(t, after, name).SourceSHA {
			t.Errorf("%s source hash changed without an edit", name)
		}
	}
	if moduleByName(t, before, "fixture-dev").SourceSHA == moduleByName(t, after, "fixture-dev").SourceSHA {
		t.Error("fixture-dev source hash unchanged despite the edit")
	}
}

func TestDerivePackageName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"openconfig-interfaces", "openconfiginterfaces"},
		{"Cisco-IOS-XE-native", "ciscoiosxenative"},
		{"ietf-inet-types", "ietfinettypes"},
		{"802-dot1q", "z802dot1q"},
	}
	for _, tc := range tests {
		if got := derivePackageName(tc.in); got != tc.want {
			t.Errorf("derivePackageName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
