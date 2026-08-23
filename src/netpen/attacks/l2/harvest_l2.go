//go:build ignore

// harvest_l2.go generates L2 attack reference fixtures into
// attacks/testdata/l2/*.pcap by crafting the exact byte sequences the
// baseline (l2l3-audit) produces, matching the construction in
// layers/harvest.py. This is the KTD14 fixture factory for the L2 attack
// behaviors.
//
// Run from the attacks/l2 directory:
//
//	go run harvest_l2.go
//
// Fully offline: no sockets, no scapy. Writes pcaps via pcapgo.
package main

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
)

const srcMAC = "00:11:22:33:44:55"

var (
	ciscoOUI     = []byte{0x00, 0x00, 0x0C}
	dtpDst       = mustMAC("01:00:0c:cc:cc:cc")
	vtpDst       = mustMAC("01:00:0c:cc:cc:cc")
	mvrpDst      = mustMAC("01:80:c2:00:00:21")
	lacpDst      = mustMAC("01:80:c2:00:00:02")
	pagpDst      = mustMAC("01:00:0c:cc:cc:cc")
	stpDst       = mustMAC("01:80:c2:00:00:00")
	partnerMAC   = mustMAC("00:aa:bb:cc:dd:ee")
	broadcastMAC = mustMAC("ff:ff:ff:ff:ff:ff")
)

func mustMAC(s string) net.HardwareAddr {
	m, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

func srcBytes() net.HardwareAddr { return mustMAC(srcMAC) }

func main() {
	outDir := "../testdata/l2"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}

	// DTP: desirable + trunk (same as layers fixture, but attack-scoped).
	writePcap(filepath.Join(outDir, "dtp.pcap"),
		dtpFrame(0x03),
		dtpFrame(0x81),
	)

	// DTP restore: access-port restore frame (status=0x02).
	writePcap(filepath.Join(outDir, "dtp_restore.pcap"),
		dtpFrame(0x02),
	)

	// Double-tag: QinQ frame hopping VLAN 10 → VLAN 20.
	writePcap(filepath.Join(outDir, "doubletag.pcap"),
		doubleTagFrame(10, 20),
	)

	// VLAN enum: a trunk with 802.1Q frames for VLANs 10, 20, 30, 40.
	writePcap(filepath.Join(outDir, "vlanenum.pcap"),
		dot1qFrame(10, []byte("data-vlan10")),
		dot1qFrame(20, []byte("voice-vlan20")),
		dot1qFrame(30, []byte("mgmt-vlan30")),
		dot1qFrame(40, []byte("guest-vlan40")),
	)

	// VLAN hop: QinQ persistent frame.
	writePcap(filepath.Join(outDir, "vlanhop.pcap"),
		doubleTagFrame(10, 20),
	)

	// Voice VLAN: LLDP-MED frame (simplified).
	writePcap(filepath.Join(outDir, "voicevlan.pcap"),
		lldpVoiceFrame(),
	)

	// STP root: superior BPDU (root priority 0, lower than default 32768).
	writePcap(filepath.Join(outDir, "stproot.pcap"),
		stpBPDU(0, 0, 0, 6, 2, 20, 15),
		stpBPDU(0, 0, 0, 6, 2, 20, 15),
		stpBPDU(0, 0, 0, 6, 2, 20, 15),
	)

	// CAM flood: many random-source frames to overflow the CAM table.
	writePcap(filepath.Join(outDir, "camflood.pcap"),
		camFloodFrame(1),
		camFloodFrame(2),
		camFloodFrame(3),
		camFloodFrame(4),
		camFloodFrame(5),
	)

	// VTP: summary + subset + request (same as layers fixture).
	writePcap(filepath.Join(outDir, "vtp.pcap"),
		vtpSummary("LABDOMAIN", 42),
		vtpSubset("LABDOMAIN", 42, []vlanEntry{{10, "data"}, {20, "voice"}}),
		vtpRequest("LABDOMAIN"),
	)

	// VTP no-domain: a single summary with empty domain (edge case).
	writePcap(filepath.Join(outDir, "vtp_nodomain.pcap"),
		vtpSummary("", 0),
	)

	// MVRP: JoinIn for VLAN 10 and VLAN 20 (registration flood).
	writePcap(filepath.Join(outDir, "mvrp.pcap"),
		mvrpFrame(10, true),
		mvrpFrame(20, true),
	)

	// Port steal: ARP spoof frame.
	writePcap(filepath.Join(outDir, "portsteal.pcap"),
		portStealARP(10),
		portStealARP(11),
		portStealARP(12),
	)

	// EtherChannel: LACP + PAgP frames.
	writePcap(filepath.Join(outDir, "etherchannel.pcap"),
		lacpFrame(),
		pagpFrame(),
	)

	fmt.Println("OK: generated L2 attack fixtures into", outDir)
}

