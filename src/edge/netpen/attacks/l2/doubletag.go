// doubletag.go implements the VLAN double-tagging (QinQ) hopping check.
// Impact none, restore none, affects none: the one double-tagged frame,
// outer tag the access port's VLAN and inner tag the target VLAN, is
// forwarded into the target VLAN by a trunk that strips the outer tag, a
// one-shot hop that leaves no state behind.

package l2

import (
	"context"
	"encoding/json"
	"net"
	"strconv"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type doubleTagFinding struct {
	Action    string `json:"action"`
	OuterVLAN uint16 `json:"outer_vlan"`
	InnerVLAN uint16 `json:"inner_vlan"`
	Target    string `json:"target"`
}

// RunDoubleTag sends one QinQ frame tagged with the target's outer and
// inner VLANs from the attack leg's MAC. The echo request inside and its
// addresses are probe content. The verdict is pending: whether the frame
// reached the inner VLAN needs a watch leg there, which it does not have.
func RunDoubleTag(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldOuterVLAN, check.FieldInnerVLAN); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	outerVLAN := deps.Target.OuterVLAN
	innerVLAN := deps.Target.InnerVLAN

	pkt, err := craftDoubleTagFrame(src, outerVLAN, innerVLAN)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "doubletag: craft frame")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "doubletag: send")
	}

	detail, _ := json.Marshal(doubleTagFinding{
		Action:    "vlan-hop",
		OuterVLAN: outerVLAN,
		InnerVLAN: innerVLAN,
		Target:    strconv.Itoa(int(innerVLAN)),
	})
	deps.Emitter.Finding("vlan", detail)

	return check.Pending("no observable: a hop into the inner VLAN needs a watch leg there"), nil
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
	return craft.Default(eth, outer, inner, ip, icmp)
}
