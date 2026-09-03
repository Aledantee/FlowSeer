//go:build yang_integration_t1

package testenv

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
)

type failedContainer struct {
	testcontainers.Container
	terminated bool
	bounded    bool
	active     bool
	err        error
	hostErr    error
	portErr    error
}

func (c *failedContainer) Host(context.Context) (string, error) {
	return "localhost", c.hostErr
}

func (c *failedContainer) MappedPort(context.Context, string) (network.Port, error) {
	return network.MustParsePort("830/tcp"), c.portErr
}

func (c *failedContainer) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	c.terminated = true
	_, c.bounded = ctx.Deadline()
	c.active = ctx.Err() == nil
	return c.err
}

func TestContainerAddressFailurePreservesCleanupError(t *testing.T) {
	addressErr := errors.New("address unavailable")
	cleanupErr := errors.New("termination failed")
	for _, tt := range []struct {
		name string
		ctr  *failedContainer
	}{
		{name: "host", ctr: &failedContainer{hostErr: addressErr, err: cleanupErr}},
		{name: "mapped port", ctr: &failedContainer{portErr: addressErr, err: cleanupErr}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, cleanup, err := startContainer(t.Context(), "unused", "830/tcp", nil,
				func(context.Context, testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
					return tt.ctr, nil
				})
			if target != "" || cleanup != nil || !errors.Is(err, addressErr) || !errors.Is(err, cleanupErr) {
				t.Fatalf("got target=%q cleanup-present=%t error=%v, want both errors", target, cleanup != nil, err)
			}
			if !tt.ctr.terminated || !tt.ctr.active || !tt.ctr.bounded {
				t.Errorf("cleanup terminated=%t active=%t bounded=%t, want all true", tt.ctr.terminated, tt.ctr.active, tt.ctr.bounded)
			}
		})
	}
}

func TestStartedContainerCleanupReturnsError(t *testing.T) {
	cleanupErr := errors.New("termination failed")
	ctr := &failedContainer{err: cleanupErr}
	target, cleanup, err := startContainer(t.Context(), "unused", "830/tcp", nil,
		func(context.Context, testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
			return ctr, nil
		})
	if err != nil || target != "localhost:830" || cleanup == nil {
		t.Fatalf("got target=%q cleanup-present=%t error=%v, want ready container", target, cleanup != nil, err)
	}
	if ctr.terminated {
		t.Fatal("container terminated before cleanup")
	}
	if err := cleanup(); !errors.Is(err, cleanupErr) {
		t.Errorf("cleanup error = %v, want %v", err, cleanupErr)
	}
	if !ctr.terminated || !ctr.active || !ctr.bounded {
		t.Errorf("cleanup terminated=%t active=%t bounded=%t, want all true", ctr.terminated, ctr.active, ctr.bounded)
	}
}

func TestFailedContainerStartCleanup(t *testing.T) {
	startErr := errors.New("startup failed")
	cleanupErr := errors.New("termination failed")
	for _, tt := range []struct {
		name string
		ctr  *failedContainer
	}{
		{name: "no container"},
		{name: "partial container", ctr: &failedContainer{}},
		{name: "cleanup failure", ctr: &failedContainer{err: cleanupErr}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			target, cleanup, err := startContainer(ctx, "unused", "830/tcp", nil,
				func(context.Context, testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
					if tt.ctr == nil {
						return nil, startErr
					}
					return tt.ctr, startErr
				})
			if target != "" || cleanup != nil || !errors.Is(err, startErr) {
				t.Fatalf("got target=%q cleanup-present=%t error=%v, want startup failure", target, cleanup != nil, err)
			}
			if tt.ctr == nil {
				return
			}
			if !tt.ctr.terminated || !tt.ctr.active || !tt.ctr.bounded {
				t.Errorf("cleanup terminated=%t active=%t bounded=%t, want all true", tt.ctr.terminated, tt.ctr.active, tt.ctr.bounded)
			}
			if tt.ctr.err != nil && !errors.Is(err, tt.ctr.err) {
				t.Errorf("got error %v, want cleanup failure", err)
			}
		})
	}
}
