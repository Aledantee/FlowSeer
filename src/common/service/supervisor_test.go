package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRunCrossesOutcomesAndActions(t *testing.T) {
	for _, outcome := range []struct {
		name string
		run  Runner
	}{
		{name: "normal", run: func(context.Context) error { return nil }},
		{name: "error", run: func(context.Context) error { return errors.New("runner error") }},
		{name: "panic", run: func(context.Context) error { panic(typedPanic("runner panic")) }},
	} {
		for _, action := range []Action{Stop, Restart, Escalate} {
			t.Run(fmt.Sprintf("%s/%d", outcome.name, action), func(t *testing.T) {
				var attempts atomic.Int32
				startedAgain := make(chan struct{})
				policy := OutcomePolicy{
					Action:  action,
					Budget:  RestartBudget{Max: 1, Window: time.Minute},
					Backoff: Backoff{Initial: time.Nanosecond, Maximum: time.Nanosecond, ResetAfter: time.Hour},
				}
				modulePolicy := Policy{}
				switch outcome.name {
				case "normal":
					modulePolicy.Normal = policy
				case "error":
					modulePolicy.Error = policy
				case "panic":
					modulePolicy.Panic = policy
				}
				cfg := Config{
					Identity: testIdentity(),
					Modules: []Module{{
						Name:   "worker",
						Policy: modulePolicy,
						Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
							attempt := attempts.Add(1)
							if attempt > 1 {
								return Attempt{Runner: func(ctx context.Context) error {
									close(startedAgain)
									<-ctx.Done()
									return ctx.Err()
								}}, nil
							}
							return Attempt{Runner: outcome.run}, nil
						}},
					}},
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- runWithOptions(ctx, cfg, immediateSupervisorOptions()) }()
				if action == Restart {
					<-startedAgain
					cancel()
					if err := <-done; err != nil {
						t.Fatalf("restart shutdown error = %v", err)
					}
					if attempts.Load() != 2 {
						t.Fatalf("setup calls = %d, want 2", attempts.Load())
					}
					return
				}

				err := <-done
				cancel()
				if action == Escalate && err == nil {
					t.Fatal("escalated outcome returned nil")
				}
				if action == Stop && err != nil {
					t.Fatalf("stopped outcome error = %v", err)
				}
			})
		}
	}
}

