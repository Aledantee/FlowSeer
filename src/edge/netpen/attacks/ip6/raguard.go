// raguard.go implements the RA-Guard observation behavior.
//
// Durability (from the catalog): non-destructive. The behavior observes
// forged RA handling under RA-Guard. The attack leg sends a forged RA,
// and the watch leg observes whether it traverses (the watch-leg rule).
//
// Integration scenario: raguard observes its own forged RA under attack
// and records traversal per the watch-leg rule.

package ip6

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type raGuardFinding struct {
	Action     string `json:"action"`
	Sent       int    `json:"sent"`
	Observed   int    `json:"observed"`
	Traversal  string `json:"traversal"`
	Attributed string `json:"attributed"`
}

// RunRAGuard sends one forged RA for the target's prefix from the attack
// leg and observes the watch leg for the observation window. A forged RA
// seen on the watch leg confirms traversal; an empty window is resisted;
// without a watch leg the verdict is pending. Cancellation while observing
// returns ctx.Err(); terminal receive errors are wrapped.
func RunRAGuard(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldPrefix6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC

	// Craft and send the forged RA (same as rogue RA).
	pkt, err := craftRogueRA(src, deps.Target.Prefix6)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "raguard: craft RA")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "raguard: send RA")
	}

	sent := 1
	observed := 0
	attackAttributed := 0
	ambientAttributed := 0
	started := time.Now()

	// Observe the watch leg for traversal evidence until the window ends,
	// the frames sent plus one ambient frame arrived, or the receive
	// closed.
	if deps.WatchLeg != nil {
		window := time.NewTimer(deps.Observe)
		defer window.Stop()
		ch := deps.WatchLeg.Receive(ctx)
		for range sent + 1 { // sent + possible ambient
			select {
			case frame, ok := <-ch:
				if err := ctx.Err(); err != nil {
					return check.Verdict{}, err
				}
				if !ok {
					goto done
				}
				if frame.Err != nil {
					return check.Verdict{}, errs.Wrap(frame.Err, "raguard: receive observation")
				}
				observed++
				if isRAGuardFrame(frame.Data, src) {
					attackAttributed++
				} else {
					ambientAttributed++
				}
			case <-window.C:
				goto done
			case <-ctx.Done():
				return check.Verdict{}, ctx.Err()
			}
		}
	}
done:
	if err := ctx.Err(); err != nil {
		return check.Verdict{}, err
	}
	elapsed := time.Since(started)

	traversal := "not-forwarded"
	if attackAttributed > 0 {
		traversal = "forwarded"
	}
	attributed := "none"
	if attackAttributed > 0 && ambientAttributed > 0 {
		attributed = "attack-distinguished-from-ambient"
	} else if attackAttributed > 0 {
		attributed = "attack-only"
	}

	detail, _ := json.Marshal(raGuardFinding{
		Action:     "raguard-observation",
		Sent:       sent,
		Observed:   observed,
		Traversal:  traversal,
		Attributed: attributed,
	})
	deps.Emitter.Finding("ra", detail)

	switch {
	case deps.WatchLeg == nil:
		return check.Pending("no observable: traversal needs a watch leg"), nil
	case attackAttributed > 0:
		return check.Confirmed(fmt.Sprintf("%d forged RA crossed to the watch leg", attackAttributed), elapsed), nil
	default:
		return check.Resisted(fmt.Sprintf("0 forged RA in %s", elapsed.Round(time.Millisecond)), elapsed), nil
	}
}

// isRAGuardFrame checks whether a frame's Ethernet source MAC matches
// the attacker's — the traversal-attribution test.
func isRAGuardFrame(data []byte, attackMAC net.HardwareAddr) bool {
	if len(data) < 14 {
		return false
	}
	srcMAC := net.HardwareAddr(data[6:12])
	return srcMAC.String() == attackMAC.String()
}

var _ gopacket.SerializableLayer = (*layers.ICMPv6RouterAdvertisement)(nil)
