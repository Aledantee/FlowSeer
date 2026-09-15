package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRunCancelsAndWaitsBeforeTelemetryShutdown(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	shutdown := make(chan struct{})
	cfg := Config{
		Identity: testIdentity(),
		Setup: func(ctx context.Context) (Attempt, error) {
			if got := ModulePath(ctx); got != "edge" {
				t.Errorf("setup module path = %q, want %q", got, "edge")
			}
			return Attempt{Runner: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(released)
				return ctx.Err()
			}}, nil
		},
		TelemetryShutdown: func(context.Context) error {
			select {
			case <-released:
			default:
				t.Error("telemetry shut down before the runner returned")
			}
			close(shutdown)
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()

	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run() error = %v, want nil shutdown", err)
	}
	select {
	case <-shutdown:
	default:
		t.Fatal("telemetry shutdown was not called")
	}
}

func TestAttemptCapabilitiesReachRunnerTaskAndHandler(t *testing.T) {
	workerReady := make(chan struct{})
	runnerSeen := make(chan struct{}, 1)
	taskSeen := make(chan struct{}, 1)
	handlerSeen := make(chan struct{}, 1)
	check := func(ctx context.Context, wantPath string, seen chan<- struct{}) error {
		if ModulePath(ctx) != wantPath {
			return errors.New("attempt module path is unavailable")
		}
		if Logger(ctx) == defaultLogger {
			return errors.New("attempt logger is unavailable")
		}
		attributes := Attributes(ctx)
		wantAttributes := map[attribute.Key]string{
			"service.name":      "context_runtime",
			"service.namespace": "flowseer",
			"service.version":   "1.0.0",
			legacyModulePathKey: wantPath,
		}
		if attributes.Len() != len(wantAttributes) {
			return fmt.Errorf("attempt attribute count = %d, want %d", attributes.Len(), len(wantAttributes))
		}
		for key, want := range wantAttributes {
			got, ok := attributes.Value(key)
			if !ok {
				return fmt.Errorf("attempt attribute %s is missing", key)
			}
			if got.AsString() != want {
				return fmt.Errorf("attempt attribute %s = %q, want %q", key, got.AsString(), want)
			}
		}
		if Bus(ctx) == disabledMessageBus {
			return errors.New("attempt bus is unavailable")
		}
		seen <- struct{}{}
		return nil
	}
	cfg := Config{
		Identity: Identity{Namespace: "flowseer", Name: "context_runtime", Version: "1.0.0"},
		Bus:      periodicBusConfig(filepath.Join(t.TempDir(), "bus")),
		Modules: []Module{
			{Name: "publisher", Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(ctx context.Context) error {
					<-workerReady
					if err := check(ctx, "context_runtime/publisher", runnerSeen); err != nil {
						return err
					}
					return Bus(ctx).Command(ctx, "context_runtime/worker", &emptypb.Empty{})
				}}, nil
			}}},
			{Name: "worker", Leaf: &Leaf{
				Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}},
				Setup: func(ctx context.Context) (Attempt, error) {
					if err := Go(ctx, func(ctx context.Context) error {
						if err := check(ctx, "context_runtime/worker", taskSeen); err != nil {
							return err
						}
						<-ctx.Done()
						return ctx.Err()
					}); err != nil {
						return Attempt{}, err
					}
					close(workerReady)
					return Attempt{
						Runner: func(ctx context.Context) error {
							<-ctx.Done()
							return ctx.Err()
						},
						Handlers: []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(ctx context.Context, _ proto.Message) error {
							return check(ctx, "context_runtime/worker", handlerSeen)
						}}},
					}, nil
				},
			}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	for name, seen := range map[string]<-chan struct{}{"runner": runnerSeen, "task": taskSeen, "handler": handlerSeen} {
		select {
		case <-seen:
		case <-ctx.Done():
			t.Fatalf("%s did not observe attempt capabilities: %v", name, ctx.Err())
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error: %v", err)
	}
}

