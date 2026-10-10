package netmodel_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

func TestNeighborMissRetainsLoadedInvalidNeighborEvidence(t *testing.T) {
	inName := "in"
	outName := "out"
	input := netmodel.Input{
		Interfaces: []*interfacev1.Interface{routedPhysicalInterface(inName), routedPhysicalInterface(outName)},
		Addresses: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{
				InterfaceName: &inName,
				Address:       protoIPv4Addr([4]byte{192, 0, 2, 1}),
				Prefix:        protoIPv4Prefix([4]byte{192, 0, 2, 0}, 24),
			}.Build(),
			ipv1.InterfaceAddress_builder{
				InterfaceName: &outName,
				Address:       protoIPv4Addr([4]byte{198, 51, 100, 1}),
				Prefix:        protoIPv4Prefix([4]byte{198, 51, 100, 0}, 24),
			}.Build(),
		},
		Neighbors: []*ipv1.NeighborEntry{
			ipv1.NeighborEntry_builder{
				InterfaceName: &outName,
				Ip:            protoIPv4Addr([4]byte{198, 51, 100, 7}),
			}.Build(),
		},
	}
	validateInput(t, input)
	loaded := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "missing-neighbor-mac"}, input)
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	hdr := ip.Header{
		Src:      netip.MustParseAddr("192.0.2.7"),
		Dst:      netip.MustParseAddr("198.51.100.7"),
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	payload, err := hdr.Encode([]byte("neighbor evidence"))
	if err != nil {
		t.Fatalf("encode packet: %v", err)
	}
	iface := loaded.Spec.Config.Routing.VRFs[routing.DefaultVRF].Interfaces[inName]
	result := sw.Receive(trustTestTime, inName, ethernet.Frame{
		Src:       netaddr.MAC{2, 0, 0, 0, 0, 2},
		Dst:       iface.MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	})

	if result.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %s, want neighbor-pending", result.Reason)
	}
	if result.Metadata.Status() != analysis.Incomplete {
		t.Fatalf("status = %s, want Incomplete; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
	}
	if !slices.ContainsFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueMissingNeighborMAC &&
			issue.Scope.Compare(routing.NeighborLookupScope(
				"sw1", routing.DefaultVRF, outName, netip.MustParseAddr("198.51.100.7"),
			)) == 0 &&
			len(issue.Evidence) > 0
	}) {
		t.Errorf("issues = %+v, want evidenced invalid neighbor issue on out", result.Metadata.Issues())
	}
}

func TestNoRouteRetainsConflictingOmittedPrefixEvidence(t *testing.T) {
	inName := "in"
	outName := "out"
	input := netmodel.Input{
		Interfaces: []*interfacev1.Interface{routedPhysicalInterface(inName), routedPhysicalInterface(outName)},
		Addresses: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{
				InterfaceName: &inName,
				Address:       protoIPv4Addr([4]byte{192, 0, 2, 1}),
				Prefix:        protoIPv4Prefix([4]byte{192, 0, 2, 0}, 24),
			}.Build(),
			ipv1.InterfaceAddress_builder{
				InterfaceName: &outName,
				Address:       protoIPv4Addr([4]byte{198, 51, 100, 129}),
				Prefix:        protoIPv4Prefix([4]byte{198, 51, 100, 0}, 24),
			}.Build(),
			ipv1.InterfaceAddress_builder{
				InterfaceName: &outName,
				Address:       protoIPv4Addr([4]byte{198, 51, 100, 129}),
				Prefix:        protoIPv4Prefix([4]byte{198, 51, 100, 128}, 25),
			}.Build(),
		},
	}
	validateInput(t, input)
	loaded := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "conflicting-prefixes"}, input)
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	hdr := ip.Header{
		Src:      netip.MustParseAddr("192.0.2.7"),
		Dst:      netip.MustParseAddr("198.51.100.130"),
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	payload, err := hdr.Encode([]byte("conflicting prefix evidence"))
	if err != nil {
		t.Fatalf("encode packet: %v", err)
	}
	iface := loaded.Spec.Config.Routing.VRFs[routing.DefaultVRF].Interfaces[inName]
	result := sw.Receive(trustTestTime, inName, ethernet.Frame{
		Src:       netaddr.MAC{2, 0, 0, 0, 0, 2},
		Dst:       iface.MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	})

	if result.Reason != routing.ReasonNoRoute {
		t.Fatalf("reason = %s, want no-route", result.Reason)
	}
	if result.Metadata.Status() != analysis.Unstable {
		t.Fatalf("status = %s, want Unstable; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
	}
	routeLookupScope := routing.RouteLookupScope("sw1", routing.DefaultVRF, netip.MustParseAddr("198.51.100.130"))
	if !slices.ContainsFunc(result.ConsultedScopes(), func(scope analysis.Scope) bool {
		return scope.Compare(routeLookupScope) == 0
	}) {
		t.Errorf("consulted scopes = %v, want %s", result.ConsultedScopes(), routeLookupScope)
	}
	routingScope := routing.VRFScope("sw1", routing.DefaultVRF)
	issueIndex := slices.IndexFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueConflictAddress && issue.Scope.Compare(routingScope) == 0
	})
	if issueIndex < 0 {
		t.Fatalf("issues = %+v, want address conflict at %s", result.Metadata.Issues(), routingScope)
	}
	issue := result.Metadata.Issues()[issueIndex]
	if len(issue.Evidence) != 1 {
		t.Fatalf("issue evidence = %+v, want one original reference", issue.Evidence)
	}
	evidence, ok := result.Metadata.Evidence().Lookup(issue.Evidence[0])
	if !ok {
		t.Fatalf("issue evidence %q is absent from forwarding catalog", issue.Evidence[0])
	}
	if evidence.Origin != "snapshot" || !strings.Contains(evidence.Context, "conflict in ip_address for out/198.51.100.129") {
		t.Errorf("evidence = %+v, want original address conflict provenance", evidence)
	}
}
