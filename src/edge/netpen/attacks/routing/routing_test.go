// routing_test.go tests the routing-injection and rogue-WPAD attack
// behaviors against fixture-mirrored TX sequences: byte-for-byte where
// deterministic, field-set where randomized. The pin tests drive each
// behavior directly inside a synctest bubble with the peer's frames fed
// at the moments the sequence expects them, since the windows are seconds
// long; the gate tests exercise the runner, which creates the Stream,
// Teardown, and Deps. The target and the leg's addresses are the fixture's
// values, so the frames the harvest generator pinned still come out byte
// for byte.

package routing_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// fixtureMAC is the attacker MAC every fixture frame carries as its source.
var fixtureMAC = net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

// fixtureTarget builds the target the fixtures were harvested against: the
// router ID the OSPF frames carry and the route the EIGRP update injects.
func fixtureTarget() check.Target {
	_, route4, _ := net.ParseCIDR("10.99.0.0/24")
	return check.Target{
		RouterID: net.IPv4(10, 0, 0, 99),
		Route4:   route4,
	}
}

// fixtureLeg returns a test leg whose own addresses are the fixture MAC
// and the attacker IPv4 the L3 fixtures carry.
func fixtureLeg(t *testing.T) *testtest.Leg {
	t.Helper()
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
	leg.SetAddr(link.Addr{MAC: fixtureMAC, IPv4: net.IPv4(10, 0, 0, 99).To4()})
	return leg
}

// memListener is the in-memory listener the WPAD behavior serves its PAC
// from under test: Accept blocks until Close, so the server loop ends
// without a socket. Its address is a loopback TCP address with a fixed
// port.
type memListener struct {
	closed chan struct{}
}

func newMemListener() *memListener { return &memListener{closed: make(chan struct{})} }

func (l *memListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *memListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *memListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080} }

// memListen is the [check.Deps.Listen] the tests inject.
func memListen(context.Context, string, string) (net.Listener, error) {
	return newMemListener(), nil
}

// familyCatalog returns the routing catalog with every Run wrapped so the
// behaviors take their listener from the in-memory seam.
func familyCatalog() catalog.Catalog {
	bs := routing.Behaviors()
	for i := range bs {
		run := bs[i].Run
		bs[i].Run = func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
			deps.Listen = memListen
			return run(ctx, deps)
		}
	}
	return catalog.MustNew(bs...)
}

// runOne runs the attacks through the runner over the family catalog
// against the fixture target and returns the findings and the run error.
// The stream is drained concurrently with Run.
func runOne(t *testing.T, opts runner.Options) ([]findings.Record, error) {
	t.Helper()
	if len(opts.Catalog.Entries()) == 0 {
		opts.Catalog = familyCatalog()
	}
	if opts.MaxImpact == "" {
		opts.MaxImpact = check.ImpactTemporary
	}
	// A test that sets no window gets a short one, so a behavior that
	// waits its window out for the target's reply does so in milliseconds.
	if opts.Observe == 0 {
		opts.Observe = 20 * time.Millisecond
	}
	r := runner.NewRunner(opts)

	var recs []findings.Record
	done := make(chan struct{})
	go func() {
		for rec := range r.Stream().Iter() {
			recs = append(recs, rec)
		}
		close(done)
	}()

	err := r.Run(context.Background())
	r.Wait()
	<-done
	return recs, err
}

func mustEntry(t *testing.T, name string) check.Entry {
	t.Helper()
	e, _, ok := catalog.MustNew(routing.Behaviors()...).Lookup(name, "")
	if !ok {
		t.Fatalf("catalog entry not found: %s", name)
	}
	return e
}

// findFinding returns the first KindFinding record's Finding, or nil.
func findFinding(t *testing.T, recs []findings.Record) *findings.Finding {
	t.Helper()
	for i := range recs {
		if recs[i].Kind == findings.KindFinding {
			return recs[i].Finding
		}
	}
	return nil
}

// verdictOf returns the attack's verdict record payload, or nil.
func verdictOf(recs []findings.Record) *findings.Verdict {
	for i := range recs {
		if recs[i].Kind == findings.KindVerdict {
			return recs[i].Verdict
		}
	}
	return nil
}

func fixturePackets(t *testing.T, name string) [][]byte {
	t.Helper()
	return testtest.ReadPcap(t, testtest.FixturePath(t, "routing/"+name))
}

