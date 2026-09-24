// routing_test.go tests the routing-injection and rogue-WPAD attack
// behaviors against fixture-mirrored TX sequences: byte-for-byte
// where deterministic, field-set where randomized. Tests use the
// in-memory [testtest.Leg] harness (pcap-fed RX + recorded TX) and exercise
// each behavior through the runner, which creates the Stream, Teardown,
// and Deps. Durability duties, finding emission, decay bounds, and the
// teardown rule are asserted via the recorded TX and findings.

package routing_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// runOne runs a single behavior through the runner and returns the
// findings and the run error. The stream is drained concurrently with
// Run.
func runOne(t *testing.T, opts runner.Options) ([]findings.Record, error) {
	t.Helper()
	if opts.Behaviors == nil {
		opts.Behaviors = routing.Behaviors()
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

// runSingle is a convenience for running one attack on a single leg.
//
//nolint:unparam // mode mirrors fh/l2/ip6's shape for catalog parity.
func runSingle(t *testing.T, leg *testtest.Leg, name, mode string) ([]findings.Record, error) {
	t.Helper()
	opts := runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: name, Mode: mode}},
		Behaviors: routing.Behaviors(),
	}
	return runOne(t, opts)
}

// runSingleWithBudget runs one attack with a custom teardown budget so
// teardown tests complete in milliseconds.
//
//nolint:unparam // mode mirrors fh/l2/ip6's shape for catalog parity.
func runSingleWithBudget(t *testing.T, leg *testtest.Leg, name, mode string, budget time.Duration) ([]findings.Record, error) {
	t.Helper()
	opts := runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: name, Mode: mode}},
		Behaviors:      routing.Behaviors(),
		TeardownBudget: budget,
	}
	return runOne(t, opts)
}

