// observe_test.go drives each L2 behavior directly, with the deps the
// runner would build, inside a synctest bubble so the observation windows
// and hello intervals run on the fake clock. Each behavior gets the
// response fixture that confirms it, a mutation of that fixture that must
// not, the quiet leg, and, where the observable is on the watch leg, a run
// without one.

package l2_test

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

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
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

func (r *teardownRecorder) names() []string {
	out := make([]string, len(r.steps))
	for i, s := range r.steps {
		out[i] = s.name
	}
	return out
}

// directRun is what one direct run of a behavior produced.
type directRun struct {
	verdict  check.Verdict
	err      error
	teardown *teardownRecorder
	emitter  *recordingEmitter
	sysctl   *runner.MapSysctl
}

// directOptions names the legs, mode, target, and window of a direct run.
type directOptions struct {
	leg    *testtest.Leg
	watch  *testtest.Leg
	mode   string
	target check.Target
	window time.Duration
}

// runDirect calls the named behavior with deps built from opts, the way
// the runner builds them, and returns what it produced. Call it inside a
// synctest bubble so the window is fake time.
func runDirect(t *testing.T, name string, opts directOptions) directRun {
	t.Helper()
	entry, run, ok := catalog.MustNew(l2.Behaviors()...).Lookup(name, opts.mode)
	if !ok {
		t.Fatalf("catalog entry not found: %s/%s", name, opts.mode)
	}
	rec := &teardownRecorder{}
	em := &recordingEmitter{}
	sysctl := runner.NewMapSysctl(map[string]string{"net.ipv4.ip_forward": "0"})
	deps := check.Deps{
		AttackLeg: opts.leg,
		Target:    opts.target,
		Entry:     entry,
		Observe:   opts.window,
		Emitter:   em,
		Sysctl:    sysctl,
	}
	if opts.watch != nil {
		deps.WatchLeg = opts.watch
	}
	if entry.Restore.Kind == check.RestoreActive {
		deps.Teardown = rec
	}
	v, err := run(context.Background(), deps)
	return directRun{verdict: v, err: err, teardown: rec, emitter: em, sysctl: sysctl}
}

// runSteps runs the recorded teardown steps in arm order.
func runSteps(t *testing.T, rec *teardownRecorder) {
	t.Helper()
	for _, s := range rec.steps {
		if err := s.step(context.Background()); err != nil {
			t.Fatalf("step %s: %v", s.name, err)
		}
	}
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
	offEthDst      = 0
	offEthSrc      = 6
	offMVRPVID     = 14 + 1 + 2 + 2 + 1 // low octet of the first VID after version, attribute header, and vector header
	offMEDVLANLow  = 14 + 9 + 7 + 4 + 2 + 4 + 2
	otherMEDVLANLo = 0x3D // VLAN 30 in the second policy octet, with the T flag and priority bits unchanged
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

// hopEcho builds the double-tagged check's echo request as the watch leg
// sees it past the trunk: tagged with vlan, or untagged when vlan is 0.
func hopEcho(t *testing.T, src net.HardwareAddr, vlan uint16) []byte {
	t.Helper()
	eth := &layers.Ethernet{SrcMAC: src, DstMAC: fixtureMAC, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, IHL: 5, TTL: 64, Protocol: layers.IPProtocolICMPv4, SrcIP: net.IPv4(10, 0, 0, 1).To4(), DstIP: net.IPv4(10, 0, 0, 2).To4()}
	icmp := &layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(8, 0)}
	if vlan == 0 {
		return serialize(t, eth, ip, icmp)
	}
	eth.EthernetType = layers.EthernetTypeDot1Q
	return serialize(t, eth, &layers.Dot1Q{VLANIdentifier: vlan, Type: layers.EthernetTypeIPv4}, ip, icmp)
}

// gatewayEchoReply builds the gateway's echo reply to the port-steal
// probe, sent to dstMAC for the victim's address.
func gatewayEchoReply(t *testing.T, dstMAC net.HardwareAddr) []byte {
	t.Helper()
	gw := fixtureTarget().Gateway
	return serialize(t,
		&layers.Ethernet{SrcMAC: gw.MAC, DstMAC: dstMAC, EthernetType: layers.EthernetTypeIPv4},
		&layers.IPv4{Version: 4, IHL: 5, TTL: 64, Protocol: layers.IPProtocolICMPv4, SrcIP: gw.IP.To4(), DstIP: net.IPv4(10, 0, 0, 1).To4()},
		&layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(0, 0), Id: 0x4e50, Seq: 1},
		gopacket.Payload([]byte("netpen")),
	)
}

