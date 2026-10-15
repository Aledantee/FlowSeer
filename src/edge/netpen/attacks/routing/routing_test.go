// routing_test.go tests the routing-injection and rogue-WPAD attack
// behaviors against fixture-mirrored TX sequences: byte-for-byte where
// deterministic, field-set where randomized. Tests use the in-memory
// [testtest.Leg] harness (pcap-fed RX + recorded TX) and exercise each
// behavior through the runner, which creates the Stream, Teardown, and
// Deps. The target and the leg's addresses are the fixture's values, so
// the frames the harvest generator pinned still come out byte for byte.

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

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
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

// runSingle runs one attack on a single leg with a one-second teardown
// budget so teardown tests complete in milliseconds.
func runSingle(t *testing.T, leg *testtest.Leg, name string) ([]findings.Record, error) {
	t.Helper()
	return runOne(t, runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: name}},
		Target:         fixtureTarget(),
		TeardownBudget: time.Second,
	})
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

// findFindingByModule returns the first KindFinding record for the given
// module, or nil.
func findFindingByModule(t *testing.T, recs []findings.Record, module string) *findings.Finding {
	t.Helper()
	for i := range recs {
		if recs[i].Kind == findings.KindFinding && recs[i].Finding != nil && recs[i].Finding.Module == module {
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

// TestOSPF_FixturePins verifies the OSPF attack TX matches the fixture
// byte-for-byte (hello, db-desc, lsa-update), and the teardown TX matches
// the restore fixture (flush, goodbye). These fixtures carry protocol-valid
// checksums instead of the zero checksums in the Python characterization
// capture, which IOS-XE rejects.
func TestOSPF_FixturePins(t *testing.T) {
	leg := fixtureLeg(t)

	targetPackets := fixturePackets(t, "ospf_target.pcap")
	for _, p := range targetPackets {
		leg.PushRX(p)
	}

	recs, err := runSingle(t, leg, "ospf")
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5 (hello + db-desc + lsa-update + flush + goodbye)", len(tx))
	}

	fixtures := fixturePackets(t, "ospf.pcap")
	testtest.AssertBytesEqual(t, "OSPF hello", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "OSPF db-desc", tx[1], fixtures[1])
	testtest.AssertBytesEqual(t, "OSPF lsa-update", tx[2], fixtures[2])

	restoreFixtures := fixturePackets(t, "ospf_restore.pcap")
	testtest.AssertBytesEqual(t, "OSPF flush", tx[3], restoreFixtures[0])
	testtest.AssertBytesEqual(t, "OSPF goodbye", tx[4], restoreFixtures[1])

	if f := findFinding(t, recs); f == nil {
		t.Fatal("no finding emitted")
	}
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

// armRecorder is the teardown handle a direct call to a behavior arms its
// steps against, keeping their names in arm order.
type armRecorder struct {
	names []string
}

func (a *armRecorder) Arm(name string, _ check.Step) { a.names = append(a.names, name) }

// nopEmitter drops the records a direct call to a behavior emits.
type nopEmitter struct{}

func (nopEmitter) Emit(findings.Record) bool            { return true }
func (nopEmitter) Finding(string, json.RawMessage) bool { return true }
func (nopEmitter) Progress(_, _ string) bool            { return true }

// directDeps builds the dependencies for a direct call to a behavior with
// the given window and a recording teardown handle.
func directDeps(leg *testtest.Leg, window time.Duration) (check.Deps, *armRecorder) {
	td := &armRecorder{}
	return check.Deps{
		AttackLeg: leg,
		Target:    fixtureTarget(),
		Observe:   window,
		Emitter:   nopEmitter{},
		Teardown:  td,
		Listen:    memListen,
	}, td
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
// skipped, the hello that arrives one second later is read, and the
// sequence proceeds then rather than at the window.
func TestOSPF_HelloBehindAnARPFrameIsRead(t *testing.T) {
	hello := fixturePackets(t, "ospf_target.pcap")[0]
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		leg.PushRX(arpFrame())
		go func() {
			time.Sleep(time.Second)
			leg.PushRX(hello)
		}()
		deps, td := directDeps(leg, 3*time.Second)
		start := time.Now()
		v, err := routing.RunOSPF(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunOSPF() error = %v, want nil", err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("returned after %v, want 1s, when the hello arrived", elapsed)
		}
		if v.Kind != findings.VerdictPending {
			t.Errorf("verdict = %+v, want pending", v)
		}
		if n := leg.SendCount(); n != 3 {
			t.Errorf("sends = %d, want 3 (hello, db-desc, lsa-update)", n)
		}
		if len(td.names) != 2 || td.names[0] != "ospf-lsa-flush" || td.names[1] != "ospf-goodbye" {
			t.Errorf("armed steps = %v, want [ospf-lsa-flush ospf-goodbye]", td.names)
		}
	})
}

// TestOSPF_NoHelloProceedsAtTheWindow pins that a quiet leg runs the window
// out and the sequence still proceeds.
func TestOSPF_NoHelloProceedsAtTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		deps, _ := directDeps(leg, 3*time.Second)
		start := time.Now()
		if _, err := routing.RunOSPF(context.Background(), deps); err != nil {
			t.Fatalf("RunOSPF() error = %v, want nil", err)
		}
		if elapsed := time.Since(start); elapsed != 3*time.Second {
			t.Errorf("returned after %v, want the 3s window", elapsed)
		}
		if n := leg.SendCount(); n != 3 {
			t.Errorf("sends = %d, want 3 (hello, db-desc, lsa-update)", n)
		}
	})
}

