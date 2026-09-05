//go:build snmp_integration_t1 || snmp_integration_t2 || snmp_integration_t3 || snmp_integration_t5

package testenv

import (
	"context"
	"errors"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// waitReady polls probe until it succeeds, ctx is canceled, or timeout
// elapses, pausing backoff between attempts. probe runs against a context
// bounded by timeout, so its own dials inherit the readiness deadline. The
// pause observes cancellation, so an expired or canceled context returns
// within one probe rather than one full backoff after the deadline. On
// timeout the last probe failure is joined with the deadline error, wrapped
// "probe exhausted"; a success returns nil.
func waitReady(ctx context.Context, timeout, backoff time.Duration, probe func(context.Context) error) error {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		if err := probeCtx.Err(); err != nil {
			return err
		}
		lastErr = probe(probeCtx)
		if lastErr == nil {
			return nil
		}

		// Sleep with context-cancellation observation. Sleeping
		// unconditionally costs up to one full backoff interval of
		// dead time after the outer deadline fires.
		select {
		case <-time.After(backoff):
		case <-probeCtx.Done():
			if lastErr == nil {
				return probeCtx.Err()
			}
			return errors.Join(probeCtx.Err(), errs.Wrap(lastErr, "probe exhausted"))
		}
	}
}
