package runner_test

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// mockLeg is an in-memory [link.Leg] for testing the runner without an
// AF_PACKET socket. It satisfies the [link.Leg] interface.
type mockLeg struct {
	frames chan link.Frame
	closed atomic.Bool
}

func newMockLeg() *mockLeg {
	return &mockLeg{frames: make(chan link.Frame, 16)}
}

func (m *mockLeg) Send(_ context.Context, _ []byte) error {
	if m.closed.Load() {
		return errs.New().Code(link.ErrCodeLegOpen).Msg("leg is closed")
	}
	return nil
}

func (m *mockLeg) SetFilter(_ []link.RawInstruction) error { return nil }

func (m *mockLeg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-m.frames:
				if !ok {
					return
				}
				select {
				case out <- f:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (m *mockLeg) Addr() link.Addr { return link.Addr{} }

func (m *mockLeg) Close() error {
	m.closed.Store(true)
	close(m.frames)
	return nil
}

// closeLeg is a t.Cleanup helper that closes a mock leg and fails the test
// on error.
func closeLeg(t *testing.T, leg *mockLeg) {
	t.Helper()
	t.Cleanup(func() {
		if err := leg.Close(); err != nil {
			t.Errorf("leg close: %v", err)
		}
	})
}

// pending is the verdict every stub returns unless the test is about the
// verdict itself.
var pending = check.Pending("stub")

// stubCatalog returns the shipped catalog with the named behaviors' Run
// functions replaced by stubs, so a test drives the runner through real
// entries (their impact, restore, and legs) without sending a frame.
func stubCatalog(stubs map[string]check.Run) catalog.Catalog {
	bs := attacks.Behaviors()
	for i := range bs {
		if fn, ok := stubs[bs[i].Name]; ok {
			bs[i].Run = fn
		}
	}
	return catalog.MustNew(bs...)
}

// newRunner builds a runner over the stub catalog. A test that leaves
// MaxImpact empty gets the command's default ceiling, temporary, so the
// entries most tests drive are admitted.
func newRunner(opts runner.Options, stubs map[string]check.Run) *runner.Runner {
	opts.Catalog = stubCatalog(stubs)
	if opts.MaxImpact == "" {
		opts.MaxImpact = check.ImpactTemporary
	}
	return runner.NewRunner(opts)
}

// drainStream consumes all records from the stream until it ends, returning
// them in order. It is the in-memory consumer harness (the embedded-host
// shape with no CLI wiring).
func drainStream(s *runner.Stream) []findings.Record {
	var recs []findings.Record
	for rec := range s.Iter() {
		recs = append(recs, rec)
	}
	return recs
}

// drainStreamAsync starts Run in a goroutine and drains the stream
// concurrently, returning the records and the Run error.
func drainStreamAsync(ctx context.Context, r *runner.Runner) ([]findings.Record, error) {
	var (
		recs []findings.Record
		err  error
		wg   sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		recs = drainStream(r.Stream())
	}()

	err = r.Run(ctx)
	r.Wait()
	wg.Wait()
	return recs, err
}

// verdicts returns the verdict records of recs keyed by attack name.
func verdicts(recs []findings.Record) map[string]findings.Record {
	out := map[string]findings.Record{}
	for _, rec := range recs {
		if rec.Kind == findings.KindVerdict {
			out[rec.Attack] = rec
		}
	}
	return out
}

func TestStubBehaviorOrderedFindings(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.1"}`))
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.2"}`))
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.3"}`))
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

	if len(recs) != 4 {
		t.Fatalf("got %d records, want 4 (3 findings + the verdict)", len(recs))
	}
	wantHosts := []string{
		`{"host":"10.0.0.1"}`,
		`{"host":"10.0.0.2"}`,
		`{"host":"10.0.0.3"}`,
	}
	for i, rec := range recs[:3] {
		if rec.Kind != findings.KindFinding {
			t.Errorf("record %d: kind %q, want %q", i, rec.Kind, findings.KindFinding)
		}
		if rec.Attack != "arpsweep" {
			t.Errorf("record %d: attack %q, want %q", i, rec.Attack, "arpsweep")
		}
		if rec.Finding == nil {
			t.Fatalf("record %d: nil finding", i)
		}
		if rec.Finding.Module != "arp" {
			t.Errorf("record %d: module %q, want %q", i, rec.Finding.Module, "arp")
		}
		if got := string(rec.Finding.Detail); got != wantHosts[i] {
			t.Errorf("record %d: detail %q, want %q", i, got, wantHosts[i])
		}
	}
	if recs[3].Kind != findings.KindVerdict {
		t.Errorf("record 3: kind %q, want the verdict last", recs[3].Kind)
	}
}

// TestVerdictRecordPerAttack pins that the runner emits each behavior's
// verdict as the attack's last record, stamped with the attack and mode and
// carrying the observed window in milliseconds.
func TestVerdictRecordPerAttack(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "ghost"}, {Name: "portsteal", Mode: "relay"}},
		WatchLeg:  leg,
	}, map[string]check.Run{
		"ghost": func(_ context.Context, _ check.Deps) (check.Verdict, error) {
			return check.Resisted("0 frames in 3s", 3*time.Second), nil
		},
		"portsteal": func(_ context.Context, deps check.Deps) (check.Verdict, error) {
			deps.Teardown.Arm("ip-forward-restore", func(context.Context) error { return nil })
			return check.Confirmed("moved", 10*time.Millisecond), nil
		},
	})

	recs, err := drainStreamAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := verdicts(recs)
	ghost, ok := got["ghost"]
	if !ok || ghost.Verdict == nil {
		t.Fatalf("no verdict record for ghost in %+v", recs)
	}
	if ghost.Verdict.Kind != findings.VerdictResisted || ghost.Verdict.Detail != "0 frames in 3s" || ghost.Verdict.ObservedMS != 3000 {
		t.Errorf("ghost verdict = %+v, want resisted, \"0 frames in 3s\", 3000 ms", *ghost.Verdict)
	}
	steal, ok := got["portsteal"]
	if !ok || steal.Verdict == nil {
		t.Fatalf("no verdict record for portsteal in %+v", recs)
	}
	if steal.Mode != "relay" || steal.Verdict.Kind != findings.VerdictConfirmed || steal.Verdict.ObservedMS != 10 {
		t.Errorf("portsteal verdict = mode %q %+v, want mode relay, confirmed, 10 ms", steal.Mode, *steal.Verdict)
	}
}

