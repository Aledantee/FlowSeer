package full

// scan.go implements the `scan` command: passive detect (STP/DTP/CDP/
// VTP/LLDP/HSRP/VRRP/DHCP/ARP issue classes from observed frames) plus
// active VLAN probing (tagged + untagged) unless --no-probe. It uses
// dual-segment observe (attack leg + watch leg) and emits findings per
// the baseline's scan finding classes. The catalog class is
// non-destructive — scan changes no device or neighbor state.
//
// It emits findings directly through the orchestrator's record
// collection, not through the runner's behavior dispatch.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	ownlayers "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// ErrCodeScan is the wire identity for a scan-level failure.
var ErrCodeScan = errs.NewCode("netpen/scan")

// ScanConfig configures a [Scan] run. Its callback state must not be mutated
// concurrently with Run.
type ScanConfig struct {
	// AttackLeg is the attack interface. Required.
	AttackLeg link.Leg
	// WatchLeg is the optional watch leg for dual-segment observe.
	WatchLeg link.Leg
	// WatchLegNamed is the watch interface name (-w). Non-empty + nil
	// WatchLeg fails fast.
	WatchLegNamed string
	// AttackLegName is the attack interface name (for the meta record).
	AttackLegName string
	// Time bounds each leg's passive listen. Non-positive values use
	// 35 seconds. The attack and watch legs are observed sequentially.
	Time time.Duration
	// NoProbe skips active VLAN probing.
	NoProbe bool
	// ProbeVLANs accepts comma-separated IDs and ranges, such as "10,20-25".
	// Only IDs 1 through 4094 are used. Invalid entries are ignored; if no
	// valid IDs remain, probing uses the default set of common VLAN IDs.
	ProbeVLANs string
	// ProbeTime bounds the active probe window. Non-positive values use
	// 6 seconds.
	ProbeTime time.Duration
	// ScanFn is the scan function. Tests substitute a stub. When nil,
	// the orchestrator uses defaultScanRun. The callback must finish all
	// calls to emit before returning.
	ScanFn func(ctx context.Context, cfg ScanConfig, emit func(findings.Record)) error
}

// Scan collects passive observations and optional VLAN probe results.
// Construct it with [NewScan] and call Run once. The zero value is unusable.
// Records may be read concurrently with Run; the record channel must be
// drained during Run so a full buffer does not block progress.
type Scan struct {
	cfg ScanConfig
	recorder
}

// NewScan constructs the scan orchestrator with a live record channel.
func NewScan(cfg ScanConfig) *Scan {
	return &Scan{cfg: cfg, recorder: newRecorder()}
}

// Run executes the scan. It passively listens on the attack leg (and the
// watch leg when attached) for the configured time, then optionally
// probes candidate VLANs. Findings are emitted for each detected
// protocol issue class.
//
// A named-but-absent watch leg fails fast (deviation from the baseline's
// silent degradation). This matches the `full` orchestrator's contract.
func (s *Scan) Run(ctx context.Context) error {
	defer s.closeRecords()
	if s.cfg.WatchLegNamed != "" && s.cfg.WatchLeg == nil {
		err := missingWatchLegErr(s.cfg.WatchLegNamed)
		s.appendRecord(errRecord(err, "scan", ""))
		return err
	}

	s.appendRecord(progress("scan", "", "listen", "passive detect started"))

	scanFn := s.cfg.ScanFn
	if scanFn == nil {
		scanFn = defaultScanRun
	}

	err := scanFn(ctx, s.cfg, s.appendRecord)

	s.appendRecord(progress("scan", "", "report", "scan complete"))
	if err != nil {
		s.appendRecord(errRecord(err, "scan", ""))
		return err
	}
	return nil
}

// defaultScanRun is the production scan: passive listen on the attack
// leg (and watch leg when present) for the configured time, then optional
// active VLAN probing. On non-Linux the listen observes nothing (no
// AF_PACKET); the scan completes with no findings, which is the correct
// behavior for a non-Linux dev host.
func defaultScanRun(ctx context.Context, cfg ScanConfig, emit func(findings.Record)) error {
	listenTime := cfg.Time
	if listenTime <= 0 {
		listenTime = 35 * time.Second
	}

	// Passive listen on the attack leg.
	listenCtx, listenCancel := context.WithTimeout(ctx, listenTime)
	defer listenCancel()

	if cfg.AttackLeg != nil {
		for frame := range cfg.AttackLeg.Receive(listenCtx) {
			if frame.Err != nil {
				break
			}
			emitScanFinding(emit, frame.Data)
		}
	}

	// Dual-segment: also listen on the watch leg if present.
	if cfg.WatchLeg != nil {
		watchCtx, watchCancel := context.WithTimeout(ctx, listenTime)
		defer watchCancel()
		for frame := range cfg.WatchLeg.Receive(watchCtx) {
			if frame.Err != nil {
				break
			}
			emitScanFinding(emit, frame.Data)
		}
	}

	// Active VLAN probing (unless --no-probe).
	if !cfg.NoProbe {
		probeTime := cfg.ProbeTime
		if probeTime <= 0 {
			probeTime = 6 * time.Second
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, probeTime)
		defer probeCancel()
		_ = activeVLANProbe(probeCtx, cfg, emit)
	}

	return nil
}