func TestRunReconstructsFreshAttemptAfterError(t *testing.T) {
	var setups atomic.Int32
	started := make(chan int32, 2)
	cfg := Config{
		Identity: testIdentity(),
		Setup: func(context.Context) (Attempt, error) {
			identity := setups.Add(1)
			return Attempt{Runner: func(ctx context.Context) error {
				started <- identity
				if identity == 1 {
					return errors.New("retry")
				}
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithOptions(ctx, cfg, immediateSupervisorOptions()) }()

	if first, second := <-started, <-started; first == second {
		t.Fatalf("runner identities = %d and %d, want fresh attempts", first, second)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() shutdown error = %v", err)
	}
}

func TestRunStrategyAffectedSetsAndOrder(t *testing.T) {
	for _, tt := range []struct {
		name     string
		strategy Strategy
		want     []string
	}{
		{name: "one for one", strategy: OneForOne, want: []string{"cancel:second", "wait:second", "setup:second", "start:second"}},
		{name: "one for all", strategy: OneForAll, want: []string{"cancel:third", "cancel:second", "cancel:first", "wait:third", "wait:second", "wait:first", "setup:first", "start:first", "setup:second", "start:second", "setup:third", "start:third"}},
		{name: "rest for one", strategy: RestForOne, want: []string{"cancel:third", "cancel:second", "wait:third", "wait:second", "setup:second", "start:second", "setup:third", "start:third"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transitions := newLifecycleRecorder()
			fail := make(chan struct{})
			started := make(chan string, 6)
			module := func(name string, failure <-chan struct{}) Module {
				return Module{Name: name, Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					return Attempt{Runner: func(ctx context.Context) error {
						started <- name
						if failure != nil {
							select {
							case <-failure:
								return errors.New("planned failure")
							case <-ctx.Done():
							}
						} else {
							<-ctx.Done()
						}
						return ctx.Err()
					}}, nil
				}}}
			}
			cfg := Config{
				Identity: testIdentity(),
				Modules: []Module{{
					Name: "group",
					Branch: &Branch{
						Strategy: tt.strategy,
						Children: []Module{
							module("first", nil),
							module("second", fail),
							module("third", nil),
						},
					},
				}},
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			options := immediateSupervisorOptions()
			options.transition = func(supervisorPath, operation, modulePath string) {
				if supervisorPath == "edge/group" {
					transitions.record(operation + ":" + modulePath[len("edge/group/"):])
				}
			}
			go func() { done <- runWithOptions(ctx, cfg, options) }()
			for range 3 {
				<-started
			}
			transitions.reset()
			close(fail)
			transitions.waitForStarts(t, len(tt.want))
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("runWithOptions() shutdown error = %v", err)
			}
			if got := transitions.prefix(len(tt.want)); !equalStrings(got, tt.want) {
				t.Fatalf("events = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunContainsSafeTaskFailureAndWaitsForOwnedTasks(t *testing.T) {
	taskFailure := errors.New("task failed")
	taskStarted := make(chan struct{})
	taskStopped := make(chan struct{})
	err := runWithOptions(context.Background(), Config{
		Identity: testIdentity(),
		Modules: []Module{{
			Name:   "worker",
			Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
			Leaf: &Leaf{Setup: func(ctx context.Context) (Attempt, error) {
				if launchErr := Go(ctx, func(ctx context.Context) error {
					close(taskStarted)
					<-ctx.Done()
					close(taskStopped)
					return nil
				}); launchErr != nil {
					return Attempt{}, launchErr
				}
				return Attempt{Runner: func(context.Context) error {
					<-taskStarted
					return taskFailure
				}}, nil
			}},
		}},
	}, immediateSupervisorOptions())
	if !errors.Is(err, taskFailure) {
		t.Fatalf("runWithOptions() error = %v, want %v", err, taskFailure)
	}
	select {
	case <-taskStopped:
	default:
		t.Fatal("run returned before safe-launched task stopped")
	}
}

func TestDeliveryFailureBeforeRunnerStartTerminatesAttempt(t *testing.T) {
	deliveryFailure := errors.New("delivery startup failed")
	deliveryReturned := make(chan struct{})
	runnerStarted := make(chan struct{})
	config := Config{
		Identity: testIdentity(),
		Bus:      &BusConfig{StoreDir: t.TempDir()},
		Modules: []Module{{
			Name: "worker",
			Leaf: &Leaf{
				Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}},
				Setup: func(context.Context) (Attempt, error) {
					return Attempt{
						Runner: func(ctx context.Context) error {
							close(runnerStarted)
							<-ctx.Done()
							return ctx.Err()
						},
						Handlers: []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error { return nil }}},
					}, nil
				},
			},
		}},
	}
	runtime, err := preflight(context.Background(), config, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	options := immediateSupervisorOptions()
	options.beforeRunnerStart = func() { <-deliveryReturned }
	attemptRuntime := supervisorRuntime{
		identity:  runtime.identity,
		envPrefix: runtime.envPrefix,
		telemetry: telemetry,
		options:   options,
		messages:  &messageRuntime{},
		delivery: func(context.Context, plannedModule, []Handler) error {
			close(deliveryReturned)
			return deliveryFailure
		},
	}

	type result struct {
		outcome lifecycleOutcome
		err     error
	}
	done := make(chan result, 1)
	go func() {
		module := runtime.modules[0]
		outcome, _, err := runLeafAttempt(context.Background(), module, telemetry.view(module.telemetryPolicy), attemptRuntime)
		done <- result{outcome: outcome, err: err}
	}()
	select {
	case got := <-done:
		if got.outcome != lifecycleOutcomeError || !errors.Is(got.err, deliveryFailure) {
			t.Fatalf("delivery failure result = (%v, %v), want error outcome wrapping %v", got.outcome, got.err, deliveryFailure)
		}
	case <-time.After(time.Second):
		t.Fatal("delivery failure did not terminate the attempt")
	}
	select {
	case <-runnerStarted:
		t.Fatal("runner started after delivery had already failed")
	default:
	}
}

func TestRunContainsSetupAndSafeTaskPanicDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup SetupFunc
		want  typedPanic
	}{
		{
			name: "setup",
			want: typedPanic("setup value"),
			setup: func(context.Context) (Attempt, error) {
				panic(typedPanic("setup value"))
			},
		},
		{
			name: "safe task",
			want: typedPanic("task value"),
			setup: func(ctx context.Context) (Attempt, error) {
				if err := Go(ctx, func(context.Context) error { panic(typedPanic("task value")) }); err != nil {
					return Attempt{}, err
				}
				return Attempt{Runner: func(ctx context.Context) error {
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := runWithOptions(context.Background(), Config{
				Identity: testIdentity(),
				Modules: []Module{{
					Name:   "worker",
					Policy: Policy{Panic: OutcomePolicy{Action: Escalate}},
					Leaf:   &Leaf{Setup: tt.setup},
				}},
			}, immediateSupervisorOptions())
			var diagnostic *panicDiagnostic
			if !errors.As(err, &diagnostic) {
				t.Fatalf("runWithOptions() error = %v, want panic diagnostic", err)
			}
			if value, ok := diagnostic.value.(typedPanic); !ok || value != tt.want {
				t.Fatalf("panic value = %T(%v), want typedPanic(%v)", diagnostic.value, diagnostic.value, tt.want)
			}
			if len(diagnostic.stack) == 0 {
				t.Fatal("panic diagnostic has no stack")
			}
		})
	}
}

func TestRunUsesIndependentOutcomeBudgets(t *testing.T) {
	var attempts atomic.Int32
	err := runWithOptions(context.Background(), Config{
		Identity:  testIdentity(),
		Intensity: RestartBudget{Max: 10, Window: time.Hour},
		Modules: []Module{{
			Name: "worker",
			Policy: Policy{
				Error: OutcomePolicy{Budget: RestartBudget{Max: 1, Window: time.Hour}},
				Panic: OutcomePolicy{Budget: RestartBudget{Max: 1, Window: time.Hour}},
			},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				attempt := attempts.Add(1)
				return Attempt{Runner: func(context.Context) error {
					if attempt == 2 {
						panic("second")
					}
					return errors.New("failure")
				}}, nil
			}},
		}},
	}, immediateSupervisorOptions())
	if err == nil || attempts.Load() != 3 {
		t.Fatalf("runWithOptions() = (%d attempts, %v), want error after three attempts", attempts.Load(), err)
	}
}

func TestRunEnforcesAggregateIntensity(t *testing.T) {
	var attempts atomic.Int32
	err := runWithOptions(context.Background(), Config{
		Identity:  testIdentity(),
		Intensity: RestartBudget{Max: 1, Window: time.Hour},
		Modules: []Module{{
			Name: "worker",
			Policy: Policy{Error: OutcomePolicy{
				Budget: RestartBudget{Max: 10, Window: time.Hour},
			}},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				attempts.Add(1)
				return Attempt{Runner: func(context.Context) error { return errors.New("failure") }}, nil
			}},
		}},
	}, immediateSupervisorOptions())
	if err == nil || attempts.Load() != 2 {
		t.Fatalf("runWithOptions() = (%d attempts, %v), want aggregate exhaustion on second outcome", attempts.Load(), err)
	}
}

