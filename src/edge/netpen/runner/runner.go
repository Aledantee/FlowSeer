package runner

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// ErrCodeRunner is the wire identity for a runner-level failure that does
// not map to a more specific code (e.g. a nil attack leg). Dispatch-level
// failures with a specific identity (unknown behavior, watch-leg required)
// carry their own codes from the catalog package.
var ErrCodeRunner = errs.NewCode("netpen/runner")

// errNoAttackLeg is returned by Run when Options.AttackLeg is nil and
// Attacks is non-empty. It carries ErrCodeRunner.
var errNoAttackLeg = errs.New().
	Code(ErrCodeRunner).
	UserMsg("an attack leg is required").
	Hint("pass -i <iface> (default eth0)").
	Msg("attack leg is nil")

// Runner is the embeddable run engine. It resolves each [Options.Attacks]
// entry through the host's catalog (unknown name = usage-shaped coded
// error), gates it on the impact ceiling, the acknowledgments, and the
// legs, executes each behavior under a per-attack context derived from
// Run's ctx (zgrab2's cancellation pattern), and emits the behavior's
// verdict as a record after it returns, so a behavior cannot forget to
// decide.
//
// Error semantics: a behavior erroring mid-run surfaces as a pending
// verdict carrying the error text and the run of remaining behaviors
// continues. A behavior whose target lacks a field it needs is a skipped
// verdict. Outside orchestration Run then returns that coded error so a
// single command exits 2. A ceiling refusal is a refusal record and a
// skipped verdict, and the run continues. Dispatch failures, a permanent
// entry without its acknowledgment, and context cancellation stop the run.
// Teardown failures are also returned. Cancellation returns the run
// context's error unwrapped unless teardown failed.
//
// Construct a Runner with [NewRunner]. The zero value is not usable.
//
// One goroutine calls Run while one consumer reads [Runner.Stream].
// Interrupt and Wait may be called concurrently. Run must be called exactly once.
type Runner struct {
	opts   Options
	stream *Stream

	// stopCtx and stopCancel form a persistent stop signal created at
	// construction. [Stream.Close] calls stopCancel so the dispatch
	// loop and any in-flight behavior observe cancellation and exit
	// promptly. Unlike runCancel (which is derived in Run), stopCancel
	// is available before Run starts, so Close can be called at any
	// time without racing the Run setup.
	stopCtx    context.Context
	stopCancel context.CancelFunc

	// closedByConsumer is set when [Stream.Close] cancels the run,
	// so Run returns nil rather than context.Canceled.
	closedByConsumer atomic.Bool

	// teardownMu guards activeTeardown and activeCancel, the
	// currently-armed teardown and its per-attack cancel, so the
	// signal [Runner.Interrupt] path can run the same teardown the
	// completion path runs (completion and interrupt share one entry
	// point).
	teardownMu     sync.Mutex
	activeTeardown *Teardown
	activeCancel   context.CancelFunc
	// activeName, activeMode, and inFlight name the attack whose behavior
	// is running, so the interrupt path can send its verdict. They are
	// guarded by teardownMu.
	activeName string
	activeMode string
	inFlight   bool
	// interrupted is set by [Runner.Interrupt] under teardownMu. Whichever
	// of the interrupt path and the dispatch loop takes the lock first
	// owns the in-flight attack's verdict, the dispatch loop starts no
	// further attack once it is set, and Run returns context.Canceled.
	interrupted bool

	// interruptErr holds the teardown error from the signal
	// [Runner.Interrupt] path, so the dispatch loop can surface it as
	// Run's return value. The dispatch loop's own runTeardown call is a
	// no-op when Interrupt ran first (runOnce), so without this the
	// teardown-partial error would be lost.
	interruptErrMu sync.Mutex // guards interruptErr
	interruptErr   error

	// interruptOnce guards the interrupt entry point so a second
	// signal during teardown does not re-enter [Runner.Interrupt].
	interruptOnce sync.Once
	interruptDone chan struct{}

	// runDone closes when the dispatch loop (Run) returns, so a host
	// that starts Run in a goroutine can synchronize on completion via
	// [Runner.Wait].
	runDone chan struct{}
}

