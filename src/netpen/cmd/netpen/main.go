// Command netpen is the L2/L3 security audit and attack binary. It replaces
// the Python l2l3-audit tool with a self-contained static Go binary.
//
// This file holds the testable run core and the subcommand dispatch table.
// Every catalog command name dispatches to a real behavior: open legs via
// link.Open, build runner.Options, construct a Runner, install the
// SignalHandler, run, stream findings into the output layer selected by
// ResolveMode, and exit via run()'s 0/1/2 contract (KTD13).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/netpen/attacks/ip6"
	"go.aledante.io/FlowSeer/src/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/netpen/catalog"
	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/full"
	"go.aledante.io/FlowSeer/src/netpen/link"
	"go.aledante.io/FlowSeer/src/netpen/output"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

// Default attack-leg interface, matching the baseline l2l3-audit.
const defaultIface = "eth0"

// version is the netpen binary version.
const version = "v0.1.0-dev"

// subcommand is one entry in the dispatch table. Each command gets its own
// flag.FlagSet (KTD2). setup registers the command-specific flags into the
// cmdFlags struct; run reads them after parsing.
type subcommand struct {
	name  string
	short string
	setup func(fs *flag.FlagSet, cf *cmdFlags)
	run   func(ctx context.Context, cf *cmdFlags, stdout, stderr io.Writer) error
}

// cmdFlags holds all per-command flags. Each command populates only the
// fields it uses; the rest stay zero-valued.
type cmdFlags struct {
	// Global flags (available on every command).
	iface    string
	watch    string
	jsonMode string
	ack      ackList
	rate     int
	timeout  time.Duration
	tdBudget time.Duration

	// Full-specific.
	scanTime    time.Duration
	duration    time.Duration
	noSpoof     bool
	raPrefix    string
	stpTag      int
	commandName string // set by run() to the dispatched command name

	// Scan-specific.
	listenTime time.Duration
	noProbe    bool
	probeVLANs string
	probeTime  time.Duration

	// Attack mode flags.
	wipe      bool
	setVLANs  string
	persist   bool
	relay     bool
	keepTrunk bool

	// Per-attack generic flags (baseline-compatible where defined).
}

// ackList is a repeatable --i-accept-permanent=<name>=<mode> flag.
type ackList []runner.AttackRef

func (a *ackList) String() string { return fmt.Sprint([]runner.AttackRef(*a)) }
func (a *ackList) Set(s string) error {
	parts := strings.SplitN(s, "=", 2)
	if len(parts) == 2 {
		*a = append(*a, runner.AttackRef{Name: parts[0], Mode: parts[1]})
	} else {
		*a = append(*a, runner.AttackRef{Mode: s})
	}
	return nil
}

// commands is the dispatch table.
var commands []subcommand

// stubNames is the list of all command names registered in the dispatch
// table, kept as a literal so the catalog's CLI reconciliation test
// (catalog_test.go) can parse it from the AST and verify every catalog
// entry has a dispatch handler and vice versa. The list is the 35
// catalog behavior names plus the orchestration-only "full" (scan is a
// catalog entry; version is a proof stub the reconciliation test does
// not enumerate). The runtime dispatch table (commands) is built from
// the catalog in init(); this literal is the AST-readable mirror.
var stubNames = []string{
	"full", "scan", "arpsweep", "arpspoof", "stproot", "camflood", "dhcpstarve",
	"roguedhcp", "roguedhcp6", "roguera", "ndpspoof", "daddos", "raguard",
	"dtp", "doubletag", "vlanenum", "vlanhop", "voicevlan", "portsteal",
	"ghost", "vtp", "mvrp", "hsrp", "icmpredirect", "gratarp", "llmnr", "vrrp",
	"ospf", "eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp",
}

var _ = stubNames // AST-readable mirror for catalog CLI reconciliation test

func init() {
	commands = []subcommand{
		{name: "version", short: "print the netpen version and exit", run: runVersion},
		{name: "full", short: "full suite: recon, bounded attack burst, recon-gated follow-ups, report", setup: setupFull, run: runFull},
		{name: "scan", short: "passive detect + active VLAN probing (dual-segment observe)", setup: setupScan, run: runScan},
	}

	// Register every catalog command name as a single-attack handler.
	for _, entry := range catalog.Entries() {
		if entry.Name == "scan" {
			continue // scan has its own orchestration handler.
		}
		if findCommand(entry.Name) != nil {
			continue // modes share the same command name.
		}
		name := entry.Name
		commands = append(commands, subcommand{
			name:  name,
			short: catalogShort(name),
			setup: func(fs *flag.FlagSet, cf *cmdFlags) { setupAttack(fs, cf, name) },
			run:   runAttack,
		})
	}
}

