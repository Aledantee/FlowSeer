//go:build ignore

// harvest_ip6.go generates DHCP and IPv6 first-hop attack reference
// fixtures into attacks/testdata/ip6/*.pcap by crafting the exact byte
// sequences the ip6 behaviors produce. It is the fixture
// factory for the DHCP/IPv6 attack behaviors.
//
// Baseline-harvested (matching l2l3-audit's craft):
//   - dhcpstarve, roguedhcp, roguedhcp6, daddos, ndpspoof, raguard,
//     roguera
//
// Spec-authored (no baseline craft):
//   - raflood (RA-flood variant), mld (MLD abuse flood)
//
// Run from the attacks/ip6 directory:
//
//	go run harvest_ip6.go
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
)

const srcMAC = "00:11:22:33:44:55"

var (
	broadcastMAC = mustMAC("ff:ff:ff:ff:ff:ff")

	allNodesMAC   = mustMAC("33:33:00:00:00:01")
	allRoutersMAC = mustMAC("33:33:00:00:00:02")
	allDHCPv6MAC  = mustMAC("33:33:00:00:01:02")

	victimMAC6 = mustMAC("00:aa:bb:cc:dd:01")
	serverMAC6 = mustMAC("00:aa:bb:cc:dd:fe")

	victimIP6  = net.ParseIP("fd00::10")
	serverIP6  = net.ParseIP("fd00::1")
	attackerIP = net.ParseIP("fd00::999")
	targetIP6  = net.ParseIP("fd00::dead")

	clientIP6  = net.ParseIP("fd00::100")
	allNodesIP = net.ParseIP("ff02::1")

	dhcpClientMAC = mustMAC("00:aa:bb:cc:dd:01")
	dhcpServerMAC = mustMAC("00:aa:bb:cc:dd:fe")
	dhcpServerIP  = net.IPv4(172, 16, 0, 1)
	dhcpClientIP  = net.IPv4(172, 16, 0, 10)
	dhcpOfferIP   = net.IPv4(172, 16, 0, 100)

	// Fixed xids for deterministic fixtures (xids are masked in
	// field-set comparisons; the harvest uses fixed values so the
	// fixture is byte-stable).
	dhcpXid   uint32 = 0x12345678
	dhcpv6Xid        = []byte{0x00, 0x11, 0x22}
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
	// DHCPv4 behaviors
	writePcap("dhcpstarve.pcap", dhcpStarveFrames()...)
	writePcap("roguedhcp.pcap", rogueDHCPFrames()...)

	// DHCPv6 behaviors
	writePcap("roguedhcp6.pcap", rogueDHCPv6Frames()...)

	// IPv6 ND behaviors
	writePcap("ndpspoof.pcap", ndpSpoofFrames()...)
	writePcap("daddos.pcap", dadDOSFrames()...)
	writePcap("daddos_resisted.pcap", dadDOSResistedFrames()...)

	// RA behaviors
	writePcap("roguera.pcap", rogueRAFrames()...)
	writePcap("raguard.pcap", raGuardFrames()...)

	// Supersets (spec-authored)
	writePcap("raflood.pcap", raFloodFrames()...)
	writePcap("mld.pcap", mldFrames()...)

	fmt.Println("harvest complete")
}

// dhcpStarveFrames sends DHCP DISCOVER frames with incrementing chaddr tails
// to exhaust the lease pool. Uses the pool craft path shape.

func dhcpStarveFrames() [][]byte {
	src := srcBytes()
	var frames [][]byte
	for i := 1; i <= 5; i++ {
		chaddr := net.HardwareAddr{
			0x00, 0x11, 0x22, 0x33,
			byte((i >> 8) & 0xFF),
			byte(i & 0xFF),
		}
		frames = append(frames, dhcpDiscoverFrame(src, chaddr, dhcpXid+uint32(i)))
	}
	return frames
}

