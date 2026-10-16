// observe_test.go drives each DHCP and IPv6 behavior directly, with the
// deps the runner would build, inside a synctest bubble so the observation
// windows run on the fake clock. Each behavior gets the response fixture
// that confirms it, a mutation of that fixture that must not, and the quiet
// leg.

package ip6_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/ip6"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// directRun is what one direct run of a behavior produced.
type directRun struct {
	verdict check.Verdict
	err     error
	emitter *recordingEmitter
}

// directWindow is the observation window of every direct run: one second
// of the bubble's fake clock.
const directWindow = time.Second

// runDirect calls the named behavior with deps built from leg and target
// and the direct window, the way the runner builds them. Call it inside a
// synctest bubble so the window is fake time.
func runDirect(t *testing.T, leg *testtest.Leg, name string, target check.Target) directRun {
	window := directWindow
	t.Helper()
	entry, run, ok := catalog.MustNew(ip6.Behaviors()...).Lookup(name, "")
	if !ok {
		t.Fatalf("catalog entry not found: %s", name)
	}
	em := &recordingEmitter{}
	deps := check.Deps{
		AttackLeg: leg,
		Target:    target,
		Entry:     entry,
		Observe:   window,
		Emitter:   em,
	}
	v, err := run(context.Background(), deps)
	return directRun{verdict: v, err: err, emitter: em}
}

// pusher delivers frames on a leg at later times on the bubble's clock.
// The bubble must join every timed goroutine before its body returns, so
// a test calls join once the behavior has returned.
type pusher struct {
	leg *testtest.Leg
	wg  sync.WaitGroup
}

func (p *pusher) at(delay time.Duration, frame []byte) {
	p.wg.Go(func() {
		time.Sleep(delay)
		p.leg.PushRX(frame)
	})
}

func (p *pusher) join() { p.wg.Wait() }

// withByte returns a copy of frame with the byte at off replaced.
func withByte(frame []byte, off int, b byte) []byte {
	out := append([]byte(nil), frame...)
	out[off] = b
	return out
}

// withMAC returns a copy of frame with the six bytes at off replaced.
func withMAC(frame []byte, off int, mac net.HardwareAddr) []byte {
	out := append([]byte(nil), frame...)
	copy(out[off:off+6], mac)
	return out
}

// wantVerdict fails the test unless the run's verdict has the kind and
// its detail contains want.
func wantVerdict(t *testing.T, r directRun, kind findings.VerdictKind, want string) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("behavior error = %v, want nil", r.err)
	}
	if r.verdict.Kind != kind || !strings.Contains(r.verdict.Detail, want) {
		t.Errorf("verdict = %s %q, want %s with %q", r.verdict.Kind, r.verdict.Detail, kind, want)
	}
}

// Offsets of the fields the mutations change, from the start of the
// Ethernet frame.
const (
	offEthDst    = 0
	offEthSrc    = 6
	offNSTarget  = 14 + 40 + 8 // the Neighbor Solicitation target after the IPv6 and ICMPv6 headers
	offChaddrLow = 14 + 20 + 8 + 28 + 5
)

var otherMAC = net.HardwareAddr{0x00, 0xde, 0xad, 0xbe, 0xef, 0x01}

// serialize builds a frame from layers with checksums and lengths filled.
func serialize(t *testing.T, ls ...gopacket.SerializableLayer) []byte {
	t.Helper()
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}, ls...); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buf.Bytes()...)
}

// solicitFor builds a host's Neighbor Solicitation for target from
// srcMAC, the probe a host sends a router it has traffic for.
func solicitFor(t *testing.T, srcMAC net.HardwareAddr, target net.IP) []byte {
	t.Helper()
	ip := &layers.IPv6{Version: 6, NextHeader: layers.IPProtocolICMPv6, HopLimit: 255, SrcIP: net.ParseIP("fd00::77"), DstIP: target}
	icmp := &layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborSolicitation, 0)}
	if err := icmp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	return serialize(t,
		&layers.Ethernet{SrcMAC: srcMAC, DstMAC: fixtureMAC, EthernetType: layers.EthernetTypeIPv6},
		ip, icmp, &layers.ICMPv6NeighborSolicitation{TargetAddress: target},
	)
}

