package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// Default values for the CLI flags. These are duplicated in the package
// doc comment (doc.go) and the plan; keep them in sync.
const (
	defaultConfigPath = "src/common/snmp/cmd/mibgen/mibgen.yaml"
	defaultOutDir     = "generated/go/mib"
	defaultPkgPrefix  = "go.aledante.io/FlowSeer/generated/go/mib"
)

// main is the OS entrypoint; it delegates to run so unit tests can
// exercise the same code path with in-memory stdout/stderr buffers and
// without calling os.Exit.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the unit-testable mibgen entrypoint.
//
// Exit-code contract:
//
//	0  success
//	1  config/load/runtime failure (with diagnostic on stderr) or
//	   no-op path that we want surfaced as "not yet wired"
//	2  flag parsing failure (handled by [flag.FlagSet] with
//	   ContinueOnError; matches the Go convention for usage errors)
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mibgen", flag.ContinueOnError)
	fs.SetOutput(stderr)

	configPath := fs.String("config", defaultConfigPath, "path to the YAML config file")
	outDir := fs.String("out", defaultOutDir, "output directory for generated packages")
	pkgPrefix := fs.String("pkg-prefix", defaultPkgPrefix, "Go import-path prefix for generated packages")
	verify := fs.Bool("verify", false, "load-only: parse config and load all modules, then exit 0 (no codegen)")
	check := fs.Bool("check", false, "regenerate into a tmpdir and diff against -out; exit 1 on drift")
	update := fs.Bool("update", false, "refresh golden-test fixtures under testdata/")

	// Custom usage so -h prints something useful even though the
	// defaults are also discoverable via -help. We do not override the
	// default usage entirely; flag.PrintDefaults gives the per-flag
	// lines for free.
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: mibgen [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Generates Go bindings for SMIv2 MIB modules listed in the config.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		// flag prints its own diagnostic; ContinueOnError returns
		// flag.ErrHelp for -h, which we treat as success.
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

	modules, err := LoadModules(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	switch {
	case *verify:
		fmt.Fprintf(stdout, "OK: loaded %d modules\n", len(modules))
		return 0
	case *check:
		if err := runCheck(cfg, *outDir, *pkgPrefix); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "OK: %d module(s) match committed output\n", len(modules))
		return 0
	case *update:
		if err := Emit(cfg, *outDir, *pkgPrefix); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "OK: %d module(s) regenerated under %s\n", len(modules), *outDir)
		return 0
	default:
		if err := Emit(cfg, *outDir, *pkgPrefix); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "OK: emitted %d module(s) to %s\n", len(modules), *outDir)
		return 0
	}
}