func TestRunBackoffCapHealthyResetAndCancellation(t *testing.T) {
	clock := newFakeSupervisorClock()
	var attempts atomic.Int32
	cfg := Config{
		Identity:  testIdentity(),
		Intensity: RestartBudget{Max: 10, Window: time.Hour},
		Modules: []Module{{
			Name: "worker",
			Policy: Policy{Error: OutcomePolicy{
				Budget:  RestartBudget{Max: 1, Window: time.Hour},
				Backoff: Backoff{Initial: time.Second, Maximum: 3 * time.Second, ResetAfter: 10 * time.Second},
			}},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				attempt := attempts.Add(1)
				return Attempt{Runner: func(context.Context) error {
					if attempt == 2 {
						clock.advance(10 * time.Second)
					}
					return errors.New("failure")
				}}, nil
			}},
		}},
	}
	options := supervisorOptions{clock: clock, jitter: func(limit time.Duration) time.Duration { return limit }}
	err := runWithOptions(context.Background(), cfg, options)
	if err == nil || attempts.Load() != 3 {
		t.Fatalf("healthy reset run = (%d attempts, %v), want third-outcome exhaustion", attempts.Load(), err)
	}
	if got := clock.delaysSnapshot(); !equalDurations(got, []time.Duration{time.Second, time.Second}) {
		t.Fatalf("healthy reset delays = %v, want [1s 1s]", got)
	}
	if got := exponentialBackoff(Backoff{Initial: time.Second, Maximum: 3 * time.Second}, 8); got != 3*time.Second {
		t.Fatalf("exponentialBackoff() = %v, want 3s cap", got)
	}

	blocking := newBlockingSupervisorClock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runWithOptions(ctx, Config{
			Identity: testIdentity(),
			Setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(context.Context) error { return errors.New("failure") }}, nil
			},
		}, supervisorOptions{clock: blocking, jitter: func(limit time.Duration) time.Duration { return limit }})
	}()
	<-blocking.waiting
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("cancellation during backoff error = %v", err)
	}
}

