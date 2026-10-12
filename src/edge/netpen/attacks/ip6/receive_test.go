package ip6_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/ip6"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type receiveLeg struct {
	link.Leg
	receive func(context.Context) <-chan link.Frame
}

func (l *receiveLeg) Receive(ctx context.Context) <-chan link.Frame {
	return l.receive(ctx)
}

// wrapped returns the family catalog with name's Run wrapped so a test can
// observe the behavior's own error.
func wrapped(name string, wrap func(run check.Run) check.Run) catalog.Catalog {
	bs := ip6.Behaviors()
	for i := range bs {
		if bs[i].Name == name {
			bs[i].Run = wrap(bs[i].Run)
		}
	}
	return catalog.MustNew(bs...)
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
				leg := &receiveLeg{Leg: fixtureLeg(t, nil)}
				var got error
				target := fixtureTarget()
				target.Victim6.IP = net.ParseIP("fd00::100")
				recs, err := runOne(t, runner.Options{
					Catalog: wrapped(name, func(run check.Run) check.Run {
						return func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
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
							var v check.Verdict
							v, got = run(ctx, deps)
							return v, got
						}
					}),
					AttackLeg: leg,
					WatchLeg:  leg,
					Attacks:   []runner.AttackRef{{Name: name}},
					Target:    target,
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
			leg := fixtureLeg(t, nil)
			leg.PushRX(tc.data)
			var got error
			target := fixtureTarget()
			target.Victim6.IP = net.ParseIP("fd00::100")
			recs, err := runOne(t, runner.Options{
				Catalog: wrapped("roguedhcp6", func(run check.Run) check.Run {
					return func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
						var v check.Verdict
						v, got = run(ctx, deps)
						return v, got
					}
				}),
				AttackLeg: leg,
				Attacks:   []runner.AttackRef{{Name: "roguedhcp6"}},
				Target:    target,
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