// scanFindingDetail is the JSON detail payload for a scan finding.
type scanFindingDetail struct {
	Class  string `json:"class"`
	Detail string `json:"detail,omitempty"`
	VLAN   int    `json:"vlan,omitempty"`
	Src    string `json:"src,omitempty"`
	Dst    string `json:"dst,omitempty"`
}

// emitScanFinding inspects one captured frame and emits a findings
// record for each detected protocol issue class. The detector covers
// the baseline's scan classes (scan_cmd, lines 314-660): STP, DTP, CDP,
// VTP, LLDP, HSRP, VRRP, DHCP, ARP conflicts, IPv6 RA, MVRP, MACsec,
// fragmented ND, and VLAN tags. One finding per (frame, class hit).
// Conservative: no speculative emission.
func emitScanFinding(emit func(findings.Record), data []byte) {
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)

	// STP BPDUs (baseline: seen["stp"]).
	if pkt.Layer(layers.LayerTypeSTP) != nil {
		emitScanClass(emit, "stp", "STP BPDU visible — root takeover feasible")
	}

	// DTP trunk-negotiation frames (baseline: seen["dtp"]).
	if dtpLayer := pkt.Layer(ownlayers.LayerTypeDTP); dtpLayer != nil {
		emitScanClass(emit, "dtp", "DTP trunk-negotiation frames — VLAN hopping surface")
	}

	// CDP disclosure (baseline: seen["cdp"]).
	if cdpLayer := pkt.Layer(layers.LayerTypeCiscoDiscoveryInfo); cdpLayer != nil {
		emitScanClass(emit, "cdp", "CDP disclosure — topology information leak")
	}

	// VTP domain (baseline: seen["vtp"]).
	if vtpLayer := pkt.Layer(ownlayers.LayerTypeVTP); vtpLayer != nil {
		if v, ok := vtpLayer.(*ownlayers.VTP); ok && v.Domain != "" {
			emitScanClass(emit, "vtp", fmt.Sprintf("VTP domain %q — VTP injection can wipe VLAN db", v.Domain))
		}
	}

	// LLDP topology disclosure (baseline: seen["lldp"]).
	if pkt.Layer(layers.LayerTypeLinkLayerDiscovery) != nil {
		emitScanClass(emit, "lldp", "LLDP topology disclosure")
	}

	// HSRP (baseline: seen["hsrp"]).
	if hsrpLayer := pkt.Layer(ownlayers.LayerTypeHSRP); hsrpLayer != nil {
		if h, ok := hsrpLayer.(*ownlayers.HSRP); ok {
			emitScanClass(emit, "hsrp", fmt.Sprintf("HSRP group %d prio %d — unauthenticated election", h.Group, h.Priority))
		}
	}

	// VRRP (baseline: seen["vrrp"]).
	if vrrpLayer := pkt.Layer(layers.LayerTypeVRRP); vrrpLayer != nil {
		if v, ok := vrrpLayer.(*layers.VRRPv2); ok {
			vips := ""
			if len(v.IPAddress) > 0 {
				vips = v.IPAddress[0].String()
			}
			emitScanClass(emit, "vrrp", fmt.Sprintf("VRRP vrid %d prio %d vip %s — takeover feasible", v.VirtualRtrID, v.Priority, vips))
		}
	}

	// DHCP server offering (baseline: seen["dhcp_srv"]).
	if dhcpLayer := pkt.Layer(layers.LayerTypeDHCPv4); dhcpLayer != nil {
		if d, ok := dhcpLayer.(*layers.DHCPv4); ok && d.Operation == layers.DHCPOpReply {
			src := ""
			if ipLayer := pkt.Layer(layers.LayerTypeIPv4); ipLayer != nil {
				if ip, ok := ipLayer.(*layers.IPv4); ok {
					src = ip.SrcIP.String()
				}
			}
			emitScanClass(emit, "dhcp", fmt.Sprintf("DHCP server offering from %s", src))
		}
	}

	// ARP conflict detection (baseline: seen["conflicts"]).
	if arpLayer := pkt.Layer(layers.LayerTypeARP); arpLayer != nil {
		if arp, ok := arpLayer.(*layers.ARP); ok && arp.Operation == layers.ARPReply {
			src := net.IP(arp.SourceProtAddress).String()
			mac := net.HardwareAddr(arp.SourceHwAddress).String()
			emitScanClass(emit, "arp", fmt.Sprintf("ARP host: %s is-at %s", src, mac))
		}
	}

	// IPv6 Router Advertisement (baseline: seen["ra6"]).
	if pkt.Layer(layers.LayerTypeICMPv6RouterAdvertisement) != nil {
		emitScanClass(emit, "ra6", "IPv6 Router Advertisement — rogue RA / RA-Guard surface")
	}

	// MVRP/GVRP dynamic-VLAN traffic (baseline: seen["mvrp"]).
	if pkt.Layer(ownlayers.LayerTypeMVRP) != nil {
		emitScanClass(emit, "mvrp", "MVRP/GVRP dynamic-VLAN traffic — registration open")
	}

	// MACsec-secured frames (baseline: seen["macsec"]).
	if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
		if eth, ok := ethLayer.(*layers.Ethernet); ok && eth.EthernetType == 0x88E5 {
			emitScanClass(emit, "macsec", "MACsec-secured frames (802.1AE) — check fail-open policy")
		}
	}

	// Fragmented ND (baseline: seen["frag_nd"]).
	if ip6Layer := pkt.Layer(layers.LayerTypeIPv6); ip6Layer != nil {
		if ip6, ok := ip6Layer.(*layers.IPv6); ok && ip6.NextHeader == 44 {
			emitScanClass(emit, "frag-nd", "fragmented ND frame — RFC 6980 drop / RA-Guard confusion surface")
		}
	}

	// VLAN tags present on the wire (baseline: seen["vlans"]).
	for _, l := range pkt.Layers() {
		if q, ok := l.(*layers.Dot1Q); ok {
			emitScanClassVLAN(emit, "vlan-tag", fmt.Sprintf("802.1Q tag VLAN %d on the wire", q.VLANIdentifier), int(q.VLANIdentifier))
			break // one finding per frame for VLAN tags
		}
	}
}

