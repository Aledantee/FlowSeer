package netmodel_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

var trustTestTime = time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)

type loadInput struct {
	ifaces          []*interfacev1.Interface
	vlans           []*switchingv1.Vlan
	fdb             []*switchingv1.FdbEntry
	budgets         []*phyv1.PseBudget
	bridgeState     *stpv1.BridgeState
	stpPorts        []*stpv1.PortState
	lacpAggregators []*lacpv1.AggregatorState
	lacpPorts       []*lacpv1.PortState
	addrs           []*ipv1.InterfaceAddress
	neighbors       []*ipv1.NeighborEntry
	want            []port.Layer
}

func (in loadInput) load(t *testing.T, src netmodel.SourceContext) netmodel.Result {
	t.Helper()

	result, err := netmodel.Load(
		trustTestTime,
		src,
		in.ifaces,
		in.vlans,
		in.fdb,
		in.budgets,
		in.bridgeState,
		in.stpPorts,
		in.lacpAggregators,
		in.lacpPorts,
		in.addrs,
		in.neighbors,
		in.want,
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return result
}

func (in loadInput) validate(t *testing.T) {
	t.Helper()

	messages := make([]proto.Message, 0, len(in.ifaces)+len(in.vlans)+len(in.fdb)+len(in.budgets)+len(in.stpPorts)+len(in.lacpAggregators)+len(in.lacpPorts)+len(in.addrs)+len(in.neighbors)+1)
	for _, message := range in.ifaces {
		messages = append(messages, message)
	}
	for _, message := range in.vlans {
		messages = append(messages, message)
	}
	for _, message := range in.fdb {
		messages = append(messages, message)
	}
	for _, message := range in.budgets {
		messages = append(messages, message)
	}
	if in.bridgeState != nil {
		messages = append(messages, in.bridgeState)
	}
	for _, message := range in.stpPorts {
		messages = append(messages, message)
	}
	for _, message := range in.lacpAggregators {
		messages = append(messages, message)
	}
	for _, message := range in.lacpPorts {
		messages = append(messages, message)
	}
	for _, message := range in.addrs {
		messages = append(messages, message)
	}
	for _, message := range in.neighbors {
		messages = append(messages, message)
	}
	for _, message := range messages {
		if err := protovalidate.Validate(message); err != nil {
			t.Fatalf("fixture validation: %v", err)
		}
	}
}

func assertEqualLoadResults(t *testing.T, got, want netmodel.Result) {
	t.Helper()

	if !got.Spec.Equal(want.Spec) {
		t.Errorf("construction specs differ:\n got: %+v\nwant: %+v", got.Spec, want.Spec)
	}
	if !reflect.DeepEqual(got.Report, want.Report) {
		t.Errorf("reports differ:\n got: %+v\nwant: %+v", got.Report, want.Report)
	}
	if got.Readiness() != want.Readiness() {
		t.Errorf("readiness = %v, want %v", got.Readiness(), want.Readiness())
	}
	if got.Metadata.Scope() != want.Metadata.Scope() {
		t.Errorf("metadata scope = %+v, want %+v", got.Metadata.Scope(), want.Metadata.Scope())
	}
	if issues := got.Metadata.Issues(); !reflect.DeepEqual(issues, want.Metadata.Issues()) {
		t.Errorf("issues differ:\n got: %+v\nwant: %+v", issues, want.Metadata.Issues())
	}
	if evidence := got.Metadata.Evidence().Entries(); !reflect.DeepEqual(evidence, want.Metadata.Evidence().Entries()) {
		t.Errorf("evidence differs:\n got: %+v\nwant: %+v", evidence, want.Metadata.Evidence().Entries())
	}
	if assumptions := got.Metadata.Assumptions(); !reflect.DeepEqual(assumptions, want.Metadata.Assumptions()) {
		t.Errorf("assumptions differ:\n got: %+v\nwant: %+v", assumptions, want.Metadata.Assumptions())
	}
}

func assertEvidenceSource(t *testing.T, result netmodel.Result, src netmodel.SourceContext) {
	t.Helper()

	for _, entry := range result.Metadata.Evidence().Entries() {
		if entry.Evidence.Origin != src.Origin || !strings.HasPrefix(entry.Evidence.Context, src.Context) {
			t.Errorf("evidence source = %+v, want origin %q and context prefix %q", entry.Evidence, src.Origin, src.Context)
		}
	}
}

func plainPhysicalInterface(name string) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()
}

func routedVLANInterface(name string, vid uint32, mac []byte) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mac: addrv1.EuiAddress_builder{
			Eui48: addrv1.Eui48Address_builder{Octets: mac}.Build(),
		}.Build(),
		Vlan: interfacev1.VlanInterface_builder{VlanId: &vid}.Build(),
		Ip: ipv1.IpFacet_builder{
			Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()
}

