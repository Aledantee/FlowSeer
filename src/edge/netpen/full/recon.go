package full

// recon.go implements the default recon phase: an ARP sweep of the
// sweep network plus a passive listen for control-plane frames (STP,
// CDP, DTP, VTP, LLDP, HSRP, VRRP, DHCP, ARP, IPv6 RA, MVRP). The
// passive listen and ARP sweep require AF_PACKET (Linux); on non-Linux
// the leg returns ErrUnsupportedPlatform and recon produces empty
// evidence, which still fires the burst's unconditional core (R3
// parity).
//
// The recon function is the Config.ReconFn seam: tests substitute a
// stub that returns canned Evidence, so the orchestration tests run
// without a real interface. The production path uses defaultRecon.

import (
	"context"
	"net"
	"sort"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	ownlayers "go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

// defaultRecon is the production recon function. It runs a passive listen
// bounded by ScanTime plus an ARP sweep of the sweep network, and
// returns the typed Evidence the gate surfaces consume.
//
// On non-Linux (no AF_PACKET), the passive listen observes nothing and
// the ARP sweep is a no-op; the returned Evidence is empty, which still
// fires the burst's unconditional core (R3 parity). On Linux, the listen
// and sweep populate the evidence keys.
func defaultRecon(ctx context.Context, cfg Config) (Evidence, error) {
	ev := Evidence{}

	// The passive listen is bounded by ScanTime. On non-Linux, the
	// leg's Receive returns no frames (ErrUnsupportedPlatform), so this
	// is a bounded wait. On Linux, it captures control-plane frames.
	scanTime := cfg.ScanTime
	if scanTime <= 0 {
		scanTime = 15 * time.Second
	}

	// Drain the attack leg for the scan window.
	listenCtx, listenCancel := context.WithTimeout(ctx, scanTime)
	defer listenCancel()
	if cfg.AttackLeg != nil {
		for frame := range cfg.AttackLeg.Receive(listenCtx) {
			if frame.Err != nil {
				break
			}
			parseReconFrame(ev, frame.Data)
		}
	}

	// ARP sweep: send ARP requests across the sweep network and collect
	// replies into ev["macs"]. On non-Linux the leg's Send returns
	// ErrUnsupportedPlatform; the sweep is a no-op. The sweep network
	// fallback chain: configured net, then 172.16.0.0/24 (baseline
	// full_cmd line 2041: srp on "172.16.0.0/24").
	sweepNet := cfg.SweepNet
	if sweepNet == "" {
		sweepNet = "172.16.0.0/24"
	}
	arpSweep(ctx, cfg, ev, sweepNet)

	// Record the sweep fallback in the evidence for the summary.
	ev["sweep-net"] = sweepNet

	return ev, nil
}

// arpSweep sends ARP requests for each host in the sweep network and
// collects replies into ev["macs"]. It mirrors the baseline's arpsweep
// (line 978) and full_cmd recon sweep (line 2041): srp with a bounded
// timeout. The sweep is rate-respecting (one request per host, no
// flooding) and ctx-cancelable. A /24 is the baseline cap; larger
// networks are clamped to 254 hosts.
func arpSweep(ctx context.Context, cfg Config, ev Evidence, sweepNet string) {
	if cfg.AttackLeg == nil {
		return
	}

	_, ipNet, err := net.ParseCIDR(sweepNet)
	if err != nil {
		return
	}

	// Clamp to /24 for the sweep (baseline parity).
	ones, bits := ipNet.Mask.Size()
	if bits-ones > 8 {
		ipNet.Mask = net.CIDRMask(24, 32)
	}

	hosts := hostIPs(ipNet)
	if len(hosts) > 254 {
		hosts = hosts[:254]
	}

	// Collect resolved MACs (deduped).
	macSet := make(map[string]struct{})
	for _, m := range ev.MACs() {
		macSet[m] = struct{}{}
	}

	// Send ARP requests for each host, bounded by a 3s deadline
	// (baseline full_cmd: srp timeout=3).
	sweepDeadline := time.Now().Add(3 * time.Second)
	for _, ip := range hosts {
		if ctx.Err() != nil || time.Now().After(sweepDeadline) {
			break
		}
		pkt := buildARPRequest(ip)
		if pkt == nil {
			continue
		}
		_ = cfg.AttackLeg.Send(ctx, pkt)
	}

	// Listen for ARP replies for a bounded window (baseline: the srp
	// call returns when its timeout elapses).
	replyCtx, replyCancel := context.WithTimeout(ctx, 2*time.Second)
	defer replyCancel()
	for frame := range cfg.AttackLeg.Receive(replyCtx) {
		if frame.Err != nil {
			break
		}
		mac := extractARPReplyMAC(frame.Data)
		if mac != "" {
			macSet[mac] = struct{}{}
		}
	}

	// Write back deduped, sorted MACs.
	macs := make([]string, 0, len(macSet))
	for m := range macSet {
		macs = append(macs, m)
	}
	sort.Strings(macs)
	if len(macs) > 0 {
		ev[EvMACs] = macs
	}
}

// hostIPs returns the list of usable host IPs in ipNet (excluding
// network and broadcast addresses).
func hostIPs(ipNet *net.IPNet) []net.IP {
	var out []net.IP
	for ip := ipNet.IP.Mask(ipNet.Mask); ipNet.Contains(ip); incIP(ip) {
		dup := make(net.IP, len(ip))
		copy(dup, ip)
		out = append(out, dup)
	}
	// Remove the first (network) and last (broadcast) entries.
	if len(out) > 2 {
		out = out[1 : len(out)-1]
	}
	return out
}

// incIP increments an IP address in place.
func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] > 0 {
			break
		}
	}
}