// ospfExchange runs the OSPF behavior against the target's two hellos,
// the second arriving one second after the probe, runs the armed steps,
// and returns the leg with all six frames recorded. Call it inside a
// synctest bubble.
func ospfExchange(t *testing.T) (*testtest.Leg, directRun) {
	t.Helper()
	target := fixturePackets(t, "ospf_target.pcap")
	leg := fixtureLeg(t)
	leg.PushRX(target[0])
	p := &pusher{leg: leg}
	p.at(time.Second, target[1])
	r := runDirect(t, leg, "ospf", 20*time.Second)
	p.join()
	if r.err != nil {
		t.Fatalf("RunOSPF() error = %v, want nil", r.err)
	}
	runSteps(t, r.teardown)
	return leg, r
}

// TestOSPF_FixturePins verifies the OSPF attack TX matches the fixture
// byte-for-byte (hello, hello listing the target, db-desc, lsa-update),
// and the teardown TX matches the restore fixture (flush, goodbye). These
// fixtures carry protocol-valid checksums instead of the zero checksums in
// the Python characterization capture, which IOS-XE rejects.
func TestOSPF_FixturePins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg, r := ospfExchange(t)
		wantVerdict(t, r, findings.VerdictConfirmed, "10.0.0.2 lists router ID 10.0.0.99 as a neighbor")

		tx := leg.TX()
		if len(tx) != 6 {
			t.Fatalf("TX count: got %d, want 6 (hello + 2-way hello + db-desc + lsa-update + flush + goodbye)", len(tx))
		}

		fixtures := fixturePackets(t, "ospf.pcap")
		testtest.AssertBytesEqual(t, "OSPF hello", tx[0], fixtures[0])
		testtest.AssertBytesEqual(t, "OSPF 2-way hello", tx[1], fixtures[1])
		testtest.AssertBytesEqual(t, "OSPF db-desc", tx[2], fixtures[2])
		testtest.AssertBytesEqual(t, "OSPF lsa-update", tx[3], fixtures[3])

		restoreFixtures := fixturePackets(t, "ospf_restore.pcap")
		testtest.AssertBytesEqual(t, "OSPF flush", tx[4], restoreFixtures[0])
		testtest.AssertBytesEqual(t, "OSPF goodbye", tx[5], restoreFixtures[1])

		if len(r.emitter.findings) != 1 {
			t.Errorf("findings = %d, want 1", len(r.emitter.findings))
		}
	})
}

// TestEmptyTargetIsSkipped pins the one-round contract on a real behavior:
// with no target the injection sends nothing and arms nothing, the runner
// records a skipped verdict naming the router ID, and the run returns the
// exit-2 error.
func TestEmptyTargetIsSkipped(t *testing.T) {
	leg := fixtureLeg(t)
	recs, err := runOne(t, runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "ospf"}},
	})
	if code, ok := errs.CodeOf(err); !ok || code != check.ErrCodeTargetMissing {
		t.Fatalf("run ospf with no target = %v, want %q", err, check.ErrCodeTargetMissing)
	}
	if errs.ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2", errs.ExitCode(err))
	}
	if leg.SendCount() != 0 {
		t.Errorf("sends = %d, want 0", leg.SendCount())
	}
	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictSkipped || v.Detail != "the check needs: --router-id <ip>" {
		t.Fatalf("verdict = %+v, want skipped naming the router-id flag", v)
	}
}