// emitScanClass emits a typed finding record for a scan detection class.
func emitScanClass(emit func(findings.Record), class, detail string) {
	if emit == nil {
		return
	}
	d, _ := json.Marshal(scanFindingDetail{Class: class, Detail: detail})
	r := findings.NewRecord(findings.KindFinding)
	r.Attack = "scan"
	r.Finding = &findings.Finding{Module: "scan", Detail: d}
	emit(r)
}

// emitScanClassVLAN emits a typed finding record for a VLAN-tagged frame.
func emitScanClassVLAN(emit func(findings.Record), class, detail string, vlan int) {
	if emit == nil {
		return
	}
	d, _ := json.Marshal(scanFindingDetail{Class: class, Detail: detail, VLAN: vlan})
	r := findings.NewRecord(findings.KindFinding)
	r.Attack = "scan"
	r.Finding = &findings.Finding{Module: "scan", Detail: d}
	emit(r)
}

// defaultVLANProbes is the candidate set for active VLAN probing when
// nothing leaked passively (baseline line 158: DEFAULT_VLAN_PROBES).
var defaultVLANProbes = []int{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 20, 30, 40, 50, 99,
	100, 110, 120, 150, 200, 250, 300, 400, 500, 666,
	999, 1000, 1234, 2000, 3000, 4000, 4094,
}

