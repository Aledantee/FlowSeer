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

	// Linked so protovalidate resolves the net/key predefined rules through the global registry (structure-record convention 4).
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/key/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

var testTime = time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)

var trustTestTime = time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)

func load(t *testing.T, src netmodel.SourceContext, in netmodel.Input) netmodel.Result {
	t.Helper()

	result, err := netmodel.Load(trustTestTime, src, in)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return result
}

func validateInput(t *testing.T, in netmodel.Input) {
	t.Helper()

	messages := make([]proto.Message, 0, len(in.Interfaces)+len(in.VLANs)+len(in.FDB)+len(in.Budgets)+len(in.STPPorts)+len(in.LACPAggregators)+len(in.LACPPorts)+len(in.Addresses)+len(in.Neighbors)+1)
	for _, message := range in.Interfaces {
		messages = append(messages, message)
	}
	for _, message := range in.VLANs {
		messages = append(messages, message)
	}
	for _, message := range in.FDB {
		messages = append(messages, message)
	}
	for _, message := range in.Budgets {
		messages = append(messages, message)
	}
	if in.BridgeState != nil {
		messages = append(messages, in.BridgeState)
	}
	for _, message := range in.STPPorts {
		messages = append(messages, message)
	}
	for _, message := range in.LACPAggregators {
		messages = append(messages, message)
	}
	for _, message := range in.LACPPorts {
		messages = append(messages, message)
	}
	for _, message := range in.Addresses {
		messages = append(messages, message)
	}
	for _, message := range in.Neighbors {
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

// gigabitCopper supplies the negotiated physical facts needed by fabric
// forwarding fixtures: auto-negotiating 10 Mb/s to 1 Gb/s over twisted pair.
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
