// observe_test.go drives each first-hop behavior directly, with the deps
// the runner would build, inside a synctest bubble so the observation
// windows and hello intervals run on the fake clock. Each behavior gets
// the response fixture that confirms it, a mutation of that fixture that
// must not, and the quiet leg.

package fh_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// armedStep is one step a behavior armed through the recorder.
type armedStep struct {
	name string
	step check.Step
}

// teardownRecorder is the [check.Teardown] the direct harness hands a
// behavior: it keeps the steps in arm order for the test to run.
type teardownRecorder struct {
	steps []armedStep
}

func (r *teardownRecorder) Arm(name string, fn check.Step) {
	r.steps = append(r.steps, armedStep{name: name, step: fn})
}

// names returns the armed step names in arm order.
func (r *teardownRecorder) names() []string {
	out := make([]string, len(r.steps))
	for i, s := range r.steps {
		out[i] = s.name
	}
	return out
}

// directResult is what one direct run of a behavior produced.
type directResult struct {
	verdict  check.Verdict
	err      error
	teardown *teardownRecorder
	emitter  *ghostEmitter
}

// runDirect calls the named behavior with deps built from leg, target,
// and window, the way the runner builds them, and returns what it
// produced. Call it inside a synctest bubble so the window is fake time.
func runDirect(t *testing.T, leg *testtest.Leg, name string, target check.Target, window time.Duration) directResult {
	t.Helper()
	entry, run, ok := catalog.MustNew(fh.Behaviors()...).Lookup(name, "")
	if !ok {
		t.Fatalf("catalog entry not found: %s", name)
	}
	rec := &teardownRecorder{}
	em := &ghostEmitter{}
	deps := check.Deps{
		AttackLeg: leg,
		Target:    target,
		Entry:     entry,
		Observe:   window,
		Emitter:   em,
	}
	if entry.Restore.Kind == check.RestoreActive {
		deps.Teardown = rec
	}
	v, err := run(context.Background(), deps)
	return directResult{verdict: v, err: err, teardown: rec, emitter: em}
}

// pusher delivers frames on a leg at later times on the bubble's clock.
// The bubble must join every timed goroutine before its body returns, so
// a test calls join once the behavior has returned.
type pusher struct {
	leg *testtest.Leg
	wg  sync.WaitGroup
}

// at delivers frame after delay.
func (p *pusher) at(delay time.Duration, frame []byte) {
	p.wg.Go(func() {
		time.Sleep(delay)
		p.leg.PushRX(frame)
	})
}

// join waits for every scheduled delivery, which the fake clock runs out
// at once while the test waits.
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

// wantVerdict fails the test unless the result's verdict has the kind and
// its detail contains want.
func wantVerdict(t *testing.T, r directResult, kind findings.VerdictKind, want string) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("behavior error = %v, want nil", r.err)
	}
	if r.verdict.Kind != kind || !strings.Contains(r.verdict.Detail, want) {
		t.Errorf("verdict = %s %q, want %s with %q", r.verdict.Kind, r.verdict.Detail, kind, want)
	}
}

// Offsets of the fields the mutations change, from the start of the
// Ethernet frame: the Ethernet destination and source, and the fields
// after the 14-byte Ethernet, 20-byte IPv4, and 8-byte UDP headers.
const (
	offEthDst   = 0
	offEthSrc   = 6
	offHSRPGrp  = 14 + 20 + 8 + 6 // group, the seventh HSRP octet
	offGLBPGrp  = 14 + 20 + 8 + 3 // group low octet of the GLBP header
	offGLBPStat = 14 + 20 + 8 + 12 + 2 + 1
	offVRID     = 14 + 20 + 1 // VRID, the second VRRP octet
	offLLMNRID  = 14 + 20 + 8 + 1
)

// otherMAC is a sender that is neither the attack leg nor a fixture peer.
var otherMAC = net.HardwareAddr{0x00, 0xde, 0xad, 0xbe, 0xef, 0x01}

