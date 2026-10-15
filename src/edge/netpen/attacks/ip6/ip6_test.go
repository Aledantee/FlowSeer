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
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

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
	if want := "the check needs: --victim <ip>,<mac>, --offer-ip <ip>"; v.Detail != want {
		t.Errorf("verdict detail = %q, want %q", v.Detail, want)
	}
}

// TestNeedFieldsPerBehavior pins, per behavior that reads a target, the
// exact flags an empty target reports and that a present field of the wrong
// family is refused with the invalid code before any frame.
func TestNeedFieldsPerBehavior(t *testing.T) {
	cases := []struct {
		name   string
		target check.Target
		code   errs.Code
		detail string
	}{
		{"roguedhcp", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>, --offer-ip <ip>"},
		{"roguedhcp6", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim6 <ip>,<mac>, --server6 <ip>,<mac>"},
		{"daddos", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim6 <ip>,<mac>"},
		{"ndpspoof", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim6 <ip>,<mac>"},
		{"raguard", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --prefix6 <cidr>"},
		{"roguera", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --prefix6 <cidr>"},
		{"daddos v4-mapped victim6", check.Target{Victim6: check.Host{IP: net.ParseIP("::ffff:10.0.0.5"), MAC: fixtureMAC}}, check.ErrCodeTargetInvalid, "the check needs: --victim6 <ip>,<mac> wants an IPv6 address"},
		{"roguedhcp IPv6 offer", check.Target{Victim: check.Host{IP: net.IPv4(172, 16, 0, 10), MAC: fixtureMAC}, OfferIP: net.ParseIP("2001:db8::1")}, check.ErrCodeTargetInvalid, "the check needs: --offer-ip <ip> wants an IPv4 address"},
		{"roguedhcp victim without MAC", check.Target{Victim: check.Host{IP: net.IPv4(172, 16, 0, 10)}, OfferIP: net.IPv4(172, 16, 0, 100)}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>"},
		{"roguedhcp6 victim6 without MAC", check.Target{Victim6: check.Host{IP: net.ParseIP("fd00::10")}, Server6: check.Host{IP: net.ParseIP("fd00::1")}}, check.ErrCodeTargetMissing, "the check needs: --victim6 <ip>,<mac>"},
		{"ndpspoof victim6 without MAC", check.Target{Victim6: check.Host{IP: net.ParseIP("fd00::10")}}, check.ErrCodeTargetMissing, "the check needs: --victim6 <ip>,<mac>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leg := fixtureLeg(t, nil)
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
	if detail.DecayNote != "600s valid lifetime announced" {
		t.Errorf("decay note: got %q, want %q", detail.DecayNote, "600s valid lifetime announced")
	}
}

// recordingEmitter keeps the findings a behavior emits when a test calls it
// directly inside a synctest bubble, where the runner's goroutines would
// outlive the bubble.
type recordingEmitter struct {
	findings []json.RawMessage
}

func (e *recordingEmitter) Emit(findings.Record) bool { return true }

func (e *recordingEmitter) Finding(_ string, detail json.RawMessage) bool {
	e.findings = append(e.findings, detail)
	return true
}

func (e *recordingEmitter) Progress(_, _ string) bool { return true }

// directDeps builds the dependencies for a direct call to a behavior with
// the given window.
func directDeps(attack, watch *testtest.Leg, target check.Target, window time.Duration) (check.Deps, *recordingEmitter) {
	em := &recordingEmitter{}
	deps := check.Deps{
		AttackLeg: attack,
		Target:    target,
		Observe:   window,
		Emitter:   em,
	}
	if watch != nil {
		deps.WatchLeg = watch
	}
	return deps, em
}

// TestRogueDHCPv6_QuietLegIsPending pins that a leg with no SOLICIT runs
// the window out: the verdict is pending naming the window, nothing is
// sent, and no finding is emitted.
func TestRogueDHCPv6_QuietLegIsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, nil)
		deps, em := directDeps(leg, nil, fixtureTarget(), 3*time.Second)
		start := time.Now()
		v, err := ip6.RunRogueDHCPv6(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunRogueDHCPv6() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictPending || v.Detail != "no solicit in 3s" {
			t.Errorf("verdict = %+v, want pending with detail %q", v, "no solicit in 3s")
		}
		if elapsed := time.Since(start); elapsed != 3*time.Second {
			t.Errorf("returned after %v, want the 3s window", elapsed)
		}
		if leg.SendCount() != 0 {
			t.Errorf("sends = %d, want 0", leg.SendCount())
		}
		if len(em.findings) != 0 {
			t.Errorf("findings = %d, want 0", len(em.findings))
		}
	})
}

// TestRogueDHCPv6_IgnoresOtherClients pins that a SOLICIT from another MAC
// is skipped and the victim's own SOLICIT behind it is the one answered,
// with the advertise matching the fixture, so the reply goes to the
// victim and not to whoever asked first.
func TestRogueDHCPv6_IgnoresOtherClients(t *testing.T) {
	fixtures := fixturePackets(t, "roguedhcp6.pcap")
	other := append([]byte(nil), fixtures[0]...)
	copy(other[6:12], net.HardwareAddr{0x00, 0xde, 0xad, 0xbe, 0xef, 0x02})
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, nil)
		leg.PushRX(other)
		leg.PushRX(fixtures[0])
		// The target names fd00::10 while the fixture SOLICIT comes from
		// fd00::100, which the pinned advertise answers.
		deps, em := directDeps(leg, nil, fixtureTarget(), 3*time.Second)
		v, err := ip6.RunRogueDHCPv6(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunRogueDHCPv6() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictPending || !strings.Contains(v.Detail, "REQUEST listener") {
			t.Errorf("verdict = %+v, want pending after the advertise", v)
		}
		tx := leg.TX()
		if len(tx) != 1 {
			t.Fatalf("TX count: got %d, want 1 (the advertise to the victim only)", len(tx))
		}
		testtest.AssertBytesEqual(t, "Rogue DHCPv6 ADVERTISE", tx[0], fixtures[1])
		if len(em.findings) != 1 {
			t.Errorf("findings = %d, want 1", len(em.findings))
		}
	})
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, nil)
		leg.PushRX(other)
		deps, _ := directDeps(leg, nil, fixtureTarget(), 2*time.Second)
		v, err := ip6.RunRogueDHCPv6(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunRogueDHCPv6() error = %v, want nil", err)
		}
		if v.Detail != "no solicit in 2s" || leg.SendCount() != 0 {
			t.Errorf("verdict %+v with %d sends, want pending at the window and 0 sends for another client's solicit", v, leg.SendCount())
		}
	})
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

// dadResisted returns the resisted field of the one finding em holds.
func dadResisted(t *testing.T, em *recordingEmitter) bool {
	t.Helper()
	if len(em.findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(em.findings))
	}
	var detail struct {
		Resisted bool `json:"resisted"`
	}
	if err := json.Unmarshal(em.findings[0], &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	return detail.Resisted
}

// TestDADDOS_NoDefense_AttackProceeds pins the NS against the fixture and
// that a quiet leg runs the window out with the finding reading no defense.
func TestDADDOS_NoDefense_AttackProceeds(t *testing.T) {
	fixtures := fixturePackets(t, "daddos.pcap")
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, nil)
		target := fixtureTarget()
		target.Victim6.IP = net.ParseIP("fd00::dead")
		deps, em := directDeps(leg, nil, target, 3*time.Second)
		start := time.Now()
		v, err := ip6.RunDADDOS(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunDADDOS() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictPending {
			t.Errorf("verdict = %+v, want pending", v)
		}
		if elapsed := time.Since(start); elapsed != 3*time.Second {
			t.Errorf("returned after %v, want the 3s window", elapsed)
		}
		tx := leg.TX()
		if len(tx) != 1 {
			t.Fatalf("TX count: got %d, want 1", len(tx))
		}
		testtest.AssertBytesEqual(t, "DAD NS", tx[0], fixtures[0])
		if dadResisted(t, em) {
			t.Error("resisted: got true, want false (no defense observed)")
		}
	})
}

