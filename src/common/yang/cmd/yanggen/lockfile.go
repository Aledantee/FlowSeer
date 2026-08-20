package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// generatorVersion identifies the emitter generation. Bump it whenever
// emitted output changes shape for unchanged input — a version
// mismatch flags every module for full regeneration (KTD7).
const generatorVersion = "yanggen-1"

// lockfileName is the lockfile's basename under the output directory.
const lockfileName = "yanggen.lock.json"

// Lockfile is the committed record tying generated output to the
// exact sources it was built from: one generator version for the run
// plus, per module, its newest revision, its own source hash, and the
// KTD7 closure hash covering every source in its dependency closure
// (imports, includes, and reverse augment/deviation contributors).
// The recorded revisions also feed R8's runtime revision-drift
// detection.
type Lockfile struct {
	GeneratorVersion string                  `json:"generator_version"`
	Modules          map[string]LockedModule `json:"modules"`
}

// LockedModule is one module's lockfile row, keyed by
// "<vendor>/<module>" in the Modules map.
type LockedModule struct {
	Revision   string `json:"revision,omitempty"`
	SourceSHA  string `json:"source_sha256"`
	ClosureSHA string `json:"closure_sha256"`
}

// BuildLockfile derives the lockfile for a loaded vendor set.
func BuildLockfile(sets []*VendorSet) *Lockfile {
	lf := &Lockfile{
		GeneratorVersion: generatorVersion,
		Modules:          make(map[string]LockedModule),
	}
	for _, vs := range sets {
		for _, m := range vs.Modules {
			lf.Modules[vs.Vendor+"/"+m.Name] = LockedModule{
				Revision:   m.Revision,
				SourceSHA:  m.SourceSHA,
				ClosureSHA: m.ClosureSHA,
			}
		}
	}
	return lf
}

// WriteLockfile writes the lockfile under outDir with a stable key
// order, creating outDir if needed.
func WriteLockfile(lf *Lockfile, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return errs.Wrapf(err, "create output directory %s", outDir)
	}
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return errs.Wrap(err, "encode lockfile")
	}
	data = append(data, '\n')
	path := filepath.Join(outDir, lockfileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return errs.Wrapf(err, "write lockfile %s", path)
	}
	return nil
}

// ReadLockfile reads the committed lockfile under outDir. A missing
// lockfile returns (nil, nil): the caller decides whether absence
// means "first generation" or "drift".
func ReadLockfile(outDir string) (*Lockfile, error) {
	path := filepath.Join(outDir, lockfileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, errs.Wrapf(err, "read lockfile %s", path)
	}
	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, errs.Wrapf(err, "parse lockfile %s", path)
	}
	return &lf, nil
}

// DiffLockfiles compares the committed lockfile against the freshly
// computed one and returns the keys needing regeneration, sorted:
// changed closures, new modules, and removed modules all flag. A
// generator-version mismatch flags every module (KTD7's full-regen
// rule). committed == nil flags everything.
func DiffLockfiles(committed, fresh *Lockfile) (flagged []string, versionMismatch bool) {
	if committed == nil || committed.GeneratorVersion != fresh.GeneratorVersion {
		flagged = make([]string, 0, len(fresh.Modules))
		for key := range fresh.Modules {
			flagged = append(flagged, key)
		}
		sort.Strings(flagged)
		return flagged, committed != nil
	}
	seen := make(map[string]struct{}, len(fresh.Modules))
	for key, mod := range fresh.Modules {
		seen[key] = struct{}{}
		prev, ok := committed.Modules[key]
		if !ok || prev.ClosureSHA != mod.ClosureSHA || prev.Revision != mod.Revision {
			flagged = append(flagged, key)
		}
	}
	for key := range committed.Modules {
		if _, ok := seen[key]; !ok {
			flagged = append(flagged, fmt.Sprintf("%s (removed)", key))
		}
	}
	sort.Strings(flagged)
	return flagged, false
}