func TestARPSpoof_Observe(t *testing.T) {
	response := fixturePackets(t, "arpspoof_response.pcap")
	arpRequest, echoReply := response[0], response[1]
	cases := []struct {
		name   string
		frames [][]byte
		kind   findings.VerdictKind
		want   string
	}{
		{"reply to the attack leg confirms", [][]byte{echoReply}, findings.VerdictConfirmed, "the victim sent its gateway traffic to the attack leg"},
		{"reply to another MAC resists", [][]byte{withMAC(echoReply, offEthDst, otherMAC)}, findings.VerdictResisted, "sent its reply for 172.16.0.1 to 00:de:ad:be:ef:01"},
		{"who-has for the gateway resists", [][]byte{arpRequest}, findings.VerdictResisted, "the victim asked who-has 172.16.0.1"},
		{"reply from another sender is skipped", [][]byte{withMAC(echoReply, offEthSrc, otherMAC)}, findings.VerdictPending, "no reply from the victim in 1s"},
		{"quiet leg is pending", nil, findings.VerdictPending, "no reply from the victim in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
				for _, f := range tc.frames {
					leg.PushRX(f)
				}
				r := runDirect(t, leg, "arpspoof", fixtureTarget(), time.Second)
				wantVerdict(t, r, tc.kind, tc.want)
				if got := r.teardown.names(); strings.Join(got, ",") != "neighbor-unicast-repair-victim,neighbor-unicast-repair-gateway" {
					t.Errorf("armed steps = %v, want the two repairs", got)
				}
			})
		})
	}
}

func TestGratARP_Observe(t *testing.T) {
	response := fixturePackets(t, "gratarp_response.pcap")
	echoReply, arpRequest := response[0], response[1]
	cases := []struct {
		name   string
		frames [][]byte
		kind   findings.VerdictKind
		want   string
	}{
		{"reply to the attack leg confirms", [][]byte{echoReply}, findings.VerdictConfirmed, "the gateway sent its traffic for 10.0.0.1 to the attack leg"},
		{"reply to another MAC resists", [][]byte{withMAC(echoReply, offEthDst, otherMAC)}, findings.VerdictResisted, "sent its reply for 10.0.0.1 to 00:de:ad:be:ef:01"},
		{"who-has for the virtual IP resists", [][]byte{arpRequest}, findings.VerdictResisted, "the gateway asked who-has 10.0.0.1"},
		{"quiet leg is pending", nil, findings.VerdictPending, "no reply from the gateway in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
				for _, f := range tc.frames {
					leg.PushRX(f)
				}
				r := runDirect(t, leg, "gratarp", fixtureTarget(), time.Second)
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestHSRP_Observe(t *testing.T) {
	response := fixturePackets(t, "hsrp_response.pcap")
	active, resign, speak := response[0], response[1], response[2]
	cases := []struct {
		name   string
		pre    []byte
		later  []byte
		window time.Duration
		kind   findings.VerdictKind
		want   string
		sends  int
	}{
		{"resign after the coup confirms", active, resign, 9 * time.Second, findings.VerdictConfirmed, "sent a Resign after the coup", 3},
		{"speak hello after the coup confirms", active, speak, 9 * time.Second, findings.VerdictConfirmed, "left the active state for state 8", 3},
		{"silence for the hold time confirms", active, nil, 15 * time.Second, findings.VerdictConfirmed, "fell silent for the 10s hold time", 6},
		{"silence short of the hold time is pending", active, nil, 9 * time.Second, findings.VerdictPending, "fell silent after the coup, but the 9s window is shorter than the 10s hold time: pass --observe 13s", 4},
		{"active hello at the window's end resists", active, active, 9 * time.Second, findings.VerdictResisted, "still sends hellos in state active", 4},
		{"hello for another group is not the pre-read", withByte(active, offHSRPGrp, 2), nil, 9 * time.Second, findings.VerdictPending, "no active router for group 1 in 3s", 0},
		{"hello from the attack leg is not the pre-read", withMAC(active, offEthSrc, fixtureMAC), nil, 9 * time.Second, findings.VerdictPending, "no active router for group 1 in 3s", 0},
		{"quiet pre-read is pending", nil, nil, 9 * time.Second, findings.VerdictPending, "no active router for group 1 in 3s", 0},
		{"short window is pending", active, nil, 4 * time.Second, findings.VerdictPending, "pass --observe 15s", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(4*time.Second, tc.later)
					p.at(7*time.Second, tc.later)
				}
				r := runDirect(t, leg, "hsrp", fixtureTarget(), tc.window)
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != tc.sends {
					t.Errorf("sends = %d, want %d", got, tc.sends)
				}
			})
		})
	}
}

