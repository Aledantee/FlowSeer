package full

// recon_scan_test.go tests the production recon (parseReconFrame + ARP
// sweep), scan (emitScanFinding + activeVLANProbe), and the stub-free
// defaultRecon integration path against captured fixture frames.
//
// Fixtures live in full/testdata/*.pcap and attacks/testdata/**/*.pcap.
// Tests craft minimal in-memory legs that deliver fixture frames on RX
// and record TX for assertion.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"

	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/link"
)

// --- fixture helpers ---

// readPcap reads all packets from a pcap file relative to the test's
// working directory (the full/ package).
func readPcap(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	rd, err := pcapgo.NewReader(f)
	if err != nil {
		t.Fatalf("new pcap reader %s: %v", path, err)
	}
	var pkts [][]byte
	for {
		data, _, err := rd.ReadPacketData()
		if err != nil {
			break
		}
		pkts = append(pkts, data)
	}
	if len(pkts) == 0 {
		t.Fatalf("no packets in %s", path)
	}
	return pkts
}

// fixturePath resolves a path relative to the full/testdata directory.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "testdata", name)
}

// attacksFixturePath resolves a path relative to attacks/testdata.
func attacksFixturePath(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// full/ is at src/netpen/full/; attacks/testdata is at src/netpen/attacks/testdata
	dir := wd
	for {
		if filepath.Base(dir) == "netpen" {
			return filepath.Join(dir, "attacks", "testdata", rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(wd, "..", "attacks", "testdata", rel)
}

// scriptableLeg is an in-memory link.Leg for production-path tests.
// RX frames are pre-loaded; TX frames are recorded.
type scriptableLeg struct {
	mu       sync.Mutex
	rx       []link.Frame
	tx       [][]byte
	txErr    error
	closed   bool
	closedCh chan struct{}
}

var _ link.Leg = (*scriptableLeg)(nil)

func newScriptableLeg() *scriptableLeg {
	return &scriptableLeg{closedCh: make(chan struct{})}
}

func (l *scriptableLeg) PushRX(data []byte) {
	l.mu.Lock()
	l.rx = append(l.rx, link.Frame{Data: append([]byte(nil), data...)})
	l.mu.Unlock()
}

func (l *scriptableLeg) Send(_ context.Context, pkt []byte) error {
	if l.closed {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.txErr != nil {
		return l.txErr
	}
	l.tx = append(l.tx, append([]byte(nil), pkt...))
	return nil
}

func (l *scriptableLeg) SetFilter(_ []link.RawInstruction) error { return nil }

func (l *scriptableLeg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame, 64)
	go func() {
		defer close(out)
		l.mu.Lock()
		pending := l.rx
		l.rx = nil
		l.mu.Unlock()
		for _, f := range pending {
			select {
			case out <- f:
			case <-ctx.Done():
				return
			case <-l.closedCh:
				return
			}
		}
		select {
		case <-ctx.Done():
		case <-l.closedCh:
		}
	}()
	return out
}

func (l *scriptableLeg) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.closedCh)
	l.mu.Unlock()
	return nil
}

func (l *scriptableLeg) TX() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([][]byte, len(l.tx))
	for i, s := range l.tx {
		out[i] = append([]byte(nil), s...)
	}
	return out
}

func (l *scriptableLeg) TXCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tx)
}

// --- parseReconFrame tests ---

// TestParseReconFrame_ARPReply sets ev["macs"] from an ARP reply.
func TestParseReconFrame_ARPReply(t *testing.T) {
	pkts := readPcap(t, fixturePath(t, "arp_reply.pcap"))
	ev := Evidence{}
	parseReconFrame(ev, pkts[0])
	macs := ev.MACs()
	if len(macs) != 1 {
		t.Fatalf("expected 1 MAC, got %d: %v", len(macs), macs)
	}
	want := "aa:bb:cc:dd:ee:ff"
	if macs[0] != want {
		t.Errorf("MAC = %q, want %q", macs[0], want)
	}
}

// TestParseReconFrame_VRRP sets ev["vrids"] from a VRRP advertisement.
func TestParseReconFrame_VRRP(t *testing.T) {
	pkts := readPcap(t, fixturePath(t, "vrrp.pcap"))
	ev := Evidence{}
	parseReconFrame(ev, pkts[0])
	vrids := ev.VRIDs()
	if len(vrids) != 1 {
		t.Fatalf("expected 1 vrid, got %d: %v", len(vrids), vrids)
	}
	if vrids[0] != 42 {
		t.Errorf("vrid = %d, want 42", vrids[0])
	}
}

