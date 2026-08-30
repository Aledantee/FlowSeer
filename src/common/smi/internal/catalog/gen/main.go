// Command gen writes package smi's diagnostic code variables from the
// catalog table.
//
// It is run by the go:generate directive in src/common/smi/generate.go
// and takes its output path relative to that package directory:
//
//	go generate ./src/common/smi/...
//
// The generator exists so that every errs.NewCode call reaches the source
// with a string literal. The repo-wide uniqueness scan in package errs
// reads NewCode arguments out of the AST and fails on anything it cannot
// read, so a code assembled at runtime from the table would silently
// leave that gate.
package main

import (
	"fmt"
	"os"

	"go.aledante.io/FlowSeer/src/common/smi/internal/catalog"
)

const outPath = "zz_generated_codes.go"

func main() {
	rows := catalog.Entries()

	src, err := catalog.RenderCodes(rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outPath, src, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen: writing %s: %v\n", outPath, err)
		os.Exit(1)
	}

	fmt.Printf("gen: wrote %s (%d codes)\n", outPath, len(rows))
}
