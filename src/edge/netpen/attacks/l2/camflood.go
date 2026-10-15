// camflood.go implements the CAM-table overflow check. Impact temporary,
// restore decay with no stated bound, affects device: the frames with
// stamped source MACs fill the switch's CAM table so it floods unicast
// frames to every port, and the entries age out on the switch's own
// timer.

package l2

import (
	"context"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type camFloodFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Method     string `json:"method"`
}

// camFloodSrcIP is the constant source IP for CAM-flood frames.
var camFloodSrcIP = net.IPv4(10, 0, 0, 1)

// camFloodDstIP is the broadcast destination IP for CAM-flood frames.
var camFloodDstIP = net.IPv4(255, 255, 255, 255)

// camFloodPayload is the constant UDP payload for CAM-flood frames.
var camFloodPayload = []byte("flood")

// RunCAMFlood sends five frames with unique source MACs to overflow the
// CAM table. The stamped MACs, addresses, and payload are probe content and
// the behavior reads no target. The verdict is pending: whether the switch
// went fail-open needs a unicast probe through the watch leg it does not
// have.
func RunCAMFlood(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	// Serialize the frame template once. Each burst packet then stamps its
	// unique source MAC into a pooled copy, which keeps the flood loop
	// allocation-free after pool warmup.
	template, err := craftCAMFloodFrame(0)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "camflood: craft template")
	}

	frameCount := 5
	for i := range frameCount {
		seq := i + 1
		buf := packetPool.Get().(*[]byte)
		pkt := (*buf)[:len(template)]
		copy(pkt, template)
		pkt[10] = byte((seq >> 8) & 0xFF)
		pkt[11] = byte(seq & 0xFF)

		err := deps.AttackLeg.Send(ctx, pkt)
		packetPool.Put(buf)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "camflood: send frame %d", i)
		}
	}

	detail, _ := json.Marshal(camFloodFinding{
		Action:     "cam-table-overflow",
		FrameCount: frameCount,
		Method:     "random-source-mac-flood",
	})
	deps.Emitter.Finding("eth", detail)

	return check.Pending("no observable: fail-open needs a unicast probe seen on the watch leg"), nil
}

// craftCAMFloodFrame builds a broadcast frame with a unique source MAC
// to fill a CAM table entry. Uses the pool craft path.
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
	payload := gopacket.Payload(camFloodPayload)
	return craftPool(eth, ip, udp, payload)
}
