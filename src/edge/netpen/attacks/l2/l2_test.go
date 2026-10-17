// l2_test.go tests the L2 attack behaviors against fixture-mirrored TX
// sequences: byte-for-byte where deterministic, field-set where
// randomized. Tests use the in-memory [testtest.Leg] harness (pcap-fed RX
// + recorded TX) and exercise each behavior through the runner, which
// creates the Stream, Teardown, and Deps. The target and the leg's MAC are
// the fixture's values, so the frames the harvest generator pinned still
// come out byte for byte.

package l2_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
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

// switchMAC is the sender of every response fixture frame: the switch on
// the other side of the attack leg.
var switchMAC = net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}

// fixtureTarget builds the target the fixtures were harvested against: the
// VLAN pair of the double-tag frames, the voice VLAN, the VTP domain, the
// port-steal victim with the MAC the CAM flood probes, and the gateway the
// port-steal probe is sent to.
func fixtureTarget() check.Target {
	return check.Target{
		Victim:    check.Host{IP: net.IPv4(10, 0, 0, 1), MAC: net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}},
		Gateway:   check.Host{IP: net.IPv4(10, 0, 0, 254), MAC: switchMAC},
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

// run drives the runner over opts. A test that sets no window gets a short
// one, so an observer that runs its window out does so in milliseconds.
func run(t *testing.T, opts runner.Options) ([]findings.Record, error) {
	t.Helper()
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
	if want := "the check needs: --outer-vlan <n>, --inner-vlan <n>"; v.Detail != want {
		t.Errorf("verdict detail = %q, want %q", v.Detail, want)
	}
}

// TestNeedFieldsPerBehavior pins, per behavior that reads a target, the
// exact flags an empty target reports, and that a present field of the wrong
// family or range is refused with the invalid code before any frame.
func TestNeedFieldsPerBehavior(t *testing.T) {
	cases := []struct {
		name   string
		target check.Target
		code   errs.Code
		detail string
	}{
		{"vlanhop", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --outer-vlan <n>, --inner-vlan <n>"},
		{"doubletag", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --outer-vlan <n>, --inner-vlan <n>"},
		{"voicevlan", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --voice-vlan <n>"},
		{"vtp", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --vtp-domain <name>"},
		{"portsteal", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>, --gateway <ip>,<mac>"},
		{"portsteal IPv6 victim", check.Target{Victim: check.Host{IP: net.ParseIP("2001:db8::1")}, Gateway: fixtureTarget().Gateway}, check.ErrCodeTargetInvalid, "the check needs: --victim <ip>,<mac> wants an IPv4 address"},
		{"camflood", check.Target{}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>"},
		{"camflood victim without MAC", check.Target{Victim: check.Host{IP: net.IPv4(10, 0, 0, 1)}}, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>"},
		{"doubletag VLAN 4095", check.Target{OuterVLAN: 4095, InnerVLAN: 20}, check.ErrCodeTargetInvalid, "the check needs: --outer-vlan <n> wants a VLAN id from 1 to 4094"},
		{"voicevlan 4095", check.Target{VoiceVLAN: 4095}, check.ErrCodeTargetInvalid, "the check needs: --voice-vlan <n> wants a VLAN id from 1 to 4094"},
		{"vtp 33-byte domain", check.Target{VTPDomain: strings.Repeat("d", 33)}, check.ErrCodeTargetInvalid, "the check needs: --vtp-domain <name> wants a name of at most 32 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leg := fixtureLeg(t)
			name, _, _ := strings.Cut(tc.name, " ")
			recs, err := run(t, runner.Options{
				Catalog:   catalog.MustNew(l2.Behaviors()...),
				AttackLeg: leg,
				Attacks:   []runner.AttackRef{{Name: name}},
				Target:    tc.target,
				MaxImpact: check.ImpactTemporary,
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

// TestVTP_FindingReportsMode pins that the finding's mode is the catalog
// entry's mode, so a wipe run is not recorded as the default one.
func TestVTP_FindingReportsMode(t *testing.T) {
	for _, mode := range []string{"", "wipe", "set"} {
		t.Run("mode "+mode, func(t *testing.T) {
			leg := fixtureLeg(t)
			leg.PushRX(fixturePackets(t, "vtp_response.pcap")[0])
			var ack []runner.AttackRef
			if mode != "" {
				ack = []runner.AttackRef{{Name: "vtp", Mode: mode}}
			}
			recs, err := runOne(t, leg, "vtp", mode, ack...)
			if err != nil {
				t.Fatalf("run vtp %q: %v", mode, err)
			}
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
				Mode string `json:"mode"`
			}
			if err := json.Unmarshal(finding.Detail, &detail); err != nil {
				t.Fatalf("unmarshal finding: %v", err)
			}
			want := mode
			if want == "" {
				want = "default"
			}
			if detail.Mode != want {
				t.Errorf("finding mode = %q, want %q", detail.Mode, want)
			}
		})
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

// TestVlanHop_SendsTheHop pins that vlanhop sends doubletag's frame, arms
// nothing, and is pending without a watch leg.
func TestVlanHop_SendsTheHop(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runOne(t, leg, "vlanhop", "")
	if err != nil {
		t.Fatalf("run vlanhop: %v", err)
	}
	tx := leg.TX()
	if len(tx) != 1 {
		t.Fatalf("TX count: got %d, want 1", len(tx))
	}
	testtest.AssertBytesEqual(t, "VLAN hop", tx[0], fixturePackets(t, "vlanhop.pcap")[0])
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictPending || !strings.Contains(v.Detail, "no watch leg") {
		t.Errorf("verdict = %+v, want pending without a watch leg", v)
	}
}

// TestVoiceVLAN_RestoreArmed pins the base entry's frames: the LLDP-MED
// frame against the regenerated fixture, decoded by gopacket to the
// target VLAN, priority 5, and DSCP 46, then the armed shutdown with TTL 0.
func TestVoiceVLAN_RestoreArmed(t *testing.T) {
	leg := fixtureLeg(t)

	_, err := runOne(t, leg, "voicevlan", "")
	if err != nil {
		t.Fatalf("run voicevlan: %v", err)
	}
	tx := leg.TX()
	if len(tx) != 2 {
		t.Fatalf("TX count: got %d, want 2 (the frame + the shutdown)", len(tx))
	}
	testtest.AssertBytesEqual(t, "LLDP-MED frame", tx[0], fixturePackets(t, "voicevlan.pcap")[0])
	policy := medPolicyOf(t, tx[0])
	if policy.VLANId != 20 || policy.L2Priority != 5 || policy.DSCPValue != 46 || !policy.Tagged {
		t.Errorf("network policy = %+v, want VLAN 20, priority 5, DSCP 46, tagged", policy)
	}
	if ttl := lldpTTLOf(t, tx[1]); ttl != 0 {
		t.Errorf("shutdown TTL = %d, want 0", ttl)
	}
}

// medPolicyOf decodes the LLDP-MED network policy of an LLDP frame with
// gopacket, the second reader of the frame's layout.
func medPolicyOf(t *testing.T, frame []byte) layers.LLDPNetworkPolicy {
	t.Helper()
	pkt := gopacket.NewPacket(frame, layers.LayerTypeEthernet, gopacket.Default)
	info, ok := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
	if !ok {
		t.Fatalf("frame decodes to %v, want an LLDP info layer", pkt.Layers())
	}
	media, err := info.DecodeMedia()
	if err != nil {
		t.Fatalf("DecodeMedia: %v", err)
	}
	return media.NetworkPolicy
}

// lldpTTLOf decodes the TTL of an LLDP frame with gopacket.
func lldpTTLOf(t *testing.T, frame []byte) uint16 {
	t.Helper()
	pkt := gopacket.NewPacket(frame, layers.LayerTypeEthernet, gopacket.Default)
	lldp, ok := pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	if !ok {
		t.Fatalf("frame decodes to %v, want an LLDP layer", pkt.Layers())
	}
	return lldp.TTL
}

// TestCAMFlood_SendsTheProbeLast pins the flood: 10,000 stamped frames,
// the first five byte for byte against the fixture, then the unicast probe
// to the victim's MAC with the sentinel payload, and pending without a
// watch leg.
func TestCAMFlood_SendsTheProbeLast(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runOne(t, leg, "camflood", "")
	if err != nil {
		t.Fatalf("run camflood: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 10_001 {
		t.Fatalf("TX count: got %d, want 10,001", len(tx))
	}

	fixtures := fixturePackets(t, "camflood.pcap")
	for i, frame := range tx[:5] {
		testtest.AssertBytesEqual(t, "CAM flood frame", frame, fixtures[i])
	}
	last := tx[len(tx)-1]
	if got := net.HardwareAddr(last[0:6]); !bytes.Equal(got, fixtureTarget().Victim.MAC) {
		t.Errorf("probe destination = %s, want the victim's MAC", got)
	}
	if !bytes.Contains(last, []byte("netpen-camflood")) {
		t.Error("probe lacks the sentinel payload")
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictPending || !strings.Contains(v.Detail, "no watch leg") {
		t.Errorf("verdict = %+v, want pending without a watch leg", v)
	}
}

// TestVTP_SafeMode feeds the switch's revision-41 summary and pins the
// revision-42 summary the base mode answers with against the fixture.
func TestVTP_SafeMode(t *testing.T) {
	leg := fixtureLeg(t)
	leg.PushRX(fixturePackets(t, "vtp_response.pcap")[0])

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

// TestPortSteal_Default pins the three announcements against the fixture
// and the echo probe to the gateway that follows them.
func TestPortSteal_Default(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runOne(t, leg, "portsteal", "")
	if err != nil {
		t.Fatalf("run portsteal: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 4 {
		t.Fatalf("TX count: got %d, want 3 announcements and the probe", len(tx))
	}
	fixtures := fixturePackets(t, "portsteal.pcap")
	for i, frame := range tx[:3] {
		testtest.AssertBytesEqual(t, "port-steal announcement", frame, fixtures[i])
	}
	if got := net.HardwareAddr(tx[3][0:6]); !bytes.Equal(got, switchMAC) {
		t.Errorf("probe destination = %s, want the gateway's MAC", got)
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictPending || !strings.Contains(v.Detail, "no reply from the gateway") {
		t.Errorf("verdict = %+v, want pending on the quiet leg", v)
	}
}

// TestPortSteal_RelaySetsAndRestoresForwarding pins the relay mode through
// the sysctl seam: with a map holding 0, the behavior sees 1 while it runs
// and the armed step writes 0 back after the teardown.
func TestPortSteal_RelaySetsAndRestoresForwarding(t *testing.T) {
	leg := fixtureLeg(t)
	sysctl := runner.NewMapSysctl(map[string]string{"net.ipv4.ip_forward": "0"})

	var during string
	bs := l2.Behaviors()
	for i := range bs {
		if bs[i].Name == "portsteal" {
			run := bs[i].Run
			bs[i].Run = func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
				v, err := run(ctx, deps)
				during, _ = sysctl.Get("net.ipv4.ip_forward")
				return v, err
			}
		}
	}
	_, err := run(t, runner.Options{
		Catalog:   catalog.MustNew(bs...),
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "portsteal", Mode: "relay"}},
		Target:    fixtureTarget(),
		MaxImpact: check.ImpactTemporary,
		Sysctl:    sysctl,
	})
	if err != nil {
		t.Fatalf("run portsteal relay: %v", err)
	}
	if during != "1" {
		t.Errorf("ip_forward during the behavior = %q, want 1", during)
	}
	if after, _ := sysctl.Get("net.ipv4.ip_forward"); after != "0" {
		t.Errorf("ip_forward after the teardown = %q, want the 0 read before", after)
	}
	if tx := leg.TX(); len(tx) != 4 {
		t.Fatalf("TX count: got %d, want 3 announcements and the probe", len(tx))
	}
}

func TestEtherChannel_LACPandPAgP(t *testing.T) {
	leg := fixtureLeg(t)

	recs, err := runOne(t, leg, "etherchannel", "")
	if err != nil {
		t.Fatalf("run etherchannel: %v", err)
	}

	tx := leg.TX()
	if len(tx) != 3 {
		t.Fatalf("TX count: got %d, want 3 (LACP + PAgP + release)", len(tx))
	}

	fixtures := fixturePackets(t, "etherchannel.pcap")
	testtest.AssertBytesEqual(t, "LACPDU", tx[0], fixtures[0])
	testtest.AssertBytesEqual(t, "PAgP hello", tx[1], fixtures[1])

	release := gopacket.NewPacket(tx[2], layers.LayerTypeEthernet, gopacket.Default)
	lacp, ok := release.Layer(nl.LayerTypeLACP).(*nl.LACP)
	if !ok {
		t.Fatalf("release frame decodes to %v, want an LACPDU", release.Layers())
	}
	if lacp.Actor.State&(nl.LACPStateSynchronization|nl.LACPStateCollecting|nl.LACPStateDistributing) != 0 {
		t.Errorf("release actor state = 0x%02x, want no synchronization, collecting, or distributing bit", uint8(lacp.Actor.State))
	}

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
// outlive the run, that stproot carries the 6 s max age its BPDU
// announces as its decay bound, that the voicevlan persist mode decays
// with the 120 s TTL its frame announces, and that vlanhop costs nothing
// and has no mode.
func TestPermanentModesArePermanent(t *testing.T) {
	for _, c := range []struct{ name, mode string }{{"vtp", "wipe"}, {"vtp", "set"}, {"dtp", "keep-trunk"}} {
		entry := mustEntry(t, c.name, c.mode)
		if entry.Impact != check.ImpactPermanent || entry.Restore.Kind != check.RestoreNone {
			t.Errorf("%s/%s = impact %s restore %s, want permanent with no restore", c.name, c.mode, entry.Impact, entry.Restore.Kind)
		}
	}
	if bound := mustEntry(t, "stproot", "").Restore.Bound.Seconds(); bound != 6 {
		t.Errorf("stproot decay bound = %v s, want 6", bound)
	}
	persist := mustEntry(t, "voicevlan", "persist")
	if persist.Impact != check.ImpactTemporary || persist.Restore.Kind != check.RestoreDecay || persist.Restore.Bound != 120*time.Second {
		t.Errorf("voicevlan/persist = %+v, want temporary, decay 120 s", persist)
	}
	hop := mustEntry(t, "vlanhop", "")
	if hop.Impact != check.ImpactNone || hop.Restore.Kind != check.RestoreNone || hop.Affects != check.AffectsNone || hop.Legs != check.WatchOptional {
		t.Errorf("vlanhop = %+v, want none, none, none, watch-optional", hop)
	}
	if _, _, ok := catalog.MustNew(l2.Behaviors()...).Lookup("vlanhop", "persist"); ok {
		t.Error("vlanhop still has a persist mode")
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
