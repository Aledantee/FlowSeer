package runner_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

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

func (l *sendCountingLeg) Addr() link.Addr { return link.Addr{} }

func (l *sendCountingLeg) sendCount() int64 { return l.sends.Load() }

// refusalOf returns the first refusal record's payload, or nil.
func refusalOf(recs []findings.Record) *findings.Refusal {
	for _, rec := range recs {
		if rec.Kind == findings.KindRefusal {
			return rec.Refusal
		}
	}
	return nil
}

// TestCeilingRefusalContinues pins the gate order and the ceiling's
// outcome: a permanent entry under a temporary ceiling, even with a valid
// acknowledgment, is refused with a reason naming both impacts, sends
// nothing, gets a skipped verdict, and the next attack in the list runs.
func TestCeilingRefusalContinues(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	invoked := atomic.Bool{}
	next := atomic.Bool{}
	r := newRunner(runner.Options{
		AttackLeg:    leg,
		Attacks:      []runner.AttackRef{{Name: "vtp", Mode: "wipe"}, {Name: "arpsweep"}},
		MaxImpact:    check.ImpactTemporary,
		Acknowledged: []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
	}, map[string]check.Run{
		"vtp": func(_ context.Context, deps check.Deps) (check.Verdict, error) {
			invoked.Store(true)
			_ = deps.AttackLeg.Send(context.Background(), []byte("must-not-send"))
			return pending, nil
		},
		"arpsweep": func(context.Context, check.Deps) (check.Verdict, error) {
			next.Store(true)
			return pending, nil
		},
	})

	recs, err := drainStreamAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned %v, want nil: a ceiling refusal continues", err)
	}
	if invoked.Load() {
		t.Error("behavior was invoked; the ceiling must refuse before invocation")
	}
	if got := leg.sendCount(); got != 0 {
		t.Errorf("leg Send called %d times, want 0", got)
	}
	refusal := refusalOf(recs)
	if refusal == nil {
		t.Fatal("no refusal record in stream")
	}
	if refusal.Reason != "impact permanent exceeds ceiling temporary" {
		t.Errorf("refusal reason = %q, want %q", refusal.Reason, "impact permanent exceeds ceiling temporary")
	}
	v, ok := verdicts(recs)["vtp"]
	if !ok || v.Verdict == nil || v.Verdict.Kind != findings.VerdictSkipped || v.Mode != "wipe" {
		t.Errorf("vtp verdict = %+v, want skipped for mode wipe", v)
	}
	if !next.Load() {
		t.Error("the next attack did not run after the ceiling refusal")
	}
}

// TestCeilingAgainstEveryImpact pins the comparison for every (impact,
// ceiling) pair: an entry runs when its impact is at or below the ceiling
// and is refused above it.
func TestCeilingAgainstEveryImpact(t *testing.T) {
	entries := map[check.Impact]runner.AttackRef{
		check.ImpactNone:      {Name: "arpsweep"},
		check.ImpactTemporary: {Name: "camflood"},
		check.ImpactPermanent: {Name: "vtp", Mode: "wipe"},
	}
	impacts := []check.Impact{check.ImpactNone, check.ImpactTemporary, check.ImpactPermanent}
	for _, ceiling := range impacts {
		for _, impact := range impacts {
			t.Run(string(impact)+" under "+string(ceiling), func(t *testing.T) {
				leg := &sendCountingLeg{}
				t.Cleanup(func() { _ = leg.Close() })
				ref := entries[impact]
				ran := atomic.Bool{}
				stub := func(context.Context, check.Deps) (check.Verdict, error) {
					ran.Store(true)
					return pending, nil
				}
				r := newRunner(runner.Options{
					AttackLeg:    leg,
					Attacks:      []runner.AttackRef{ref},
					MaxImpact:    ceiling,
					Acknowledged: []runner.AttackRef{ref},
				}, map[string]check.Run{ref.Name: stub})
				recs, err := drainStreamAsync(context.Background(), r)
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				wantRun := impact.Level() <= ceiling.Level()
				if ran.Load() != wantRun {
					t.Errorf("behavior ran = %v, want %v", ran.Load(), wantRun)
				}
				if (refusalOf(recs) != nil) == wantRun {
					t.Errorf("refusal present = %v, want %v", refusalOf(recs) != nil, !wantRun)
				}
			})
		}
	}
}

