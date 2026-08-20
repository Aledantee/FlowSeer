package main

import (
	"path/filepath"
	"testing"
)

func TestLockfileRoundTrip(t *testing.T) {
	vs := fixtureVendor(t)
	lf := BuildLockfile([]*VendorSet{vs})
	if lf.GeneratorVersion != generatorVersion {
		t.Errorf("generator version = %q, want %q", lf.GeneratorVersion, generatorVersion)
	}
	if _, ok := lf.Modules["fixture/fixture-main"]; !ok {
		t.Fatalf("lockfile lacks fixture/fixture-main; keys: %d", len(lf.Modules))
	}

	dir := t.TempDir()
	if err := WriteLockfile(lf, dir); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	got, err := ReadLockfile(dir)
	if err != nil {
		t.Fatalf("ReadLockfile: %v", err)
	}
	if got == nil {
		t.Fatal("ReadLockfile returned nil for an existing lockfile")
	}
	flagged, mismatch := DiffLockfiles(got, lf)
	if mismatch || len(flagged) != 0 {
		t.Errorf("round-tripped lockfile diffs: flagged=%v mismatch=%v", flagged, mismatch)
	}
}

func TestReadLockfileMissingIsNil(t *testing.T) {
	lf, err := ReadLockfile(filepath.Join(t.TempDir(), "empty"))
	if err != nil || lf != nil {
		t.Fatalf("ReadLockfile(missing) = %v, %v; want nil, nil", lf, err)
	}
}

func TestDiffLockfilesFlagsExactChanges(t *testing.T) {
	committed := &Lockfile{
		GeneratorVersion: generatorVersion,
		Modules: map[string]LockedModule{
			"v/a": {Revision: "2026-01-01", SourceSHA: "s1", ClosureSHA: "c1"},
			"v/b": {Revision: "2026-01-01", SourceSHA: "s2", ClosureSHA: "c2"},
			"v/c": {Revision: "2026-01-01", SourceSHA: "s3", ClosureSHA: "c3"},
		},
	}
	fresh := &Lockfile{
		GeneratorVersion: generatorVersion,
		Modules: map[string]LockedModule{
			"v/a": {Revision: "2026-01-01", SourceSHA: "s1", ClosureSHA: "c1"},
			"v/b": {Revision: "2026-01-01", SourceSHA: "s2", ClosureSHA: "c2-changed"},
			"v/d": {Revision: "2026-01-01", SourceSHA: "s4", ClosureSHA: "c4"},
		},
	}

	flagged, mismatch := DiffLockfiles(committed, fresh)
	if mismatch {
		t.Fatal("version mismatch reported for identical generator versions")
	}
	want := []string{"v/b", "v/c (removed)", "v/d"}
	if len(flagged) != len(want) {
		t.Fatalf("flagged = %v, want %v", flagged, want)
	}
	for i := range want {
		if flagged[i] != want[i] {
			t.Fatalf("flagged = %v, want %v", flagged, want)
		}
	}
}

func TestDiffLockfilesGeneratorVersionFlagsEverything(t *testing.T) {
	committed := &Lockfile{
		GeneratorVersion: "yanggen-0-ancient",
		Modules:          map[string]LockedModule{"v/a": {ClosureSHA: "c1"}},
	}
	fresh := &Lockfile{
		GeneratorVersion: generatorVersion,
		Modules: map[string]LockedModule{
			"v/a": {ClosureSHA: "c1"},
			"v/b": {ClosureSHA: "c2"},
		},
	}
	flagged, mismatch := DiffLockfiles(committed, fresh)
	if !mismatch {
		t.Error("generator version drift not reported")
	}
	if len(flagged) != 2 {
		t.Errorf("flagged = %v, want every module", flagged)
	}

	// A missing committed lockfile flags everything without claiming
	// version drift.
	flagged, mismatch = DiffLockfiles(nil, fresh)
	if mismatch {
		t.Error("nil committed lockfile reported as version drift")
	}
	if len(flagged) != 2 {
		t.Errorf("flagged = %v, want every module", flagged)
	}
}