// TestHSRP_GroupZero reads the group byte and the virtual IP back from
// every frame sent for group 0, the value a zero-initialized field would
// also carry.
func TestHSRP_GroupZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
		leg.PushRX(withByte(fixturePackets(t, "hsrp_response.pcap")[0], offHSRPGrp, 0))
		target := fixtureTarget()
		target.Group = new(uint16(0))
		r := runDirect(t, leg, "hsrp", target, 6*time.Second)
		if r.err != nil {
			t.Fatal(r.err)
		}
		for _, s := range r.teardown.steps {
			if err := s.step(context.Background()); err != nil {
				t.Fatalf("step %s: %v", s.name, err)
			}
		}
		tx := leg.TX()
		if len(tx) != 4 {
			t.Fatalf("sends = %d, want coup, two hellos, and resign", len(tx))
		}
		for i, frame := range tx {
			if frame[offHSRPGrp] != 0 {
				t.Errorf("frame %d group = %d, want 0", i, frame[offHSRPGrp])
			}
			if vip := net.IP(frame[14+20+8+16 : 14+20+8+20]); !vip.Equal(net.IPv4(10, 0, 0, 1)) {
				t.Errorf("frame %d virtual IP = %s, want 10.0.0.1", i, vip)
			}
		}
	})
}

func TestGLBP_Observe(t *testing.T) {
	response := fixturePackets(t, "glbp_response.pcap")
	active, listen := response[0], response[1]
	cases := []struct {
		name   string
		pre    []byte
		later  []byte
		window time.Duration
		kind   findings.VerdictKind
		want   string
	}{
		{"listen hello after the probe confirms", active, listen, 9 * time.Second, findings.VerdictConfirmed, "left the active state for state 0x04"},
		{"silence for the hold time confirms", active, nil, 15 * time.Second, findings.VerdictConfirmed, "fell silent for the 10s hold time"},
		{"silence short of the hold time is pending", active, nil, 9 * time.Second, findings.VerdictPending, "fell silent after the probe, but the 9s window is shorter than the 10s hold time: pass --observe 13s"},
		{"active hello at the window's end resists", active, active, 9 * time.Second, findings.VerdictResisted, "still sends hellos in state active"},
		{"hello for another group is not the pre-read", withByte(active, offGLBPGrp, 2), nil, 9 * time.Second, findings.VerdictPending, "no active gateway for group 1 in 3s"},
		{"listen hello is not the pre-read", withByte(active, offGLBPStat, 4), nil, 9 * time.Second, findings.VerdictPending, "no active gateway for group 1 in 3s"},
		{"short window is pending", active, nil, 4 * time.Second, findings.VerdictPending, "pass --observe 15s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(4*time.Second, tc.later)
					p.at(7*time.Second, tc.later)
				}
				r := runDirect(t, leg, "glbp", fixtureTarget(), tc.window)
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestVRRP_Observe(t *testing.T) {
	advert := fixturePackets(t, "vrrp_response.pcap")[0]
	cases := []struct {
		name   string
		pre    []byte
		keep   bool
		window time.Duration
		kind   findings.VerdictKind
		want   string
	}{
		{"silent master confirms", advert, false, 6 * time.Second, findings.VerdictConfirmed, "fell silent for 6s"},
		{"advertising master resists", advert, true, 6 * time.Second, findings.VerdictResisted, "kept advertising"},
		{"advertisement for another VRID is not the pre-read", withByte(advert, offVRID, 52), false, 6 * time.Second, findings.VerdictPending, "no master for VRID 51 in 1s"},
		{"quiet pre-read is pending", nil, false, 6 * time.Second, findings.VerdictPending, "no master for VRID 51 in 1s"},
		{"short window is pending", advert, false, 3 * time.Second, findings.VerdictPending, "pass --observe 6s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.keep {
					for i := 1; i <= 5; i++ {
						p.at(time.Duration(i)*time.Second+500*time.Millisecond, advert)
					}
				}
				r := runDirect(t, leg, "vrrp", fixtureTarget(), tc.window)
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

// TestVRRP_ChecksumResidue sums the body of the sent advertisement as RFC
// 3768 section 5.3.8 does and expects the residue 0xffff, and pins the
// eight Authentication Data octets as zero.
func TestVRRP_ChecksumResidue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
		leg.PushRX(fixturePackets(t, "vrrp_response.pcap")[0])
		if r := runDirect(t, leg, "vrrp", fixtureTarget(), 6*time.Second); r.err != nil {
			t.Fatal(r.err)
		}
		tx := leg.TX()
		if len(tx) == 0 {
			t.Fatal("no advertisement sent")
		}
		// The frame is padded to the Ethernet minimum, so the body ends
		// where the IPv4 total length says, not where the frame does.
		body := tx[0][34 : 14+binary.BigEndian.Uint16(tx[0][16:18])]
		if len(body) != 20 {
			t.Fatalf("body length = %d, want 20 (8 header, 4 address, 8 auth)", len(body))
		}
		var sum uint32
		for i := 0; i < len(body); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(body[i:]))
		}
		for sum>>16 != 0 {
			sum = (sum & 0xffff) + (sum >> 16)
		}
		if sum != 0xffff {
			t.Errorf("checksum residue = 0x%04x, want 0xffff", sum)
		}
		if !bytes.Equal(body[12:], make([]byte, 8)) {
			t.Errorf("authentication data = %x, want eight zero octets", body[12:])
		}
	})
}

