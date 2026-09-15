// This file proves that a host outside this directory (vswitch) can
// construct mcast's exported Config.
package mcast_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestAggregateValidationAllowsInactiveMulticastRouterPorts(t *testing.T) {
	t.Parallel()

	for _, state := range []port.LinkState{port.Down, port.Unknown} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			ports, err := port.NewBuilder().Add(port.Port{
				Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: state,
			}).Build()
			if err != nil {
				t.Fatalf("build port table: %v", err)
			}
			cfg := vswitch.Config{
				Ports: ports,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table:       map[vlan.ID]string{10: "ten"},
					Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{10}}},
				}},
				Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
					10: {RouterPorts: []string{"1/1/1"}},
				}},
			}
			if err := cfg.Normalize().Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestVSwitchConstructsEffectiveMcastConfiguration proves a package outside
// lag and mcast can name every exported mcast.VLANSnooping field, including
// the leave-timing fields this phase added, and construct a value the
// switch's normalization accepts unchanged.
func TestVSwitchConstructsEffectiveMcastConfiguration(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	lmqi := 2 * time.Second
	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "ten"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tagged: []vlan.ID{10}},
				"1/1/2": {Tagged: []vlan.ID{10}},
			},
		}},
		Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			10: {
				RouterPorts:             []string{"1/1/2"},
				FastLeave:               false,
				MembershipInterval:      120 * time.Second,
				RouterPortInterval:      120 * time.Second,
				LastMemberQueryInterval: lmqi,
				LastMemberQueryCount:    3,
			},
		}},
	}

	norm := cfg.Normalize()
	if err := norm.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	if changes := vswitch.Diff(sw.Config(), norm); len(changes) != 0 {
		t.Errorf("Diff(Switch.Config(), norm) = %+v, want no changes", changes)
	}
}