// catalogShort returns the help text for a catalog command name.
func catalogShort(name string) string {
	for _, e := range catalog.Entries() {
		if e.Name == name && e.Mode == "" {
			return e.Help
		}
	}
	return name
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

	cf := &cmdFlags{
		iface:    defaultIface,
		raPrefix: "fd00:dead:beef::",
	}

	// Global flags on every command.
	fs.StringVar(&cf.iface, "i", defaultIface, "attack interface")
	fs.StringVar(&cf.watch, "w", "", "watch interface (optional; required by ghost)")
	fs.StringVar(&cf.jsonMode, "json", "", "emit JSONL on stdout (empty=auto, true=force JSON, false=force TUI)")
	fs.Var(&cf.ack, "i-accept-permanent", "acknowledge a permanent-destructive mode (repeatable: -i-accept-permanent=<name>=<mode>)")
	fs.IntVar(&cf.rate, "rate", 0, "packet rate limit (pps, 0=unlimited)")
	fs.DurationVar(&cf.timeout, "timeout", 0, "bound the whole run")
	fs.DurationVar(&cf.tdBudget, "td-budget", 0, "teardown budget (default 10s)")

	if cmd.setup != nil {
		cmd.setup(fs, cf)
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
	cf.commandName = name

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	if err := cmd.run(ctx, cf, stdout, stderr); err != nil {
		if um := errs.UserMessage(err); um != "" {
			fmt.Fprintln(stderr, um)
			if h := errs.Hint(err); h != "" {
				fmt.Fprintf(stderr, "hint: %s\n", h)
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
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

// printUsage writes the top-level usage to stderr.
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
	fmt.Fprintln(stderr, "  -i string             attack interface (default eth0)")
	fmt.Fprintln(stderr, "  -w string             watch interface (optional)")
	fmt.Fprintln(stderr, "  --json                emit JSONL on stdout (auto/true/false)")
	fmt.Fprintln(stderr, "  --i-accept-permanent  acknowledge a permanent mode (repeatable)")
	fmt.Fprintln(stderr, "  --rate int            packet rate limit (pps, 0=unlimited)")
	fmt.Fprintln(stderr, "  --timeout duration    bound the whole run")
}

// runVersion prints the netpen version.
func runVersion(_ context.Context, _ *cmdFlags, stdout, _ io.Writer) error {
	fmt.Fprintln(stdout, "netpen "+version)
	return nil
}

// --- leg opening seam ---

// legOpen is the seam for opening legs. Tests replace it to inject a
// mock leg without touching link.Open (linux-only). Production calls
// link.Open.
var legOpen = func(iface string) (link.Leg, error) { //nolint:gocritic // test seam
	return link.Open(iface)
}

// openLegs opens the attack leg and the optional watch leg.
func openLegs(attackIface, watchIface string) (link.Leg, link.Leg, error) {
	attackLeg, err := legOpen(attackIface)
	if err != nil {
		return nil, nil, errs.From(err).
			Code(link.ErrCodeLegOpen).
			ExitCode(1).
			UserMsg(fmt.Sprintf("cannot open attack interface %q", attackIface)).
			Hint("pass an existing -i <iface> (default eth0)").
			Msgf("attack leg open: %v", err)
	}

	var watchLeg link.Leg
	if watchIface != "" {
		wl, err := legOpen(watchIface)
		if err != nil {
			_ = attackLeg.Close()
			return nil, nil, errs.From(err).
				Code(link.ErrCodeLegOpen).
				ExitCode(1).
				UserMsg(fmt.Sprintf("cannot open watch interface %q", watchIface)).
				Msgf("watch leg open: %v", err)
		}
		watchLeg = wl
	}
	return attackLeg, watchLeg, nil
}

// --- output mode ---

func resolveOutputMode(jsonFlag string, stdoutIsTTY bool) output.Mode {
	if jsonFlag == "true" {
		return output.ModeJSON
	}
	if jsonFlag == "false" {
		return output.ModeTUI
	}
	return output.ResolveMode(jsonFlag, stdoutIsTTY)
}

// --- single-attack handler ---

func setupAttack(fs *flag.FlagSet, cf *cmdFlags, name string) {
	fs.DurationVar(&cf.duration, "duration", 0, "bound the attack window")
	switch name {
	case "vtp":
		fs.BoolVar(&cf.wipe, "wipe", false, "VTP VLAN-database wipe (permanent; requires --i-accept-permanent)")
		fs.StringVar(&cf.setVLANs, "set", "", "VTP VLAN-database overwrite (permanent; requires --i-accept-permanent)")
	case "vlanhop":
		fs.BoolVar(&cf.persist, "persist", false, "persistent VLAN hop (permanent; requires --i-accept-permanent)")
	case "voicevlan":
		fs.BoolVar(&cf.persist, "persist", false, "persistent voice-VLAN hijack (permanent; requires --i-accept-permanent)")
	case "portsteal":
		fs.BoolVar(&cf.relay, "relay", false, "port-steal with relay (arms ip_forward restore)")
	case "dtp":
		fs.BoolVar(&cf.keepTrunk, "keep-trunk", false, "keep the negotiated trunk (permanent; requires --i-accept-permanent)")
	}
}

// resolveMode determines the mode flag from the command flags.
func resolveMode(cf *cmdFlags) string {
	if cf.wipe {
		return "wipe"
	}
	if cf.setVLANs != "" {
		return "set"
	}
	if cf.persist {
		return "persist"
	}
	if cf.relay {
		return "relay"
	}
	if cf.keepTrunk {
		return "keep-trunk"
	}
	return ""
}

func runAttack(ctx context.Context, cf *cmdFlags, stdout, stderr io.Writer) error {
	attackLeg, watchLeg, err := openLegs(cf.iface, cf.watch)
	if err != nil {
		return err
	}
	defer func() { _ = attackLeg.Close() }()
	if watchLeg != nil {
		defer func() { _ = watchLeg.Close() }()
	}

	mode := resolveMode(cf)
	ref := runner.AttackRef{Name: cf.commandName, Mode: mode}

	opts := runner.Options{
		AttackLeg:      attackLeg,
		WatchLeg:       watchLeg,
		Attacks:        []runner.AttackRef{ref},
		Behaviors:      mergedCmdBehaviors(),
		Rate:           cf.rate,
		Timeout:        cf.timeout,
		Acknowledged:   cf.ack,
		TeardownBudget: cf.tdBudget,
		SuppressOutput: true,
	}

	return runAndOutput(ctx, opts, cf, stdout, stderr)
}

// mergedCmdBehaviors builds the merged behavior map from the four
// behavior packages.
func mergedCmdBehaviors() map[string]runner.Behavior {
	out := make(map[string]runner.Behavior)
	for _, m := range []map[string]runner.Behavior{
		l2.Behaviors(), fh.Behaviors(), ip6.Behaviors(), routing.Behaviors(),
	} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// --- full handler ---

func setupFull(fs *flag.FlagSet, cf *cmdFlags) {
	fs.DurationVar(&cf.scanTime, "scan-time", 15*time.Second, "passive scan window")
	fs.DurationVar(&cf.duration, "duration", 30*time.Second, "attack window")
	fs.BoolVar(&cf.noSpoof, "no-spoof", false, "skip ARP poisoning of discovered hosts")
	fs.StringVar(&cf.raPrefix, "ra-prefix", "fd00:dead:beef::", "IPv6 prefix offered by the rogue RA")
	fs.IntVar(&cf.stpTag, "stp-tag", 0, "also attack this VLAN with tagged PVST+ BPDUs")
}

func runFull(ctx context.Context, cf *cmdFlags, stdout, stderr io.Writer) error {
	attackLeg, watchLeg, err := openLegs(cf.iface, cf.watch)
	if err != nil {
		return err
	}
	defer func() { _ = attackLeg.Close() }()
	if watchLeg != nil {
		defer func() { _ = watchLeg.Close() }()
	}

	cfg := full.FullConfig{
		AttackLeg:      attackLeg,
		WatchLeg:       watchLeg,
		WatchLegNamed:  cf.watch,
		AttackLegName:  cf.iface,
		Duration:       cf.duration,
		ScanTime:       cf.scanTime,
		NoSpoof:        cf.noSpoof,
		Rate:           cf.rate,
		Behaviors:      mergedCmdBehaviors(),
		TeardownBudget: cf.tdBudget,
		SweepNet:       "172.16.0.0/24",
	}

	f := full.NewFull(cfg)
	return runOrchestrator(ctx, f.RecordChan(), f.Run, cf, stdout, stderr)
}

// runOrchestrator is the shared live-streaming driver for full and scan.
// It starts the run in a goroutine, then drains the live record channel
// into the output layer (JSONL or TUI) concurrently (R11, KTD10).
func runOrchestrator(ctx context.Context, ch <-chan findings.Record, runFn func(context.Context) error, cf *cmdFlags, stdout, stderr io.Writer) error {
	meta := findings.Meta{
		Tool:      "netpen",
		Version:   version,
		AttackLeg: cf.iface,
		WatchLeg:  cf.watch,
		Started:   time.Now(),
	}
	mode := resolveOutputMode(cf.jsonMode, isStdoutTTY(stdout))

	if mode == output.ModeJSON {
		done := make(chan struct{})
		go func() {
			streamJSON(stdout, stderr, ch, meta)
			close(done)
		}()
		runErr := runFn(ctx)
		<-done
		return runErr
	}

	// TUI mode: feed records live via bubbletea program.
	done := make(chan struct{})
	go func() {
		_ = streamTUI(ch, meta)
		close(done)
	}()
	runErr := runFn(ctx)
	<-done
	return runErr
}

// --- scan handler ---

func setupScan(fs *flag.FlagSet, cf *cmdFlags) {
	fs.DurationVar(&cf.listenTime, "time", 35*time.Second, "passive listen window")
	fs.BoolVar(&cf.noProbe, "no-probe", false, "stay passive; skip active VLAN probing")
	fs.StringVar(&cf.probeVLANs, "probe-vlans", "", "candidate VLANs to probe (default: common ids + leaked)")
	fs.DurationVar(&cf.probeTime, "probe-time", 6*time.Second, "active-probe reply window")
}

func runScan(ctx context.Context, cf *cmdFlags, stdout, stderr io.Writer) error {
	attackLeg, watchLeg, err := openLegs(cf.iface, cf.watch)
	if err != nil {
		return err
	}
	defer func() { _ = attackLeg.Close() }()
	if watchLeg != nil {
		defer func() { _ = watchLeg.Close() }()
	}

	cfg := full.ScanConfig{
		AttackLeg:     attackLeg,
		WatchLeg:      watchLeg,
		WatchLegNamed: cf.watch,
		AttackLegName: cf.iface,
		Time:          cf.listenTime,
		NoProbe:       cf.noProbe,
		ProbeVLANs:    cf.probeVLANs,
		ProbeTime:     cf.probeTime,
	}

	s := full.NewScan(cfg)
	return runOrchestrator(ctx, s.RecordChan(), s.Run, cf, stdout, stderr)
}

// --- run + output streaming ---

// runAndOutput runs a single-attack runner and streams findings LIVE
// into the output layer as they arrive (R11, KTD10). The runner's
// stream is consumed concurrently with Run.
func runAndOutput(ctx context.Context, opts runner.Options, cf *cmdFlags, stdout, stderr io.Writer) error {
	r := runner.NewRunner(opts)
	sig := runner.NewSignalHandler(r)
	stopSig := sig.Install()
	defer stopSig()

	// Bridge runner.Stream.Iter() to a channel for streamJSON/streamTUI.
	ch := make(chan findings.Record, 64)
	go func() {
		defer close(ch)
		for rec := range r.Stream().Iter() {
			ch <- rec
		}
	}()

	meta := findings.Meta{
		Tool:      "netpen",
		Version:   version,
		AttackLeg: cf.iface,
		WatchLeg:  cf.watch,
		Started:   time.Now(),
	}
	mode := resolveOutputMode(cf.jsonMode, isStdoutTTY(stdout))

	if mode == output.ModeJSON {
		done := make(chan struct{})
		go func() {
			streamJSON(stdout, stderr, ch, meta)
			close(done)
		}()
		runErr := r.Run(ctx)
		r.Wait()
		<-done
		return runErr
	}

	// TUI mode.
	done := make(chan struct{})
	go func() {
		_ = streamTUI(ch, meta)
		close(done)
	}()
	runErr := r.Run(ctx)
	r.Wait()
	<-done
	return runErr
}

// isStdoutTTY reports whether stdout is a terminal.
func isStdoutTTY(stdout io.Writer) bool {
	if f, ok := stdout.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

// main is the OS entrypoint.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// legOpenError is a seam for testing the missing-interface failure path.
func legOpenError(iface string) error {
	_, err := legOpen(iface)
	return err
}