// NewRunner constructs a Runner from opts. The findings stream is created
// here, so a host can obtain [Runner.Stream] and begin iterating before Run
// is invoked. The producer (the dispatch loop) starts when [Runner.Run] is
// called.
//
// A zero Options is accepted but Run rejects a nil AttackLeg with a coded
// error when Attacks is non-empty. The host is expected to validate
// domain-level constraints (e.g. a named watch leg exists) before
// constructing the Runner. The runner enforces only what it can check
// itself, which is the gates the catalog entries declare.
func NewRunner(opts Options) *Runner {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	r := &Runner{
		opts:          opts,
		stream:        newStream(context.Background()),
		interruptDone: make(chan struct{}),
		stopCtx:       stopCtx,
		stopCancel:    stopCancel,
		runDone:       make(chan struct{}),
	}
	r.stream.closeHook = r.close
	return r
}

// close is the [Stream.Close] hook: it marks the shutdown as consumer-
// initiated and cancels the stop context so Run and any in-flight behavior
// observe cancellation.
func (r *Runner) close() {
	r.closedByConsumer.Store(true)
	r.stopCancel()
}

// Stream returns the findings stream. It is available before [Runner.Run]
// is called so a host can wire its consumer (a range loop, a JSONL writer,
// or a TUI feed) before the run starts. The same stream is returned on
// every call.
func (r *Runner) Stream() *Stream {
	return r.stream
}

// StopContext returns the persistent stop context, done once the run is
// stopping. A host's signal handler supervises its goroutines under it.
func (r *Runner) StopContext() context.Context {
	return r.stopCtx
}

