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
	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"

	// Linked so protovalidate resolves the net/key predefined rules through the global registry (structure-record convention 4).
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/key/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

var testTime = time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)

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
	filterSets      []*filterv1.FilterRuleSet
	want            []trace.Layer
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
		in.filterSets,
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
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()
}

func switchedPhysicalInterface(name string, taggedVLANs ...uint32) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	admission := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltering := false
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds:    taggedVLANs,
				FrameAdmission:   &admission,
				IngressFiltering: &ingressFiltering,
			}.Build(),
		}.Build(),
	}.Build()
}

func routedPhysicalInterface(name string) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()
}

func activeFDBRow(portName string, vid uint32) *switchingv1.FdbEntry {
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	return switchingv1.FdbEntry_builder{
		NetworkInstance: ptr("default"),
		VlanId:          &vid,
		InterfaceName:   &portName,
		Mac:             addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		Kind:            &static,
		Status:          &active,
	}.Build()
}

func routedVLANInterface(name string, vid uint32, mac []byte) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Mac: addrv1.MacAddress_builder{
			Eui48: addrv1.Eui48Address_builder{Octets: mac}.Build(),
		}.Build(),
		Vlan: interfacev1.VlanInterface_builder{VlanId: &vid}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
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
				LagParentInterfaceName: &lagName,
			}.Build(),
		}.Build(),
	}
}

func validBridgeState() *stpv1.BridgeState {
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	priority := uint32(32768)
	return stpv1.BridgeState_builder{
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Priority: &priority,
			Address: addrv1.Eui48Address_builder{
				Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			}.Build(),
		}.Build(),
	}.Build()
}

func reversed[T any](values []T) []T {
	result := slices.Clone(values)
	slices.Reverse(result)
	return result
}

func ptr[T any](value T) *T {
	return &value
}

func protoIPv4Addr(octets [4]byte) *addrv1.IpAddress {
	return addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: octets[:]}.Build(),
	}.Build()
}

func protoIPv4Prefix(masked [4]byte, length uint32) *addrv1.IpPrefix {
	return addrv1.IpPrefix_builder{
		V4: addrv1.Ipv4Prefix_builder{
			Address: addrv1.Ipv4Address_builder{Octets: masked[:]}.Build(),
			Length:  &length,
		}.Build(),
	}.Build()
}

func protoIPv6Addr(octets [16]byte) *addrv1.IpAddress {
	return addrv1.IpAddress_builder{
		V6: addrv1.Ipv6Address_builder{Octets: octets[:]}.Build(),
	}.Build()
}

func protoIPv6Prefix(masked [16]byte, length uint32) *addrv1.IpPrefix {
	return addrv1.IpPrefix_builder{
		V6: addrv1.Ipv6Prefix_builder{
			Address: addrv1.Ipv6Address_builder{Octets: masked[:]}.Build(),
			Length:  &length,
		}.Build(),
	}.Build()
}

func protoEUI48(octets [6]byte) *addrv1.MacAddress {
	return addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{Octets: octets[:]}.Build(),
	}.Build()
}

func validateFixtures(t *testing.T, ifaces []*interfacev1.Interface, vlans []*switchingv1.Vlan, addrs []*ipv1.InterfaceAddress, neighbors []*ipv1.NeighborEntry) {
	t.Helper()
	for _, m := range ifaces {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("interface %s validation failed: %v", m.GetName(), err)
		}
	}
	for _, m := range vlans {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("vlan %d validation failed: %v", m.GetId(), err)
		}
	}
	for _, m := range addrs {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("address %s validation failed: %v", m.GetInterfaceName(), err)
		}
	}
	for _, m := range neighbors {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("neighbor %s validation failed: %v", m.GetInterfaceName(), err)
		}
	}
}

// routedSwitchedPhysicalInterface builds a physical interface carrying both
// a switchport facet (untagged on vid) and an IP facet, the shape a device
// reports when it configures a port as a switchport and starts routing on
// it without withdrawing the switchport configuration.
func routedSwitchedPhysicalInterface(name string, vid uint32) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:            &vid,
				UntaggedVlanIds: []uint32{vid},
			}.Build(),
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()
}

func vlanTag(tpid packetv1.EtherType, vid uint32) *switchingv1.VlanTag {
	pcp := uint32(0)
	dei := false
	return switchingv1.VlanTag_builder{
		Tpid:   &tpid,
		VlanId: &vid,
		Pcp:    &pcp,
		Dei:    &dei,
	}.Build()
}

