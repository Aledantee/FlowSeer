package ip6_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/ip6"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type receiveLeg struct {
	*testtest.Leg
	receive func(context.Context) <-chan link.Frame
}

func (l *receiveLeg) Receive(ctx context.Context) <-chan link.Frame {
	return l.receive(ctx)
}

func TestReceiveFailures(t *testing.T) {
	errReceive := errors.New("receive failed")
	for _, name := range []string{"raguard", "daddos", "roguedhcp6"} {
		for _, tc := range []struct {
			name   string
			cancel bool
			closed bool
			want   error
		}{
			{name: "canceled", cancel: true, want: context.Canceled},
			{name: "canceled and closed", cancel: true, closed: true, want: context.Canceled},
			{name: "receive error", closed: true, want: errReceive},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				leg := &receiveLeg{Leg: testtest.New()}
				t.Cleanup(func() { _ = leg.Close() })
				behavior := ip6.Behaviors()[name]
				var got error
				recs, err := runOne(t, runner.Options{
					AttackLeg: leg,
					WatchLeg:  leg,
					Attacks:   []runner.AttackRef{{Name: name}},
					Behaviors: map[string]runner.Behavior{
						name: func(ctx context.Context, deps runner.Deps) error {
							ctx, cancel := context.WithCancel(ctx)
							defer cancel()
							leg.receive = func(context.Context) <-chan link.Frame {
								ch := make(chan link.Frame, 1)
								if tc.cancel {
									cancel()
								} else {
									ch <- link.Frame{Err: errReceive}
								}
								if tc.closed {
									close(ch)
								}
								return ch
							}
							got = behavior(ctx, deps)
							return got
						},
					},
				})
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				if !errors.Is(got, tc.want) {
					t.Errorf("behavior error: got %v, want %v", got, tc.want)
				}
				if f := findFinding(t, recs); f != nil {
					t.Errorf("finding: got %s, want none after receive failure", f.Detail)
				}
			})
		}
	}
}

func TestRogueDHCPv6MalformedSolicit(t *testing.T) {
	fixture := fixturePackets(t, "roguedhcp6.pcap")[0]
	pkt := gopacket.NewPacket(fixture, layers.LayerTypeEthernet, gopacket.Default)
	eth := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	ip := pkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	udp := pkt.Layer(layers.LayerTypeUDP).(*layers.UDP)
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	dhcp := &layers.DHCPv6{
		MsgType:       layers.DHCPv6MsgTypeSolicit,
		TransactionID: []byte{0, 0x11, 0x22},
		Options: layers.DHCPv6Options{
			layers.NewDHCPv6Option(layers.DHCPv6OptClientID, nil),
		},
	}
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}, eth, ip, udp, dhcp); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "empty frame"},
		{name: "truncated options", data: fixture[:len(fixture)-1]},
		{name: "empty client ID", data: buf.Bytes()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leg := testtest.New()
			t.Cleanup(func() { _ = leg.Close() })
			leg.PushRX(tc.data)
			var got error
			recs, err := runOne(t, runner.Options{
				AttackLeg: leg,
				Attacks:   []runner.AttackRef{{Name: "roguedhcp6"}},
				Behaviors: map[string]runner.Behavior{
					"roguedhcp6": func(ctx context.Context, deps runner.Deps) error {
						got = ip6.RunRogueDHCPv6(ctx, deps)
						return got
					},
				},
			})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got == nil {
				t.Error("behavior error: got nil, want malformed input error")
			}
			if n := leg.SendCount(); n != 0 {
				t.Errorf("TX count: got %d, want 0", n)
			}
			if f := findFinding(t, recs); f != nil {
				t.Errorf("finding: got %s, want none for malformed input", f.Detail)
			}
		})
	}
}