// Run resolves and executes each attack in [Options.Attacks] order. It
// blocks until all behaviors have run or the context is canceled. The
// findings stream's producer is this method's dispatch loop: Run drives
// send, and the stream's data channel is closed when Run returns.
//
// Run returns nil on successful completion (including when behaviors
// returned verdicts other than confirmed). It returns a non-nil error for
// dispatch failures, teardown failures, a missing or invalid target outside
// orchestration, or cancellation. Cancellation returns the run context's
// error unwrapped unless teardown failed, and a run that [Runner.Interrupt]
// stopped returns context.Canceled, whatever the scheduling of the stop.
// Closing the stream requests a graceful stop. Teardown runs with its own
// budget even when the run context is canceled.
func (r *Runner) Run(ctx context.Context) (result error) {
	defer close(r.runDone)
	defer r.stream.done()
	defer r.stream.pump.Cancel()

	if len(r.opts.Attacks) == 0 {
		return nil
	}

	if r.opts.AttackLeg == nil {
		r.stream.fail(errNoAttackLeg)
		return errNoAttackLeg
	}

	// Derive runCtx from the caller's ctx and the persistent stop
	// context. When the consumer closes the stream ([Stream.Close]),
	// stopCancel fires, canceling runCtx and any in-flight behavior's
	// per-attack context. closedByConsumer distinguishes a consumer
	// close (returns nil) from a caller-ctx cancellation (returns
	// ctx.Err() unwrapped).
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	spawn.Go(runCtx, "netpen runner stop watchdog", func() {
		select {
		case <-r.stopCtx.Done():
			runCancel()
		case <-r.runDone:
		}
	})
	if r.opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, r.opts.Timeout)
		defer cancel()
	}

	// The stream exists before Run receives its context. Connect cancellation
	// here so a stalled consumer cannot keep a send blocked past the deadline.
	stopStreamCancel := context.AfterFunc(runCtx, r.stream.pump.Cancel)
	defer stopStreamCancel()
	defer func() {
		if result != nil || r.closedByConsumer.Load() {
			return
		}
		if r.interruptEngaged() {
			result = context.Canceled
		} else if err := runCtx.Err(); err != nil {
			result = err
		}
		if result != nil {
			r.stream.fail(result)
		}
	}()

	// Build the opt-in acknowledgment set: per (name, mode), so
	// accepting "vtp wipe" does not accept "vtp set".
	ack := make(map[string]bool, len(r.opts.Acknowledged))
	for _, a := range r.opts.Acknowledged {
		ack[entryKey(a.Name, a.Mode)] = true
	}

	ceiling := r.opts.MaxImpact
	if ceiling == "" {
		ceiling = check.ImpactNone
	}
	observe := r.opts.Observe
	if observe <= 0 {
		observe = DefaultObserve
	}
	attackLeg := r.opts.AttackLeg
	if r.opts.Rate > 0 {
		attackLeg = newPacedLeg(attackLeg, r.opts.Rate)
	}

	// teardownBudget is the total time the executor may spend. Tests
	// scale it down via [Options.TeardownBudget].
	teardownBudget := r.opts.TeardownBudget

	var runErr error // first dispatch-level or teardown-partial failure

	for _, ref := range r.opts.Attacks {
		// A consumer-initiated [Stream.Close] cancels runCtx (via
		// closeHook) and sets closedByConsumer. The dispatch loop
		// returns nil — a consumer close is graceful, not a run
		// failure. A caller-ctx cancellation (closedByConsumer is
		// false) returns ctx.Err() unwrapped.
		select {
		case <-r.stream.pump.Stopped():
			// The stream was stopped. Surface the error the interrupt
			// path set, if any (a teardown partial failure). A plain
			// consumer close returns nil.
			if err := r.interruptError(); err != nil {
				r.stream.fail(err)
				return err
			}
			return nil
		default:
		}
		// If the interrupt path set a teardown-partial error, surface
		// it before the context-cancellation check: the interrupt
		// cancels the run context, so runCtx.Err() is also set, but
		// the teardown-partial error is the actionable failure.
		if err := r.interruptError(); err != nil {
			r.stream.fail(err)
			return err
		}
		if err := runCtx.Err(); err != nil {
			if r.closedByConsumer.Load() {
				return nil
			}
			r.stream.fail(err)
			return err
		}

		entry, run, ok := r.opts.Catalog.Lookup(ref.Name, ref.Mode)
		if !ok {
			err := errs.New().
				Code(catalog.ErrCodeUnknownBehavior).
				Attr("name", ref.Name).
				Attr("mode", ref.Mode).
				UserMsg(fmt.Sprintf("unknown attack %q", ref.Name)).
				Hint("run 'netpen help' for the list of attacks").
				Msgf("unknown behavior %q (mode %q)", ref.Name, ref.Mode)
			r.stream.fail(err)
			return err
		}
		if run == nil {
			err := errs.New().
				Code(catalog.ErrCodeUnknownBehavior).
				Attr("name", ref.Name).
				UserMsg(fmt.Sprintf("%q is an orchestration entry and runs through its own command", ref.Name)).
				Hint("run 'netpen "+ref.Name+"' directly").
				Msgf("orchestration entry %q has no run function", ref.Name)
			r.stream.fail(err)
			return err
		}

		// Stamp the current attack identity (name and mode) for
		// record emission.
		r.stream.setAttack(ref.Name, entry.Mode)

		// The gate consults the entry BEFORE the leg's TX is ever handed
		// to the behavior: a refused behavior never receives a
		// send-capable leg, so zero frames are possible.
		dec := gate(r.stream, entry, ceiling, ack, r.opts.Orchestrated, r.opts.WatchLeg != nil)
		if !dec.proceed {
			if !dec.stop {
				// A ceiling refusal. The refusal record is already out.
				// Count it with a skipped verdict and move on to the
				// next attack.
				if !r.stream.send(verdictRecord(ref.Name, entry.Mode, check.Verdict{Kind: findings.VerdictSkipped, Detail: dec.announce.Refusal.Reason})) {
					return r.terminated(runCtx)
				}
				continue
			}
			err := refusalError(entry, dec.announce.Refusal.Reason)
			r.stream.fail(err)
			return err
		}

		// Emit the run-start announcement (if any) before the first
		// frame: a decaying entry's bound, or the acknowledged
		// permanent entry's accepted mode.
		if dec.announce != nil {
			if !r.stream.send(*dec.announce) {
				return r.terminated(runCtx)
			}
		}

		// Derive a per-attack context (zgrab2's cancellation pattern):
		// a behavior runs under a child of runCtx so canceling the
		// attack (interrupt) does not abort siblings' contexts
		// directly. The teardown path cancels this to unblock the
		// behavior.
		attackCtx, cancel := context.WithCancel(runCtx)

		// Register the active teardown + cancel so [Runner.Interrupt]
		// (the signal path) can engage the same teardown the
		// completion path runs (completion and interrupt share one
		// entry point). An interrupt that already engaged starts no
		// further attack, since its verdict would have no sender.
		r.teardownMu.Lock()
		if r.interrupted {
			r.teardownMu.Unlock()
			cancel()
			<-r.interruptDone
			break
		}
		r.activeTeardown = dec.teardown
		r.activeCancel = cancel
		r.activeName, r.activeMode, r.inFlight = ref.Name, entry.Mode, true
		r.teardownMu.Unlock()

		deps := check.Deps{
			AttackLeg: attackLeg,
			WatchLeg:  r.opts.WatchLeg,
			Target:    r.opts.Target,
			Entry:     entry,
			Observe:   observe,
			Emitter:   &emitter{stream: r.stream},
			Listen:    listen,
		}
		// A nil *Teardown must not become a non-nil interface holding a
		// nil pointer, which a behavior's nil check would pass.
		if dec.teardown != nil {
			deps.Teardown = dec.teardown
		}

		verdict, behErr := run(attackCtx, deps)
		cancel()

		// A behavior that stopped on its target check sent nothing and
		// armed nothing, so there is no restore to run or to miss.
		skipped := check.IsTargetError(behErr)

		// Run the armed teardown (if any) on completion or interrupt.
		// The teardown executes the armed steps in reverse-dependency
		// order, each in its own error scope, bounded by the budget.
		// An actively restored behavior that armed zero steps is a
		// coded runtime failure (a required restore that restores
		// nothing is a bug, not a silent skip).
		if dec.teardown != nil && !skipped {
			tdErr := r.runTeardown(runCtx, dec.teardown, teardownBudget)
			if tdErr != nil {
				// Surface the named partial-failure record through
				// the stream BEFORE flushing (teardown completes
				// before sink flush — asserted in tests).
				rec := dec.teardown.partialRecord()
				_ = r.stream.send(rec)
				if runErr == nil {
					runErr = tdErr
				}
			}
		}

		// Clear the active teardown now that completion has run it. An
		// interrupt that took the lock first owns this attack's verdict:
		// wait for it to publish its result, then stop.
		r.teardownMu.Lock()
		interrupted := r.interrupted
		r.activeTeardown = nil
		r.activeCancel = nil
		r.inFlight = false
		r.teardownMu.Unlock()
		if interrupted {
			<-r.interruptDone
			break
		}

		if runCtx.Err() != nil {
			break
		}

		if behErr != nil {
			verdict = check.Verdict{Kind: findings.VerdictPending, Detail: behErr.Error()}
			if skipped {
				verdict.Kind = findings.VerdictSkipped
			}
		}

		// The runner emits the verdict itself so a behavior cannot
		// forget to decide.
		if !r.stream.send(verdictRecord(ref.Name, entry.Mode, verdict)) {
			return r.terminated(runCtx)
		}
		if verdict.Kind == findings.VerdictSkipped && !r.opts.Orchestrated {
			// Outside orchestration a missing or invalid target is the
			// operator's mistake on this one command, so the run stops
			// with the coded error and the command exits 2.
			r.stream.fail(behErr)
			return behErr
		}
	}

	// The interrupt path may have set a teardown-partial error that
	// the completion path's runTeardown no-op did not capture.
	if err := r.interruptError(); err != nil {
		r.stream.fail(err)
		return err
	}

	if runErr != nil {
		r.stream.fail(runErr)
		return runErr
	}

	return nil
}

