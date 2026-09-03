//go:build snmp_integration_t1 || snmp_integration_t3 || snmp_integration_t5

package testenv

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

type failedContainer struct {
	testcontainers.Container
	terminated bool
	bounded    bool
	err        error
}

func (c *failedContainer) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	c.terminated = true
	_, c.bounded = ctx.Deadline()
	return c.err
}

func assertTerminated(t *testing.T, c *failedContainer) {
	t.Helper()
	if !c.terminated {
		t.Error("partially started container was not terminated")
	}
	if !c.bounded {
		t.Error("termination has no context deadline")
	}
}
