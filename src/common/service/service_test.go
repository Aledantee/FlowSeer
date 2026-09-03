package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
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
