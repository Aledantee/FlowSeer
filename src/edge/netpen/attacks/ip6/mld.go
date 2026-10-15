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
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

type mldFinding struct {
	Action     string `json:"action"`
	FrameCount int    `json:"frame_count"`
	Bounded    bool   `json:"bounded"`
}

// RunMLD sends five MLDv1 reports for a fixed multicast group from the
// attack leg's MAC and link-local address. The group is probe content;
// the behavior reads no target. The verdict is pending: whether the
// snooping table took the membership needs a read of the device it does not
// have.
func RunMLD(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	addr := deps.AttackLeg.Addr()
	mcastGroup := net.ParseIP("ff0e::1234")

	frameCount := 5
	for i := 1; i <= frameCount; i++ {
		pkt, err := craftMLDReport(addr, mcastGroup)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "mld: craft frame %d", i)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "mld: send frame %d", i)
		}
		if err := ctx.Err(); err != nil {
			return check.Verdict{}, err
		}
	}

	detail, _ := json.Marshal(mldFinding{
		Action:     "mld-abuse-flood",
		FrameCount: frameCount,
		Bounded:    true,
	})
	deps.Emitter.Finding("mld", detail)

	return check.Pending("no observable: membership acceptance needs a read of the snooping table"), nil
}

// craftMLDReport builds an MLDv1 Multicast Listener Report for the
// given multicast group. Uses the pool craft path.
func craftMLDReport(addr link.Addr, mcastGroup net.IP) ([]byte, error) {
	mcastMAC := multicastMACForIPv6(mcastGroup)

	eth := &layers.Ethernet{
		DstMAC:       mcastMAC,
		SrcMAC:       addr.MAC,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   1,
		SrcIP:      addr.IPv6,
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