// TestPermanentRefusalZeroFrames proves a permanent (attack, mode) pair
// admitted by the ceiling but invoked without its acknowledgment refuses
// at the gate before any frame is emitted. The behavior is never invoked,
// the leg's Send is never called, a typed refusal record is emitted, and
// Run returns a coded non-zero error.
func TestPermanentRefusalZeroFrames(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	invoked := atomic.Bool{}
	stub := func(_ context.Context, _ check.Deps) (check.Verdict, error) {
		invoked.Store(true)
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
		MaxImpact: check.ImpactPermanent,
		// No Acknowledged: the gate must refuse.
	}, map[string]check.Run{"vtp": stub})

	recs, err := drainStreamAsync(context.Background(), r)

	if err == nil {
		t.Fatal("Run returned nil, want coded refusal error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodePermanentRefused {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodePermanentRefused)
	}
	if got := errs.ExitCode(err); got != 1 {
		t.Errorf("exit code: got %d, want 1", got)
	}
	if invoked.Load() {
		t.Error("behavior was invoked; the gate must refuse before invocation")
	}
	if got := leg.sendCount(); got != 0 {
		t.Errorf("leg Send called %d times, want 0 (zero frames)", got)
	}
	refusal := refusalOf(recs)
	if refusal == nil {
		t.Fatal("no refusal record in stream")
	}
	if !strings.Contains(refusal.Reason, "permanent") {
		t.Errorf("refusal reason %q, want it to name the permanent mode", refusal.Reason)
	}
	if !strings.Contains(refusal.Reason, "opt-in") {
		t.Errorf("refusal reason %q, want it to name the required opt-in", refusal.Reason)
	}
}

// TestPermanentRefusalNeverEnablesTX proves the harder invariant: the
// leg's TX capability is never handed to a refused behavior, not merely
// that no write call was made.
func TestPermanentRefusalNeverEnablesTX(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		if deps.AttackLeg != nil {
			_ = deps.AttackLeg.Send(context.Background(), []byte("must-not-send"))
		}
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vlanhop", Mode: "persist"}},
		MaxImpact: check.ImpactPermanent,
	}, map[string]check.Run{"vlanhop": stub})

	_, err := drainStreamAsync(context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want coded refusal error")
	}
	if got := leg.sendCount(); got != 0 {
		t.Errorf("leg Send called %d times, want 0", got)
	}
}

// TestAcknowledgedPermanentAnnouncesAndArmsNothing proves the acknowledged
// permanent path: the gate emits a run-start announcement naming the
// accepted mode before the first frame, and the behavior receives no
// teardown handle, since a permanent mode restores nothing.
func TestAcknowledgedPermanentAnnouncesAndArmsNothing(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	teardownWasNil := atomic.Bool{}
	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		teardownWasNil.Store(deps.Teardown == nil)
		deps.Emitter.Finding("vtp", []byte(`{"action":"wipe"}`))
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
		MaxImpact: check.ImpactPermanent,
		Acknowledged: []runner.AttackRef{
			{Name: "vtp", Mode: "wipe"},
		},
	}, map[string]check.Run{"vtp": stub})

	recs, err := drainStreamAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !teardownWasNil.Load() {
		t.Error("acknowledged permanent: Deps.Teardown is non-nil, want nil")
	}

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
}

