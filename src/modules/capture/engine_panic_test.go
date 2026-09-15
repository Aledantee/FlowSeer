package capture

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

// panicSource panics from its very first call, simulating a failure on the
// frame the converted goroutine's first statement makes.
type panicSource struct{}

func (panicSource) Receive(context.Context) <-chan rawsocket.Frame {
	panic("simulated source panic")
}

func (panicSource) Stats() (uint64, uint64, error) { return 0, 0, nil }

func (panicSource) Close() error { return nil }

// TestEngineRunPanicRecordsErrorAndClosesPump is evidence for this change:
// it forces a real panic on the goroutine Run starts (Source.Receive, not a
// manufactured error), then asserts p.Err() is already set at the moment
// p.Data() is observed closed — not merely eventually, since p.Fail records
// the error before it closes the channel. Before the conversion, this panic
// took the whole process down; recovered instead, a consumer ranging over
// Data() must see a reported failure rather than what looks like a clean,
// empty finish.
func TestEngineRunPanicRecordsErrorAndClosesPump(t *testing.T) {
	e := newEngine(panicSource{}, testBudget(10), false)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, stillOpen := <-p.Data(); stillOpen {
		t.Fatal("expected p.Data() to close after Run's goroutine panicked")
	}
	if p.Err() == nil {
		t.Fatal("p.Err() is nil once p.Data() closed; the panic was reported as a clean finish")
	}
}