func TestCloseStopsProducerAndIdempotent(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// A behavior that blocks forever (until ctx canceled).
	block := func(ctx context.Context, _ check.Deps) (check.Verdict, error) {
		<-ctx.Done()
		return check.Verdict{}, ctx.Err()
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks: []runner.AttackRef{
			{Name: "arpsweep"},
			{Name: "vlanenum"},
		},
	}, map[string]check.Run{
		"arpsweep": block,
		"vlanenum": block,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = r.Run(ctx) }()

	// Give the run a moment to start the first behavior.
	time.Sleep(20 * time.Millisecond)

	// Close the stream — the producer should stop within one poll cycle.
	start := time.Now()
	if err := r.Stream().Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Wait for Run to return.
	r.Wait()
	elapsed := time.Since(start)

	cancel()
	r.Wait()

	if elapsed > 500*time.Millisecond {
		t.Errorf("Close took %v, want < 500ms", elapsed)
	}

	// Second Close is idempotent (no panic, no error).
	if err := r.Stream().Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestEarlyLeaveNoGoroutineLeak(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// A behavior that emits a few findings then returns. A second
	// behavior that also emits. The consumer leaves after the first
	// record.
	emitted := atomic.Int32{}
	stub1 := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		for i := 0; i < 10; i++ {
			if !deps.Emitter.Finding("arp", []byte(`{}`)) {
				return pending, nil
			}
			emitted.Add(1)
		}
		return pending, nil
	}
	stub2 := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		deps.Emitter.Finding("arp", []byte(`{}`))
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks: []runner.AttackRef{
			{Name: "arpsweep"},
			{Name: "vlanenum"},
		},
	}, map[string]check.Run{
		"arpsweep": stub1,
		"vlanenum": stub2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = r.Run(ctx) }()

	// Consume one record then leave.
	consumed := 0
	for rec := range r.Stream().Iter() {
		consumed++
		_ = rec
		break
	}
	if consumed != 1 {
		t.Fatalf("consumed %d records, want 1", consumed)
	}

	// Wait for Run to finish. The Stopped check in the dispatch loop
	// prevents new behaviors from starting. The in-flight behavior
	// finishes when Emitter.Finding returns false.
	r.Wait()

	cancel()

	// Give goroutines a moment to exit.
	time.Sleep(50 * time.Millisecond)

	got := runtime.NumGoroutine()
	if got > 20 {
		t.Errorf("goroutine count %d after early leave, want <= 20 (no leak)", got)
	}
}