func lagInterfaces() []*interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	lagName := "lag1"
	memberName := "1/1/1"
	return []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &lagName,
			AdminStatus: &admin,
			OperStatus:  &oper,
			Lag: interfacev1.LagInterface_builder{
				Aggregation: switchingv1.AggregationFacet_builder{}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &memberName,
			AdminStatus: &admin,
			OperStatus:  &oper,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParent: &lagName,
			}.Build(),
		}.Build(),
	}
}

func validBridgeState() *stpv1.BridgeState {
	priority := uint32(32768)
	return stpv1.BridgeState_builder{
		BridgeId: stpv1.BridgeId_builder{
			Priority: &priority,
			Address: addrv1.Eui48Address_builder{
				Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			}.Build(),
		}.Build(),
	}.Build()
}

func TestLoadFDBRequiresAuthoritativeKindAndStatus(t *testing.T) {
	vid := uint32(10)
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	dynamic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC
	unspecifiedKind := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_UNSPECIFIED
	unspecifiedStatus := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_UNSPECIFIED
	unknownKind := switchingv1.FdbEntryKind(99)
	unknownStatus := switchingv1.FdbEntryStatus(99)
	macs := [][]byte{
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x04},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x05},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x06},
	}
	fdb := []*switchingv1.FdbEntry{
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[0]}.Build(), Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[1]}.Build(), Kind: &unknownKind, Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[2]}.Build(), Kind: &dynamic,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[3]}.Build(), Kind: &dynamic, Status: &unknownStatus,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[4]}.Build(), Kind: &unspecifiedKind, Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[5]}.Build(), Kind: &dynamic, Status: &unspecifiedStatus,
		}.Build(),
	}

	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "fdb-trust"}
	result := (loadInput{ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)}, fdb: fdb}).load(t, src)

	if len(result.Spec.Seeds) != 0 {
		t.Errorf("seeds = %+v, want none", result.Spec.Seeds)
	}
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want non-Complete")
	}
	if len(result.Report.Skipped) != len(fdb) {
		t.Errorf("skipped rows = %d, want %d: %+v", len(result.Report.Skipped), len(fdb), result.Report.Skipped)
	}
	assertEvidenceSource(t, result, src)
}

func TestLoadKeepsExplicitActiveFDBKindsAndDeduplicatesEqualRows(t *testing.T) {
	vid := uint32(10)
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE

	for _, tt := range []struct {
		name     string
		kind     switchingv1.FdbEntryKind
		isStatic bool
	}{
		{name: "dynamic", kind: switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC},
		{name: "static", kind: switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC, isStatic: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := switchingv1.FdbEntry_builder{
				VlanId: &vid, InterfaceName: &portName,
				Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), Kind: &tt.kind, Status: &active,
			}.Build()
			input := loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				fdb:    []*switchingv1.FdbEntry{row, row},
			}
			input.validate(t)
			result := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

			if len(result.Spec.Seeds) != 1 || result.Spec.Seeds[0].Static != tt.isStatic {
				t.Errorf("seeds = %+v, want one seed with static=%t", result.Spec.Seeds, tt.isStatic)
			}
			if len(result.Report.Conflicts) != 0 || result.Readiness() != analysis.Complete {
				t.Errorf("conflicts = %+v, readiness = %v, want no conflict and Complete", result.Report.Conflicts, result.Readiness())
			}
		})
	}
}

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
					switchingv1.FdbEntry_builder{VlanId: &vid, Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), InterfaceName: &portName, Kind: &static, Status: &active}.Build(),
					switchingv1.FdbEntry_builder{VlanId: &vid, Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), InterfaceName: &otherPortName, Kind: &static, Status: &active}.Build(),
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
					switchingv1.Vlan_builder{Id: &vid, Name: ptr("blue")}.Build(),
					switchingv1.Vlan_builder{Id: &vid, Name: ptr("red")}.Build(),
				},
				want: []port.Layer{port.LayerVlan},
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
					phyv1.PseBudget_builder{PseGroup: ptr(uint32(1)), PowerMilliwatts: ptr(uint32(100_000))}.Build(),
					phyv1.PseBudget_builder{PseGroup: ptr(uint32(1)), PowerMilliwatts: ptr(uint32(200_000))}.Build(),
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

