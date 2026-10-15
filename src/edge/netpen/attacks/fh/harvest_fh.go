//go:build ignore

// harvest_fh.go generates first-hop/identity attack reference fixtures into
// attacks/testdata/fh/*.pcap by crafting the exact byte sequences the
// baseline (l2l3-audit) produces. It is the fixture factory for the
// first-hop attack behaviors: byte-for-byte deterministic fixtures.
//
// Baseline-harvested (matching l2l3-audit's craft):
//   - arpsweep, arpspoof, gratarp, hsrp, vrrp, icmpredirect, llmnr, ghost
//
// Spec-authored (the baseline has no craft for these):
//   - glbp (Wireshark packet-glbp.c), lldpspoof (IEEE 802.1AB)
//   - vrrp carries the RFC 3768 section 5.3.8 checksum and the eight
//     Authentication Data octets of section 5.3.10, which the baseline's
//     vrrp_frame leaves out
//
// Response fixtures, the frames a peer sends back that the predicates
// read, are spec-authored and decoded by gopacket or the owned layer as
// the second reader. The Wireshark sample capture page
// (https://wiki.wireshark.org/SampleCaptures, fetched 2026-10-15) carries
// no licence statement for its capture files, so no frame is copied from
// one:
//   - arpspoof_response.pcap: an ARP request (RFC 826) and an ICMP echo
//     reply (RFC 792) from the victim
//   - gratarp_response.pcap: an ICMP echo reply and an ARP request from the
//     gateway
//   - hsrp_response.pcap: an Active hello, a Resign, and a Speak hello from
//     the active router (RFC 2281 section 5.1 layout, section 5.7 events)
//   - glbp_response.pcap: an Active hello and a Listen hello from the
//     active gateway (packet-glbp.c layout)
//   - vrrp_response.pcap: a priority-100 advertisement from the master
//     (RFC 3768 section 5.1 layout)
//   - llmnr_query.pcap: a query from the victim (RFC 4795 section 2.1.1
//     header, RFC 1035 question)
//
// Run from the attacks/fh directory:
//
//	go run harvest_fh.go
//
// Fully offline: no sockets, no scapy. Writes pcaps via pcapgo.
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/fixtureaddr"
)

const srcMAC = "00:11:22:33:44:55"

var (
	broadcastMAC = fixtureaddr.MustMAC("ff:ff:ff:ff:ff:ff")
	vrrpDstMAC   = fixtureaddr.MustMAC("01:00:5e:00:00:12")
	hsrpDstMAC   = fixtureaddr.MustMAC("01:00:5e:00:00:02")
	glbpDstMAC   = fixtureaddr.MustMAC("01:00:5e:00:00:66")
	llmnrDstMAC  = fixtureaddr.MustMAC("01:00:5e:00:00:fc")
	lldpDstMAC   = fixtureaddr.MustMAC("01:80:c2:00:00:0e") // LLDP nearest bridge
	stpDstMAC    = fixtureaddr.MustMAC("01:80:c2:00:00:00")
)

func srcBytes() net.HardwareAddr { return fixtureaddr.MustMAC(srcMAC) }

// genErr holds the first packet-assembly or write failure. The builders and
// [writePcap] record into it instead of panicking, and [run] returns it, so a
// generator bug exits non-zero through log.Fatal rather than with a stack
// trace.
var genErr error