func TestSlowFailingSetupDoesNotResetHealthyBudget(t *testing.T) {
	clock := newFakeSupervisorClock()
	var attempts atomic.Int32
	err := runWithOptions(context.Background(), Config{
		Identity:  testIdentity(),
		Intensity: RestartBudget{Max: 10, Window: time.Hour},
		Modules: []Module{{
			Name: "worker",
			Policy: Policy{Error: OutcomePolicy{
				Budget:  RestartBudget{Max: 1, Window: time.Hour},
				Backoff: Backoff{Initial: time.Nanosecond, Maximum: time.Nanosecond, ResetAfter: 10 * time.Second},
			}},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				attempts.Add(1)
				clock.advance(10 * time.Second)
				return Attempt{}, errors.New("setup failed")
			}},
		}},
	}, supervisorOptions{clock: clock, jitter: func(time.Duration) time.Duration { return 0 }})
	if err == nil || attempts.Load() != 2 {
		t.Fatalf("slow setup run = (%d attempts, %v), want outcome budget exhaustion after two failures", attempts.Load(), err)
	}
}

func TestCancellationDuringBackoffPreservesCallerCause(t *testing.T) {
	clock := newBlockingSupervisorClock()
	cause := errors.New("caller stopped service")
	peerCause := make(chan error, 1)
	peerRunning := make(chan struct{})
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runWithOptions(ctx, Config{
			Identity: testIdentity(),
			Modules: []Module{
				{Name: "failing", Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					return Attempt{Runner: func(context.Context) error { return errors.New("failure") }}, nil
				}}},
				{Name: "peer", Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					return Attempt{Runner: func(ctx context.Context) error {
						close(peerRunning)
						<-ctx.Done()
						peerCause <- context.Cause(ctx)
						return ctx.Err()
					}}, nil
				}}},
			},
		}, supervisorOptions{clock: clock, jitter: func(limit time.Duration) time.Duration { return limit }})
	}()
	// Siblings start concurrently, so wait for the peer runner as well as the
	// failing module's backoff. Canceling before the peer runs would skip its
	// runner entirely and leave nothing to observe the cause.
	<-peerRunning
	<-clock.waiting
	cancel(cause)
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() cancellation error = %v", err)
	}
	if got := <-peerCause; !errors.Is(got, cause) {
		t.Fatalf("unaffected sibling cancellation cause = %v, want %v", got, cause)
	}
}

