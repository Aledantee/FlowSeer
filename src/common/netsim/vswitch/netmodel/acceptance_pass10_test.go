package netmodel_test

import (
	"net/netip"
	"slices"
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

func TestRequestedMissingSTPRemainsAForwardingDependency(t *testing.T) {
	input := loadInput{
		ifaces: []*interfacev1.Interface{reportedPhysical("in"), reportedPhysical("out")},
		want:   []port.Layer{port.LayerRelay, port.LayerStp},
	}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	result := sw.Peek(trustTestTime, "in", ethernet.Frame{
		Src: netaddr.MAC{0, 1, 2, 3, 4, 5},
		Dst: netaddr.MAC{0, 1, 2, 3, 4, 6},
	})
	if result.Metadata.Status() != analysis.Incomplete {
		t.Fatalf("status = %s, want Incomplete; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
	}
	assertForwardIssueEvidence(t, result.Metadata, netmodel.IssueMissingSTPBridgeState)

	input.want = []port.Layer{port.LayerRelay}
	withoutRequest := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})
	plain, err := vswitch.NewWithSpec(withoutRequest.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec without STP request: %v", err)
	}
	plainResult := plain.Peek(trustTestTime, "in", ethernet.Frame{
		Src: netaddr.MAC{0, 1, 2, 3, 4, 5},
		Dst: netaddr.MAC{0, 1, 2, 3, 4, 6},
	})
	if plainResult.Metadata.Status() != analysis.Complete {
		t.Fatalf("unrequested STP status = %s, want Complete; issues: %+v", plainResult.Metadata.Status(), plainResult.Metadata.Issues())
	}
}

func TestMissingMTUIsAnEvidencedForwardingAssumption(t *testing.T) {
	input := loadInput{
		ifaces: []*interfacev1.Interface{reportedPhysical("in"), physicalWithoutMTU("out")},
		want:   []port.Layer{port.LayerRelay},
	}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})

	issueIndex := slices.IndexFunc(loaded.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueMissingMTU &&
			issue.Scope.Compare(analysis.PortScope("sw1", "out")) == 0
	})
	if issueIndex < 0 {
		t.Fatalf("issues = %+v, want port-scoped missing MTU", loaded.Metadata.Issues())
	}
	issue := loaded.Metadata.Issues()[issueIndex]
	if len(issue.Evidence) == 0 {
		t.Fatal("missing MTU issue has no evidence")
	}
	if _, ok := loaded.Metadata.Evidence().Lookup(issue.Evidence[0]); !ok {
		t.Fatalf("missing MTU evidence %q is absent from catalog", issue.Evidence[0])
	}
	if !slices.ContainsFunc(loaded.Metadata.Assumptions(), func(assumption analysis.Assumption) bool {
		return assumption.Scope.Compare(analysis.PortScope("sw1", "out")) == 0 &&
			len(assumption.Evidence) > 0
	}) {
		t.Fatalf("assumptions = %+v, want evidenced port-scoped MTU fallback", loaded.Metadata.Assumptions())
	}

	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	result := sw.Peek(trustTestTime, "in", ethernet.Frame{
		Src:     netaddr.MAC{0, 1, 2, 3, 4, 5},
		Dst:     netaddr.MAC{0, 1, 2, 3, 4, 6},
		Payload: make([]byte, 9_000),
	})
	if result.Metadata.Status() != analysis.Incomplete {
		t.Fatalf("oversized forwarding status = %s, want Incomplete; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
	}

	input.ifaces[1] = reportedPhysical("out")
	explicitZero := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})
	if slices.ContainsFunc(explicitZero.Report.Defaults, func(value netmodel.Default) bool {
		return value.Port == "out" && value.Field == "mtu"
	}) {
		t.Fatalf("explicit MTU zero reported as a default: %+v", explicitZero.Report.Defaults)
	}
	if explicitZero.Metadata.Status() != analysis.Complete {
		t.Fatalf("explicit MTU zero status = %s, want Complete; issues: %+v", explicitZero.Metadata.Status(), explicitZero.Metadata.Issues())
	}
}