// buildARPRequest constructs an Ethernet/ARP request for the given target
// IP. The source MAC and IP are zero (the kernel/leg fills them on Linux;
// on non-Linux Send is a no-op).
func buildARPRequest(targetIP net.IP) []byte {
	if targetIP == nil || targetIP.To4() == nil {
		return nil
	}
	ip4 := targetIP.To4()

	eth := &layers.Ethernet{
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   []byte{0, 0, 0, 0, 0, 0},
		SourceProtAddress: []byte{0, 0, 0, 0},
		DstHwAddress:      []byte{0, 0, 0, 0, 0, 0},
		DstProtAddress:    ip4,
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, arp); err != nil {
		return nil
	}
	return buf.Bytes()
}

// extractARPReplyMAC returns the source hardware MAC from an ARP reply
// frame, or "" if the frame is not an ARP reply.
func extractARPReplyMAC(data []byte) string {
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
	arpLayer := pkt.Layer(layers.LayerTypeARP)
	if arpLayer == nil {
		return ""
	}
	arp, ok := arpLayer.(*layers.ARP)
	if !ok || arp.Operation != layers.ARPReply {
		return ""
	}
	return net.HardwareAddr(arp.SourceHwAddress).String()
}

// parseReconFrame inspects one captured frame and updates the evidence
// map. It decodes the frame with gopacket and the owned layer decoders,
// setting evidence keys only on confirmed protocol signatures. The
// parser is conservative: it sets evidence keys only for protocols the
// gate surfaces consume (gates.go), never on heuristics.
func parseReconFrame(ev Evidence, data []byte) {
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)

	// Walk all Dot1Q layers to collect VLAN tags.
	vlanSet := make(map[int]struct{})
	for _, l := range pkt.Layers() {
		if q, ok := l.(*layers.Dot1Q); ok {
			vlanSet[int(q.VLANIdentifier)] = struct{}{}
		}
	}
	if len(vlanSet) > 0 {
		vlans := ev.VLANs()
		for v := range vlanSet {
			vlans = appendUniqueInt(vlans, v)
		}
		ev[EvVLANs] = vlans
	}

	// ICMPv6 Router Advertisement → ra6 evidence.
	if pkt.Layer(layers.LayerTypeICMPv6RouterAdvertisement) != nil {
		ev[EvRA6] = true
	}

	// VRRP → vrids evidence.
	if vrrpLayer := pkt.Layer(layers.LayerTypeVRRP); vrrpLayer != nil {
		if v, ok := vrrpLayer.(*layers.VRRPv2); ok {
			vrids := ev.VRIDs()
			vrids = appendUniqueInt(vrids, int(v.VirtualRtrID))
			ev[EvVRIDs] = vrids
		}
	}

	// VTP summary → vtp-domain + vtp-rev.
	if vtpLayer := pkt.Layer(ownlayers.LayerTypeVTP); vtpLayer != nil {
		if v, ok := vtpLayer.(*ownlayers.VTP); ok {
			if v.Domain != "" {
				ev[EvVTPDomain] = v.Domain
			}
			ev[EvVTPRev] = int(v.Revision)
		}
	}

	// MVRP → mvrp evidence.
	if pkt.Layer(ownlayers.LayerTypeMVRP) != nil {
		ev[EvMVRP] = true
	}

	// CDP voice VLAN indicator → voicevlan.
	if cdpInfoLayer := pkt.Layer(layers.LayerTypeCiscoDiscoveryInfo); cdpInfoLayer != nil {
		if info, ok := cdpInfoLayer.(*layers.CiscoDiscoveryInfo); ok {
			if vvid := cdpVoiceVLAN(info); vvid != 0 {
				ev[EvVoiceVLAN] = vvid
			}
		}
	}

	// LLDP-MED network policy voice VLAN → voicevlan.
	if lldpInfoLayer := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo); lldpInfoLayer != nil {
		if info, ok := lldpInfoLayer.(*layers.LinkLayerDiscoveryInfo); ok {
			if vvid := lldpVoiceVLAN(info); vvid != 0 {
				ev[EvVoiceVLAN] = vvid
			}
		}
	}

	// ARP reply → macs evidence (source hardware address).
	if arpLayer := pkt.Layer(layers.LayerTypeARP); arpLayer != nil {
		if arp, ok := arpLayer.(*layers.ARP); ok && arp.Operation == layers.ARPReply {
			mac := net.HardwareAddr(arp.SourceHwAddress).String()
			if mac != "" {
				macs := ev.MACs()
				macs = appendUniqueStr(macs, mac)
				ev[EvMACs] = macs
			}
		}
	}
}

