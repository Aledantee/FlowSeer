// dhcpstarve.go implements the DHCP starvation check. Impact temporary,
// restore decay with no stated bound, affects device: the DISCOVER frames
// with incrementing chaddr tails take leases from the DHCP server's pool,
// and the pool recovers as those unused leases expire on the server's own
// lease time. The chaddr tails and transaction IDs are fixed, so the
// fixture compares bytes.

package ip6

import (
	"context"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type dhcpStarveFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Method     string `json:"method"`
}

// dhcpZeroIP is the constant 0.0.0.0 IP for DHCP DISCOVER frames.
var dhcpZeroIP = net.IPv4(0, 0, 0, 0)

// dhcpBroadcastIP is the constant broadcast IP for DHCP DISCOVER frames.
var dhcpBroadcastIP = net.IPv4(255, 255, 255, 255)

// dhcpDiscoverType is the constant message-type option value.
var dhcpDiscoverType = []byte{byte(layers.DHCPMsgTypeDiscover)}

// dhcpParamRequest is the constant parameter-request option value.
var dhcpParamRequest = []byte{
	byte(layers.DHCPOptSubnetMask),
	byte(layers.DHCPOptRouter),
	byte(layers.DHCPOptDNS),
}

// RunDHCPStarve sends five DISCOVER frames with incrementing chaddr tails
// from the attack leg's MAC. The behavior reads no target: the chaddr tails
// and transaction IDs are probe content. The verdict is pending: whether
// the pool ran dry needs an offer listener it does not have.
func RunDHCPStarve(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	src := deps.AttackLeg.Addr().MAC

	frameCount := 5
	for i := 1; i <= frameCount; i++ {
		pkt, err := craftDHCPDiscover(src, i)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "dhcpstarve: craft frame %d", i)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "dhcpstarve: send frame %d", i)
		}
	}

	detail, _ := json.Marshal(dhcpStarveFinding{
		Action:     "lease-pool-exhaustion",
		FrameCount: frameCount,
		Method:     "incrementing-chaddr-discover",
	})
	deps.Emitter.Finding("dhcp", detail)

	return check.Pending("no observable: pool exhaustion needs an offer listener"), nil
}

// craftDHCPDiscover builds a DHCP DISCOVER with a unique chaddr for the
// given sequence. Uses the pool craft path.
func craftDHCPDiscover(src net.HardwareAddr, seq int) ([]byte, error) {
	chaddr := net.HardwareAddr{
		0x00, 0x11, 0x22, 0x33,
		byte((seq >> 8) & 0xFF),
		byte(seq & 0xFF),
	}
	xid := uint32(0x12345678) + uint32(seq)

	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      128,
		SrcIP:    dhcpZeroIP,
		DstIP:    dhcpBroadcastIP,
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
		ClientIP:     dhcpZeroIP,
		YourClientIP: dhcpZeroIP,
		NextServerIP: dhcpZeroIP,
		RelayAgentIP: dhcpZeroIP,
		ClientHWAddr: chaddr,
		Options: layers.DHCPOptions{
			layers.NewDHCPOption(layers.DHCPOptMessageType, dhcpDiscoverType),
			layers.NewDHCPOption(layers.DHCPOptParamsRequest, dhcpParamRequest),
		},
	}
	return craftPool(eth, ip, udp, dhcp)
}

var _ gopacket.SerializableLayer = (*layers.DHCPv4)(nil)
