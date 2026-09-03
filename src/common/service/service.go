package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Run validates config and runs its modules until a module returns, the caller
// cancels ctx, or the process receives an interrupt or termination signal. Run
// waits for every started module before flushing caller-owned telemetry.
func Run(ctx context.Context, config Config) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return run(ctx, config)
}

func runWithSignalChannel(ctx context.Context, config Config, signals <-chan os.Signal) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	return run(ctx, config)
}

func run(ctx context.Context, config Config) (runErr error) {
	normalized, err := preflight(ctx, config, os.LookupEnv)
	if err != nil {
		return err
	}
	telemetry, err := newTelemetry(config)
	if err != nil {
		return err
	}
	if normalized.telemetryShutdown != nil {
		defer func() {
			runErr = errors.Join(runErr, normalized.telemetryShutdown(context.WithoutCancel(ctx)))
		}()
	}
	if ctx.Err() != nil {
		return nil
	}

	type runtimeAttempt struct {
		module plannedModule
		values contextValues
		runner Runner
	}
	leaves := enabledLeafModules(normalized.modules)
	attempts := make([]runtimeAttempt, 0, len(leaves))
	for _, module := range leaves {
		values := telemetry.values(normalized.identity, normalized.envPrefix, module.path)
		attemptCtx := withContextValues(ctx, values)
		moduleAttempt, setupErr := callSetup(attemptCtx, module)
		if setupErr != nil {
			return setupErr
		}
		if moduleAttempt.Runner == nil {
			return fmt.Errorf("module %s setup returned a nil runner", module.path)
		}
		if err := validateAttemptHandlers(module.path, module.leaf.subscriptions, moduleAttempt.Handlers); err != nil {
			return err
		}
		attempts = append(attempts, runtimeAttempt{module: module, values: values, runner: moduleAttempt.Runner})
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		module  plannedModule
		outcome lifecycleOutcome
		err     error
	}
	results := make(chan result, len(attempts))
	for _, attempt := range attempts {
		attemptCtx := withContextValues(runCtx, attempt.values)
		if err := telemetry.recordLifecycle(attemptCtx, normalized.identity, attempt.module.path, lifecycleActionStart, lifecycleOutcomeRunning); err != nil {
			return err
		}
		go func() {
			outcome, runnerErr := callRunner(attemptCtx, attempt.module, attempt.runner)
			results <- result{module: attempt.module, outcome: outcome, err: runnerErr}
		}()
	}

	var resultErr error
	for range attempts {
		result := <-results
		cancel()
		if err := telemetry.recordLifecycle(context.WithoutCancel(ctx), normalized.identity, result.module.path, lifecycleActionStop, result.outcome); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
		if result.err != nil && !errors.Is(result.err, context.Canceled) {
			resultErr = errors.Join(resultErr, result.err)
		}
	}

	return resultErr
}

func callSetup(ctx context.Context, module plannedModule) (attempt Attempt, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("module %s setup panic: %v", module.path, recovered)
		}
	}()

	return module.leaf.setup(ctx)
}

func callRunner(ctx context.Context, module plannedModule, runner Runner) (outcome lifecycleOutcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = lifecycleOutcomePanic
			err = fmt.Errorf("module %s runner panic: %v", module.path, recovered)
		}
	}()

	err = runner(ctx)
	switch {
	case errors.Is(err, context.Canceled):
		return lifecycleOutcomeCanceled, err
	case err != nil:
		return lifecycleOutcomeError, err
	default:
		return lifecycleOutcomeNormal, nil
	}
}
