package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// childReady is the line a subprocess prints once its signal handler is
// installed and its behavior has armed its teardown, so the parent signals
// a process that can act on it.
const childReady = "netpen signal child ready"

// signalCatalog is the shipped catalog with arpspoof replaced by a
// behavior that arms the given step, reports readiness, and blocks until
// interrupted.
func signalCatalog(step check.Step, name string) catalog.Catalog {
	bs := attacks.Behaviors()
	for i := range bs {
		if bs[i].Name == "arpspoof" {
			bs[i].Run = func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
				deps.Teardown.Arm(name, step)
				fmt.Println(childReady)
				<-ctx.Done()
				return check.Verdict{}, ctx.Err()
			}
		}
	}
	return catalog.MustNew(bs...)
}

// child is a re-executed test binary whose readiness line has been read.
type child struct {
	cmd    *exec.Cmd
	stderr bytes.Buffer
	rest   bytes.Buffer
	copied chan struct{}
}

// startChild re-executes the test binary into the named test with env set
// and returns once the child printed its readiness line, so a signal
// reaches a process whose handler is installed.
func startChild(t *testing.T, test, env string) *child {
	t.Helper()
	c := &child{copied: make(chan struct{})}
	c.cmd = exec.Command(os.Args[0], "-test.run="+test)
	c.cmd.Env = append(os.Environ(), env+"=1")
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.cmd.Stderr = &c.stderr
	if err := c.cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if scanner.Text() == childReady {
			break
		}
	}
	go func() {
		defer close(c.copied)
		_, _ = io.Copy(&c.rest, stdout)
	}()
	return c
}

// wait waits for the child and returns its exit code and output, stderr
// first, once both copies have finished.
func (c *child) wait() (int, string) {
	code := exitCode(c.cmd.Wait())
	<-c.copied
	return code, c.stderr.String() + c.rest.String()
}

// TestSignalInterruptEngagesTeardown is a subprocess test proving the
// signal lifecycle: a subprocess runs an actively restored behavior that
// blocks, then receives SIGINT or SIGHUP. The teardown completes (the
// planted failing step runs), the partial record flushes, and the process
// exits 1. It re-executes the test binary so the force-exit path cannot
// kill the test process.
func TestSignalInterruptEngagesTeardown(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess signal test skipped in -short")
	}
	if os.Getenv("NETPEN_SIGNAL_CHILD") == "1" {
		runSignalChild()
		return
	}
	for _, sig := range []struct {
		name string
		sig  os.Signal
	}{{"SIGINT", os.Interrupt}, {"SIGHUP", syscall.SIGHUP}} {
		t.Run(sig.name, func(t *testing.T) {
			sub := startChild(t, "TestSignalInterruptEngagesTeardown", "NETPEN_SIGNAL_CHILD")
			if err := sub.cmd.Process.Signal(sig.sig); err != nil {
				t.Fatalf("signal child: %v", err)
			}
			if code, out := sub.wait(); code != 1 {
				t.Errorf("child exit code: got %d, want 1 (teardown partial). output:\n%s", code, out)
			}
		})
	}
}

// TestSecondSignalForceExitNamesAbandoned proves the second-signal
// force-exit: a second signal during teardown force-exits the process and
// reports by name the abandoned teardown steps.
func TestSecondSignalForceExitNamesAbandoned(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess signal test skipped in -short")
	}
	if os.Getenv("NETPEN_DOUBLESIG_CHILD") == "1" {
		runDoubleSignalChild()
		return
	}

	sub := startChild(t, "TestSecondSignalForceExitNamesAbandoned", "NETPEN_DOUBLESIG_CHILD")
	if err := sub.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("first signal: %v", err)
	}
	// The second signal must land while the slow step runs. The gap is the
	// timing under test, and the step sleeps for seconds.
	time.Sleep(20 * time.Millisecond)
	if err := sub.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("second signal: %v", err)
	}
	code, out := sub.wait()
	if code != 1 {
		t.Errorf("child exit code: got %d, want 1 (forced exit). output:\n%s", code, out)
	}
	if !strings.Contains(out, "abandoned teardown steps") {
		t.Errorf("stderr does not name abandoned steps. output:\n%s", out)
	}
	if !strings.Contains(out, "slow-restore") {
		t.Errorf("stderr does not name the abandoned step 'slow-restore'. output:\n%s", out)
	}
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 0
}

// runSignalChild is the subprocess body for the single-signal teardown
// test: a runner with a hooked actively restored behavior and the
// command's signal handler, blocking in Run until the signal engages
// teardown and the run exits.
func runSignalChild() {
	leg := &noopLeg{}
	r := runner.NewRunner(runner.Options{
		Catalog:        signalCatalog(func(context.Context) error { return errors.New("restore failed") }, "restore-fails"),
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		MaxImpact:      check.ImpactTemporary,
		TeardownBudget: 2 * time.Second,
	})
	stop := newSignalHandler(r, os.Stderr).install()
	go func() {
		for range r.Stream().Iter() {
		}
	}()
	err := r.Run(context.Background())
	stop()
	os.Exit(errs.ExitCode(err))
}

// runDoubleSignalChild is the subprocess body for the second-signal
// force-exit test. The teardown step sleeps long enough that a second
// signal arrives during teardown, triggering the force-exit path that
// names the abandoned step.
func runDoubleSignalChild() {
	leg := &noopLeg{}
	slow := func(stepCtx context.Context) error {
		select {
		case <-stepCtx.Done():
			return stepCtx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	}
	r := runner.NewRunner(runner.Options{
		Catalog:        signalCatalog(slow, "slow-restore"),
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		MaxImpact:      check.ImpactTemporary,
		TeardownBudget: 10 * time.Second,
	})
	stop := newSignalHandler(r, os.Stderr).install()
	go func() {
		for range r.Stream().Iter() {
		}
	}()
	_ = r.Run(context.Background())
	stop()
	// Reaching here means teardown completed before the second signal; the
	// force-exit path in the handler exits 1 on its own.
	os.Exit(1)
}

// TestSignalHandlerInstallStop proves install and stop are idempotent and
// the run completes normally when no signal arrives.
func TestSignalHandlerInstallStop(t *testing.T) {
	r := runner.NewRunner(runner.Options{
		Catalog:   signalCatalog(func(context.Context) error { return nil }, "restore"),
		AttackLeg: &noopLeg{},
		Attacks:   nil,
	})
	stop := newSignalHandler(r, os.Stderr).install()
	stop()
	stop()
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// TestForceExitReportsAbandonedSteps pins the text a second signal prints
// and the exit status it asks for, through the handler's exit seam.
func TestForceExitReportsAbandonedSteps(t *testing.T) {
	var stderr bytes.Buffer
	code := -1
	h := newSignalHandler(runner.NewRunner(runner.Options{}), &stderr)
	h.exit = func(c int) { code = c }
	h.forceExit([]string{"slow-restore", "resign"})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := stderr.String(); got != "netpen: forced exit; abandoned teardown steps: slow-restore, resign\n" {
		t.Errorf("stderr = %q", got)
	}
	stderr.Reset()
	h.forceExit(nil)
	if got := stderr.String(); got != "netpen: forced exit\n" {
		t.Errorf("stderr = %q", got)
	}
}
