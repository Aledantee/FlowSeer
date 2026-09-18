package vswitch_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported vswitch.Config field
// reaches vswitch.Diff. vswitch.Diff delegates each capability to that capability's own
// Diff, so this walk also re-covers every capability Config's leaves; that duplicates
// each capability package's own diff_coverage_test.go rather than contradicting it,
// since vswitch.Config field participation is what R9 asks for at this package too.
// Ports is exempt: port.Table's fields are unexported, so the walk finds nothing inside
// it regardless; port's own diff_coverage_test.go covers port.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	pvid := vlan.ID(10)
	outputVLAN := vlan.ID(20)
	limit := uint32(5000)
	seed := vswitch.Config{
		MAC:   netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		Ports: ports,
		Phy: &phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS:       []uint64{1_000_000_000},
					AutoNegotiationSupported: phy.CapabilitySupported,
					Setting:                  &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full, AutoNegotiation: true},
				},
			},
			PoE: &phy.PoE{Ports: map[string]phy.PsePort{"1/1/1": {Limit: &limit, Priority: phy.PriorityHigh}}},
		},
		Bridge: &bridge.Config{
			AgingTime: 300 * time.Second,
			VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &pvid, Admission: bridge.TaggedOnly}},
			},
		},
		LAG: &lag.Config{
			LAGs: map[string]lag.LAG{"lag1": {
				Mode: lag.BalanceSLB,
				// Non-zero LACP and member keys: perturbing a zero Key yields exactly
				// the value Normalize derives from the port table, so both sides
				// would normalize to the same key and the leaf would read as
				// unchanged.
				LACP:    lag.LACPConfig{Key: 7},
				Members: map[string]lag.Member{"1/1/1": {Priority: 1, Key: 7}},
			}},
		},
		STP: &stp.Config{
			Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x09},
			Ports:   map[string]stp.Port{"1/1/1": {PathCost: 20000}},
		},
		LoopProtect: &loopprotect.Config{
			Interval: 5 * time.Second,
			Ports:    map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
		},
		Mcast: &mcast.Config{
			VLANs: map[vlan.ID]mcast.VLANSnooping{10: {FastLeave: true}},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					},
				},
			},
		},
		Traffic: &traffic.Config{
			Mirrors: []traffic.Mirror{{Name: "m1", OutputPort: "1/1/1", OutputVLAN: &outputVLAN}},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, vswitch.Config.Normalize, vswitch.Diff, nil)
}
