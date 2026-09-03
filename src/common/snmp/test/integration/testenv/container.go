//go:build snmp_integration_t1 || snmp_integration_t3 || snmp_integration_t5

package testenv

import (
	"context"
	"time"

	"github.com/testcontainers/testcontainers-go"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Startup contexts may already be canceled when partial resources need removal.
func terminateContainer(ctr testcontainers.Container) error {
	if ctr == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return errs.Wrap(ctr.Terminate(ctx), "terminate test container")
}