// terminated is Run's return once the stream refused a send: a consumer
// close returns nil, a canceled run context returns its error, and a
// stream that stopped for any other reason returns nil.
func (r *Runner) terminated(runCtx context.Context) error {
	if r.closedByConsumer.Load() {
		return nil
	}
	if err := runCtx.Err(); err != nil {
		r.stream.fail(err)
		return err
	}
	return nil
}

// refusalError is the coded error Run returns when the gate refused an
// entry the operator named: a permanent mode without its acknowledgment
// or under orchestration, or a watch-required behavior without a watch
// leg.
func refusalError(entry check.Entry, reason string) error {
	if entry.Legs == check.WatchRequired && reason == "requires a watch leg; none attached" {
		return errs.New().
			Code(catalog.ErrCodeWatchLegRequired).
			Attr("name", entry.Name).
			UserMsg(fmt.Sprintf("%q requires a watch leg", entry.Name)).
			Hint("pass -w <iface> to attach a watch leg").
			Msgf("behavior %q requires a watch leg; none attached", entry.Name)
	}
	return errs.New().
		Code(catalog.ErrCodePermanentRefused).
		Attr("name", entry.Name).
		Attr("mode", entry.Mode).
		ExitCode(1).
		UserMsg(fmt.Sprintf("%q mode %q refused", entry.Name, entry.Mode)).
		Hint("supply the per-run opt-in acknowledgment for this permanent mode").
		Msgf("permanent %q (mode %q) refused: %s", entry.Name, entry.Mode, reason)
}

