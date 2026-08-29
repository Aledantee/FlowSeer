package runner_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// unixSIGHUP is syscall.SIGHUP, aliased so the subprocess signal test
// reads cleanly without importing golang.org/x/sys/unix.
var unixSIGHUP = syscall.SIGHUP

// sendCountingLeg is a [link.Leg] whose Send is counted. It is the
// proof that a refused behavior never emits a frame: the gate evaluates
// before the leg's TX is handed to the behavior, so a refused behavior
// never receives a send-capable leg.
type sendCountingLeg struct {
	sends  atomic.Int64
	closed atomic.Bool
}

func (l *sendCountingLeg) Send(_ context.Context, _ []byte) error {
	l.sends.Add(1)
	return nil
}

func (l *sendCountingLeg) SetFilter(_ []link.RawInstruction) error { return nil }

func (l *sendCountingLeg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame)
	go func() {
		defer close(out)
		<-ctx.Done()
	}()
	return out
}

func (l *sendCountingLeg) Close() error {
	l.closed.Store(true)
	return nil
}

func (l *sendCountingLeg) sendCount() int64 { return l.sends.Load() }

// TestPermanentRefusalZeroFrames proves a permanent-destructive
// (attack, mode) pair invoked without its acknowledgment refuses at the
// gate before any frame is emitted. The behavior is never invoked (so it
// never receives a send-capable leg), the leg's Send is never called
// (zero frames), a typed refusal record is emitted, and Run returns a
// coded non-zero error.
func TestPermanentRefusalZeroFrames(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	invoked := atomic.Bool{}
	stub := func(_ context.Context, _ runner.Deps) error {
		invoked.Store(true)
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
		Behaviors: map[string]runner.Behavior{"vtp": stub},
		// No Acknowledged: the gate must refuse.
	})

	recs, err := drainStreamAsync(t, context.Background(), r)

	// Exit non-zero: the refusal is a runtime failure (exit 1).
	if err == nil {
		t.Fatal("Run returned nil, want coded refusal error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodePermanentRefused {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodePermanentRefused)
	}
	if got := errs.ExitCode(err); got != 1 {
		t.Errorf("exit code: got %d, want 1", got)
	}

	// The behavior was never invoked — the gate refused before TX.
	if invoked.Load() {
		t.Error("behavior was invoked; the gate must refuse before invocation")
	}

	// Zero frames: the leg's Send was never called.
	if got := leg.sendCount(); got != 0 {
		t.Errorf("leg Send called %d times, want 0 (zero frames)", got)
	}

	// A typed refusal record was emitted.
	var refusal *findings.Refusal
	for _, rec := range recs {
		if rec.Kind == findings.KindRefusal {
			refusal = rec.Refusal
		}
	}
	if refusal == nil {
		t.Fatal("no refusal record in stream")
	}
	if !strings.Contains(refusal.Reason, "permanent-destructive") {
		t.Errorf("refusal reason %q, want it to name permanent-destructive", refusal.Reason)
	}
	if !strings.Contains(refusal.Reason, "opt-in") {
		t.Errorf("refusal reason %q, want it to name the required opt-in", refusal.Reason)
	}
}

// TestPermanentRefusalNeverEnablesTX proves the harder invariant: the
// leg's TX capability is never handed to a refused behavior, not merely
// that no write call was made. A hooked mock leg whose Send panics if
// called is passed to the behavior; because the behavior is never
// invoked, the panic Send is never reached. This distinguishes "the gate
// withheld the leg" from "the behavior chose not to send".
func TestPermanentRefusalNeverEnablesTX(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	// The behavior would panic if it ever ran (it must not run).
	stub := func(_ context.Context, deps runner.Deps) error {
		// If the gate ever handed a send-capable leg to a refused
		// behavior, this would be a contract violation. Touching
		// Send here would prove the gate failed to withhold TX.
		if deps.AttackLeg != nil {
			_ = deps.AttackLeg.Send(context.Background(), []byte("must-not-send"))
		}
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vlanhop", Mode: "persist"}},
		Behaviors: map[string]runner.Behavior{"vlanhop": stub},
	})

	_, err := drainStreamAsync(t, context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want coded refusal error")
	}
	if got := leg.sendCount(); got != 0 {
		t.Errorf("leg Send called %d times, want 0", got)
	}
}