func dhcpDiscoverFrame(src, chaddr net.HardwareAddr, xid uint32) []byte {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    net.IPv4(0, 0, 0, 0),
		DstIP:    net.IPv4(255, 255, 255, 255),
		Protocol: layers.IPProtocolUDP,
	}
	udp := &layers.UDP{
		SrcPort: 68,
		DstPort: 67,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	dhcp := &layers.DHCPv4{
		Operation:    layers.DHCPOpRequest,
		HardwareType: layers.LinkTypeEthernet,
		HardwareLen:  6,
		Xid:          xid,
		Secs:         0,
		Flags:        0x8000, // broadcast
		ClientIP:     net.IPv4(0, 0, 0, 0),
		YourClientIP: net.IPv4(0, 0, 0, 0),
		NextServerIP: net.IPv4(0, 0, 0, 0),
		RelayAgentIP: net.IPv4(0, 0, 0, 0),
		ClientHWAddr: chaddr,
		Options: layers.DHCPOptions{
			layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeDiscover)}),
			layers.NewDHCPOption(layers.DHCPOptParamsRequest, []byte{
				byte(layers.DHCPOptSubnetMask),
				byte(layers.DHCPOptRouter),
				byte(layers.DHCPOptDNS),
			}),
		},
	}
	return craft(eth, ip, udp, dhcp)
}

// rogueDHCPFrames sends a DHCP OFFER/ACK from a rogue server.

func rogueDHCPFrames() [][]byte {
	src := srcBytes() // attacker/rogue server MAC
	rogueIP := net.IPv4(172, 16, 0, 99)

	// OFFER frame
	offerEth := &layers.Ethernet{
		DstMAC:       dhcpClientMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	offerIP := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    rogueIP,
		DstIP:    dhcpClientIP,
		Protocol: layers.IPProtocolUDP,
	}
	offerUDP := &layers.UDP{
		SrcPort: 67,
		DstPort: 68,
	}
	_ = offerUDP.SetNetworkLayerForChecksum(offerIP)

	leaseTime := make([]byte, 4)
	binary.BigEndian.PutUint32(leaseTime, 1800)

	offerDHCP := &layers.DHCPv4{
		Operation:    layers.DHCPOpReply,
		HardwareType: layers.LinkTypeEthernet,
		HardwareLen:  6,
		Xid:          dhcpXid,
		Flags:        0,
		ClientIP:     net.IPv4(0, 0, 0, 0),
		YourClientIP: dhcpOfferIP,
		NextServerIP: rogueIP,
		RelayAgentIP: net.IPv4(0, 0, 0, 0),
		ClientHWAddr: dhcpClientMAC,
		Options: layers.DHCPOptions{
			layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeOffer)}),
			layers.NewDHCPOption(layers.DHCPOptServerID, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptLeaseTime, leaseTime),
			layers.NewDHCPOption(layers.DHCPOptSubnetMask, net.IPv4(255, 255, 255, 0).To4()),
			layers.NewDHCPOption(layers.DHCPOptRouter, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptDNS, rogueIP.To4()),
		},
	}
	offer := craft(offerEth, offerIP, offerUDP, offerDHCP)

	// ACK frame
	ackDHCP := &layers.DHCPv4{
		Operation:    layers.DHCPOpReply,
		HardwareType: layers.LinkTypeEthernet,
		HardwareLen:  6,
		Xid:          dhcpXid,
		Flags:        0,
		ClientIP:     dhcpClientIP,
		YourClientIP: dhcpOfferIP,
		NextServerIP: rogueIP,
		RelayAgentIP: net.IPv4(0, 0, 0, 0),
		ClientHWAddr: dhcpClientMAC,
		Options: layers.DHCPOptions{
			layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeAck)}),
			layers.NewDHCPOption(layers.DHCPOptServerID, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptLeaseTime, leaseTime),
			layers.NewDHCPOption(layers.DHCPOptSubnetMask, net.IPv4(255, 255, 255, 0).To4()),
			layers.NewDHCPOption(layers.DHCPOptRouter, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptDNS, rogueIP.To4()),
		},
	}
	ackEth := &layers.Ethernet{
		DstMAC:       dhcpClientMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ackIP := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    rogueIP,
		DstIP:    dhcpClientIP,
		Protocol: layers.IPProtocolUDP,
	}
	ackUDP := &layers.UDP{
		SrcPort: 67,
		DstPort: 68,
	}
	_ = ackUDP.SetNetworkLayerForChecksum(ackIP)
	ack := craft(ackEth, ackIP, ackUDP, ackDHCP)

	return [][]byte{offer, ack}
}

// rogueDHCPv6Frames sends a SOLICIT then an ADVERTISE response from the rogue.
// The behavior completes the solicit→advertise cycle against the fixture.

