package runner

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"go.aledante.io/FlowSeer/src/common/spawn"
)

// SignalHandler installs the three-signal lifecycle (SIGINT, SIGTERM,
// SIGHUP) over a [Runner]. All three engage the same teardown
// path: the first signal interrupts the run (canceling the in-flight
// behavior) and runs the armed teardown steps; a second signal during
// teardown force-exits the process and reports by name the teardown
// steps that were abandoned.
//
// The handler is the bridge between the OS signal the operator sends and
// the runner's teardown executor: it calls [Runner.Interrupt], which
// cancels the run context (so the in-flight behavior returns promptly)
// and then runs the armed teardown through the shared completion entry
// point. The third-state case — teardown running while [Runner.Run]
// returns — is handled by the ordering guarantee that teardown completes
// before the stream's records flush (the runner flushes the
// partial-failure record through the stream before [Runner.Run] returns,
// so a sink draining the stream sees teardown's record before it sees
// the stream close).
//
// Install returns a stop function that restores the previous signal
// handlers; a host (main) defers it. A SignalHandler is single-use: one
// handler per run.
type SignalHandler struct {
	r *Runner

	// teardownStarted is 0 before the first signal, 1 once the first
	// signal has engaged teardown. The signal goroutine reads it
	// atomically: a second signal with teardownStarted==1 force-exits.
	teardownStarted atomic.Int32

	// teardownDone closes when the runner's teardown completes, so the
	// signal goroutine does not race a late second signal against the
	// teardown finishing.
	teardownDone chan struct{}
}

// NewSignalHandler constructs a SignalHandler for r. It does not install
// handlers yet; call [SignalHandler.Install] to arm the lifecycle.
func NewSignalHandler(r *Runner) *SignalHandler {
	return &SignalHandler{
		r:            r,
		teardownDone: make(chan struct{}),
	}
}

// Install arms the three-signal lifecycle and returns a stop function
// that restores the previous handlers. The first of SIGINT, SIGTERM, or
// SIGHUP engages teardown via [Runner.Interrupt]; a second signal while
// teardown is running force-exits the process (via [os.Exit]) after
// reporting the abandoned teardown steps by name to stderr.
//
// The stop function is idempotent and safe to defer. After stop, no
// further signals are intercepted.
func (h *SignalHandler) Install() func() {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	stop := make(chan struct{})

	spawn.Go(h.r.stopCtx, "netpen signal handler", func() {
		for {
			select {
			case <-stop:
				signal.Stop(sigCh)
				return
			case <-sigCh:
				h.handleSignal()
			}
		}
	})

	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}
}

// handleSignal processes one signal. The first signal engages teardown
// asynchronously (so the signal goroutine returns to the loop and can
// observe a second signal). A second signal while teardown is running
// force-exits, naming the abandoned steps.
func (h *SignalHandler) handleSignal() {
	if h.teardownStarted.Load() == 1 {
		// Second signal during teardown (or after): force-exit,
		// naming the steps that were abandoned. The teardown may
		// still be running; the abandoned set is the armed steps
		// that have not completed.
		select {
		case <-h.teardownDone:
			// Teardown already completed: no abandoned steps.
			forceExit(nil)
		default:
			abandoned := h.r.abandonedSteps()
			forceExit(abandoned)
		}
		return
	}

	// First signal: mark teardown started and engage it in a goroutine
	// so the signal loop is free to observe a second signal. teardownDone
	// closes once, from either the normal return below or the ReportTo
	// sink on a panic, so a second-signal check never blocks waiting for
	// a teardown attempt that will not complete on its own.
	h.teardownStarted.Store(1)
	spawn.Go(h.r.stopCtx, "netpen signal teardown", func() {
		h.r.Interrupt(nil)
		close(h.teardownDone)
	}, spawn.ReportTo(func(error) {
		close(h.teardownDone)
	}))
}

// forceExit reports the abandoned teardown steps by name to stderr and
// exits non-zero. It is called only from the second-signal force-exit
// path. abandoned is nil when no steps were left (rapid double-signal
// after completion); the exit is still non-zero because the operator
// forced termination.
func forceExit(abandoned []string) {
	if len(abandoned) > 0 {
		fmt.Fprintf(os.Stderr, "netpen: forced exit; abandoned teardown steps: %s\n",
			strings.Join(abandoned, ", "))
	} else {
		fmt.Fprintln(os.Stderr, "netpen: forced exit")
	}
	os.Exit(1)
}