// TestDADDOS_LateDefenseIsRead pins that a defending advertisement arriving
// after an unrelated frame and one second into the window is still read,
// and that the behavior returns on it rather than at the window.
func TestDADDOS_LateDefenseIsRead(t *testing.T) {
	defense := fixturePackets(t, "daddos_resisted.pcap")[0]
	unrelated := fixturePackets(t, "ndpspoof.pcap")[0]
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t, nil)
		leg.PushRX(unrelated)
		go func() {
			time.Sleep(time.Second)
			leg.PushRX(defense)
		}()
		target := fixtureTarget()
		target.Victim6.IP = net.ParseIP("fd00::dead")
		deps, em := directDeps(leg, nil, target, 3*time.Second)
		start := time.Now()
		if _, err := ip6.RunDADDOS(context.Background(), deps); err != nil {
			t.Fatalf("RunDADDOS() error = %v, want nil", err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("returned after %v, want 1s, when the defense arrived", elapsed)
		}
		if !dadResisted(t, em) {
			t.Error("resisted: got false, want true (the owner defended the address)")
		}
	})
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
	if detail.Traversal != "unobserved" {
		t.Errorf("traversal: got %q, want %q (no watch leg)", detail.Traversal, "unobserved")
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictPending {
		t.Errorf("verdict = %+v, want pending without a watch leg", v)
	}
}

