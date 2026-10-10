package netmodel_test

import (
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestLoadConflictsAreOrderIndependent(t *testing.T) {
	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "conflict-permutations"}
	vid := uint32(10)
	portName := "1/1/1"
	otherPortName := "1/1/2"
	routedName := "vlan10"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC

	tests := []struct {
		name       string
		forward    loadInput
		reverse    loadInput
		conflictOn string
		omitted    func(netmodel.Result) bool
	}{
		{
			name: "forwarding database",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName), plainPhysicalInterface(otherPortName)},
				fdb: []*switchingv1.FdbEntry{
					switchingv1.FdbEntry_builder{NetworkInstance: ptr("default"), VlanId: &vid, Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), InterfaceName: &portName, Kind: &static, Status: &active}.Build(),
					switchingv1.FdbEntry_builder{NetworkInstance: ptr("default"), VlanId: &vid, Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), InterfaceName: &otherPortName, Kind: &static, Status: &active}.Build(),
				},
			},
			conflictOn: "fdb_entry",
			omitted: func(result netmodel.Result) bool {
				return len(result.Spec.Seeds) == 0
			},
		},
		{
			name: "vlan",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				vlans: []*switchingv1.Vlan{
					switchingv1.Vlan_builder{NetworkInstance: ptr("default"), Id: &vid, Name: ptr("blue")}.Build(),
					switchingv1.Vlan_builder{NetworkInstance: ptr("default"), Id: &vid, Name: ptr("red")}.Build(),
				},
				want: []trace.Layer{bridge.LayerNameVLAN},
			},
			conflictOn: "vlan",
			omitted: func(result netmodel.Result) bool {
				_, exists := result.Spec.Config.Bridge.VLAN.Table[10]
				return !exists
			},
		},
		{
			name: "power budget",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				budgets: []*phyv1.PseBudget{
					phyv1.PseBudget_builder{PseGroup: ptr(uint32(1)), PowerNanowatts: ptr(uint64(100_000_000_000))}.Build(),
					phyv1.PseBudget_builder{PseGroup: ptr(uint32(1)), PowerNanowatts: ptr(uint64(200_000_000_000))}.Build(),
				},
			},
			conflictOn: "pse_budget",
			omitted: func(result netmodel.Result) bool {
				_, exists := result.Spec.Config.Phy.PoE.Groups["1"]
				return !exists
			},
		},
		{
			name: "spanning tree port",
			forward: loadInput{
				ifaces:      []*interfacev1.Interface{plainPhysicalInterface(portName)},
				bridgeState: validBridgeState(),
				stpPorts: []*stpv1.PortState{
					stpv1.PortState_builder{InterfaceName: &portName, Priority: ptr(uint32(64)), AdminPathCost: ptr(uint32(10))}.Build(),
					stpv1.PortState_builder{InterfaceName: &portName, Priority: ptr(uint32(128)), AdminPathCost: ptr(uint32(20))}.Build(),
				},
			},
			conflictOn: "stp_port",
			omitted: func(result netmodel.Result) bool {
				_, exists := result.Spec.Config.STP.Ports[portName]
				return !exists
			},
		},
		{
			name: "link aggregation aggregator",
			forward: loadInput{
				ifaces: lagInterfaces(),
				lacpAggregators: []*lacpv1.AggregatorState{
					lacpv1.AggregatorState_builder{InterfaceName: ptr("lag1"), Mode: ptr(lacpv1.LacpMode_LACP_MODE_ACTIVE), Key: ptr(uint32(10))}.Build(),
					lacpv1.AggregatorState_builder{InterfaceName: ptr("lag1"), Mode: ptr(lacpv1.LacpMode_LACP_MODE_PASSIVE), Key: ptr(uint32(20))}.Build(),
				},
			},
			conflictOn: "lacp_aggregator",
			omitted: func(result netmodel.Result) bool {
				return result.Spec.Config.LAG.LAGs["lag1"].LACP.Mode == lag.Off
			},
		},
		{
			name: "link aggregation member",
			forward: loadInput{
				ifaces: lagInterfaces(),
				lacpPorts: []*lacpv1.PortState{
					lacpv1.PortState_builder{InterfaceName: &portName, PortPriority: ptr(uint32(10)), Key: ptr(uint32(100))}.Build(),
					lacpv1.PortState_builder{InterfaceName: &portName, PortPriority: ptr(uint32(20)), Key: ptr(uint32(200))}.Build(),
				},
			},
			conflictOn: "lacp_port_state",
			omitted: func(result netmodel.Result) bool {
				member := result.Spec.Config.LAG.LAGs["lag1"].Members[portName]
				return (member.Priority != 10 || member.Key != 100) && (member.Priority != 20 || member.Key != 200)
			},
		},
		{
			name: "interface address",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{routedVLANInterface(routedName, vid, []byte{0, 1, 2, 3, 4, 5})},
				addrs: []*ipv1.InterfaceAddress{
					ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build(),
					ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 25)}.Build(),
				},
			},
			conflictOn: "ip_address",
			omitted: func(result netmodel.Result) bool {
				return len(result.Spec.Config.Routing.VRFs[routing.DefaultVRF].Interfaces[routedName].Prefixes) == 0
			},
		},
		{
			name: "neighbor",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{routedVLANInterface(routedName, vid, []byte{0, 1, 2, 3, 4, 5})},
				addrs: []*ipv1.InterfaceAddress{
					ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build(),
				},
				neighbors: []*ipv1.NeighborEntry{
					ipv1.NeighborEntry_builder{InterfaceName: &routedName, Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}), Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 6})}.Build(),
					ipv1.NeighborEntry_builder{InterfaceName: &routedName, Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}), Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 7})}.Build(),
				},
			},
			conflictOn: "ip_neighbor",
			omitted: func(result netmodel.Result) bool {
				return len(result.Spec.Config.Routing.VRFs[routing.DefaultVRF].Neighbors) == 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.forward.validate(t)
			tt.reverse = tt.forward
			tt.reverse.fdb = reversed(tt.forward.fdb)
			tt.reverse.vlans = reversed(tt.forward.vlans)
			tt.reverse.budgets = reversed(tt.forward.budgets)
			tt.reverse.stpPorts = reversed(tt.forward.stpPorts)
			tt.reverse.lacpAggregators = reversed(tt.forward.lacpAggregators)
			tt.reverse.lacpPorts = reversed(tt.forward.lacpPorts)
			tt.reverse.addrs = reversed(tt.forward.addrs)
			tt.reverse.neighbors = reversed(tt.forward.neighbors)

			forward := tt.forward.load(t, src)
			reverse := tt.reverse.load(t, src)
			repeated := tt.forward.load(t, src)
			assertEqualLoadResults(t, forward, reverse)
			assertEqualLoadResults(t, forward, repeated)
			assertEvidenceSource(t, forward, src)

			if forward.Readiness() != analysis.Unstable {
				t.Errorf("readiness = %v, want Unstable", forward.Readiness())
			}
			if len(forward.Report.Conflicts) != 1 || forward.Report.Conflicts[0].What != tt.conflictOn {
				t.Errorf("conflicts = %+v, want one %q conflict", forward.Report.Conflicts, tt.conflictOn)
			}
			if !tt.omitted(forward) {
				t.Errorf("conflicting %s remained executable: %+v", tt.conflictOn, forward.Spec)
			}
			if _, err := vswitch.NewWithSpec(forward.Spec); err != nil {
				t.Errorf("NewWithSpec: %v", err)
			}
		})
	}
}
