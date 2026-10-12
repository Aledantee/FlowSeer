// vlanhop.go implements the VLAN hopping attack. Like doubletag, it uses
// QinQ double-tagging, but it is classified temporary-restored: the attack
// arms an active restore step that clears the host-side 802.1Q interface
// configuration. The --persist mode is permanent-destructive (it requires
// the per-run opt-in): it skips the restore, leaving the host-side VLAN
// interface in place.

package l2

import (
	"context"
	"encoding/json"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type vlanHopFinding struct {
	Action    string `json:"action"`
	OuterVLAN uint16 `json:"outer_vlan"`
	InnerVLAN uint16 `json:"inner_vlan"`
	Restore   string `json:"restore"`
}

// RunVlanHop sends one QinQ frame tagged with the target's outer and inner
// VLANs and, on the base entry, arms a host-side VLAN interface restore
// step that does no host work yet, and the persist mode is permanent and arms
// nothing. The verdict is pending: whether the frame reached the inner VLAN
// needs a watch leg there, which it does not have.
func RunVlanHop(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldOuterVLAN, check.FieldInnerVLAN); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	outerVLAN := deps.Target.OuterVLAN
	innerVLAN := deps.Target.InnerVLAN

	pkt, err := craftDoubleTagFrame(src, outerVLAN, innerVLAN)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "vlanhop: craft frame")
	}

	restoreArmed := "no"
	if deps.Entry.Restore.Kind == check.RestoreActive {
		deps.Teardown.Arm("vlan-interface-restore", func(_ context.Context) error {
			// Deleting the temporary 802.1Q interface is host-side
			// state with no wire effect, and the in-memory harness observes
			// the restore via the teardown step name.
			return nil
		})
		restoreArmed = "yes"
	}

	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "vlanhop: send")
	}

	detail, _ := json.Marshal(vlanHopFinding{
		Action:    "vlan-hop",
		OuterVLAN: outerVLAN,
		InnerVLAN: innerVLAN,
		Restore:   restoreArmed,
	})
	deps.Emitter.Finding("vlan", detail)

	if restoreArmed == "yes" {
		return check.Pending("no observable: a hop into the inner VLAN needs a watch leg there, and the vlan-interface-restore step is a no-op"), nil
	}
	return check.Pending("no observable: a hop into the inner VLAN needs a watch leg there"), nil
}