// gatewayWhoHas builds the gateway's ARP request for the victim's address.
func gatewayWhoHas(t *testing.T) []byte {
	t.Helper()
	gw := fixtureTarget().Gateway
	return serialize(t,
		&layers.Ethernet{SrcMAC: gw.MAC, DstMAC: net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, EthernetType: layers.EthernetTypeARP},
		&layers.ARP{AddrType: 1, Protocol: layers.EthernetTypeIPv4, HwAddressSize: 6, ProtAddressSize: 4, Operation: 1, SourceHwAddress: gw.MAC, SourceProtAddress: gw.IP.To4(), DstHwAddress: make([]byte, 6), DstProtAddress: net.IPv4(10, 0, 0, 1).To4()},
	)
}

// TestSTPRoot_BurstSend pins the BPDU against the regenerated fixture: a
// quiet pre-read of one hello time, then one BPDU per hello time for the
// rest of a 3.5 s window.
func TestSTPRoot_BurstSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		r := runDirect(t, "stproot", directOptions{leg: leg, target: fixtureTarget(), window: 3500 * time.Millisecond})
		wantVerdict(t, r, findings.VerdictResisted, "no BPDU in 3.5s")
		tx := leg.TX()
		if len(tx) != 3 {
			t.Fatalf("TX count: got %d, want 3", len(tx))
		}
		fixtures := fixturePackets(t, "stproot.pcap")
		for _, frame := range tx {
			testtest.AssertBytesEqual(t, "STP BPDU", frame, fixtures[0])
		}
		if len(r.emitter.findings) != 1 {
			t.Errorf("findings = %d, want 1", len(r.emitter.findings))
		}
	})
}

func TestSTPRoot_Observe(t *testing.T) {
	response := fixturePackets(t, "stproot_response.pcap")
	ownRoot, legRoot := response[0], response[1]
	cases := []struct {
		name  string
		pre   []byte
		later []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"neighbor naming the leg as root confirms", ownRoot, legRoot, findings.VerdictConfirmed, "names the attack leg as root"},
		{"neighbor keeping its root resists", ownRoot, ownRoot, findings.VerdictResisted, "the neighbor keeps root 32768/00:aa:bb:cc:dd:ee"},
		{"root id from the attack leg itself is not read", ownRoot, withMAC(legRoot, offEthSrc, fixtureMAC), findings.VerdictResisted, "the neighbor keeps root 32768/00:aa:bb:cc:dd:ee"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				leg.PushRX(tc.pre)
				p := &pusher{leg: leg}
				p.at(1500*time.Millisecond, tc.later)
				r := runDirect(t, "stproot", directOptions{leg: leg, target: fixtureTarget(), window: 3500 * time.Millisecond})
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestDTP_Observe(t *testing.T) {
	response := fixturePackets(t, "dtp_response.pcap")
	trunk, access := response[0], response[1]
	cases := []struct {
		name  string
		pre   []byte
		later []byte
		kind  findings.VerdictKind
		want  string
		sends int
	}{
		{"trunk status after the probe confirms", nil, trunk, findings.VerdictConfirmed, "trunk status trunk (0x81) after the probe", 1},
		{"access status after the probe resists", nil, access, findings.VerdictResisted, "trunk status access (0x02) after the probe", 1},
		{"trunk status from the attack leg is not read", nil, withMAC(trunk, offEthSrc, fixtureMAC), findings.VerdictResisted, "no DTP reply in 1s", 1},
		{"port trunking before the probe is pending", trunk, nil, findings.VerdictPending, "the port was trunking before the probe", 0},
		{"quiet leg resists", nil, nil, findings.VerdictResisted, "no DTP reply in 1s", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(600*time.Millisecond, tc.later)
				}
				r := runDirect(t, "dtp", directOptions{leg: leg, target: fixtureTarget(), window: time.Second})
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != tc.sends {
					t.Errorf("sends = %d, want %d", got, tc.sends)
				}
				if got := r.teardown.names(); strings.Join(got, ",") != "access-port-restore" {
					t.Errorf("armed steps = %v, want access-port-restore", got)
				}
			})
		})
	}
}

