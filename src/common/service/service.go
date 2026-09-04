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
	return runWithOptionsAndTelemetryFactories(ctx, config, options, defaultTelemetryFactories)
}

func runWithOptionsAndTelemetryFactories(
	ctx context.Context,
	config Config,
	options supervisorOptions,
	factories telemetryFactorySet,
) (runErr error) {
	options = options.withDefaults()
	normalized, err := preflight(ctx, config, options.lookup)
	if err != nil {
		return err
	}
	telemetryOwner, err := newRunTelemetry(ctx, normalized.identity, normalized.telemetry, factories)
	if err != nil {
		return err
	}
	telemetry := telemetryOwner.telemetry
	defer func() {
		runErr = errors.Join(runErr, telemetryOwner.shutdown(context.WithoutCancel(ctx)))
	}()
	if ctx.Err() != nil {
		return nil
	}
	rootTelemetry := telemetry.view(normalized.telemetry.rootPolicy)
	lifecycleCtx := rootTelemetry.context(ctx)
	_, startupSpan := startLifecycleSpan(
		lifecycleCtx,
		rootTelemetry.tracer,
		normalized.identity,
		normalized.identity.Name,
		startupSpanName,
		lifecycleActionStart,
	)
	var bus *localBus
	busHealthy := true
	if normalized.bus != nil {
		bus, err = startLocalBus(ctx, *normalized.bus, reconcileRuntimeManifest(normalized))
		if err != nil {
			endLifecycleSpan(startupSpan, lifecycleOutcomeError)
			return err
		}
	}
	admission := newAdmissionState(normalized.admission.withModuleSnapshot(normalized.modules))
	var messages *messageRuntime
	if bus != nil {
		messages = newMessageRuntime(bus.resources, normalized.registry, admission.load, telemetry)
	}
	runtime := supervisorRuntime{
		identity:  normalized.identity,
		envPrefix: normalized.envPrefix,
		telemetry: telemetry,
		options:   options,
		admission: admission,
		messages:  messages,
	}
	if bus != nil {
		runtime.infrastructureFailure = bus.reportFailure
	}
	supervisor := newSupervisorState(normalized.identity.Name, normalized.rootSupervisor, normalized.modules, runtime)
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Cause(ctx))
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() { done <- supervisor.runWithStarted(runCtx, started) }()
	<-started
	endLifecycleSpan(startupSpan, lifecycleOutcomeRunning)

	var failures <-chan error
	if bus != nil {
		failures = bus.failures()
	}
	shutdownOutcome := lifecycleOutcomeNormal
	var shutdownCause error
	supervisorDone := false
	select {
	case runErr = <-done:
		supervisorDone = true
		shutdownCause = runErr
		if runErr != nil {
			shutdownOutcome = lifecycleOutcomeError
		}
	case err = <-failures:
		busHealthy = false
		shutdownCause = err
		shutdownOutcome = lifecycleOutcomeError
	case <-ctx.Done():
		shutdownCause = context.Cause(ctx)
		shutdownOutcome = lifecycleOutcomeCanceled
	}
	admission.setPhase(admissionShuttingDown)
	_, shutdownSpan := startLifecycleSpan(
		lifecycleCtx,
		rootTelemetry.tracer,
		normalized.identity,
		normalized.identity.Name,
		shutdownSpanName,
		lifecycleActionStop,
	)
	cancel(shutdownCause)
	if !supervisorDone {
		supervisorErr := <-done
		if shutdownOutcome == lifecycleOutcomeError {
			runErr = errors.Join(shutdownCause, supervisorErr)
		} else {
			runErr = supervisorErr
		}
	}
	if bus != nil {
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), normalized.bus.startupTimeout)
		closeErr := bus.close(closeCtx, busHealthy)
		closeCancel()
		runErr = errors.Join(runErr, closeErr)
		if closeErr != nil {
			shutdownOutcome = lifecycleOutcomeError
		}
	}
	endLifecycleSpan(shutdownSpan, shutdownOutcome)
	return runErr
}