// TestBehaviorErrorIsPendingVerdictAndContinues pins the error path: a
// behavior that fails gets a pending verdict carrying its error text, no
// error record, and the behaviors after it still run.
func TestBehaviorErrorIsPendingVerdictAndContinues(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	behErr := errs.New().Code(runner.ErrCodeRunner).Msg("behavior exploded")

	callOrder := atomic.Int32{}
	stub1 := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		callOrder.Add(1)
		deps.Emitter.Finding("arp", []byte(`{"step":1}`))
		return pending, nil
	}
	stub2 := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		callOrder.Add(1)
		// arpspoof restores actively: arm a restore step so the
		// teardown gate does not fail it for arming zero steps.
		deps.Teardown.Arm("restore", func(_ context.Context) error { return nil })
		return check.Verdict{}, behErr
	}
	stub3 := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		callOrder.Add(1)
		deps.Emitter.Finding("arp", []byte(`{"step":3}`))
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks: []runner.AttackRef{
			{Name: "arpsweep"},
			{Name: "arpspoof"},
			{Name: "vlanenum"},
		},
	}, map[string]check.Run{
		"arpsweep": stub1,
		"arpspoof": stub2,
		"vlanenum": stub3,
	})

	recs, err := drainStreamAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if got := callOrder.Load(); got != 3 {
		t.Errorf("call order count %d, want 3", got)
	}
	for _, rec := range recs {
		if rec.Kind == findings.KindError {
			t.Errorf("unexpected error record %+v; a behavior error is a pending verdict", rec.Error)
		}
	}
	v, ok := verdicts(recs)["arpspoof"]
	if !ok || v.Verdict == nil {
		t.Fatalf("no verdict record for arpspoof in %+v", recs)
	}
	if v.Verdict.Kind != findings.VerdictPending || v.Verdict.Detail != "behavior exploded" {
		t.Errorf("arpspoof verdict = %+v, want pending with the error text", *v.Verdict)
	}
}

