package gnmi_test

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/pump"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// TestSubscribeCancelWatcherCancelsAfterAPanic reproduces the cancel
// watcher Subscribe starts alongside its receive goroutine: on pump.Stopped,
// it must cancel the stream context so a blocked Recv can return, or Close
// hangs waiting for the receive goroutine to notice sctx is done. cancel is
// deferred so that guarantee survives a panic in the watcher itself.
func TestSubscribeCancelWatcherCancelsAfterAPanic(t *testing.T) {
	p := pump.New[int](context.Background(), 1)
	defer p.Cancel()

	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spawn.Go(sctx, "test subscribe cancel watcher", func() {
		defer cancel()
		select {
		case <-p.Stopped():
			panic("boom")
		case <-sctx.Done():
		}
	})

	p.SignalStop()

	select {
	case <-sctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("sctx was never canceled after a panic in the cancel watcher")
	}
}

// TestSubscribeReceiveReportsPanicToThePump reproduces the receive
// goroutine's failure sink: a silent, deferred pump.Done would otherwise
// leave Stream.Err() nil after a panic, indistinguishable from a stream
// that finished cleanly.
func TestSubscribeReceiveReportsPanicToThePump(t *testing.T) {
	p := pump.New[int](context.Background(), 1)
	defer p.Cancel()

	spawn.Go(context.Background(), "test subscribe receive", func() {
		defer p.Done()
		panic("boom")
	}, spawn.ReportTo(p.Fail))

	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("pump never recorded an error after the receive goroutine panicked")
		case <-ticker.C:
			if p.Err() != nil {
				return
			}
		}
	}
}