// activeVLANProbe sends tagged DHCP Discovers + Router Solicitations per
// candidate VLAN and one untagged Discover, then listens for replies. It
// mirrors the baseline's active_vlan_probe (line 673): per-VID tagged
// DHCP Discover with a fingerprint xid + tagged RS, one untagged native
// Discover, and a bounded listen window for offers and ambient frames.
//
// Findings distinguish answering-VLAN (offer received — bidirectional)
// from leakage-only (ambient frames observed on that tag), exactly as
// the baseline distinguishes (lines 540-560).
func activeVLANProbe(ctx context.Context, cfg ScanConfig, emit func(findings.Record)) error {
	if cfg.AttackLeg == nil {
		return nil
	}

	// Determine candidate VLANs.
	cands := parseCandidateVLANs(cfg.ProbeVLANs)
	if len(cands) == 0 {
		cands = defaultVLANProbes
	}

	probeTime := cfg.ProbeTime
	if probeTime <= 0 {
		probeTime = 6 * time.Second
	}

	// Per-VID fingerprint xids: xid -> VID mapping.
	xids := make(map[uint32]int) // tagged xid -> VID
	nativeXid := uint32(0)       // untagged native xid

	// Send tagged DHCP Discovers + RS per candidate VID.
	for _, vid := range cands {
		if ctx.Err() != nil {
			return nil
		}
		xid := fingerprintXID(vid)
		xids[xid] = vid
		pkt := buildTaggedDHCPDiscover(vid, xid)
		if pkt != nil {
			_ = cfg.AttackLeg.Send(ctx, pkt)
		}
		rs := buildTaggedRS(vid)
		if rs != nil {
			_ = cfg.AttackLeg.Send(ctx, rs)
		}
	}

	// Send one untagged native Discover.
	nativeXid = fingerprintXID(0)
	nativePkt := buildUntaggedDHCPDiscover(nativeXid)
	if nativePkt != nil {
		_ = cfg.AttackLeg.Send(ctx, nativePkt)
	}

	// Listen for replies (offers matching our xids + ambient tagged frames).
	stats := make(map[int]*vlanProbeStat)
	for _, vid := range cands {
		stats[vid] = &vlanProbeStat{}
	}
	nativeOffer := ""

	listenCtx, listenCancel := context.WithTimeout(ctx, probeTime)
	defer listenCancel()
	for frame := range cfg.AttackLeg.Receive(listenCtx) {
		if frame.Err != nil {
			break
		}
		processProbeReply(frame.Data, xids, nativeXid, stats, &nativeOffer, emit)
	}

	// Emit per-VLAN findings: answering (offer received) vs leakage-only.
	for _, vid := range cands {
		s := stats[vid]
		if s == nil {
			continue
		}
		if s.offer != "" {
			emitVLANProbeFinding(emit, vid, "answering", fmt.Sprintf("VLAN %d DHCP answered by %s — bidirectional reachability confirmed", vid, s.offer))
		} else if s.frames > 0 || s.ra > 0 {
			emitVLANProbeFinding(emit, vid, "leakage", fmt.Sprintf("VLAN %d leaking: %d tagged frame(s) + %d RA(s) — bidirectional unproven", vid, s.frames, s.ra))
		}
	}

	// Native VLAN finding.
	if nativeOffer != "" {
		emitVLANProbeFinding(emit, 0, "native", fmt.Sprintf("untagged/native VLAN answered DHCP (%s)", nativeOffer))
	}

	return nil
}

// vlanProbeStat tracks per-VLAN probe results.
type vlanProbeStat struct {
	offer  string // DHCP server IP if an offer was received
	frames int    // ambient tagged frames seen on this VID
	ra     int    // RAs seen on this VID
}

func parseCandidateVLANs(spec string) []int {
	var out []int
	for _, part := range strings.Split(spec, ",") {
		low, high, isRange := strings.Cut(strings.TrimSpace(part), "-")
		lo, err := strconv.Atoi(low)
		if err != nil {
			continue
		}
		hi := lo
		if isRange {
			hi, err = strconv.Atoi(high)
			if err != nil {
				continue
			}
		}
		for v := max(lo, 1); v <= min(hi, 4094); v++ {
			out = append(out, v)
		}
	}
	return out
}

// fingerprintXID generates a deterministic xid for a VID so we can match
// replies. The baseline uses random xids mapped per-VID (line 707); we
// use a VID-derived value so tests can predict the mapping.
func fingerprintXID(vid int) uint32 {
	if vid == 0 {
		return 0x4E455400 // "NET\0" — native fingerprint
	}
	return 0x564C0000 | uint32(vid&0xFFF) // "VL" prefix + VID
}

// buildTaggedDHCPDiscover constructs an 802.1Q-tagged DHCP Discover frame.
func buildTaggedDHCPDiscover(vid int, xid uint32) []byte {
	eth := &layers.Ethernet{
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		SrcMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x00},
		EthernetType: layers.EthernetTypeDot1Q,
	}
	dot1q := &layers.Dot1Q{
		VLANIdentifier: uint16(vid),
		Type:           layers.EthernetTypeIPv4,
	}
	return buildDHCPDiscover(eth, dot1q, xid)
}

// buildUntaggedDHCPDiscover constructs an untagged DHCP Discover frame.
func buildUntaggedDHCPDiscover(xid uint32) []byte {
	eth := &layers.Ethernet{
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		SrcMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x00},
		EthernetType: layers.EthernetTypeIPv4,
	}
	return buildDHCPDiscover(eth, nil, xid)
}

