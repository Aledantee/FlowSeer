// ip6_test.go tests the DHCP/IPv6 attack behaviors against fixture-
// mirrored TX sequences: byte-for-byte where deterministic, field-set
// where randomized. Tests use the in-memory [testtest.Leg] harness
// (pcap-fed RX + recorded TX) and exercise each behavior through the
// runner, which creates the Stream and Deps. The target and the leg's
// addresses are the fixture's values, so the frames the harvest generator
// pinned still come out byte for byte.

package ip6_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/ip6"
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
// DHCPv4 client and the address offered to it, the IPv6 victim the
// neighbor-discovery fixtures name, the rogue DHCPv6 server address, and
// the advertised prefix.
func fixtureTarget() check.Target {
	_, prefix6, _ := net.ParseCIDR("fd00::/64")
	clientMAC := net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	return check.Target{
		Victim:  check.Host{IP: net.IPv4(172, 16, 0, 10), MAC: clientMAC},
		OfferIP: net.IPv4(172, 16, 0, 100),
		Victim6: check.Host{IP: net.ParseIP("fd00::10"), MAC: clientMAC},
		Server6: check.Host{IP: net.ParseIP("fd00::1")},
		Prefix6: prefix6,
	}
}

// fixtureLeg returns a test leg whose own addresses are the fixture MAC,
// the rogue DHCPv4 server's IPv4, and the given IPv6.
func fixtureLeg(t *testing.T, ipv6 net.IP) *testtest.Leg {
	t.Helper()
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
	leg.SetAddr(link.Addr{MAC: fixtureMAC, IPv4: net.IPv4(172, 16, 0, 99).To4(), IPv6: ipv6})
	return leg
}

// runOne runs the attacks through the runner over the family's own
// catalog and returns the findings and the run error. The stream is drained
// concurrently with Run.
func runOne(t *testing.T, opts runner.Options) ([]findings.Record, error) {
	t.Helper()
	if len(opts.Catalog.Entries()) == 0 {
		opts.Catalog = catalog.MustNew(ip6.Behaviors()...)
	}
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

// runSingle runs one attack on a single leg against the given target.
func runSingle(t *testing.T, leg *testtest.Leg, name string, target check.Target) ([]findings.Record, error) {
	t.Helper()
	return runOne(t, runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: name}},
		Target:    target,
	})
}

func mustEntry(t *testing.T, name string) check.Entry {
	t.Helper()
	e, _, ok := catalog.MustNew(ip6.Behaviors()...).Lookup(name, "")
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
	return testtest.ReadPcap(t, testtest.FixturePath(t, "ip6/"+name))
}

func TestDHCPStarve_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, nil)

	recs, err := runSingle(t, leg, "dhcpstarve", fixtureTarget())
	if err != nil {
		t.Fatalf("run dhcpstarve: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5", len(tx))
	}

	fixtures := fixturePackets(t, "dhcpstarve.pcap")
	for i, frame := range tx {
		testtest.AssertBytesEqual(t, "DHCP starve frame", frame, fixtures[i])
	}

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

// TestRogueDHCP_FixturePins pins the OFFER and ACK against the fixture and
// that the finding carries no credential fields.
func TestRogueDHCP_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, nil)

	recs, err := runSingle(t, leg, "roguedhcp", fixtureTarget())
	if err != nil {
		t.Fatalf("run roguedhcp: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (offer + ack)", len(tx))
	}

	fixtures := fixturePackets(t, "roguedhcp.pcap")
	testtest.AssertBytesEqual(t, "Rogue DHCP OFFER", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "Rogue DHCP ACK", tx[1], fixtures[1])

	f := findFinding(t, recs)
	if f == nil {
		t.Fatal("no finding emitted")
	}
	var detail map[string]any
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	for _, field := range []string{"protocol", "length"} {
		if _, ok := detail[field]; ok {
			t.Errorf("finding carries %q; the behavior captures no credential", field)
		}
	}
	if detail["server_id"] != "172.16.0.99" {
		t.Errorf("server_id = %v, want the leg's own IPv4", detail["server_id"])
	}
}