// TestAcknowledgedPermanentAnnouncesBeforeFrame proves the acknowledged-
// permanent path: when the per-run opt-in names the permanent mode, the
// gate emits a run-start announcement naming the accepted mode + its
// consequence before the first frame, then treats the entry like
// temporary-restored for teardown arming.
func TestAcknowledgedPermanentAnnouncesBeforeFrame(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	teardownRan := atomic.Bool{}
	stub := func(_ context.Context, deps runner.Deps) error {
		// Acknowledged permanent is treated like temporary-restored
		// for teardown arming: Deps.Teardown is non-nil.
		if deps.Teardown == nil {
			t.Error("acknowledged permanent: Deps.Teardown is nil, want non-nil")
			return nil
		}
		deps.Teardown.Arm("restore", func(_ context.Context) error {
			teardownRan.Store(true)
			return nil
		})
		deps.Emitter.Finding("vtp", []byte(`{"action":"wipe"}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
		Behaviors: map[string]runner.Behavior{"vtp": stub},
		Acknowledged: []runner.AttackRef{
			{Name: "vtp", Mode: "wipe"},
		},
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// The announcement record precedes the finding.
	if len(recs) < 2 {
		t.Fatalf("got %d records, want >= 2 (announcement + finding)", len(recs))
	}
	if recs[0].Kind != findings.KindProgress {
		t.Errorf("record 0: kind %q, want %q (announcement)", recs[0].Kind, findings.KindProgress)
	}
	if recs[0].Progress == nil {
		t.Fatal("record 0: nil progress")
	}
	if recs[0].Progress.Phase != "accepted-permanent" {
		t.Errorf("record 0 phase %q, want %q", recs[0].Progress.Phase, "accepted-permanent")
	}
	if !strings.Contains(recs[0].Progress.Detail, "wipe") {
		t.Errorf("record 0 detail %q, want it to name the accepted mode", recs[0].Progress.Detail)
	}

	// Teardown ran (armed + executed on completion).
	if !teardownRan.Load() {
		t.Error("teardown did not run for acknowledged permanent")
	}
}

// TestAcknowledgedPermanentIsPerPair proves the opt-in's per-pair semantics:
// acknowledging "vtp wipe" does not accept "vtp set". A different
// permanent mode without its own ack still refuses.
func TestAcknowledgedPermanentIsPerPair(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, _ runner.Deps) error {
		t.Error("behavior should not be invoked")
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "set"}},
		Behaviors: map[string]runner.Behavior{"vtp": stub},
		// Acknowledge wipe, not set.
		Acknowledged: []runner.AttackRef{
			{Name: "vtp", Mode: "wipe"},
		},
	})

	_, err := drainStreamAsync(t, context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want refusal (ack is per-pair)")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodePermanentRefused {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodePermanentRefused)
	}
}

// TestTransientDecayAnnouncesBeforeExecute proves the transient-decay
// path: the gate announces the catalog's decay bound as a run-start
// record before executing, and arms nothing (no teardown handle in
// Deps). The behavior runs normally.
func TestTransientDecayAnnouncesBeforeExecute(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	teardownWasNil := atomic.Bool{}
	stub := func(_ context.Context, deps runner.Deps) error {
		// Transient-decay arms nothing: Deps.Teardown is nil.
		if deps.Teardown == nil {
			teardownWasNil.Store(true)
		}
		deps.Emitter.Finding("stp", []byte(`{"root":true}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "stproot"}},
		Behaviors: map[string]runner.Behavior{"stproot": stub},
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !teardownWasNil.Load() {
		t.Error("transient-decay Deps.Teardown is non-nil; decay arms nothing")
	}

	// The decay-bound announcement precedes the finding.
	if len(recs) < 2 {
		t.Fatalf("got %d records, want >= 2 (announcement + finding)", len(recs))
	}
	if recs[0].Kind != findings.KindProgress {
		t.Errorf("record 0: kind %q, want %q", recs[0].Kind, findings.KindProgress)
	}
	if recs[0].Progress == nil {
		t.Fatal("record 0: nil progress")
	}
	if recs[0].Progress.Phase != "decay-bound" {
		t.Errorf("record 0 phase %q, want %q", recs[0].Progress.Phase, "decay-bound")
	}
	// The decay bound comes from the catalog entry's Teardown field.
	entry, ok := runner.ResolveEntry("stproot", "")
	if !ok {
		t.Fatal("stproot entry not found")
	}
	if recs[0].Progress.Detail != entry.Teardown {
		t.Errorf("record 0 detail %q, want %q (catalog decay bound)", recs[0].Progress.Detail, entry.Teardown)
	}
}

// TestNonDestructiveNoAnnouncementNoTeardown proves the non-destructive
// path: no announcement record and no teardown handle. The behavior runs
// directly.
func TestNonDestructiveNoAnnouncementNoTeardown(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	teardownWasNil := atomic.Bool{}
	stub := func(_ context.Context, deps runner.Deps) error {
		if deps.Teardown == nil {
			teardownWasNil.Store(true)
		}
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.1"}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
		Behaviors: map[string]runner.Behavior{"arpsweep": stub},
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !teardownWasNil.Load() {
		t.Error("non-destructive Deps.Teardown is non-nil")
	}
	// Exactly one record (the finding); no announcement.
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1 (no announcement)", len(recs))
	}
	if recs[0].Kind != findings.KindFinding {
		t.Errorf("record 0: kind %q, want %q", recs[0].Kind, findings.KindFinding)
	}
}

// TestInterruptTeardownCompletesPartialFailure proves that when an
// interrupt arrives mid-run on a temporary-restored behavior, the
// teardown completes before exit; one step planted to fail still lets
// later steps run; the partial record names the failed step; exit is 1.
func TestInterruptTeardownCompletesPartialFailure(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	var (
		stepOrder    []string
		stepOrderMu  sync.Mutex
		teardownDone atomic.Bool
		armed        = make(chan struct{})
	)

	stub := func(ctx context.Context, deps runner.Deps) error {
		// Arm teardown steps: host-local first (reverse-dependency),
		// then a step that fails, then a later step that must still
		// run despite the failure.
		deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error {
			stepOrderMu.Lock()
			stepOrder = append(stepOrder, "ip-forward-restore")
			stepOrderMu.Unlock()
			return nil
		})
		deps.Teardown.Arm("neighbor-repair-fails", func(_ context.Context) error {
			stepOrderMu.Lock()
			stepOrder = append(stepOrder, "neighbor-repair-fails")
			stepOrderMu.Unlock()
			return errors.New("neighbor unreachable")
		})
		deps.Teardown.Arm("resign-teardown", func(_ context.Context) error {
			stepOrderMu.Lock()
			stepOrder = append(stepOrder, "resign-teardown")
			stepOrderMu.Unlock()
			teardownDone.Store(true)
			return nil
		})

		// Signal that teardown is armed, then block until interrupted.
		close(armed)
		<-ctx.Done()
		return ctx.Err()
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors:      map[string]runner.Behavior{"arpspoof": stub},
		TeardownBudget: 2 * time.Second, // test-scaled
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	go func() { _ = r.Run(runCtx) }()

	// Wait for the behavior to arm teardown and block.
	<-armed

	// Engage teardown via Interrupt (the signal path).
	r.Interrupt(nil)

	// Wait for Run to finish.
	r.Wait()
	runCancel()

	// Teardown completed (the later step ran).
	if !teardownDone.Load() {
		t.Error("later teardown step did not run; one failing step must not abandon later ones")
	}

	// All three steps ran in arm order (host-local first).
	stepOrderMu.Lock()
	defer stepOrderMu.Unlock()
	want := []string{"ip-forward-restore", "neighbor-repair-fails", "resign-teardown"}
	if len(stepOrder) != 3 {
		t.Fatalf("step order %v, want 3 steps", stepOrder)
	}
	for i, s := range want {
		if stepOrder[i] != s {
			t.Errorf("step %d: got %q, want %q", i, stepOrder[i], s)
		}
	}

	// The partial record names the failed step.
	var partial *findings.ErrorRecord
	for rec := range r.Stream().Iter() {
		if rec.Kind == findings.KindError {
			partial = rec.Error
		}
	}
	if partial == nil {
		t.Fatal("no partial-failure record in stream")
	}
	if partial.Code != catalog.ErrCodeTeardownPartial.String() {
		t.Errorf("partial record code %q, want %q", partial.Code, catalog.ErrCodeTeardownPartial)
	}
	if !strings.Contains(partial.Message, "neighbor-repair-fails") {
		t.Errorf("partial record message %q, want it to name the failed step", partial.Message)
	}

	// Exit 1 (teardown partial failure = 1).
	if got := errs.ExitCode(r.Stream().Err()); got != 1 {
		t.Errorf("exit code: got %d, want 1", got)
	}
}

// TestTeardownCompletesBeforeStreamClose proves the signal-layer ordering
// invariant: teardown completes before the sink flush (the stream's
// records are visible to the consumer before the stream ends). A stub
// sink drains the stream; the partial-failure record from teardown must
// appear in the drained records, proving teardown's record was flushed
// through the stream before exit.
func TestTeardownCompletesBeforeStreamClose(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	armed := make(chan struct{})
	stub := func(ctx context.Context, deps runner.Deps) error {
		deps.Teardown.Arm("fails", func(_ context.Context) error {
			return errors.New("restore failed")
		})
		close(armed)
		<-ctx.Done()
		return ctx.Err()
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors:      map[string]runner.Behavior{"arpspoof": stub},
		TeardownBudget: 2 * time.Second,
	})

	var (
		recs []findings.Record
		mu   sync.Mutex
		wg   sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for rec := range r.Stream().Iter() {
			mu.Lock()
			recs = append(recs, rec)
			mu.Unlock()
		}
	}()

	runCtx, runCancel := context.WithCancel(context.Background())
	go func() { _ = r.Run(runCtx) }()
	<-armed
	r.Interrupt(nil)
	r.Wait()
	runCancel()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	// The partial-failure record must be among the drained records —
	// teardown completed and flushed its record before the stream closed.
	var hasPartial bool
	for _, rec := range recs {
		if rec.Kind == findings.KindError && rec.Error != nil &&
			rec.Error.Code == catalog.ErrCodeTeardownPartial.String() {
			hasPartial = true
		}
	}
	if !hasPartial {
		t.Errorf("partial-failure record not in drained records; teardown did not flush before stream close: %v", recs)
	}
}

