package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Emit renders every loaded module's binding package under
// outDir/<vendor>/<package>/. Each vendor's output directory is
// rebuilt from scratch so removed modules leave no stale packages
// behind; the lockfile is written separately by the caller.
func Emit(sets []*VendorSet, outDir, pkgPrefix string) error {
	_ = pkgPrefix // package identity comes from the directory layout
	for _, vs := range sets {
		vendorDir := filepath.Join(outDir, vs.Vendor)
		if err := os.RemoveAll(vendorDir); err != nil {
			return errs.Wrapf(err, "clear vendor output %s", vendorDir)
		}
		for _, m := range vs.Modules {
			files, err := emitModuleFiles(m)
			if err != nil {
				return err
			}
			pkgDir := filepath.Join(vendorDir, m.Package)
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				return errs.Wrapf(err, "create package dir %s", pkgDir)
			}
			for name, src := range files {
				if err := os.WriteFile(filepath.Join(pkgDir, name), src, 0o644); err != nil {
					return errs.Wrapf(err, "write %s", filepath.Join(pkgDir, name))
				}
			}
		}
	}
	return nil
}

// emitOne renders a single module and returns its files concatenated
// in filename order — the golden-test seam (fixture modules fit one
// chunk).
func emitOne(m *LoadedModule, _ string) (string, error) {
	files, err := emitModuleFiles(m)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.Write(files[name])
	}
	return b.String(), nil
}