// TestEmptyTargetIsSkipped pins the one-round contract on a real behavior:
// with no target the rogue server sends nothing, the runner records a
// skipped verdict naming the missing fields, and the run returns the
// exit-2 error.
func TestEmptyTargetIsSkipped(t *testing.T) {
	leg := fixtureLeg(t, nil)
	recs, err := runSingle(t, leg, "roguedhcp", check.Target{})
	if code, ok := errs.CodeOf(err); !ok || code != check.ErrCodeTargetMissing {
		t.Fatalf("run roguedhcp with no target = %v, want %q", err, check.ErrCodeTargetMissing)
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
	for _, field := range []string{"victim", "victim-mac", "offer-ip"} {
		if !strings.Contains(v.Detail, field) {
			t.Errorf("verdict detail %q does not name %s", v.Detail, field)
		}
	}
}

func TestRogueDHCPv6_SolicitAdvertiseCycle(t *testing.T) {
	leg := fixtureLeg(t, nil)

	fixtures := fixturePackets(t, "roguedhcp6.pcap")
	leg.PushRX(fixtures[0]) // SOLICIT

	target := fixtureTarget()
	target.Victim6.IP = net.ParseIP("fd00::100")
	recs, err := runSingle(t, leg, "roguedhcp6", target)
	if err != nil {
		t.Fatalf("run roguedhcp6: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1 (advertise)", len(tx))
	}
	testtest.AssertBytesEqual(t, "Rogue DHCPv6 ADVERTISE", tx[0], fixtures[1])

	f := findFinding(t, recs)
	if f == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Action    string `json:"action"`
		DecayNote string `json:"decay_note"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if detail.Action != "rogue-dhcpv6-server" {
		t.Errorf("action: got %q, want %q", detail.Action, "rogue-dhcpv6-server")
	}
	if detail.DecayNote != "~300s lifetime announced" {
		t.Errorf("decay note: got %q, want %q", detail.DecayNote, "~300s lifetime announced")
	}
}

func TestNDPSpoof_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, net.ParseIP("fd00::999"))

	recs, err := runSingle(t, leg, "ndpspoof", fixtureTarget())
	if err != nil {
		t.Fatalf("run ndpspoof: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	fixtures := fixturePackets(t, "ndpspoof.pcap")
	testtest.AssertBytesEqual(t, "NDP spoof NA", tx[0], fixtures[0])

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

func TestDADDOS_NoDefense_AttackProceeds(t *testing.T) {
	leg := fixtureLeg(t, nil)
	target := fixtureTarget()
	target.Victim6.IP = net.ParseIP("fd00::dead")

	recs, err := runSingle(t, leg, "daddos", target)
	if err != nil {
		t.Fatalf("run daddos: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	fixtures := fixturePackets(t, "daddos.pcap")
	testtest.AssertBytesEqual(t, "DAD NS", tx[0], fixtures[0])

	f := findFinding(t, recs)
	if f == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Resisted bool `json:"resisted"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if detail.Resisted {
		t.Error("resisted: got true, want false (no defense observed)")
	}
}

func TestDADDOS_HostCompletesDAD_ReportsResisted(t *testing.T) {
	leg := fixtureLeg(t, nil)
	target := fixtureTarget()
	target.Victim6.IP = net.ParseIP("fd00::dead")

	fixtures := fixturePackets(t, "daddos_resisted.pcap")
	leg.PushRX(fixtures[0]) // defending NA

	recs, err := runSingle(t, leg, "daddos", target)
	if err != nil {
		t.Fatalf("run daddos: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	f := findFinding(t, recs)
	if f == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Resisted bool `json:"resisted"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if !detail.Resisted {
		t.Error("resisted: got false, want true (host completed DAD before attack window)")
	}
}

func TestRogueRA_FixturePins(t *testing.T) {
	leg := fixtureLeg(t, nil)

	recs, err := runSingle(t, leg, "roguera", fixtureTarget())
	if err != nil {
		t.Fatalf("run roguera: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	fixtures := fixturePackets(t, "roguera.pcap")
	testtest.AssertBytesEqual(t, "Rogue RA", tx[0], fixtures[0])

	if findFinding(t, recs) == nil {
		t.Fatal("no finding emitted")
	}
}

func TestRAGuard_TraversalAttribution(t *testing.T) {
	attackLeg := fixtureLeg(t, nil)
	watchLeg := fixtureLeg(t, nil)

	raFixtures := fixturePackets(t, "raguard.pcap")
	for _, f := range raFixtures {
		watchLeg.PushRX(f)
	}

	recs, err := runOne(t, runner.Options{
		AttackLeg: attackLeg,
		WatchLeg:  watchLeg,
		Attacks:   []runner.AttackRef{{Name: "raguard"}},
		Target:    fixtureTarget(),
	})
	if err != nil {
		t.Fatalf("run raguard: %v", err)
	}

	tx := attackLeg.TX()
	if len(tx) != 1 {
		t.Fatalf("attack TX count: got %d, want 1", len(tx))
	}
	testtest.AssertBytesEqual(t, "RAGuard forged RA", tx[0], raFixtures[0])

	f := findFinding(t, recs)
	if f == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Sent       int    `json:"sent"`
		Observed   int    `json:"observed"`
		Traversal  string `json:"traversal"`
		Attributed string `json:"attributed"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if detail.Traversal != "forwarded" {
		t.Errorf("traversal: got %q, want %q", detail.Traversal, "forwarded")
	}
	if detail.Attributed == "none" {
		t.Error("attribution: got none, want attack-only or attack-distinguished-from-ambient")
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictConfirmed {
		t.Errorf("verdict = %+v, want confirmed", v)
	}
}

// TestRAGuard_EmptyWindowIsResisted pins the observation window: a watch
// leg that delivers nothing yields a resisted verdict once the window
// ends, with the observed time at or above the window and the run back
// within a generous bound of it. The bound is the timing under test.
func TestRAGuard_EmptyWindowIsResisted(t *testing.T) {
	attackLeg := fixtureLeg(t, nil)
	watchLeg := fixtureLeg(t, nil)
	const window = 50 * time.Millisecond

	start := time.Now()
	recs, err := runOne(t, runner.Options{
		AttackLeg: attackLeg,
		WatchLeg:  watchLeg,
		Attacks:   []runner.AttackRef{{Name: "raguard"}},
		Target:    fixtureTarget(),
		Observe:   window,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("run raguard: %v", err)
	}
	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictResisted {
		t.Fatalf("verdict = %+v, want resisted on an empty window", v)
	}
	if v.ObservedMS < window.Milliseconds() {
		t.Errorf("observed_ms = %d, want at least the %d ms window", v.ObservedMS, window.Milliseconds())
	}
	if elapsed > 20*window {
		t.Errorf("run took %v, want it bounded by the %v window", elapsed, window)
	}
}

func TestRAGuard_WithoutWatchLeg_ReportsSentOnly(t *testing.T) {
	leg := fixtureLeg(t, nil)

	recs, err := runSingle(t, leg, "raguard", fixtureTarget())
	if err != nil {
		t.Fatalf("run raguard: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	f := findFinding(t, recs)
	if f == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Sent      int    `json:"sent"`
		Traversal string `json:"traversal"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if detail.Sent != 1 {
		t.Errorf("sent: got %d, want 1", detail.Sent)
	}
	if detail.Traversal != "not-forwarded" {
		t.Errorf("traversal: got %q, want %q (no watch leg)", detail.Traversal, "not-forwarded")
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictPending {
		t.Errorf("verdict = %+v, want pending without a watch leg", v)
	}
}

func TestBoundedBursts(t *testing.T) {
	for _, c := range []struct {
		name    string
		fixture string
		ipv6    net.IP
	}{
		{"raflood", "raflood.pcap", nil},
		{"mld", "mld.pcap", net.ParseIP("fe80::1")},
	} {
		t.Run(c.name, func(t *testing.T) {
			leg := fixtureLeg(t, c.ipv6)

			recs, err := runSingle(t, leg, c.name, fixtureTarget())
			if err != nil {
				t.Fatalf("run %s: %v", c.name, err)
			}

			tx := leg.TX()
			if len(tx) != 5 {
				t.Fatalf("TX count: got %d, want 5", len(tx))
			}
			fixtures := fixturePackets(t, c.fixture)
			for i, frame := range tx {
				testtest.AssertBytesEqual(t, c.name+" frame", frame, fixtures[i])
			}

			f := findFinding(t, recs)
			if f == nil {
				t.Fatal("no finding emitted")
			}
			var detail struct {
				FrameCount int  `json:"frame_count"`
				Bounded    bool `json:"bounded"`
			}
			if err := json.Unmarshal(f.Detail, &detail); err != nil {
				t.Fatalf("unmarshal finding: %v", err)
			}
			if detail.FrameCount != 5 {
				t.Errorf("frame count: got %d, want 5", detail.FrameCount)
			}
			if !detail.Bounded {
				t.Error("bounded: got false, want true")
			}
		})
	}
}

// TestReproducibility: two in-memory runs over the same fixture produce
// the same finding class and TX bytes.
func TestReproducibility(t *testing.T) {
	for _, c := range []struct {
		name string
		ipv6 net.IP
	}{{"raflood", nil}, {"mld", net.ParseIP("fe80::1")}} {
		t.Run(c.name, func(t *testing.T) {
			leg1 := fixtureLeg(t, c.ipv6)
			recs1, _ := runSingle(t, leg1, c.name, fixtureTarget())
			leg2 := fixtureLeg(t, c.ipv6)
			recs2, _ := runSingle(t, leg2, c.name, fixtureTarget())

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

// TestCatalogCostFields pins each behavior's impact and restore, and the
// decay bounds the probes announce: the DHCPv6 advertise's 300 s valid
// lifetime and the 1800 s router lifetime and DHCPv4 lease.
func TestCatalogCostFields(t *testing.T) {
	cases := []struct {
		name    string
		impact  check.Impact
		restore check.RestoreKind
		bound   time.Duration
	}{
		{"dhcpstarve", check.ImpactTemporary, check.RestoreDecay, 0},
		{"roguedhcp", check.ImpactTemporary, check.RestoreDecay, 1800 * time.Second},
		{"roguedhcp6", check.ImpactTemporary, check.RestoreDecay, 300 * time.Second},
		{"daddos", check.ImpactTemporary, check.RestoreDecay, 0},
		{"ndpspoof", check.ImpactTemporary, check.RestoreDecay, 0},
		{"raguard", check.ImpactNone, check.RestoreNone, 0},
		{"roguera", check.ImpactTemporary, check.RestoreDecay, 1800 * time.Second},
		{"raflood", check.ImpactTemporary, check.RestoreDecay, 0},
		{"mld", check.ImpactTemporary, check.RestoreDecay, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entry := mustEntry(t, c.name)
			if entry.Impact != c.impact || entry.Restore.Kind != c.restore || entry.Restore.Bound != c.bound {
				t.Errorf("%s = impact %s, restore %s bound %v; want %s, %s, %v", c.name, entry.Impact, entry.Restore.Kind, entry.Restore.Bound, c.impact, c.restore, c.bound)
			}
		})
	}
	if legs := mustEntry(t, "raguard").Legs; legs != check.WatchOptional {
		t.Errorf("raguard legs = %s, want %s", legs, check.WatchOptional)
	}
}
