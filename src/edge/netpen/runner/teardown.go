package runner

import (
	"context"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
)

// Run executes the armed teardown steps in arm order (reverse-dependency:
// least-dependent — host-local state — first), each in its own error
// scope, so one failing step does not abandon later ones.
// Per-step failures collect into the named partial-failure record and
// set the returned error's exit code to 1. Completion and
// interrupt share this one entry point.
//
// Each step receives a fraction of the remaining budget under a child of
// ctx. Non-positive budgets use [DefaultTeardownBudget]. Steps must return
// promptly on cancellation; Run cannot preempt a step that ignores its context.
//
// Run is idempotent and goroutine-safe: completion and interrupt may both
// call it concurrently, but the steps execute exactly once. The runOnce
// guard serializes the first caller and makes later callers no-ops. The
// returned error is the result of the single execution.
func (t *Teardown) Run(ctx context.Context, budget time.Duration) error {
	var result error
	t.runOnce.Do(func() {
		result = t.execute(ctx, budget)
	})
	return result
}

// execute is the single-pass step runner, called once by Run under the
// runOnce guard. It is not goroutine-safe on its own; Run serializes.
func (t *Teardown) execute(ctx context.Context, budget time.Duration) error {
	if budget <= 0 {
		budget = DefaultTeardownBudget
	}

	deadline := time.Now().Add(budget)

	t.mu.Lock()
	t.failed = t.failed[:0]
	t.completed = t.completed[:0]
	steps := make([]teardownStep, len(t.steps))
	copy(steps, t.steps)
	t.mu.Unlock()

	var (
		failed    []string
		completed []string
	)

	for i, step := range steps {
		// Each step gets its own context with a share of the remaining
		// budget, so a hung step cannot consume the whole allowance and
		// starve later steps. The share is the remaining time divided
		// evenly across the steps left to run.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			// Budget exhausted: the steps still pending are failures
			// by name, but we keep collecting rather than abandoning.
			for _, s := range steps[i:] {
				failed = append(failed, s.name)
			}
			break
		}
		share := remaining / time.Duration(len(steps)-i)
		stepCtx, cancel := context.WithTimeout(ctx, share)

		err := step.fn(stepCtx)
		cancel()

		// A step completes whether it succeeded or failed; record it
		// so the abandoned-steps report can distinguish completed from
		// still-running steps.
		completed = append(completed, step.name)
		if err != nil {
			failed = append(failed, step.name)
			// Continue: one failing step must not abandon later steps.
		}

		// Publish progress so a concurrent force-exit reads a
		// consistent snapshot of completed vs. armed steps.
		t.mu.Lock()
		t.failed = append([]string(nil), failed...)
		t.completed = append([]string(nil), completed...)
		t.mu.Unlock()
	}

	if len(failed) > 0 {
		return errs.New().
			Code(catalog.ErrCodeTeardownPartial).
			ExitCode(1).
			Msgf("teardown partial failure: %v", failed)
	}
	return nil
}

// failedSteps returns a copy of the failed-step names under the mutex, so
// the abandoned-steps report reads a consistent snapshot.
func (t *Teardown) failedSteps() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.failed))
	copy(out, t.failed)
	return out
}

// abandonedSnapshot returns the armed step names that have not yet
// completed (success or failure), computed under a single lock so the
// force-exit report reads a consistent snapshot. Called by the signal
// handler during a second-signal force-exit.
func (t *Teardown) abandonedSnapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	completed := make(map[string]bool, len(t.completed))
	for _, n := range t.completed {
		completed[n] = true
	}
	var abandoned []string
	for _, s := range t.steps {
		if !completed[s.name] {
			abandoned = append(abandoned, s.name)
		}
	}
	return abandoned
}
