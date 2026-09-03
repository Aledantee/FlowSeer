package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

var (
	errSupervisorRestart = errors.New("service supervisor restart")
	errUnmanagedTask     = errors.New("service task is outside a managed module attempt")
)

type supervisorClock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type realSupervisorClock struct{}

func (realSupervisorClock) Now() time.Time { return time.Now() }

func (realSupervisorClock) Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type supervisorOptions struct {
	clock      supervisorClock
	jitter     func(time.Duration) time.Duration
	lookup     envLookup
	transition func(supervisorPath, operation, modulePath string)
}

func (o supervisorOptions) withDefaults() supervisorOptions {
	if o.clock == nil {
		o.clock = realSupervisorClock{}
	}
	if o.jitter == nil {
		o.jitter = func(limit time.Duration) time.Duration {
			if limit <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(limit)))
		}
	}
	if o.lookup == nil {
		o.lookup = envLookupFromOS
	}
	return o
}

func envLookupFromOS(key string) (string, bool) {
	return os.LookupEnv(key)
}

type supervisorRuntime struct {
	identity  Identity
	envPrefix string
	telemetry telemetry
	options   supervisorOptions
}

type childResult struct {
	index      int
	generation uint64
	outcome    lifecycleOutcome
	err        error
	fatal      bool
}

type childSlot struct {
	module     plannedModule
	active     bool
	generation uint64
	cancel     context.CancelCauseFunc
	done       chan struct{}
	startedAt  time.Time

	budgets  [3]rollingBudget
	backoffs [3]int
}

type supervisorState struct {
	path      string
	root      bool
	policy    normalizedSupervisor
	runtime   supervisorRuntime
	results   chan childResult
	slots     []childSlot
	intensity rollingBudget
}

func newSupervisorState(
	path string,
	root bool,
	policy normalizedSupervisor,
	modules []plannedModule,
	runtime supervisorRuntime,
) *supervisorState {
	slots := make([]childSlot, len(modules))
	for i, module := range modules {
		slots[i] = childSlot{module: module, active: module.enabled}
		outcomes := [...]normalizedOutcomePolicy{module.policy.normal, module.policy.failure, module.policy.panic}
		for outcomeIndex, outcome := range outcomes {
			slots[i].budgets[outcomeIndex].limit = outcome.budget
		}
	}
	return &supervisorState{
		path:      path,
		root:      root,
		policy:    policy,
		runtime:   runtime,
		results:   make(chan childResult, len(modules)*2+1),
		slots:     slots,
		intensity: rollingBudget{limit: policy.intensity},
	}
}

func (s *supervisorState) run(ctx context.Context) error {
	for i := range s.slots {
		if s.slots[i].active {
			s.start(ctx, i, false)
		}
	}
	for {
		if ctx.Err() != nil {
			s.stopAll(errSupervisorRestart)
			return nil
		}
		if !s.hasActiveChild() {
			return nil
		}

		select {
		case <-ctx.Done():
			s.stopAll(context.Cause(ctx))
			return nil
		case result := <-s.results:
			if ctx.Err() != nil {
				s.stopAll(context.Cause(ctx))
				return nil
			}
			if !s.current(result) {
				continue
			}
			if result.fatal {
				s.stopAll(result.err)
				return result.err
			}
			if result.outcome == lifecycleOutcomeCanceled {
				continue
			}
			if err := s.decide(ctx, result); err != nil {
				s.stopAll(err)
				return err
			}
		}
	}
}

func (s *supervisorState) current(result childResult) bool {
	if result.index < 0 || result.index >= len(s.slots) {
		return false
	}
	slot := &s.slots[result.index]
	return slot.active && slot.generation == result.generation
}