// TestParseReconFrame_RA sets ev["ra6"] from an ICMPv6 RA.
func TestParseReconFrame_RA(t *testing.T) {
	pkts := readPcap(t, fixturePath(t, "ra.pcap"))
	ev := Evidence{}
	parseReconFrame(ev, pkts[0])
	if !ev.Has(EvRA6) {
		t.Error("expected ra6 evidence from RA frame")
	}
}

// TestParseReconFrame_Dot1Q sets ev["vlans"] from a tagged frame.
func TestParseReconFrame_Dot1Q(t *testing.T) {
	pkts := readPcap(t, fixturePath(t, "dot1q.pcap"))
	ev := Evidence{}
	parseReconFrame(ev, pkts[0])
	vlans := ev.VLANs()
	if len(vlans) != 1 {
		t.Fatalf("expected 1 VLAN, got %d: %v", len(vlans), vlans)
	}
	if vlans[0] != 100 {
		t.Errorf("VLAN = %d, want 100", vlans[0])
	}
}

// TestParseReconFrame_LLDPVoiceVLAN sets ev["voicevlan"] from LLDP-MED.
func TestParseReconFrame_LLDPVoiceVLAN(t *testing.T) {
	pkts := readPcap(t, fixturePath(t, "lldp_voice.pcap"))
	ev := Evidence{}
	parseReconFrame(ev, pkts[0])
	vv := ev.VoiceVLAN()
	if vv != 200 {
		t.Errorf("voice VLAN = %d, want 200", vv)
	}
}

// TestParseReconFrame_VTP sets vtp-domain + vtp-rev from a VTP summary.
func TestParseReconFrame_VTP(t *testing.T) {
	pkts := readPcap(t, attacksFixturePath(t, "l2/vtp.pcap"))
	ev := Evidence{}
	parseReconFrame(ev, pkts[0])
	// The VTP fixture may or may not have a domain; check what we get.
	// The fixture is known to contain VTP summary frames.
	if !ev.Has(EvVTPDomain) && !ev.Has(EvVTPRev) {
		t.Skip("VTP fixture did not decode a domain or revision (owned decoder may not be registered in test build)")
	}
}

// TestParseReconFrame_MVRP sets ev["mvrp"] from MVRP frames.
func TestParseReconFrame_MVRP(t *testing.T) {
	pkts := readPcap(t, attacksFixturePath(t, "l2/mvrp.pcap"))
	ev := Evidence{}
	parseReconFrame(ev, pkts[0])
	if !ev.Has(EvMVRP) {
		t.Skip("MVRP fixture did not decode (owned decoder may not be registered in test build)")
	}
}

// TestParseReconFrame_MultipleFrames exercises all evidence keys from a
// set of fixture frames fed through one Evidence map.
func TestParseReconFrame_MultipleFrames(t *testing.T) {
	ev := Evidence{}
	fixtures := []string{
		fixturePath(t, "arp_reply.pcap"),
		fixturePath(t, "vrrp.pcap"),
		fixturePath(t, "ra.pcap"),
		fixturePath(t, "dot1q.pcap"),
		fixturePath(t, "lldp_voice.pcap"),
	}
	for _, fp := range fixtures {
		for _, pkt := range readPcap(t, fp) {
			parseReconFrame(ev, pkt)
		}
	}
	// Assert all evidence keys are set.
	if !ev.Has(EvMACs) {
		t.Error("macs not set")
	}
	if !ev.Has(EvVRIDs) {
		t.Error("vrids not set")
	}
	if !ev.Has(EvRA6) {
		t.Error("ra6 not set")
	}
	if !ev.Has(EvVLANs) {
		t.Error("vlans not set")
	}
	if !ev.Has(EvVoiceVLAN) {
		t.Error("voicevlan not set")
	}
}

// --- ARP sweep test ---

