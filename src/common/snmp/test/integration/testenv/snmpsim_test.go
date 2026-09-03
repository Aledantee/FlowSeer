//go:build snmp_integration_t3 || snmp_integration_t5

package testenv

import (
	"context"
	"errors"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func TestSnmpsimReadinessCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForSnmpsimReady(ctx, "invalid:port", "fixture"); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestSnmpsimPartialStartupCleanup(t *testing.T) {
	startErr := errors.New("startup failed")
	stopErr := errors.New("termination failed")
	c := &failedContainer{err: stopErr}
	before := startSnmpsimContainer
	t.Cleanup(func() { startSnmpsimContainer = before })
	startSnmpsimContainer = func(context.Context, testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
		return c, startErr
	}
	_, cleanup, err := StartSnmpsim(context.Background(), t.TempDir(), t.TempDir(), "fixture")
	if cleanup != nil {
		t.Error("failed startup returned a cleanup callback")
		if err := cleanup(); err != nil {
			t.Errorf("unexpected callback cleanup: %v", err)
		}
	}
	if !errors.Is(err, startErr) || !errors.Is(err, stopErr) {
		t.Errorf("got %v, want both startup and termination failures", err)
	}
	assertTerminated(t, c)
}