// TestMissingTargetIsSkippedVerdict pins the target error path: a behavior
// whose Need fails gets a skipped verdict naming the fields and sends
// nothing. On a single command Run returns the coded error so the command
// exits 2. Under orchestration the run continues to the next attack.
func TestMissingTargetIsSkippedVerdict(t *testing.T) {
	for _, orchestrated := range []bool{false, true} {
		name := "single command"
		if orchestrated {
			name = "orchestrated"
		}
		t.Run(name, func(t *testing.T) {
			leg := newMockLeg()
			closeLeg(t, leg)
			var sends atomic.Int32
			second := atomic.Bool{}
			r := newRunner(runner.Options{
				AttackLeg:    leg,
				Attacks:      []runner.AttackRef{{Name: "arpspoof"}, {Name: "arpsweep"}},
				Orchestrated: orchestrated,
			}, map[string]check.Run{
				"arpspoof": func(_ context.Context, deps check.Deps) (check.Verdict, error) {
					if err := deps.Target.Need(check.FieldVictim, check.FieldGateway); err != nil {
						return check.Verdict{}, err
					}
					sends.Add(1)
					return pending, nil
				},
				"arpsweep": func(context.Context, check.Deps) (check.Verdict, error) {
					second.Store(true)
					return pending, nil
				},
			})

			recs, err := drainStreamAsync(context.Background(), r)
			v, ok := verdicts(recs)["arpspoof"]
			if !ok || v.Verdict == nil || v.Verdict.Kind != findings.VerdictSkipped {
				t.Fatalf("arpspoof verdict = %+v, want skipped", v.Verdict)
			}
			for _, flag := range []string{"--victim <ip>,<mac>", "--gateway <ip>,<mac>"} {
				if !contains(v.Verdict.Detail, flag) {
					t.Errorf("verdict detail %q does not name %s", v.Verdict.Detail, flag)
				}
			}
			if sends.Load() != 0 {
				t.Errorf("behavior sent %d times after a missing target", sends.Load())
			}
			if orchestrated {
				if err != nil {
					t.Errorf("Run under orchestration = %v, want nil", err)
				}
				if !second.Load() {
					t.Error("the next attack did not run after a skipped one under orchestration")
				}
				return
			}
			if code, ok := errs.CodeOf(err); !ok || code != check.ErrCodeTargetMissing {
				t.Errorf("Run error = %v (code %q), want %q", err, code, check.ErrCodeTargetMissing)
			}
			if errs.ExitCode(err) != 2 {
				t.Errorf("exit code = %d, want 2", errs.ExitCode(err))
			}
			if second.Load() {
				t.Error("the next attack ran after a missing target on a single command")
			}
		})
	}
}

// TestInvalidTargetIsSkippedVerdict pins that a target field of the wrong
// form is handled as a missing one: the behavior's invalid-code error
// becomes a skipped verdict naming the flag and the form, nothing is sent,
// and the single command exits 2 with that code.
func TestInvalidTargetIsSkippedVerdict(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)
	var sends atomic.Int32
	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "hsrp"}},
		Target:    check.Target{Group: new(uint16(300)), VirtualIP: net.IPv4(10, 0, 0, 1)},
	}, map[string]check.Run{
		"hsrp": func(_ context.Context, deps check.Deps) (check.Verdict, error) {
			if err := deps.Target.Within(check.FieldGroup, 255); err != nil {
				return check.Verdict{}, err
			}
			sends.Add(1)
			return pending, nil
		},
	})

	recs, err := drainStreamAsync(context.Background(), r)
	v, ok := verdicts(recs)["hsrp"]
	if !ok || v.Verdict == nil || v.Verdict.Kind != findings.VerdictSkipped {
		t.Fatalf("hsrp verdict = %+v, want skipped", v.Verdict)
	}
	if want := "the check needs: --group at most 255"; v.Verdict.Detail != want {
		t.Errorf("verdict detail = %q, want %q", v.Verdict.Detail, want)
	}
	if sends.Load() != 0 {
		t.Errorf("behavior sent %d times after an invalid target", sends.Load())
	}
	if code, ok := errs.CodeOf(err); !ok || code != check.ErrCodeTargetInvalid {
		t.Errorf("Run error = %v (code %q), want %q", err, code, check.ErrCodeTargetInvalid)
	}
	if errs.ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2", errs.ExitCode(err))
	}
}