// TestNeedFieldsPerBehavior pins, per behavior that reads a target, the
// exact flag an empty target reports, and that an IPv6 router ID or route
// is refused with the invalid code and no frame.
func TestNeedFieldsPerBehavior(t *testing.T) {
	_, route6, _ := net.ParseCIDR("2001:db8::/64")
	cases := []struct {
		name   string
		target check.Target
		code   errs.Code
		detail string
	}{
		{"ospf", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --router-id <ip>"},
		{"eigrp", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --route4 <cidr>"},
		{"ospf IPv6 router ID", check.Target{RouterID: net.ParseIP("2001:db8::1")}, check.ErrCodeTargetInvalid, "the check needs: --router-id <ip> wants an IPv4 address"},
		{"eigrp IPv6 route", check.Target{Route4: route6}, check.ErrCodeTargetInvalid, "the check needs: --route4 <cidr> wants an IPv4 network"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leg := fixtureLeg(t)
			name, _, _ := strings.Cut(tc.name, " ")
			recs, err := runOne(t, runner.Options{
				AttackLeg: leg,
				Attacks:   []runner.AttackRef{{Name: name}},
				Target:    tc.target,
			})
			if code, ok := errs.CodeOf(err); !ok || code != tc.code {
				t.Fatalf("run %s = %v (code %q), want %q", name, err, code, tc.code)
			}
			if errs.ExitCode(err) != 2 {
				t.Errorf("exit code = %d, want 2", errs.ExitCode(err))
			}
			if leg.SendCount() != 0 {
				t.Errorf("sends = %d, want 0", leg.SendCount())
			}
			v := verdictOf(recs)
			if v == nil || v.Kind != findings.VerdictSkipped {
				t.Fatalf("verdict = %+v, want skipped", v)
			}
			if v.Detail != tc.detail {
				t.Errorf("verdict detail = %q, want %q", v.Detail, tc.detail)
			}
		})
	}
}

// arpFrame is a broadcast ARP request from the fixture MAC, a frame neither
// routing decoder reads.
func arpFrame() []byte {
	frame := make([]byte, 42)
	copy(frame[0:6], net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(frame[6:12], fixtureMAC)
	frame[12], frame[13] = 0x08, 0x06
	return frame
}

// TestOSPF_HelloBehindAnARPFrameIsRead pins that an unrelated frame ahead of
// the target's hello does not end the observation: the ARP frame is
// skipped, the hello that arrives one second later is read, the hello
// listing the attack leg two seconds later confirms, and the sequence
// proceeds then rather than at the window.
func TestOSPF_HelloBehindAnARPFrameIsRead(t *testing.T) {
	target := fixturePackets(t, "ospf_target.pcap")
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		leg.PushRX(arpFrame())
		p := &pusher{leg: leg}
		p.at(time.Second, target[0])
		p.at(2*time.Second, target[1])
		start := time.Now()
		r := runDirect(t, leg, "ospf", 20*time.Second)
		p.join()
		wantVerdict(t, r, findings.VerdictConfirmed, "lists router ID 10.0.0.99")
		if elapsed := time.Since(start); elapsed != 2*time.Second {
			t.Errorf("returned after %v, want 2s, when the 2-way hello arrived", elapsed)
		}
		if n := leg.SendCount(); n != 4 {
			t.Errorf("sends = %d, want 4 (hello, 2-way hello, db-desc, lsa-update)", n)
		}
		if got := r.teardown.names(); strings.Join(got, ",") != "ospf-lsa-flush,ospf-goodbye" {
			t.Errorf("armed steps = %v, want [ospf-lsa-flush ospf-goodbye]", got)
		}
	})
}

// TestOSPF_NoHelloIsResisted pins that a quiet leg runs the hello interval
// out with the first hello alone sent and a resisted verdict.
func TestOSPF_NoHelloIsResisted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		start := time.Now()
		r := runDirect(t, leg, "ospf", 20*time.Second)
		wantVerdict(t, r, findings.VerdictResisted, "no OSPF hello in 10s")
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Errorf("returned after %v, want the 10s hello interval", elapsed)
		}
		if n := leg.SendCount(); n != 1 {
			t.Errorf("sends = %d, want 1 (the hello only)", n)
		}
	})
}

// TestEIGRP_GoodbyeBehindAnotherFrameIsRead pins that a non-matching first
// frame does not end the observation: the ARP frame is skipped, the
// goodbye with the Peer Termination TLV that arrives one second later is
// read, the verdict is resisted at that moment with no inject sent, and
// the goodbye teardown was armed under its name.
func TestEIGRP_GoodbyeBehindAnotherFrameIsRead(t *testing.T) {
	goodbye := fixturePackets(t, "eigrp_reject.pcap")[0]
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		leg.PushRX(arpFrame())
		p := &pusher{leg: leg}
		p.at(time.Second, goodbye)
		r := runDirect(t, leg, "eigrp", 3*time.Second)
		p.join()
		wantVerdict(t, r, findings.VerdictResisted, "adjacency refused")
		if r.verdict.Observed != time.Second {
			t.Errorf("observed = %v, want 1s, when the goodbye arrived", r.verdict.Observed)
		}
		if n := leg.SendCount(); n != 1 {
			t.Errorf("sends = %d, want 1 (the hello only)", n)
		}
		if got := r.teardown.names(); strings.Join(got, ",") != "eigrp-goodbye" {
			t.Errorf("armed steps = %v, want [eigrp-goodbye]", got)
		}
	})
}