// TestEIGRP_GoodbyeBehindAnotherFrameIsRead pins that a non-matching first
// frame does not end the observation: the ARP frame is skipped, the
// goodbye that arrives one second later is read, the verdict is resisted
// at that moment with no inject sent, and the goodbye teardown was armed
// under its name.
func TestEIGRP_GoodbyeBehindAnotherFrameIsRead(t *testing.T) {
	goodbye := fixturePackets(t, "eigrp_reject.pcap")[0]
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		leg.PushRX(arpFrame())
		go func() {
			time.Sleep(time.Second)
			leg.PushRX(goodbye)
		}()
		deps, td := directDeps(leg, 3*time.Second)
		v, err := routing.RunEIGRP(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunEIGRP() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictResisted || v.Observed != time.Second {
			t.Errorf("verdict = %+v, want resisted at 1s, when the goodbye arrived", v)
		}
		if n := leg.SendCount(); n != 1 {
			t.Errorf("sends = %d, want 1 (the hello only)", n)
		}
		if len(td.names) != 1 || td.names[0] != "eigrp-goodbye" {
			t.Errorf("armed steps = %v, want [eigrp-goodbye]", td.names)
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
	leg := fixtureLeg(t)

	_, err := runSingle(t, leg, "ospf")
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	names := []string{"hello", "db-desc", "lsa-update", "flush", "goodbye"}
	fixtures := append(fixturePackets(t, "ospf.pcap"), fixturePackets(t, "ospf_restore.pcap")...)
	sources := []struct {
		name   string
		frames [][]byte
	}{
		{name: "emitted", frames: leg.TX()},
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
	leg := fixtureLeg(t)

	_, err := runSingle(t, leg, "ospf")
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	if len(tx) < 4 {
		t.Fatalf("TX count: got %d, want at least 4", len(tx))
	}

	tests := []struct {
		name  string
		frame []byte
	}{
		{name: "lsa-update", frame: tx[2]},
		{name: "flush", frame: tx[3]},
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

// TestOSPF_TeardownOrder: the teardown steps execute in arm order:
// flush first (armed first), then goodbye.
func TestOSPF_TeardownOrder(t *testing.T) {
	leg := fixtureLeg(t)

	_, err := runSingle(t, leg, "ospf")
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	if len(tx) < 5 {
		t.Fatalf("TX count: got %d, want >= 5", len(tx))
	}

	restoreFixtures := fixturePackets(t, "ospf_restore.pcap")
	testtest.AssertBytesEqual(t, "OSPF flush (teardown step 1)", tx[3], restoreFixtures[0])
	testtest.AssertBytesEqual(t, "OSPF goodbye (teardown step 2)", tx[4], restoreFixtures[1])
}

// TestReproducibility: two runs over the same fixture produce the same
// findings class and identical TX sequences. The WPAD PAC port comes from
// the in-memory listener, so the TX frames are identical there too.
func TestReproducibility(t *testing.T) {
	for _, name := range []string{"ospf", "eigrp", "wpad"} {
		t.Run(name, func(t *testing.T) {
			leg1 := fixtureLeg(t)
			recs1, err1 := runSingle(t, leg1, name)
			if err1 != nil {
				t.Fatalf("run 1: %v", err1)
			}
			tx1 := leg1.TX()

			leg2 := fixtureLeg(t)
			recs2, err2 := runSingle(t, leg2, name)
			if err2 != nil {
				t.Fatalf("run 2: %v", err2)
			}
			tx2 := leg2.TX()

			f1 := findFinding(t, recs1)
			f2 := findFinding(t, recs2)
			if f1 == nil || f2 == nil {
				t.Fatal("both runs should emit a finding")
			}
			if f1.Module != f2.Module {
				t.Fatalf("finding module mismatch: %s vs %s", f1.Module, f2.Module)
			}
			if len(tx1) != len(tx2) {
				t.Fatalf("TX count mismatch: run1=%d, run2=%d", len(tx1), len(tx2))
			}
			for i := range tx1 {
				testtest.AssertBytesEqual(t, name+" frame "+strconv.Itoa(i), tx1[i], tx2[i])
			}
		})
	}
}

// TestEIGRP_FixturePins verifies the EIGRP attack TX matches the fixture
// byte-for-byte (hello, route-inject), and the teardown TX matches the
// restore fixture (goodbye hello).
func TestEIGRP_FixturePins(t *testing.T) {
	leg := fixtureLeg(t)

	targetPackets := fixturePackets(t, "eigrp_target.pcap")
	for _, p := range targetPackets {
		leg.PushRX(p)
	}

	recs, err := runSingle(t, leg, "eigrp")
	if err != nil {
		t.Fatalf("run eigrp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3 (hello + inject + goodbye)", len(tx))
	}

	fixtures := fixturePackets(t, "eigrp.pcap")
	testtest.AssertBytesEqual(t, "EIGRP hello", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "EIGRP route-inject", tx[1], fixtures[1])

	restoreFixtures := fixturePackets(t, "eigrp_restore.pcap")
	testtest.AssertBytesEqual(t, "EIGRP goodbye", tx[2], restoreFixtures[0])

	if f := findFinding(t, recs); f == nil {
		t.Fatal("no finding emitted")
	}
}

// TestEIGRP_AuthMismatchRefusal: the target rejects adjacency mid-formation
// (auth mismatch, fed via PushRX as a goodbye frame). The behavior reports
// a refused adjacency finding, a resisted verdict, and tears down cleanly
// with no route inject frame on TX.
func TestEIGRP_AuthMismatchRefusal(t *testing.T) {
	leg := fixtureLeg(t)

	rejectPackets := fixturePackets(t, "eigrp_reject.pcap")
	for _, p := range rejectPackets {
		leg.PushRX(p)
	}

	recs, err := runSingle(t, leg, "eigrp")
	if err != nil {
		t.Fatalf("run eigrp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (hello + goodbye, no inject)", len(tx))
	}

	f := findFindingByModule(t, recs, "eigrp")
	if f == nil {
		t.Fatal("no eigrp finding emitted")
	}
	var detail struct {
		Refused bool `json:"refused"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if !detail.Refused {
		t.Fatal("finding should report refused=true for auth-mismatch")
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictResisted {
		t.Errorf("verdict = %+v, want resisted on a refused adjacency", v)
	}
}

// TestWPAD_FixturePins verifies the WPAD attack TX matches the fixture
// byte-for-byte (NBT-NS response, LLMNR response), that the PAC port came
// from the in-memory listener, and that the finding carries no credential
// fields.
func TestWPAD_FixturePins(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runSingle(t, leg, "wpad")
	if err != nil {
		t.Fatalf("run wpad: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (nbt-ns + llmnr)", len(tx))
	}

	fixtures := fixturePackets(t, "wpad.pcap")
	testtest.AssertBytesEqual(t, "WPAD nbt-ns", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "WPAD llmnr", tx[1], fixtures[1])

	f := findFindingByModule(t, recs, "wpad")
	if f == nil {
		t.Fatal("no wpad finding emitted")
	}
	var detail map[string]any
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
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
	if strings.Contains(string(f.Detail), "NTLMSSP") {
		t.Errorf("finding carries credential material: %s", f.Detail)
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
	leg := fixtureLeg(t)

	_, err := runSingle(t, leg, "ospf")
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	if len(tx) < 3 {
		t.Fatalf("TX count: got %d, want >= 3", len(tx))
	}
	for i, frame := range tx[:3] {
		if len(frame) < 35 {
			t.Fatalf("frame %d too short for OSPF header", i)
		}
		if frame[34] != 2 {
			t.Errorf("frame %d: OSPF version: got %d, want 2", i, frame[34])
		}
	}
}

// TestTeardownStepCounts verifies the OSPF behavior arms exactly 2
// teardown steps (flush + goodbye), EIGRP exactly 1 (goodbye), and WPAD
// none, read off the TX counts.
func TestTeardownStepCounts(t *testing.T) {
	for _, c := range []struct {
		name string
		want int
	}{{"ospf", 5}, {"eigrp", 3}, {"wpad", 2}} {
		t.Run(c.name, func(t *testing.T) {
			leg := fixtureLeg(t)
			if _, err := runSingle(t, leg, c.name); err != nil {
				t.Fatalf("run %s: %v", c.name, err)
			}
			if got := len(leg.TX()); got != c.want {
				t.Fatalf("TX count: got %d, want %d", got, c.want)
			}
		})
	}
}