// ---------------------------------------------------------------------------
// DTP
// ---------------------------------------------------------------------------

func dtpFrame(mode uint8) []byte {
	neigh := srcBytes()
	body := []byte{0x01} // version
	body = append(body, dtpTLV(0x0001, []byte{0x00})...)
	body = append(body, dtpTLV(0x0002, []byte{mode})...)
	body = append(body, dtpTLV(0x0003, []byte{0xa5})...)
	body = append(body, dtpTLV(0x0004, neigh)...)

	return dot3SNAP(dtpDst, srcBytes(), 0x2004, body)
}

func dtpTLV(t uint16, v []byte) []byte {
	out := make([]byte, 4+len(v))
	binary.BigEndian.PutUint16(out[0:2], t)
	binary.BigEndian.PutUint16(out[2:4], uint16(4+len(v)))
	copy(out[4:], v)
	return out
}

// ---------------------------------------------------------------------------
// Double-tag (QinQ)
// ---------------------------------------------------------------------------

func doubleTagFrame(outerVLAN, innerVLAN uint16) []byte {
	// Outer Ethernet + outer 802.1Q tag + inner 802.1Q tag + payload.
	eth := layers.Ethernet{
		DstMAC:       mustMAC("00:11:22:33:44:55"), // target on outer VLAN
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	outerDot1Q := layers.Dot1Q{
		VLANIdentifier: outerVLAN,
		Type:           layers.EthernetTypeDot1Q,
	}
	innerDot1Q := layers.Dot1Q{
		VLANIdentifier: innerVLAN,
		Type:           layers.EthernetTypeIPv4,
	}
	ip := layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		SrcIP:    net.ParseIP("10.0.0.1").To4(),
		DstIP:    net.ParseIP("10.0.0.2").To4(),
		Protocol: layers.IPProtocolICMPv4,
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(8, 0), // echo request
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{ComputeChecksums: true, FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, &eth, &outerDot1Q, &innerDot1Q, &ip, &icmp); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// VLAN enum (802.1Q tagged frames)
// ---------------------------------------------------------------------------

func dot1qFrame(vlan uint16, payload []byte) []byte {
	eth := layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	dot1q := layers.Dot1Q{
		VLANIdentifier: vlan,
		Type:           layers.EthernetTypeIPv4,
	}
	ip := layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		SrcIP:    net.ParseIP("10.0.0.1").To4(),
		DstIP:    net.ParseIP("10.0.0.2").To4(),
		Protocol: layers.IPProtocolICMPv4,
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(8, 0),
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{ComputeChecksums: true, FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, &eth, &dot1q, &ip, &icmp); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Voice VLAN (LLDP-MED)
// ---------------------------------------------------------------------------

func lldpVoiceFrame() []byte {
	eth := layers.Ethernet{
		DstMAC:       mustMAC("01:80:c2:00:00:0e"), // LLDP multicast
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeLinkLayerDiscovery,
	}
	// Build LLDP payload as raw bytes (the fork's LLDP layer API is
	// cumbersome; the wire format is straightforward TLVs).
	lldpPayload := buildLLDPVoiceVLANPayload()
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{}, &eth, gopacket.Payload(lldpPayload)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func buildLLDPVoiceVLANPayload() []byte {
	var out []byte
	// Chassis ID TLV: type=1, subtype=4 (MAC address).
	chassisID := []byte{0x04}
	chassisID = append(chassisID, srcBytes()...)
	out = append(out, lldpTLV(1, chassisID)...)
	// Port ID TLV: type=2, subtype=5 (interface name).
	portID := []byte("eth0")
	out = append(out, lldpTLV(2, portID)...)
	// TTL TLV: type=3, value=120 seconds.
	ttl := make([]byte, 2)
	binary.BigEndian.PutUint16(ttl, 120)
	out = append(out, lldpTLV(3, ttl)...)
	// Network Policy TLV (voice VLAN): type=8, subtype=voice (1), tagged, VLAN 20, priority 5, DSCP 46.
	// OUI 00:12:bb (IEEE 802.1AB-2009 Annex E, but Cisco uses 12bb).
	// TLV: type=8(7 bits) | len(9 bits). Subtype=1 (voice), flags=0x20 (tagged), VLAN=20.
	netPolicy := []byte{0x01, 0x20}                        // subtype=voice, flags: tagged
	vlanField := uint16(20)<<9 | uint16(5)<<5 | uint16(46) // VLAN + priority + DSCP
	nf := make([]byte, 2)
	binary.BigEndian.PutUint16(nf, vlanField)
	netPolicy = append(netPolicy, nf...)
	out = append(out, lldpTLV(8, netPolicy)...)
	// End TLV: type=0, len=0.
	out = append(out, 0x00, 0x00)
	return out
}

func lldpTLV(tlvType uint8, value []byte) []byte {
	header := uint16(tlvType)<<9 | uint16(len(value)&0x1FF)
	out := make([]byte, 2+len(value))
	binary.BigEndian.PutUint16(out[0:2], header)
	copy(out[2:], value)
	return out
}

// ---------------------------------------------------------------------------
// STP root BPDU
// ---------------------------------------------------------------------------

func stpBPDU(rootPriority, rootExtID, rootID, helloTime, forwardDelay, maxAge, msgAge uint16) []byte {
	stpPayload := buildSTPPayload(rootPriority, rootID, helloTime, forwardDelay, maxAge, msgAge)
	eth := layers.Ethernet{
		DstMAC:       stpDst,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeLLC,
	}
	llc := layers.LLC{
		DSAP:    0x42,
		SSAP:    0x42,
		Control: 3,
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, &eth, &llc, gopacket.Payload(stpPayload)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func buildSTPPayload(rootPriority, rootID, helloTime, forwardDelay, maxAge, msgAge uint16) []byte {
	// STP Configuration BPDU:
	// Protocol(2) = 0x0000, Version(1) = 0x00, Type(1) = 0x00 (config),
	// Flags(1) = 0x00, RootID(8) = priority(2)+bridge(6),
	// RootPathCost(4), BridgeID(8) = priority(2)+bridge(6),
	// PortID(2), MessageAge(2), MaxAge(2), HelloTime(2), ForwardDelay(2)
	out := make([]byte, 0, 35)
	// Protocol ID
	out = append(out, 0x00, 0x00)
	// Version
	out = append(out, 0x00)
	// BPDU type = config
	out = append(out, 0x00)
	// Flags
	out = append(out, 0x00)
	// Root ID: priority + MAC
	rootBridge := make([]byte, 8)
	binary.BigEndian.PutUint16(rootBridge[0:2], rootPriority)
	copy(rootBridge[2:8], srcBytes())
	out = append(out, rootBridge...)
	// Root path cost
	out = append(out, 0x00, 0x00, 0x00, 0x00)
	// Bridge ID: same as root (we are the root)
	bridgeID := make([]byte, 8)
	binary.BigEndian.PutUint16(bridgeID[0:2], rootPriority)
	copy(bridgeID[2:8], srcBytes())
	out = append(out, bridgeID...)
	// Port ID
	portID := make([]byte, 2)
	binary.BigEndian.PutUint16(portID, 0x8001)
	out = append(out, portID...)
	// Message age
	ma := make([]byte, 2)
	binary.BigEndian.PutUint16(ma, msgAge)
	out = append(out, ma...)
	// Max age
	mav := make([]byte, 2)
	binary.BigEndian.PutUint16(mav, maxAge)
	out = append(out, mav...)
	// Hello time
	ht := make([]byte, 2)
	binary.BigEndian.PutUint16(ht, helloTime)
	out = append(out, ht...)
	// Forward delay
	fd := make([]byte, 2)
	binary.BigEndian.PutUint16(fd, forwardDelay)
	out = append(out, fd...)
	return out
}

// ---------------------------------------------------------------------------
// CAM flood frames
// ---------------------------------------------------------------------------

func camFloodFrame(seq int) []byte {
	// Each frame uses a unique source MAC to fill CAM table entries.
	src := fmt.Sprintf("00:00:00:00:%02x:%02x", (seq>>8)&0xFF, seq&0xFF)
	eth := layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       mustMAC(src),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		SrcIP:    net.ParseIP("10.0.0.1").To4(),
		DstIP:    net.ParseIP("255.255.255.255").To4(),
		Protocol: layers.IPProtocolUDP,
	}
	udp := layers.UDP{
		SrcPort: 1234,
		DstPort: 5678,
	}
	udp.SetNetworkLayerForChecksum(&ip)
	payload := []byte("flood")
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{ComputeChecksums: true, FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, &eth, &ip, &udp, gopacket.Payload(payload)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// VTP
// ---------------------------------------------------------------------------

type vlanEntry struct {
	id   uint16
	name string
}

func vtpSummary(domain string, revision uint32) []byte {
	dom := []byte(domain)
	db := make([]byte, 32)
	copy(db, dom)
	ts := []byte("260823120000") // deterministic
	body := make([]byte, 0, 72)
	body = append(body, 0x01, 0x01, 0x00, byte(len(dom))) // version, code, followers, domainLen
	body = append(body, db...)
	rev := make([]byte, 4)
	binary.BigEndian.PutUint32(rev, revision)
	body = append(body, rev...)
	body = append(body, make([]byte, 4)...) // updater IP = 0.0.0.0
	body = append(body, ts...)
	body = append(body, make([]byte, 16)...) // digest placeholder
	digest := md5.Sum(append(append(make([]byte, 16), body...), make([]byte, 16)...))
	copy(body[56:72], digest[:])
	return dot3SNAP(vtpDst, srcBytes(), 0x2003, body)
}

func vtpSubset(domain string, revision uint32, vlans []vlanEntry) []byte {
	dom := []byte(domain)
	body := make([]byte, 0, 40)
	body = append(body, 0x01, 0x02, 0x01, byte(len(dom))) // version, code, seq, domainLen
	db := make([]byte, 32)
	copy(db, dom)
	body = append(body, db...)
	rev := make([]byte, 4)
	binary.BigEndian.PutUint32(rev, revision)
	body = append(body, rev...)
	for _, v := range vlans {
		nb := []byte(v.name)
		npad := (len(nb) + 3) &^ 3
		rec := make([]byte, 12+npad)
		rec[0] = byte(12 + npad)
		rec[2] = 1 // type
		rec[3] = byte(len(nb))
		binary.BigEndian.PutUint16(rec[6:8], v.id) // MTU=1500
		binary.BigEndian.PutUint16(rec[4:6], 1500)
		binary.BigEndian.PutUint32(rec[8:12], 0x100000+uint32(v.id))
		copy(rec[12:], nb)
		body = append(body, rec...)
	}
	return dot3SNAP(vtpDst, srcBytes(), 0x2003, body)
}

func vtpRequest(domain string) []byte {
	dom := []byte(domain)
	body := make([]byte, 0, 38)
	body = append(body, 0x01, 0x03, 0x00, byte(len(dom)))
	db := make([]byte, 32)
	copy(db, dom)
	body = append(body, db...)
	sv := make([]byte, 2)
	binary.BigEndian.PutUint16(sv, 0)
	body = append(body, sv...)
	return dot3SNAP(vtpDst, srcBytes(), 0x2003, body)
}

// ---------------------------------------------------------------------------
// MVRP
// ---------------------------------------------------------------------------

func mvrpFrame(vid uint16, join bool) []byte {
	event := byte(2) // JoinIn
	if !join {
		event = 3 // LeaveEmpty
	}
	vh := uint16(1) // LeaveAll=0, NumberOfValues=1
	msg := make([]byte, 0, 8)
	msg = append(msg, 0x01, 0x04) // AttributeType=1(VID), AttributeLength=4
	vhb := make([]byte, 2)
	binary.BigEndian.PutUint16(vhb, vh)
	msg = append(msg, vhb...)
	fv := make([]byte, 2)
	binary.BigEndian.PutUint16(fv, vid)
	msg = append(msg, fv...)
	msg = append(msg, event<<6)         // ThreePackedEvents
	msg = append(msg, 0x00, 0x00)       // padding
	pdu := append([]byte{0x00}, msg...) // ProtocolVersion = 0
	eth := layers.Ethernet{
		DstMAC:       mvrpDst,
		SrcMAC:       srcBytes(),
		EthernetType: 0x88F5,
	}
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{}, &eth, gopacket.Payload(pdu)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Port steal (ARP)
// ---------------------------------------------------------------------------

func portStealARP(seq int) []byte {
	eth := layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkType(1), // Ethernet
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2, // ARP reply (gratuitous)
		SourceHwAddress:   srcBytes(),
		SourceProtAddress: net.ParseIP("10.0.0.1").To4(),
		DstHwAddress:      broadcastMAC,
		DstProtAddress:    net.ParseIP("10.0.0.1").To4(),
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{ComputeChecksums: true, FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, &eth, &arp); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// EtherChannel: LACP + PAgP
// ---------------------------------------------------------------------------

func lacpFrame() []byte {
	actorSys := srcBytes()
	partnerSys := partnerMAC
	pdu := make([]byte, 110)
	pdu[0] = 0x01 // subtype
	pdu[1] = 0x01 // version
	// Actor TLV: type=0x01, len=20
	pdu[2] = 0x01
	pdu[3] = 20
	binary.BigEndian.PutUint16(pdu[4:6], 0x8000) // sys priority
	copy(pdu[6:12], actorSys)
	binary.BigEndian.PutUint16(pdu[12:14], 0x0001) // key
	binary.BigEndian.PutUint16(pdu[14:16], 0x8000) // port priority
	binary.BigEndian.PutUint16(pdu[16:18], 0x0001) // port
	pdu[18] = 0x3C                                 // state: activity+aggregation+sync+collecting
	// Partner TLV: type=0x02, len=20
	pdu[22] = 0x02
	pdu[23] = 20
	binary.BigEndian.PutUint16(pdu[24:26], 0x8000)
	copy(pdu[26:32], partnerSys)
	binary.BigEndian.PutUint16(pdu[32:34], 0x0002)
	binary.BigEndian.PutUint16(pdu[34:36], 0x8000)
	binary.BigEndian.PutUint16(pdu[36:38], 0x0002)
	pdu[38] = 0x00
	// Collector TLV: type=0x03, len=16
	pdu[42] = 0x03
	pdu[43] = 16
	binary.BigEndian.PutUint16(pdu[44:46], 0x8000) // max delay
	// Terminator
	pdu[58] = 0x00
	pdu[59] = 0x00
	eth := layers.Ethernet{
		DstMAC:       lacpDst,
		SrcMAC:       srcBytes(),
		EthernetType: 0x8809,
	}
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{}, &eth, gopacket.Payload(pdu)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func pagpFrame() []byte {
	srcB := srcBytes()
	partner := partnerMAC
	body := make([]byte, 38)
	body[0] = 0x01 // version
	body[1] = 0x01 // command = hello
	body[2] = 0x01 // group capability
	body[3] = 0x01 // group number
	copy(body[4:10], srcB)
	binary.BigEndian.PutUint32(body[10:14], 0x00000001) // local port ID
	binary.BigEndian.PutUint32(body[14:18], 0x00000001) // local ifindex
	body[18] = 0x01                                     // local group cap
	body[19] = 0x00
	copy(body[20:26], partner)
	binary.BigEndian.PutUint32(body[26:30], 0x00000002) // partner port ID
	binary.BigEndian.PutUint32(body[30:34], 0x00000002) // partner ifindex
	body[34] = 0x01                                     // partner group cap
	body[35] = 0x00
	body[36] = 0x00 // count
	body[37] = 0x00
	return dot3SNAP(pagpDst, srcBytes(), 0x0104, body)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func dot3SNAP(dst, src net.HardwareAddr, snapPID uint16, body []byte) []byte {
	eth := layers.Ethernet{
		DstMAC:       dst,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeLLC,
	}
	llc := layers.LLC{
		DSAP:    0xAA,
		SSAP:    0xAA,
		Control: 3,
	}
	snap := layers.SNAP{
		OrganizationalCode: ciscoOUI,
		Type:               layers.EthernetType(snapPID),
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, &eth, &llc, &snap, gopacket.Payload(body)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func writePcap(path string, pkts ...[]byte) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		panic(err)
	}
	for _, pkt := range pkts {
		if err := w.WritePacket(gopacket.CaptureInfo{
			CaptureLength: len(pkt),
			Length:        len(pkt),
		}, pkt); err != nil {
			panic(err)
		}
	}
}