// fail records the first non-nil error into [genErr].
func fail(err error) {
	if err != nil && genErr == nil {
		genErr = err
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	outDir := "../testdata/fh"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// ARP sweep: broadcast ARP request for a /24 range (first 3 hosts).
	writePcap(filepath.Join(outDir, "arpsweep.pcap"),
		arpSweepFrame(net.IPv4(172, 16, 0, 1)),
		arpSweepFrame(net.IPv4(172, 16, 0, 2)),
		arpSweepFrame(net.IPv4(172, 16, 0, 3)),
	)

	// ARP spoof: unicast ARP replies poisoning target <-> gateway.
	writePcap(filepath.Join(outDir, "arpspoof.pcap"),
		arpSpoofFrame(victimMAC, victimIP, gwIP),
		arpSpoofFrame(gwMAC, gwIP, victimIP),
	)
	// ARP spoof restore: unicast ARP replies with real MACs.
	writePcap(filepath.Join(outDir, "arpspoof_restore.pcap"),
		arpRestoreFrame(victimMAC, victimIP, gwIP, gwMAC),
		arpRestoreFrame(gwMAC, gwIP, victimIP, victimMAC),
	)

	// Gratuitous ARP: broadcast ARP reply announcing IP→MAC.
	writePcap(filepath.Join(outDir, "gratarp.pcap"),
		gratARPFrame(net.IPv4(10, 0, 0, 1)),
		gratARPFrame(net.IPv4(10, 0, 0, 1)),
		gratARPFrame(net.IPv4(10, 0, 0, 1)),
	)

	// HSRP: coup + hello (matching l2l3-audit's hsrp_cmd).
	writePcap(filepath.Join(outDir, "hsrp.pcap"),
		hsrpFrame(1, 16, 255, 1, 3, 10), // coup: opcode=1, state=active(16)
		hsrpFrame(0, 16, 255, 1, 3, 10), // hello: opcode=0, state=active(16)
	)
	writePcap(filepath.Join(outDir, "hsrp_restore.pcap"),
		hsrpFrame(2, 8, 255, 1, 3, 10), // resign: opcode=2, state=speak(8)
	)

	// VRRP: advertisement (matching l2l3-audit's vrrp_frame).
	writePcap(filepath.Join(outDir, "vrrp.pcap"),
		vrrpFrame(51, 120, []net.IP{net.IPv4(10, 0, 0, 1)}),
	)

	// ICMP redirect: redirect from gw telling victim to route via attacker.
	writePcap(filepath.Join(outDir, "icmpredirect.pcap"),
		icmpRedirectFrame(gwIP, victimIP, net.IPv4(10, 0, 0, 99), net.IPv4(8, 8, 8, 8)),
	)

	// LLMNR: query + poisoned response (matching l2l3-audit's make_poisoner).
	writePcap(filepath.Join(outDir, "llmnr.pcap"),
		llmnrQueryFrame("host.lab"),
		llmnrResponseFrame("host.lab"),
	)

	// Ghost: ghost-SA sentinel frames (matching l2l3-audit's ghost_cmd).
	// Traversal mode: worst-prio STP + LLDP -0E probe with ghost SA.
	writePcap(filepath.Join(outDir, "ghost.pcap"),
		ghostSTPFrame(),
		ghostLLDPFrame(),
	)
	// Ghost BUM mode: broadcast frame with experimental ethertype.
	writePcap(filepath.Join(outDir, "ghost_bum.pcap"),
		ghostBUMFrame(),
	)
	// Ambient (non-attack) frames for traversal-attribution test.
	writePcap(filepath.Join(outDir, "ghost_ambient.pcap"),
		ambientFrame(),
	)

	// GLBP: packet-glbp.c at 1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3,
	// lines 137-218 and 289-365.
	writePcap(filepath.Join(outDir, "glbp.pcap"),
		glbpHelloFrame(1, 255, 0x20), // group=1, priority=255, state=active
	)
	writePcap(filepath.Join(outDir, "glbp_restore.pcap"),
		glbpHelloFrame(1, 100, 4), // resign: priority lowered, state=listen
	)

	// LLDP spoof: spoofed LLDP with false chassis/port/TTL (spec-authored).
	writePcap(filepath.Join(outDir, "lldpspoof.pcap"),
		lldpSpoofFrame(120), // TTL=120
	)

	// Response fixtures: what a peer sends back, from a MAC other than the
	// attack leg's. The router at routerMAC is the active HSRP router, the
	// active GLBP gateway, and the VRRP master.
	writePcap(filepath.Join(outDir, "arpspoof_response.pcap"),
		arpRequestFrame(victimMAC, victimIP, gwIP),
		echoReplyFrame(victimMAC, srcBytes(), victimIP, gwIP),
	)
	writePcap(filepath.Join(outDir, "gratarp_response.pcap"),
		echoReplyFrame(gwMAC, srcBytes(), gwIP, net.IPv4(10, 0, 0, 1)),
		arpRequestFrame(gwMAC, gwIP, net.IPv4(10, 0, 0, 1)),
	)
	writePcap(filepath.Join(outDir, "hsrp_response.pcap"),
		hsrpFrameFrom(routerMAC, routerIP, 0, 16, 100, 1, 3, 10), // hello, state active
		hsrpFrameFrom(routerMAC, routerIP, 2, 8, 100, 1, 3, 10),  // resign, state speak
		hsrpFrameFrom(routerMAC, routerIP, 0, 8, 100, 1, 3, 10),  // hello, state speak
	)
	writePcap(filepath.Join(outDir, "glbp_response.pcap"),
		glbpHelloFrameFrom(routerMAC, routerIP, 1, 100, 0x20), // hello, state active
		glbpHelloFrameFrom(routerMAC, routerIP, 1, 100, 4),    // hello, state listen
	)
	writePcap(filepath.Join(outDir, "vrrp_response.pcap"),
		vrrpFrameFrom(routerMAC, routerIP, 51, 100, []net.IP{net.IPv4(10, 0, 0, 1)}),
	)
	writePcap(filepath.Join(outDir, "llmnr_query.pcap"),
		llmnrQueryFrameFrom(victimMAC, victimIP, 0x4a1c, "host.lab"),
	)

	if genErr != nil {
		return genErr
	}

	fmt.Println("OK: generated FH attack fixtures into", outDir)

	return nil
}

var (
	victimMAC = fixtureaddr.MustMAC("00:aa:bb:cc:dd:01")
	gwMAC     = fixtureaddr.MustMAC("00:aa:bb:cc:dd:fe")
	victimIP  = net.IPv4(172, 16, 0, 10)
	gwIP      = net.IPv4(172, 16, 0, 1)
	routerMAC = fixtureaddr.MustMAC("00:aa:bb:cc:dd:fe")
	routerIP  = net.IPv4(10, 0, 0, 254)
)

// arpRequestFrame crafts a broadcast ARP request from a peer asking who
// has target (RFC 826).
func arpRequestFrame(peerMAC net.HardwareAddr, peerIP, target net.IP) []byte {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       peerMAC,
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         1, // request
		SourceHwAddress:   peerMAC,
		SourceProtAddress: peerIP,
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    target,
	}
	return craft(eth, arp)
}