func TestCancellationDuringQuiesceDoesNotStartReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	peerStarted := make(chan struct{})
	var failingSetups atomic.Int32
	var peerSetups atomic.Int32
	err := runWithOptions(ctx, Config{
		Identity: testIdentity(),
		Strategy: OneForAll,
		Modules: []Module{
			{Name: "failing", Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				failingSetups.Add(1)
				return Attempt{Runner: func(context.Context) error {
					<-peerStarted
					return errors.New("failure")
				}}, nil
			}}},
			{Name: "peer", Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				peerSetups.Add(1)
				return Attempt{Runner: func(ctx context.Context) error {
					close(peerStarted)
					<-ctx.Done()
					if errors.Is(context.Cause(ctx), errSupervisorRestart) {
						cancel()
					}
					return ctx.Err()
				}}, nil
			}}},
		},
	}, supervisorOptions{})
	if err != nil {
		t.Fatalf("runWithOptions() cancellation error = %v", err)
	}
	if got := failingSetups.Load(); got != 1 {
		t.Errorf("failing module setups = %d, want 1", got)
	}
	if got := peerSetups.Load(); got != 1 {
		t.Errorf("peer module setups = %d, want 1", got)
	}
}

func TestRunFencesSimultaneousSiblingResults(t *testing.T) {
	release := make(chan struct{})
	var firstSetups atomic.Int32
	var secondSetups atomic.Int32
	startedReplacements := make(chan struct{}, 2)
	module := func(name string, setups *atomic.Int32) Module {
		return Module{
			Name: name,
			Policy: Policy{Error: OutcomePolicy{
				Budget: RestartBudget{Max: 1, Window: time.Hour},
			}},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				attempt := setups.Add(1)
				return Attempt{Runner: func(ctx context.Context) error {
					if attempt == 1 {
						<-release
						return errors.New("simultaneous")
					}
					startedReplacements <- struct{}{}
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			}},
		}
	}
	cfg := Config{
		Identity:  testIdentity(),
		Intensity: RestartBudget{Max: 1, Window: time.Hour},
		Strategy:  OneForAll,
		Modules: []Module{
			module("first", &firstSetups),
			module("second", &secondSetups),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithOptions(ctx, cfg, immediateSupervisorOptions()) }()
	close(release)
	for range 2 {
		<-startedReplacements
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() error = %v, stale result consumed intensity", err)
	}
	if firstSetups.Load() != 2 || secondSetups.Load() != 2 {
		t.Fatalf("setup calls = (%d, %d), want one shared reconstruction", firstSetups.Load(), secondSetups.Load())
	}
}

func TestRunKeepsStoppedSiblingInactive(t *testing.T) {
	var stoppedSetups atomic.Int32
	var retrySetups atomic.Int32
	replacementStarted := make(chan struct{})
	releaseRetry := make(chan struct{})
	stoppedProcessed := make(chan struct{})
	cfg := Config{
		Identity: testIdentity(),
		Strategy: OneForAll,
		Modules: []Module{
			{
				Name: "stopped",
				Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					stoppedSetups.Add(1)
					return Attempt{Runner: func(context.Context) error { return nil }}, nil
				}},
			},
			{
				Name: "retry",
				Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					attempt := retrySetups.Add(1)
					return Attempt{Runner: func(ctx context.Context) error {
						if attempt == 1 {
							<-releaseRetry
							return errors.New("retry")
						}
						close(replacementStarted)
						<-ctx.Done()
						return ctx.Err()
					}}, nil
				}},
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	options := immediateSupervisorOptions()
	options.transition = func(_ string, operation, modulePath string) {
		if operation == "wait" && modulePath == "edge/stopped" {
			close(stoppedProcessed)
		}
	}
	go func() { done <- runWithOptions(ctx, cfg, options) }()
	<-stoppedProcessed
	close(releaseRetry)
	<-replacementStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() error = %v", err)
	}
	if stoppedSetups.Load() != 1 {
		t.Fatalf("stopped sibling setup calls = %d, want 1", stoppedSetups.Load())
	}
}

