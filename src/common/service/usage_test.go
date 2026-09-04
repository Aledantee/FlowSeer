package service

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// These tests exercise the shapes a FlowSeer service author is expected to
// write, through the exported API only.

// startedPaths records the module paths that reached their runner.
type startedPaths struct {
	mu    sync.Mutex
	paths []string
}

func (s *startedPaths) add(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
}

func (s *startedPaths) sorted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(slices.Values(s.paths))
}

// blockingLeaf declares a leaf that reports its module path and then runs until
// the service stops it.
func blockingLeaf(name string, started *startedPaths, ready func()) Module {
	return Module{Name: name, Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
		return Attempt{Runner: func(ctx context.Context) error {
			started.add(ModulePath(ctx))
			ready()
			<-ctx.Done()
			return ctx.Err()
		}}, nil
	}}}
}

func TestEdgeAgentShapeRunsOnlyItsGatedModules(t *testing.T) {
	// The operator disables one ingest leaf and the whole uplink subtree.
	t.Setenv("FLOWSEER_EDGE_INGEST_SNMP_ENABLED", "false")
	t.Setenv("FLOWSEER_EDGE_UPLINK_ENABLED", "false")
	t.Setenv("FLOWSEER_EDGE_LISTEN_ADDRESS", ":5514")

	started := &startedPaths{}
	var ready sync.WaitGroup
	ready.Add(2)
	once := make(map[string]*sync.Once, 2)
	for _, name := range []string{"edge/ingest/syslog", "edge/api"} {
		once[name] = &sync.Once{}
	}
	done := func(path string) func() {
		return func() { once[path].Do(ready.Done) }
	}

	var listenAddress string
	config := Config{
		Identity: Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Modules: []Module{
			{Name: "ingest", Branch: &Branch{Children: []Module{
				{Name: "syslog", Leaf: &Leaf{Setup: func(ctx context.Context) (Attempt, error) {
					listenAddress, _ = LookupEnv(ctx, "LISTEN_ADDRESS")
					return Attempt{Runner: func(ctx context.Context) error {
						started.add(ModulePath(ctx))
						done("edge/ingest/syslog")()
						<-ctx.Done()
						return ctx.Err()
					}}, nil
				}}},
				// A probe gate must not run once the environment decides.
				{Name: "snmp", Gate: ProbeGate(func(context.Context) (bool, error) {
					t.Error("the snmp gate probe ran despite an environment override")
					return true, nil
				}), Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
					t.Error("the disabled snmp module was set up")
					return Attempt{Runner: func(context.Context) error { return nil }}, nil
				}}},
			}}},
			blockingLeaf("api", started, done("edge/api")),
			{Name: "uplink", Branch: &Branch{Children: []Module{
				blockingLeaf("bridge", started, func() {}),
			}}},
		},
	}

	waited := make(chan struct{})
	go func() {
		ready.Wait()
		close(waited)
	}()
	if err := runUntil(t, config, waited); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	want := []string{"edge/api", "edge/ingest/syslog"}
	if got := started.sorted(); !slices.Equal(got, want) {
		t.Errorf("started modules = %v, want %v", got, want)
	}
	if listenAddress != ":5514" {
		t.Errorf("LookupEnv(LISTEN_ADDRESS) = %q, want %q", listenAddress, ":5514")
	}
}

func TestConcurrentServicesKeepIndependentIdentity(t *testing.T) {
	newService := func(name string) (Config, chan string, chan struct{}) {
		observed := make(chan string, 1)
		ready := make(chan struct{})
		return Config{
			Identity: Identity{Name: name, Namespace: "flowseer", Version: "v1"},
			Setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(ctx context.Context) error {
					observed <- Name(ctx) + "|" + ModulePath(ctx) + "|" + EnvPrefix(ctx)
					close(ready)
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			},
		}, observed, ready
	}

	first, firstObserved, firstReady := newService("collector")
	second, secondObserved, secondReady := newService("gateway")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	go func() { errs <- run(ctx, first) }()
	go func() { errs <- run(ctx, second) }()

	for _, ready := range []chan struct{}{firstReady, secondReady} {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatalf("a concurrent service never started: %v", ctx.Err())
		}
	}
	if got := <-firstObserved; got != "collector|collector|FLOWSEER_COLLECTOR_" {
		t.Errorf("first service context = %q", got)
	}
	if got := <-secondObserved; got != "gateway|gateway|FLOWSEER_GATEWAY_" {
		t.Errorf("second service context = %q", got)
	}

	cancel()
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("run() error: %v", err)
		}
	}
}

func TestRunStartsNoModuleWhenTheCallerHasAlreadyStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := run(ctx, Config{
		Identity: Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Setup: func(context.Context) (Attempt, error) {
			t.Error("setup ran for a canceled service")
			return Attempt{Runner: func(context.Context) error { return nil }}, nil
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v, want nil", err)
	}
}

func TestFailedAttemptReleasesItsResourcesBeforeTheReplacement(t *testing.T) {
	// The documented pattern: setup constructs, the runner owns cleanup. The
	// runtime must not overlap a failed attempt with its replacement.
	var mu sync.Mutex
	var events []string
	attempts := 0
	stopped := make(chan struct{})
	errAttempt := errors.New("attempt failed")

	config := Config{
		Identity: Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Modules: []Module{{
			Name: "collector",
			Policy: Policy{
				Error:  OutcomePolicy{Action: Restart, Backoff: Backoff{Initial: time.Millisecond, Maximum: time.Millisecond, ResetAfter: time.Minute}},
				Normal: OutcomePolicy{Action: Stop},
			},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				mu.Lock()
				attempts++
				attempt := attempts
				events = append(events, "open")
				mu.Unlock()
				return Attempt{Runner: func(context.Context) error {
					mu.Lock()
					events = append(events, "close")
					mu.Unlock()
					if attempt == 1 {
						return errAttempt
					}
					close(stopped)
					return nil
				}}, nil
			}},
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, config) }()
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatalf("the module was never reconstructed: %v", ctx.Err())
	}
	if err := <-done; err != nil {
		t.Fatalf("run() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"open", "close", "open", "close"}
	if !slices.Equal(events, want) {
		t.Errorf("resource events = %v, want %v", events, want)
	}
}
