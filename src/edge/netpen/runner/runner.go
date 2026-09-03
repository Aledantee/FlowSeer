package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
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
// entry through the [catalog] (unknown name = usage-shaped coded error),
// enforces leg preconditions uniformly (a behavior requiring a watch leg
// when none is attached fails fast before invocation with a coded error),
// and executes each behavior under a per-attack context derived from
// Run's ctx (zgrab2's cancellation pattern).
//
// Error semantics: a behavior erroring mid-run surfaces as a typed
// [findings.ErrorRecord] through the stream and the run of remaining
// behaviors continues. Dispatch failures and context cancellation stop the
// run. Teardown failures are also returned. Cancellation returns the run
// context's error unwrapped unless teardown failed.
//
// The zero value is not usable; construct a Runner via [NewRunner].
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

	// interruptErr holds the teardown error from the signal
	// [Runner.Interrupt] path, so the dispatch loop can surface it as
	// Run's return value. The dispatch loop's own runTeardown call is a
	// no-op when Interrupt ran first (runOnce), so without this the
	// teardown-partial error would be lost.
	interruptErrMu sync.Mutex
	interruptErr   error

	// interruptOnce guards the interrupt entry point so a second
	// signal during teardown does not re-enter [Runner.Interrupt].
	interruptOnce sync.Once

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
// constructing the Runner; the runner enforces only what it can check
// itself (leg preconditions declared in the catalog).
func NewRunner(opts Options) *Runner {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	r := &Runner{
		opts:       opts,
		stream:     newStream(context.Background()),
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
		runDone:    make(chan struct{}),
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

// Run resolves and executes each attack in [Options.Attacks] order. It
// blocks until all behaviors have run or the context is canceled. The
// findings stream's producer is this method's dispatch loop: Run drives
// send, and the stream's data channel is closed when Run returns.
//
// Run returns nil on successful completion (including when behaviors
// emitted error records — those are findings, not run failures). It returns
// a non-nil error for dispatch failures, teardown failures, or cancellation.
// Cancellation returns the run context's error unwrapped unless teardown
// failed. Closing the stream requests a graceful stop. Teardown runs with
// its own budget even when the run context is canceled.
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
	go func() {
		select {
		case <-r.stopCtx.Done():
			runCancel()
		case <-r.runDone:
		}
	}()
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
		if result == nil && !r.closedByConsumer.Load() && runCtx.Err() != nil {
			result = runCtx.Err()
			r.stream.fail(result)
		}
	}()

	// Build the entry lookup once: a map from (name, mode) to catalog
	// entry. The catalog is the single source for dispatch metadata.
	entries := catalog.Entries()
	lookup := make(map[string]catalog.Entry, len(entries))
	for _, e := range entries {
		lookup[entryKey(e.Name, e.Mode)] = e
	}

	// Build the opt-in acknowledgment set: per (name, mode), so
	// accepting "vtp wipe" does not accept "vtp set".
	ack := make(map[string]bool, len(r.opts.Acknowledged))
	for _, a := range r.opts.Acknowledged {
		ack[entryKey(a.Name, a.Mode)] = true
	}

	// teardownBudget is the total time the executor may spend; tests
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
			// The stream was stopped. If the interrupt path set an
			// error (teardown partial failure), surface it; otherwise
			// a consumer close returns nil.
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

		entry, ok := lookup[entryKey(ref.Name, ref.Mode)]
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

		// Enforce the catalog's legs requirement uniformly. The
		// runtime fails fast before invocation so no behavior
		// re-checks it.
		if entry.Legs == catalog.WatchRequired && r.opts.WatchLeg == nil {
			err := errs.New().
				Code(catalog.ErrCodeWatchLegRequired).
				Attr("name", ref.Name).
				UserMsg(fmt.Sprintf("%q requires a watch leg", ref.Name)).
				Hint("pass -w <iface> to attach a watch leg").
				Msgf("behavior %q requires a watch leg; none attached", ref.Name)
			r.stream.fail(err)
			return err
		}

		// Durability gate. The gate consults
		// the catalog entry BEFORE the leg's TX is ever handed to the
		// behavior: a refused behavior literally never receives a
		// send-capable leg, so zero frames are possible.
		dec := gate(r.stream, entry, ack, r.opts.Orchestrated)
		if !dec.proceed {
			// Refusal: the gate already emitted the typed refusal
			// record. Return the coded non-zero exit (runtime
			// failure = 1).
			err := errs.New().
				Code(catalog.ErrCodePermanentRefused).
				Attr("name", ref.Name).
				Attr("mode", ref.Mode).
				ExitCode(1).
				UserMsg(fmt.Sprintf("%q mode %q refused", ref.Name, ref.Mode)).
				Hint("supply the per-run opt-in acknowledgment for this permanent mode").
				Msgf("permanent-destructive %q (mode %q) refused by durability gate", ref.Name, ref.Mode)
			r.stream.fail(err)
			return err
		}

		fn, ok := r.opts.Behaviors[ref.Name]
		if !ok || fn == nil {
			err := errs.New().
				Code(catalog.ErrCodeUnknownBehavior).
				Attr("name", ref.Name).
				UserMsg(fmt.Sprintf("attack %q is registered but has no implementation", ref.Name)).
				Hint("this is a netpen internal error; report it").
				Msgf("no behavior function registered for %q", ref.Name)
			r.stream.fail(err)
			return err
		}

		// Stamp the current attack identity (name and mode) for
		// record emission.
		r.stream.setAttack(ref.Name, entry.Mode)

		// Emit the run-start announcement (if any) before the first
		// frame: transient-decay's decay bound, or the acknowledged-
		// permanent accepted-mode + consequence.
		if dec.announce != nil {
			if !r.stream.send(*dec.announce) {
				// Stream terminating; stop dispatching.
				if r.closedByConsumer.Load() {
					return nil
				}
				if runCtxErr := runCtx.Err(); runCtxErr != nil {
					r.stream.fail(runCtxErr)
					return runCtxErr
				}
				return nil
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
		// entry point).
		r.teardownMu.Lock()
		r.activeTeardown = dec.teardown
		r.activeCancel = cancel
		r.teardownMu.Unlock()

		deps := Deps{
			AttackLeg: r.opts.AttackLeg,
			WatchLeg:  r.opts.WatchLeg,
			Rate:      r.opts.Rate,
			Entry:     entry,
			Emitter:   &Emitter{stream: r.stream},
			Teardown:  dec.teardown,
		}

		behErr := fn(attackCtx, deps)
		cancel()

		// Run the armed teardown (if any) on completion or interrupt.
		// The teardown executes the armed steps in reverse-dependency
		// order, each in its own error scope, bounded by the budget.
		// A temporary-restored behavior that armed zero steps is a
		// coded runtime failure (a required restore that restores
		// nothing is a bug, not a silent skip).
		if dec.teardown != nil {
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

		// Clear the active teardown now that completion has run it.
		r.teardownMu.Lock()
		r.activeTeardown = nil
		r.activeCancel = nil
		r.teardownMu.Unlock()

		if runCtx.Err() != nil {
			break
		}

		if behErr != nil {
			// When the interrupt path engaged teardown, it canceled
			// the behavior's context; the resulting context.Canceled
			// is an expected consequence, not an independent behavior
			// error. Skip the behavior error record and let the
			// teardown-partial error (if any) surface from the
			// interrupt path.
			if r.interruptError() != nil && errors.Is(behErr, context.Canceled) {
				continue
			}
			// A behavior error is a finding, not a run failure:
			// surface it as a typed error record and continue
			// with remaining behaviors.
			if !emitErrorRecord(r.stream, ref.Name, entry.Mode, behErr) {
				// The stream is terminating (ctx canceled or
				// Close called). Stop dispatching.
				if r.closedByConsumer.Load() {
					return nil
				}
				if runCtxErr := runCtx.Err(); runCtxErr != nil {
					r.stream.fail(runCtxErr)
					return runCtxErr
				}
				return nil
			}
			continue
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

// runTeardown executes the armed teardown steps and returns the partial-
// failure error when one or more steps failed. It is the
// single entry point shared by completion and interrupt: the dispatch
// loop calls it after the behavior returns, and [Runner.Interrupt]
// (the signal path) calls it when a signal arrives mid-behavior. The
// [Teardown.Run] guard makes a second call a no-op, so completion-after-
// interrupt does not double-execute.
func (r *Runner) runTeardown(ctx context.Context, t *Teardown, budget time.Duration) error {
	// A temporary-restored behavior that armed zero steps is a coded
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
			Msgf("temporary-restored %q armed zero teardown steps", t.entry.Name)
		t.failed = []string{"<no-steps-armed>"}
		return err
	}
	// Restoration must survive the cancellation that stopped the behavior.
	// Teardown.Run applies a fresh deadline to each step.
	return t.Run(context.WithoutCancel(ctx), budget)
}

// Interrupt engages the teardown path from a signal (SIGINT, SIGTERM, or
// SIGHUP). It cancels the in-flight behavior's per-attack context
// (so the behavior returns promptly) and then runs the currently-armed
// teardown through the same entry point completion uses. If no teardown
// is armed (the run is between behaviors, or the current behavior is not
// temporary-restored), it cancels the run context so the dispatch loop
// exits.
//
// forceExit is a channel closed by the signal handler when a second
// signal arrives during teardown; the teardown loop observes it and
// reports the abandoned steps by name before the process force-exits.
// It may be nil when the caller does not need second-signal handling.
//
// Interrupt is idempotent: a second call (e.g. completion after the
// signal already engaged teardown) is a no-op. It is safe to call from
// any goroutine.
func (r *Runner) Interrupt(forceExit <-chan struct{}) {
	r.interruptOnce.Do(func() {
		r.teardownMu.Lock()
		td := r.activeTeardown
		ac := r.activeCancel
		r.teardownMu.Unlock()

		// Cancel the in-flight behavior so it returns promptly.
		if ac != nil {
			ac()
		}

		if td != nil {
			// Run the teardown. The budget comes from Options; the
			// signal path uses the same budget as completion. The
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
				// Emit the named partial-failure record through the
				// stream so the consumer sees it before the stream
				// closes (teardown completes before sink flush).
				// Store the error for the dispatch loop to
				// surface as Run's return value; do NOT call
				// stream.fail here, because that would stop the
				// stream and make the dispatch loop treat the
				// shutdown as a consumer close (returning nil).
				rec := td.partialRecord()
				_ = r.stream.send(rec)
				r.interruptErrMu.Lock()
				r.interruptErr = tdErr
				r.interruptErrMu.Unlock()
			}

			// After teardown, cancel the run context so the
			// dispatch loop exits and does not start the next
			// behavior.
			r.stopCancel()
		} else {
			// No teardown armed: cancel the run context so the
			// dispatch loop exits promptly.
			r.stopCancel()
		}

		// forceExit is closed by the signal handler when a second
		// signal arrives during teardown; the handler's watcher reads
		// the abandoned steps by name. The teardown here runs to
		// completion (Run is bounded), so abandoned steps surface
		// through the partial record; the force-exit itself is driven
		// by the handler.
		_ = forceExit
	})
}

// abandonedSteps returns the names of the teardown steps that were
// abandoned (did not complete) when a second signal force-exits during
// teardown. The abandoned set is the armed steps minus the ones that
// already finished (success or failure): a step still running or never
// reached is abandoned.
func (r *Runner) abandonedSteps() []string {
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

// ResolveEntry is a convenience for hosts that need to look up a catalog
// entry by (name, mode) without running. It returns the entry and true on a
// hit, or the zero entry and false when no catalog row matches. It is the
// same lookup Run uses internally.
func ResolveEntry(name, mode string) (catalog.Entry, bool) {
	for _, e := range catalog.Entries() {
		if e.Name == name && e.Mode == mode {
			return e, true
		}
	}
	return catalog.Entry{}, false
}

// entryKey builds the map key for a (name, mode) pair. The NUL byte separator
// prevents collisions between (name="a", mode="bc") and (name="ab",
// mode="c") — neither name nor mode may contain a NUL.
func entryKey(name, mode string) string {
	return name + "\x00" + mode
}

// emitErrorRecord surfaces a behavior error as a typed
// [findings.KindError] record through the stream. Returns false when the
// stream is terminating (the send did not deliver).
func emitErrorRecord(s *Stream, name, mode string, err error) bool {
	return s.send(findings.NewErrorRecord(err, name, mode))
}
