package traffic_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestQueueThresholdFact(t *testing.T) {
	fact := traffic.QueueThresholdFact(1014, 1014, 1518)
	if got, want := fact.TypeID(), "traffic.queue_threshold"; got != want {
		t.Errorf("TypeID() = %q, want %q", got, want)
	}
	if got, want := fact.Canonical(), "depth_before_octets=1014;frame_octets=1014;threshold_octets=1518"; got != want {
		t.Errorf("Canonical() = %q, want %q", got, want)
	}
}
