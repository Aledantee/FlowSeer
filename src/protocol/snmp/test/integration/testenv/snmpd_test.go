//go:build snmp_integration_t1

package testenv

import (
	"context"
	"errors"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func TestSnmpdReadinessCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForSnmpdReady(ctx, "invalid:port"); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestSnmpdPartialStartupCleanup(t *testing.T) {
	startErr := errors.New("startup failed")
	stopErr := errors.New("termination failed")
	c := &failedContainer{err: stopErr}
	before := startSnmpdContainer
	t.Cleanup(func() { startSnmpdContainer = before })
	startSnmpdContainer = func(context.Context, testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
		return c, startErr
	}
	_, cleanup, err := StartSnmpd(context.Background(), t.TempDir())
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
