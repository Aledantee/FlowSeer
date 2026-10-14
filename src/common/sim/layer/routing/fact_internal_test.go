package routing

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
)

func TestPacketSnapshotAddressInitialization(t *testing.T) {
	for _, test := range []struct {
		name   string
		header ip.Header
		want   string
	}{
		{"zero", ip.Header{}, `interface="in";ether_type=0;src="undecoded";dst="undecoded";hop_limit=0;valid=false;reason="bad-header"`},
		{"known source", ip.Header{Src: netip.MustParseAddr("192.0.2.1")}, `interface="in";ether_type=0;src="192.0.2.1";dst="undecoded";hop_limit=0;valid=false;reason="bad-header"`},
		{"known destination", ip.Header{Dst: netip.MustParseAddr("2001:db8::1")}, `interface="in";ether_type=0;src="undecoded";dst="2001:db8::1";hop_limit=0;valid=false;reason="bad-header"`},
		{"unspecified", ip.Header{Src: netip.IPv4Unspecified(), Dst: netip.IPv6Unspecified()}, `interface="in";ether_type=0;src="0.0.0.0";dst="::";hop_limit=0;valid=false;reason="bad-header"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fact := packetSnapshot("in", ethernet.Frame{}, test.header, false, ReasonBadHeader)
			if fact.TypeID() != "routing.packet_decision" || fact.Canonical() != test.want {
				t.Errorf("packet fact = %s %s, want routing.packet_decision %s", fact.TypeID(), fact.Canonical(), test.want)
			}
		})
	}
}

func TestRouteSnapshotAbsentNextHop(t *testing.T) {
	sel := &selection{candidates: []routeEntry{{Prefix: netip.MustParsePrefix("10.0.20.0/24"), Interface: "out", kind: routeConnected}}}
	fact := routeSnapshot("default", netip.Addr{}, sel)
	want := `vrf="default";destination="undecoded";matched=true;prefix="10.0.20.0/24";next_hop="absent";interface="out";kind="connected";hash_src="undecoded";hash_dst="undecoded";hash_flow_label=0;hash=0;chosen=0;candidates=[absent|out|absent]`
	if fact.TypeID() != "routing.lookup_decision" || fact.Canonical() != want {
		t.Errorf("route fact = %s %s, want routing.lookup_decision %s", fact.TypeID(), fact.Canonical(), want)
	}
	if got := routeSnapshot("default", netip.Addr{}, nil).Canonical(); got != `vrf="default";destination="undecoded";matched=false` {
		t.Errorf("unmatched fact = %s", got)
	}
}
