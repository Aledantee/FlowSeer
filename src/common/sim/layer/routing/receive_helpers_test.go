package routing_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
)

type receivedBinding struct {
	Interface                           string
	Addr                                netip.Addr
	MAC                                 netaddr.MAC
	HasMAC, Solicited, Override, Router bool
}

func receiveBinding(t *testing.T, l *routing.Layer, now time.Time, binding receivedBinding) {
	t.Helper()
	iface, ok := l.Interface(binding.Interface)
	if !ok {
		t.Fatalf("missing receiving interface %q", binding.Interface)
	}
	var local netip.Addr
	for _, prefix := range iface.Prefixes {
		if prefix.Addr().Is4() == binding.Addr.Is4() {
			local = prefix.Addr()
			break
		}
	}
	if !local.IsValid() {
		t.Fatalf("interface %q lacks address for %s", binding.Interface, binding.Addr)
	}
	var frame ethernet.Frame
	if binding.Addr.Is4() {
		op := arp.Request
		if binding.Solicited {
			op = arp.Reply
		}
		var err error
		frame, err = arp.Encode(arp.Message{
			HardwareType: 1, ProtocolType: 0x0800,
			Operation: op, SenderAddr: binding.Addr, SenderMAC: binding.MAC, TargetAddr: local, TargetMAC: iface.MAC,
		}, iface.MAC)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		hdr := ip.Header{Src: binding.Addr, Dst: local, HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
		body, err := ndp.Encode(hdr, ndp.Message{
			Type: ndp.NeighborAdvertisement, Target: binding.Addr,
			LinkLayerAddr: binding.MAC, HasLinkLayerAddr: binding.HasMAC, Solicited: binding.Solicited,
			Override: binding.Override, Router: binding.Router,
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := hdr.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
		frame = ethernet.Frame{Src: binding.MAC, Dst: iface.MAC, EtherType: ethernet.EtherTypeIPv6, Payload: payload}
	}
	l.Receive(now, binding.Interface, frame)
}