func rogueDHCPv6Frames() [][]byte {
	src := srcBytes()

	// Solicit frame (sent by the victim client; the behavior observes it).
	solicitEth := &layers.Ethernet{
		DstMAC:       allDHCPv6MAC,
		SrcMAC:       dhcpClientMAC,
		EthernetType: layers.EthernetTypeIPv6,
	}
	solicitIP := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   1,
		SrcIP:      clientIP6,
		DstIP:      net.ParseIP("ff02::1:2"),
	}
	solicitUDP := &layers.UDP{
		SrcPort: 546,
		DstPort: 547,
	}
	_ = solicitUDP.SetNetworkLayerForChecksum(solicitIP)

	// Client DUID (LL type): type=1(LL), hwtype=1(eth), MAC.
	clientDUID := append([]byte{0x00, 0x01, 0x00, 0x01}, dhcpClientMAC...)

	// IA_NA option: IAID(4) + T1(4) + T2(4) = 12 bytes data.
	iaNAData := make([]byte, 12)
	binary.BigEndian.PutUint32(iaNAData[0:4], 0x00000001) // IAID

	solicitDHCP := &layers.DHCPv6{
		MsgType:       layers.DHCPv6MsgTypeSolicit,
		TransactionID: dhcpv6Xid,
		Options: layers.DHCPv6Options{
			layers.NewDHCPv6Option(layers.DHCPv6OptClientID, clientDUID),
			layers.NewDHCPv6Option(layers.DHCPv6OptIANA, iaNAData),
			layers.NewDHCPv6Option(layers.DHCPv6OptOro, []byte{0x00, 0x17, 0x00, 0x18}), // DNS, Domain
		},
	}
	solicit := craft(solicitEth, solicitIP, solicitUDP, solicitDHCP)

	// Advertise frame (rogue server response).
	advEth := &layers.Ethernet{
		DstMAC:       dhcpClientMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	advIP := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   255,
		SrcIP:      serverIP6,
		DstIP:      clientIP6,
	}
	advUDP := &layers.UDP{
		SrcPort: 547,
		DstPort: 546,
	}
	_ = advUDP.SetNetworkLayerForChecksum(advIP)

	// Server DUID (LL type): type=1(LL), hwtype=1(eth), MAC.
	serverDUID := append([]byte{0x00, 0x01, 0x00, 0x01}, src...)

	// IA_NA with IAAddr: IAID(4)+T1(4)+T2(4) + IAAddr option(4+16+4+4)
	iaNAWithAddr := make([]byte, 12+4+16+4+4)
	binary.BigEndian.PutUint32(iaNAWithAddr[0:4], 0x00000001) // IAID
	binary.BigEndian.PutUint32(iaNAWithAddr[4:8], 300)        // T1
	binary.BigEndian.PutUint32(iaNAWithAddr[8:12], 600)       // T2
	// IAAddr option embedded: code(2)+len(2)+addr(16)+pref(4)+valid(4)
	binary.BigEndian.PutUint16(iaNAWithAddr[12:14], uint16(layers.DHCPv6OptIAAddr))
	binary.BigEndian.PutUint16(iaNAWithAddr[14:16], 24) // option data length
	copy(iaNAWithAddr[16:32], net.ParseIP("fd00::200").To16())
	binary.BigEndian.PutUint32(iaNAWithAddr[32:36], 300) // preferred
	binary.BigEndian.PutUint32(iaNAWithAddr[36:40], 600) // valid

	// Status code option: Success.
	statusData := []byte{0x00, 0x00} // Success

	advDHCP := &layers.DHCPv6{
		MsgType:       layers.DHCPv6MsgTypeAdvertise,
		TransactionID: dhcpv6Xid,
		Options: layers.DHCPv6Options{
			layers.NewDHCPv6Option(layers.DHCPv6OptServerID, serverDUID),
			layers.NewDHCPv6Option(layers.DHCPv6OptClientID, clientDUID),
			layers.NewDHCPv6Option(layers.DHCPv6OptIANA, iaNAWithAddr),
			layers.NewDHCPv6Option(layers.DHCPv6OptStatusCode, statusData),
			layers.NewDHCPv6Option(layers.DHCPv6OptDNSServers, net.ParseIP("fd00::1").To16()),
		},
	}
	advertise := craft(advEth, advIP, advUDP, advDHCP)

	return [][]byte{solicit, advertise}
}

// ndpSpoofFrames sends a spoofed Neighbor Advertisement claiming the
// victim's IP is at the attacker's MAC.