// vtpRevisionOf decodes the revision of a sent VTP frame with the owned
// layer.
func vtpRevisionOf(t *testing.T, frame []byte) uint32 {
	t.Helper()
	pkt := gopacket.NewPacket(frame, layers.LayerTypeEthernet, gopacket.Default)
	vtp, ok := pkt.Layer(nl.LayerTypeVTP).(*nl.VTP)
	if !ok {
		t.Fatalf("frame decodes to %v, want a VTP layer", pkt.Layers())
	}
	return vtp.Revision
}

// TestVTP_RevisionFollowsThePreRead feeds the switch's revision-41 summary
// and reads the revision each mode sends: 42 on the base mode, 0 on wipe,
// and 42 on set.
func TestVTP_RevisionFollowsThePreRead(t *testing.T) {
	summary41 := fixturePackets(t, "vtp_response.pcap")[0]
	for _, tc := range []struct {
		mode string
		want uint32
	}{{"", 42}, {"wipe", 0}, {"set", 42}} {
		t.Run("mode "+tc.mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				leg.PushRX(summary41)
				r := runDirect(t, "vtp", directOptions{leg: leg, mode: tc.mode, target: fixtureTarget(), window: time.Second})
				if r.err != nil {
					t.Fatal(r.err)
				}
				tx := leg.TX()
				if len(tx) != 1 {
					t.Fatalf("sends = %d, want 1", len(tx))
				}
				if got := vtpRevisionOf(t, tx[0]); got != tc.want {
					t.Errorf("sent revision = %d, want %d", got, tc.want)
				}
			})
		})
	}
}

func TestVTP_Observe(t *testing.T) {
	response := fixturePackets(t, "vtp_response.pcap")
	rev41, rev42, rev0 := response[0], response[1], response[2]
	cases := []struct {
		name  string
		mode  string
		pre   []byte
		later []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"higher revision after the probe confirms", "", rev41, rev42, findings.VerdictConfirmed, "advertises revision 42 after the sent 42"},
		{"pre-read revision again resists", "", rev41, rev41, findings.VerdictResisted, "still advertises revision 41"},
		{"revision 0 after the wipe confirms", "wipe", rev41, rev0, findings.VerdictConfirmed, "advertises revision 0 after the wipe"},
		{"pre-read revision after the wipe resists", "wipe", rev41, rev41, findings.VerdictResisted, "still advertises revision 41"},
		{"summary from the attack leg is not the pre-read", "", withMAC(rev41, offEthSrc, fixtureMAC), nil, findings.VerdictPending, "no VTP summary for domain LABDOMAIN in 500ms"},
		{"quiet pre-read is pending", "", nil, nil, findings.VerdictPending, "no VTP summary for domain LABDOMAIN in 500ms"},
		{"no summary after the probe is pending", "", rev41, nil, findings.VerdictPending, "no summary in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(600*time.Millisecond, tc.later)
				}
				r := runDirect(t, "vtp", directOptions{leg: leg, mode: tc.mode, target: fixtureTarget(), window: time.Second})
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

// probeForwarder copies the attack leg's last frame onto the watch leg
// once the behavior has sent n frames, as a switch that floods it would.
func probeForwarder(leg, watch *testtest.Leg, n int, rewrite func([]byte) []byte) *pusher {
	p := &pusher{leg: watch}
	p.wg.Go(func() {
		for leg.SendCount() < n {
			time.Sleep(time.Millisecond)
		}
		frame := leg.TX()[n-1]
		if rewrite != nil {
			frame = rewrite(frame)
		}
		watch.PushRX(frame)
	})
	return p
}

func TestCAMFlood_Observe(t *testing.T) {
	cases := []struct {
		name    string
		watch   bool
		rewrite func([]byte) []byte
		kind    findings.VerdictKind
		want    string
	}{
		{"probe on the watch leg confirms", true, nil, findings.VerdictConfirmed, "flooded the unicast probe for 00:aa:bb:cc:dd:01 to the watch leg"},
		{"probe to another MAC on the watch leg resists", true, func(f []byte) []byte { return withMAC(f, offEthDst, otherMAC) }, findings.VerdictResisted, "the unicast probe did not reach the watch leg in 1s"},
		{"no watch leg is pending", false, nil, findings.VerdictPending, "no watch leg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				opts := directOptions{leg: leg, target: fixtureTarget(), window: time.Second}
				var p *pusher
				if tc.watch {
					opts.watch = fixtureLeg(t)
					p = probeForwarder(leg, opts.watch, 10_001, tc.rewrite)
				}
				r := runDirect(t, "camflood", opts)
				if p != nil {
					p.join()
				}
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != 10_001 {
					t.Errorf("sends = %d, want 10,001", got)
				}
			})
		})
	}
}