// routedFrame builds an IPv4 datagram from the victim to dstMAC for dst,
// the traffic a victim that took the redirect sends through the attacker.
func routedFrame(t *testing.T, dstMAC net.HardwareAddr, dst net.IP) []byte {
	t.Helper()
	victim := fixtureTarget().Victim
	buf := gopacket.NewSerializeBuffer()
	ip := &layers.IPv4{Version: 4, IHL: 5, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: victim.IP.To4(), DstIP: dst.To4()}
	udp := &layers.UDP{SrcPort: 40000, DstPort: 53}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&layers.Ethernet{SrcMAC: victim.MAC, DstMAC: dstMAC, EthernetType: layers.EthernetTypeIPv4},
		ip, udp, gopacket.Payload([]byte("query")),
	)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buf.Bytes()...)
}

func TestICMPRedirect_Observe(t *testing.T) {
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"victim traffic for the destination through the leg confirms", routedFrame(t, fixtureMAC, net.IPv4(8, 8, 8, 8)), findings.VerdictConfirmed, "the victim routes 8.8.8.8 through the attack leg"},
		{"victim traffic through the gateway is pending", routedFrame(t, fixtureTarget().Gateway.MAC, net.IPv4(8, 8, 8, 8)), findings.VerdictPending, "on its own schedule"},
		{"victim traffic for another destination is pending", routedFrame(t, fixtureMAC, net.IPv4(1, 1, 1, 1)), findings.VerdictPending, "on its own schedule"},
		{"quiet leg is pending", nil, findings.VerdictPending, "the victim sends to 8.8.8.8 on its own schedule: nothing in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t, net.IPv4(10, 0, 0, 99))
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, leg, "icmpredirect", fixtureTarget(), time.Second)
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestLLMNR_Observe(t *testing.T) {
	query := fixturePackets(t, "llmnr_query.pcap")[0]
	t.Run("query is answered with its id", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			leg := fixtureLeg(t, net.IPv4(172, 16, 0, 99))
			leg.PushRX(withByte(withByte(query, offLLMNRID-1, 0x4b), offLLMNRID, 0x2d))
			r := runDirect(t, leg, "llmnr", fixtureTarget(), time.Second)
			wantVerdict(t, r, findings.VerdictConfirmed, "answered host.lab for 172.16.0.10")
			tx := leg.TX()
			if len(tx) != 1 {
				t.Fatalf("sends = %d, want the one answer", len(tx))
			}
			answer := tx[0]
			if got := net.HardwareAddr(answer[offEthDst : offEthDst+6]); !bytes.Equal(got, fixtureTarget().Victim.MAC) {
				t.Errorf("answer destination = %s, want the victim's MAC", got)
			}
			if got := binary.BigEndian.Uint16(answer[offLLMNRID-1:]); got != 0x4b2d {
				t.Errorf("answer id = 0x%04x, want the query's 0x4b2d", got)
			}
			if got := net.IP(answer[30:34]); !got.Equal(net.IPv4(172, 16, 0, 10)) {
				t.Errorf("answer IPv4 destination = %s, want the query's source", got)
			}
			if got := net.IP(answer[len(answer)-4:]); !got.Equal(net.IPv4(172, 16, 0, 99)) {
				t.Errorf("answer rdata = %s, want the leg's own IPv4", got)
			}
		})
	})
	t.Run("query from another sender is pending", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			leg := fixtureLeg(t, net.IPv4(172, 16, 0, 99))
			leg.PushRX(withMAC(query, offEthSrc, otherMAC))
			r := runDirect(t, leg, "llmnr", fixtureTarget(), time.Second)
			wantVerdict(t, r, findings.VerdictPending, "no LLMNR query from the victim in 1s")
			if leg.SendCount() != 0 {
				t.Errorf("sends = %d, want 0", leg.SendCount())
			}
		})
	})
}

// TestLLDPSpoof_IsPending pins the behavior that reads no frame and the
// reason its verdict names.
func TestLLDPSpoof_IsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
		r := runDirect(t, leg, "lldpspoof", fixtureTarget(), time.Second)
		wantVerdict(t, r, findings.VerdictPending, "the neighbor table is readable only over management")
	})
}
