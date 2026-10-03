package sim_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestLayerNames(t *testing.T) {
	layers := []struct {
		constant trace.Layer
		expected string
	}{
		{port.LayerName, "port"},
		{lag.LayerName, "lag"},
		{phy.LayerName, "ethernet"},
		{phy.LayerNamePoE, "poe"},
		{bridge.LayerName, "relay"},
		{bridge.LayerNameVLAN, "vlan"},
		{stp.LayerName, "stp"},
		{loopprotect.LayerName, "loopprotect"},
		{mcast.LayerName, "mcast"},
		{routing.LayerName, "routing"},
		{traffic.LayerName, "traffic"},
		{filter.LayerName, "filter"},
	}

	for _, l := range layers {
		if string(l.constant) != l.expected {
			t.Errorf("layer constant %q != expected %q", l.constant, l.expected)
		}
	}
}