func TestDoubleTag_Observe(t *testing.T) {
	for _, name := range []string{"doubletag", "vlanhop"} {
		cases := []struct {
			name  string
			watch bool
			frame []byte
			kind  findings.VerdictKind
			want  string
		}{
			{"echo tagged with the inner VLAN confirms", true, hopEcho(t, fixtureMAC, 20), findings.VerdictConfirmed, "the echo request crossed into VLAN 20"},
			{"untagged echo confirms", true, hopEcho(t, fixtureMAC, 0), findings.VerdictConfirmed, "the echo request crossed into VLAN 20"},
			{"echo in another VLAN resists", true, hopEcho(t, fixtureMAC, 30), findings.VerdictResisted, "no echo request in VLAN 20 on the watch leg in 1s"},
			{"echo from another sender resists", true, hopEcho(t, otherMAC, 20), findings.VerdictResisted, "no echo request in VLAN 20 on the watch leg in 1s"},
			{"no watch leg is pending", false, nil, findings.VerdictPending, "no watch leg"},
		}
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					leg := fixtureLeg(t)
					opts := directOptions{leg: leg, target: fixtureTarget(), window: time.Second}
					if tc.watch {
						opts.watch = fixtureLeg(t)
						opts.watch.PushRX(tc.frame)
					}
					r := runDirect(t, name, opts)
					wantVerdict(t, r, tc.kind, tc.want)
					if got := leg.SendCount(); got != 1 {
						t.Errorf("sends = %d, want 1", got)
					}
					if len(r.teardown.steps) != 0 {
						t.Errorf("armed steps = %v, want none", r.teardown.names())
					}
				})
			})
		}
	}
}

func TestEtherChannel_Observe(t *testing.T) {
	response := fixturePackets(t, "etherchannel_response.pcap")
	lacpLeg, lacpOther, pagpLeg := response[0], response[1], response[2]
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"LACPDU naming the leg as synchronized partner confirms", lacpLeg, findings.VerdictConfirmed, "names the attack leg as its synchronized partner"},
		{"PAgP naming the leg confirms", pagpLeg, findings.VerdictConfirmed, "a PAgP frame from the switch names the attack leg as its partner"},
		{"LACPDU naming another partner resists", lacpOther, findings.VerdictResisted, "the switch names partner 00:aa:bb:cc:dd:ee"},
		{"LACPDU from the attack leg is not read", withMAC(lacpLeg, offEthSrc, fixtureMAC), findings.VerdictResisted, "no LACP or PAgP from the switch in 1s"},
		{"quiet leg resists", nil, findings.VerdictResisted, "no LACP or PAgP from the switch in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, "etherchannel", directOptions{leg: leg, target: fixtureTarget(), window: time.Second})
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestMVRP_Observe(t *testing.T) {
	joinIn10 := fixturePackets(t, "mvrp_response.pcap")[0]
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"JoinIn for VLAN 10 confirms", joinIn10, findings.VerdictConfirmed, "the switch declares VLAN 10 with a JoinIn"},
		{"JoinIn for another VLAN resists", withByte(joinIn10, offMVRPVID, 30), findings.VerdictResisted, "no MVRP JoinIn for VLAN 10 or 20 from the switch in 1s"},
		{"JoinIn from the attack leg is not read", withMAC(joinIn10, offEthSrc, fixtureMAC), findings.VerdictResisted, "no MVRP JoinIn for VLAN 10 or 20 from the switch in 1s"},
		{"quiet leg resists", nil, findings.VerdictResisted, "no MVRP JoinIn for VLAN 10 or 20 from the switch in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, "mvrp", directOptions{leg: leg, target: fixtureTarget(), window: time.Second})
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestPortSteal_Observe(t *testing.T) {
	cases := []struct {
		name  string
		frame []byte
		kind  findings.VerdictKind
		want  string
	}{
		{"gateway reply to the attack leg confirms", gatewayEchoReply(t, fixtureMAC), findings.VerdictConfirmed, "the gateway sent its traffic for 10.0.0.1 to the attack leg"},
		{"gateway reply to another MAC resists", gatewayEchoReply(t, otherMAC), findings.VerdictResisted, "the gateway sent its reply for 10.0.0.1 to 00:de:ad:be:ef:01"},
		{"gateway who-has resists", gatewayWhoHas(t), findings.VerdictResisted, "the gateway asked who-has 10.0.0.1"},
		{"quiet leg is pending", nil, findings.VerdictPending, "no reply from the gateway in 1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.frame != nil {
					leg.PushRX(tc.frame)
				}
				r := runDirect(t, "portsteal", directOptions{leg: leg, target: fixtureTarget(), window: time.Second})
				wantVerdict(t, r, tc.kind, tc.want)
			})
		})
	}
}