func assertOSPFChecksumValid(t *testing.T, frame []byte) {
	t.Helper()

	if len(frame) < 15 {
		t.Fatalf("frame length: got %d, want at least 15", len(frame))
	}

	ospfOffset := 14 + int(frame[14]&0x0f)*4
	if len(frame) < ospfOffset+24 {
		t.Fatalf("frame length: got %d, want at least %d", len(frame), ospfOffset+24)
	}

	packetLength := int(binary.BigEndian.Uint16(frame[ospfOffset+2 : ospfOffset+4]))
	if packetLength < 24 || len(frame) < ospfOffset+packetLength {
		t.Fatalf("OSPF packet length: got %d, frame has %d bytes", packetLength, len(frame)-ospfOffset)
	}
	packet := frame[ospfOffset : ospfOffset+packetLength]
	got := binary.BigEndian.Uint16(packet[12:14])
	if got == 0 {
		t.Fatal("OSPF checksum is zero")
	}

	var sum uint32
	for i := 0; i+1 < len(packet); i += 2 {
		if i >= 16 && i < 24 {
			continue
		}
		sum += uint32(packet[i])<<8 | uint32(packet[i+1])
	}
	if len(packet)%2 == 1 {
		sum += uint32(packet[len(packet)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	if uint16(sum) != 0xffff {
		t.Errorf("OSPF checksum residue: got %#04x, want 0xffff", uint16(sum))
	}
}

func TestOSPF_ChecksumValid(t *testing.T) {
	var emitted [][]byte
	synctest.Test(t, func(t *testing.T) {
		leg, _ := ospfExchange(t)
		emitted = leg.TX()
	})

	names := []string{"hello", "2-way-hello", "db-desc", "lsa-update", "flush", "goodbye"}
	fixtures := append(fixturePackets(t, "ospf.pcap"), fixturePackets(t, "ospf_restore.pcap")...)
	sources := []struct {
		name   string
		frames [][]byte
	}{
		{name: "emitted", frames: emitted},
		{name: "fixture", frames: fixtures},
	}

	for _, source := range sources {
		if len(source.frames) != len(names) {
			t.Fatalf("%s frame count: got %d, want %d", source.name, len(source.frames), len(names))
		}
		for i, name := range names {
			t.Run(source.name+"/"+name, func(t *testing.T) {
				assertOSPFChecksumValid(t, source.frames[i])
			})
		}
	}
}

func TestOSPF_LSAChecksumValid(t *testing.T) {
	var tx [][]byte
	synctest.Test(t, func(t *testing.T) {
		leg, _ := ospfExchange(t)
		tx = leg.TX()
	})
	if len(tx) < 5 {
		t.Fatalf("TX count: got %d, want at least 5", len(tx))
	}

	tests := []struct {
		name  string
		frame []byte
	}{
		{name: "lsa-update", frame: tx[3]},
		{name: "flush", frame: tx[4]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			frame := test.frame
			if len(frame) < 15 {
				t.Fatalf("frame length: got %d, want at least 15", len(frame))
			}

			ospfOffset := 14 + int(frame[14]&0x0f)*4
			lsaOffset := ospfOffset + 24 + 4
			if len(frame) < lsaOffset+20 {
				t.Fatalf("frame length: got %d, want at least %d", len(frame), lsaOffset+20)
			}

			lsaLength := int(binary.BigEndian.Uint16(frame[lsaOffset+18 : lsaOffset+20]))
			if lsaLength < 20 || len(frame) < lsaOffset+lsaLength {
				t.Fatalf("LSA length: got %d, frame has %d bytes", lsaLength, len(frame)-lsaOffset)
			}
			lsa := frame[lsaOffset : lsaOffset+lsaLength]
			got := binary.BigEndian.Uint16(lsa[16:18])
			if got == 0 {
				t.Fatal("LSA checksum is zero")
			}

			var c0, c1 int
			for _, octet := range lsa[2:] {
				c0 = (c0 + int(octet)) % 255
				c1 = (c1 + c0) % 255
			}
			if c0 != 0 || c1 != 0 {
				t.Errorf("LSA Fletcher residues: got (%d, %d), want (0, 0)", c0, c1)
			}
		})
	}
}

// TestReproducibility: two direct runs over the same fed frames produce
// identical TX sequences. The WPAD PAC port comes from the in-memory
// listener, so the TX frames are identical there too.
func TestReproducibility(t *testing.T) {
	for _, name := range []string{"ospf", "eigrp", "wpad"} {
		t.Run(name, func(t *testing.T) {
			var tx [2][][]byte
			for i := range tx {
				synctest.Test(t, func(t *testing.T) {
					leg := fixtureLeg(t)
					window := 3 * time.Second
					switch name {
					case "ospf":
						target := fixturePackets(t, "ospf_target.pcap")
						leg.PushRX(target[0])
						p := &pusher{leg: leg}
						p.at(time.Second, target[1])
						defer p.join()
						window = 20 * time.Second
					case "eigrp":
						target := fixturePackets(t, "eigrp_target.pcap")
						leg.PushRX(target[0])
						p := &pusher{leg: leg}
						p.at(time.Second, target[1])
						defer p.join()
					case "wpad":
						leg.PushRX(fixturePackets(t, "wpad_query.pcap")[0])
					}
					r := runDirect(t, leg, name, window)
					if r.err != nil {
						t.Fatalf("run %d: %v", i, r.err)
					}
					runSteps(t, r.teardown)
					tx[i] = leg.TX()
				})
			}
			if len(tx[0]) == 0 || len(tx[0]) != len(tx[1]) {
				t.Fatalf("TX count mismatch: run1=%d, run2=%d", len(tx[0]), len(tx[1]))
			}
			for i := range tx[0] {
				testtest.AssertBytesEqual(t, name+" frame "+strconv.Itoa(i), tx[0][i], tx[1][i])
			}
		})
	}
}

// eigrpExchange runs the EIGRP behavior against the target's hello and its
// acknowledgment one second after the null update, runs the armed step,
// and returns the leg with all four frames recorded.
func eigrpExchange(t *testing.T) (*testtest.Leg, directRun) {
	t.Helper()
	target := fixturePackets(t, "eigrp_target.pcap")
	leg := fixtureLeg(t)
	leg.PushRX(target[0])
	p := &pusher{leg: leg}
	p.at(time.Second, target[1])
	r := runDirect(t, leg, "eigrp", 3*time.Second)
	p.join()
	if r.err != nil {
		t.Fatalf("RunEIGRP() error = %v, want nil", r.err)
	}
	runSteps(t, r.teardown)
	return leg, r
}

// TestEIGRP_FixturePins verifies the EIGRP attack TX matches the fixture
// byte-for-byte (hello, null update, route-inject), and the teardown TX
// matches the restore fixture (goodbye with the Peer Termination TLV).
func TestEIGRP_FixturePins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg, r := eigrpExchange(t)
		wantVerdict(t, r, findings.VerdictConfirmed, "the target acknowledged the adjacency in AS 100")

		tx := leg.TX()
		if len(tx) != 4 {
			t.Fatalf("TX count: got %d, want 4 (hello + null update + inject + goodbye)", len(tx))
		}

		fixtures := fixturePackets(t, "eigrp.pcap")
		testtest.AssertBytesEqual(t, "EIGRP hello", tx[0], fixtures[0])
		testtest.AssertBytesEqual(t, "EIGRP null update", tx[1], fixtures[1])
		testtest.AssertBytesEqual(t, "EIGRP route-inject", tx[2], fixtures[2])

		restoreFixtures := fixturePackets(t, "eigrp_restore.pcap")
		testtest.AssertBytesEqual(t, "EIGRP goodbye", tx[3], restoreFixtures[0])

		if len(r.emitter.findings) != 1 {
			t.Errorf("findings = %d, want 1", len(r.emitter.findings))
		}
	})
}

