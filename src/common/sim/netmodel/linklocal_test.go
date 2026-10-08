package netmodel_test

import (
	"net/netip"
	"slices"
	"testing"

	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

// TestLoadRoutingKeepsLinkLocalPrefixOnEachInterface proves a shared link-local
// subnet does not conflict across interfaces in one VRF.
func TestLoadRoutingKeepsLinkLocalPrefixOnEachInterface(t *testing.T) {
	input := loadInput{}
	for i, name := range []string{"eth1", "eth2"} {
		iface := routedPhysicalInterface(name)
		iface.SetIp(ipv1.IpFacet_builder{NetworkInstance: new("default"), Ipv6: ipv1.Ipv6Facet_builder{}.Build()}.Build())
		input.ifaces = append(input.ifaces, iface)
		addr := [16]byte{0xfe, 0x80}
		addr[15] = byte(i + 1)
		input.addrs = append(input.addrs, ipv1.InterfaceAddress_builder{
			InterfaceName: &name,
			Address:       protoIPv6Addr(addr),
			Prefix:        protoIPv6Prefix([16]byte{0xfe, 0x80}, 64),
		}.Build())
	}
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})
	if loaded.Spec.Config.Routing == nil {
		t.Fatal("missing routing config")
	}
	vrf := loaded.Spec.Config.Routing.VRFs[routing.DefaultVRF]
	for _, tc := range []struct{ name, prefix string }{{"eth1", "fe80::1/64"}, {"eth2", "fe80::2/64"}} {
		if got := vrf.Interfaces[tc.name].Prefixes; !slices.Equal(got, []netip.Prefix{netip.MustParsePrefix(tc.prefix)}) {
			t.Errorf("%s prefixes = %v, want [%s]", tc.name, got, tc.prefix)
		}
	}
	if issues := loaded.Metadata.Issues(); slices.ContainsFunc(issues, func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueConflictAddress
	}) {
		t.Errorf("issues = %+v, want no address conflict", issues)
	}
}
