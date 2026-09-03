// Command gen writes the diagnostic code variables from the catalog
// table: the declarations in internal/diag, where the lexer, framer and
// parser read them, and package smi's re-export of the same names.
//
// It is run by the go:generate directive in src/protocol/smi/generate.go
// and takes its output paths relative to that package directory:
//
//	go generate ./src/protocol/smi/...
//
// The output directories must exist. A write failure exits with status 1
// and may leave the first output updated; rerun after correcting the error.
//
// The generator exists so that every errs.NewCode call reaches the source
// with a string literal. The repo-wide uniqueness scan in package errs
// reads NewCode arguments out of the AST and fails on anything it cannot
// read, so a code assembled at runtime from the table would fail that gate.
package main

import (
	"fmt"
	"os"

	"go.aledante.io/FlowSeer/src/protocol/smi/internal/catalog"
)

const (
	codesPath   = "internal/diag/zz_generated_codes.go"
	aliasesPath = "zz_generated_codes.go"
)

func main() {
	rows := catalog.Entries()

	write(codesPath, rows, catalog.RenderCodes)
	write(aliasesPath, rows, catalog.RenderAliases)

	fmt.Printf("gen: wrote %s and %s (%d codes)\n", codesPath, aliasesPath, len(rows))
}

func write(path string, rows []catalog.Entry, render func([]catalog.Entry) ([]byte, error)) {
	src, err := render(rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(path, src, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen: writing %s: %v\n", path, err)
		os.Exit(1)
	}
}