func ndpSpoofFrames() [][]byte {
	src := srcBytes()

	eth := &layers.Ethernet{
		DstMAC:       victimMAC6,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      attackerIP,
		DstIP:      victimIP6,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	// Flags: Router=0, Solicited=1, Override=1 (0x60)
	na := &layers.ICMPv6NeighborAdvertisement{
		Flags:         0x60,
		TargetAddress: victimIP6,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptTargetAddress,
				Data: src, // attacker's MAC as target LLA
			},
		},
	}
	return [][]byte{craft(eth, ip, icmp, na)}
}

// dadDOSFrames sends a Neighbor Solicitation for the target address
// (claiming it during DAD). The resisted variant simulates a host that
// completed DAD before the attack window.

func dadDOSFrames() [][]byte {
	src := srcBytes()
	snMAC := solicitedNodeMAC(targetIP6)
	snIP := solicitedNodeIP(targetIP6)

	eth := &layers.Ethernet{
		DstMAC:       snMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      net.IPv6zero, // DAD: source is unspecified
		DstIP:      snIP,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborSolicitation, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	ns := &layers.ICMPv6NeighborSolicitation{
		TargetAddress: targetIP6,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptSourceAddress,
				Data: src, // attacker's source LLA
			},
		},
	}
	return [][]byte{craft(eth, ip, icmp, ns)}
}

// dadDOSResistedFrames simulates a host that completed DAD before the
// attack window — a Neighbor Advertisement from the legitimate owner
// defending its address. The behavior observes this and reports resisted.
func dadDOSResistedFrames() [][]byte {
	src := srcBytes()
	snMAC := solicitedNodeMAC(targetIP6)
	snIP := solicitedNodeIP(targetIP6)

	// Legitimate owner's NA defending the address.
	eth := &layers.Ethernet{
		DstMAC:       allNodesMAC,
		SrcMAC:       serverMAC6,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      targetIP6,
		DstIP:      allNodesIP,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	na := &layers.ICMPv6NeighborAdvertisement{
		Flags:         0x60, // Solicited + Override
		TargetAddress: targetIP6,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptTargetAddress,
				Data: serverMAC6, // legitimate owner's MAC
			},
		},
	}
	_ = snMAC
	_ = snIP
	_ = src
	return [][]byte{craft(eth, ip, icmp, na)}
}

// rogueRAFrames sends a Router Advertisement claiming to be a router with
// a high priority (lifetime ~1800s).

func rogueRAFrames() [][]byte {
	src := srcBytes()

	eth := &layers.Ethernet{
		DstMAC:       allNodesMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      linkLocalFromMAC(src), // EUI-64 link-local from src MAC
		DstIP:      allNodesIP,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeRouterAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	// Prefix info option: prefix fd00::/64, valid 1800s, preferred 1200s.
	prefixData := make([]byte, 30)
	prefixData[0] = 64                                 // prefix length
	prefixData[1] = 0xC0                               // flags: L=1, A=1
	binary.BigEndian.PutUint32(prefixData[2:6], 1200)  // preferred
	binary.BigEndian.PutUint32(prefixData[6:10], 1800) // valid
	copy(prefixData[14:30], net.ParseIP("fd00::").To16())

	ra := &layers.ICMPv6RouterAdvertisement{
		HopLimit:       64,
		Flags:          0, // M=0, O=0
		RouterLifetime: 1800,
		ReachableTime:  0,
		RetransTimer:   0,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptPrefixInfo,
				Data: prefixData,
			},
			{
				Type: layers.ICMPv6OptSourceAddress,
				Data: src, // source LLA
			},
		},
	}
	return [][]byte{craft(eth, ip, icmp, ra)}
}

// RA Guard: forged RA under attack (the watch leg observes it).
// The attack leg sends a forged RA; the watch leg receives it, proving
// traversal (the watch-leg rule).

func raGuardFrames() [][]byte {
	return rogueRAFrames()
}

// RA flood (spec-authored): bounded burst of RAs with varying source.

