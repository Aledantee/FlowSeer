package testenv

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadinessSuccess(t *testing.T) {
	err := waitReady(t.Context(), func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > readyTimeout {
			t.Error("readiness probe has no bounded deadline")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadinessCanceledBeforeProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := waitReady(ctx, func(context.Context) error {
		t.Fatal("probe called after cancellation")
		return nil
	})
	if err != context.Canceled {
		t.Fatalf("got error %v, want context.Canceled", err)
	}
}

func TestReadinessCancellation(t *testing.T) {
	for _, activeProbe := range []bool{false, true} {
		name := "backoff"
		if activeProbe {
			name = "active probe"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			started := make(chan struct{}, 1)
			done := make(chan error, 1)
			go func() {
				done <- waitReady(ctx, func(probeCtx context.Context) error {
					select {
					case started <- struct{}{}:
					default:
					}
					if activeProbe {
						<-probeCtx.Done()
						return probeCtx.Err()
					}
					return errors.New("not ready")
				})
			}()
			<-started
			cancel()
			select {
			case err := <-done:
				if err != context.Canceled {
					t.Fatalf("got error %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("readiness did not stop after cancellation")
			}
		})
	}
}
