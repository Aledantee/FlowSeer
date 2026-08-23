// Command netpen is the L2/L3 security audit and attack binary. It replaces
// the Python l2l3-audit tool with a self-contained static Go binary.
//
// This file holds the testable run core and the subcommand dispatch table.
// The 27 parity commands and 8 superset attacks are registered as stubs here
// and filled in by later units; the skeleton proves the dispatch, flag, and
// exit-code seams.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/netpen/link"
)

// Default attack-leg interface, matching the baseline l2l3-audit.
const defaultIface = "eth0"

// subcommand is one entry in the dispatch table. Each command gets its own
// flag.FlagSet (KTD2); the run function parses the remaining args and
// executes. A stub returns nil for a clean exit-0; later units replace it.
type subcommand struct {
	name  string
	short string
	setup func(fs *flag.FlagSet) // register flags
	run   func(ctx context.Context, attackIface, watchIface string, stdout, stderr io.Writer) error
}

// commands is the dispatch table. U1 registers a proof stub (version) and the
// full set of names with nil bodies; later units replace the nils with real
// behavior registrations. Every name listed here corresponds to a baseline
// l2l3-audit subcommand (R1) or a superset attack (R4).
var commands = []subcommand{
	{name: "version", short: "print the netpen version and exit", setup: nil, run: runVersion},
}

// stubNames are the 27 parity commands and 8 superset attacks, registered as
// stubs so dispatch and help text are exercised. Later units replace each
// with a real subcommand entry. The names are verified against R1 and R4.
var stubNames = []string{
	// 27 parity commands (R1)
	"full", "scan", "arpsweep", "arpspoof", "stproot", "camflood", "dhcpstarve",
	"roguedhcp", "roguedhcp6", "roguera", "ndpspoof", "daddos", "raguard",
	"dtp", "doubletag", "vlanenum", "vlanhop", "voicevlan", "portsteal",
	"ghost", "vtp", "mvrp", "hsrp", "icmpredirect", "gratarp", "llmnr", "vrrp",
	// 8 superset attacks (R4)
	"ospf", "eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp",
}

func init() {
	for _, name := range stubNames {
		name := name
		commands = append(commands, subcommand{
			name:  name,
			short: "stub: " + name + " (not yet implemented)",
			run: func(_ context.Context, _, _ string, _ io.Writer, stderr io.Writer) error {
				fmt.Fprintf(stderr, "netpen %s: not yet implemented\n", name)
				return nil
			},
		})
	}
}

// main is the OS entrypoint; it delegates to run so unit tests exercise the
// same code path with in-memory stdout/stderr and without calling os.Exit.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the unit-testable netpen entrypoint.
//
// Exit-code contract (KTD13):
//
//	0  run completion regardless of findings
//	1  runtime failure (carries errs ExitCode(1))
//	2  usage error (returned directly from run on flag.FlagSet parse failure)
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr, "")
		return 2
	}

	name := args[0]
	cmd := findCommand(name)
	if cmd == nil {
		printUsage(stderr, name)
		return 2
	}

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)

	attackIface := fs.String("i", defaultIface, "attack interface")
	watchIface := fs.String("w", "", "watch interface (optional; required by ghost)")

	if cmd.setup != nil {
		cmd.setup(fs)
	}

	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: netpen %s [flags]\n\nFlags:\n", name)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	if err := cmd.run(ctx, *attackIface, *watchIface, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return errs.ExitCode(err)
	}

	return 0
}

// findCommand returns the subcommand with the given name, or nil.
func findCommand(name string) *subcommand {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}

// printUsage writes the top-level usage to stderr. When unknown is non-empty,
// it names the unrecognized command.
func printUsage(stderr io.Writer, unknown string) {
	if unknown != "" {
		fmt.Fprintf(stderr, "netpen: unknown command %q\n\n", unknown)
	}
	fmt.Fprintln(stderr, "Usage: netpen <command> [flags]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Commands:")
	for _, cmd := range commands {
		fmt.Fprintf(stderr, "  %-14s %s\n", cmd.name, cmd.short)
	}
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Global flags (per command):")
	fmt.Fprintln(stderr, "  -i string    attack interface (default eth0)")
	fmt.Fprintln(stderr, "  -w string    watch interface (optional)")
}

// runVersion prints the netpen version. It is the proof stub that exercises
// the dispatch path end-to-end.
func runVersion(_ context.Context, _, _ string, stdout, _ io.Writer) error {
	fmt.Fprintln(stdout, "netpen v0.1.0-dev")
	return nil
}

// legOpenError is a seam for testing the missing-interface failure path
// without importing the link package's linux-only types. It wraps link.Open
// so the test can inject a stub.
func legOpenError(iface string) error {
	_, err := link.Open(iface)
	return err
}
