// Package main provides the dependency inventory command.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/tools/deps/inventory"
	"go.aledante.io/FlowSeer/tools/deps/lookup"
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
	if command != "inventory" && command != "tree" && command != "advisories" && command != "age" {
		printUsage(stderr)
		return errs.New().ExitCode(2).Msgf("unknown dependency command %q", command)
	}
	flags := flag.NewFlagSet("deps "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write JSON")
	rootFlag := flags.String("root", "", "repository root (default: current working directory)")
	osvBatchURL := flags.String("osv-batch-url", "https://api.osv.dev/v1/querybatch", "OSV batch endpoint")
	osvVulnURL := flags.String("osv-vuln-url", "https://api.osv.dev/v1/vulns", "OSV advisory endpoint")
	goProxyURL := flags.String("go-proxy-url", "https://proxy.golang.org", "Go module proxy endpoint")
	npmRegistryURL := flags.String("npm-registry-url", "https://registry.npmjs.org", "npm registry endpoint")
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

	if command == "advisories" || command == "age" {
		result, err := inventory.Read(root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return errs.From(err).ExitCode(1).Msg("dependency inventory failed")
		}
		if command == "advisories" {
			advisories, err := lookup.Advisories(context.Background(), result.Entries, *osvBatchURL, *osvVulnURL)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return errs.From(err).ExitCode(1).Msg("dependency advisory lookup failed")
			}
			if *jsonOutput {
				return writeJSON(stdout, advisories)
			}
			for _, advisory := range advisories {
				if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", advisory.Ecosystem, advisory.Name, advisory.Version, advisory.ID, cleanLine(advisory.Summary), strings.Join(advisory.Imports, ",")); err != nil {
					return errs.From(err).ExitCode(1).Msg("write dependency advisories")
				}
			}
			return nil
		}

		ages, err := lookup.Age(context.Background(), result.Entries, *goProxyURL, *npmRegistryURL, time.Now().UTC())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return errs.From(err).ExitCode(1).Msg("dependency publication lookup failed")
		}
		if *jsonOutput {
			return writeJSON(stdout, ages)
		}
		for _, age := range ages {
			status := "mature"
			if age.Under14Days {
				status = "under-14-days"
			}
			origin := age.OriginCommit
			if age.OriginMissing {
				origin = "absent"
			}
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", age.Ecosystem, age.Name, age.Version, age.Published.Format(time.RFC3339Nano), status, age.Until.Format(time.RFC3339Nano), origin); err != nil {
				return errs.From(err).ExitCode(1).Msg("write dependency ages")
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
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%d versions\t%d only\n", stat.Manifest, stat.Name, stat.Versions, stat.Only); err != nil {
			return errs.From(err).ExitCode(1).Msg("write dependency tree")
		}
	}
	return nil
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage: deps <inventory|tree|advisories|age> [--json] [--root path]")
}

func cleanLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return errs.From(err).ExitCode(1).Msg("write JSON")
	}
	return nil
}