// echoReplyFrame crafts the ICMP echo reply (RFC 792) a peer sends to the
// probe: from the peer's MAC and IPv4 to dstMAC and the spoofed address,
// carrying the probe's identifier and payload.
func echoReplyFrame(peerMAC, dstMAC net.HardwareAddr, peerIP, spoofed net.IP) []byte {
	eth := &layers.Ethernet{
		DstMAC:       dstMAC,
		SrcMAC:       peerMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    peerIP,
		DstIP:    spoofed,
	}
	icmp := &layers.ICMPv4{
		TypeCode: 0x0000, // type=0 (echo reply), code=0
		Id:       0x4e50,
		Seq:      1,
	}
	return craft(eth, ip, icmp, gopacket.Payload([]byte("netpen")))
}

// llmnrQueryFrameFrom crafts an LLMNR query from a victim to the LLMNR
// group: the RFC 4795 section 2.1.1 header with the id and the QR bit
// clear, one RFC 1035 question for name, type A, class IN.
func llmnrQueryFrameFrom(peerMAC net.HardwareAddr, peerIP net.IP, id uint16, qname string) []byte {
	var body []byte
	body = binary.BigEndian.AppendUint16(body, id)
	body = binary.BigEndian.AppendUint16(body, 0x0000) // flags: QR clear
	body = binary.BigEndian.AppendUint16(body, 1)      // QDCOUNT
	body = binary.BigEndian.AppendUint16(body, 0)      // ANCOUNT
	body = binary.BigEndian.AppendUint16(body, 0)      // NSCOUNT
	body = binary.BigEndian.AppendUint16(body, 0)      // ARCOUNT
	for _, label := range splitLabels(qname) {
		body = append(body, byte(len(label)))
		body = append(body, label...)
	}
	body = append(body, 0)                        // root label
	body = binary.BigEndian.AppendUint16(body, 1) // QTYPE=A
	body = binary.BigEndian.AppendUint16(body, 1) // QCLASS=IN
	return craftUDPL3(llmnrDstMAC, peerMAC, peerIP, net.IPv4(224, 0, 0, 252), 5355, 5355, body)
}

