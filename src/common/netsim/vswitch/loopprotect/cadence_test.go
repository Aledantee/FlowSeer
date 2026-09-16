package loopprotect_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
)

// TestWakeEmitsOnlyWhenTheProbeIntervalIsDue is evidence that the layer meters
// its own probes. A switch wakes its layers together, so this Wake runs at
// every spanning tree hello and every recovery expiry as well as at its own
// interval; emitting on each of them would probe far faster than configured.
func TestWakeEmitsOnlyWhenTheProbeIntervalIsDue(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.LinkChange(t0, "1/1/1", true)

	due, ok := l.NextWake()
	if !ok {
		t.Fatalf("NextWake after LinkChange: no timer")
	}

	if fx := l.Wake(t0.Add(time.Second)); len(fx.Emissions) != 0 {
		t.Errorf("Wake one second in: %d emissions, want 0", len(fx.Emissions))
	}
	if fx := l.Wake(due); len(fx.Emissions) != 1 {
		t.Fatalf("Wake at the due time: %d emissions, want 1", len(fx.Emissions))
	}
	if fx := l.Wake(due.Add(time.Second)); len(fx.Emissions) != 0 {
		t.Errorf("Wake one second after the due time: %d emissions, want 0", len(fx.Emissions))
	}
	if fx := l.Wake(due.Add(5 * time.Second)); len(fx.Emissions) != 1 {
		t.Errorf("Wake one interval after the due time: %d emissions, want 1", len(fx.Emissions))
	}
}