// verdictRecord builds the [findings.KindVerdict] record for one attack.
func verdictRecord(name, mode string, v check.Verdict) findings.Record {
	r := findings.NewRecord(findings.KindVerdict)
	r.Time = time.Now()
	r.Attack = name
	r.Mode = mode
	r.Verdict = &findings.Verdict{Kind: v.Kind, Detail: v.Detail, ObservedMS: v.Observed.Milliseconds()}
	return r
}

// listen is the production [check.Deps.Listen]: the net package's own
// listener.
func listen(ctx context.Context, network, address string) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(ctx, network, address)
}

// runTeardown executes the armed teardown steps and returns the partial-
// failure error when one or more steps failed. It is the
// single entry point shared by completion and interrupt: the dispatch
// loop calls it after the behavior returns, and [Runner.Interrupt]
// (the signal path) calls it when a signal arrives mid-behavior. The
// [Teardown.Run] guard makes a second call a no-op, so completion-after-
// interrupt does not double-execute.
func (r *Runner) runTeardown(ctx context.Context, t *Teardown, budget time.Duration) error {
	// An actively restored behavior that armed zero steps is a coded
	// runtime failure: a required restore that restores nothing is a
	// bug, not a silent skip.
	if !t.armed() {
		err := errs.New().
			Code(catalog.ErrCodeTeardownPartial).
			Attr("name", t.entry.Name).
			Attr("mode", t.entry.Mode).
			ExitCode(1).
			UserMsg(fmt.Sprintf("%q armed no teardown steps", t.entry.Name)).
			Hint("this is a netpen internal error; report it").
			Msgf("actively restored %q armed zero teardown steps", t.entry.Name)
		t.failed = []string{"<no-steps-armed>"}
		return err
	}
	// Restoration must survive the cancellation that stopped the behavior.
	// Teardown.Run applies a fresh deadline to each step.
	return t.Run(context.WithoutCancel(ctx), budget)
}