func arpSweepFrame(target net.IP) []byte {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         1, // request
		SourceHwAddress:   srcBytes(),
		SourceProtAddress: gwIP,
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    target,
	}
	return craft(eth, arp)
}

// arpSpoofFrame crafts a unicast ARP reply: "spoofedIP is-at attackerMAC" sent to dst.
func arpSpoofFrame(dstMAC net.HardwareAddr, dstIP, spoofedIP net.IP) []byte {
	eth := &layers.Ethernet{
		DstMAC:       dstMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2,          // reply
		SourceHwAddress:   srcBytes(), // attacker MAC
		SourceProtAddress: spoofedIP,  // "I am gwIP"
		DstHwAddress:      dstMAC,
		DstProtAddress:    dstIP,
	}
	return craft(eth, arp)
}

// arpRestoreFrame crafts a unicast ARP reply restoring the real MAC mapping.
func arpRestoreFrame(dstMAC net.HardwareAddr, dstIP, srcIP net.IP, realMAC net.HardwareAddr) []byte {
	eth := &layers.Ethernet{
		DstMAC:       dstMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2,       // reply
		SourceHwAddress:   realMAC, // real MAC
		SourceProtAddress: srcIP,   // "gwIP is really at gwMAC"
		DstHwAddress:      dstMAC,
		DstProtAddress:    dstIP,
	}
	return craft(eth, arp)
}

func gratARPFrame(ip net.IP) []byte {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2, // reply
		SourceHwAddress:   srcBytes(),
		SourceProtAddress: ip,
		DstHwAddress:      broadcastMAC,
		DstProtAddress:    ip,
	}
	return craft(eth, arp)
}

// hsrpFrame builds the HSRP body as raw bytes (matching netpen's owned HSRP layer
// SerializeTo): Ethernet → IP(224.0.0.2) → UDP(1985) → HSRP body.

func hsrpFrame(opcode, state, priority, group, hellotime, holdtime uint8) []byte {
	return hsrpFrameFrom(srcBytes(), net.IPv4(10, 0, 0, 2), opcode, state, priority, group, hellotime, holdtime)
}

// hsrpFrameFrom builds the HSRP frame from the given sender.
func hsrpFrameFrom(srcMAC net.HardwareAddr, srcIP net.IP, opcode, state, priority, group, hellotime, holdtime uint8) []byte {
	vip := net.IPv4(10, 0, 0, 1)
	auth := [8]byte{'c', 'i', 's', 'c', 'o', 0, 0, 0}

	// HSRP body: Version(1) Op(1) State(1) Hellotime(1) Holdtime(1)
	// Priority(1) Group(1) Reserved(1) Auth(8) VirtualIP(4)
	body := make([]byte, 20)
	body[0] = 0 // version
	body[1] = opcode
	body[2] = state
	body[3] = hellotime
	body[4] = holdtime
	body[5] = priority
	body[6] = group
	body[7] = 0 // reserved
	copy(body[8:16], auth[:])
	copy(body[16:20], vip.To4())

	return craftUDPL3(hsrpDstMAC, srcMAC,
		srcIP, net.IPv4(224, 0, 0, 2),
		1985, 1985, body)
}

// vrrpFrame builds the VRRP body as raw bytes (gopacket's VRRPv2 has no SerializeTo):
// Ethernet → IP(224.0.0.18, ttl=255, proto=112) → VRRP body.

func vrrpFrame(vrid, priority uint8, vips []net.IP) []byte {
	return vrrpFrameFrom(srcBytes(), net.IPv4(10, 0, 0, 2), vrid, priority, vips)
}

// vrrpFrameFrom builds an advertisement from the given sender. The body
// is RFC 3768 section 5.1: Version|Type(1) | VRID(1) | Priority(1) |
// CountIP(1) | AuthType(1) | AdverInt(1) | Checksum(2) | IP(s) |
// Authentication Data(8), with the section 5.3.8 checksum computed here
// since gopacket sees the body as an opaque payload.
func vrrpFrameFrom(srcMAC net.HardwareAddr, srcIP net.IP, vrid, priority uint8, vips []net.IP) []byte {
	body := make([]byte, 8+4*len(vips)+8)
	body[0] = 0x21 // version=2, type=1 (advertisement)
	body[1] = vrid
	body[2] = priority
	body[3] = byte(len(vips))
	body[4] = 0 // auth type
	body[5] = 1 // adver interval
	for i, vip := range vips {
		copy(body[8+4*i:], vip.To4())
	}
	binary.BigEndian.PutUint16(body[6:8], onesComplementChecksum(body, 6))

	// VRRP rides directly on IP (proto=112), no UDP.
	eth := &layers.Ethernet{
		DstMAC:       vrrpDstMAC,
		SrcMAC:       srcMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      255,
		Protocol: 112, // VRRP
		SrcIP:    srcIP,
		DstIP:    net.IPv4(224, 0, 0, 18),
		Length:   20 + uint16(len(body)),
	}
	return craftL3Payload(eth, ip, body)
}

