package conformance

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	dnsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/dns/v1"
)

func validDNSResolver(t *testing.T) *dnsv1.DnsResolver_builder {
	t.Helper()
	return &dnsv1.DnsResolver_builder{
		NetworkInstance: proto.String("default"),
		Servers: []*dnsv1.DnsServer{
			dnsv1.DnsServer_builder{
				Address: ipAddress(t, "192.0.2.53"),
			}.Build(),
		},
		SearchDomains: []string{"example.com"},
	}
}

func TestDnsResolverRules(t *testing.T) {
	withoutNetInst := validDNSResolver(t)
	withoutNetInst.NetworkInstance = nil

	emptyNetInst := validDNSResolver(t)
	emptyNetInst.NetworkInstance = proto.String("")

	netInst256 := validDNSResolver(t)
	netInst256.NetworkInstance = proto.String(strings.Repeat("a", 256))

	search253 := validDNSResolver(t)
	search253.SearchDomains = []string{strings.Repeat("a", 253)}

	search254 := validDNSResolver(t)
	search254.SearchDomains = []string{strings.Repeat("a", 254)}

	searchEmpty := validDNSResolver(t)
	searchEmpty.SearchDomains = []string{""}

	searchDup := validDNSResolver(t)
	searchDup.SearchDomains = []string{"example.com", "example.com"}

	serverWithoutAddr := validDNSResolver(t)
	serverWithoutAddr.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{Port: proto.Uint32(53)}.Build(),
	}

	serverPort0 := validDNSResolver(t)
	serverPort0.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{
			Address: ipAddress(t, "192.0.2.53"),
			Port:    proto.Uint32(0),
		}.Build(),
	}

	serverPort65536 := validDNSResolver(t)
	serverPort65536.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{
			Address: ipAddress(t, "192.0.2.53"),
			Port:    proto.Uint32(65536),
		}.Build(),
	}

	serverPort53 := validDNSResolver(t)
	serverPort53.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{
			Address: ipAddress(t, "192.0.2.53"),
			Port:    proto.Uint32(53),
		}.Build(),
	}

	serverWithoutPort := validDNSResolver(t)
	serverWithoutPort.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{
			Address: ipAddress(t, "192.0.2.53"),
		}.Build(),
	}

	serverIfEmpty := validDNSResolver(t)
	serverIfEmpty.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{
			Address:       ipAddress(t, "fe80::1"),
			InterfaceName: proto.String(""),
		}.Build(),
	}

	serverIf256 := validDNSResolver(t)
	serverIf256.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{
			Address:       ipAddress(t, "fe80::1"),
			InterfaceName: proto.String(strings.Repeat("i", 256)),
		}.Build(),
	}

	serverIfValid := validDNSResolver(t)
	serverIfValid.Servers = []*dnsv1.DnsServer{
		dnsv1.DnsServer_builder{
			Address:       ipAddress(t, "fe80::1"),
			InterfaceName: proto.String("eth0"),
		}.Build(),
	}

	runFieldCases(t, []fieldCase{
		{name: "valid resolver passes", message: validDNSResolver(t).Build()},
		{name: "without network_instance fails", message: withoutNetInst.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "empty network_instance fails", message: emptyNetInst.Build(), wantField: "network_instance", wantText: "network instance name"},
		{name: "network_instance 256 chars fails", message: netInst256.Build(), wantField: "network_instance", wantText: "network instance name"},
		{name: "search domain 253 chars passes", message: search253.Build()},
		{name: "search domain 254 chars fails", message: search254.Build(), wantField: "search_domains[0]", wantText: "at most 253"},
		{name: "empty search domain fails", message: searchEmpty.Build(), wantField: "search_domains[0]", wantText: "at least 1"},
		{name: "duplicate search domains fail", message: searchDup.Build(), wantField: "search_domains", wantText: "unique items"},
		{name: "server without address fails", message: serverWithoutAddr.Build(), wantField: "servers[0].address", wantText: "value is required"},
		{name: "server with port 0 fails", message: serverPort0.Build(), wantField: "servers[0].port", wantText: "greater than or equal to 1"},
		{name: "server with port 65536 fails", message: serverPort65536.Build(), wantField: "servers[0].port", wantText: "less than or equal to 65535"},
		{name: "server with port 53 passes", message: serverPort53.Build()},
		{name: "server without port passes", message: serverWithoutPort.Build()},
		{name: "server with empty interface_name fails", message: serverIfEmpty.Build(), wantField: "servers[0].interface_name", wantText: "interface name"},
		{name: "server with 256 char interface_name fails", message: serverIf256.Build(), wantField: "servers[0].interface_name", wantText: "interface name"},
		{name: "server with valid interface_name passes", message: serverIfValid.Build()},
	})
}
