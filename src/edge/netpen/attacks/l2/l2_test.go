// l2_test.go tests the L2 attack behaviors against fixture-mirrored TX
// sequences: byte-for-byte where deterministic, field-set where
// randomized. Tests use the in-memory [testtest.Leg] harness (pcap-fed RX
// + recorded TX) and exercise each behavior through the runner, which
// creates the Stream, Teardown, and Deps. The target and the leg's MAC are
// the fixture's values, so the frames the harvest generator pinned still
// come out byte for byte.

package l2_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
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
// VLAN pair of the double-tag frames, the voice VLAN, the VTP domain, and
// the port-steal victim.
func fixtureTarget() check.Target {
	return check.Target{
		Victim:    check.Host{IP: net.IPv4(10, 0, 0, 1)},
		OuterVLAN: 10,
		InnerVLAN: 20,
		VoiceVLAN: 20,
		VTPDomain: "LABDOMAIN",
	}
}

// fixtureLeg returns a test leg whose own MAC is the fixture MAC.
func fixtureLeg(t *testing.T) *testtest.Leg {
	t.Helper()
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
	leg.SetAddr(link.Addr{MAC: fixtureMAC})
	return leg
}

// runOne runs a single behavior through the runner over the family's own
// catalog against the fixture target and returns the findings and the run
// error. An acknowledged permanent mode raises the ceiling to permanent.
func runOne(t *testing.T, leg *testtest.Leg, name, mode string, ack ...runner.AttackRef) ([]findings.Record, error) {
	t.Helper()
	opts := runner.Options{
		Catalog:   catalog.MustNew(l2.Behaviors()...),
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: name, Mode: mode}},
		Target:    fixtureTarget(),
		MaxImpact: check.ImpactTemporary,
	}
	if len(ack) > 0 {
		opts.Acknowledged = ack
		opts.MaxImpact = check.ImpactPermanent
	}
	return run(t, opts)
}

func run(t *testing.T, opts runner.Options) ([]findings.Record, error) {
	t.Helper()
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
func mustEntry(t *testing.T, name, mode string) check.Entry {
	t.Helper()
	e, _, ok := catalog.MustNew(l2.Behaviors()...).Lookup(name, mode)
	if !ok {
		t.Fatalf("catalog entry not found: %s/%s", name, mode)
	}
	return e
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
	return testtest.ReadPcap(t, testtest.FixturePath(t, "l2/"+name))
}

func TestDTP_NegotiateAndRestore(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runOne(t, leg, "dtp", "")
	if err != nil {
		t.Fatalf("run dtp: %v", err)
	}

	// The behavior sends the desirable frame, then the teardown sends
	// the access-restore frame after the behavior returns.
	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (negotiate + restore)", len(tx))
	}

	fixtures := fixturePackets(t, "dtp.pcap")
	testtest.AssertBytesEqual(t, "DTP desirable", tx[0], fixtures[0])

	restoreFixtures := fixturePackets(t, "dtp_restore.pcap")
	testtest.AssertBytesEqual(t, "DTP restore", tx[1], restoreFixtures[0])

	var finding *findings.Finding
	for i := range recs {
		if recs[i].Kind == findings.KindFinding {
			finding = recs[i].Finding
		}
	}
	if finding == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Durability string `json:"durability"`
	}
	if err := json.Unmarshal(finding.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if detail.Durability != "temporary/active" {
		t.Errorf("durability = %q, want the entry's cost fields temporary/active", detail.Durability)
	}
}

// TestDTP_KeepTrunkNoRestore pins the permanent mode: acknowledged and
// admitted by a permanent ceiling, it sends the desirable frame only and
// arms nothing.
func TestDTP_KeepTrunkNoRestore(t *testing.T) {
	leg := fixtureLeg(t)

	_, err := runOne(t, leg, "dtp", "keep-trunk",
		runner.AttackRef{Name: "dtp", Mode: "keep-trunk"},
	)
	if err != nil {
		t.Fatalf("run dtp keep-trunk: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1 (no restore in keep-trunk)", len(tx))
	}
}