// groupTraffic builds an IPv6 UDP datagram from srcMAC to group.
func groupTraffic(t *testing.T, srcMAC net.HardwareAddr, group net.IP) []byte {
	t.Helper()
	ip := &layers.IPv6{Version: 6, NextHeader: layers.IPProtocolUDP, HopLimit: 64, SrcIP: net.ParseIP("fd00::77"), DstIP: group}
	udp := &layers.UDP{SrcPort: 5000, DstPort: 5004}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	return serialize(t,
		&layers.Ethernet{SrcMAC: srcMAC, DstMAC: net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x12, 0x34}, EthernetType: layers.EthernetTypeIPv6},
		ip, udp, gopacket.Payload([]byte("rtp")),
	)
}

func TestRogueRA_Observe(t *testing.T) {
	dad := fixturePackets(t, "roguera_response.pcap")[0]
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"DAD for an address in the prefix confirms", dad, findings.VerdictConfirmed, "a host formed fd00::aabb:ccff:fedd:1 in fd00::/64"},
		{"DAD for an address outside the prefix resists", withByte(dad, offNSTarget+1, 0x01), findings.VerdictResisted, "no host formed an address in fd00::/64 within 1s"},
		{"DAD from the attack leg is not read", withMAC(dad, offEthSrc, fixtureMAC), findings.VerdictResisted, "no host formed an address in fd00::/64 within 1s"},
		{"quiet leg resists", nil, findings.VerdictResisted, "no host formed an address in fd00::/64 within 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, nil)
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, leg, "roguera", fixtureTarget())
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

// TestShortMACIsATargetError pins that an attack leg whose MAC is not six
// octets stops roguera and raguard with the invalid-target code before
// any frame, since no link-local address derives from it.
func TestShortMACIsATargetError(t *testing.T) {
	for _, name := range []string{"roguera", "raguard"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := testtest.New()
				t.Cleanup(func() { _ = leg.Close() })
				leg.SetAddr(link.Addr{MAC: net.HardwareAddr{0x00, 0x11, 0x22, 0x33}})
				r := runDirect(t, leg, name, fixtureTarget())
				if code, ok := errs.CodeOf(r.err); !ok || code != check.ErrCodeTargetInvalid {
					t.Fatalf("error = %v (code %q), want %q", r.err, code, check.ErrCodeTargetInvalid)
				}
				if !strings.Contains(r.err.Error(), "00:11:22:33") {
					t.Errorf("error %q does not name the MAC", r.err)
				}
				if leg.SendCount() != 0 {
					t.Errorf("sends = %d, want 0", leg.SendCount())
				}
			})
		})
	}
}

func TestRAFlood_Observe(t *testing.T) {
	// The first flood source is 00:11:22:33:00:01, whose EUI-64
	// link-local is fe80::211:22ff:fe33:1.
	floodRouter := net.ParseIP("fe80::211:22ff:fe33:1")
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"solicitation for a flood router confirms", solicitFor(t, otherMAC, floodRouter), findings.VerdictConfirmed, "a host probed the flood router fe80::211:22ff:fe33:1"},
		{"solicitation for another address is pending", solicitFor(t, otherMAC, net.ParseIP("fe80::1")), findings.VerdictPending, "no host probed a flood router in 1s: a host probes a router only when it sends traffic"},
		{"quiet leg is pending", nil, findings.VerdictPending, "no host probed a flood router in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, nil)
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, leg, "raflood", fixtureTarget())
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != 5 {
					t.Errorf("sends = %d, want 5", got)
				}
			})
		})
	}
}