// onesComplementChecksum is the RFC 1071 checksum of data with the
// two-byte field at skip read as zero, as RFC 3768 section 5.3.8 sums.
func onesComplementChecksum(data []byte, skip int) uint16 {
	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		if i == skip {
			continue
		}
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// icmpRedirectFrame crafts an ICMP redirect: src=gw, dst=victim, gw=attacker.
// The embedded IP/UDP is the "original" datagram that triggered the redirect.
func icmpRedirectFrame(srcGW, dstVictim, redirectGW, origDst net.IP) []byte {
	eth := &layers.Ethernet{
		DstMAC:       victimMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcGW,
		DstIP:    dstVictim,
	}
	icmp := &layers.ICMPv4{
		TypeCode: 0x0501, // type=5 (redirect), code=1 (redirect for host)
	}
	// ICMP redirect body: gateway address (4) + original IP header + 64 bits.
	origIPHdr := make([]byte, 20)
	origIPHdr[0] = 0x45 // version=4, IHL=5
	origIPHdr[9] = 17   // protocol=UDP
	copy(origIPHdr[12:16], srcGW.To4())
	copy(origIPHdr[16:20], origDst.To4())
	// 8 bytes of original datagram (UDP header: sport=0, dport=53, len=8, cksum=0).
	origData := append(origIPHdr, []byte{0, 0, 0, 53, 0, 8, 0, 0}...)
	// Redirect gateway address first, then original datagram.
	redirectGWBytes := redirectGW.To4()
	payload := append(redirectGWBytes, origData...)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, icmp, gopacket.Payload(payload)); err != nil {
		fail(err)
		return nil
	}
	return append([]byte(nil), buf.Bytes()...)
}

func llmnrQueryFrame(qname string) []byte {
	body := buildDNSQuery(qname)
	return craftUDPL3(llmnrDstMAC, srcBytes(),
		net.IPv4(10, 0, 0, 2), net.IPv4(224, 0, 0, 252),
		5355, 5355, body)
}

func llmnrResponseFrame(qname string) []byte {
	body := buildDNSResponse(qname)
	return craftUDPL3(srcBytes(), srcBytes(),
		net.IPv4(10, 0, 0, 1), net.IPv4(10, 0, 0, 2),
		5355, 5355, body)
}

