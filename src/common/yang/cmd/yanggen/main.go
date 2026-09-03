package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// Default values for the CLI flags, mirroring mibgen's layout under
// the yang tree.
const (
	defaultConfigPath = "src/common/yang/cmd/yanggen/yanggen.yaml"
	defaultOutDir     = "generated/go/yang"
	defaultPkgPrefix  = "go.aledante.io/FlowSeer/generated/go/yang"
)

// main is the OS entrypoint; it delegates to run so unit tests can
// exercise the same code path with in-memory stdout/stderr buffers
// and without calling os.Exit.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the unit-testable yanggen entrypoint.
//
// Exit-code contract (mibgen's):
//
//	0  success
//	1  config/load/check/runtime failure (diagnostic on stderr)
//	2  flag parsing failure
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("yanggen", flag.ContinueOnError)
	fs.SetOutput(stderr)

	configPath := fs.String("config", defaultConfigPath, "path to the YAML config file")
	outDir := fs.String("out", defaultOutDir, "output directory for generated packages")
	pkgPrefix := fs.String("pkg-prefix", defaultPkgPrefix, "Go import-path prefix for generated packages")
	verify := fs.Bool("verify", false, "load-only: parse config and resolve all vendor trees, then exit 0 (no codegen)")
	check := fs.Bool("check", false, "compare the committed lockfile against freshly-hashed sources; exit 1 on drift")
	fs.Bool("update", false, "regenerate all configured modules and refresh the lockfile (the default)")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: yanggen [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Generates Go bindings for the vendored YANG trees listed in the config.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	sets, err := LoadVendors(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	total, skipped := 0, 0
	for _, vs := range sets {
		total += len(vs.Modules)
		skipped += len(vs.Skipped)
	}

	switch {
	case *verify:
		for _, vs := range sets {
			fmt.Fprintf(stdout, "vendor %s: %d module(s), %d skipped\n", vs.Vendor, len(vs.Modules), len(vs.Skipped))
			for _, s := range vs.Skipped {
				fmt.Fprintf(stdout, "  skip %s: %s\n", s.Module, s.Reason)
			}
		}
		fmt.Fprintf(stdout, "OK: resolved %d modules (%d skipped)\n", total, skipped)
		return 0
	case *check:
		committed, err := ReadLockfile(*outDir)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if committed == nil {
			fmt.Fprintf(stderr, "no lockfile under %s — run yanggen to generate\n", *outDir)
			return 1
		}
		flagged, versionMismatch := DiffLockfiles(committed, BuildLockfile(sets))
		if versionMismatch {
			fmt.Fprintf(stderr, "generator version drift: lockfile %q, binary %q — full regeneration required\n",
				committed.GeneratorVersion, generatorVersion)
			return 1
		}
		if len(flagged) > 0 {
			fmt.Fprintf(stderr, "%d module(s) drifted from committed output:\n", len(flagged))
			for _, key := range flagged {
				fmt.Fprintf(stderr, "  %s\n", key)
			}
			return 1
		}
		fmt.Fprintf(stdout, "OK: %d module(s) match the committed lockfile\n", total)
		return 0
	default:
		if err := Emit(sets, *outDir, *pkgPrefix); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := WriteLockfile(BuildLockfile(sets), *outDir); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "OK: emitted %d module(s) to %s\n", total, *outDir)
		return 0
	}
}
