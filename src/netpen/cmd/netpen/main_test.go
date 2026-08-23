package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TestBogusCommandExitsTwoWithUsage verifies that an unrecognized command
// exits 2 with usage on stderr.
func TestBogusCommandExitsTwoWithUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"bogus"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code: got %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr: got %q, want it to contain 'unknown command'", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr: got %q, want it to contain 'Usage:'", stderr.String())
	}
}

// TestNoArgsExitsTwo verifies that running with no arguments exits 2 with
// usage.
func TestNoArgsExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code: got %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr: got %q, want 'Usage:'", stderr.String())
	}
}

// TestVersionExitsZero verifies the version stub exits 0 and prints to stdout.
func TestVersionExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code: got %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "netpen") {
		t.Errorf("stdout: got %q, want it to contain 'netpen'", stdout.String())
	}
}

// TestStubSubcommandExitsZero verifies a stub subcommand exits 0.
func TestStubSubcommandExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"scan"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code: got %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "not yet implemented") {
		t.Errorf("stderr: got %q, want 'not yet implemented'", stderr.String())
	}
}

// TestFlagParseErrorExitsTwo verifies a flag parse failure exits 2.
func TestFlagParseErrorExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version", "-nonexistent"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code: got %d, want 2", code)
	}
}

// TestMissingInterfaceSurfacesCodedError verifies that a command that tries
// to open a missing interface surfaces the coded error, not a panic. The
// test uses a stub command that calls link.Open; on non-Linux it gets the
// unsupported-platform error, on Linux the open fails. Both carry
// netpen/leg-open.
func TestMissingInterfaceSurfacesCodedError(t *testing.T) {
	// Inject a command that opens a nonexistent interface and fails.
	orig := commands
	defer func() { commands = orig }()

	commands = []subcommand{{
		name:  "test-leg-open",
		short: "test: open a leg",
		run: func(_ context.Context, attackIface, _ string, _ io.Writer, _ io.Writer) error {
			err := legOpenError(attackIface)
			if err == nil {
				return nil
			}
			return errs.From(err).ExitCode(1).Msg("leg open failed")
		},
	}}

	var stdout, stderr bytes.Buffer

	code := run([]string{"test-leg-open", "-i", "netpen-nonexistent-0xdead"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code: got %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "leg open failed") {
		t.Errorf("stderr: got %q, want 'leg open failed'", stderr.String())
	}
}

// TestRuntimeFailureExitsOne verifies that a command returning a runtime
// error carrying ExitCode(1) exits 1.
func TestRuntimeFailureExitsOne(t *testing.T) {
	orig := commands
	defer func() { commands = orig }()

	commands = []subcommand{{
		name:  "fail",
		short: "test: always fail",
		run: func(_ context.Context, _, _ string, _ io.Writer, _ io.Writer) error {
			return errs.New().ExitCode(1).Msg("simulated runtime failure")
		},
	}}

	var stdout, stderr bytes.Buffer

	code := run([]string{"fail"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code: got %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "simulated runtime failure") {
		t.Errorf("stderr: got %q, want 'simulated runtime failure'", stderr.String())
	}
}

// TestAllStubNamesPresent verifies the 27 parity + 8 superset names are
// registered.
func TestAllStubNamesPresent(t *testing.T) {
	want := 27 + 8 + 1 // parity + superset + version
	if len(commands) != want {
		t.Errorf("command count: got %d, want %d", len(commands), want)
	}

	seen := make(map[string]bool)
	for _, cmd := range commands {
		seen[cmd.name] = true
	}

	required := []string{
		"version",
		"full", "scan", "arpsweep", "arpspoof", "stproot", "camflood", "dhcpstarve",
		"roguedhcp", "roguedhcp6", "roguera", "ndpspoof", "daddos", "raguard",
		"dtp", "doubletag", "vlanenum", "vlanhop", "voicevlan", "portsteal",
		"ghost", "vtp", "mvrp", "hsrp", "icmpredirect", "gratarp", "llmnr", "vrrp",
		"ospf", "eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp",
	}
	for _, name := range required {
		if !seen[name] {
			t.Errorf("missing command %q", name)
		}
	}
}

// errors.Is is referenced indirectly through the coded-error assertions; keep
// the import honest without a lint-tripper.
var _ = errors.Is