// TestInterruptAfterReturnKeepsTheVerdict pins that an interrupt landing
// as the behavior returns, whichever of the interrupt path and the
// dispatch loop takes the verdict, leaves exactly one verdict for that
// attack, across many iterations so the window between the loop taking
// the verdict and sending it is hit. The next attack, when the loop
// reaches it first, is interrupted in turn and gets at most one verdict.
func TestInterruptAfterReturnKeepsTheVerdict(t *testing.T) {
	for i := range 2000 {
		leg := newMockLeg()
		returned := make(chan struct{})
		r := newRunner(runner.Options{
			AttackLeg: leg,
			Attacks:   []runner.AttackRef{{Name: "arpsweep"}, {Name: "gratarp"}},
		}, map[string]check.Run{
			"arpsweep": func(_ context.Context, _ check.Deps) (check.Verdict, error) {
				close(returned)
				return pending, nil
			},
			"gratarp": func(ctx context.Context, _ check.Deps) (check.Verdict, error) {
				<-ctx.Done()
				return check.Verdict{}, ctx.Err()
			},
		})

		var (
			recs []findings.Record
			wg   sync.WaitGroup
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs = drainStream(r.Stream())
		}()
		result := make(chan error, 1)
		go func() { result <- r.Run(context.Background()) }()
		<-returned
		r.Interrupt()
		err := <-result
		r.Wait()
		wg.Wait()
		_ = leg.Close()

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: Run error = %v, want %v", i, err, context.Canceled)
		}
		count := map[string]int{}
		for _, rec := range recs {
			if rec.Kind == findings.KindVerdict {
				count[rec.Attack]++
			}
		}
		if count["arpsweep"] != 1 {
			t.Fatalf("iteration %d: %d verdict records for arpsweep, want 1", i, count["arpsweep"])
		}
		if count["gratarp"] > 1 {
			t.Fatalf("iteration %d: %d verdict records for gratarp, want at most 1", i, count["gratarp"])
		}
	}
}

// TestInterruptBeforeAnAttackEmitsNoRecordForIt pins that an interrupt
// that engaged before the dispatch loop reaches an attack stops the loop
// ahead of that attack's gate, so no announcement and no verdict names an
// attack that never ran. The interrupt lands before Run, where the stop
// context's cancellation reaches the run context on another goroutine,
// so only the loop's own check keeps the gate from running.
func TestInterruptBeforeAnAttackEmitsNoRecordForIt(t *testing.T) {
	for i := range 50 {
		leg := newMockLeg()
		r := newRunner(runner.Options{
			AttackLeg: leg,
			Attacks:   []runner.AttackRef{{Name: "gratarp"}},
		}, map[string]check.Run{
			"gratarp": func(_ context.Context, _ check.Deps) (check.Verdict, error) {
				return pending, nil
			},
		})
		r.Interrupt()
		recs, err := drainStreamAsync(context.Background(), r)
		r.Wait()
		_ = leg.Close()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: Run error = %v, want %v", i, err, context.Canceled)
		}
		for _, rec := range recs {
			if rec.Attack == "gratarp" {
				t.Fatalf("iteration %d: a %s record for gratarp after the interrupt", i, rec.Kind)
			}
		}
	}
}