func TestVoiceVLAN_Observe(t *testing.T) {
	policy20 := fixturePackets(t, "voicevlan_response.pcap")[0]
	cases := []struct {
		name  string
		pre   []byte
		later []byte
		kind  findings.VerdictKind
		want  string
		sends int
	}{
		{"policy after the probe confirms", nil, policy20, findings.VerdictConfirmed, "advertised the MED policy for VLAN 20 after the probe", 1},
		{"policy before the probe is pending", policy20, nil, findings.VerdictPending, "the switch advertised the policy before the probe", 0},
		{"policy for another VLAN resists", nil, withByte(policy20, offMEDVLANLow, otherMEDVLANLo), findings.VerdictResisted, "no MED policy for VLAN 20 in 1s", 1},
		{"policy from the attack leg is not read", nil, withMAC(policy20, offEthSrc, fixtureMAC), findings.VerdictResisted, "no MED policy for VLAN 20 in 1s", 1},
		{"quiet leg resists", nil, nil, findings.VerdictResisted, "no MED policy for VLAN 20 in 1s", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(600*time.Millisecond, tc.later)
				}
				r := runDirect(t, "voicevlan", directOptions{leg: leg, target: fixtureTarget(), window: time.Second})
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != tc.sends {
					t.Errorf("sends = %d, want %d", got, tc.sends)
				}
			})
		})
	}
}

// TestVoiceVLAN_FullRange sends VLAN 4094, the top of the twelve-bit id,
// and reads it back through gopacket's LLDP-MED decoder.
func TestVoiceVLAN_FullRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		target := fixtureTarget()
		target.VoiceVLAN = 4094
		r := runDirect(t, "voicevlan", directOptions{leg: leg, mode: "persist", target: target, window: time.Second})
		if r.err != nil {
			t.Fatal(r.err)
		}
		tx := leg.TX()
		if len(tx) != 1 {
			t.Fatalf("sends = %d, want 1", len(tx))
		}
		if policy := medPolicyOf(t, tx[0]); policy.VLANId != 4094 || policy.L2Priority != 5 || policy.DSCPValue != 46 {
			t.Errorf("network policy = %+v, want VLAN 4094, priority 5, DSCP 46", policy)
		}
		if len(r.teardown.steps) != 0 {
			t.Errorf("persist mode armed %v, want nothing", r.teardown.names())
		}
	})
}

// TestTeardownStepCounts lists each actively restored behavior's armed
// steps by name and asserts that every step sends one frame, or writes
// the setting it restores, when run.
func TestTeardownStepCounts(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		steps []string
	}{
		{"dtp", "", []string{"access-port-restore"}},
		{"portsteal", "relay", []string{"ip-forward-restore"}},
		{"voicevlan", "", []string{"lldp-shutdown"}},
		{"etherchannel", "", []string{"port-channel-release"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				r := runDirect(t, tc.name, directOptions{leg: leg, mode: tc.mode, target: fixtureTarget(), window: time.Second})
				if r.err != nil {
					t.Fatalf("run %s: %v", tc.name, r.err)
				}
				if got := r.teardown.names(); strings.Join(got, ",") != strings.Join(tc.steps, ",") {
					t.Fatalf("armed steps = %v, want %v", got, tc.steps)
				}
				if tc.name == "portsteal" {
					if v, _ := r.sysctl.Get("net.ipv4.ip_forward"); v != "1" {
						t.Errorf("ip_forward during the behavior = %q, want 1", v)
					}
					runSteps(t, r.teardown)
					if v, _ := r.sysctl.Get("net.ipv4.ip_forward"); v != "0" {
						t.Errorf("ip_forward after the step = %q, want the 0 read before", v)
					}
					return
				}
				before := leg.SendCount()
				runSteps(t, r.teardown)
				if got := leg.SendCount() - before; got != 1 {
					t.Errorf("the step sent %d frames, want 1", got)
				}
			})
		})
	}
}