func (s *supervisorState) decide(ctx context.Context, result childResult) error {
	slot := &s.slots[result.index]
	policy, outcomeIndex := outcomePolicy(slot.module.policy, result.outcome)
	if policy == nil {
		return fmt.Errorf("module %s produced unknown lifecycle outcome %d", slot.module.path, result.outcome)
	}

	now := s.runtime.options.clock.Now()
	if now.Sub(slot.startedAt) >= policy.backoff.ResetAfter {
		slot.budgets[outcomeIndex].reset()
		slot.backoffs[outcomeIndex] = 0
	}
	switch policy.action {
	case Stop:
		slot.active = false
		s.wait(result.index)
		return s.record(slot.module, lifecycleActionStop, result.outcome)
	case Escalate:
		return causalOutcomeError(slot.module.path, "escalated", result.err)
	case Restart:
		if !slot.budgets[outcomeIndex].consume(now) {
			return causalOutcomeError(slot.module.path, "outcome restart budget exhausted", result.err)
		}
		if !s.intensity.consume(now) {
			return causalOutcomeError(slot.module.path, "supervisor restart intensity exhausted", result.err)
		}
	default:
		return fmt.Errorf("module %s has unknown outcome action %d", slot.module.path, policy.action)
	}

	affected := s.affected(result.index)
	s.quiesce(affected)
	if err := s.record(slot.module, lifecycleActionRestart, result.outcome); err != nil {
		return err
	}
	delay := exponentialBackoff(policy.backoff, slot.backoffs[outcomeIndex])
	slot.backoffs[outcomeIndex]++
	delay = s.runtime.options.jitter(delay)
	if delay < 0 {
		delay = 0
	}
	if delay > policy.backoff.Maximum {
		delay = policy.backoff.Maximum
	}
	if err := s.runtime.options.clock.Wait(ctx, delay); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("module %s restart backoff: %w", slot.module.path, err)
	}
	for _, index := range affected {
		s.start(ctx, index, true)
	}
	return nil
}

func (s *supervisorState) affected(failed int) []int {
	indices := make([]int, 0, len(s.slots))
	for i := range s.slots {
		if !s.slots[i].active {
			continue
		}
		switch s.policy.strategy {
		case OneForOne:
			if i == failed {
				indices = append(indices, i)
			}
		case OneForAll:
			indices = append(indices, i)
		case RestForOne:
			if i >= failed {
				indices = append(indices, i)
			}
		}
	}
	return indices
}

func (s *supervisorState) quiesce(indices []int) {
	for i := len(indices) - 1; i >= 0; i-- {
		index := indices[i]
		s.transition("cancel", s.slots[index].module.path)
		s.slots[index].cancel(errSupervisorRestart)
	}
	for i := len(indices) - 1; i >= 0; i-- {
		s.wait(indices[i])
	}
}

func (s *supervisorState) wait(index int) {
	slot := &s.slots[index]
	if slot.done == nil {
		return
	}
	<-slot.done
	s.transition("wait", slot.module.path)
	slot.done = nil
}

func (s *supervisorState) stopAll(cause error) {
	indices := make([]int, 0, len(s.slots))
	for i := range s.slots {
		if s.slots[i].active {
			indices = append(indices, i)
		}
	}
	for i := len(indices) - 1; i >= 0; i-- {
		s.slots[indices[i]].cancel(cause)
	}
	for i := len(indices) - 1; i >= 0; i-- {
		s.wait(indices[i])
		s.slots[indices[i]].active = false
	}
}

func (s *supervisorState) start(parent context.Context, index int, reconstructed bool) {
	slot := &s.slots[index]
	module := slot.module
	if reconstructed && module.leaf == nil {
		children, enabledLeaves, err := snapshotGates(parent, module.children, s.runtime.options.lookup, true)
		if err != nil {
			s.publishImmediate(index, lifecycleOutcomeError, err)
			return
		}
		if enabledLeaves == 0 {
			s.publishImmediate(index, lifecycleOutcomeError, fmt.Errorf("module %s has no enabled leaf modules", module.path))
			return
		}
		module.children = children
		slot.module = module
	}

	slot.generation++
	generation := slot.generation
	childCtx, cancel := context.WithCancelCause(parent)
	slot.cancel = cancel
	slot.done = make(chan struct{})
	slot.startedAt = s.runtime.options.clock.Now()
	s.transition("setup", module.path)
	go func(done chan struct{}) {
		defer close(done)
		result := runChild(childCtx, index, generation, module, s.runtime)
		s.results <- result
	}(slot.done)
	s.transition("start", module.path)
	_ = s.record(module, lifecycleActionStart, lifecycleOutcomeRunning)
}