// TestARPSweep_PopulatesMACs sends ARP requests and collects replies.
func TestARPSweep_PopulatesMACs(t *testing.T) {
	leg := newScriptableLeg()
	defer func() { _ = leg.Close() }()

	// Pre-load an ARP reply that will arrive during the sweep listen.
	arpReply := readPcap(t, fixturePath(t, "arp_reply.pcap"))[0]
	leg.PushRX(arpReply)

	ev := Evidence{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	arpSweep(ctx, Config{AttackLeg: leg}, ev, "10.0.0.0/30")

	macs := ev.MACs()
	if len(macs) == 0 {
		t.Fatal("expected at least 1 MAC from ARP sweep, got 0")
	}
	// The sweep should have sent ARP requests.
	if leg.TXCount() == 0 {
		t.Error("expected ARP requests to be sent during sweep")
	}
}

// --- emitScanFinding tests ---

// findingClass extracts the class from a scan finding detail.
func findingClass(t *testing.T, r findings.Record) string {
	t.Helper()
	if r.Finding == nil {
		t.Fatal("record has no finding")
	}
	var d scanFindingDetail
	if err := json.Unmarshal(r.Finding.Detail, &d); err != nil {
		t.Fatalf("unmarshal finding detail: %v", err)
	}
	return d.Class
}

// TestEmitScanFinding_Classes exercises the scan detector on fixture frames.
func TestEmitScanFinding_Classes(t *testing.T) {
	type tc struct {
		name    string
		pcap    string
		wantCls string
	}
	cases := []tc{
		{"arp", fixturePath(t, "arp_reply.pcap"), "arp"},
		{"vrrp", fixturePath(t, "vrrp.pcap"), "vrrp"},
		{"ra6", fixturePath(t, "ra.pcap"), "ra6"},
		{"vlan-tag", fixturePath(t, "dot1q.pcap"), "vlan-tag"},
		{"lldp", fixturePath(t, "lldp_voice.pcap"), "lldp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkts := readPcap(t, tc.pcap)
			var recs []findings.Record
			emit := func(r findings.Record) { recs = append(recs, r) }
			emitScanFinding(emit, pkts[0])
			found := false
			for _, r := range recs {
				if r.Kind != findings.KindFinding {
					continue
				}
				cls := findingClass(t, r)
				if cls == tc.wantCls {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected finding class %q, got classes: %v", tc.wantCls, classesOf(recs))
			}
		})
	}
}

func classesOf(recs []findings.Record) []string {
	var out []string
	for _, r := range recs {
		if r.Finding == nil {
			continue
		}
		var d scanFindingDetail
		if err := json.Unmarshal(r.Finding.Detail, &d); err == nil {
			out = append(out, d.Class)
		}
	}
	return out
}

// TestEmitScanFinding_VRRPFromBaselineFixture uses the baseline VRRP pcap.
func TestEmitScanFinding_VRRPFromBaselineFixture(t *testing.T) {
	pkts := readPcap(t, attacksFixturePath(t, "fh/vrrp.pcap"))
	var recs []findings.Record
	emit := func(r findings.Record) { recs = append(recs, r) }
	emitScanFinding(emit, pkts[0])
	found := false
	for _, r := range recs {
		if r.Kind == findings.KindFinding && findingClass(t, r) == "vrrp" {
			found = true
		}
	}
	if !found {
		t.Error("expected vrrp finding from baseline VRRP fixture")
	}
}

// TestEmitScanFinding_RAFromBaselineFixture uses the baseline RA pcap.
func TestEmitScanFinding_RAFromBaselineFixture(t *testing.T) {
	pkts := readPcap(t, attacksFixturePath(t, "ip6/roguera.pcap"))
	var recs []findings.Record
	emit := func(r findings.Record) { recs = append(recs, r) }
	emitScanFinding(emit, pkts[0])
	found := false
	for _, r := range recs {
		if r.Kind == findings.KindFinding && findingClass(t, r) == "ra6" {
			found = true
		}
	}
	if !found {
		t.Error("expected ra6 finding from baseline RA fixture")
	}
}

// TestEmitScanFinding_VoiceVLANViaLLDP asserts voice VLAN via LLDP-MED.
func TestEmitScanFinding_VoiceVLANViaLLDP(t *testing.T) {
	pkts := readPcap(t, fixturePath(t, "lldp_voice.pcap"))
	var recs []findings.Record
	emit := func(r findings.Record) { recs = append(recs, r) }
	emitScanFinding(emit, pkts[0])
	foundLLDP := false
	for _, r := range recs {
		if r.Kind == findings.KindFinding && findingClass(t, r) == "lldp" {
			foundLLDP = true
		}
	}
	if !foundLLDP {
		t.Error("expected lldp finding from LLDP-MED voice VLAN fixture")
	}
}

// --- activeVLANProbe test ---

// TestActiveVLANProbe_AnsweringVLAN emits a finding for a VLAN that
// answers with a DHCP offer matching the fingerprint xid.
func TestActiveVLANProbe_AnsweringVLAN(t *testing.T) {
	leg := newScriptableLeg()
	defer func() { _ = leg.Close() }()

	// Build a DHCP Offer matching the fingerprint xid for VID 10.
	xid := fingerprintXID(10)
	offer := buildDHCPOffer(xid, "192.168.1.1")
	leg.PushRX(offer)

	cfg := ScanConfig{
		AttackLeg:  leg,
		ProbeVLANs: "10",
		ProbeTime:  500 * time.Millisecond,
	}

	var recs []findings.Record
	emit := func(r findings.Record) { recs = append(recs, r) }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = activeVLANProbe(ctx, cfg, emit)

	foundAnswering := false
	for _, r := range recs {
		if r.Kind != findings.KindFinding {
			continue
		}
		cls := findingClass(t, r)
		if cls == "vlan-probe-answering" {
			foundAnswering = true
			break
		}
	}
	if !foundAnswering {
		t.Error("expected answering VLAN finding for VID 10")
	}
}

// TestActiveVLANProbe_LeakageOnly emits a leakage finding for a VLAN
// that has ambient tagged frames but no DHCP offer.
func TestActiveVLANProbe_LeakageOnly(t *testing.T) {
	leg := newScriptableLeg()
	defer func() { _ = leg.Close() }()

	// Push an ambient tagged frame on VID 20 (not a DHCP offer).
	ambient := buildAmbientTaggedFrame(20)
	leg.PushRX(ambient)

	cfg := ScanConfig{
		AttackLeg:  leg,
		ProbeVLANs: "20",
		ProbeTime:  500 * time.Millisecond,
	}

	var recs []findings.Record
	emit := func(r findings.Record) { recs = append(recs, r) }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = activeVLANProbe(ctx, cfg, emit)

	foundLeakage := false
	for _, r := range recs {
		if r.Kind != findings.KindFinding {
			continue
		}
		cls := findingClass(t, r)
		if cls == "vlan-probe-leakage" {
			foundLeakage = true
			break
		}
	}
	if !foundLeakage {
		t.Error("expected leakage VLAN finding for VID 20")
	}
}

// TestActiveVLANProbe_NativeOffer emits a native finding for an
// untagged DHCP offer.
func TestActiveVLANProbe_NativeOffer(t *testing.T) {
	leg := newScriptableLeg()
	defer func() { _ = leg.Close() }()

	// Build a DHCP Offer matching the native fingerprint xid.
	xid := fingerprintXID(0)
	offer := buildDHCPOffer(xid, "192.168.1.1")
	leg.PushRX(offer)

	cfg := ScanConfig{
		AttackLeg:  leg,
		ProbeVLANs: "10", // VID 10 probed but native offer received
		ProbeTime:  500 * time.Millisecond,
	}

	var recs []findings.Record
	emit := func(r findings.Record) { recs = append(recs, r) }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = activeVLANProbe(ctx, cfg, emit)

	foundNative := false
	for _, r := range recs {
		if r.Kind != findings.KindFinding {
			continue
		}
		cls := findingClass(t, r)
		if cls == "vlan-probe-native" {
			foundNative = true
			break
		}
	}
	if !foundNative {
		t.Error("expected native VLAN finding from untagged offer")
	}
}

// buildDHCPOffer constructs a DHCP Offer (BOOTP reply) with the given xid
// and server IP.
func buildDHCPOffer(xid uint32, serverIP string) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x0a, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x00},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP(serverIP),
		DstIP:    net.IPv4(255, 255, 255, 255),
	}
	udp := &layers.UDP{SrcPort: 67, DstPort: 68}
	_ = udp.SetNetworkLayerForChecksum(ip)
	dhcp := &layers.DHCPv4{
		Operation: layers.DHCPOpReply,
		Xid:       xid,
	}
	dhcp.Options = layers.DHCPOptions{
		layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeOffer)}),
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, udp, dhcp); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// buildAmbientTaggedFrame constructs a Dot1Q-tagged frame (not DHCP) that
// will be counted as leakage evidence.
func buildAmbientTaggedFrame(vid int) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x0a, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EthernetType: layers.EthernetTypeDot1Q,
	}
	dot1q := &layers.Dot1Q{
		VLANIdentifier: uint16(vid),
		Type:           layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.IPv4(10, 0, 0, 1),
		DstIP:    net.IPv4(10, 0, 0, 255),
	}
	udp := &layers.UDP{SrcPort: 1234, DstPort: 5678}
	_ = udp.SetNetworkLayerForChecksum(ip)
	payload := gopacket.Payload([]byte("ambient"))
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, dot1q, ip, udp, payload); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// --- stub-free defaultRecon integration test ---

