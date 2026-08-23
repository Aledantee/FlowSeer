package runner

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/netpen/catalog"
	"go.aledante.io/FlowSeer/src/netpen/findings"
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
// when none is attached fails fast before invocation with a coded error —
// R2), and executes each behavior under a per-attack context derived from
// Run's ctx (zgrab2's cancellation pattern).
//
// Error semantics: a behavior erroring mid-run surfaces as a typed
// [findings.ErrorRecord] through the stream and the run of remaining
// behaviors continues. Only a dispatch-level failure (unknown attack, leg
// precondition) or context cancellation aborts the whole run. Run returns
// ctx.Err() unwrapped on cancellation (code-style).
//
// The zero value is not usable; construct a Runner via [NewRunner].
//
// Runner is safe for concurrent use only in the restricted sense that one
// goroutine calls Run while any number of goroutines consume
// [Runner.Stream]. Run must not be called concurrently with itself.
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
// a non-nil error only for dispatch-level failures (unknown attack, leg
// precondition) or context cancellation; on cancellation it returns
// ctx.Err() unwrapped.
func (r *Runner) Run(ctx context.Context) error {
	defer close(r.runDone)
	defer r.stream.done()

	if len(r.opts.Attacks) == 0 {
		return nil
	}

	if r.opts.AttackLeg == nil {
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

	// Build the entry lookup once: a map from (name, mode) to catalog
	// entry. The catalog is the single source for dispatch metadata.
	entries := catalog.Entries()
	lookup := make(map[string]catalog.Entry, len(entries))
	for _, e := range entries {
		lookup[entryKey(e.Name, e.Mode)] = e
	}

	for _, ref := range r.opts.Attacks {
		// A consumer-initiated [Stream.Close] cancels runCtx (via
		// closeHook) and sets closedByConsumer. The dispatch loop
		// returns nil — a consumer close is graceful, not a run
		// failure. A caller-ctx cancellation (closedByConsumer is
		// false) returns ctx.Err() unwrapped.
		select {
		case <-r.stream.pump.Stopped():
			return nil
		default:
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

		// Enforce the catalog's legs requirement uniformly (R2). The
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

		fn, ok := r.opts.Behaviors[ref.Name]
		if !ok {
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

		// Derive a per-attack context (zgrab2's cancellation pattern):
		// a behavior runs under a child of runCtx so canceling one
		// attack does not abort siblings. In this unit all behaviors
		// share runCtx's cancellation; U6 may layer per-attack cancel
		// for the teardown/signal lifecycle.
		attackCtx, cancel := context.WithCancel(runCtx)

		deps := Deps{
			AttackLeg: r.opts.AttackLeg,
			WatchLeg:  r.opts.WatchLeg,
			Rate:      r.opts.Rate,
			Entry:     entry,
			Emitter:   &Emitter{stream: r.stream},
		}

		if err := fn(attackCtx, deps); err != nil {
			cancel()
			// A behavior error is a finding, not a run failure:
			// surface it as a typed error record and continue
			// with remaining behaviors.
			if !emitErrorRecord(r.stream, ref.Name, entry.Mode, err) {
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
		cancel()
	}

	return nil
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
	rec := findings.NewRecord(findings.KindError)
	rec.Attack = name
	rec.Mode = mode
	rec.Error = &findings.ErrorRecord{
		Code:    errorCodeString(err),
		Message: err.Error(),
		Attack:  name,
	}
	return s.send(rec)
}

// errorCodeString extracts the errs.Code from err's chain if present,
// returning its wire form. The code is the stable identity a consumer
// matches on; empty when the error carries no code.
func errorCodeString(err error) string {
	if c, ok := errs.CodeOf(err); ok {
		return c.String()
	}
	return ""
}
