package main

import "go.aledante.io/FlowSeer/src/common/errs"

// Emit renders every loaded module's binding package under outDir.
// The emitters land with U4; until then generation is a hard error so
// nobody mistakes a lockfile-only run for generated output.
func Emit(sets []*VendorSet, outDir, pkgPrefix string) error {
	_ = sets
	_ = outDir
	_ = pkgPrefix
	return errs.Msg("yanggen emitters are not implemented yet (U4); use -verify or -check")
}