// TestAcknowledgedPermanentIsPerPair proves the opt-in's per-pair semantics:
// acknowledging "vtp wipe" does not accept "vtp set".
func TestAcknowledgedPermanentIsPerPair(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, _ check.Deps) (check.Verdict, error) {
		t.Error("behavior should not be invoked")
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "set"}},
		MaxImpact: check.ImpactPermanent,
		Acknowledged: []runner.AttackRef{
			{Name: "vtp", Mode: "wipe"},
		},
	}, map[string]check.Run{"vtp": stub})

	_, err := drainStreamAsync(context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want refusal (ack is per-pair)")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodePermanentRefused {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodePermanentRefused)
	}
}

// TestDecayAnnouncesBoundBeforeExecute proves the decaying path: the gate
// announces the entry's bound as a run-start record before executing, as
// the duration when the probe states one and as unknown otherwise, and
// arms nothing.
func TestDecayAnnouncesBoundBeforeExecute(t *testing.T) {
	cases := []struct {
		name   string
		attack string
		detail string
	}{
		{"bound from the probe", "stproot", "decays within 6s"},
		{"no bound", "camflood", "decay bound unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			leg := &sendCountingLeg{}
			t.Cleanup(func() { _ = leg.Close() })

			teardownWasNil := atomic.Bool{}
			stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
				teardownWasNil.Store(deps.Teardown == nil)
				deps.Emitter.Finding("stp", []byte(`{"root":true}`))
				return pending, nil
			}

			r := newRunner(runner.Options{
				AttackLeg: leg,
				Attacks:   []runner.AttackRef{{Name: c.attack}},
			}, map[string]check.Run{c.attack: stub})

			recs, err := drainStreamAsync(context.Background(), r)
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if !teardownWasNil.Load() {
				t.Error("decaying entry's Deps.Teardown is non-nil; decay arms nothing")
			}
			if len(recs) < 2 || recs[0].Kind != findings.KindProgress || recs[0].Progress == nil {
				t.Fatalf("records = %+v, want an announcement first", recs)
			}
			if recs[0].Progress.Phase != "decay-bound" {
				t.Errorf("record 0 phase %q, want %q", recs[0].Progress.Phase, "decay-bound")
			}
			if recs[0].Progress.Detail != c.detail {
				t.Errorf("record 0 detail %q, want %q", recs[0].Progress.Detail, c.detail)
			}
		})
	}
}

// TestNoImpactNoAnnouncementNoTeardown proves the no-impact path: no
// announcement record and no teardown handle. The behavior runs directly
// and its verdict closes the attack.
func TestNoImpactNoAnnouncementNoTeardown(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	teardownWasNil := atomic.Bool{}
	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		teardownWasNil.Store(deps.Teardown == nil)
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.1"}`))
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
	}, map[string]check.Run{"arpsweep": stub})

	recs, err := drainStreamAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !teardownWasNil.Load() {
		t.Error("no-impact Deps.Teardown is non-nil")
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (finding + verdict, no announcement)", len(recs))
	}
	if recs[0].Kind != findings.KindFinding {
		t.Errorf("record 0: kind %q, want %q", recs[0].Kind, findings.KindFinding)
	}
}

// TestInterruptTeardownCompletesPartialFailure proves that when an
// interrupt arrives mid-run on an actively restored behavior, the
// teardown completes before exit, and one step planted to fail still lets
// later steps run, and the partial record names the failed step, and exit is 1.
func TestInterruptTeardownCompletesPartialFailure(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	var (
		stepOrder    []string
		stepOrderMu  sync.Mutex
		teardownDone atomic.Bool
		armed        = make(chan struct{})
	)

	stub := func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
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

		close(armed)
		<-ctx.Done()
		return check.Verdict{}, ctx.Err()
	}

	r := newRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		TeardownBudget: 2 * time.Second,
	}, map[string]check.Run{"arpspoof": stub})

	runCtx, runCancel := context.WithCancel(context.Background())
	go func() { _ = r.Run(runCtx) }()

	<-armed
	r.Interrupt()
	r.Wait()
	runCancel()

	if !teardownDone.Load() {
		t.Error("later teardown step did not run; one failing step must not abandon later ones")
	}

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
	if got := errs.ExitCode(r.Stream().Err()); got != 1 {
		t.Errorf("exit code: got %d, want 1", got)
	}
}

