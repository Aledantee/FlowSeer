// fh_test.go tests the first-hop/identity attack behaviors against
// fixture-mirrored TX sequences: byte-for-byte where deterministic,
// field-set where randomized. Tests use the in-memory [testtest.Leg]
// harness (pcap-fed RX + recorded TX) and exercise each behavior through
// the runner, which creates the Stream, Teardown, and Deps. The target and
// the leg's addresses are the fixture's values, so the frames the harvest
// generator pinned still come out byte for byte.

package fh_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/fh"
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
// victim and gateway pair of the ARP fixtures, the virtual IP, group, and
// VRID of the first-hop redundancy fixtures, and the sweep network.
func fixtureTarget() check.Target {
	_, sweep, _ := net.ParseCIDR("172.16.0.0/24")
	return check.Target{
		Victim:    check.Host{IP: net.IPv4(172, 16, 0, 10), MAC: net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}},
		Gateway:   check.Host{IP: net.IPv4(172, 16, 0, 1), MAC: net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xfe}},
		VirtualIP: net.IPv4(10, 0, 0, 1),
		Group:     new(uint16(1)),
		VRID:      51,
		SweepNet:  sweep,
	}
}

// fixtureLeg returns a test leg whose own addresses are the fixture MAC and
// the given IPv4, the attacker address the fixture frames carry.
func fixtureLeg(t *testing.T, ipv4 net.IP) *testtest.Leg {
	t.Helper()
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
	leg.SetAddr(link.Addr{MAC: fixtureMAC, IPv4: ipv4.To4()})
	return leg
}

// runOne runs the attacks through the runner over the family's own
// catalog and returns the findings and the run error. The stream is drained
// concurrently with Run.
func runOne(t *testing.T, opts runner.Options) ([]findings.Record, error) {
	t.Helper()
	opts.Catalog = catalog.MustNew(fh.Behaviors()...)
	if opts.MaxImpact == "" {
		opts.MaxImpact = check.ImpactTemporary
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

// runSingle runs one attack on a single leg against the fixture target.
func runSingle(t *testing.T, leg *testtest.Leg, name string, target check.Target) ([]findings.Record, error) {
	t.Helper()
	return runOne(t, runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: name}},
		Target:    target,
	})
}

func mustEntry(t *testing.T, name, mode string) check.Entry {
	t.Helper()
	e, _, ok := catalog.MustNew(fh.Behaviors()...).Lookup(name, mode)
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
	return testtest.ReadPcap(t, testtest.FixturePath(t, "fh/"+name))
}

func TestARPSweep_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(172, 16, 0, 1))

	recs, err := runSingle(t, leg, "arpsweep", fixtureTarget())
	if err != nil {
		t.Fatalf("run arpsweep: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3", len(tx))
	}

	fixtures := fixturePackets(t, "arpsweep.pcap")
	for i, frame := range tx {
		testtest.AssertBytesEqual(t, "ARP sweep frame", frame, fixtures[i])
	}

	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictConfirmed {
		t.Fatalf("verdict = %+v, want confirmed with the inventory", v)
	}
	if !strings.Contains(v.Detail, "172.16.0.3") {
		t.Errorf("verdict detail %q does not list the requested hosts", v.Detail)
	}
}