func TestRunNestedEscalationResamplesBranchGates(t *testing.T) {
	cause := errors.New("nested cause")
	var probes atomic.Int32
	var attempts atomic.Int32
	replacementStarted := make(chan struct{})
	cfg := Config{
		Identity: testIdentity(),
		Modules: []Module{
			{
				Name:   "group",
				Policy: Policy{Error: OutcomePolicy{Action: Restart}},
				Branch: &Branch{Children: []Module{
					{
						Name: "worker",
						Gate: ProbeGate(func(context.Context) (bool, error) {
							probes.Add(1)
							return true, nil
						}),
						Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
						Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
							attempt := attempts.Add(1)
							return Attempt{Runner: func(ctx context.Context) error {
								if attempt == 1 {
									return cause
								}
								close(replacementStarted)
								<-ctx.Done()
								return ctx.Err()
							}}, nil
						}},
					},
				}},
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithOptions(ctx, cfg, immediateSupervisorOptions()) }()
	<-replacementStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() error = %v", err)
	}
	if probes.Load() != 2 {
		t.Fatalf("gate probes = %d, want startup plus owning-supervisor reconstruction", probes.Load())
	}
}

func TestLeafRetryRetainsTelemetryPolicySnapshot(t *testing.T) {
	localLogger, localSink := newRecordingLogger()
	exportLogger, exportSink := newRecordingLogger()
	var policyReads atomic.Int32
	var attempts atomic.Int32
	replacementStarted := make(chan struct{})
	options := immediateSupervisorOptions()
	options.lookup = func(key string) (string, bool) {
		if key != "FLOWSEER_EDGE_WORKER_TELEMETRY_LOGS_ENABLED" {
			return "", false
		}
		if policyReads.Add(1) == 1 {
			return "false", true
		}
		return "true", true
	}
	config := Config{
		Identity:   testIdentity(),
		Logger:     localLogger,
		LogHandler: exportLogger.Handler(),
		Modules: []Module{{
			Name: "worker",
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				attempt := attempts.Add(1)
				return Attempt{Runner: func(ctx context.Context) error {
					Logger(ctx).InfoContext(ctx, fmt.Sprintf("attempt %d", attempt))
					if attempt == 1 {
						return errors.New("retry")
					}
					close(replacementStarted)
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			}},
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithOptions(ctx, config, options) }()
	<-replacementStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() error: %v", err)
	}
	if got := policyReads.Load(); got != 1 {
		t.Fatalf("leaf policy reads = %d, want one owning-generation snapshot", got)
	}
	if _, ok := localSink.find("attempt 1"); !ok {
		t.Fatal("first attempt did not reach local logger")
	}
	if _, ok := localSink.find("attempt 2"); !ok {
		t.Fatal("replacement attempt did not reach local logger")
	}
	if _, ok := exportSink.find("attempt 1"); ok {
		t.Fatal("first attempt reached export handler")
	}
	if _, ok := exportSink.find("attempt 2"); ok {
		t.Fatal("leaf retry resampled its telemetry policy")
	}
}

func TestBranchReconstructionResamplesDescendantTelemetryAndRetainsBranch(t *testing.T) {
	localLogger, _ := newRecordingLogger()
	exportLogger, exportSink := newRecordingLogger()
	var branchPolicyReads atomic.Int32
	var leafPolicyReads atomic.Int32
	var attempts atomic.Int32
	replacementStarted := make(chan struct{})
	options := immediateSupervisorOptions()
	options.lookup = func(key string) (string, bool) {
		switch key {
		case "FLOWSEER_EDGE_GROUP_TELEMETRY_LOGS_ENABLED":
			branchPolicyReads.Add(1)
			return "false", true
		case "FLOWSEER_EDGE_GROUP_WORKER_TELEMETRY_LOGS_ENABLED":
			if leafPolicyReads.Add(1) == 1 {
				return "false", true
			}
			return "true", true
		default:
			return "", false
		}
	}
	config := Config{
		Identity:   testIdentity(),
		Logger:     localLogger,
		LogHandler: exportLogger.Handler(),
		Modules: []Module{{
			Name:   "group",
			Policy: Policy{Error: OutcomePolicy{Action: Restart}},
			Branch: &Branch{Children: []Module{{
				Name:   "worker",
				Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
				Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					attempt := attempts.Add(1)
					return Attempt{Runner: func(ctx context.Context) error {
						Logger(ctx).InfoContext(ctx, fmt.Sprintf("branch attempt %d", attempt))
						if attempt == 1 {
							return errors.New("reconstruct")
						}
						close(replacementStarted)
						<-ctx.Done()
						return ctx.Err()
					}}, nil
				}},
			}}},
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithOptions(ctx, config, options) }()
	<-replacementStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() error: %v", err)
	}
	if got := branchPolicyReads.Load(); got != 1 {
		t.Fatalf("branch policy reads = %d, want retained parent snapshot", got)
	}
	if got := leafPolicyReads.Load(); got != 2 {
		t.Fatalf("descendant policy reads = %d, want startup plus branch reconstruction", got)
	}
	if _, ok := exportSink.find("branch attempt 1"); ok {
		t.Fatal("initial disabled descendant reached export handler")
	}
	if _, ok := exportSink.find("branch attempt 2"); !ok {
		t.Fatal("reconstructed enabled descendant did not reach export handler")
	}
}

