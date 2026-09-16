package host

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TestARunAllLoopPanicIsTheAttemptsError is evidence for the converted
// goroutine in runAll: it forces a real panic in one loop and checks that
// runAll returns that panic as its error rather than nil. Before the sink
// was wired, a panicking loop never reached the `failures <- err` send, and
// the coordinator's select on an empty channel read that as every loop
// having returned cleanly — a crash silently became a report of success.
func TestARunAllLoopPanicIsTheAttemptsError(t *testing.T) {
	other := func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}
	panicking := func(context.Context) error {
		panic("a loop fell over")
	}

	done := make(chan error, 1)
	go func() {
		done <- runAll(context.Background(), other, panicking)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("runAll returned nil after a loop panicked, want the panic reported as an error")
		}
		if errs.Attributes(err)["panic"] == nil {
			t.Errorf("runAll's error carries no recovered panic value: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runAll never returned after a loop panicked")
	}
}
