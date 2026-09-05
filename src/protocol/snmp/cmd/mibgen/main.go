package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// Default values for the CLI flags. These are duplicated in the package
// doc comment (doc.go); keep them in sync.
const (
	defaultConfigPath   = "mibgen.yaml"
	defaultBaselineName = "mibgen-baseline.yaml"
	defaultOutDir       = "generated/go/mib"
	defaultPkgPrefix    = "go.aledante.io/FlowSeer/generated/go/mib"
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
	verify := fs.Bool("verify", false, "load-only: parse config and load all modules, then exit 0 (no codegen, no baseline gate)")
	check := fs.Bool("check", false, "regenerate into a tmpdir and diff against -out; exit 1 on drift")
	update := fs.Bool("update", false, "regenerate configured bindings under -out")
	baselinePath := fs.String("baseline", "", "path to the diagnostic baseline (default: "+defaultBaselineName+" beside the config)")
	refreshBaseline := fs.Bool("refresh-baseline", false, "rewrite the baseline from the diagnostics this load raised, then exit")

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
		printErr(stderr, err)
		return 1
	}

	set, err := LoadModules(cfg)
	if err != nil {
		printErr(stderr, err)
		return 1
	}

	blPath := *baselinePath
	if blPath == "" {
		blPath = filepath.Join(filepath.Dir(*configPath), defaultBaselineName)
	}

	if *refreshBaseline {
		if err := refreshBaselineFile(cfg, set, blPath); err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "OK: baseline written to %s\n", blPath)
		return 0
	}

	switch {
	case *verify:
		fmt.Fprintf(stdout, "OK: loaded %d modules\n", len(cfg.Modules))
		return 0
	case *check:
		if err := gateOnBaseline(cfg, set, blPath); err != nil {
			printErr(stderr, err)
			return 1
		}
		if err := runCheck(cfg, set, *outDir, *pkgPrefix); err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "OK: %d module(s) and the identity package match committed output\n", len(cfg.Modules))
		return 0
	case *update:
		if err := gateOnBaseline(cfg, set, blPath); err != nil {
			printErr(stderr, err)
			return 1
		}
		report, err := Emit(cfg, set, *outDir, *pkgPrefix)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		reportEmit(stdout, report)
		fmt.Fprintf(stdout, "OK: %d module(s) regenerated under %s\n", len(cfg.Modules), *outDir)
		return 0
	default:
		if err := gateOnBaseline(cfg, set, blPath); err != nil {
			printErr(stderr, err)
			return 1
		}
		report, err := Emit(cfg, set, *outDir, *pkgPrefix)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		reportEmit(stdout, report)
		fmt.Fprintf(stdout, "OK: emitted %d module(s) to %s\n", len(cfg.Modules), *outDir)
		return 0
	}
}

// reportEmit prints one line per reference emitted in its base type, so
// a configuration that leaves a key type's module out is visible in the
// run rather than only in the diff, then the identity table's size so a
// vendor module that contributed nothing is noticed.
func reportEmit(stdout io.Writer, report emitReport) {
	for _, d := range report.Degraded {
		fmt.Fprintln(stdout, d)
	}
	fmt.Fprintf(stdout, "OK: identity table with %d naming nodes from %d module(s)\n",
		report.Identity.Nodes, report.Identity.Modules)
}

// printErr writes err's diagnostic to w, followed by the remedy the
// error carries as an [errs.Hint] when it has one. The hint is what
// tells an operator what to do next, so it has to reach the terminal
// alongside the message rather than only the error value.
func printErr(w io.Writer, err error) {
	fmt.Fprintln(w, err)
	if hint := errs.Hint(err); hint != "" {
		fmt.Fprintln(w, "hint:", hint)
	}
}

// gateOnBaseline refuses to render anything the committed baseline does
// not already account for.
//
// A missing baseline file is a refusal rather than an empty baseline: a
// config whose file has been deleted or mistyped would otherwise render
// with the gate silently doing nothing, which is the failure mode the
// gate exists to rule out.
func gateOnBaseline(cfg *Config, set *smi.ModuleSet, path string) error {
	bl, err := LoadBaseline(path)
	if err != nil {
		return errs.From(err).
			Hint("run -refresh-baseline to create it").
			Msg("the diagnostic baseline is required")
	}

	return CheckBaseline(cfg, set, bl)
}

// refreshBaselineFile rewrites the baseline from the current load. It is
// the only path that writes one: the gate reports and never repairs.
func refreshBaselineFile(cfg *Config, set *smi.ModuleSet, path string) error {
	old, err := LoadBaseline(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	fresh, err := RefreshBaseline(cfg, set, old)
	if err != nil {
		return err
	}

	return WriteBaseline(path, fresh)
}