func TestBranchReconstructionTelemetryConfigFailureIsFatal(t *testing.T) {
	for _, tt := range []struct {
		name       string
		logHandler slog.Handler
		second     string
		category   string
	}{
		{name: "malformed", logHandler: slog.DiscardHandler, second: "yes", category: "malformed"},
		{name: "unavailable", second: "true", category: "unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var policyReads atomic.Int32
			var attempts atomic.Int32
			options := immediateSupervisorOptions()
			options.lookup = func(key string) (string, bool) {
				if key != "FLOWSEER_EDGE_GROUP_WORKER_TELEMETRY_LOGS_ENABLED" {
					return "", false
				}
				if policyReads.Add(1) == 1 {
					return "false", true
				}
				return tt.second, true
			}
			err := runWithOptions(context.Background(), Config{
				Identity:   testIdentity(),
				Logger:     slog.New(slog.DiscardHandler),
				LogHandler: tt.logHandler,
				Modules: []Module{{
					Name:   "group",
					Policy: Policy{Error: OutcomePolicy{Action: Restart, Budget: RestartBudget{Max: 10, Window: time.Hour}}},
					Branch: &Branch{Children: []Module{{
						Name:   "worker",
						Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
						Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
							attempts.Add(1)
							return Attempt{Runner: func(context.Context) error { return errors.New("reconstruct") }}, nil
						}},
					}}},
				}},
			}, options)
			assertTelemetryConfigError(t, err, "FLOWSEER_EDGE_GROUP_WORKER_TELEMETRY_LOGS_ENABLED", tt.category, tt.second)
			if got := policyReads.Load(); got != 2 {
				t.Fatalf("policy reads = %d, want startup plus one reconstruction", got)
			}
			if got := attempts.Load(); got != 1 {
				t.Fatalf("attempts = %d, want no restart loop after fatal configuration", got)
			}
		})
	}
}

