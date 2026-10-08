package vswitch

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

type ingressCountingGate struct{ calls int }

func (g *ingressCountingGate) Learns(string, vlan.ID) bool   { g.calls++; return true }
func (g *ingressCountingGate) Forwards(string, vlan.ID) bool { return true }

func TestIGMPReportClassifiedOnce(t *testing.T) {
	ports, err := port.NewBuilder().Range("p%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	vid := vlan.ID(10)
	s, err := New(Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{Table: map[vlan.ID]string{10: "ten"}, Switchports: map[string]bridge.Switchport{
			"p1": {PVID: &vid, Untagged: []vlan.ID{10}}, "p2": {PVID: &vid, Untagged: []vlan.ID{10}},
		}}}, Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gate := &ingressCountingGate{}
	s.bridge.SetGate(gate, analysis.WholeScope())
	group := netip.MustParseAddr("239.1.1.1")
	body, err := igmp.Encode(igmp.Message{Type: igmp.ReportV2, Group: group})
	if err != nil {
		t.Fatal(err)
	}
	hdr := ip.Header{Src: netip.MustParseAddr("10.0.0.1"), Dst: group, Protocol: 2, HopLimit: 1, V4: &ip.V4{}}
	payload, err := hdr.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	s.Receive(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "p1", ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 1}, Dst: netaddr.MAC{1, 0, 0x5e, 1, 1, 1}, EtherType: ethernet.EtherTypeIPv4, Payload: payload,
	})
	if gate.calls != 1 {
		t.Fatalf("ingress gate calls = %d, want 1", gate.calls)
	}
}