// buildDHCPDiscover builds the IP/UDP/BOOTP/DHCP layers and serializes
// the full frame.
func buildDHCPDiscover(eth *layers.Ethernet, dot1q *layers.Dot1Q, xid uint32) []byte {
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		Length:   0,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.IPv4(0, 0, 0, 0),
		DstIP:    net.IPv4(255, 255, 255, 255),
	}
	udp := &layers.UDP{
		SrcPort: 68,
		DstPort: 67,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	bootp := &layers.DHCPv4{
		Operation: layers.DHCPOpRequest,
		Xid:       xid,
	}
	bootp.Options = layers.DHCPOptions{
		layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeDiscover)}),
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	var layersList []gopacket.SerializableLayer
	layersList = append(layersList, eth)
	if dot1q != nil {
		layersList = append(layersList, dot1q)
	}
	layersList = append(layersList, ip, udp, bootp)
	if err := gopacket.SerializeLayers(buf, opts, layersList...); err != nil {
		return nil
	}
	return buf.Bytes()
}

// buildTaggedRS constructs an 802.1Q-tagged IPv6 Router Solicitation.
func buildTaggedRS(vid int) []byte {
	eth := &layers.Ethernet{
		DstMAC:       net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0x02},
		SrcMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x00},
		EthernetType: layers.EthernetTypeDot1Q,
	}
	dot1q := &layers.Dot1Q{
		VLANIdentifier: uint16(vid),
		Type:           layers.EthernetTypeIPv6,
	}
	ip6 := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      net.ParseIP("fe80::"),
		DstIP:      net.ParseIP("ff02::2"),
	}
	rs := &layers.ICMPv6RouterSolicitation{}
	icmp := &layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeRouterSolicitation, 0)}
	_ = icmp.SetNetworkLayerForChecksum(ip6)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, dot1q, ip6, icmp, rs); err != nil {
		return nil
	}
	return buf.Bytes()
}

// processProbeReply inspects a captured frame during the probe window and
// updates the per-VLAN stats. If a DHCP Offer matches one of our xids,
// the corresponding VLAN is marked as answering. Tagged ambient frames
// and RAs are counted as leakage evidence.
func processProbeReply(
	data []byte,
	xids map[uint32]int,
	nativeXid uint32,
	stats map[int]*vlanProbeStat,
	nativeOffer *string,
	_ func(findings.Record),
) {
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)

	// Check for DHCP Offer matching our xids.
	if dhcpLayer := pkt.Layer(layers.LayerTypeDHCPv4); dhcpLayer != nil {
		if d, ok := dhcpLayer.(*layers.DHCPv4); ok && d.Operation == layers.DHCPOpReply {
			if vid, matches := xids[d.Xid]; matches {
				src := ""
				if ipLayer := pkt.Layer(layers.LayerTypeIPv4); ipLayer != nil {
					if ip, ok := ipLayer.(*layers.IPv4); ok {
						src = ip.SrcIP.String()
					}
				}
				if s := stats[vid]; s != nil {
					s.offer = src
				}
				return
			}
			if d.Xid == nativeXid && nativeOffer != nil {
				src := ""
				if ipLayer := pkt.Layer(layers.LayerTypeIPv4); ipLayer != nil {
					if ip, ok := ipLayer.(*layers.IPv4); ok {
						src = ip.SrcIP.String()
					}
				}
				*nativeOffer = src
				return
			}
		}
	}

	// Check for tagged ambient frames / RAs (leakage evidence).
	var vid int
	for _, l := range pkt.Layers() {
		if q, ok := l.(*layers.Dot1Q); ok {
			vid = int(q.VLANIdentifier)
			break
		}
	}
	if vid == 0 {
		return
	}
	s := stats[vid]
	if s == nil {
		return
	}
	if pkt.Layer(layers.LayerTypeICMPv6RouterAdvertisement) != nil {
		s.ra++
	} else {
		s.frames++
	}
}

// emitVLANProbeFinding emits a typed finding for a VLAN probe result.
func emitVLANProbeFinding(emit func(findings.Record), vid int, result, detail string) {
	if emit == nil {
		return
	}
	d, _ := json.Marshal(scanFindingDetail{
		Class:  fmt.Sprintf("vlan-probe-%s", result),
		Detail: detail,
		VLAN:   vid,
	})
	r := findings.NewRecord(findings.KindFinding)
	r.Attack = "scan"
	r.Finding = &findings.Finding{Module: "scan", Detail: d}
	emit(r)
}
