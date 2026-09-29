// Package main provides the check-guarantees CLI for verifying package GUARANTEES.md files.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(errs.ExitCode(err))
	}
}

// run executes the check-guarantees CLI logic.
func run(args []string, _ io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("check-guarantees", flag.ContinueOnError)
	fs.SetOutput(stderr)

	all := fs.Bool("all", false, "check all GUARANTEES.md files across the repository")
	rootFlag := fs.String("root", "", "root directory (default: current working directory)")

	if err := fs.Parse(args); err != nil {
		return errs.From(err).ExitCode(2).Msg("flag parse failed")
	}

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			wrapped := errs.From(err).ExitCode(1).Msg("cannot determine current working directory")
			fmt.Fprintln(stderr, wrapped)
			return wrapped
		}
		root = cwd
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		wrapped := errs.From(err).ExitCode(1).Msg("cannot resolve root path")
		fmt.Fprintln(stderr, wrapped)
		return wrapped
	}

	paths := fs.Args()
	var filtered []string
	for _, p := range paths {
		if p != "--" {
			filtered = append(filtered, p)
		}
	}

	violations, err := checkGuarantees(filtered, rootAbs, *all)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return errs.From(err).ExitCode(1).Msg("check guarantees failed")
	}

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(stderr, v)
		}
		return errs.New().ExitCode(1).Msgf("%d guarantee check violations found", len(violations))
	}

	return nil
}
