package conformance

import (
	"net/netip"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	natv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/nat/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
)

func ipv4Range(start, end string) *addrv1.IpRange {
	s := netip.MustParseAddr(start).As4()
	e := netip.MustParseAddr(end).As4()
	return addrv1.IpRange_builder{
		V4: addrv1.Ipv4Range_builder{
			Start: addrv1.Ipv4Address_builder{Octets: s[:]}.Build(),
			End:   addrv1.Ipv4Address_builder{Octets: e[:]}.Build(),
		}.Build(),
	}.Build()
}

func natEndpoint(t *testing.T, ip string, port *uint32) *natv1.NatEndpoint {
	t.Helper()
	b := &natv1.NatEndpoint_builder{
		Address: ipAddress(t, ip),
	}
	if port != nil {
		b.Port = port
	}
	return b.Build()
}

func validNatMapping(t *testing.T) *natv1.NatMapping_builder {
	t.Helper()
	return &natv1.NatMapping_builder{
		NetworkInstance: proto.String("default"),
		Kind:            natv1.NatMappingKind_NAT_MAPPING_KIND_DYNAMIC.Enum(),
		Translations: []natv1.NatTranslation{
			natv1.NatTranslation_NAT_TRANSLATION_OUTBOUND_SOURCE,
		},
		LocalAddresses:  ipv4Range("192.168.1.0", "192.168.1.255"),
		GlobalAddresses: ipv4Range("203.0.113.1", "203.0.113.1"),
	}
}

func TestNatMappingRules(t *testing.T) {
	withoutNetInst := validNatMapping(t)
	withoutNetInst.NetworkInstance = nil

	emptyNetInst := validNatMapping(t)
	emptyNetInst.NetworkInstance = proto.String("")

	netInst256 := validNatMapping(t)
	netInst256.NetworkInstance = proto.String(strings.Repeat("a", 256))

	withoutKind := validNatMapping(t)
	withoutKind.Kind = nil

	kindUnspecified := validNatMapping(t)
	kindUnspecified.Kind = natv1.NatMappingKind_NAT_MAPPING_KIND_UNSPECIFIED.Enum()

	noTranslations := validNatMapping(t)
	noTranslations.Translations = nil

	dupTranslations := validNatMapping(t)
	dupTranslations.Translations = []natv1.NatTranslation{
		natv1.NatTranslation_NAT_TRANSLATION_OUTBOUND_SOURCE,
		natv1.NatTranslation_NAT_TRANSLATION_OUTBOUND_SOURCE,
	}

	unspecTranslation := validNatMapping(t)
	unspecTranslation.Translations = []natv1.NatTranslation{
		natv1.NatTranslation_NAT_TRANSLATION_UNSPECIFIED,
	}

	withoutLocalAddrs := validNatMapping(t)
	withoutLocalAddrs.LocalAddresses = nil

	noGlobalNoIf := validNatMapping(t)
	noGlobalNoIf.GlobalAddresses = nil

	noGlobalWithIf := validNatMapping(t)
	noGlobalWithIf.GlobalAddresses = nil
	noGlobalWithIf.InterfaceName = proto.String("ether1")

	name256 := validNatMapping(t)
	name256.Name = proto.String(strings.Repeat("m", 256))

	protocol256 := validNatMapping(t)
	protocol256.Protocol = packetv1.IpProtocol(256).Enum()

	orderedPorts := validNatMapping(t)
	orderedPorts.LocalPorts = packetv1.TransportPortRange_builder{
		Start: proto.Uint32(1000),
		End:   proto.Uint32(2000),
	}.Build()

	unorderedPorts := validNatMapping(t)
	unorderedPorts.LocalPorts = packetv1.TransportPortRange_builder{
		Start: proto.Uint32(2000),
		End:   proto.Uint32(1000),
	}.Build()

	ifEmpty := validNatMapping(t)
	ifEmpty.InterfaceName = proto.String("")

	if256 := validNatMapping(t)
	if256.InterfaceName = proto.String(strings.Repeat("i", 256))

	runFieldCases(t, []fieldCase{
		{name: "valid mapping with global_addresses passes", message: validNatMapping(t).Build()},
		{name: "without global_addresses with interface_name passes", message: noGlobalWithIf.Build()},
		{name: "without global_addresses without interface_name fails", message: noGlobalNoIf.Build(), wantRule: "nat_mapping.global_or_interface"},
		{name: "without network_instance fails", message: withoutNetInst.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "empty network_instance fails", message: emptyNetInst.Build(), wantField: "network_instance", wantText: "network instance name"},
		{name: "network_instance 256 chars fails", message: netInst256.Build(), wantField: "network_instance", wantText: "network instance name"},
		{name: "without kind fails", message: withoutKind.Build(), wantField: "kind", wantText: "value is required"},
		{name: "kind unspecified fails", message: kindUnspecified.Build(), wantField: "kind", wantText: "must not be in list"},
		{name: "no translations fails", message: noTranslations.Build(), wantField: "translations", wantText: "at least 1"},
		{name: "duplicate translations fails", message: dupTranslations.Build(), wantField: "translations", wantText: "unique items"},
		{name: "unspecified translation fails", message: unspecTranslation.Build(), wantField: "translations[0]", wantText: "must not be in list"},
		{name: "without local_addresses fails", message: withoutLocalAddrs.Build(), wantField: "local_addresses", wantText: "value is required"},
		{name: "name 256 chars fails", message: name256.Build(), wantField: "name", wantText: "at most 255"},
		{name: "protocol 256 fails", message: protocol256.Build(), wantRule: "enum.ip_protocol"},
		{name: "ordered local_ports passes", message: orderedPorts.Build()},
		{name: "unordered local_ports fails", message: unorderedPorts.Build(), wantRule: "transport_port_range.ordered"},
		{name: "empty interface_name fails", message: ifEmpty.Build(), wantField: "interface_name", wantText: "interface name"},
		{name: "interface_name 256 chars fails", message: if256.Build(), wantField: "interface_name", wantText: "interface name"},
	})
}

