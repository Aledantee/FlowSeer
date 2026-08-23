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

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type raGuardFinding struct {
	Action     string `json:"action"`
	Sent       int    `json:"sent"`
	Observed   int    `json:"observed"`
	Traversal  string `json:"traversal"`
	Attributed string `json:"attributed"`
}

// RunRAGuard sends a forged RA from the attack leg and observes the
// watch leg for traversal evidence (the watch-leg rule). The behavior
// records whether the forged RA traversed the fabric.
func RunRAGuard(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()

	// Craft and send the forged RA (same as rogue RA).
	pkt, err := craftRogueRA(src)
	if err != nil {
		return fmt.Errorf("raguard: craft RA: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return fmt.Errorf("raguard: send RA: %w", err)
	}

	sent := 1
	observed := 0
	attackAttributed := 0
	ambientAttributed := 0

	// Observe the watch leg for traversal evidence. The observation
	// is bounded by a short timeout: if no frame arrives within the
	// window, the behavior records what it observed and exits.
	if deps.WatchLeg != nil {
		ch := deps.WatchLeg.Receive(ctx)
		for range sent + 1 { // sent + possible ambient
			select {
			case frame, ok := <-ch:
				if !ok {
					goto done
				}
				observed++
				if isRAGuardFrame(frame.Data, src) {
					attackAttributed++
				} else {
					ambientAttributed++
				}
			case <-time.After(100 * time.Millisecond):
				goto done
			case <-ctx.Done():
				goto done
			}
		}
	}
done:

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

	return nil
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
