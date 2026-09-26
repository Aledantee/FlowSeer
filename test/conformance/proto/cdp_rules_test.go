package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	cdpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/cdp/v1"
)

// cdpV2SoftwareVersion is the 272-octet Version TLV of cdp_v2.pcap, longer
// than the 255 characters of a DisplayString.
const cdpV2SoftwareVersion = "Cisco Internetwork Operating System Software \n" +
	"IOS (tm) C2950 Software (C2950-I6K2L2Q4-M), Version 12.1(22)EA14, RELEASE SOFTWARE (fc1)\n" +
	"Technical Support: http://www.cisco.com/techsupport\n" +
	"Copyright (c) 1986-2010 by cisco Systems, Inc.\n" +
	"Compiled Tue 26-Oct-10 10:35 by nburra"

// cdpV2Neighbor holds the first announcement of the Wireshark sample capture
// cdp_v2.pcap (sha256
// 90ec1ad708be84af0844a45e4e2f7ef6bdd014d72da215568d496231d4f87e4b), decoded
// with tcpdump: CDPv2, TTL 180 s, capability mask 0x00000028 (bits 3 and 5),
// native VLAN 1, full duplex, address and management address 192.168.0.253.
// The capture names no receiving interface, so the local one is invented.
func cdpV2Neighbor() *cdpv1.Neighbor_builder {
	address := func() *addrv1.IpAddress {
		return addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{192, 168, 0, 253}}.Build()}.Build()
	}

	return &cdpv1.Neighbor_builder{
		LocalInterfaceName:  proto.String("GigabitEthernet1/0/1"),
		DeviceId:            proto.String("myswitch"),
		PortId:              proto.String("FastEthernet0/1"),
		Platform:            proto.String("cisco WS-C2950-12"),
		SoftwareVersion:     proto.String(cdpV2SoftwareVersion),
		Capabilities:        []cdpv1.Capability{cdpv1.Capability_CAPABILITY_SWITCH, cdpv1.Capability_CAPABILITY_IGMP},
		VtpManagementDomain: proto.String("MYDOMAIN"),
		NativeVlanId:        proto.Uint32(1),
		Duplex:              phyv1.EthernetDuplex_ETHERNET_DUPLEX_FULL.Enum(),
		Addresses:           []*addrv1.IpAddress{address()},
		ManagementAddresses: []*addrv1.IpAddress{address()},
		TimeToLive:          durationpb.New(180 * time.Second),
		ProtocolVersion:     proto.Uint32(2),
	}
}

// TestCdpNeighborRules holds a CDP neighbor, as a published capture carries
// it, to its key and to the MIB's ranges.
func TestCdpNeighborRules(t *testing.T) {
	if got := len(cdpV2SoftwareVersion); got != 272 {
		t.Fatalf("fixture software version is %d octets, want the capture's 272", got)
	}

	applianceVlan := func(vid uint32) *cdpv1.Neighbor_builder {
		b := cdpV2Neighbor()
		b.ApplianceVlanId = proto.Uint32(vid)
		return b
	}
	withoutDeviceID := cdpV2Neighbor()
	withoutDeviceID.DeviceId = nil
	withoutLocal := cdpV2Neighbor()
	withoutLocal.LocalInterfaceName = nil
	reservedNative := cdpV2Neighbor()
	reservedNative.NativeVlanId = proto.Uint32(4095)
	longDomain := cdpV2Neighbor()
	longDomain.VtpManagementDomain = proto.String(strings.Repeat("D", 33))
	longTTL := cdpV2Neighbor()
	longTTL.TimeToLive = durationpb.New(256 * time.Second)
	versionThree := cdpV2Neighbor()
	versionThree.ProtocolVersion = proto.Uint32(3)
	badOID := cdpV2Neighbor()
	badOID.SystemObjectId = proto.String("1.3.6.1.4.1.9.1.")
	goodOID := cdpV2Neighbor()
	goodOID.SystemObjectId = proto.String("1.3.6.1.4.1.9.1.559")

	runFieldCases(t, []fieldCase{
		{name: "cdp_v2.pcap announcement", message: cdpV2Neighbor().Build()},
		{name: "appliance VLAN 4095 kept as sent", message: applianceVlan(4095).Build()},
		{name: "appliance VLAN 0 kept as sent", message: applianceVlan(0).Build()},
		{name: "system object identifier", message: goodOID.Build()},
		{name: "device ID absent", message: withoutDeviceID.Build(), wantField: "device_id", wantText: "value is required"},
		{name: "local interface absent", message: withoutLocal.Build(), wantField: "local_interface_name", wantText: "value is required"},
		{name: "native VLAN 4095", message: reservedNative.Build(), wantField: "native_vlan_id", wantText: "VLAN"},
		{name: "VTP domain of 33 octets", message: longDomain.Build(), wantField: "vtp_management_domain", wantText: "32 bytes"},
		{name: "appliance VLAN beyond 12 bits", message: applianceVlan(4096).Build(), wantField: "appliance_vlan_id", wantText: "less than or equal to 4095"},
		{name: "time to live beyond one octet", message: longTTL.Build(), wantField: "time_to_live", wantText: "less than or equal to 255s"},
		{name: "CDP version 3", message: versionThree.Build(), wantField: "protocol_version", wantText: "less than or equal to 2"},
		{name: "malformed system object identifier", message: badOID.Build(), wantField: "system_object_id", wantText: "match regex"},
	})
}
