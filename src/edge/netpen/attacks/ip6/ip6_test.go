// ip6_test.go tests the DHCP/IPv6 attack behaviors against fixture-
// mirrored TX sequences: byte-for-byte where deterministic,
// field-set where randomized. Tests use the in-memory [testtest.Leg]
// harness (pcap-fed RX + recorded TX) and exercise each behavior
// through the runner, which creates the Stream, Teardown, and Deps.
// Durability duties, finding emission, decay bounds, and the
// watch-leg rule are asserted via the recorded TX and findings.

package ip6_test

import (
	"context"
	"encoding/json"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/ip6"
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
		opts.Behaviors = ip6.Behaviors()
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
//nolint:unparam // mode mirrors fh/l2's shape for catalog parity.
func runSingle(t *testing.T, leg *testtest.Leg, name, mode string, ack ...runner.AttackRef) ([]findings.Record, error) {
	t.Helper()
	opts := runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: name, Mode: mode}},
		Behaviors: ip6.Behaviors(),
	}
	if len(ack) > 0 {
		opts.Acknowledged = ack
	}
	return runOne(t, opts)
}

//nolint:unparam // mode mirrors fh/l2's shape for catalog parity.
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

func fixturePackets(t *testing.T, name string) [][]byte {
	t.Helper()
	return testtest.ReadPcap(t, testtest.FixturePath(t, "ip6/"+name))
}

func TestDHCPStarve_FixturePins(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "dhcpstarve", "")
	if err != nil {
		t.Fatalf("run dhcpstarve: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5", len(tx))
	}

	fixtures := fixturePackets(t, "dhcpstarve.pcap")
	for i, frame := range tx {
		// DHCP xids and chaddr are randomized fields; the
		// behavior uses the same fixed values as the harvest for
		// byte-stable fixtures.
		testtest.AssertBytesEqual(t, "DHCP starve frame", frame, fixtures[i])
	}

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestRogueDHCP_FixturePins(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "roguedhcp", "")
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

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}

	// Verify the finding carries secret-material metadata (protocol +
	// length, not the value).
	for _, rec := range recs {
		if rec.Kind == findings.KindFinding && rec.Finding != nil {
			var detail struct {
				Protocol string `json:"protocol"`
				Length   int    `json:"length"`
			}
			if err := json.Unmarshal(rec.Finding.Detail, &detail); err != nil {
				continue
			}
			if detail.Protocol != "dhcp" {
				t.Errorf("secret protocol: got %q, want %q", detail.Protocol, "dhcp")
			}
			if detail.Length <= 0 {
				t.Errorf("secret length: got %d, want > 0", detail.Length)
			}
		}
	}
}