func TestNDPSpoof_Observe(t *testing.T) {
	response := fixturePackets(t, "ndpspoof_response.pcap")
	reply, solicit := response[0], response[1]
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"reply to the attack leg confirms", reply, findings.VerdictConfirmed, "the victim sent its traffic for fd00::1 to the attack leg"},
		{"reply to another MAC resists", withMAC(reply, offEthDst, otherMAC), findings.VerdictResisted, "the victim sent its reply for fd00::1 to 00:de:ad:be:ef:01"},
		{"solicitation for the server resists", solicit, findings.VerdictResisted, "the victim solicited fd00::1 itself"},
		{"reply from another sender is skipped", withMAC(reply, offEthSrc, otherMAC), findings.VerdictPending, "no reply from the victim in 1s"},
		{"quiet leg is pending", nil, findings.VerdictPending, "no reply from the victim in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.ParseIP("fd00::999"))
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, leg, "ndpspoof", fixtureTarget())
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestDHCPStarve_Observe(t *testing.T) {
	offer := fixturePackets(t, "dhcpstarve_response.pcap")[0]
	all := make([][]byte, 0, 5)
	for i := 1; i <= 5; i++ {
		all = append(all, withByte(offer, offChaddrLow, byte(i)))
	}
	cases := []struct {
		name   string
		frames [][]byte
		kind   findings.VerdictKind
		want   string
	}{
		{"offers to all five confirm", all, findings.VerdictConfirmed, "the server leased to 5 of 5 spoofed clients"},
		{"an offer to one confirms with the count", [][]byte{offer, offer}, findings.VerdictConfirmed, "the server leased to 1 of 5 spoofed clients"},
		{"an offer to another client resists", [][]byte{withByte(offer, offChaddrLow, 9)}, findings.VerdictResisted, "no offer in 1s"},
		{"an offer from the attack leg is not read", [][]byte{withMAC(offer, offEthSrc, fixtureMAC)}, findings.VerdictResisted, "no offer in 1s"},
		{"quiet leg resists", nil, findings.VerdictResisted, "no offer in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, nil)
				for _, f := range tc.frames {
					leg.PushRX(f)
				}
				r := runDirect(t, leg, "dhcpstarve", fixtureTarget())
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestRogueDHCP_Observe(t *testing.T) {
	query := fixturePackets(t, "roguedhcp_query.pcap")
	discover, requestLeg, requestOther := query[0], query[1], query[2]
	cases := []struct {
		name  string
		pre   []byte
		later []byte
		kind  findings.VerdictKind
		want  string
		sends int
	}{
		{"REQUEST naming the attack leg confirms", discover, requestLeg, findings.VerdictConfirmed, "the victim requested 172.16.0.100 from the attack leg", 2},
		{"REQUEST naming another server resists", discover, requestOther, findings.VerdictResisted, "the victim requested from server 172.16.0.1", 1},
		{"no REQUEST after the OFFER resists", discover, nil, findings.VerdictResisted, "the victim did not request 172.16.0.100", 1},
		{"DISCOVER from another client is not answered", withMAC(discover, offEthSrc, otherMAC), nil, findings.VerdictPending, "no DISCOVER from the victim in 1s", 0},
		{"quiet leg is pending", nil, nil, findings.VerdictPending, "no DISCOVER from the victim in 1s", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, nil)
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(500*time.Millisecond, tc.later)
				}
				r := runDirect(t, leg, "roguedhcp", fixtureTarget())
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != tc.sends {
					t.Errorf("sends = %d, want %d", got, tc.sends)
				}
			})
		})
	}
}

func TestRogueDHCPv6_Observe(t *testing.T) {
	solicit := fixturePackets(t, "roguedhcp6.pcap")[0]
	requests := fixturePackets(t, "roguedhcp6_request.pcap")
	requestLeg, requestOther := requests[0], requests[1]
	cases := []struct {
		name  string
		later []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"REQUEST naming the attack leg's DUID confirms", requestLeg, findings.VerdictConfirmed, "the client requested fd00::200 from the attack leg"},
		{"REQUEST naming another DUID resists", requestOther, findings.VerdictResisted, "the client requested from server DUID 0001000100aabbccddfe"},
		{"REQUEST from another client is not read", withMAC(requestLeg, offEthSrc, otherMAC), findings.VerdictResisted, "the client did not request the lease"},
		{"no REQUEST resists", nil, findings.VerdictResisted, "the client did not request the lease"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, nil)
				leg.PushRX(solicit)
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(500*time.Millisecond, tc.later)
				}
				target := fixtureTarget()
				target.Victim6.IP = net.ParseIP("fd00::100")
				r := runDirect(t, leg, "roguedhcp6", target)
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != 1 {
					t.Errorf("sends = %d, want the one advertise", got)
				}
			})
		})
	}
}

func TestMLD_Observe(t *testing.T) {
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"traffic to the group confirms", groupTraffic(t, otherMAC, net.ParseIP("ff0e::1234")), findings.VerdictConfirmed, "traffic to ff0e::1234 reached the port"},
		{"traffic to another group is pending", groupTraffic(t, otherMAC, net.ParseIP("ff0e::4321")), findings.VerdictPending, "no group traffic in 1s: a Report draws no reply (RFC 2710 section 4)"},
		{"traffic from the attack leg is not read", groupTraffic(t, fixtureMAC, net.ParseIP("ff0e::1234")), findings.VerdictPending, "no group traffic in 1s"},
		{"quiet leg is pending", nil, findings.VerdictPending, "no group traffic in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.ParseIP("fe80::1"))
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, leg, "mld", fixtureTarget())
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}