func raFloodFrames() [][]byte {
	src := srcBytes()
	var frames [][]byte
	for i := 1; i <= 5; i++ {
		floodSrc := net.HardwareAddr{
			0x00, 0x11, 0x22, 0x33,
			byte((i >> 8) & 0xFF),
			byte(i & 0xFF),
		}
		linkLocal := linkLocalFromMAC(floodSrc)

		eth := &layers.Ethernet{
			DstMAC:       allNodesMAC,
			SrcMAC:       floodSrc,
			EthernetType: layers.EthernetTypeIPv6,
		}
		ip := &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolICMPv6,
			HopLimit:   255,
			SrcIP:      linkLocal,
			DstIP:      allNodesIP,
		}
		icmp := &layers.ICMPv6{
			TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeRouterAdvertisement, 0),
		}
		_ = icmp.SetNetworkLayerForChecksum(ip)

		ra := &layers.ICMPv6RouterAdvertisement{
			HopLimit:       64,
			Flags:          0,
			RouterLifetime: 300,
			ReachableTime:  0,
			RetransTimer:   0,
		}
		frames = append(frames, craft(eth, ip, icmp, ra))
	}
	_ = src
	return frames
}

// MLD abuse (spec-authored): bounded burst of MLDv1 report frames.

func mldFrames() [][]byte {
	src := srcBytes()
	var frames [][]byte
	mcastGroup := net.ParseIP("ff0e::1234")
	mcastMAC := multicastMACForIPv6(mcastGroup)

	for i := 1; i <= 5; i++ {
		eth := &layers.Ethernet{
			DstMAC:       mcastMAC,
			SrcMAC:       src,
			EthernetType: layers.EthernetTypeIPv6,
		}
		ip := &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolICMPv6,
			HopLimit:   1,
			SrcIP:      net.ParseIP("fe80::1"),
			DstIP:      mcastGroup,
		}
		icmp := &layers.ICMPv6{
			TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeMLDv1MulticastListenerReportMessage, 0),
		}
		_ = icmp.SetNetworkLayerForChecksum(ip)

		mld := &layers.MLDv1MulticastListenerReportMessage{
			MLDv1Message: layers.MLDv1Message{
				MaximumResponseDelay: 0,
				MulticastAddress:     mcastGroup,
			},
		}
		frames = append(frames, craft(eth, ip, icmp, mld))
	}
	return frames
}

func solicitedNodeMAC(ip net.IP) net.HardwareAddr {
	ip16 := ip.To16()
	return net.HardwareAddr{0x33, 0x33, 0xff, ip16[13], ip16[14], ip16[15]}
}

func solicitedNodeIP(ip net.IP) net.IP {
	ip16 := ip.To16()
	addr := make(net.IP, 16)
	addr[0] = 0xff
	addr[1] = 0x02
	addr[11] = 0x01
	addr[12] = 0xff
	addr[13] = ip16[13]
	addr[14] = ip16[14]
	addr[15] = ip16[15]
	return addr
}

func multicastMACForIPv6(ip net.IP) net.HardwareAddr {
	ip16 := ip.To16()
	return net.HardwareAddr{0x33, 0x33, ip16[12], ip16[13], ip16[14], ip16[15]}
}

// linkLocalFromMAC derives an IPv6 link-local address from a MAC using
// the EUI-64 format: fe80::XXff:feYY:ZZZZ (U/L bit flipped).
func linkLocalFromMAC(mac net.HardwareAddr) net.IP {
	addr := make(net.IP, 16)
	addr[0] = 0xfe
	addr[1] = 0x80
	addr[8] = mac[0] ^ 0x02
	addr[9] = mac[1]
	addr[10] = mac[2]
	addr[11] = 0xff
	addr[12] = 0xfe
	addr[13] = mac[3]
	addr[14] = mac[4]
	addr[15] = mac[5]
	return addr
}

func craft(layersList ...gopacket.SerializableLayer) []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, layersList...); err != nil {
		panic(err)
	}
	return append([]byte(nil), buf.Bytes()...)
}

func writePcap(path string, pkts ...[]byte) {
	fullPath := filepath.Join("..", "testdata", "ip6", filepath.Base(path))
	f, err := os.Create(fullPath)
	if err != nil {
		panic(fmt.Sprintf("create %s: %v", fullPath, err))
	}
	defer func() { _ = f.Close() }()

	w := pcapgo.NewWriter(f)
	// LinkTypeEthernet = 1
	if err := w.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		panic(fmt.Sprintf("write header %s: %v", fullPath, err))
	}

	for _, pkt := range pkts {
		if err := w.WritePacket(gopacket.CaptureInfo{
			Timestamp:     time.Now(),
			CaptureLength: len(pkt),
			Length:        len(pkt),
		}, pkt); err != nil {
			panic(fmt.Sprintf("write packet %s: %v", fullPath, err))
		}
	}
	fmt.Println("wrote", fullPath, len(pkts), "packets")
}