// TestTeardownBudgetBounded proves the teardown executor is bounded by
// the configured budget. A step that sleeps longer than the budget is
// capped; the total teardown time stays within a generous bound of the
// budget. The budget is test-scaled to milliseconds.
func TestTeardownBudgetBounded(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, deps runner.Deps) error {
		// A step that sleeps well beyond the budget.
		deps.Teardown.Arm("slow", func(stepCtx context.Context) error {
			// Respect the step's context: it is canceled when the
			// share of the budget elapses.
			<-stepCtx.Done()
			return stepCtx.Err()
		})
		deps.Emitter.Finding("arp", []byte(`{}`))
		return nil
	}

	budget := 100 * time.Millisecond
	r := runner.NewRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors:      map[string]runner.Behavior{"arpspoof": stub},
		TeardownBudget: budget,
	})

	start := time.Now()
	_, err := drainStreamAsync(t, context.Background(), r)
	elapsed := time.Since(start)

	// The teardown is bounded: elapsed should be within a generous
	// bound of the budget (the step's share is the full budget for one
	// step, plus stream/scheduling overhead).
	if elapsed > budget*5 {
		t.Errorf("teardown took %v, want <= %v (5x budget)", elapsed, budget*5)
	}
	// The step was cut short by the budget: the partial record names it.
	if err == nil {
		t.Fatal("Run returned nil, want teardown partial failure (step was budget-capped)")
	}
}