// TestRAGuard_ProbeInstallsNothing pins the probe byte for byte against the
// fixture and decodes the fixture: every router parameter is zero, the
// options are exactly one Prefix Information option with flags 0, both
// lifetimes 0, and the target prefix, and there is no Source Link-Layer
// Address and no MTU option, so a host installs nothing from it.
func TestRAGuard_ProbeInstallsNothing(t *testing.T) {
	leg := fixtureLeg(t, nil)
	if _, err := runSingle(t, leg, "raguard", fixtureTarget()); err != nil {
		t.Fatalf("run raguard: %v", err)
	}
	fixture := fixturePackets(t, "raguard.pcap")[0]
	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
	testtest.AssertBytesEqual(t, "RAGuard probe RA", tx[0], fixture)

	pkt := gopacket.NewPacket(fixture, layers.LayerTypeEthernet, gopacket.Default)
	ra, ok := pkt.Layer(layers.LayerTypeICMPv6RouterAdvertisement).(*layers.ICMPv6RouterAdvertisement)
	if !ok {
		t.Fatalf("fixture does not decode as a router advertisement: %v", pkt.ErrorLayer())
	}
	if ra.RouterLifetime != 0 || ra.HopLimit != 0 || ra.ReachableTime != 0 || ra.RetransTimer != 0 || ra.Flags != 0 {
		t.Errorf("RA fields = lifetime %d hop %d reachable %d retrans %d flags %#x, want all 0", ra.RouterLifetime, ra.HopLimit, ra.ReachableTime, ra.RetransTimer, ra.Flags)
	}
	if len(ra.Options) != 1 {
		t.Fatalf("options = %d, want exactly one (prefix information)", len(ra.Options))
	}
	opt := ra.Options[0]
	if opt.Type != layers.ICMPv6OptPrefixInfo {
		t.Fatalf("option type = %v, want prefix information", opt.Type)
	}
	if len(opt.Data) != 30 {
		t.Fatalf("prefix option data = %d bytes, want 30", len(opt.Data))
	}
	if opt.Data[0] != 64 || opt.Data[1] != 0 {
		t.Errorf("prefix length %d flags %#x, want 64 and 0x00", opt.Data[0], opt.Data[1])
	}
	for _, b := range opt.Data[2:10] {
		if b != 0 {
			t.Errorf("prefix lifetimes = % x, want all zero", opt.Data[2:10])
			break
		}
	}
	if !net.IP(opt.Data[14:30]).Equal(net.ParseIP("fd00::")) {
		t.Errorf("prefix = %v, want fd00::", net.IP(opt.Data[14:30]))
	}
}