// TestEIGRP_HelloCarriesParameters decodes the sent hello with the owned
// layer and reads the Parameters TLV: type 1, length 12, the K-values at
// their RFC 7868 section 6.7.1 defaults.
func TestEIGRP_HelloCarriesParameters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg, _ := eigrpExchange(t)
		hello := leg.TX()[0]
		pkt := gopacket.NewPacket(hello, layers.LayerTypeEthernet, gopacket.Default)
		eigrp, ok := pkt.Layer(nl.LayerTypeEIGRP).(*nl.EIGRP)
		if !ok {
			t.Fatalf("hello decodes to %v, want an EIGRP layer", pkt.Layers())
		}
		if eigrp.Flags != 0 {
			t.Errorf("hello flags = %#x, want 0", eigrp.Flags)
		}
		if len(eigrp.TLVs) != 1 || eigrp.TLVs[0].Type != 0x0001 || eigrp.TLVs[0].Length != 12 {
			t.Fatalf("hello TLVs = %+v, want one Parameters TLV of length 12", eigrp.TLVs)
		}
		if got := eigrp.TLVs[0].Value; string(got[:6]) != string([]byte{1, 0, 1, 0, 0, 0}) {
			t.Errorf("K-values = %v, want [1 0 1 0 0 0]", got[:6])
		}
	})
}