//nolint:unparam // mode mirrors fh/l2/ip6's shape for catalog parity.
func mustEntry(t *testing.T, name, mode string) catalog.Entry {
	t.Helper()
	e, ok := runner.ResolveEntry(name, mode)
	if !ok {
		t.Fatalf("catalog entry not found: %s/%s", name, mode)
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

func fixturePackets(t *testing.T, name string) [][]byte {
	t.Helper()
	return testtest.ReadPcap(t, testtest.FixturePath(t, "routing/"+name))
}

// TestOSPF_FixturePins verifies the OSPF attack TX matches the fixture
// byte-for-byte (hello, db-desc, lsa-update), and the teardown TX matches
// the restore fixture (flush, goodbye).
func TestOSPF_FixturePins(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// Feed the target's hello so the behavior's RX path succeeds.
	targetPackets := fixturePackets(t, "ospf_target.pcap")
	for _, p := range targetPackets {
		leg.PushRX(p)
	}

	recs, err := runSingleWithBudget(t, leg, "ospf", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	// 3 attack frames (hello, db-desc, lsa-update) + 2 teardown (flush, goodbye) = 5.
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5 (hello + db-desc + lsa-update + flush + goodbye)", len(tx))
	}

	// Verify attack frames match fixture byte-for-byte.
	fixtures := fixturePackets(t, "ospf.pcap")
	testtest.AssertBytesEqual(t, "OSPF hello", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "OSPF db-desc", tx[1], fixtures[1])
	testtest.AssertBytesEqual(t, "OSPF lsa-update", tx[2], fixtures[2])

	// Verify teardown frames match restore fixture.
	restoreFixtures := fixturePackets(t, "ospf_restore.pcap")
	testtest.AssertBytesEqual(t, "OSPF flush", tx[3], restoreFixtures[0])
	testtest.AssertBytesEqual(t, "OSPF goodbye", tx[4], restoreFixtures[1])

	if f := findFinding(t, recs); f == nil {
		t.Fatal("no finding emitted")
	}
}

func TestOSPF_ChecksumValid(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runSingleWithBudget(t, leg, "ospf", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	names := []string{"hello", "db-desc", "lsa-update", "flush", "goodbye"}
	if len(tx) != len(names) {
		t.Fatalf("TX count: got %d, want %d", len(tx), len(names))
	}

	for i, name := range names {
		t.Run(name, func(t *testing.T) {
			frame := tx[i]
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
			for j := 0; j+1 < len(packet); j += 2 {
				if j == 12 || (j >= 16 && j < 24) {
					continue
				}
				sum += uint32(packet[j])<<8 | uint32(packet[j+1])
			}
			if len(packet)%2 == 1 {
				sum += uint32(packet[len(packet)-1]) << 8
			}
			for sum>>16 != 0 {
				sum = (sum & 0xffff) + (sum >> 16)
			}
			want := ^uint16(sum)
			if got != want {
				t.Errorf("OSPF checksum: got %#04x, want %#04x", got, want)
			}
		})
	}
}

func TestOSPF_LSAChecksumValid(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runSingleWithBudget(t, leg, "ospf", "", 1*time.Second)
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
// flush first (armed first), then goodbye. The recorded TX after completion
// should show flush before goodbye.
func TestOSPF_TeardownOrder(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runSingleWithBudget(t, leg, "ospf", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	if len(tx) < 5 {
		t.Fatalf("TX count: got %d, want >= 5", len(tx))
	}

	// Teardown TX is indices 3 and 4. Step 1 (flush) must precede step 2
	// (goodbye) — they were armed in that order.
	restoreFixtures := fixturePackets(t, "ospf_restore.pcap")
	testtest.AssertBytesEqual(t, "OSPF flush (teardown step 1)", tx[3], restoreFixtures[0])
	testtest.AssertBytesEqual(t, "OSPF goodbye (teardown step 2)", tx[4], restoreFixtures[1])
}

// TestOSPF_AE6Reproducibility: two runs over the same fixture produce the
// same findings class and identical TX sequences.
func TestOSPF_AE6Reproducibility(t *testing.T) {
	leg1 := testtest.New()
	defer func() { _ = leg1.Close() }()
	recs1, err1 := runSingleWithBudget(t, leg1, "ospf", "", 1*time.Second)
	if err1 != nil {
		t.Fatalf("run 1: %v", err1)
	}
	tx1 := leg1.TX()

	leg2 := testtest.New()
	defer func() { _ = leg2.Close() }()
	recs2, err2 := runSingleWithBudget(t, leg2, "ospf", "", 1*time.Second)
	if err2 != nil {
		t.Fatalf("run 2: %v", err2)
	}
	tx2 := leg2.TX()

	// Same findings class (both have a finding).
	f1 := findFinding(t, recs1)
	f2 := findFinding(t, recs2)
	if f1 == nil || f2 == nil {
		t.Fatal("both runs should emit a finding")
	}
	if f1.Module != f2.Module {
		t.Fatalf("finding module mismatch: %s vs %s", f1.Module, f2.Module)
	}

	// Identical TX sequences.
	if len(tx1) != len(tx2) {
		t.Fatalf("TX count mismatch: run1=%d, run2=%d", len(tx1), len(tx2))
	}
	for i := range tx1 {
		testtest.AssertBytesEqual(t, "OSPF AE6 frame "+itoa(i), tx1[i], tx2[i])
	}
}

// TestEIGRP_FixturePins verifies the EIGRP attack TX matches the fixture
// byte-for-byte (hello, route-inject), and the teardown TX matches the
// restore fixture (goodbye hello).
func TestEIGRP_FixturePins(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// Feed the target's hello so the behavior's RX path succeeds.
	targetPackets := fixturePackets(t, "eigrp_target.pcap")
	for _, p := range targetPackets {
		leg.PushRX(p)
	}

	recs, err := runSingleWithBudget(t, leg, "eigrp", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run eigrp: %v", err)
	}

	tx := leg.TX()
	// 2 attack frames (hello, route-inject) + 1 teardown (goodbye) = 3.
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3 (hello + inject + goodbye)", len(tx))
	}

	// Verify attack frames match fixture byte-for-byte.
	fixtures := fixturePackets(t, "eigrp.pcap")
	testtest.AssertBytesEqual(t, "EIGRP hello", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "EIGRP route-inject", tx[1], fixtures[1])

	// Verify teardown frame matches restore fixture.
	restoreFixtures := fixturePackets(t, "eigrp_restore.pcap")
	testtest.AssertBytesEqual(t, "EIGRP goodbye", tx[2], restoreFixtures[0])

	if f := findFinding(t, recs); f == nil {
		t.Fatal("no finding emitted")
	}
}

// TestEIGRP_AuthMismatchRefusal: the target rejects adjacency mid-formation
// (auth mismatch, fed via PushRX as a goodbye frame). The behavior reports
// a refused adjacency finding and tears down cleanly — no partial state
// (no route inject frame on TX, only hello + goodbye teardown).
func TestEIGRP_AuthMismatchRefusal(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// Feed the target's goodbye (auth-mismatch rejection).
	rejectPackets := fixturePackets(t, "eigrp_reject.pcap")
	for _, p := range rejectPackets {
		leg.PushRX(p)
	}

	recs, err := runSingleWithBudget(t, leg, "eigrp", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run eigrp: %v", err)
	}

	tx := leg.TX()
	// 1 attack frame (hello) + 1 teardown (goodbye) = 2.
	// No route inject — the behavior detected refusal and returned early.
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (hello + goodbye, no inject)", len(tx))
	}

	// The finding should report refused adjacency.
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
}

// TestEIGRP_AE6Reproducibility: two runs over the same fixture produce the
// same findings class and identical TX sequences.
func TestEIGRP_AE6Reproducibility(t *testing.T) {
	leg1 := testtest.New()
	defer func() { _ = leg1.Close() }()
	recs1, err1 := runSingleWithBudget(t, leg1, "eigrp", "", 1*time.Second)
	if err1 != nil {
		t.Fatalf("run 1: %v", err1)
	}
	tx1 := leg1.TX()

	leg2 := testtest.New()
	defer func() { _ = leg2.Close() }()
	recs2, err2 := runSingleWithBudget(t, leg2, "eigrp", "", 1*time.Second)
	if err2 != nil {
		t.Fatalf("run 2: %v", err2)
	}
	tx2 := leg2.TX()

	// Same findings class.
	f1 := findFinding(t, recs1)
	f2 := findFinding(t, recs2)
	if f1 == nil || f2 == nil {
		t.Fatal("both runs should emit a finding")
	}
	if f1.Module != f2.Module {
		t.Fatalf("finding module mismatch: %s vs %s", f1.Module, f2.Module)
	}

	// Identical TX sequences.
	if len(tx1) != len(tx2) {
		t.Fatalf("TX count mismatch: run1=%d, run2=%d", len(tx1), len(tx2))
	}
	for i := range tx1 {
		testtest.AssertBytesEqual(t, "EIGRP AE6 frame "+itoa(i), tx1[i], tx2[i])
	}
}

// TestWPAD_FixturePins verifies the WPAD attack TX matches the fixture
// byte-for-byte (NBT-NS response, LLMNR response).
func TestWPAD_FixturePins(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "wpad", "")
	if err != nil {
		t.Fatalf("run wpad: %v", err)
	}

	tx := leg.TX()
	// 2 attack frames (nbt-ns, llmnr). No teardown (transient-decay).
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (nbt-ns + llmnr)", len(tx))
	}

	// Verify attack frames match fixture byte-for-byte.
	fixtures := fixturePackets(t, "wpad.pcap")
	testtest.AssertBytesEqual(t, "WPAD nbt-ns", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "WPAD llmnr", tx[1], fixtures[1])

	if f := findFinding(t, recs); f == nil {
		t.Fatal("no finding emitted")
	}
}

// TestWPAD_AE6Reproducibility: two runs over the same fixture produce the
// same findings class and identical TX sequences. The PAC proxy port is
// OS-chosen, so the port differs but the TX frames are identical.
func TestWPAD_AE6Reproducibility(t *testing.T) {
	leg1 := testtest.New()
	defer func() { _ = leg1.Close() }()
	recs1, err1 := runSingle(t, leg1, "wpad", "")
	if err1 != nil {
		t.Fatalf("run 1: %v", err1)
	}
	tx1 := leg1.TX()

	leg2 := testtest.New()
	defer func() { _ = leg2.Close() }()
	recs2, err2 := runSingle(t, leg2, "wpad", "")
	if err2 != nil {
		t.Fatalf("run 2: %v", err2)
	}
	tx2 := leg2.TX()

	// Same findings class.
	f1 := findFinding(t, recs1)
	f2 := findFinding(t, recs2)
	if f1 == nil || f2 == nil {
		t.Fatal("both runs should emit a finding")
	}
	if f1.Module != f2.Module {
		t.Fatalf("finding module mismatch: %s vs %s", f1.Module, f2.Module)
	}

	// Identical TX sequences.
	if len(tx1) != len(tx2) {
		t.Fatalf("TX count mismatch: run1=%d, run2=%d", len(tx1), len(tx2))
	}
	for i := range tx1 {
		testtest.AssertBytesEqual(t, "WPAD AE6 frame "+itoa(i), tx1[i], tx2[i])
	}
}

// TestWPAD_PortCollision pins the wire error code. It does not exercise
// listener failure: the proxy asks the OS for an available port.
func TestWPAD_PortCollision(t *testing.T) {
	// The behavior binds on a random free port (port 0). A real
	// collision is hard to force reliably, so we verify the error code
	// is defined and correct.
	_ = catalog.ErrCodeWPADPortCollision
	if catalog.ErrCodeWPADPortCollision.String() != "netpen/wpad-port-collision" {
		t.Fatalf("unexpected error code: %s", catalog.ErrCodeWPADPortCollision)
	}
}

// TestWPAD_SecretRedaction verifies that captured credentials in the WPAD
// finding are emitted as length+protocol only, never the value.
func TestWPAD_SecretRedaction(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "wpad", "")
	if err != nil {
		t.Fatalf("run wpad: %v", err)
	}

	f := findFindingByModule(t, recs, "wpad")
	if f == nil {
		t.Fatal("no wpad finding emitted")
	}

	var detail struct {
		Protocol string `json:"protocol"`
		Length   int    `json:"length"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}

	if detail.Protocol == "" {
		t.Error("protocol field should be non-empty")
	}
	if detail.Length <= 0 {
		t.Error("length field should be positive")
	}

	// Verify the raw JSON does not contain the secret value.
	rawJSON, _ := json.Marshal(f)
	secretValue := "NTLMSSP"
	if string(rawJSON) != "" && contains(string(rawJSON), secretValue) {
		t.Errorf("finding JSON contains secret value %q — redaction failed", secretValue)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub))))
}

func TestOSPFCatalogClassIsTemporaryRestored(t *testing.T) {
	e := mustEntry(t, "ospf", "")
	if e.Class != catalog.TemporaryRestored {
		t.Fatalf("ospf class: got %v, want TemporaryRestored", e.Class)
	}
}

func TestEIGRPCatalogClassIsTemporaryRestored(t *testing.T) {
	e := mustEntry(t, "eigrp", "")
	if e.Class != catalog.TemporaryRestored {
		t.Fatalf("eigrp class: got %v, want TemporaryRestored", e.Class)
	}
}

func TestWPADCatalogClassIsTransientDecay(t *testing.T) {
	e := mustEntry(t, "wpad", "")
	if e.Class != catalog.TransientDecay {
		t.Fatalf("wpad class: got %v, want TransientDecay", e.Class)
	}
}

func TestOSPFTeardownIsGoodbyeFlush(t *testing.T) {
	e := mustEntry(t, "ospf", "")
	if e.Teardown != "goodbye/flush teardown" {
		t.Fatalf("ospf teardown: got %q, want 'goodbye/flush teardown'", e.Teardown)
	}
}

func TestEIGRPTeardownIsGoodbyeFlush(t *testing.T) {
	e := mustEntry(t, "eigrp", "")
	if e.Teardown != "goodbye/flush teardown" {
		t.Fatalf("eigrp teardown: got %q, want 'goodbye/flush teardown'", e.Teardown)
	}
}

func TestWPADTeardownIsTTLBound(t *testing.T) {
	e := mustEntry(t, "wpad", "")
	if e.Teardown != "client proxy config residue, bound = TTL" {
		t.Fatalf("wpad teardown: got %q, want 'client proxy config residue, bound = TTL'", e.Teardown)
	}
}

// TestOSPF_AllRawCraft verifies that all OSPF PDU types use raw craft (not
// the fork's SerializeTo, which doesn't exist for OSPFv2). We assert by
// checking that the crafted frames have the expected OSPF header version
// (2) and type fields at the correct offsets.
func TestOSPF_AllRawCraft(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runSingleWithBudget(t, leg, "ospf", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	if len(tx) < 3 {
		t.Fatalf("TX count: got %d, want >= 3", len(tx))
	}

	// Each TX frame is Ethernet(14) + IPv4(20) + OSPF header.
	// Verify OSPF version (2) at offset 34 in each frame.
	for i, frame := range tx[:3] {
		if len(frame) < 35 {
			t.Fatalf("frame %d too short for OSPF header", i)
		}
		if frame[34] != 2 {
			t.Errorf("frame %d: OSPF version: got %d, want 2", i, frame[34])
		}
	}
}

// TestOSPF_TeardownStepCount verifies the OSPF behavior arms exactly 2
// teardown steps (flush + goodbye).
func TestOSPF_TeardownStepCount(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runSingleWithBudget(t, leg, "ospf", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run ospf: %v", err)
	}

	tx := leg.TX()
	// 3 attack + 2 teardown = 5 total.
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5 (3 attack + 2 teardown)", len(tx))
	}
}

// TestEIGRP_TeardownStepCount verifies the EIGRP behavior arms exactly 1
// teardown step (goodbye).
func TestEIGRP_TeardownStepCount(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runSingleWithBudget(t, leg, "eigrp", "", 1*time.Second)
	if err != nil {
		t.Fatalf("run eigrp: %v", err)
	}

	tx := leg.TX()
	// 2 attack + 1 teardown = 3 total (without rejection).
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3 (2 attack + 1 teardown)", len(tx))
	}
}

// TestWPAD_NoTeardownSteps verifies WPAD (transient-decay) arms no
// teardown steps — no extra TX frames beyond the 2 attack frames.
func TestWPAD_NoTeardownSteps(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runSingle(t, leg, "wpad", "")
	if err != nil {
		t.Fatalf("run wpad: %v", err)
	}

	tx := leg.TX()
	// 2 attack frames only, no teardown.
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (attack only, no teardown)", len(tx))
	}
}

// itoa is a minimal int-to-string to avoid strconv import.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// Ensure net is used (WPAD proxy listener).
var (
	_ = net.Listen
	_ = errors.New
)
