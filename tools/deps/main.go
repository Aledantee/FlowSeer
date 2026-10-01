// Package main provides the dependency inventory command.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/tools/deps/inventory"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(errs.ExitCode(err))
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errs.New().ExitCode(2).Msg("missing dependency command")
	}

	command := args[0]
	if command != "inventory" && command != "tree" {
		printUsage(stderr)
		return errs.New().ExitCode(2).Msgf("unknown dependency command %q", command)
	}
	flags := flag.NewFlagSet("deps "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write JSON")
	rootFlag := flags.String("root", "", "repository root (default: current working directory)")
	flags.Usage = func() { printUsage(stderr) }
	if err := flags.Parse(args[1:]); err != nil {
		return errs.From(err).ExitCode(2).Msg("dependency command flag parse failed")
	}

	root := *rootFlag
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return errs.From(err).ExitCode(1).Msg("cannot determine repository root")
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return errs.From(err).ExitCode(1).Msg("cannot resolve repository root")
	}

	if command == "inventory" {
		result, err := inventory.Collect(root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return errs.From(err).ExitCode(1).Msg("dependency inventory failed")
		}
		if *jsonOutput {
			return writeJSON(stdout, result.Entries)
		}
		for _, entry := range result.Entries {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", entry.Ecosystem, entry.Name, entry.Version, entry.Hash, entry.Criteria, strings.Join(entry.Manifests, ","), strings.Join(entry.Via, ",")); err != nil {
				return errs.From(err).ExitCode(1).Msg("write dependency inventory")
			}
		}
		return nil
	}

	stats, err := inventory.Tree(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return errs.From(err).ExitCode(1).Msg("dependency tree failed")
	}
	if *jsonOutput {
		return writeJSON(stdout, stats)
	}
	for _, stat := range stats {
		if _, err := fmt.Fprintf(stdout, "%s\t%d versions\t%d only\n", stat.Name, stat.Versions, stat.Only); err != nil {
			return errs.From(err).ExitCode(1).Msg("write dependency tree")
		}
	}
	return nil
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage: deps <inventory|tree> [--json] [--root path]")
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return errs.From(err).ExitCode(1).Msg("write JSON")
	}
	return nil
}
