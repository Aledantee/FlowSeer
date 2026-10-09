package routing_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// RFC 4861 section 4.4 puts R at bit 31 of the flags word after the ICMP
// header: https://www.rfc-editor.org/rfc/rfc4861.html#section-4.4.
func TestHostAdvertisementClearsRouterFlag(t *testing.T) {
	cfg := neighborLifecycleConfig(routing.NeighborPolicy{Mode: routing.NeighborDisabled})
	cfg.ClearRouterFlag = true
	iface := cfg.VRFs[routing.DefaultVRF].Interfaces["vlan10"]
	own := iface.Prefixes[1].Addr()
	header := ip.Header{Src: lifecycleDstV6, Dst: own, Protocol: 58, HopLimit: 255, V6: &ip.V6{}}
	body, err := ndp.Encode(header, ndp.Message{Type: ndp.NeighborSolicitation, Target: own, HasLinkLayerAddr: true, LinkLayerAddr: netaddr.MAC{2, 0, 0, 0, 0, 99}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := header.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	frame := ethernet.Frame{Src: netaddr.MAC{2, 0, 0, 0, 0, 99}, Dst: iface.MAC, EtherType: ethernet.EtherTypeIPv6, Payload: payload}
	checked := 0
	for _, clone := range []bool{false, true} {
		l := mustNewRouting(t, cfg)
		if clone {
			l = l.Clone()
		}
		out := l.Receive(testNow, "vlan10", frame)
		if len(out.Emissions) != 1 {
			t.Fatalf("clone=%t: answers = %d, want 1", clone, len(out.Emissions))
		}
		raw := out.Emissions[0].Frame.Payload
		if len(raw) < 64 || raw[40] != 136 || raw[44] != 0x60 {
			t.Errorf("clone=%t: advertisement = % x, want type 136 and flags byte 0x60 (S/O, no R)", clone, raw)
		}
		checked++
	}
	if checked != 2 {
		t.Fatalf("checked cases = %d, want 2", checked)
	}
}

func TestRouterFlagConfigurationChangesDiffAndRetention(t *testing.T) {
	a := neighborLifecycleConfig(routing.NeighborPolicy{Mode: routing.NeighborDisabled})
	b := a.Clone()
	b.ClearRouterFlag = true
	changes := routing.Diff(a, b)
	if len(changes) != 1 {
		t.Fatalf("Diff = %+v, want one Router flag change", changes)
	}
	c := changes[0]
	if c.Layer != routing.LayerName || c.Subject != (trace.Subject{Kind: "routing"}) || c.Field != "clear_router_flag" || c.From.Canonical() != "false" || c.To.Canonical() != "true" {
		t.Errorf("change = %+v, want routing clear_router_flag false to true", c)
	}
	if routing.RetentionKey(a, layer.Env{NodeID: "sw"}) == routing.RetentionKey(b, layer.Env{NodeID: "sw"}) {
		t.Error("retention key omits Router flag behavior")
	}
}