func TestRogueDHCPv6_SolicitAdvertiseCycle(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// Feed the SOLICIT frame as RX so the behavior can decode it.
	fixtures := fixturePackets(t, "roguedhcp6.pcap")
	leg.PushRX(fixtures[0]) // SOLICIT

	recs, err := runSingle(t, leg, "roguedhcp6", "")
	if err != nil {
		t.Fatalf("run roguedhcp6: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1 (advertise)", len(tx))
	}

	// Verify the ADVERTISE matches the fixture.
	testtest.AssertBytesEqual(t, "Rogue DHCPv6 ADVERTISE", tx[0], fixtures[1])

	// Verify the finding carries the decay note.
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
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "ndpspoof", "")
	if err != nil {
		t.Fatalf("run ndpspoof: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	// The poison answer matches the reference fixture's fields.
	fixtures := fixturePackets(t, "ndpspoof.pcap")
	testtest.AssertBytesEqual(t, "NDP spoof NA", tx[0], fixtures[0])

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestDADDOS_NoDefense_AttackProceeds(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "daddos", "")
	if err != nil {
		t.Fatalf("run daddos: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	// Verify the DAD NS matches the fixture.
	fixtures := fixturePackets(t, "daddos.pcap")
	testtest.AssertBytesEqual(t, "DAD NS", tx[0], fixtures[0])

	// The finding should report not resisted (no defense observed).
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
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// Feed the defending NA from the legitimate owner (the host
	// completed DAD before the attack window).
	fixtures := fixturePackets(t, "daddos_resisted.pcap")
	leg.PushRX(fixtures[0]) // defending NA

	recs, err := runSingle(t, leg, "daddos", "")
	if err != nil {
		t.Fatalf("run daddos: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	// The finding should report resisted (the host completed DAD
	// before the attack window — never failed open).
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
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "roguera", "")
	if err != nil {
		t.Fatalf("run roguera: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}

	fixtures := fixturePackets(t, "roguera.pcap")
	testtest.AssertBytesEqual(t, "Rogue RA", tx[0], fixtures[0])

	if len(recs) == 0 {
		t.Fatal("no findings emitted")
	}
}

func TestRAGuard_TraversalAttribution(t *testing.T) {
	attackLeg := testtest.New()
	defer func() { _ = attackLeg.Close() }()

	watchLeg := testtest.New()
	defer func() { _ = watchLeg.Close() }()

	// Feed the watch leg with the forged RA (attack-leg-produced).
	raFixtures := fixturePackets(t, "raguard.pcap")
	for _, f := range raFixtures {
		watchLeg.PushRX(f)
	}

	opts := runner.Options{
		AttackLeg: attackLeg,
		WatchLeg:  watchLeg,
		Attacks:   []runner.AttackRef{{Name: "raguard"}},
		Behaviors: ip6.Behaviors(),
	}
	recs, err := runOne(t, opts)
	if err != nil {
		t.Fatalf("run raguard: %v", err)
	}

	// The attack leg sent 1 frame.
	tx := attackLeg.TX()
	if len(tx) != 1 {
		t.Fatalf("attack TX count: got %d, want 1", len(tx))
	}

	// Verify the attack frame matches the fixture.
	testtest.AssertBytesEqual(t, "RAGuard forged RA", tx[0], raFixtures[0])

	// The finding should report traversal evidence and attribution.
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
}

func TestRAGuard_WithoutWatchLeg_ReportsSentOnly(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	// No watch leg set (raguard is WatchOptional).
	recs, err := runSingle(t, leg, "raguard", "")
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
}

func TestRAFlood_BoundedBurst(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "raflood", "")
	if err != nil {
		t.Fatalf("run raflood: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5", len(tx))
	}

	fixtures := fixturePackets(t, "raflood.pcap")
	for i, frame := range tx {
		testtest.AssertBytesEqual(t, "RA flood frame", frame, fixtures[i])
	}

	// The finding should report frame counts and bounded=true.
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
}

// TestRAFlood_Reproducibility: two in-memory runs over the
// same fixture produce the same findings class.
func TestRAFlood_Reproducibility(t *testing.T) {
	leg1 := testtest.New()
	recs1, _ := runSingle(t, leg1, "raflood", "")
	defer func() { _ = leg1.Close() }()

	leg2 := testtest.New()
	recs2, _ := runSingle(t, leg2, "raflood", "")
	defer func() { _ = leg2.Close() }()

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
}

func TestMLD_BoundedBurst(t *testing.T) {
	leg := testtest.New()
	defer func() { _ = leg.Close() }()

	recs, err := runSingle(t, leg, "mld", "")
	if err != nil {
		t.Fatalf("run mld: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 5 {
		t.Fatalf("TX count: got %d, want 5", len(tx))
	}

	fixtures := fixturePackets(t, "mld.pcap")
	for i, frame := range tx {
		testtest.AssertBytesEqual(t, "MLD report frame", frame, fixtures[i])
	}

	// The finding should report frame counts and bounded=true.
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
}

// TestMLD_Reproducibility: two in-memory runs over the
// same fixture produce the same findings class.
func TestMLD_Reproducibility(t *testing.T) {
	leg1 := testtest.New()
	recs1, _ := runSingle(t, leg1, "mld", "")
	defer func() { _ = leg1.Close() }()

	leg2 := testtest.New()
	recs2, _ := runSingle(t, leg2, "mld", "")
	defer func() { _ = leg2.Close() }()

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
}

func TestSecretMaterialRedaction(t *testing.T) {
	secret := findings.NewSecret("dhcp", []byte("rogue-auth-secret"))

	data, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("marshal secret: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["protocol"] != "dhcp" {
		t.Errorf("protocol: got %v, want %v", m["protocol"], "dhcp")
	}
	if m["length"] != float64(len("rogue-auth-secret")) {
		t.Errorf("length: got %v, want %d", m["length"], len("rogue-auth-secret"))
	}
	if string(data) != `{"protocol":"dhcp","length":17}` {
		t.Errorf("secret JSON leaked value: %s", string(data))
	}
}

func TestDHCPStarveCatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "dhcpstarve", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("dhcpstarve class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestRogueDHCPv4CatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "roguedhcp", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("roguedhcp class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestRogueDHCPv6CatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "roguedhcp6", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("roguedhcp6 class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestDADDOSCatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "daddos", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("daddos class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestNDPSpoofCatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "ndpspoof", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("ndpspoof class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestRAGuardCatalogClassIsNonDestructive(t *testing.T) {
	entry := mustEntry(t, "raguard", "")
	if entry.Class != catalog.NonDestructive {
		t.Errorf("raguard class: got %v, want %v", entry.Class, catalog.NonDestructive)
	}
}

func TestRogueRACatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "roguera", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("roguera class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestRAFloodCatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "raflood", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("raflood class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestMLDCatalogClassIsTransientDecay(t *testing.T) {
	entry := mustEntry(t, "mld", "")
	if entry.Class != catalog.TransientDecay {
		t.Errorf("mld class: got %v, want %v", entry.Class, catalog.TransientDecay)
	}
}

func TestRogueDHCPv6DecayBoundIs300s(t *testing.T) {
	entry := mustEntry(t, "roguedhcp6", "")
	if entry.Teardown != "~300s lifetime announced" {
		t.Errorf("roguedhcp6 decay bound: got %q, want %q", entry.Teardown, "~300s lifetime announced")
	}
}

func TestRogueRADecayBoundIs1800s(t *testing.T) {
	entry := mustEntry(t, "roguera", "")
	if entry.Teardown != "~1800s lifetime announced" {
		t.Errorf("roguera decay bound: got %q, want %q", entry.Teardown, "~1800s lifetime announced")
	}
}

func TestRogueDHCPv4DecayBoundIs1800s(t *testing.T) {
	entry := mustEntry(t, "roguedhcp", "")
	if entry.Teardown != "client leases ~1800s" {
		t.Errorf("roguedhcp decay bound: got %q, want %q", entry.Teardown, "client leases ~1800s")
	}
}
