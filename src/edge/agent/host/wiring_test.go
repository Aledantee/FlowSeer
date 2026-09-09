package host

import "testing"

func TestProductionLaneQueueCapacity(t *testing.T) {
	if got := (&assembly{}).laneConfig(&laneReporter{}, nil).QueueCapacity; got != 4 {
		t.Errorf("production lane QueueCapacity = %d, want 4", got)
	}
}