// TestTeardownCompletesBeforeStreamClose proves the ordering invariant:
// teardown completes before the sink flush, so the partial-failure record
// is among the drained records.
func TestTeardownCompletesBeforeStreamClose(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	armed := make(chan struct{})
	stub := func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Teardown.Arm("fails", func(_ context.Context) error {
			return errors.New("restore failed")
		})
		close(armed)
		<-ctx.Done()
		return check.Verdict{}, ctx.Err()
	}

	r := newRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		TeardownBudget: 2 * time.Second,
	}, map[string]check.Run{"arpspoof": stub})

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
	r.Interrupt()
	r.Wait()
	runCancel()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
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
// the configured budget.
func TestTeardownBudgetBounded(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Teardown.Arm("slow", func(stepCtx context.Context) error {
			<-stepCtx.Done()
			return stepCtx.Err()
		})
		deps.Emitter.Finding("arp", []byte(`{}`))
		return pending, nil
	}

	budget := 100 * time.Millisecond
	r := newRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		TeardownBudget: budget,
	}, map[string]check.Run{"arpspoof": stub})

	start := time.Now()
	_, err := drainStreamAsync(context.Background(), r)
	elapsed := time.Since(start)

	if elapsed > budget*5 {
		t.Errorf("teardown took %v, want <= %v (5x budget)", elapsed, budget*5)
	}
	if err == nil {
		t.Fatal("Run returned nil, want teardown partial failure (step was budget-capped)")
	}
}

// TestActiveRestoreZeroStepsArmedFails proves the loud-failure contract: an
// actively restored behavior that arms zero teardown steps is a coded
// runtime failure, not a silent skip.
func TestActiveRestoreZeroStepsArmedFails(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Emitter.Finding("arp", []byte(`{}`))
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpspoof"}},
	}, map[string]check.Run{"arpspoof": stub})

	_, err := drainStreamAsync(context.Background(), r)
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
// no permanent entry may dispatch even with an ack supplied and a
// permanent ceiling.
func TestOrchestratedPermanentCannotDispatch(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	invoked := atomic.Bool{}
	stub := func(_ context.Context, _ check.Deps) (check.Verdict, error) {
		invoked.Store(true)
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
		MaxImpact: check.ImpactPermanent,
		Acknowledged: []runner.AttackRef{
			{Name: "vtp", Mode: "wipe"},
		},
		Orchestrated: true,
	}, map[string]check.Run{"vtp": stub})

	_, err := drainStreamAsync(context.Background(), r)
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

// TestGateRefusalRecordFields pins the refusal record's fields: the
// attack, mode, and reason are set so a consumer can match on them.
func TestGateRefusalRecordFields(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "dtp", Mode: "keep-trunk"}},
		MaxImpact: check.ImpactPermanent,
	}, map[string]check.Run{"dtp": func(_ context.Context, _ check.Deps) (check.Verdict, error) { return pending, nil }})

	recs, err := drainStreamAsync(context.Background(), r)
	if err == nil {
		t.Fatal("Run returned nil, want refusal")
	}

	var seen bool
	for _, rec := range recs {
		if rec.Kind == findings.KindRefusal {
			seen = true
			if rec.Attack != "dtp" {
				t.Errorf("refusal attack %q, want %q", rec.Attack, "dtp")
			}
			if rec.Mode != "keep-trunk" {
				t.Errorf("refusal mode %q, want %q", rec.Mode, "keep-trunk")
			}
			if rec.Refusal == nil || rec.Refusal.Reason == "" {
				t.Error("refusal record carries no reason")
			}
		}
	}
	if !seen {
		t.Fatal("no refusal record")
	}
}