func TestRunWithSignalChannelCancelsService(t *testing.T) {
	started := make(chan struct{})
	signals := make(chan os.Signal, 1)
	cfg := Config{
		Identity: testIdentity(),
		Setup: func(context.Context) (Attempt, error) {
			return Attempt{Runner: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- runWithSignalChannel(context.Background(), cfg, signals) }()

	<-started
	signals <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatalf("runWithSignalChannel() error = %v, want nil", err)
	}
}

func TestRunContainsSetupAndRunnerPanics(t *testing.T) {
	tests := []struct {
		name  string
		setup SetupFunc
	}{
		{
			name: "setup panic",
			setup: func(context.Context) (Attempt, error) {
				panic("setup boom")
			},
		},
		{
			name: "runner panic",
			setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(context.Context) error { panic("runner boom") }}, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(context.Background(), Config{
				Identity: testIdentity(),
				Modules: []Module{{
					Name:   "worker",
					Policy: Policy{Error: OutcomePolicy{Action: Escalate}, Panic: OutcomePolicy{Action: Escalate}},
					Leaf:   &Leaf{Setup: tt.setup},
				}},
			})
			if err == nil {
				t.Fatal("run() succeeded, want contained panic error")
			}
		})
	}
}

func TestRunStartsExplicitModulesConcurrently(t *testing.T) {
	var mu sync.Mutex
	started := make(map[string]bool)
	allStarted := make(chan struct{})
	makeSetup := func(name string) SetupFunc {
		return func(context.Context) (Attempt, error) {
			return Attempt{Runner: func(ctx context.Context) error {
				mu.Lock()
				started[name] = true
				if len(started) == 2 {
					close(allStarted)
				}
				mu.Unlock()
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		}
	}
	cfg := Config{
		Identity: testIdentity(),
		Modules: []Module{
			{Name: "first", Leaf: &Leaf{Setup: makeSetup("first")}},
			{Name: "second", Leaf: &Leaf{Setup: makeSetup("second")}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()

	<-allStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run() error = %v, want nil", err)
	}
}

// TestRunReturnsInsteadOfBlockingOnPanicBeforeStarted is evidence for this
// change: runWithStarted schedules every active slot before it closes
// started, so a panic recovered mid-scheduling never reaches that close.
// Watched against a conversion missing the started/done select, this test
// hung until its own timeout instead of returning — see the report for that
// run's output. options.transition runs synchronously inside the scheduling
// loop, before started can close, so panicking there reproduces the case
// without depending on any particular module's Setup or Runner.
func TestRunReturnsInsteadOfBlockingOnPanicBeforeStarted(t *testing.T) {
	cfg := Config{
		Identity: testIdentity(),
		Modules: []Module{{
			Name: "worker",
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(ctx context.Context) error {
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			}},
		}},
	}
	options := immediateSupervisorOptions()
	options.transition = func(_, operation, _ string) {
		if operation == "setup" {
			panic(typedPanic("scheduling panic"))
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- runWithOptionsAndTelemetryFactories(context.Background(), cfg, options, defaultTelemetryFactories)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("runWithOptionsAndTelemetryFactories() = nil, want the recovered scheduling panic")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWithOptionsAndTelemetryFactories() blocked on <-started instead of returning")
	}
}

func TestRunReturnsSetupAndShutdownErrors(t *testing.T) {
	errSetup := errors.New("setup failed")
	errShutdown := errors.New("shutdown failed")
	err := run(context.Background(), Config{
		Identity: testIdentity(),
		Modules: []Module{{
			Name:   "worker",
			Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				return Attempt{}, errSetup
			}},
		}},
		TelemetryShutdown: func(context.Context) error { return errShutdown },
	})
	if !errors.Is(err, errSetup) || !errors.Is(err, errShutdown) {
		t.Errorf("run() error = %v, want setup and shutdown causes", err)
	}
}