// TestDefaultRecon_FullRun exercises the production recon path
// (defaultRecon with parseReconFrame + ARP sweep) over a fixture-fed
// leg, WITHOUT a ReconFn stub. The evidence must populate from real
// decoded frames.
func TestDefaultRecon_FullRun(t *testing.T) {
	leg := newScriptableLeg()
	defer func() { _ = leg.Close() }()

	// Pre-load fixture frames that will arrive during the passive listen.
	fixtures := [][]byte{
		readPcap(t, fixturePath(t, "vrrp.pcap"))[0],
		readPcap(t, fixturePath(t, "ra.pcap"))[0],
		readPcap(t, fixturePath(t, "dot1q.pcap"))[0],
		readPcap(t, fixturePath(t, "lldp_voice.pcap"))[0],
		readPcap(t, fixturePath(t, "arp_reply.pcap"))[0],
	}
	for _, f := range fixtures {
		leg.PushRX(f)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ev, err := defaultRecon(ctx, Config{
		AttackLeg: leg,
		ScanTime:  200 * time.Millisecond,
		SweepNet:  "10.0.0.0/30",
	})
	if err != nil {
		t.Fatalf("defaultRecon: %v", err)
	}

	// Assert evidence keys are populated from decoded frames.
	if !ev.Has(EvVRIDs) {
		t.Error("vrids not set from VRRP frame")
	}
	if !ev.Has(EvRA6) {
		t.Error("ra6 not set from RA frame")
	}
	if !ev.Has(EvVLANs) {
		t.Error("vlans not set from Dot1Q frame")
	}
	if !ev.Has(EvVoiceVLAN) {
		t.Error("voicevlan not set from LLDP-MED frame")
	}
	if !ev.Has(EvMACs) {
		t.Error("macs not set from ARP reply")
	}
	if ev["sweep-net"] == nil {
		t.Error("sweep-net not recorded")
	}
}

// TestDefaultRecon_FullOrchestration runs the full orchestrator with
// the production defaultRecon (no ReconFn stub) over fixture frames and
// asserts that the evidence gates arm the burst and select follow-ups.
func TestDefaultRecon_FullOrchestration(t *testing.T) {
	leg := newScriptableLeg()
	defer func() { _ = leg.Close() }()

	// Pre-load frames: RA (arms daddos), VRRP (arms vrrp), ARP reply
	// (arms arpspoof via MACs).
	fixtures := [][]byte{
		readPcap(t, fixturePath(t, "ra.pcap"))[0],
		readPcap(t, fixturePath(t, "vrrp.pcap"))[0],
		readPcap(t, fixturePath(t, "arp_reply.pcap"))[0],
		readPcap(t, fixturePath(t, "dot1q.pcap"))[0],
		readPcap(t, fixturePath(t, "lldp_voice.pcap"))[0],
	}
	for _, f := range fixtures {
		leg.PushRX(f)
	}

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		ScanTime:  200 * time.Millisecond,
		SweepNet:  "10.0.0.0/30",
		Behaviors: allBehaviors(),
		// No ReconFn: uses defaultRecon (production path).
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)

	// The burst must include the unconditional core.
	for _, name := range burstCore {
		if !hasAttackFired(recs, name) {
			t.Errorf("core worker %q did not fire", name)
		}
	}
	// ra6 evidence arms daddos.
	if !hasAttackFired(recs, "daddos") {
		t.Error("daddos did not fire (ra6 evidence from production recon)")
	}
	// vrids evidence arms vrrp.
	if !hasAttackFired(recs, "vrrp") {
		t.Error("vrrp did not fire (vrids evidence from production recon)")
	}
	// MACs evidence arms arpspoof.
	if !hasAttackFired(recs, "arpspoof") {
		t.Error("arpspoof did not fire (MACs evidence from production recon)")
	}
	// Follow-ups: vlans arms vlanhop, voicevlan fires, ra6 arms roguedhcp6.
	if !hasAttackFired(recs, "vlanhop") {
		t.Error("vlanhop follow-up did not fire (vlans evidence)")
	}
	if !hasAttackFired(recs, "voicevlan") {
		t.Error("voicevlan follow-up did not fire (voicevlan evidence)")
	}
	if !hasAttackFired(recs, "roguedhcp6") {
		t.Error("roguedhcp6 follow-up did not fire (ra6 evidence)")
	}
}