// TestTeardownStepOrderHostLocalFirst proves the reverse-dependency
// execution order: host-local state (armed first) restores before
// neighbor-cache repairs (armed later).
func TestTeardownStepOrderHostLocalFirst(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	var order []string
	var mu sync.Mutex

	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
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
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		TeardownBudget: 2 * time.Second,
	}, map[string]check.Run{"arpspoof": stub})

	_, err := drainStreamAsync(context.Background(), r)
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

	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Teardown.Arm("restore-a", func(_ context.Context) error { return nil })
		deps.Teardown.Arm("restore-b", func(_ context.Context) error { return nil })
		deps.Emitter.Finding("arp", []byte(`{}`))
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		TeardownBudget: 2 * time.Second,
	}, map[string]check.Run{"arpspoof": stub})

	recs, err := drainStreamAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	for _, rec := range recs {
		if rec.Kind == findings.KindError {
			t.Errorf("unexpected error record: %+v", rec.Error)
		}
	}
}

func TestCancellationPreservesTeardownContext(t *testing.T) {
	for _, source := range []string{"caller", "timeout", "consumer"} {
		t.Run(source, func(t *testing.T) {
			leg := newMockLeg()
			closeLeg(t, leg)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			armed := make(chan struct{})
			restored := false
			opts := runner.Options{
				AttackLeg:      leg,
				Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
				TeardownBudget: time.Second,
			}
			stubs := map[string]check.Run{
				"arpspoof": func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
					deps.Teardown.Arm("restore", func(ctx context.Context) error {
						if err := ctx.Err(); err != nil {
							return err
						}
						if _, ok := ctx.Deadline(); !ok {
							t.Error("teardown context has no deadline")
						}
						restored = true
						return nil
					})
					close(armed)
					<-ctx.Done()
					return check.Verdict{}, ctx.Err()
				},
			}
			wantErr := context.Canceled
			switch source {
			case "timeout":
				opts.Timeout = 50 * time.Millisecond
				wantErr = context.DeadlineExceeded
			case "consumer":
				wantErr = nil
			}
			r := newRunner(opts, stubs)
			result := make(chan error, 1)
			go func() { result <- r.Run(ctx) }()
			<-armed
			switch source {
			case "caller":
				cancel()
			case "consumer":
				if err := r.Stream().Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-result; err != wantErr {
				t.Errorf("Run error = %v, want %v", err, wantErr)
			}
			if !restored {
				t.Error("cancellation prevented restoration")
			}
		})
	}
}

// TestAbandonedStepsNamesTheRunningStep pins what a host reports on a
// forced exit: the armed steps that have not completed, read while a step
// is still running, and nil between behaviors.
func TestAbandonedStepsNamesTheRunningStep(t *testing.T) {
	leg := &sendCountingLeg{}
	t.Cleanup(func() { _ = leg.Close() })

	armed := make(chan struct{})
	stepStarted := make(chan struct{})
	release := make(chan struct{})
	stub := func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Teardown.Arm("done-first", func(context.Context) error { return nil })
		deps.Teardown.Arm("slow-restore", func(context.Context) error {
			close(stepStarted)
			<-release
			return nil
		})
		close(armed)
		<-ctx.Done()
		return check.Verdict{}, ctx.Err()
	}
	r := newRunner(runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		TeardownBudget: 5 * time.Second,
	}, map[string]check.Run{"arpspoof": stub})
	if got := r.AbandonedSteps(); got != nil {
		t.Errorf("AbandonedSteps before the run = %v, want nil", got)
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	go func() { _ = r.Run(runCtx) }()
	<-armed
	go r.Interrupt()
	<-stepStarted
	got := r.AbandonedSteps()
	close(release)
	r.Wait()
	for range r.Stream().Iter() {
	}
	if len(got) != 1 || got[0] != "slow-restore" {
		t.Errorf("AbandonedSteps during the slow step = %v, want [slow-restore]", got)
	}
}
