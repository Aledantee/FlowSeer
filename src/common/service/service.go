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
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		case <-done:
		}
	}()

	err := run(ctx, config)
	close(done)
	cancel()
	return err
}

func run(ctx context.Context, config Config) (runErr error) {
	normalized, err := normalizeConfig(config)
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

	type attempt struct {
		module runtimeModule
		ctx    context.Context
		runner Runner
	}
	attempts := make([]attempt, 0, len(normalized.modules))
	for _, module := range normalized.modules {
		attemptCtx := withContextValues(ctx, telemetry.values(normalized.identity, normalized.envPrefix, module.path))
		runner, setupErr := callSetup(attemptCtx, module)
		if setupErr != nil {
			return setupErr
		}
		if runner == nil {
			return fmt.Errorf("module %s setup returned a nil runner", module.path)
		}
		attempts = append(attempts, attempt{module: module, ctx: attemptCtx, runner: runner})
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		module  runtimeModule
		outcome lifecycleOutcome
		err     error
	}
	results := make(chan result, len(attempts))
	for _, attempt := range attempts {
		attempt := attempt
		attemptCtx := withContextValues(runCtx, valuesFromContext(attempt.ctx))
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

func callSetup(ctx context.Context, module runtimeModule) (runner Runner, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("module %s setup panic: %v", module.path, recovered)
		}
	}()

	return module.setup(ctx)
}

func callRunner(ctx context.Context, module runtimeModule, runner Runner) (outcome lifecycleOutcome, err error) {
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
