package testenv

import (
	"context"
	"errors"
	"time"
)

const (
	readyTimeout = 60 * time.Second
	probeBackoff = 500 * time.Millisecond
)

func waitReady(ctx context.Context, probe func(context.Context) error) error {
	probeCtx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := probeCtx.Err(); err != nil {
			return errors.Join(err, lastErr)
		}
		lastErr = probe(probeCtx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := probeCtx.Err(); err != nil {
			return errors.Join(err, lastErr)
		}
		if lastErr == nil {
			return nil
		}

		select {
		case <-probeCtx.Done():
		case <-time.After(probeBackoff):
		}
	}
}