func buildDNSQuery(qname string) []byte {
	var buf []byte
	buf = binary.BigEndian.AppendUint16(buf, 0x1234) // ID
	buf = binary.BigEndian.AppendUint16(buf, 0x0000) // flags
	buf = binary.BigEndian.AppendUint16(buf, 1)      // QDCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 0)      // ANCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 0)      // NSCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 0)      // ARCOUNT
	for _, label := range splitLabels(qname) {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	buf = append(buf, 0)                        // root label
	buf = binary.BigEndian.AppendUint16(buf, 1) // QTYPE=A
	buf = binary.BigEndian.AppendUint16(buf, 1) // QCLASS=IN
	return buf
}

func buildDNSResponse(qname string) []byte {
	var buf []byte
	buf = binary.BigEndian.AppendUint16(buf, 0x1234) // ID
	buf = binary.BigEndian.AppendUint16(buf, 0x8000) // flags=response
	buf = binary.BigEndian.AppendUint16(buf, 1)      // QDCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 1)      // ANCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 0)      // NSCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 0)      // ARCOUNT
	for _, label := range splitLabels(qname) {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	buf = append(buf, 0)                             // root
	buf = binary.BigEndian.AppendUint16(buf, 1)      // QTYPE=A
	buf = binary.BigEndian.AppendUint16(buf, 1)      // QCLASS=IN
	buf = binary.BigEndian.AppendUint16(buf, 0xC00C) // compression pointer to offset 12
	buf = binary.BigEndian.AppendUint16(buf, 1)      // type A
	buf = binary.BigEndian.AppendUint16(buf, 1)      // class IN
	buf = binary.BigEndian.AppendUint32(buf, 30)     // TTL
	buf = binary.BigEndian.AppendUint16(buf, 4)      // rdlength
	buf = append(buf, 10, 0, 0, 1)                   // rdata = 10.0.0.1
	return buf
}

func splitLabels(name string) [][]byte {
	var labels [][]byte
	start := 0
	for i := range len(name) {
		if name[i] == '.' {
			labels = append(labels, []byte(name[start:i]))
			start = i + 1
		}
	}
	if start < len(name) {
		labels = append(labels, []byte(name[start:]))
	}
	return labels
}

// ghostSA is the reserved group MAC used as the source in ghost frames.
var ghostSA = fixtureaddr.MustMAC("01:80:c2:00:00:01")

// ghostSTPFrame crafts a worst-priority STP BPDU with the ghost SA.
func ghostSTPFrame() []byte {
	eth := &layers.Ethernet{
		DstMAC:       stpDstMAC,
		SrcMAC:       ghostSA,
		EthernetType: layers.EthernetTypeLLC,
	}
	llc := &layers.LLC{
		DSAP:    0x42,
		SSAP:    0x42,
		Control: 3,
	}
	rootMac := fixtureaddr.MustMAC("00:00:00:00:00:01")
	payload := buildSTPPayload(0xF000, rootMac)
	return craft(eth, llc, gopacket.Payload(payload))
}

func buildSTPPayload(rootPriority uint16, rootMac net.HardwareAddr) []byte {
	var buf []byte
	buf = append(buf, 0)    // protocol
	buf = append(buf, 0)    // version
	buf = append(buf, 0)    // bpdu type
	buf = append(buf, 0x7E) // bpdu flags
	buf = binary.BigEndian.AppendUint16(buf, rootPriority)
	buf = append(buf, rootMac...)
	buf = binary.BigEndian.AppendUint32(buf, 0) // path cost
	buf = binary.BigEndian.AppendUint16(buf, rootPriority)
	buf = append(buf, rootMac...)
	buf = binary.BigEndian.AppendUint16(buf, 0x8001) // port ID
	buf = binary.BigEndian.AppendUint16(buf, 0)      // message age
	buf = binary.BigEndian.AppendUint16(buf, 1)      // hello time
	buf = binary.BigEndian.AppendUint16(buf, 6)      // max age
	buf = binary.BigEndian.AppendUint16(buf, 4)      // forward delay
	return buf
}

// ghostLLDPFrame crafts a raw LLDP frame with the ghost SA and a sentinel payload.
func ghostLLDPFrame() []byte {
	eth := &layers.Ethernet{
		DstMAC:       lldpDstMAC,
		SrcMAC:       ghostSA,
		EthernetType: layers.EthernetTypeLinkLayerDiscovery,
	}
	payload := []byte("GHOST-TRAVERSAL sentinel-0x42")
	return craft(eth, gopacket.Payload(payload))
}

// ghostBUMFrame crafts a broadcast frame with experimental ethertype 0x88B5.
func ghostBUMFrame() []byte {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       ghostSA,
		EthernetType: 0x88B5,
	}
	payload := []byte("GHOST-PROBE sentinel-0x42")
	return craft(eth, gopacket.Payload(payload))
}

// ambientFrame crafts a non-attack ambient frame (different source MAC).
func ambientFrame() []byte {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       fixtureaddr.MustMAC("00:aa:bb:cc:dd:99"),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version: 4,
		IHL:     5,
		TTL:     64,
		SrcIP:   net.IPv4(10, 0, 0, 50),
		DstIP:   net.IPv4(10, 0, 0, 99),
	}
	return craft(eth, ip)
}

// glbpHelloFrame crafts a GLBP hello with the given priority and state.
func glbpHelloFrame(group uint16, priority uint8, state uint8) []byte {
	return glbpHelloFrameFrom(srcBytes(), net.IPv4(10, 0, 0, 2), group, priority, state)
}

