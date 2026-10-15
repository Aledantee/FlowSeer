package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/full"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
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
	original := legOpen
	t.Cleanup(func() { legOpen = original })
	legOpen = func(string) (link.Leg, error) {
		return nil, errs.New().Code(link.ErrCodeLegOpen).Msg("interface unavailable")
	}
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

// TestCatalogAndDispatchReconcile walks the shipped catalog against the
// dispatch table both ways: every entry name has a handler, and every
// handler is a catalog entry, with full and version the only commands
// outside the catalog.
func TestCatalogAndDispatchReconcile(t *testing.T) {
	cmdSet := make(map[string]bool)
	for _, cmd := range commands {
		cmdSet[cmd.name] = true
	}
	catalogNames := make(map[string]bool)
	for _, e := range attacks.Default().Entries() {
		catalogNames[e.Name] = true
		if !cmdSet[e.Name] {
			t.Errorf("catalog entry %q has no CLI dispatch handler", e.Name)
		}
	}
	for name := range cmdSet {
		if name == "version" || name == "full" {
			continue
		}
		if !catalogNames[name] {
			t.Errorf("CLI dispatch handler %q has no catalog entry", name)
		}
	}
	for _, name := range []string{"version", "full", "scan"} {
		if !cmdSet[name] {
			t.Errorf("missing orchestration command %q", name)
		}
	}
}

// TestJSONModeEmitsJSONL verifies that --json=true produces JSONL output
// with a meta header on stdout for a simple command.
func TestJSONModeEmitsJSONL(t *testing.T) {
	orig := commands
	defer func() { commands = orig }()

	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
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
				return streamJSON(stdout, stderr, ch, meta)
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

// TestPermanentModeRequiresAck verifies that a permanent mode admitted by
// the ceiling but without --i-accept-permanent is refused (exit 1) with a
// hint naming the acknowledgment in the flag's own form.
func TestPermanentModeRequiresAck(t *testing.T) {
	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
	legOpen = func(_ string) (link.Leg, error) {
		return &noopLeg{}, nil
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"vtp", "--wipe", "--max-impact", "permanent", "--vtp-domain", "lab", "--json=true"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code: got %d, want 1 (permanent mode refused without ack)", code)
	}
	if !strings.Contains(stdout.String(), "--i-accept-permanent vtp=wipe") {
		t.Errorf("refusal does not name the acknowledgment; stdout:\n%s", stdout.String())
	}
}

// TestCeilingFlag pins the ceiling on the command line: the default admits
// a temporary entry, a ceiling of none refuses it with a refusal record
// and exit 0, and an unknown value is a usage error.
func TestCeilingFlag(t *testing.T) {
	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
	legOpen = func(_ string) (link.Leg, error) {
		return &noopLeg{}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"camflood", "--max-impact", "none", "--json=true"}, &stdout, &stderr); code != 0 {
		t.Errorf("exit code under a ceiling of none: got %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"reason":"impact temporary exceeds ceiling none"`) {
		t.Errorf("no ceiling refusal in stdout:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"camflood", "--json=true"}, &stdout, &stderr); code != 0 {
		t.Errorf("exit code under the default ceiling: got %d, want 0; stderr: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), `"kind":"refusal"`) {
		t.Errorf("the default ceiling refused a temporary entry:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"camflood", "--max-impact", "catastrophic"}, &stdout, &stderr); code != 2 {
		t.Errorf("exit code for an unknown ceiling: got %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--max-impact must be none, temporary, or permanent") {
		t.Errorf("stderr does not explain the ceiling values: %s", stderr.String())
	}
}

// verdictFromStdout decodes the JSONL on stdout and returns the verdict
// record's payload, or nil.
func verdictFromStdout(t *testing.T, stdout string) *findings.Verdict {
	t.Helper()
	var verdict *findings.Verdict
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		var rec findings.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode JSONL record %q: %v", line, err)
		}
		if rec.Kind == findings.KindVerdict {
			verdict = rec.Verdict
		}
	}
	return verdict
}

// stubShipped replaces the shipped catalog for the test with one whose
// named behavior runs stub, so a command test reads what the runner
// handed the behavior.
func stubShipped(t *testing.T, name string, stub check.Run) {
	t.Helper()
	orig := shipped
	t.Cleanup(func() { shipped = orig })
	bs := attacks.Behaviors()
	for i := range bs {
		if bs[i].Name == name {
			bs[i].Run = stub
		}
	}
	shipped = catalog.MustNew(bs...)
}

// TestMissingTargetExitsTwo pins the one-round contract on the command: a
// check run with its hosts' addresses but not their MACs emits a skipped
// verdict naming the two flags in the form that carries both parts, sends
// nothing, prints that text on stderr, and exits 2.
func TestMissingTargetExitsTwo(t *testing.T) {
	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
	leg := &noopLeg{}
	legOpen = func(_ string) (link.Leg, error) { return leg, nil }

	var stdout, stderr bytes.Buffer
	code := run([]string{"arpspoof", "--json=true", "--victim", "172.16.0.10", "--gateway", "172.16.0.1"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code: got %d, want 2; stderr: %s", code, stderr.String())
	}
	if leg.sends.Load() != 0 {
		t.Errorf("sends = %d, want 0", leg.sends.Load())
	}
	verdict := verdictFromStdout(t, stdout.String())
	if verdict == nil || verdict.Kind != findings.VerdictSkipped {
		t.Fatalf("verdict = %+v, want skipped", verdict)
	}
	const want = "the check needs: --victim <ip>,<mac>, --gateway <ip>,<mac>"
	if verdict.Detail != want {
		t.Errorf("verdict detail = %q, want %q", verdict.Detail, want)
	}
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr lacks %q: %s", want, stderr.String())
	}
}

// TestTargetFlagsParse pins the flag shapes: a host with and without its
// MAC, an address, a network, and the number ranges, each typed by family
// so a value of the other family exits 2 at the flag with the form it
// wants.
func TestTargetFlagsParse(t *testing.T) {
	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
	legOpen = func(_ string) (link.Leg, error) { return &noopLeg{}, nil }

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"arpspoof", "--json=true",
		"--victim", "172.16.0.10,00:aa:bb:cc:dd:01", "--gateway", "172.16.0.1,00:aa:bb:cc:dd:fe",
	}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code with a full target: got %d, want 0; stderr: %s", code, stderr.String())
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"arpspoof", "--victim", "not-an-ip"}, "want an IPv4 address as <ip> or <ip>,<mac>"},
		{[]string{"arpspoof", "--victim", "172.16.0.10,zz"}, "want a six-octet MAC after the comma"},
		{[]string{"arpspoof", "--victim", "10.0.0.1,0102.0304.0506.0708"}, "want a six-octet MAC after the comma"},
		{[]string{"portsteal", "--victim", "2001:db8::1"}, "want an IPv4 address as <ip> or <ip>,<mac>"},
		{[]string{"ndpspoof", "--victim6", "10.0.0.5"}, "want an IPv6 address as <ip> or <ip>,<mac>"},
		{[]string{"ospf", "--router-id", "2001:db8::1"}, `invalid value "2001:db8::1" for flag -router-id: want an IPv4 address`},
		{[]string{"roguera", "--prefix6", "fd00::"}, "want an IPv6 network in CIDR form"},
		{[]string{"roguera", "--prefix6", "10.0.0.0/8"}, "want an IPv6 network in CIDR form"},
		{[]string{"eigrp", "--route4", "2001:db8::/64"}, "want an IPv4 network in CIDR form"},
		{[]string{"vrrp", "--vrid", "300"}, "want a number from 1 to 255"},
		{[]string{"vlanhop", "--outer-vlan", "0"}, "want a VLAN id from 1 to 4094"},
		{[]string{"vlanhop", "--outer-vlan", "4095"}, "want a VLAN id from 1 to 4094"},
		{[]string{"vlanhop", "--inner-vlan", "4096"}, "want a VLAN id from 1 to 4094"},
		{[]string{"hsrp", "--group", "-1"}, "want a number from 0 to 65535"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(tc.args, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit code %d, want 2", tc.args, code)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%v: stderr lacks %q:\n%s", tc.args, tc.want, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%v: stdout = %q, want no record", tc.args, stdout.String())
		}
	}
}

