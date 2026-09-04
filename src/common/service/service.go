package service

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// Run validates config and runs its enabled modules until they all stop, root
// supervision fails or exhausts policy, the caller cancels ctx, or the process
// receives an interrupt or termination signal. Run waits for every started
// module before flushing caller-owned telemetry. Graceful cancellation returns
// nil; lifecycle and shutdown failures are returned.
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

func run(ctx context.Context, config Config) error {
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
	var bus *localBus
	busHealthy := true
	if normalized.bus != nil {
		bus, err = startLocalBus(ctx, *normalized.bus, reconcileRuntimeManifest(normalized))
		if err != nil {
			return err
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), normalized.bus.startupTimeout)
			defer cancel()
			runErr = errors.Join(runErr, bus.close(closeCtx, busHealthy))
		}()
	}
	admission := newAdmissionState(normalized.admission.withModuleSnapshot(normalized.modules))
	var messages *messageRuntime
	if bus != nil {
		messages = newMessageRuntime(bus.resources, normalized.registry, admission.load, telemetry)
	}
	runtime := supervisorRuntime{
		identity:              normalized.identity,
		envPrefix:             normalized.envPrefix,
		telemetry:             telemetry,
		options:               options,
		admission:             admission,
		messages:              messages,
		infrastructureFailure: func(err error) { bus.reportFailure(err) },
	}
	supervisor := newSupervisorState(normalized.identity.Name, normalized.rootSupervisor, normalized.modules, runtime)
	if bus == nil {
		return supervisor.run(ctx)
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Cause(ctx))
	done := make(chan error, 1)
	go func() { done <- supervisor.run(runCtx) }()
	select {
	case err := <-done:
		admission.setPhase(admissionShuttingDown)
		cancel(err)
		return err
	case err := <-bus.failures():
		admission.setPhase(admissionShuttingDown)
		busHealthy = false
		cancel(err)
		return errors.Join(err, <-done)
	case <-ctx.Done():
		admission.setPhase(admissionShuttingDown)
		cancel(context.Cause(ctx))
		return <-done
	}
}