// TestEmptyTargetIsSkipped pins the one-round contract on a real behavior:
// with no target the behavior sends nothing, the runner records a skipped
// verdict naming the missing fields, and the single command's run returns
// the exit-2 error.
func TestEmptyTargetIsSkipped(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
	recs, err := runSingle(t, leg, "arpspoof", check.Target{})
	if code, ok := errs.CodeOf(err); !ok || code != check.ErrCodeTargetMissing {
		t.Fatalf("run arpspoof with no target = %v, want %q", err, check.ErrCodeTargetMissing)
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
	if want := "the check needs: --victim <ip>,<mac>, --gateway <ip>,<mac>"; v.Detail != want {
		t.Errorf("verdict detail = %q, want %q", v.Detail, want)
	}
}

// TestNeedFieldsPerBehavior pins, per behavior that reads a target, the
// exact flags an empty target reports, and that hsrp refuses a group its
// one-octet frame field cannot carry with the invalid code and no frame.
func TestNeedFieldsPerBehavior(t *testing.T) {
	cases := []struct {
		name   string
		target check.Target
		code   errs.Code
		detail string
	}{
		{"arpsweep", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --sweep-net <cidr>"},
		{"arpspoof", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>, --gateway <ip>,<mac>"},
		{"gratarp", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --vip <ip>"},
		{"hsrp", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --group <n>, --vip <ip>"},
		{"vrrp", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --vrid <n>, --vip <ip>"},
		{"icmpredirect", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>, --gateway <ip>,<mac>"},
		{"llmnr", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>"},
		{"llmnr victim without MAC", check.Target{Victim: check.Host{IP: net.IPv4(172, 16, 0, 10)}}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>"},
		{"icmpredirect victim without MAC", check.Target{Victim: check.Host{IP: net.IPv4(172, 16, 0, 10)}, Gateway: check.Host{IP: net.IPv4(172, 16, 0, 1)}}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>"},
		{"glbp", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --group <n>, --vip <ip>"},
		{"hsrp group 300", check.Target{Group: new(uint16(300)), VirtualIP: net.IPv4(10, 0, 0, 1)}, check.ErrCodeTargetInvalid, "the check needs: --group at most 255"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
			name, _, _ := strings.Cut(tc.name, " ")
			recs, err := runSingle(t, leg, name, tc.target)
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

// TestARPSpoof_RestoreOrder: the arpspoof behavior arms ip-forward-
// restore FIRST, then neighbor-unicast-repair steps. The runner executes
// teardown in arm order, so the recorded TX after completion shows:
// poison frames, then neighbor repair frames.
func TestARPSpoof_RestoreOrder(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	recs, err := runSingle(t, leg, "arpspoof", fixtureTarget())
	if err != nil {
		t.Fatalf("run arpspoof: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 4 {
		t.Fatalf("TX count: got %d, want 4 (2 poison + 2 restore)", len(tx))
	}

	poisonFixtures := fixturePackets(t, "arpspoof.pcap")
	testtest.AssertBytesEqual(t, "ARP poison victim", tx[0], poisonFixtures[0])
	testtest.AssertBytesEqual(t, "ARP poison gateway", tx[1], poisonFixtures[1])

	restoreFixtures := fixturePackets(t, "arpspoof_restore.pcap")
	testtest.AssertBytesEqual(t, "ARP restore victim", tx[2], restoreFixtures[0])
	testtest.AssertBytesEqual(t, "ARP restore gateway", tx[3], restoreFixtures[1])

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictPending {
		t.Errorf("verdict = %+v, want pending", v)
	}
}

// TestARPSpoof_UnreachableTargetAfterPoison plants a TX error so the poison
// send fails: the behavior's error becomes a pending verdict, the
// host-local restore step still runs, the wire repairs fail into the
// teardown's partial record, and the run completes without hanging.
func TestARPSpoof_UnreachableTargetAfterPoison(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
	leg.SetTXError(context.Canceled)

	recs, err := runOne(t, runner.Options{
		AttackLeg:      leg,
		Attacks:        []runner.AttackRef{{Name: "arpspoof"}},
		Target:         fixtureTarget(),
		TeardownBudget: 1_000_000_000,
	})
	if code, _ := errs.CodeOf(err); code != catalog.ErrCodeTeardownPartial {
		t.Errorf("run error = %v, want the teardown partial failure of the wire repairs", err)
	}
	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictPending || !strings.Contains(v.Detail, "send victim poison") {
		t.Errorf("verdict = %+v, want pending with the send error", v)
	}
	var partial *findings.ErrorRecord
	for _, rec := range recs {
		if rec.Kind == findings.KindError && rec.Error != nil && rec.Error.Code == catalog.ErrCodeTeardownPartial.String() {
			partial = rec.Error
		}
	}
	if partial == nil {
		t.Fatal("no teardown partial record naming the failed repairs")
	}
	for _, step := range []string{"neighbor-unicast-repair-victim", "neighbor-unicast-repair-gateway"} {
		if !strings.Contains(partial.Message, step) {
			t.Errorf("partial record %q does not name the failed step %s", partial.Message, step)
		}
	}
}

func TestGratARP_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	recs, err := runSingle(t, leg, "gratarp", fixtureTarget())
	if err != nil {
		t.Fatalf("run gratarp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3", len(tx))
	}

	fixtures := fixturePackets(t, "gratarp.pcap")
	for i, frame := range tx {
		testtest.AssertBytesEqual(t, "GratARP frame", frame, fixtures[i])
	}

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

func TestHSRP_ResignTeardown(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	recs, err := runSingle(t, leg, "hsrp", fixtureTarget())
	if err != nil {
		t.Fatalf("run hsrp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3 (coup + hello + resign)", len(tx))
	}

	fixtures := fixturePackets(t, "hsrp.pcap")
	testtest.AssertBytesEqual(t, "HSRP coup", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "HSRP hello", tx[1], fixtures[1])

	restoreFixtures := fixturePackets(t, "hsrp_restore.pcap")
	testtest.AssertBytesEqual(t, "HSRP resign", tx[2], restoreFixtures[0])

	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictPending || v.Detail != "no observable: coup acceptance needs a hello listener" {
		t.Errorf("verdict = %+v, want the pending reason naming the hello listener", v)
	}
}

func TestVRRP_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	recs, err := runSingle(t, leg, "vrrp", fixtureTarget())
	if err != nil {
		t.Fatalf("run vrrp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2", len(tx))
	}

	fixtures := fixturePackets(t, "vrrp.pcap")
	testtest.AssertBytesEqual(t, "VRRP advertisement", tx[0], fixtures[0])

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

func TestICMPRedirect_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 99))

	recs, err := runSingle(t, leg, "icmpredirect", fixtureTarget())
	if err != nil {
		t.Fatalf("run icmpredirect: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	fixtures := fixturePackets(t, "icmpredirect.pcap")
	testtest.AssertBytesEqual(t, "ICMP redirect", tx[0], fixtures[0])

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

// TestLLMNR_FixturePins pins the spoofed answer against the fixture, whose
// victim is 10.0.0.2 behind the attacker's own MAC, and that the finding
// carries no credential fields.
func TestLLMNR_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 1))
	target := fixtureTarget()
	target.Victim = check.Host{IP: net.IPv4(10, 0, 0, 2), MAC: fixtureMAC}

	recs, err := runSingle(t, leg, "llmnr", target)
	if err != nil {
		t.Fatalf("run llmnr: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	// LLMNR response is the 2nd fixture frame.
	fixtures := fixturePackets(t, "llmnr.pcap")
	testtest.AssertBytesEqual(t, "LLMNR response", tx[0], fixtures[1])

	finding := findFinding(t, recs)
	if finding == nil {
		t.Fatal("no finding emitted")
	}
	var detail map[string]any
	if err := json.Unmarshal(finding.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	for _, field := range []string{"protocol", "length"} {
		if _, ok := detail[field]; ok {
			t.Errorf("finding carries %q; the behavior captures no credential", field)
		}
	}
	if detail["answer"] != "10.0.0.1" {
		t.Errorf("answer = %v, want the leg's own IPv4 10.0.0.1", detail["answer"])
	}
}

// TestGhost_WithoutWatchLegFailsFast: ghost invoked without a watch
// leg fails fast with a coded error and zero frames sent.
func TestGhost_WithoutWatchLegFailsFast(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	_, err := runSingle(t, leg, "ghost", fixtureTarget())
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodeWatchLegRequired {
		t.Fatalf("run ghost without a watch leg = %v, want %q", err, catalog.ErrCodeWatchLegRequired)
	}
	if leg.SendCount() != 0 {
		t.Errorf("frames sent: got %d, want 0 (fails fast before TX)", leg.SendCount())
	}
}

// TestGhost_TraversalAttribution: the ghost behavior sends frames from
// the attack leg, and the watch leg is fed matching frames (proving
// traversal). The behavior distinguishes attack-leg-produced frames
// (ghost SA source MAC) from ambient traffic and confirms traversal.
func TestGhost_TraversalAttribution(t *testing.T) {
	attackLeg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
	watchLeg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	ghostFixtures := fixturePackets(t, "ghost.pcap")
	ambientFixtures := fixturePackets(t, "ghost_ambient.pcap")
	for _, f := range ghostFixtures {
		watchLeg.PushRX(f)
	}
	for _, f := range ambientFixtures {
		watchLeg.PushRX(f)
	}

	recs, err := runOne(t, runner.Options{
		AttackLeg: attackLeg,
		WatchLeg:  watchLeg,
		Attacks:   []runner.AttackRef{{Name: "ghost"}},
	})
	if err != nil {
		t.Fatalf("run ghost: %v", err)
	}

	tx := attackLeg.TX()
	if len(tx) != 2 {
		t.Fatalf("attack TX count: got %d, want 2", len(tx))
	}
	testtest.AssertBytesEqual(t, "ghost STP", tx[0], ghostFixtures[0])
	testtest.AssertBytesEqual(t, "ghost LLDP", tx[1], ghostFixtures[1])

	ghostRec := findFinding(t, recs)
	if ghostRec == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Sent       int    `json:"sent"`
		Observed   int    `json:"observed"`
		Traversal  string `json:"traversal"`
		Attributed string `json:"attributed"`
	}
	if err := json.Unmarshal(ghostRec.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if detail.Traversal != "forwarded" {
		t.Errorf("traversal: got %q, want %q", detail.Traversal, "forwarded")
	}
	if detail.Attributed == "none" {
		t.Error("attribution: got none, want attack-distinguished-from-ambient or attack-only")
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictConfirmed {
		t.Errorf("verdict = %+v, want confirmed", v)
	}
}

func TestGLBP_ResignTeardown(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	recs, err := runSingle(t, leg, "glbp", fixtureTarget())
	if err != nil {
		t.Fatalf("run glbp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (hello + resign)", len(tx))
	}

	fixtures := fixturePackets(t, "glbp.pcap")
	testtest.AssertBytesEqual(t, "GLBP hello", tx[0], fixtures[0])

	restoreFixtures := fixturePackets(t, "glbp_restore.pcap")
	testtest.AssertBytesEqual(t, "GLBP resign", tx[1], restoreFixtures[0])

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

// TestReproducibility: two in-memory runs over the same fixture produce
// the same finding class and TX bytes.
func TestReproducibility(t *testing.T) {
	for _, name := range []string{"glbp", "lldpspoof"} {
		t.Run(name, func(t *testing.T) {
			leg1 := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
			recs1, _ := runSingle(t, leg1, name, fixtureTarget())
			leg2 := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
			recs2, _ := runSingle(t, leg2, name, fixtureTarget())

			f1 := findFinding(t, recs1)
			f2 := findFinding(t, recs2)
			if f1 == nil || f2 == nil {
				t.Fatal("expected findings from both runs")
			}
			if f1.Module != f2.Module {
				t.Errorf("module mismatch: %q vs %q", f1.Module, f2.Module)
			}
			var d1, d2 struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(f1.Detail, &d1)
			_ = json.Unmarshal(f2.Detail, &d2)
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
		})
	}
}

func TestLLDPSpoof_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))

	recs, err := runSingle(t, leg, "lldpspoof", fixtureTarget())
	if err != nil {
		t.Fatalf("run lldpspoof: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2", len(tx))
	}

	fixtures := fixturePackets(t, "lldpspoof.pcap")
	for _, frame := range tx {
		testtest.AssertBytesEqual(t, "LLDP spoof frame", frame, fixtures[0])
	}

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

// TestCatalogCostFields pins each behavior's impact, restore, and legs
// against what the plan assigns from the registration text they replace.
func TestCatalogCostFields(t *testing.T) {
	cases := []struct {
		name    string
		impact  check.Impact
		restore check.RestoreKind
		legs    check.Legs
	}{
		{"arpsweep", check.ImpactNone, check.RestoreNone, check.AttackOnly},
		{"ghost", check.ImpactNone, check.RestoreNone, check.WatchRequired},
		{"arpspoof", check.ImpactTemporary, check.RestoreActive, check.AttackOnly},
		{"hsrp", check.ImpactTemporary, check.RestoreActive, check.AttackOnly},
		{"glbp", check.ImpactTemporary, check.RestoreActive, check.AttackOnly},
		{"vrrp", check.ImpactTemporary, check.RestoreDecay, check.AttackOnly},
		{"icmpredirect", check.ImpactTemporary, check.RestoreDecay, check.AttackOnly},
		{"gratarp", check.ImpactTemporary, check.RestoreDecay, check.AttackOnly},
		{"llmnr", check.ImpactTemporary, check.RestoreDecay, check.AttackOnly},
		{"lldpspoof", check.ImpactTemporary, check.RestoreDecay, check.AttackOnly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entry := mustEntry(t, c.name, "")
			if entry.Impact != c.impact || entry.Restore.Kind != c.restore || entry.Legs != c.legs {
				t.Errorf("%s = impact %s, restore %s, legs %s; want %s, %s, %s", c.name, entry.Impact, entry.Restore.Kind, entry.Legs, c.impact, c.restore, c.legs)
			}
		})
	}
	if bound := mustEntry(t, "llmnr", "").Restore.Bound; bound.Seconds() != 30 {
		t.Errorf("llmnr decay bound = %v, want the 30 s TTL of the answer", bound)
	}
}