func validNatSession(t *testing.T) *natv1.NatSession_builder {
	t.Helper()
	return &natv1.NatSession_builder{
		NetworkInstance: proto.String("default"),
		Protocol:        packetv1.IpProtocol_IP_PROTOCOL_TCP.Enum(),
		PrivateSource:   natEndpoint(t, "192.168.1.100", proto.Uint32(50000)),
		PublicSource:    natEndpoint(t, "203.0.113.1", proto.Uint32(10000)),
	}
}

func TestNatSessionRules(t *testing.T) {
	withoutNetInst := validNatSession(t)
	withoutNetInst.NetworkInstance = nil

	withoutProtocol := validNatSession(t)
	withoutProtocol.Protocol = nil

	protocol256 := validNatSession(t)
	protocol256.Protocol = packetv1.IpProtocol(256).Enum()

	withoutPrivSrc := validNatSession(t)
	withoutPrivSrc.PrivateSource = nil

	withoutPubSrc := validNatSession(t)
	withoutPubSrc.PublicSource = nil

	allEndpoints := validNatSession(t)
	allEndpoints.PrivateDestination = natEndpoint(t, "198.51.100.1", proto.Uint32(80))
	allEndpoints.PublicDestination = natEndpoint(t, "198.51.100.1", proto.Uint32(80))
	allEndpoints.Direction = natv1.NatSessionDirection_NAT_SESSION_DIRECTION_OUTBOUND.Enum()
	allEndpoints.ExpiresIn = &durationpb.Duration{Seconds: 300}
	allEndpoints.Counters = natv1.NatSessionCounters_builder{
		InPackets:  proto.Uint64(100),
		OutPackets: proto.Uint64(150),
		InBytes:    proto.Uint64(10000),
		OutBytes:   proto.Uint64(20000),
	}.Build()

	endpointWithoutAddr := validNatSession(t)
	endpointWithoutAddr.PrivateSource = natv1.NatEndpoint_builder{Port: proto.Uint32(80)}.Build()

	endpointPort65536 := validNatSession(t)
	endpointPort65536.PrivateSource = natEndpoint(t, "192.168.1.100", proto.Uint32(65536))

	endpointWithoutPort := validNatSession(t)
	endpointWithoutPort.PrivateSource = natEndpoint(t, "192.168.1.100", nil)

	expiresInNegative := validNatSession(t)
	expiresInNegative.ExpiresIn = &durationpb.Duration{Seconds: -1}

	expiresIn0 := validNatSession(t)
	expiresIn0.ExpiresIn = &durationpb.Duration{Seconds: 0}

	runFieldCases(t, []fieldCase{
		{name: "valid session passes", message: validNatSession(t).Build()},
		{name: "session with all endpoints and counters passes", message: allEndpoints.Build()},
		{name: "without network_instance fails", message: withoutNetInst.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "without protocol fails", message: withoutProtocol.Build(), wantField: "protocol", wantText: "value is required"},
		{name: "protocol 256 fails", message: protocol256.Build(), wantRule: "enum.ip_protocol"},
		{name: "without private_source fails", message: withoutPrivSrc.Build(), wantField: "private_source", wantText: "value is required"},
		{name: "without public_source fails", message: withoutPubSrc.Build(), wantField: "public_source", wantText: "value is required"},
		{name: "endpoint without address fails", message: endpointWithoutAddr.Build(), wantField: "private_source.address", wantText: "value is required"},
		{name: "endpoint with port 65536 fails", message: endpointPort65536.Build(), wantField: "private_source.port", wantText: "less than or equal to 65535"},
		{name: "endpoint without port passes", message: endpointWithoutPort.Build()},
		{name: "negative expires_in fails", message: expiresInNegative.Build(), wantField: "expires_in", wantText: "greater than or equal to 0s"},
		{name: "expires_in 0s passes", message: expiresIn0.Build()},
	})
}
