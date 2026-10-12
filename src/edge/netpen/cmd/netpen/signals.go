package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// signalHandler installs the three-signal lifecycle (SIGINT, SIGTERM,
// SIGHUP) over a [runner.Runner]. All three engage the same teardown path:
// the first signal interrupts the run (canceling the in-flight behavior)
// and runs the armed teardown steps; a second signal during teardown
// force-exits the process and reports by name the teardown steps that were
// abandoned. The runner owns no process, so exit lives here with the
// command.
//
// A signalHandler is single-use: one handler per run. install must be
// called once; after it returns the handler is safe for concurrent use.
type signalHandler struct {
	r      *runner.Runner
	stderr io.Writer
	exit   func(int)

	// teardownStarted is 0 before the first signal, 1 once the first
	// signal has engaged teardown. The signal goroutine reads it
	// atomically: a second signal with teardownStarted==1 force-exits.
	teardownStarted atomic.Int32

	// teardownDone closes when the runner's teardown completes, so the
	// signal goroutine does not race a late second signal against the
	// teardown finishing.
	teardownDone chan struct{}
}

func newSignalHandler(r *runner.Runner, stderr io.Writer) *signalHandler {
	return &signalHandler{
		r:            r,
		stderr:       stderr,
		exit:         os.Exit,
		teardownDone: make(chan struct{}),
	}
}

// install arms the three-signal lifecycle and returns a stop function that
// restores the previous handlers. The stop function is idempotent and safe
// to defer. After stop, no further signals are intercepted.
func (h *signalHandler) install() func() {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	stop := make(chan struct{})

	spawn.Go(h.r.StopContext(), "netpen signal handler", func() {
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
func (h *signalHandler) handleSignal() {
	if h.teardownStarted.Load() == 1 {
		select {
		case <-h.teardownDone:
			h.forceExit(nil)
		default:
			h.forceExit(h.r.AbandonedSteps())
		}
		return
	}

	// First signal: mark teardown started and engage it in a goroutine
	// so the signal loop is free to observe a second signal. teardownDone
	// closes once, from either the normal return below or the ReportTo
	// sink on a panic, so a second-signal check never blocks waiting for
	// a teardown attempt that will not complete on its own.
	h.teardownStarted.Store(1)
	spawn.Go(h.r.StopContext(), "netpen signal teardown", func() {
		h.r.Interrupt()
		close(h.teardownDone)
	}, spawn.ReportTo(func(error) {
		close(h.teardownDone)
	}))
}

// forceExit reports the abandoned teardown steps by name to stderr and
// exits non-zero. abandoned is nil when no steps were left (rapid
// double-signal after completion); the exit is still non-zero because the
// operator forced termination.
func (h *signalHandler) forceExit(abandoned []string) {
	if len(abandoned) > 0 {
		fmt.Fprintf(h.stderr, "netpen: forced exit; abandoned teardown steps: %s\n",
			strings.Join(abandoned, ", "))
	} else {
		fmt.Fprintln(h.stderr, "netpen: forced exit")
	}
	h.exit(1)
}