// glbpHelloFrameFrom crafts the GLBP hello from the given sender, which
// is also the owner ID the header carries.
func glbpHelloFrameFrom(srcMAC net.HardwareAddr, srcIP net.IP, group uint16, priority uint8, state uint8) []byte {
	// Fixed header: version(1), unknown(1), group(2), unknown(2), owner ID(6).
	header := make([]byte, 12)
	header[0] = 1 // version
	binary.BigEndian.PutUint16(header[2:4], group)
	copy(header[6:12], srcMAC)

	// Hello TLV length includes the one-byte type and one-byte length.
	helloTLV := make([]byte, 28)
	helloTLV[0] = 1
	helloTLV[1] = 28
	helloTLV[3] = state
	helloTLV[5] = priority
	binary.BigEndian.PutUint32(helloTLV[8:12], 3000)
	binary.BigEndian.PutUint32(helloTLV[12:16], 10000)
	helloTLV[22] = 1 // IPv4
	helloTLV[23] = 4
	copy(helloTLV[24:28], net.IPv4(10, 0, 0, 1).To4())

	body := append(header, helloTLV...)
	return craftUDPL3(glbpDstMAC, srcMAC,
		srcIP, net.IPv4(224, 0, 0, 102),
		3222, 3222, body)
}

// lldpSpoofFrame crafts an LLDP frame with spoofed chassis/port/TTL.
func lldpSpoofFrame(ttl uint16) []byte {
	eth := &layers.Ethernet{
		DstMAC:       lldpDstMAC,
		SrcMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeLinkLayerDiscovery,
	}
	lldp := &layers.LinkLayerDiscovery{
		ChassisID: layers.LLDPChassisID{
			Subtype: layers.LLDPChassisIDSubTypeMACAddr,
			ID:      srcBytes(),
		},
		PortID: layers.LLDPPortID{
			Subtype: layers.LLDPPortIDSubtypeIfaceName,
			ID:      []byte("Gi0/1"),
		},
		TTL: ttl,
		Values: []layers.LinkLayerDiscoveryValue{
			{
				Type:   layers.LLDPTLVSysName,
				Length: 11,
				Value:  []byte("spoofed-sw1"),
			},
			{
				Type:   layers.LLDPTLVSysCapabilities,
				Length: 4,
				Value:  []byte{0x00, 0x14, 0x00, 0x14}, // router + bridge caps
			},
		},
	}
	return craft(eth, lldp)
}

func craft(layersList ...gopacket.SerializableLayer) []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, layersList...); err != nil {
		fail(err)
		return nil
	}
	return append([]byte(nil), buf.Bytes()...)
}

// craftUDPL3 builds Ethernet → IPv4 → UDP → payload with proper checksums.
func craftUDPL3(dstMAC, srcMAC net.HardwareAddr, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	eth := &layers.Ethernet{
		DstMAC:       dstMAC,
		SrcMAC:       srcMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}
	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	udp.SetNetworkLayerForChecksum(ip)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, udp, gopacket.Payload(payload)); err != nil {
		fail(err)
		return nil
	}
	return append([]byte(nil), buf.Bytes()...)
}

// craftL3Payload builds Ethernet → IPv4 → payload (no transport layer).
func craftL3Payload(eth *layers.Ethernet, ip *layers.IPv4, payload []byte) []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, gopacket.Payload(payload)); err != nil {
		fail(err)
		return nil
	}
	return append([]byte(nil), buf.Bytes()...)
}

// writePcap writes pkts to path, recording the first failure, its own IO or
// an assembly error a builder already recorded, into [genErr] and returning
// without writing, so a bad packet never lands in a fixture and [run] reports
// the fault. Each packet is stamped with the epoch, since pcapgo writes the
// wall clock for a zero stamp and every fixture would then change on each
// run.
func writePcap(path string, pkts ...[]byte) {
	if genErr != nil {
		return
	}

	f, err := os.Create(path)
	if err != nil {
		fail(err)
		return
	}
	defer func() { _ = f.Close() }()

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		fail(err)
		return
	}
	for _, pkt := range pkts {
		if err := w.WritePacket(gopacket.CaptureInfo{
			Timestamp:     time.Unix(0, 0),
			CaptureLength: len(pkt),
			Length:        len(pkt),
		}, pkt); err != nil {
			fail(err)
			return
		}
	}
}
