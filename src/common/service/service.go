package service

import (
	"context"
	"errors"
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
	return runWithOptions(ctx, config, supervisorOptions{})
}

func runWithOptions(ctx context.Context, config Config, options supervisorOptions) (runErr error) {
	options = options.withDefaults()
	normalized, err := preflight(ctx, config, options.lookup)
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
	runtime := supervisorRuntime{
		identity:  normalized.identity,
		envPrefix: normalized.envPrefix,
		telemetry: telemetry,
		options:   options,
	}
	return newSupervisorState(normalized.identity.Name, true, normalized.rootSupervisor, normalized.modules, runtime).run(ctx)
}
