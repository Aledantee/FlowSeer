// vlanhop.go implements the VLAN hopping check. It sends the same QinQ
// frame as doubletag and reads the watch leg the same way. Impact none,
// restore none, affects none: the one double-tagged frame leaves no state
// behind. The name stays because the full command arms it from recon for
// the first VLAN ids it sees. The baseline's hop, a host subinterface in
// the target VLAN with a DHCP lease, needs the host's own IP stack and is
// not what this port sends.

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
}

// RunVlanHop sends one QinQ frame tagged with the target's outer and inner
// VLANs from the attack leg's MAC and reads the watch leg for the echo
// request inside it. The predicate is doubletag's: the echo request on the
// watch leg, tagged with the inner VLAN or untagged, confirms; its absence
// at the window's end resists; no watch leg is pending. It arms nothing.
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
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "vlanhop: send")
	}

	detail, _ := json.Marshal(vlanHopFinding{
		Action:    "vlan-hop",
		OuterVLAN: outerVLAN,
		InnerVLAN: innerVLAN,
	})
	deps.Emitter.Finding("vlan", detail)

	return observeHop(ctx, deps, "vlanhop", src, innerVLAN)
}