func (s *supervisorState) publishImmediate(index int, outcome lifecycleOutcome, err error) {
	slot := &s.slots[index]
	slot.generation++
	slot.cancel = func(error) {}
	slot.done = make(chan struct{})
	close(slot.done)
	slot.startedAt = s.runtime.options.clock.Now()
	s.results <- childResult{index: index, generation: slot.generation, outcome: outcome, err: err}
}

func (s *supervisorState) transition(operation, modulePath string) {
	if s.runtime.options.transition != nil {
		s.runtime.options.transition(s.path, operation, modulePath)
	}
}

func (s *supervisorState) hasActiveChild() bool {
	for i := range s.slots {
		if s.slots[i].active {
			return true
		}
	}
	return false
}

func (s *supervisorState) record(module plannedModule, action lifecycleAction, outcome lifecycleOutcome) error {
	return s.runtime.telemetry.recordLifecycle(
		context.Background(),
		s.runtime.identity,
		module.path,
		action,
		outcome,
	)
}

func runChild(
	ctx context.Context,
	index int,
	generation uint64,
	module plannedModule,
	runtime supervisorRuntime,
) (result childResult) {
	result.index = index
	result.generation = generation
	defer func() {
		if recovered := recover(); recovered != nil {
			result.outcome = lifecycleOutcomePanic
			result.err = newPanicDiagnostic(module.path, "supervisor", recovered)
			result.fatal = true
		}
	}()

	if module.leaf != nil {
		result.outcome, result.err = runLeafAttempt(ctx, module, runtime)
		return result
	}
	err := newSupervisorState(module.path, false, module.supervisor, module.children, runtime).run(ctx)
	if ctx.Err() != nil {
		result.outcome = lifecycleOutcomeCanceled
		return result
	}
	if err != nil {
		result.outcome = lifecycleOutcomeError
		result.err = err
		return result
	}
	result.outcome = lifecycleOutcomeNormal
	return result
}

type attemptCoordinatorKey struct{}

type attemptCoordinator struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	module   plannedModule
	terminal chan attemptTermination

	mu        sync.Mutex
	accepting bool
	owned     sync.WaitGroup
}

type attemptTermination struct {
	outcome lifecycleOutcome
	err     error
}

func newAttemptCoordinator(parent context.Context, module plannedModule) *attemptCoordinator {
	ctx, cancel := context.WithCancelCause(parent)
	return &attemptCoordinator{
		ctx:       ctx,
		cancel:    cancel,
		module:    module,
		terminal:  make(chan attemptTermination, 1),
		accepting: true,
	}
}

// Go starts task as work owned by the current module attempt. A returned error
// or panic terminates the attempt; a nil return is nonterminal. Go rejects calls
// outside a managed attempt and calls made after cancellation has begun.
func Go(ctx context.Context, task Runner) error {
	if ctx == nil || task == nil {
		return errUnmanagedTask
	}
	coordinator, ok := ctx.Value(attemptCoordinatorKey{}).(*attemptCoordinator)
	if !ok {
		return errUnmanagedTask
	}
	return coordinator.launch(task)
}

func (c *attemptCoordinator) launch(task Runner) error {
	c.mu.Lock()
	if !c.accepting || c.ctx.Err() != nil {
		c.mu.Unlock()
		return errUnmanagedTask
	}
	c.owned.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.owned.Done()
		outcome, err := callOwned(c.ctx, c.module.path, "task", task)
		if outcome == lifecycleOutcomeNormal || outcome == lifecycleOutcomeCanceled {
			return
		}
		c.terminate(attemptTermination{outcome: outcome, err: err})
	}()
	return nil
}

func (c *attemptCoordinator) terminate(result attemptTermination) {
	select {
	case c.terminal <- result:
		c.cancel(result.err)
	default:
	}
}

func (c *attemptCoordinator) stop(cause error) {
	c.mu.Lock()
	c.accepting = false
	c.mu.Unlock()
	c.cancel(cause)
	c.owned.Wait()
}