func TestLoadMalformedNetworkValuesAreScopedPartialRows(t *testing.T) {
	vid := uint32(10)
	routedName := "vlan10"
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	malformedIP := addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: []byte{10, 0, 0}}.Build(),
	}.Build()
	malformedPrefix := addrv1.IpPrefix_builder{
		V4: addrv1.Ipv4Prefix_builder{
			Address: addrv1.Ipv4Address_builder{Octets: []byte{10, 0, 0}}.Build(),
			Length:  ptr(uint32(24)),
		}.Build(),
	}.Build()

	input := loadInput{
		ifaces: []*interfacev1.Interface{
			routedVLANInterface(routedName, vid, []byte{0, 1, 2, 3, 4}),
			plainPhysicalInterface(portName),
		},
		fdb: []*switchingv1.FdbEntry{
			switchingv1.FdbEntry_builder{
				VlanId: &vid, InterfaceName: &portName,
				Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(), Kind: &static, Status: &active,
			}.Build(),
		},
		addrs: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: malformedIP, Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build(),
			ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: malformedPrefix}.Build(),
		},
		neighbors: []*ipv1.NeighborEntry{
			ipv1.NeighborEntry_builder{InterfaceName: &routedName, Ip: malformedIP, Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 6})}.Build(),
			ipv1.NeighborEntry_builder{
				InterfaceName: &routedName, Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}),
				Mac: addrv1.EuiAddress_builder{Eui48: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build()}.Build(),
			}.Build(),
		},
	}

	result := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "malformed"})
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want non-Complete")
	}
	if len(result.Spec.Seeds) != 0 {
		t.Errorf("seeds = %+v, want malformed FDB row omitted", result.Spec.Seeds)
	}
	vrf := result.Spec.Config.Routing.VRFs[routing.DefaultVRF]
	if prefixes := vrf.Interfaces[routedName].Prefixes; len(prefixes) != 0 {
		t.Errorf("prefixes = %v, want malformed rows omitted", prefixes)
	}
	if len(vrf.Neighbors) != 0 {
		t.Errorf("neighbors = %+v, want malformed rows omitted", vrf.Neighbors)
	}

	wants := []struct {
		what string
		port string
	}{
		{what: "interface_mac", port: routedName},
		{what: "fdb_entry", port: portName},
		{what: "ip_address", port: routedName},
		{what: "ip_neighbor", port: routedName},
	}
	for _, want := range wants {
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == want.what && skip.Port == want.port
		}) {
			t.Errorf("skipped = %+v, want scoped %s skip for %s", result.Report.Skipped, want.what, want.port)
		}
	}
	if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
		t.Errorf("NewWithSpec: %v", err)
	}
}

func TestLoadMalformedProtocolMACsArePartial(t *testing.T) {
	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "protocol-mac"}

	t.Run("spanning tree bridge", func(t *testing.T) {
		priority := uint32(32768)
		bridgeState := stpv1.BridgeState_builder{
			BridgeId: stpv1.BridgeId_builder{
				Priority: &priority,
				Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(),
			}.Build(),
		}.Build()
		result := (loadInput{
			ifaces:      []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
			bridgeState: bridgeState,
		}).load(t, src)

		if result.Spec.Config.STP != nil {
			t.Errorf("STP config = %+v, want malformed bridge state omitted", result.Spec.Config.STP)
		}
		if result.Readiness() == analysis.Complete {
			t.Fatal("readiness = Complete, want non-Complete")
		}
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == "stp_bridge"
		}) {
			t.Errorf("skipped = %+v, want stp_bridge", result.Report.Skipped)
		}
		if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
			t.Errorf("NewWithSpec: %v", err)
		}
	})

	t.Run("link aggregation system id", func(t *testing.T) {
		lagName := "lag1"
		active := lacpv1.LacpMode_LACP_MODE_ACTIVE
		result := (loadInput{
			ifaces: lagInterfaces(),
			lacpAggregators: []*lacpv1.AggregatorState{
				lacpv1.AggregatorState_builder{
					InterfaceName: &lagName,
					Mode:          &active,
					SystemId:      addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(),
				}.Build(),
			},
		}).load(t, src)

		if result.Spec.Config.LAG.LAGs[lagName].LACP.Mode != lag.Off {
			t.Errorf("LACP mode = %v, want default Off", result.Spec.Config.LAG.LAGs[lagName].LACP.Mode)
		}
		if result.Readiness() == analysis.Complete {
			t.Fatal("readiness = Complete, want non-Complete")
		}
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == "lacp_aggregator" && skip.Port == lagName
		}) {
			t.Errorf("skipped = %+v, want scoped lacp_aggregator", result.Report.Skipped)
		}
		if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
			t.Errorf("NewWithSpec: %v", err)
		}
	})
}

func reversed[T any](values []T) []T {
	result := slices.Clone(values)
	slices.Reverse(result)
	return result
}

func ptr[T any](value T) *T {
	return &value
}