// raGuardTraversal returns the traversal field of the one finding em holds.
func raGuardTraversal(t *testing.T, em *recordingEmitter) string {
	t.Helper()
	if len(em.findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(em.findings))
	}
	var detail struct {
		Traversal string `json:"traversal"`
	}
	if err := json.Unmarshal(em.findings[0], &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	return detail.Traversal
}

// TestRAGuard_AmbientOnlyIsResisted pins attribution by content: a
// broadcast ARP from the attack leg's MAC, an RA from that MAC with another
// prefix, an RA from another MAC, a truncated RA, an RA whose prefix option
// carries the target prefix bytes under another prefix length, and an RA
// whose prefix option is too short to hold a prefix each leave the verdict
// resisted at the window, and the probe itself makes it confirmed.
func TestRAGuard_AmbientOnlyIsResisted(t *testing.T) {
	probe := fixturePackets(t, "raguard.pcap")[0]
	arpFromLeg := make([]byte, 42)
	copy(arpFromLeg[0:6], net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(arpFromLeg[6:12], fixtureMAC)
	arpFromLeg[12], arpFromLeg[13] = 0x08, 0x06
	otherPrefix := append([]byte(nil), fixturePackets(t, "roguera.pcap")[0]...)
	copy(otherPrefix[len(otherPrefix)-16:], net.ParseIP("fd01::"))
	otherMAC := append([]byte(nil), probe...)
	copy(otherMAC[6:12], net.HardwareAddr{0x00, 0xde, 0xad, 0xbe, 0xef, 0x02})
	truncated := probe[:len(probe)-20]
	// The prefix option is the last 32 bytes of the probe: type, length
	// 4, and 30 data bytes whose first is the prefix length.
	otherLen := append([]byte(nil), probe...)
	otherLen[len(otherLen)-30] = 48
	// An option of length 3 holds 22 data bytes, short of the 16-byte
	// prefix at data offset 14. The IPv6 payload length shrinks with it.
	shortOption := append([]byte(nil), probe[:len(probe)-32]...)
	shortOption = append(shortOption, byte(layers.ICMPv6OptPrefixInfo), 3, 64)
	shortOption = append(shortOption, make([]byte, 21)...)
	binary.BigEndian.PutUint16(shortOption[18:20], uint16(len(shortOption)-54))
	ambient := [][]byte{arpFromLeg, otherPrefix, otherMAC, truncated, otherLen, shortOption}

	synctest.Test(t, func(t *testing.T) {
		attackLeg := fixtureLeg(t, nil)
		watchLeg := fixtureLeg(t, nil)
		for _, f := range ambient {
			watchLeg.PushRX(f)
		}
		deps, em := directDeps(attackLeg, watchLeg, fixtureTarget(), 2*time.Second)
		start := time.Now()
		v, err := ip6.RunRAGuard(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunRAGuard() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictResisted {
			t.Errorf("verdict = %+v, want resisted with only ambient frames", v)
		}
		if elapsed := time.Since(start); elapsed != 2*time.Second {
			t.Errorf("returned after %v, want the 2s window", elapsed)
		}
		if v.Observed != 2*time.Second {
			t.Errorf("observed = %v, want 2s", v.Observed)
		}
		if got := raGuardTraversal(t, em); got != "not-forwarded" {
			t.Errorf("traversal = %q, want not-forwarded", got)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		attackLeg := fixtureLeg(t, nil)
		watchLeg := fixtureLeg(t, nil)
		for _, f := range ambient {
			watchLeg.PushRX(f)
		}
		watchLeg.PushRX(probe)
		deps, em := directDeps(attackLeg, watchLeg, fixtureTarget(), 2*time.Second)
		v, err := ip6.RunRAGuard(context.Background(), deps)
		if err != nil {
			t.Fatalf("RunRAGuard() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictConfirmed || v.Observed != 0 {
			t.Errorf("verdict = %+v, want confirmed at once on the probe", v)
		}
		if got := raGuardTraversal(t, em); got != "forwarded" {
			t.Errorf("traversal = %q, want forwarded", got)
		}
	})
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
// decay bounds the probes announce: the DHCPv6 advertise's 600 s valid
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
		{"roguedhcp6", check.ImpactTemporary, check.RestoreDecay, 600 * time.Second},
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