// TestInterruptYieldsVerdictAndCanceled pins that an interrupted attack
// gets one verdict, pending with the detail "interrupted", and that Run
// returns context.Canceled on every iteration rather than by the
// scheduling of the stop.
func TestInterruptYieldsVerdictAndCanceled(t *testing.T) {
	for i := range 20 {
		leg := newMockLeg()
		started := make(chan struct{})
		r := newRunner(runner.Options{
			AttackLeg: leg,
			Attacks:   []runner.AttackRef{{Name: "arpsweep"}, {Name: "gratarp"}},
		}, map[string]check.Run{
			"arpsweep": func(ctx context.Context, _ check.Deps) (check.Verdict, error) {
				close(started)
				<-ctx.Done()
				return check.Verdict{}, ctx.Err()
			},
		})

		var (
			recs []findings.Record
			wg   sync.WaitGroup
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs = drainStream(r.Stream())
		}()
		result := make(chan error, 1)
		go func() { result <- r.Run(context.Background()) }()
		<-started
		r.Interrupt()
		err := <-result
		r.Wait()
		wg.Wait()
		_ = leg.Close()

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: Run error = %v, want %v", i, err, context.Canceled)
		}
		vs := verdicts(recs)
		v, ok := vs["arpsweep"]
		if !ok || v.Verdict == nil || v.Verdict.Kind != findings.VerdictPending || v.Verdict.Detail != "interrupted" {
			t.Fatalf("iteration %d: arpsweep verdict = %+v, want pending with detail interrupted", i, v.Verdict)
		}
		if _, ran := vs["gratarp"]; ran {
			t.Fatalf("iteration %d: the next attack got a verdict after the interrupt", i)
		}
		count := 0
		for _, rec := range recs {
			if rec.Kind == findings.KindVerdict {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("iteration %d: %d verdict records, want 1", i, count)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestWatchLegRequiredFastFailure(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	stub := func(_ context.Context, _ check.Deps) (check.Verdict, error) {
		t.Error("behavior should not be invoked")
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		// No WatchLeg set.
		Attacks: []runner.AttackRef{{Name: "ghost"}},
	}, map[string]check.Run{"ghost": stub})

	err := r.Run(context.Background())
	r.Wait()

	if err == nil {
		t.Fatal("Run returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodeWatchLegRequired {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodeWatchLegRequired)
	}
}

// TestDepsCarryObserveTargetAndListen pins what a behavior receives: the
// run's target, the observation window with its default, the entry it was
// resolved to, and a working listener seam.
func TestDepsCarryObserveTargetAndListen(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)
	target := check.Target{VTPDomain: "lab"}
	var got check.Deps
	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp"}},
		Target:    target,
	}, map[string]check.Run{"vtp": func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		got = deps
		return pending, nil
	}})
	if _, err := drainStreamAsync(context.Background(), r); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Target.VTPDomain != "lab" {
		t.Errorf("Deps.Target.VTPDomain = %q, want lab", got.Target.VTPDomain)
	}
	if got.Observe != runner.DefaultObserve {
		t.Errorf("Deps.Observe = %v, want the default %v", got.Observe, runner.DefaultObserve)
	}
	if got.Entry.Name != "vtp" || got.Entry.Restore.Kind != check.RestoreDecay {
		t.Errorf("Deps.Entry = %+v, want the vtp base row", got.Entry)
	}
	if got.Listen == nil {
		t.Fatal("Deps.Listen is nil")
	}
	ln, err := got.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local listen unavailable: %v", err)
	}
	if _, ok := ln.Addr().(*net.TCPAddr); !ok {
		t.Errorf("Listen returned %T address, want TCP", ln.Addr())
	}
	_ = ln.Close()
}

func TestStreamCloseLatency(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	stub := func(_ context.Context, _ check.Deps) (check.Verdict, error) {
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
	}, map[string]check.Run{"arpsweep": stub})

	go func() { _ = r.Run(context.Background()) }()

	r.Wait()

	start := time.Now()
	if err := r.Stream().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("Close took %v, want < 100ms", elapsed)
	}
}

func TestUnknownAttackCodedFailure(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "nonexistent"}},
	}, nil)

	err := r.Run(context.Background())
	r.Wait()

	if err == nil {
		t.Fatal("Run returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodeUnknownBehavior {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodeUnknownBehavior)
	}
}

// TestOrchestrationEntryCodedFailure pins that the one entry with no Run
// cannot be dispatched through the runner and fails with the unknown
// behavior code, not a nil call.
func TestOrchestrationEntryCodedFailure(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)
	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "scan"}},
	}, nil)
	err := r.Run(context.Background())
	r.Wait()
	if code, _ := errs.CodeOf(err); code != catalog.ErrCodeUnknownBehavior {
		t.Errorf("got %v, want unknown behavior error", err)
	}
	if code, _ := errs.CodeOf(r.Stream().Err()); code != catalog.ErrCodeUnknownBehavior {
		t.Errorf("got stream error %v, want unknown behavior error", r.Stream().Err())
	}
}

func TestNilAttackLegCodedFailure(t *testing.T) {
	r := newRunner(runner.Options{
		AttackLeg: nil,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
	}, map[string]check.Run{
		"arpsweep": func(_ context.Context, _ check.Deps) (check.Verdict, error) { return pending, nil },
	})

	err := r.Run(context.Background())
	r.Wait()

	if err == nil {
		t.Fatal("Run returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != runner.ErrCodeRunner {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, runner.ErrCodeRunner)
	}
}

func TestEmptyAttacksReturnsNil(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   nil,
	}, nil)

	err := r.Run(context.Background())
	r.Wait()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestContextCancellationReturnsCtxErr(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	started := make(chan struct{})
	block := func(ctx context.Context, _ check.Deps) (check.Verdict, error) {
		close(started)
		<-ctx.Done()
		return check.Verdict{}, ctx.Err()
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
	}, map[string]check.Run{"arpsweep": block})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	result := make(chan error, 1)
	go func() { result <- r.Run(ctx) }()

	<-started
	cancel()

	if err := <-result; err != context.Canceled {
		t.Errorf("Run error = %v, want context.Canceled", err)
	}
	if err := r.Stream().Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("stream error = %v, want context.Canceled", err)
	}
}