func runLeafAttempt(ctx context.Context, module plannedModule, runtime supervisorRuntime) (lifecycleOutcome, error) {
	coordinator := newAttemptCoordinator(ctx, module)
	values := runtime.telemetry.values(runtime.identity, runtime.envPrefix, module.path)
	attemptCtx := withContextValues(coordinator.ctx, values)
	attemptCtx = context.WithValue(attemptCtx, attemptCoordinatorKey{}, coordinator)

	attempt, setupOutcome, setupErr := callSetup(attemptCtx, module)
	if setupErr != nil {
		coordinator.stop(setupErr)
		if ctx.Err() != nil {
			return lifecycleOutcomeCanceled, nil
		}
		return setupOutcome, setupErr
	}
	if attempt.Runner == nil {
		err := fmt.Errorf("module %s setup returned a nil runner", module.path)
		coordinator.stop(err)
		return lifecycleOutcomeError, err
	}
	if err := validateAttemptHandlers(module.path, module.leaf.subscriptions, attempt.Handlers); err != nil {
		coordinator.stop(err)
		return lifecycleOutcomeError, err
	}

	coordinator.mu.Lock()
	if !coordinator.accepting || coordinator.ctx.Err() != nil {
		coordinator.mu.Unlock()
		coordinator.stop(context.Cause(coordinator.ctx))
		if ctx.Err() != nil {
			return lifecycleOutcomeCanceled, nil
		}
		terminal := <-coordinator.terminal
		return terminal.outcome, terminal.err
	}
	coordinator.owned.Add(1)
	coordinator.mu.Unlock()
	go func() {
		defer coordinator.owned.Done()
		outcome, err := callOwned(coordinator.ctx, module.path, "runner", attempt.Runner)
		coordinator.terminate(attemptTermination{outcome: outcome, err: err})
	}()

	var terminal attemptTermination
	select {
	case <-ctx.Done():
		terminal = attemptTermination{outcome: lifecycleOutcomeCanceled}
	case terminal = <-coordinator.terminal:
		if ctx.Err() != nil {
			terminal = attemptTermination{outcome: lifecycleOutcomeCanceled}
		}
	}
	coordinator.stop(terminal.err)
	return terminal.outcome, terminal.err
}

func callSetup(ctx context.Context, module plannedModule) (attempt Attempt, outcome lifecycleOutcome, err error) {
	outcome = lifecycleOutcomeError
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = lifecycleOutcomePanic
			err = newPanicDiagnostic(module.path, "setup", recovered)
		}
	}()
	attempt, err = module.leaf.setup(ctx)
	return attempt, outcome, err
}

func callOwned(ctx context.Context, modulePath, boundary string, runner Runner) (outcome lifecycleOutcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = lifecycleOutcomePanic
			err = newPanicDiagnostic(modulePath, boundary, recovered)
		}
	}()
	err = runner(ctx)
	if ctx.Err() != nil {
		return lifecycleOutcomeCanceled, nil
	}
	if err != nil {
		return lifecycleOutcomeError, err
	}
	return lifecycleOutcomeNormal, nil
}

type panicDiagnostic struct {
	modulePath string
	boundary   string
	value      any
	stack      []byte
}

func newPanicDiagnostic(modulePath, boundary string, value any) *panicDiagnostic {
	return &panicDiagnostic{modulePath: modulePath, boundary: boundary, value: value, stack: debug.Stack()}
}

func (e *panicDiagnostic) Error() string {
	return fmt.Sprintf("module %s %s panic (%T): %v", e.modulePath, e.boundary, e.value, e.value)
}

func outcomePolicy(policy normalizedPolicy, outcome lifecycleOutcome) (*normalizedOutcomePolicy, int) {
	switch outcome {
	case lifecycleOutcomeNormal:
		return &policy.normal, 0
	case lifecycleOutcomeError:
		return &policy.failure, 1
	case lifecycleOutcomePanic:
		return &policy.panic, 2
	default:
		return nil, -1
	}
}

func exponentialBackoff(backoff Backoff, exponent int) time.Duration {
	delay := backoff.Initial
	for range exponent {
		if delay >= backoff.Maximum/2 {
			return backoff.Maximum
		}
		delay *= 2
	}
	if delay > backoff.Maximum {
		return backoff.Maximum
	}
	return delay
}

func causalOutcomeError(modulePath, reason string, cause error) error {
	if cause == nil {
		return fmt.Errorf("module %s %s", modulePath, reason)
	}
	return fmt.Errorf("module %s %s: %w", modulePath, reason, cause)
}
