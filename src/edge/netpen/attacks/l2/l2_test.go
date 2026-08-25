// l2_test.go tests the L2 attack behaviors against fixture-mirrored TX
// sequences: byte-for-byte where deterministic, field-set where
// randomized. Tests use the in-memory [testtest.Leg] harness (pcap-fed RX
// + recorded TX) and exercise each behavior through the runner, which
// creates the Stream, Teardown, and Deps. Durability duties, finding
// emission, and the DTP restore ordering are asserted via the recorded TX.

package l2_test

import (
	"context"
	"encoding/json"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// runOne runs a single behavior through the runner and returns the findings
// and the run error. The stream is drained concurrently with Run.
func runOne(t *testing.T, leg *testtest.Leg, name, mode string, ack ...runner.AttackRef) ([]findings.Record, error) {
	t.Helper()
	opts := runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: name, Mode: mode}},
		Behaviors: l2.Behaviors(),
	}
	if len(ack) > 0 {
		opts.Acknowledged = ack
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

// mustEntry looks up a catalog entry.
func mustEntry(t *testing.T, name, mode string) catalog.Entry {
	t.Helper()
	e, ok := runner.ResolveEntry(name, mode)
	if !ok {
		t.Fatalf("catalog entry not found: %s/%s", name, mode)
	}
	return e
}

func assertBytesEqual(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: length mismatch: got %d, want %d", label, len(got), len(want))
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: byte %d: got 0x%02x, want 0x%02x", label, i, got[i], want[i])
			return
		}
	}
}

func fixturePackets(t *testing.T, name string) [][]byte {
	t.Helper()
	return testtest.ReadPcap(t, testtest.FixturePath(t, "l2/"+name))
}

func TestDTP_NegotiateAndRestore(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runOne(t, leg, "dtp", "")
	if err != nil {
		t.Fatalf("run dtp: %v", err)
	}

	// The behavior sends the desirable frame, then the teardown
	// sends the access-restore frame. The runner runs teardown AFTER the
	// behavior returns. So TX should be: [desirable, access-restore].
	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (negotiate + restore)", len(tx))
	}

	// Verify the negotiate frame matches the fixture's desirable frame.
	fixtures := fixturePackets(t, "dtp.pcap")
	assertBytesEqual(t, "DTP desirable", tx[0], fixtures[0])

	// Verify the restore frame matches the fixture's access frame.
	restoreFixtures := fixturePackets(t, "dtp_restore.pcap")
	assertBytesEqual(t, "DTP restore", tx[1], restoreFixtures[0])

	// A finding was emitted.
	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestDTP_KeepTrunkNoRestore(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "dtp", "keep-trunk",
		runner.AttackRef{Name: "dtp", Mode: "keep-trunk"},
	)
	if err != nil {
		t.Fatalf("run dtp keep-trunk: %v", err)
	}

	// keep-trunk: only the desirable frame, no restore.
	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1 (no restore in keep-trunk)", len(tx))
	}
}

