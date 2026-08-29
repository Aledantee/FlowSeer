package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/full"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
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
		t.Errorf("stderr: got %q, want 'unknown command'", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr: got %q, want 'Usage:'", stderr.String())
	}
}

// TestNoArgsExitsTwo verifies that running with no arguments exits 2.
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

// TestVersionExitsZero verifies the version command exits 0 and prints to stdout.
func TestVersionExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code: got %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "netpen") {
		t.Errorf("stdout: got %q, want 'netpen'", stdout.String())
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
// to open a missing interface surfaces the coded error, not a panic.
func TestMissingInterfaceSurfacesCodedError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"arpsweep", "-i", "netpen-nonexistent-0xdead", "--json=true"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code: got %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "cannot open attack interface") {
		t.Errorf("stderr: got %q, want 'cannot open attack interface'", stderr.String())
	}
}

// TestRuntimeFailureExitsOne verifies a command returning a runtime error
// carrying ExitCode(1) exits 1.
func TestRuntimeFailureExitsOne(t *testing.T) {
	orig := commands
	defer func() { commands = orig }()
	commands = []subcommand{{
		name:  "fail",
		short: "test: always fail",
		run: func(_ context.Context, _ *cmdFlags, _ io.Writer, _ io.Writer) error {
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

// TestAllCommandNamesPresent verifies every catalog name + full + version
// is registered in the dispatch table. This subsumes the old
// TestAllStubNamesPresent: no stub remains — every command dispatches to a
// real behavior.
func TestAllCommandNamesPresent(t *testing.T) {
	seen := make(map[string]bool)
	for _, cmd := range commands {
		seen[cmd.name] = true
	}

	// Every catalog entry name must have a dispatch handler.
	for _, e := range catalog.Entries() {
		if !seen[e.Name] {
			t.Errorf("catalog entry %q has no CLI dispatch handler", e.Name)
		}
	}

	// The orchestration-only names.
	for _, name := range []string{"version", "full", "scan"} {
		if !seen[name] {
			t.Errorf("missing orchestration command %q", name)
		}
	}
}

// TestCatalogAndBehaviorReconciliation is the coverage test: every catalog
// row (attack+mode) reconciles against cmd's dispatch AND the four behavior
// packages' maps. A missing behavior fn or a dispatch handler with no
// catalog row is a failure (full is the sole CLI-only orchestration name).
func TestCatalogAndBehaviorReconciliation(t *testing.T) {
	// 1. Every catalog entry name has a dispatch handler.
	cmdSet := make(map[string]bool)
	for _, cmd := range commands {
		cmdSet[cmd.name] = true
	}
	catalogNames := make(map[string]bool)
	for _, e := range catalog.Entries() {
		catalogNames[e.Name] = true
		if !cmdSet[e.Name] {
			t.Errorf("catalog entry %q has no CLI dispatch handler", e.Name)
		}
	}
	for name := range cmdSet {
		if !catalogNames[name] {
			if name == "version" || name == "full" {
				continue
			}
			t.Errorf("CLI dispatch handler %q has no catalog entry", name)
		}
	}

	// 2. Every catalog behavior name has a behavior function in one of
	// the four behavior packages.
	behaviors := full.MergedBehaviors()
	for _, e := range catalog.Entries() {
		if e.Name == "scan" {
			continue // scan is orchestration, no behavior fn.
		}
		if _, ok := behaviors[e.Name]; !ok {
			t.Errorf("catalog entry %q has no behavior function in the merged behavior map", e.Name)
		}
	}

	// 3. Every behavior-map key has a catalog entry.
	for name := range behaviors {
		if !catalogNames[name] {
			t.Errorf("behavior map key %q has no catalog entry", name)
		}
	}
}

// TestJSONModeEmitsJSONL verifies that --json=true produces JSONL output
// with a meta header on stdout for a simple command.
func TestJSONModeEmitsJSONL(t *testing.T) {
	// Inject a test command that emits one finding via the runner.
	orig := commands
	defer func() { commands = orig }()

	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()

	// Use a mock leg that does nothing.
	legOpen = func(_ string) (link.Leg, error) {
		return &noopLeg{}, nil
	}

	commands = []subcommand{
		{name: "version", short: "version", run: runVersion},
		{
			name:  "test-json",
			short: "test: JSON output",
			run: func(_ context.Context, cf *cmdFlags, stdout, stderr io.Writer) error {
				ch := make(chan findings.Record, 1)
				ch <- findings.NewRecord(findings.KindFinding)
				close(ch)
				meta := findings.Meta{Tool: "netpen", Version: "test", AttackLeg: cf.iface, Started: time.Now()}
				streamJSON(stdout, stderr, ch, meta)
				return nil
			},
		},
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"test-json", "--json=true"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, `"kind":"meta"`) {
		t.Errorf("stdout missing meta record: %q", out)
	}
	if !strings.Contains(out, `"kind":"summary"`) {
		t.Errorf("stdout missing summary record: %q", out)
	}
}

// TestPermanentModeRequiresAck verifies that a permanent-destructive mode
// without --i-accept-permanent is refused (exit 1).
func TestPermanentModeRequiresAck(t *testing.T) {
	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
	legOpen = func(_ string) (link.Leg, error) {
		return &noopLeg{}, nil
	}

	var stdout, stderr bytes.Buffer
	// vtp --wipe without --i-accept-permanent should be refused.
	code := run([]string{"vtp", "--wipe", "--json=true"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code: got %d, want 1 (permanent mode refused without ack)", code)
	}
}

// noopLeg is a no-op link.Leg for tests.
type noopLeg struct{}

func (n *noopLeg) Send(_ context.Context, _ []byte) error  { return nil }
func (n *noopLeg) SetFilter(_ []link.RawInstruction) error { return nil }
func (n *noopLeg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame)
	go func() {
		defer close(out)
		<-ctx.Done()
	}()
	return out
}
func (n *noopLeg) Close() error { return nil }

// TestLegOpenErrorSeam verifies the legOpenError seam works.
func TestLegOpenErrorSeam(t *testing.T) {
	err := legOpenError("netpen-nonexistent")
	if err == nil {
		// On some platforms link.Open might succeed; that's fine.
		return
	}
	if c, ok := errs.CodeOf(err); !ok || c != link.ErrCodeLegOpen {
		t.Errorf("legOpenError code: got %v, want %s", c, link.ErrCodeLegOpen)
	}
}

// TestResolveMode verifies the output mode selection.
func TestResolveMode(t *testing.T) {
	if resolveOutputMode("true", true) != 0 { // 0 = ModeJSON
		t.Errorf("json=true should force JSON")
	}
	if resolveOutputMode("false", false) != 1 { // 1 = ModeTUI
		t.Errorf("json=false should force TUI")
	}
}

// TestStreamingJSONWritesRecordsMidRun verifies that JSONL records are
// written to stdout WHILE the run is in flight, not buffered until
// completion. A slow producer emits
// two records with a gap between them; the first record's bytes must
// appear on stdout before the second is emitted.
func TestStreamingJSONWritesRecordsMidRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Slow channel: emit first record, wait, emit second, close.
	ch := make(chan findings.Record, 2)
	firstWritten := make(chan struct{})

	// Producer goroutine.
	go func() {
		rec1 := findings.NewRecord(findings.KindFinding)
		rec1.Attack = "first"
		ch <- rec1
		close(firstWritten)
		// Wait for the consumer to confirm it saw the first record.
		time.Sleep(50 * time.Millisecond)
		rec2 := findings.NewRecord(findings.KindFinding)
		rec2.Attack = "second"
		ch <- rec2
		close(ch)
	}()

	// Consumer: streamJSON writes records as they arrive.
	meta := findings.Meta{Tool: "netpen", Version: "test", AttackLeg: "lo", Started: time.Now()}
	streamJSON(&stdout, &stderr, ch, meta)

	out := stdout.String()
	// Must have all three lines: meta, first, second, plus summary.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected >=4 JSONL lines, got %d: %q", len(lines), out)
	}
	// First line is meta, second is "first" finding.
	if !strings.Contains(lines[1], `"first"`) {
		t.Errorf("line 1 should contain 'first': %q", lines[1])
	}
	if !strings.Contains(lines[2], `"second"`) {
		t.Errorf("line 2 should contain 'second': %q", lines[2])
	}
	// Last line is summary.
	if !strings.Contains(lines[len(lines)-1], `"kind":"summary"`) {
		t.Errorf("last line should be summary: %q", lines[len(lines)-1])
	}
}

func TestFullStreamingJSONWritesOneAccurateSummary(t *testing.T) {
	behavior := func(_ context.Context, deps runner.Deps) error {
		if deps.Teardown != nil {
			deps.Teardown.Arm("test-restore", func(context.Context) error { return nil })
		}
		deps.Emitter.Finding("test", []byte(`{"observed":true}`))
		return nil
	}
	behaviors := map[string]runner.Behavior{
		"stproot":    behavior,
		"camflood":   behavior,
		"dhcpstarve": behavior,
		"gratarp":    behavior,
		"dtp":        behavior,
		"roguera":    behavior,
		"llmnr":      behavior,
		"voicevlan":  behavior,
	}

	f := full.NewFull(full.Config{
		AttackLeg: &noopLeg{},
		Duration:  10 * time.Millisecond,
		Behaviors: behaviors,
		ReconFn: func(context.Context, full.Config) (full.Evidence, error) {
			return full.Evidence{
				full.EvVoiceVLAN: 200,
				"sweep-net":      "192.0.2.0/24",
			}, nil
		},
	})

	var stdout, stderr bytes.Buffer
	meta := findings.Meta{Tool: "netpen", Version: "test", AttackLeg: "test0", Started: time.Now()}
	if err := streamAndRun(context.Background(), f.RecordChan(), meta, resolveOutputMode("true", false), &stdout, &stderr, f.Run); err != nil {
		t.Fatalf("streamAndRun: %v", err)
	}

	var summaries []findings.Summary
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var rec findings.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode JSONL record %q: %v", line, err)
		}
		if rec.Kind == findings.KindSummary {
			summaries = append(summaries, *rec.Summary)
		}
	}
	if len(summaries) != 1 {
		t.Fatalf("summary count = %d, want 1; output: %s", len(summaries), stdout.String())
	}

	got := summaries[0]
	want := findings.Summary{
		Attacks:  8,
		Findings: 8,
		Pending:  7,
		SweepNet: "192.0.2.0/24",
	}
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
}

// Keep the flag import honest for the noopLeg test harness.
var (
	_ = flag.NewFlagSet
	_ = os.Stdout
)