func TestRunNestedEscalationPreservesCause(t *testing.T) {
	cause := errors.New("nested cause")
	err := runWithOptions(context.Background(), Config{
		Identity: testIdentity(),
		Modules: []Module{{
			Name:   "group",
			Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
			Branch: &Branch{Children: []Module{{
				Name:   "worker",
				Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
				Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					return Attempt{Runner: func(context.Context) error { return cause }}, nil
				}},
			}}},
		}},
	}, immediateSupervisorOptions())
	if !errors.Is(err, cause) {
		t.Fatalf("runWithOptions() error = %v, want nested cause", err)
	}
}

type typedPanic string

type fakeSupervisorClock struct {
	mu     sync.Mutex
	now    time.Time
	delays []time.Duration
}

func newFakeSupervisorClock() *fakeSupervisorClock {
	return &fakeSupervisorClock{now: time.Unix(100, 0)}
}

func (c *fakeSupervisorClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeSupervisorClock) Wait(_ context.Context, delay time.Duration) error {
	c.mu.Lock()
	c.delays = append(c.delays, delay)
	c.now = c.now.Add(delay)
	c.mu.Unlock()
	return nil
}

func (c *fakeSupervisorClock) advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func (c *fakeSupervisorClock) delaysSnapshot() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

type blockingSupervisorClock struct {
	waiting chan struct{}
}

func newBlockingSupervisorClock() *blockingSupervisorClock {
	return &blockingSupervisorClock{waiting: make(chan struct{})}
}

func (*blockingSupervisorClock) Now() time.Time { return time.Unix(100, 0) }

func (c *blockingSupervisorClock) Wait(ctx context.Context, _ time.Duration) error {
	close(c.waiting)
	<-ctx.Done()
	return ctx.Err()
}

func equalDurations(left, right []time.Duration) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestGoRejectsUnmanagedAndCanceledContexts(t *testing.T) {
	if err := Go(context.Background(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("Go() outside attempt succeeded")
	}

	checked := make(chan error, 1)
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{Identity: testIdentity(), Setup: func(context.Context) (Attempt, error) {
		return Attempt{Runner: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			checked <- Go(ctx, func(context.Context) error { return nil })
			return ctx.Err()
		}}, nil
	}}
	done := make(chan error, 1)
	go func() { done <- runWithOptions(ctx, cfg, immediateSupervisorOptions()) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithOptions() shutdown error = %v", err)
	}
	if err := <-checked; err == nil {
		t.Fatal("Go() after cancellation succeeded")
	}
}

type lifecycleRecorder struct {
	mu     sync.Mutex
	events []string
	notify chan struct{}
}

func newLifecycleRecorder() *lifecycleRecorder {
	return &lifecycleRecorder{notify: make(chan struct{}, 64)}
}

func (r *lifecycleRecorder) record(event string) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	r.notify <- struct{}{}
}

func (r *lifecycleRecorder) waitForStarts(t *testing.T, count int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		r.mu.Lock()
		n := len(r.events)
		r.mu.Unlock()
		if n >= count {
			return
		}
		select {
		case <-r.notify:
		case <-deadline.C:
			t.Fatalf("timed out after %d events", n)
		}
	}
}

func (r *lifecycleRecorder) reset() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

func (r *lifecycleRecorder) prefix(count int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) < count {
		return append([]string(nil), r.events...)
	}
	return append([]string(nil), r.events[:count]...)
}

func immediateSupervisorOptions() supervisorOptions {
	return supervisorOptions{
		clock:  realSupervisorClock{},
		jitter: func(time.Duration) time.Duration { return 0 },
	}
}