func TestCancellationUnblocksFullStream(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "caller"
		if timeout {
			name = "timeout"
		}
		t.Run(name, func(t *testing.T) {
			leg := newMockLeg()
			closeLeg(t, leg)
			full := make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			opts := runner.Options{
				AttackLeg: leg,
				Attacks:   []runner.AttackRef{{Name: "arpsweep"}, {Name: "vlanenum"}},
			}
			stubs := map[string]check.Run{
				"arpsweep": func(_ context.Context, deps check.Deps) (check.Verdict, error) {
					for range 64 {
						deps.Emitter.Progress("scan", "")
					}
					close(full)
					deps.Emitter.Progress("blocked", "")
					return pending, nil
				},
				"vlanenum": func(_ context.Context, _ check.Deps) (check.Verdict, error) {
					t.Error("dispatched another behavior after cancellation")
					return pending, nil
				},
			}
			wantErr := context.Canceled
			if timeout {
				opts.Timeout = 50 * time.Millisecond
				wantErr = context.DeadlineExceeded
			}
			r := newRunner(opts, stubs)
			result := make(chan error, 1)
			go func() { result <- r.Run(ctx) }()
			<-full
			if !timeout {
				cancel()
			}
			select {
			case err := <-result:
				if err != wantErr {
					t.Errorf("Run error = %v, want %v", err, wantErr)
				}
				if err := r.Stream().Err(); err != wantErr {
					t.Errorf("stream error = %v, want %v", err, wantErr)
				}
			case <-time.After(time.Second):
				if err := r.Stream().Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
				<-result
				t.Fatal("Run remained blocked on a full stream after cancellation")
			}
		})
	}
}

func TestModeBearingBehaviorResolvesEntry(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	var gotEntry check.Entry
	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		gotEntry = deps.Entry
		// portsteal --relay restores actively: arm a restore step.
		deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error { return nil })
		deps.Emitter.Finding("arp", []byte(`{}`))
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "portsteal", Mode: "relay"}},
	}, map[string]check.Run{"portsteal": stub})

	recs, err := drainStreamAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if gotEntry.Name != "portsteal" {
		t.Errorf("entry name %q, want %q", gotEntry.Name, "portsteal")
	}
	if gotEntry.Mode != "relay" {
		t.Errorf("entry mode %q, want %q", gotEntry.Mode, "relay")
	}
	if gotEntry.Restore.Kind != check.RestoreActive {
		t.Errorf("entry restore %q, want %q", gotEntry.Restore.Kind, check.RestoreActive)
	}

	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (finding + verdict)", len(recs))
	}
	for i, rec := range recs {
		if rec.Mode != "relay" {
			t.Errorf("record %d mode %q, want %q", i, rec.Mode, "relay")
		}
	}
}

func TestBackpressureBoundedChannel(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	emitted := atomic.Int32{}
	stub := func(_ context.Context, deps check.Deps) (check.Verdict, error) {
		for i := 0; i < 1000; i++ {
			if !deps.Emitter.Finding("arp", []byte(`{}`)) {
				return pending, nil
			}
			emitted.Add(1)
		}
		return pending, nil
	}

	r := newRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
	}, map[string]check.Run{"arpsweep": stub})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = r.Run(ctx) }()

	consumed := 0
	for rec := range r.Stream().Iter() {
		_ = rec
		consumed++
		time.Sleep(1 * time.Millisecond)
		if consumed >= 10 {
			break
		}
	}

	if err := r.Stream().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r.Wait()

	got := emitted.Load()
	if got > 200 {
		t.Errorf("emitted %d records, want <= 200 (backpressure)", got)
	}
}