func TestSVINeighborMissRetainsExactLoadedIssue(t *testing.T) {
	const (
		insideVID  = uint32(10)
		outsideVID = uint32(20)
	)
	routerMAC := []byte{2, 0, 0, 0, 0, 1}
	input := loadInput{
		ifaces: []*interfacev1.Interface{
			reportedAccess("in", insideVID),
			reportedAccess("out", outsideVID),
			reportedSVI("vlan10", insideVID, routerMAC),
			reportedSVI("vlan20", outsideVID, routerMAC),
		},
		vlans: []*switchingv1.Vlan{
			switchingv1.Vlan_builder{Id: ptr(insideVID), Name: ptr("inside")}.Build(),
			switchingv1.Vlan_builder{Id: ptr(outsideVID), Name: ptr("outside")}.Build(),
		},
		addrs: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{InterfaceName: ptr("vlan10"), Address: protoIPv4Addr([4]byte{192, 0, 2, 1}), Prefix: protoIPv4Prefix([4]byte{192, 0, 2, 0}, 24)}.Build(),
			ipv1.InterfaceAddress_builder{InterfaceName: ptr("vlan20"), Address: protoIPv4Addr([4]byte{198, 51, 100, 1}), Prefix: protoIPv4Prefix([4]byte{198, 51, 100, 0}, 24)}.Build(),
		},
		neighbors: []*ipv1.NeighborEntry{
			ipv1.NeighborEntry_builder{InterfaceName: ptr("vlan20"), Ip: protoIPv4Addr([4]byte{198, 51, 100, 7})}.Build(),
		},
	}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	hdr := ip.Header{Src: netip.MustParseAddr("192.0.2.7"), Dst: netip.MustParseAddr("198.51.100.7"), HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
	payload, err := hdr.Encode([]byte("neighbor lookup"))
	if err != nil {
		t.Fatalf("encode packet: %v", err)
	}
	result := sw.Peek(trustTestTime, "in", ethernet.Frame{
		Src:       netaddr.MAC{2, 0, 0, 0, 0, 2},
		Dst:       netaddr.MAC(routerMAC),
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	})
	if result.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("reason = %s, want neighbor-miss", result.Reason)
	}
	wantScope := analysis.FieldScope(
		analysis.ProtocolScope("sw1", string(port.LayerRouting), routing.DefaultVRF),
		"interfaces", "vlan20", "neighbors", "198.51.100.7",
	)
	if !slices.ContainsFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueMissingNeighborMAC &&
			issue.Scope.Compare(wantScope) == 0 && len(issue.Evidence) > 0
	}) {
		t.Fatalf("issues = %+v, want exact evidenced SVI neighbor omission at %s", result.Metadata.Issues(), wantScope)
	}
	if !slices.ContainsFunc(result.ConsultedScopes(), func(scope analysis.Scope) bool {
		return scope.Compare(wantScope) == 0
	}) {
		t.Errorf("consulted scopes = %v, want %s", result.ConsultedScopes(), wantScope)
	}
	assertForwardIssueEvidence(t, result.Metadata, netmodel.IssueMissingNeighborMAC)
}

func reportedPhysical(name string) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name: &name, AdminStatus: &admin, OperStatus: &oper, Mtu: &mtu,
		Physical: interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()
}

func physicalWithoutMTU(name string) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	return interfacev1.Interface_builder{
		Name: &name, AdminStatus: &admin, OperStatus: &oper,
		Physical: interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()
}

func reportedAccess(name string, vid uint32) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	admission := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltering := false
	return interfacev1.Interface_builder{
		Name: &name, AdminStatus: &admin, OperStatus: &oper, Mtu: &mtu,
		Physical: interfacev1.PhysicalInterface_builder{Switchport: switchingv1.SwitchportFacet_builder{
			UntaggedVlanIds: []uint32{vid}, FrameAdmission: &admission, IngressFiltering: &ingressFiltering,
		}.Build()}.Build(),
	}.Build()
}

func reportedSVI(name string, vid uint32, mac []byte) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name: &name, AdminStatus: &admin, OperStatus: &oper, Mtu: &mtu,
		Mac:  addrv1.EuiAddress_builder{Eui48: addrv1.Eui48Address_builder{Octets: mac}.Build()}.Build(),
		Vlan: interfacev1.VlanInterface_builder{VlanId: &vid}.Build(),
		Ip:   ipv1.IpFacet_builder{Ipv4: ipv1.Ipv4Facet_builder{}.Build()}.Build(),
	}.Build()
}

func assertForwardIssueEvidence(t *testing.T, metadata analysis.Metadata, code analysis.IssueCode) {
	t.Helper()
	index := slices.IndexFunc(metadata.Issues(), func(issue analysis.Issue) bool { return issue.Code == code })
	if index < 0 {
		t.Fatalf("issues = %+v, want %s", metadata.Issues(), code)
	}
	for _, ref := range metadata.Issues()[index].Evidence {
		if _, ok := metadata.Evidence().Lookup(ref); !ok {
			t.Errorf("issue %s evidence %q is absent from catalog", code, ref)
		}
	}
}