// cdpVoiceVLAN extracts the voice VLAN ID from a CDP VLAN Reply TLV. The
// baseline (scan_cmd line ~385) reads TLV type 0x000E. The fork's
// CiscoDiscoveryInfo decodes it into the VLANReply field
// (CDPVLANDialogue with a VLAN uint16).
func cdpVoiceVLAN(info *layers.CiscoDiscoveryInfo) int {
	if info.VLANReply.VLAN != 0 {
		return int(info.VLANReply.VLAN)
	}
	return 0
}

// lldpVoiceVLAN extracts the voice VLAN ID from an LLDP-MED network
// policy TLV for the voice application type. The fork's
// LinkLayerDiscoveryInfo.DecodeMedia() method parses TR-41 org-specific
// TLVs into LLDPInfoMedia, which carries the NetworkPolicy struct.
func lldpVoiceVLAN(info *layers.LinkLayerDiscoveryInfo) int {
	media, err := info.DecodeMedia()
	if err != nil {
		return 0
	}
	np := media.NetworkPolicy
	if np.ApplicationType == layers.LLDPAppTypeVoice && np.Tagged && np.VLANId != 0 {
		return int(np.VLANId)
	}
	return 0
}

// appendUniqueInt appends v to s if not already present, returning the
// (possibly grown) slice.
func appendUniqueInt(s []int, v int) []int {
	for _, e := range s {
		if e == v {
			return s
		}
	}
	return append(s, v)
}

// appendUniqueStr appends v to s if not already present.
func appendUniqueStr(s []string, v string) []string {
	for _, e := range s {
		if e == v {
			return s
		}
	}
	return append(s, v)
}