// TestGroupZeroReachesRunner pins that --group 0 is a set group: the
// behavior sees a non-nil pointer to 0, since HSRP group 0 is a group.
func TestGroupZeroReachesRunner(t *testing.T) {
	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
	legOpen = func(_ string) (link.Leg, error) { return &noopLeg{}, nil }
	var got *uint16
	stubShipped(t, "hsrp", func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Teardown.Arm("stub", func(context.Context) error { return nil })
		got = deps.Target.Group
		return check.Pending("stub"), nil
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"hsrp", "--json=true", "--group", "0", "--vip", "10.0.0.1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if got == nil || *got != 0 {
		t.Errorf("behavior saw group %v, want a pointer to 0", got)
	}
}

// TestObserveFlagReachesRunner pins that --observe is the window the
// behavior receives.
func TestObserveFlagReachesRunner(t *testing.T) {
	origLegOpen := legOpen
	defer func() { legOpen = origLegOpen }()
	legOpen = func(_ string) (link.Leg, error) { return &noopLeg{}, nil }
	stubShipped(t, "arpspoof", func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Teardown.Arm("stub", func(context.Context) error { return nil })
		return check.Pending(deps.Observe.String()), nil
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"arpspoof", "--json=true", "--observe", "750ms"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if v := verdictFromStdout(t, stdout.String()); v == nil || v.Detail != "750ms" {
		t.Errorf("verdict = %+v, want the 750ms window as its detail", v)
	}
}

// TestAckListRejectsBareMode pins that the acknowledgment flag takes only
// name=mode, since a bare mode can never match a gate key.
func TestAckListRejectsBareMode(t *testing.T) {
	var a ackList
	for _, bad := range []string{"wipe", "=wipe", "vtp=", ""} {
		if err := a.Set(bad); err == nil {
			t.Errorf("Set(%q) = nil, want an error", bad)
		}
	}
	if err := a.Set("vtp=wipe"); err != nil {
		t.Fatalf("Set(vtp=wipe): %v", err)
	}
	if len(a) != 1 || a[0].Name != "vtp" || a[0].Mode != "wipe" {
		t.Errorf("ackList = %+v, want [{vtp wipe}]", a)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"vtp", "--i-accept-permanent", "wipe"}, &stdout, &stderr); code != 2 {
		t.Errorf("exit code for a bare mode: got %d, want 2", code)
	}
}

