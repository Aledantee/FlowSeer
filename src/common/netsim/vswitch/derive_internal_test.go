package vswitch

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// TestDeriveKeepsOneGateEntryPerScope is evidence that Derive's install of the
// retained spanning tree clone replaces the entry New already installed for the
// same scope. An appending install would leave the bridge consulting both the
// layer New built and the converged clone that replaced it.
func TestDeriveKeepsOneGateEntryPerScope(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	cfg := Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
		},
	}

	sw, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := sw.bridge.GateCount(); got != 1 {
		t.Fatalf("GateCount() on New = %d, want 1", got)
	}

	derived, err := Derive(sw, ConstructionSpec{Config: cfg})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if got := derived.bridge.GateCount(); got != 1 {
		t.Errorf("GateCount() after Derive = %d, want 1", got)
	}
}