// Interrupt engages the teardown path from a signal (SIGINT, SIGTERM, or
// SIGHUP). It cancels the in-flight behavior's per-attack context
// (so the behavior returns promptly), runs the currently-armed
// teardown through the same entry point completion uses, sends the
// in-flight attack's verdict, pending with the detail "interrupted", and
// then cancels the run context so the dispatch loop exits. Between
// behaviors there is no teardown to run and no verdict to send.
//
// Interrupt is idempotent: a second call (e.g. completion after the
// signal already engaged teardown) is a no-op. It is safe to call from
// any goroutine. A host that force-exits on a second signal reads
// [Runner.AbandonedSteps] first.
func (r *Runner) Interrupt() {
	r.interruptOnce.Do(func() {
		defer close(r.interruptDone)

		r.teardownMu.Lock()
		r.interrupted = true
		td := r.activeTeardown
		ac := r.activeCancel
		name, mode, inFlight := r.activeName, r.activeMode, r.inFlight
		r.teardownMu.Unlock()

		// Cancel the in-flight behavior so it returns promptly.
		if ac != nil {
			ac()
		}

		if td != nil {
			// Run the teardown under the budget from Options, the same
			// one the completion path uses. The
			// Teardown.Run guard makes the completion-path call a
			// no-op, so completion and interrupt share one entry
			// point without double-execution.
			budget := r.opts.TeardownBudget
			tdErr := r.runTeardown(r.stopCtx, td, budget)

			// If teardown failed, emit the named partial-failure
			// record through the stream so the consumer sees it
			// before the stream closes (teardown completes before
			// sink flush). The completion path's Run call is
			// a no-op (runOnce), so only this path emits the record.
			if tdErr != nil {
				// Store the error for the dispatch loop to
				// surface as Run's return value. Do NOT call
				// stream.fail here, because that would stop the
				// stream and make the dispatch loop treat the
				// shutdown as a consumer close (returning nil).
				rec := td.partialRecord()
				_ = r.stream.send(rec)
				r.interruptErrMu.Lock()
				r.interruptErr = tdErr
				r.interruptErrMu.Unlock()
			}
		}

		// The attack in flight gets its verdict here, before the stop
		// context cancels the pump, since the dispatch loop leaves it to
		// this path.
		if inFlight {
			_ = r.stream.send(verdictRecord(name, mode, check.Verdict{Kind: findings.VerdictPending, Detail: "interrupted"}))
		}

		// Cancel the run context so the dispatch loop exits and does
		// not start the next behavior.
		r.stopCancel()
	})
}

// interruptEngaged reports whether [Runner.Interrupt] has taken the run.
func (r *Runner) interruptEngaged() bool {
	r.teardownMu.Lock()
	defer r.teardownMu.Unlock()
	return r.interrupted
}

// AbandonedSteps returns the names of the teardown steps that have not
// completed when a host force-exits during teardown: the armed steps minus
// the ones that already finished (success or failure). A step still
// running or never reached is abandoned. It is nil between behaviors.
func (r *Runner) AbandonedSteps() []string {
	r.teardownMu.Lock()
	td := r.activeTeardown
	r.teardownMu.Unlock()
	if td == nil {
		return nil
	}
	return td.abandonedSnapshot()
}

// interruptError returns the teardown-partial error the interrupt path
// stored, if any, so the dispatch loop can surface it as Run's return
// value. nil when the interrupt path ran no teardown or the teardown
// succeeded.
func (r *Runner) interruptError() error {
	r.interruptErrMu.Lock()
	defer r.interruptErrMu.Unlock()
	return r.interruptErr
}

// Wait blocks until the dispatch loop has finished. It is a convenience for
// hosts that start Run in a goroutine and need to synchronize on completion.
func (r *Runner) Wait() {
	<-r.runDone
}

// entryKey builds the map key for a (name, mode) pair. The NUL byte separator
// prevents collisions between (name="a", mode="bc") and (name="ab",
// mode="c") — neither name nor mode may contain a NUL.
func entryKey(name, mode string) string {
	return name + "\x00" + mode
}

var _ link.Leg = (*pacedLeg)(nil)
