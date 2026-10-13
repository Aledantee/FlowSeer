package flow_test

import (
	"maps"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/flow"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestFlowStatsCloneKeepsIndependentMaps(t *testing.T) {
	stats := flow.Stats{
		Offered: 7, Lost: 1, Unresolved: 2, Rejected: 3, Held: 4,
		Delivered: map[string]uint64{"host": 5},
		Drops:     map[trace.Reason]uint64{"drop": 6},
		Copies:    map[string]uint64{"mirror": 8},
		Latency:   flow.LatencyStats{Min: time.Millisecond, Max: 3 * time.Millisecond, Sum: 4 * time.Millisecond, Count: 2},
	}
	clone := stats.Clone()
	if !reflect.DeepEqual(clone, stats) {
		t.Fatalf("clone = %+v, want %+v", clone, stats)
	}
	clone.Delivered["host"] = 50
	clone.Drops["drop"] = 60
	clone.Copies["mirror"] = 80
	if !maps.Equal(stats.Delivered, map[string]uint64{"host": 5}) {
		t.Errorf("Delivered = %v, want host:5", stats.Delivered)
	}
	if !maps.Equal(stats.Drops, map[trace.Reason]uint64{"drop": 6}) {
		t.Errorf("Drops = %v, want drop:6", stats.Drops)
	}
	if !maps.Equal(stats.Copies, map[string]uint64{"mirror": 8}) {
		t.Errorf("Copies = %v, want mirror:8", stats.Copies)
	}
}
