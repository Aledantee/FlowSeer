package host_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
)

func TestHostAnswersForItsOwnAddress(t *testing.T) {
	var cases int
	for _, family := range []string{"ARP", "ND"} {
		for _, local := range []bool{true, false} {
			t.Run(family+"/local="+map[bool]string{true: "true", false: "false"}[local], func(t *testing.T) {
				own, foreign, source := netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("192.0.2.11"), netip.MustParseAddr("192.0.2.99")
				prefix := netip.MustParsePrefix("192.0.2.10/24")
				if family == "ND" {
					own, foreign, source = netip.MustParseAddr("2001:db8::a"), netip.MustParseAddr("2001:db8::b"), netip.MustParseAddr("2001:db8::99")
					prefix = netip.MustParsePrefix("2001:db8::a/64")
				}
				target := own
				if !local {
					target = foreign
				}
				h, err := hostmodel.New("h", hostmodel.Config{Address: acceptReceiver, IP: &hostmodel.IP{Addresses: []netip.Prefix{prefix}}})
				if err != nil {
					t.Fatal(err)
				}
				var frame ethernet.Frame
				if family == "ARP" {
					frame, err = arp.Encode(arp.Message{HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Request, SenderMAC: acceptSender, SenderAddr: source, TargetAddr: target}, netaddr.MAC{255, 255, 255, 255, 255, 255})
				} else {
					header := ip.Header{Src: source, Dst: own, Protocol: 58, HopLimit: 255, V6: &ip.V6{}}
					body, encodeErr := ndp.Encode(header, ndp.Message{Type: ndp.NeighborSolicitation, Target: target, HasLinkLayerAddr: true, LinkLayerAddr: acceptSender})
					if encodeErr != nil {
						t.Fatal(encodeErr)
					}
					payload, encodeErr := header.Encode(body)
					if encodeErr != nil {
						t.Fatal(encodeErr)
					}
					frame = ethernet.Frame{Src: acceptSender, Dst: acceptReceiver, EtherType: ethernet.EtherTypeIPv6, Payload: payload}
				}
				if err != nil {
					t.Fatal(err)
				}
				out := h.Receive(fixedTime, "", frame)
				want := 0
				if local {
					want = 1
				}
				if len(out.Emissions) != want {
					t.Fatalf("answers = %d, want %d", len(out.Emissions), want)
				}
				if local {
					answer := out.Emissions[0]
					if !answer.Protocol || answer.Frame.Src != acceptReceiver || answer.Frame.Dst != acceptSender {
						t.Errorf("answer = %+v, want a protocol reply from the host to sender", answer)
					}
					if family == "ARP" {
						msg, err := arp.Decode(answer.Frame)
						if err != nil {
							t.Fatal(err)
						}
						if msg.Operation != arp.Reply || msg.SenderAddr != own || msg.TargetAddr != source {
							t.Errorf("ARP reply = %+v", msg)
						}
					} else {
						header, body, err := ip.Decode(answer.Frame.Payload)
						if err != nil {
							t.Fatal(err)
						}
						msg, err := ndp.Decode(header, body)
						if err != nil {
							t.Fatal(err)
						}
						if msg.Type != ndp.NeighborAdvertisement || msg.Target != own || msg.Router {
							t.Errorf("ND reply = %+v, want local advertisement with Router clear", msg)
						}
					}
				}
				if neighbors := h.State(fixedTime).Neighbors; len(neighbors) != 0 {
					t.Errorf("neighbors = %+v, want no learned bindings", neighbors)
				}
				cases++
			})
		}
	}
	if cases != 4 {
		t.Fatalf("checked cases = %d, want 4", cases)
	}
}

func TestHostResolvesNothing(t *testing.T) {
	h, err := hostmodel.New("h", hostmodel.Config{Address: acceptReceiver, IP: &hostmodel.IP{Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.Originate(fixedTime, "", ethernet.Frame{}, &device.Packet{To: netip.MustParseAddr("192.0.2.99"), Protocol: 17, Payload: []byte("data")})
	if err == nil || !strings.Contains(err.Error(), string(routing.ReasonNeighborMiss)) {
		t.Fatalf("Originate = %+v, %v, want neighbor-miss refusal", out, err)
	}
	if len(out.Emissions) != 0 || len(h.State(fixedTime).Neighbors) != 0 {
		t.Fatalf("refusal resolved or held a neighbor: %+v, %+v", out, h.State(fixedTime))
	}
	if wake, ok := h.NextWake(); ok {
		t.Errorf("wake = %s, want none", wake)
	}
}

func TestHostOriginateRefusesInvalidPriority(t *testing.T) {
	checked := 0
	vid := vlan.ID(10)
	for _, tagged := range []bool{false, true} {
		cfg := hostmodel.Config{Address: acceptReceiver}
		if tagged {
			cfg.VLAN = &vid
		}
		h, err := hostmodel.New("h", cfg)
		if err != nil {
			t.Fatal(err)
		}
		out, err := h.Originate(fixedTime, "", ethernet.Frame{Tags: []vlan.Tag{{PCP: 8}}}, nil)
		if err == nil || len(out.Emissions) != 0 {
			t.Errorf("tagged=%t: Originate = %+v, %v, want invalid priority refusal", tagged, out, err)
		}
		checked++
	}
	if checked != 2 {
		t.Fatalf("checked cases = %d, want 2", checked)
	}
}

// TestHostStateReportsConfiguredNeighbors checks that State carries every field of a configured
// neighbor from the routing layer's table into the device state.
func TestHostStateReportsConfiguredNeighbors(t *testing.T) {
	peer, peerMAC := netip.MustParseAddr("192.0.2.20"), netaddr.MAC{0x02, 0, 0, 0, 0, 0x20}
	h, err := hostmodel.New("h", hostmodel.Config{
		Address: acceptReceiver,
		IP: &hostmodel.IP{
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")},
			Neighbors: map[netip.Addr]netaddr.MAC{peer: peerMAC},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []device.NeighborEntry{{VRF: routing.DefaultVRF, Interface: "h", Addr: peer, MAC: peerMAC, State: device.NeighborReachable}}
	if got := h.State(fixedTime).Neighbors; !slices.Equal(got, want) {
		t.Fatalf("neighbors = %+v, want %+v", got, want)
	}
}