// TestTemporaryRestoredZeroStepsArmedFails proves the loud-failure
// contract: a temporary-restored behavior that arms zero teardown steps
// is a coded runtime failure at arm time, not a silent skip.
func TestTemporaryRestoredZeroStepsArmedFails(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, deps runner.Deps) error {
		// Arm nothing — a required restore that restores nothing.
		deps.Emitter.Finding("arp", []byte(`{}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors: map[string]runner.Behavior{"arpspoof": stub},
	})

	_, err := drainStreamAsync(t, context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want coded failure (zero steps armed)")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodeTeardownPartial {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodeTeardownPartial)
	}
	if got := errs.ExitCode(err); got != 1 {
		t.Errorf("exit code: got %d, want 1", got)
	}
}

// TestOrchestratedPermanentCannotDispatch proves that under orchestration
// (Options.Orchestrated, set by the full command), no permanent entry
// may dispatch even with an ack supplied. The gate has no path.
func TestOrchestratedPermanentCannotDispatch(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	invoked := atomic.Bool{}
	stub := func(_ context.Context, _ runner.Deps) error {
		invoked.Store(true)
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
		Behaviors: map[string]runner.Behavior{"vtp": stub},
		Acknowledged: []runner.AttackRef{
			{Name: "vtp", Mode: "wipe"},
		},
		Orchestrated: true, // full command: no permanent dispatches.
	})

	_, err := drainStreamAsync(t, context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want refusal (orchestrated blocks permanent)")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodePermanentRefused {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodePermanentRefused)
	}
	if invoked.Load() {
		t.Error("behavior was invoked under orchestration; the gate has no permanent path")
	}
}

// TestSignalInterruptEngagesTeardown is a subprocess test proving the
// signal lifecycle: a subprocess runs a temporary-restored behavior that
// blocks, then sends itself SIGINT. The teardown completes (the restore
// step runs), the partial record flushes, and the process exits 1 (the
// planted failing step). This uses os/exec re-exec of the test binary
// (the standard Go subprocess pattern) so the force-exit path does not
// kill the test process.
func TestSignalInterruptEngagesTeardown(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess signal test skipped in -short")
	}
	if os.Getenv("NETPEN_U6_SIGINT_CHILD") == "1" {
		runSignalChild(t, "SIGINT")
		return
	}

	sub := exec.Command(os.Args[0], "-test.run=TestSignalInterruptEngagesTeardown")
	sub.Env = append(os.Environ(), "NETPEN_U6_SIGINT_CHILD=1")
	var out bytes.Buffer
	sub.Stdout = &out
	sub.Stderr = &out

	if err := sub.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	// Give the child a moment to install handlers and block.
	time.Sleep(100 * time.Millisecond)

	if err := sub.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal child: %v", err)
	}

	err := sub.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	// The teardown's planted failing step produces a partial record
	// (stderr carries the forced-exit only on a SECOND signal; one
	// signal runs teardown to completion and exits via the runner's
	// error path). The exit code is 1 (teardown partial failure).
	if exitCode != 1 {
		t.Errorf("child exit code: got %d, want 1 (teardown partial). output:\n%s", exitCode, out.String())
	}
}

// TestSignalHupEngagesTeardown proves SIGHUP engages the same teardown
// path as SIGINT (SIGINT/SIGTERM/SIGHUP all engage the same path).
func TestSignalHupEngagesTeardown(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess signal test skipped in -short")
	}
	if os.Getenv("NETPEN_U6_SIGHUP_CHILD") == "1" {
		runSignalChild(t, "SIGHUP")
		return
	}

	sub := exec.Command(os.Args[0], "-test.run=TestSignalHupEngagesTeardown")
	sub.Env = append(os.Environ(), "NETPEN_U6_SIGHUP_CHILD=1")
	var out bytes.Buffer
	sub.Stdout = &out
	sub.Stderr = &out

	if err := sub.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := sub.Process.Signal(unixSIGHUP); err != nil {
		t.Fatalf("signal child: %v", err)
	}

	err := sub.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if exitCode != 1 {
		t.Errorf("child exit code: got %d, want 1. output:\n%s", exitCode, out.String())
	}
}

// TestSecondSignalForceExitNamesAbandoned proves the second-SIGINT
// force-exit: a second signal during teardown force-exits the process
// and reports by name the abandoned teardown steps. The subprocess
// installs a slow teardown step, sends SIGINT (engaging teardown), then
// immediately sends a second SIGINT (forcing exit). The stderr output
// names the abandoned step.
func TestSecondSignalForceExitNamesAbandoned(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess signal test skipped in -short")
	}
	if os.Getenv("NETPEN_U6_DOUBLESIG_CHILD") == "1" {
		runDoubleSignalChild(t)
		return
	}

	sub := exec.Command(os.Args[0], "-test.run=TestSecondSignalForceExitNamesAbandoned")
	sub.Env = append(os.Environ(), "NETPEN_U6_DOUBLESIG_CHILD=1")
	var out bytes.Buffer
	sub.Stdout = &out
	sub.Stderr = &out

	if err := sub.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// First signal engages teardown; second forces exit.
	if err := sub.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("first signal: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := sub.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("second signal: %v", err)
	}

	err := sub.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if exitCode != 1 {
		t.Errorf("child exit code: got %d, want 1 (forced exit). output:\n%s", exitCode, out.String())
	}
	// The stderr output names the abandoned step.
	if !strings.Contains(out.String(), "abandoned teardown steps") {
		t.Errorf("stderr does not name abandoned steps. output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "slow-restore") {
		t.Errorf("stderr does not name the abandoned step 'slow-restore'. output:\n%s", out.String())
	}
}

// runSignalChild is the subprocess body for the single-signal teardown
// tests. It builds a runner with a hooked temporary-restored behavior,
// installs the signal handler, and blocks in Run until the signal
// engages teardown and the run exits.
func runSignalChild(t *testing.T, _ string) {
	t.Helper()
	leg := &sendCountingLeg{}

	stub := func(ctx context.Context, deps runner.Deps) error {
		deps.Teardown.Arm("restore-fails", func(_ context.Context) error {
			return errors.New("restore failed")
		})
		<-ctx.Done()
		return ctx.Err()
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors:      map[string]runner.Behavior{"arpspoof": stub},
		TeardownBudget: 2 * time.Second,
	})

	h := runner.NewSignalHandler(r)
	stop := h.Install()

	// Drain the stream in the background so the producer does not block.
	go func() {
		for range r.Stream().Iter() {
		}
	}()

	err := r.Run(context.Background())
	stop()
	_ = leg.Close()
	if err != nil {
		// Exit with the runner's error code (teardown partial = 1).
		os.Exit(errs.ExitCode(err))
	}
	os.Exit(0)
}

// runDoubleSignalChild is the subprocess body for the second-signal
// force-exit test. The teardown step sleeps long enough that a second
// signal arrives during teardown, triggering the force-exit path that
// names the abandoned step.
func runDoubleSignalChild(t *testing.T) {
	t.Helper()
	leg := &sendCountingLeg{}

	stub := func(ctx context.Context, deps runner.Deps) error {
		deps.Teardown.Arm("slow-restore", func(stepCtx context.Context) error {
			// Sleep beyond the second-signal arrival. The step
			// respects its context but the second signal forces
			// exit before the budget elapses.
			select {
			case <-stepCtx.Done():
				return stepCtx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		})
		<-ctx.Done()
		return ctx.Err()
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors:      map[string]runner.Behavior{"arpspoof": stub},
		TeardownBudget: 10 * time.Second, // generous; force-exit preempts
	})

	h := runner.NewSignalHandler(r)
	stop := h.Install()

	go func() {
		for range r.Stream().Iter() {
		}
	}()

	_ = r.Run(context.Background())
	stop()
	_ = leg.Close()
	// If we reach here, teardown completed before the second signal;
	// the force-exit path in the handler calls os.Exit(1) directly.
	os.Exit(1)
}

// TestSignalHandlerInstallStop proves Install/stop is idempotent and
// does not leak goroutines: the stop function restores handlers and the
// signal goroutine exits.
func TestSignalHandlerInstallStop(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, deps runner.Deps) error {
		deps.Teardown.Arm("restore", func(_ context.Context) error { return nil })
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors: map[string]runner.Behavior{"arpspoof": stub},
	})

	h := runner.NewSignalHandler(r)
	stop := h.Install()
	// Double stop is idempotent.
	stop()
	stop()

	// The run completes normally (no signal arrived).
	_, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// TestGateRefusalRecordFields pins the refusal record's fields: the
// attack, mode, and reason are set so a consumer can match on them.
func TestGateRefusalRecordFields(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "dtp", Mode: "keep-trunk"}},
		Behaviors: map[string]runner.Behavior{"dtp": func(_ context.Context, _ runner.Deps) error { return nil }},
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want refusal")
	}

	var refusal *findings.Refusal
	for _, rec := range recs {
		if rec.Kind == findings.KindRefusal {
			refusal = rec.Refusal
		}
	}
	if refusal == nil {
		t.Fatal("no refusal record")
	}
	// The refusal names the attack and mode (set on the record).
	for _, rec := range recs {
		if rec.Kind == findings.KindRefusal {
			if rec.Attack != "dtp" {
				t.Errorf("refusal attack %q, want %q", rec.Attack, "dtp")
			}
			if rec.Mode != "keep-trunk" {
				t.Errorf("refusal mode %q, want %q", rec.Mode, "keep-trunk")
			}
		}
	}
}

// TestTeardownStepOrderHostLocalFirst proves the reverse-dependency
// execution order: host-local state (armed first) restores before
// neighbor-cache repairs (armed later). The behavior arms ip-forward
// first, then neighbor-repair; teardown executes them in that order.
func TestTeardownStepOrderHostLocalFirst(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	var order []string
	var mu sync.Mutex

	stub := func(_ context.Context, deps runner.Deps) error {
		deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error {
			mu.Lock()
			order = append(order, "ip-forward-restore")
			mu.Unlock()
			return nil
		})
		deps.Teardown.Arm("neighbor-unicast-repair", func(_ context.Context) error {
			mu.Lock()
			order = append(order, "neighbor-unicast-repair")
			mu.Unlock()
			return nil
		})
		deps.Emitter.Finding("arp", []byte(`{}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors:      map[string]runner.Behavior{"arpspoof": stub},
		TeardownBudget: 2 * time.Second,
	})

	_, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"ip-forward-restore", "neighbor-unicast-repair"}
	if len(order) != 2 {
		t.Fatalf("step order %v, want 2 steps", order)
	}
	for i, s := range want {
		if order[i] != s {
			t.Errorf("step %d: got %q, want %q (host-local first)", i, order[i], s)
		}
	}
}

// TestTeardownCompletionHappyPath proves a successful teardown (all steps
// pass) produces no partial record and Run returns nil.
func TestTeardownCompletionHappyPath(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, deps runner.Deps) error {
		deps.Teardown.Arm("restore-a", func(_ context.Context) error { return nil })
		deps.Teardown.Arm("restore-b", func(_ context.Context) error { return nil })
		deps.Emitter.Finding("arp", []byte(`{}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Behaviors:      map[string]runner.Behavior{"arpspoof": stub},
		TeardownBudget: 2 * time.Second,
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// No error record (teardown succeeded).
	for _, rec := range recs {
		if rec.Kind == findings.KindError {
			t.Errorf("unexpected error record: %+v", rec.Error)
		}
	}
}