// TestEIGRP_AuthMismatchRefusal: the target rejects the adjacency with a
// goodbye, in the Peer Termination TLV form and in the older K-values-of-
// 255 form. The behavior reports a refused adjacency finding, a resisted
// verdict, and tears down cleanly with no inject frame on TX.
func TestEIGRP_AuthMismatchRefusal(t *testing.T) {
	rejects := fixturePackets(t, "eigrp_reject.pcap")
	for i, name := range []string{"peer termination TLV", "K-values of 255"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				leg.PushRX(rejects[i])
				r := runDirect(t, leg, "eigrp", 3*time.Second)
				wantVerdict(t, r, findings.VerdictResisted, "adjacency refused")
				runSteps(t, r.teardown)
				if tx := leg.TX(); len(tx) != 2 {
					t.Fatalf("TX count: got %d, want 2 (hello + goodbye, no inject)", len(tx))
				}
				if len(r.emitter.findings) != 1 {
					t.Fatalf("findings = %d, want 1", len(r.emitter.findings))
				}
				var detail struct {
					Refused bool `json:"refused"`
				}
				if err := json.Unmarshal(r.emitter.findings[0], &detail); err != nil {
					t.Fatalf("unmarshal finding: %v", err)
				}
				if !detail.Refused {
					t.Fatal("finding should report refused=true for the goodbye")
				}
			})
		})
	}
}

// TestWPAD_FixturePins feeds the client's NBT-NS query and its LLMNR query
// in turn and pins each answer against the fixture: unicast to the client
// with the query's id. The PAC port comes from the in-memory listener and
// the finding carries no credential fields.
func TestWPAD_FixturePins(t *testing.T) {
	queries := fixturePackets(t, "wpad_query.pcap")
	fixtures := fixturePackets(t, "wpad.pcap")
	for i, name := range []string{"nbt-ns", "llmnr"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				leg.PushRX(queries[i])
				r := runDirect(t, leg, "wpad", time.Second)
				wantVerdict(t, r, findings.VerdictResisted, "no PAC fetch in 1s")

				tx := leg.TX()
				if len(tx) != 1 {
					t.Fatalf("TX count: got %d, want 1 (the answer)", len(tx))
				}
				testtest.AssertBytesEqual(t, "WPAD "+name+" answer", tx[0], fixtures[i])

				if len(r.emitter.findings) != 1 {
					t.Fatalf("findings = %d, want 1", len(r.emitter.findings))
				}
				var detail map[string]any
				if err := json.Unmarshal(r.emitter.findings[0], &detail); err != nil {
					t.Fatalf("unmarshal finding: %v", err)
				}
				if detail["port"] != float64(8080) {
					t.Errorf("port = %v, want 8080 from the in-memory listener", detail["port"])
				}
				for _, field := range []string{"protocol", "length"} {
					if _, ok := detail[field]; ok {
						t.Errorf("finding carries %q; the behavior captures no credential", field)
					}
				}
				if strings.Contains(string(r.emitter.findings[0]), "NTLMSSP") {
					t.Errorf("finding carries credential material: %s", r.emitter.findings[0])
				}
			})
		})
	}
}

// TestWPAD_ListenFailureIsCoded pins that a listener the seam cannot open
// surfaces as the port-collision code and sends nothing.
func TestWPAD_ListenFailureIsCoded(t *testing.T) {
	leg := fixtureLeg(t)
	var behaviorErr error
	bs := routing.Behaviors()
	for i := range bs {
		if bs[i].Name == "wpad" {
			run := bs[i].Run
			bs[i].Run = func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
				deps.Listen = func(context.Context, string, string) (net.Listener, error) {
					return nil, errors.New("address in use")
				}
				var v check.Verdict
				v, behaviorErr = run(ctx, deps)
				return v, behaviorErr
			}
		}
	}
	recs, err := runOne(t, runner.Options{
		Catalog:   catalog.MustNew(bs...),
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "wpad"}},
	})
	if err != nil {
		t.Fatalf("run wpad: %v", err)
	}
	if leg.SendCount() != 0 {
		t.Errorf("sends = %d, want 0 without a listener", leg.SendCount())
	}
	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictPending || !strings.Contains(v.Detail, "address in use") {
		t.Errorf("verdict = %+v, want pending carrying the bind error", v)
	}
	if code, ok := errs.CodeOf(behaviorErr); !ok || code != catalog.ErrCodeWPADPortCollision {
		t.Errorf("behavior error = %v (code %q), want %q", behaviorErr, code, catalog.ErrCodeWPADPortCollision)
	}
}

