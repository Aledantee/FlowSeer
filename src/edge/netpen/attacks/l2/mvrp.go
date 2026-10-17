// mvrp.go implements the MVRP (Multiple VLAN Registration Protocol) check.
// Impact temporary, restore decay with no stated bound, affects fabric:
// the MVRP JoinIn messages register a set of VLANs on the attack leg's
// port, and the registrations expire on the switches' MRP timers. The
// registrar semantics are unverified, since IEEE 802.1Q clause 10 is not
// vendored; the predicate reads the frames the owned decoder parses.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
// MAC and reads the attack leg for the switch's registration. The behavior
// reads no target: any VLAN id registers, so the pair is probe content.
// The predicate is an MVRP frame from another sender carrying one of the
// pair's ids with a JoinIn event, which confirms; none within the window
// resists.
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

	var registered uint16
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		vid, ok := isMVRPJoinFor(data, src, vlans)
		if ok {
			registered = vid
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "mvrp: receive")
	}
	if found != nil {
		return check.Confirmed(fmt.Sprintf("the switch declares VLAN %d with a JoinIn", registered), obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("no MVRP JoinIn for VLAN 10 or 20 from the switch in %s", obs.Elapsed.Round(time.Millisecond)), obs.Elapsed), nil
}
