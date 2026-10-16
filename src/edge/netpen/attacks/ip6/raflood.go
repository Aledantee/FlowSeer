// raflood.go implements the RA-flood check. Impact temporary, restore
// decay with no stated bound, affects clients: the burst of five Router
// Advertisements, each from its own stamped source, adds routers and
// prefixes to the hosts that take them, and those entries expire on the
// lifetimes the frames carry. The fixture is spec-authored, since the
// baseline has no craft for it.

package ip6

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type raFloodFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Bounded    bool   `json:"bounded"`
}

// raFloodCount is the number of advertisements the flood sends.
const raFloodCount = 5

// RunRAFlood sends five Router Advertisements, each from a stamped source
// MAC and its derived link-local address, and reads the attack leg. The
// behavior reads no target and no leg address: the stamped sources are
// probe content. The predicate is a Neighbor Solicitation whose target is
// one of the stamped link-local sources, the probe a host sends a router
// it took once it has traffic for it, which confirms; none within the
// window is pending, since a host probes a router only when it sends
// traffic (RFC 4861 section 7.3.3).
func RunRAFlood(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	sources := make([]net.IP, 0, raFloodCount)
	for i := 1; i <= raFloodCount; i++ {
		pkt, source, err := craftRAFloodFrame(i)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "raflood: craft frame %d", i)
		}
		sources = append(sources, source)
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "raflood: send frame %d", i)
		}
		if err := ctx.Err(); err != nil {
			return check.Verdict{}, err
		}
	}

	detail, _ := json.Marshal(raFloodFinding{
		Action:     "ra-flood",
		FrameCount: raFloodCount,
		Bounded:    true,
	})
	deps.Emitter.Finding("ra", detail)

	var probed net.IP
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		target, ok := isSolicitForAny(data, sources)
		if ok {
			probed = target
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "raflood: receive solicitation")
	}
	if found != nil {
		return check.Confirmed(fmt.Sprintf("a host probed the flood router %s", probed), obs.Elapsed), nil
	}
	return check.Pending(fmt.Sprintf("no host probed a flood router in %s: a host probes a router only when it sends traffic", obs.Elapsed.Round(time.Millisecond))), nil
}

// craftRAFloodFrame builds a Router Advertisement with a unique source
// MAC and derived link-local, which it returns beside the frame. Uses the
// pool craft path.
func craftRAFloodFrame(seq int) ([]byte, net.IP, error) {
	floodSrc := net.HardwareAddr{
		0x00, 0x11, 0x22, 0x33,
		byte((seq >> 8) & 0xFF),
		byte(seq & 0xFF),
	}
	linkLocal, err := linkLocalFromMAC(floodSrc)
	if err != nil {
		return nil, nil, err
	}

	eth := &layers.Ethernet{
		DstMAC:       allNodesMAC,
		SrcMAC:       floodSrc,
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

	ra := &layers.ICMPv6RouterAdvertisement{
		HopLimit:       64,
		Flags:          0,
		RouterLifetime: 300,
		ReachableTime:  0,
		RetransTimer:   0,
	}
	pkt, err := craftPool(eth, ip, icmp, ra)
	return pkt, linkLocal, err
}

var _ gopacket.SerializableLayer = (*layers.ICMPv6RouterAdvertisement)(nil)