// TestWPAD_BindsTheLegAddress pins the address the listener is asked for:
// the attack leg's IPv4 on port 80.
func TestWPAD_BindsTheLegAddress(t *testing.T) {
	leg := fixtureLeg(t)
	var asked string
	bs := routing.Behaviors()
	for i := range bs {
		if bs[i].Name == "wpad" {
			run := bs[i].Run
			bs[i].Run = func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
				deps.Listen = func(ctx context.Context, network, address string) (net.Listener, error) {
					asked = network + " " + address
					return memListen(ctx, network, address)
				}
				return run(ctx, deps)
			}
		}
	}
	if _, err := runOne(t, runner.Options{
		Catalog:   catalog.MustNew(bs...),
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "wpad"}},
	}); err != nil {
		t.Fatalf("run wpad: %v", err)
	}
	if asked != "tcp 10.0.0.99:80" {
		t.Errorf("listen address = %q, want %q", asked, "tcp 10.0.0.99:80")
	}
}

// TestCatalogCostFields pins the routing entries' cost fields: both
// injections restore actively and WPAD decays with its 30 s PAC TTL.
func TestCatalogCostFields(t *testing.T) {
	for _, name := range []string{"ospf", "eigrp"} {
		e := mustEntry(t, name)
		if e.Impact != check.ImpactTemporary || e.Restore.Kind != check.RestoreActive || e.Affects != check.AffectsFabric {
			t.Errorf("%s = impact %s restore %s affects %s, want temporary, active, fabric", name, e.Impact, e.Restore.Kind, e.Affects)
		}
	}
	e := mustEntry(t, "wpad")
	if e.Impact != check.ImpactTemporary || e.Restore.Kind != check.RestoreDecay || e.Restore.Bound != 30*time.Second {
		t.Errorf("wpad = impact %s restore %s bound %v, want temporary, decay, 30s", e.Impact, e.Restore.Kind, e.Restore.Bound)
	}
}

// TestOSPF_AllRawCraft verifies that all OSPF PDU types use raw craft (not
// the fork's SerializeTo, which doesn't exist for OSPFv2), by checking the
// OSPF version at the header offset of each emitted frame.
func TestOSPF_AllRawCraft(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg, _ := ospfExchange(t)
		tx := leg.TX()
		if len(tx) < 4 {
			t.Fatalf("TX count: got %d, want >= 4", len(tx))
		}
		for i, frame := range tx[:4] {
			if len(frame) < 35 {
				t.Fatalf("frame %d too short for OSPF header", i)
			}
			if frame[34] != 2 {
				t.Errorf("frame %d: OSPF version: got %d, want 2", i, frame[34])
			}
		}
	})
}

// TestTeardownStepCounts lists each actively restored behavior's armed
// steps by name and asserts that every step sends one frame when run, and
// that WPAD arms none.
func TestTeardownStepCounts(t *testing.T) {
	cases := []struct {
		name  string
		steps []string
	}{
		{"ospf", []string{"ospf-lsa-flush", "ospf-goodbye"}},
		{"eigrp", []string{"eigrp-goodbye"}},
		{"wpad", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				r := runDirect(t, leg, tc.name, time.Second)
				if r.err != nil {
					t.Fatalf("run %s: %v", tc.name, r.err)
				}
				if got := r.teardown.names(); strings.Join(got, ",") != strings.Join(tc.steps, ",") {
					t.Fatalf("armed steps = %v, want %v", got, tc.steps)
				}
				for _, s := range r.teardown.steps {
					before := leg.SendCount()
					if err := s.step(context.Background()); err != nil {
						t.Fatalf("step %s: %v", s.name, err)
					}
					if got := leg.SendCount() - before; got != 1 {
						t.Errorf("step %s sent %d frames, want 1", s.name, got)
					}
				}
			})
		})
	}
}