// noopLeg is a no-op link.Leg for tests that counts its sends.
type noopLeg struct{ sends atomic.Int64 }

func (n *noopLeg) Send(_ context.Context, _ []byte) error {
	n.sends.Add(1)
	return nil
}
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

func (n *noopLeg) Addr() link.Addr {
	return link.Addr{MAC: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}, IPv4: net.IPv4(10, 0, 0, 2).To4()}
}

// TestLegOpenErrorSeam verifies the legOpenError seam works.
func TestLegOpenErrorSeam(t *testing.T) {
	original := legOpen
	t.Cleanup(func() { legOpen = original })
	legOpen = func(string) (link.Leg, error) {
		return nil, errs.New().Code(link.ErrCodeLegOpen).Msg("interface unavailable")
	}
	err := legOpenError("netpen-nonexistent")
	if err == nil {
		t.Fatal("legOpenError = nil, want missing-interface error")
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
// completion.
func TestStreamingJSONWritesRecordsMidRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	ch := make(chan findings.Record, 2)
	firstWritten := make(chan struct{})

	go func() {
		rec1 := findings.NewRecord(findings.KindFinding)
		rec1.Attack = "first"
		ch <- rec1
		close(firstWritten)
		// The gap is the timing under test: the first record must be
		// on stdout before the second is produced.
		time.Sleep(50 * time.Millisecond)
		rec2 := findings.NewRecord(findings.KindFinding)
		rec2.Attack = "second"
		ch <- rec2
		close(ch)
	}()

	meta := findings.Meta{Tool: "netpen", Version: "test", AttackLeg: "lo", Started: time.Now()}
	if err := streamJSON(&stdout, &stderr, ch, meta); err != nil {
		t.Fatal(err)
	}

	out := stdout.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 {
		t.Fatalf("got %d JSONL lines, want at least 4: %q", len(lines), out)
	}
	if !strings.Contains(lines[1], `"first"`) {
		t.Errorf("line 1 should contain 'first': %q", lines[1])
	}
	if !strings.Contains(lines[2], `"second"`) {
		t.Errorf("line 2 should contain 'second': %q", lines[2])
	}
	if !strings.Contains(lines[len(lines)-1], `"kind":"summary"`) {
		t.Errorf("last line should be summary: %q", lines[len(lines)-1])
	}
}

func TestFullStreamingJSONWritesOneAccurateSummary(t *testing.T) {
	bs := attacks.Behaviors()
	for i := range bs {
		if bs[i].Orchestration {
			continue
		}
		bs[i].Run = func(_ context.Context, deps check.Deps) (check.Verdict, error) {
			if deps.Teardown != nil {
				deps.Teardown.Arm("test-restore", func(context.Context) error { return nil })
			}
			deps.Emitter.Finding("test", []byte(`{"observed":true}`))
			return check.Pending("stub"), nil
		}
	}

	f := full.NewFull(full.Config{
		AttackLeg: &noopLeg{},
		Duration:  10 * time.Millisecond,
		Catalog:   catalog.MustNew(bs...),
		MaxImpact: check.ImpactTemporary,
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
		Pending:  8,
		SweepNet: "192.0.2.0/24",
	}
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
}

func TestOrchestratorTimeoutFlagReachesRun(t *testing.T) {
	for _, name := range []string{"full", "scan"} {
		t.Run(name, func(t *testing.T) {
			original := commands
			t.Cleanup(func() { commands = original })
			commands = []subcommand{{name: name, run: func(ctx context.Context, cf *cmdFlags, stdout, stderr io.Writer) error {
				ch := make(chan findings.Record)
				return runOrchestrator(ctx, ch, func(ctx context.Context) error {
					defer close(ch)
					if _, ok := ctx.Deadline(); !ok {
						t.Error("orchestrator context has no deadline")
						return nil
					}
					<-ctx.Done()
					if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
						t.Errorf("context error = %v, want deadline exceeded", ctx.Err())
					}
					return ctx.Err()
				}, cf, stdout, stderr)
			}}}
			var stdout, stderr bytes.Buffer
			if code := run([]string{name, "--timeout=1ms", "--json=true"}, &stdout, &stderr); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
		})
	}
}