func TestDoubleTag_ByteForByte(t *testing.T) {
	leg := fixtureLeg(t)

	_, err := runOne(t, leg, "doubletag", "")
	if err != nil {
		t.Fatalf("run doubletag: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
	fixtures := fixturePackets(t, "doubletag.pcap")
	testtest.AssertBytesEqual(t, "DoubleTag", tx[0], fixtures[0])
}

// TestEmptyTargetIsSkipped pins the one-round contract on a real behavior:
// with no target the hop sends nothing, the runner records a skipped
// verdict naming the missing VLANs, and the run returns the exit-2 error.
func TestEmptyTargetIsSkipped(t *testing.T) {
	leg := fixtureLeg(t)
	recs, err := run(t, runner.Options{
		Catalog:   catalog.MustNew(l2.Behaviors()...),
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vlanhop"}},
		MaxImpact: check.ImpactTemporary,
	})
	if code, ok := errs.CodeOf(err); !ok || code != check.ErrCodeTargetMissing {
		t.Fatalf("run vlanhop with no target = %v, want %q", err, check.ErrCodeTargetMissing)
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
	for _, field := range []string{"outer-vlan", "inner-vlan"} {
		if !strings.Contains(v.Detail, field) {
			t.Errorf("verdict detail %q does not name %s", v.Detail, field)
		}
	}
}

func TestVlanEnum_EnumeratesFixtureVLANs(t *testing.T) {
	leg := fixtureLeg(t)

	for _, f := range fixturePackets(t, "vlanenum.pcap") {
		leg.PushRX(f)
	}

	recs, err := runOne(t, leg, "vlanenum", "")
	if err != nil {
		t.Fatalf("run vlanenum: %v", err)
	}

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
	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictConfirmed || v.Detail != "VLANs 10, 20, 30, 40" {
		t.Errorf("verdict = %+v, want confirmed with the inventory", v)
	}
}

func TestVlanHop_RestoreArmed(t *testing.T) {
	leg := fixtureLeg(t)

	_, err := runOne(t, leg, "vlanhop", "")
	if err != nil {
		t.Fatalf("run vlanhop: %v", err)
	}
	// The runner runs the teardown step, and a behavior that armed zero steps
	// would fail the run, so a nil error means the restore step ran.
	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
}

func TestVlanHop_PersistNoRestore(t *testing.T) {
	leg := fixtureLeg(t)

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
	leg := fixtureLeg(t)

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
	leg := fixtureLeg(t)

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
		testtest.AssertBytesEqual(t, "STP BPDU", frame, fixtures[0])
	}

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestCAMFlood_UniqueSourceMACs(t *testing.T) {
	leg := fixtureLeg(t)

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
		testtest.AssertBytesEqual(t, "CAM flood frame", frame, fixtures[i])
	}
}

func TestVTP_SafeMode(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runOne(t, leg, "vtp", "")
	if err != nil {
		t.Fatalf("run vtp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	fixtures := fixturePackets(t, "vtp.pcap")
	testtest.AssertBytesEqual(t, "VTP summary", tx[0], fixtures[0])

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestMVRP_JoinInFlood(t *testing.T) {
	leg := fixtureLeg(t)

	_, err := runOne(t, leg, "mvrp", "")
	if err != nil {
		t.Fatalf("run mvrp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2", len(tx))
	}

	fixtures := fixturePackets(t, "mvrp.pcap")
	testtest.AssertBytesEqual(t, "MVRP JoinIn VLAN 10", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "MVRP JoinIn VLAN 20", tx[1], fixtures[1])
}

func TestPortSteal_Default(t *testing.T) {
	leg := fixtureLeg(t)

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
	leg := fixtureLeg(t)

	_, err := runOne(t, leg, "portsteal", "relay")
	if err != nil {
		t.Fatalf("run portsteal relay: %v", err)
	}
	// A nil error means the armed restore step ran.
	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3", len(tx))
	}
}

func TestEtherChannel_LACPandPAgP(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runOne(t, leg, "etherchannel", "")
	if err != nil {
		t.Fatalf("run etherchannel: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (LACP + PAgP)", len(tx))
	}

	fixtures := fixturePackets(t, "etherchannel.pcap")
	testtest.AssertBytesEqual(t, "LACPDU", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "PAgP hello", tx[1], fixtures[1])

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

// TestEtherChannel_Reproducibility: two in-memory runs over the same
// fixture produce the same findings class.
func TestEtherChannel_Reproducibility(t *testing.T) {
	leg1 := fixtureLeg(t)
	recs1, _ := runOne(t, leg1, "etherchannel", "")
	leg2 := fixtureLeg(t)
	recs2, _ := runOne(t, leg2, "etherchannel", "")

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

// TestPermanentModesArePermanent pins the cost fields of the modes that
// outlive the run, and that stproot carries the 6 s max age its BPDU
// announces as its decay bound.
func TestPermanentModesArePermanent(t *testing.T) {
	for _, c := range []struct{ name, mode string }{{"vtp", "wipe"}, {"vtp", "set"}, {"dtp", "keep-trunk"}, {"vlanhop", "persist"}, {"voicevlan", "persist"}} {
		entry := mustEntry(t, c.name, c.mode)
		if entry.Impact != check.ImpactPermanent || entry.Restore.Kind != check.RestoreNone {
			t.Errorf("%s/%s = impact %s restore %s, want permanent with no restore", c.name, c.mode, entry.Impact, entry.Restore.Kind)
		}
	}
	if bound := mustEntry(t, "stproot", "").Restore.Bound.Seconds(); bound != 6 {
		t.Errorf("stproot decay bound = %v s, want 6", bound)
	}
}

// TestVTPWipeWithoutAckRefused pins the gate on a real behavior: under a
// permanent ceiling but without the acknowledgment, vtp --wipe sends
// nothing and the run stops on the refusal.
func TestVTPWipeWithoutAckRefused(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := run(t, runner.Options{
		Catalog:   catalog.MustNew(l2.Behaviors()...),
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "vtp", Mode: "wipe"}},
		Target:    fixtureTarget(),
		MaxImpact: check.ImpactPermanent,
	})
	if err == nil {
		t.Fatal("expected gate refusal error, got nil")
	}
	if leg.SendCount() != 0 {
		t.Errorf("frames sent: got %d, want 0 (gate refuses before TX)", leg.SendCount())
	}
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