// subInterface builds a Subinterface-kind interface over parent, carrying
// an IP facet so it is eligible to route. A nil tags slice leaves the
// encapsulation absent, matching a source that reported no encapsulation.
func subInterface(name, parent string, tags ...*switchingv1.VlanTag) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP

	var stack *switchingv1.VlanTagStack
	if tags != nil {
		stack = switchingv1.VlanTagStack_builder{Tags: tags}.Build()
	}

	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Sub: interfacev1.Subinterface_builder{
			ParentInterfaceName: &parent,
			Encapsulation:       stack,
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()
}

func mustEncode(t *testing.T, b bpdu.BPDU, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := bpdu.Encode(b, src)
	if err != nil {
		t.Fatalf("stp.Encode: %v", err)
	}
	return frame
}

// gigabitCopper is the physical profile these fabric fixtures assume, since
// they exercise protocol export and loading rather than link negotiation:
// auto-negotiating 10 Mb/s to 1 Gb/s ends over twisted pair.
func gigabitCopper() *cablemodel.PhyAssumption {
	return &cablemodel.PhyAssumption{
		Medium: cablemodel.TwistedPair,
		Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		},
	}
}

func newRingTopology(t *testing.T) (*fabric.Fabric, time.Time, map[string]netaddr.MAC) {
	t.Helper()

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	newPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
	}

	macs := map[string]netaddr.MAC{
		"sw1": {0, 0, 0, 0, 1, 1},
		"sw2": {0, 0, 0, 0, 1, 2},
		"sw3": {0, 0, 0, 0, 1, 3},
	}

	cfg := assembly.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  macs["sw1"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw2": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 12288,
					Address:  macs["sw2"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw3": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 8192,
					Address:  macs["sw3"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{
				Cable: cablemodel.Cable{LengthMeters: 1.0},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 1.0},
				A:     fabric.Endpoint{Node: "sw2", Port: "1/1/3"},
				B:     fabric.Endpoint{Node: "sw3", Port: "1/1/2"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 1.0},
				A:     fabric.Endpoint{Node: "sw3", Port: "1/1/3"},
				B:     fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
			},
		},
		Uncabled: []fabric.Uncabled{
			{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Endpoint: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{Endpoint: fabric.Endpoint{Node: "sw3", Port: "1/1/1"}},
		},
		PhyAssumption: gigabitCopper(),
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	return fab, t0, macs
}

func roleMatches(protoRole stpv1.PortRole, layerRole bpdu.Role) bool {
	switch layerRole {
	case bpdu.RoleRoot:
		return protoRole == stpv1.PortRole_PORT_ROLE_ROOT
	case bpdu.RoleDesignated:
		return protoRole == stpv1.PortRole_PORT_ROLE_DESIGNATED
	case bpdu.RoleAlternate:
		return protoRole == stpv1.PortRole_PORT_ROLE_ALTERNATE
	case bpdu.RoleBackup:
		return protoRole == stpv1.PortRole_PORT_ROLE_BACKUP
	case bpdu.RoleDisabled:
		return protoRole == stpv1.PortRole_PORT_ROLE_DISABLED
	default:
		return protoRole == stpv1.PortRole_PORT_ROLE_UNSPECIFIED
	}
}

func stateMatches(protoState stpv1.ForwardingState, layerState stp.State) bool {
	switch layerState {
	case stp.StateDiscarding:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_DISCARDING
	case stp.StateLearning:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_LEARNING
	case stp.StateForwarding:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_FORWARDING
	default:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_UNSPECIFIED
	}
}

func newLacpFabric(t *testing.T, start time.Time, macA, macB netaddr.MAC, lagA, lagB *lag.Config) *fabric.Fabric {
	t.Helper()

	vid10 := vlan.ID(10)
	buildPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
	}

	bridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/4": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"lag1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		}
	}

	cfg := assembly.Config{
		Start: start,
		Switches: map[string]vswitch.Config{
			"A": {
				MAC:    macA,
				Ports:  buildPorts(),
				Bridge: bridgeCfg(),
				LAG:    lagA,
			},
			"B": {
				MAC:    macB,
				Ports:  buildPorts(),
				Bridge: bridgeCfg(),
				LAG:    lagB,
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "A", Port: "1/1/1"}, B: fabric.Endpoint{Node: "B", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "A", Port: "1/1/2"}, B: fabric.Endpoint{Node: "B", Port: "1/1/2"}},
		},
		Uncabled: []fabric.Uncabled{
			{Endpoint: fabric.Endpoint{Node: "A", Port: "1/1/3"}},
			{Endpoint: fabric.Endpoint{Node: "A", Port: "1/1/4"}},
			{Endpoint: fabric.Endpoint{Node: "B", Port: "1/1/3"}},
			{Endpoint: fabric.Endpoint{Node: "B", Port: "1/1/4"}},
		},
		PhyAssumption: gigabitCopper(),
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab
}