func TestDoubleTag_ByteForByte(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "doubletag", "")
	if err != nil {
		t.Fatalf("run doubletag: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
	fixtures := fixturePackets(t, "doubletag.pcap")
	assertBytesEqual(t, "DoubleTag", tx[0], fixtures[0])
}

func TestVlanEnum_EnumeratesFixtureVLANs(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// Feed the vlanenum fixture frames (VLANs 10, 20, 30, 40).
	for _, f := range fixturePackets(t, "vlanenum.pcap") {
		leg.PushRX(f)
	}

	recs, err := runOne(t, leg, "vlanenum", "")
	if err != nil {
		t.Fatalf("run vlanenum: %v", err)
	}

	// Find the finding record (may not be the first record).
	var findingRec *findings.Finding
	for i := range recs {
		if recs[i].Kind == findings.KindFinding {
			findingRec = recs[i].Finding
			break
		}
	}
	if findingRec == nil {
		t.Fatal("no finding record in recs")
	}

	var detail struct {
		VLANs []uint16 `json:"vlans"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal(findingRec.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	want := []uint16{10, 20, 30, 40}
	if len(detail.VLANs) != len(want) {
		t.Fatalf("VLAN count: got %d, want %d", len(detail.VLANs), len(want))
	}
	for i, v := range want {
		if detail.VLANs[i] != v {
			t.Errorf("VLAN[%d]: got %d, want %d", i, detail.VLANs[i], v)
		}
	}
}

func TestVlanHop_RestoreArmed(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "vlanhop", "")
	if err != nil {
		t.Fatalf("run vlanhop: %v", err)
	}
	// The runner runs the teardown step; if the behavior armed zero
	// steps, the runner would fail. A nil error means restore ran.
	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
}

func TestVlanHop_PersistNoRestore(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "vlanhop", "persist",
		runner.AttackRef{Name: "vlanhop", Mode: "persist"},
	)
	if err != nil {
		t.Fatalf("run vlanhop persist: %v", err)
	}
	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
}

func TestVoiceVLAN_RestoreArmed(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "voicevlan", "")
	if err != nil {
		t.Fatalf("run voicevlan: %v", err)
	}
	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
}

func TestSTPRoot_BurstSend(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runOne(t, leg, "stproot", "")
	if err != nil {
		t.Fatalf("run stproot: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3", len(tx))
	}

	fixtures := fixturePackets(t, "stproot.pcap")
	for _, frame := range tx {
		assertBytesEqual(t, "STP BPDU", frame, fixtures[0])
	}

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestCAMFlood_UniqueSourceMACs(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "camflood", "")
	if err != nil {
		t.Fatalf("run camflood: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5", len(tx))
	}

	fixtures := fixturePackets(t, "camflood.pcap")
	for i, frame := range tx {
		assertBytesEqual(t, "CAM flood frame", frame, fixtures[i])
	}
}

func TestVTP_SafeMode(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runOne(t, leg, "vtp", "")
	if err != nil {
		t.Fatalf("run vtp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	fixtures := fixturePackets(t, "vtp.pcap")
	assertBytesEqual(t, "VTP summary", tx[0], fixtures[0])

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestMVRP_JoinInFlood(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "mvrp", "")
	if err != nil {
		t.Fatalf("run mvrp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2", len(tx))
	}

	fixtures := fixturePackets(t, "mvrp.pcap")
	assertBytesEqual(t, "MVRP JoinIn VLAN 10", tx[0], fixtures[0])
	assertBytesEqual(t, "MVRP JoinIn VLAN 20", tx[1], fixtures[1])
}

func TestPortSteal_Default(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "portsteal", "")
	if err != nil {
		t.Fatalf("run portsteal: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3", len(tx))
	}
}

func TestPortSteal_RelayArmsRestore(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	_, err := runOne(t, leg, "portsteal", "relay",
		runner.AttackRef{Name: "portsteal", Mode: "relay"},
	)
	if err != nil {
		t.Fatalf("run portsteal relay: %v", err)
	}
	// A nil error means the teardown ran (temporary-restored).
	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3", len(tx))
	}
}

func TestEtherChannel_LACPandPAgP(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runOne(t, leg, "etherchannel", "")
	if err != nil {
		t.Fatalf("run etherchannel: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (LACP + PAgP)", len(tx))
	}

	fixtures := fixturePackets(t, "etherchannel.pcap")
	assertBytesEqual(t, "LACPDU", tx[0], fixtures[0])
	assertBytesEqual(t, "PAgP hello", tx[1], fixtures[1])

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

// TestEtherChannel_Reproducibility: two in-memory runs over
// the same fixture produce the same findings class.
func TestEtherChannel_Reproducibility(t *testing.T) {
	// Run 1.
	leg1 := testtest.New()
	recs1, _ := runOne(t, leg1, "etherchannel", "")
	defer func() { _ = leg1.Close() }()

	// Run 2.
	leg2 := testtest.New()
	recs2, _ := runOne(t, leg2, "etherchannel", "")
	defer func() { _ = leg2.Close() }()

	if len(recs1) == 0 || len(recs2) == 0 {
		t.Fatal("expected findings from both runs")
	}
	if recs1[0].Finding.Module != recs2[0].Finding.Module {
		t.Errorf("module mismatch: %q vs %q", recs1[0].Finding.Module, recs2[0].Finding.Module)
	}

	var d1, d2 struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(recs1[0].Finding.Detail, &d1)
	_ = json.Unmarshal(recs2[0].Finding.Detail, &d2)
	if d1.Action != d2.Action {
		t.Errorf("action mismatch: %q vs %q", d1.Action, d2.Action)
	}

	tx1, tx2 := leg1.TX(), leg2.TX()
	if len(tx1) != len(tx2) {
		t.Fatalf("TX count mismatch: %d vs %d", len(tx1), len(tx2))
	}
	for i := range tx1 {
		if string(tx1[i]) != string(tx2[i]) {
			t.Errorf("TX[%d] mismatch", i)
		}
	}
}

func TestSecretMaterialRedaction(t *testing.T) {
	secret := findings.NewSecret("vtp", []byte("supersecret"))

	data, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("marshal secret: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["protocol"] != "vtp" {
		t.Errorf("protocol: got %v, want %v", m["protocol"], "vtp")
	}
	if m["length"] != float64(len("supersecret")) {
		t.Errorf("length: got %v, want %d", m["length"], len("supersecret"))
	}
	if string(data) != `{"protocol":"vtp","length":11}` {
		t.Errorf("secret JSON leaked value: %s", string(data))
	}
}

func TestVTPWipeCatalogClassIsPermanent(t *testing.T) {
	entry := mustEntry(t, "vtp", "wipe")
	if entry.Class != catalog.PermanentDestructive {
		t.Errorf("vtp/wipe class: got %v, want %v", entry.Class, catalog.PermanentDestructive)
	}
}

func TestDTPKeepTrunkCatalogClassIsPermanent(t *testing.T) {
	entry := mustEntry(t, "dtp", "keep-trunk")
	if entry.Class != catalog.PermanentDestructive {
		t.Errorf("dtp/keep-trunk class: got %v, want %v", entry.Class, catalog.PermanentDestructive)
	}
}

func TestVTPWipeWithoutAckRefused(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// Run vtp --wipe WITHOUT the ack — the gate should refuse.
	recs, err := runOne(t, leg, "vtp", "wipe")
	if err == nil {
		t.Fatal("expected gate refusal error, got nil")
	}

	// No frames should have been sent — the gate was evaluated before TX.
	if leg.SendCount() != 0 {
		t.Errorf("frames sent: got %d, want 0 (gate refuses before TX)", leg.SendCount())
	}

	// A refusal record should be in the findings.
	found := false
	for _, rec := range recs {
		if rec.Kind == findings.KindRefusal {
			found = true
			break
		}
	}
	if !found {
		t.Error("no refusal record in findings")
	}
}

// (The test runner's -timeout flag handles hangs.)
