package conformance

import (
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
)

func eui48(octets ...byte) *addrv1.Eui48Address {
	return addrv1.Eui48Address_builder{Octets: octets}.Build()
}

// TestFilterMatchL2Rules holds the L2 match terms to an EtherType rather than
// an 802.3 length, a MAC predicate that names its address, six-octet
// addresses, and PCP and DSCP lists of distinct values that fit their header
// fields.
func TestFilterMatchL2Rules(t *testing.T) {
	rule := func(match *filterv1.FilterMatch_builder) *filterv1.FilterRule {
		return filterv1.FilterRule_builder{
			Action: filterv1.FilterAction_FILTER_ACTION_ACCEPT.Enum(),
			Match:  match.Build(),
		}.Build()
	}
	bridgeGroup := func() *filterv1.FilterMatch_builder {
		return &filterv1.FilterMatch_builder{
			DstMac: filterv1.MacMatch_builder{
				Address: eui48(0x01, 0x80, 0xc2, 0x00, 0x00, 0x00),
				Mask:    eui48(0xff, 0xff, 0xff, 0xff, 0xff, 0xf0),
			}.Build(),
			EtherType: packetv1.EtherType_ETHER_TYPE_LLDP.Enum(),
		}
	}
	lengthNotType := bridgeGroup()
	lengthNotType.EtherType = packetv1.EtherType(1500).Enum()
	noAddress := bridgeGroup()
	noAddress.DstMac = filterv1.MacMatch_builder{Mask: eui48(0xff, 0xff, 0xff, 0xff, 0xff, 0xff)}.Build()
	shortAddress := bridgeGroup()
	shortAddress.SrcMac = filterv1.MacMatch_builder{Address: eui48(0x00, 0x1b, 0x21, 0x0a, 0x0b)}.Build()

	runFieldCases(t, []fieldCase{
		{name: "destination MAC with mask and EtherType", message: rule(bridgeGroup())},
		{name: "802.3 length", message: rule(lengthNotType), wantField: "match.ether_type", wantText: "EtherType"},
		{name: "MAC predicate without address", message: rule(noAddress), wantField: "match.dst_mac.address", wantText: "value is required"},
		{name: "five-octet address", message: rule(shortAddress), wantField: "match.src_mac.address.octets", wantText: "6 bytes"},
		{name: "PCP list", message: rule(&filterv1.FilterMatch_builder{Pcps: []uint32{5, 6}})},
		{name: "PCP above seven", message: rule(&filterv1.FilterMatch_builder{Pcps: []uint32{8}}), wantField: "match.pcps[0]", wantText: "priority code point"},
		{name: "repeated PCP", message: rule(&filterv1.FilterMatch_builder{Pcps: []uint32{5, 5}}), wantField: "match.pcps", wantText: "unique"},
		{name: "DSCP list", message: rule(&filterv1.FilterMatch_builder{Dscps: []packetv1.IpDscp{packetv1.IpDscp_IP_DSCP_EF, packetv1.IpDscp_IP_DSCP_CS0}})},
		{name: "DSCP above 63", message: rule(&filterv1.FilterMatch_builder{Dscps: []packetv1.IpDscp{64}}), wantField: "match.dscps[0]", wantText: "DSCP"},
		{name: "repeated DSCP", message: rule(&filterv1.FilterMatch_builder{Dscps: []packetv1.IpDscp{packetv1.IpDscp_IP_DSCP_EF, packetv1.IpDscp_IP_DSCP_EF}}), wantField: "match.dscps", wantText: "unique"},
	})
}
