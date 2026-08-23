// ghost.go implements the ghost-frame proof attack behavior.
//
// Durability (from the catalog): non-destructive. The attack sends
// sentinel frames with a reserved group MAC as the source address (an
// 802.3 §3.2.6 violation) to prove forward-but-never-learn behavior on
// non-conformant silicon. The watch leg confirms traversal evidence.
//
// R2 + AE3: the behavior requires a watch leg; the runner fast-fails
// without one (catalog Legs=WatchRequired). The behavior asserts
// traversal-attribution: frames produced by attack-leg activity observed
// on the watch leg are distinguished from ambient traffic on the two-leg
// fixture harness.

package fh

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type ghostFinding struct {
	Action     string `json:"action"`
	GhostSA    string `json:"ghost_sa"`
	Sentinel   string `json:"sentinel"`
	Mode       string `json:"mode"`
	Sent       int    `json:"sent"`
	Observed   int    `json:"observed"`
	Traversal  string `json:"traversal"`
	Attributed string `json:"attributed"`
}

// RunGhost sends ghost-SA sentinel frames from the attack leg and observes
// the watch leg for traversal evidence. The two-leg fixture harness feeds
// the watch leg with frames matching what the attack leg sent (proving the
// frames traversed the fabric).
func RunGhost(ctx context.Context, deps runner.Deps) error {
	// The runner guarantees a non-nil WatchLeg for WatchRequired
	// behaviors (R2, AE3). We do not re-check it here.
	watchLeg := deps.WatchLeg

	sentinel := "sentinel-0x42"
	count := 2

	// Craft the ghost traversal frames (STP worst-prio + LLDP -0E).
	stpPkt, err := craftGhostSTP()
	if err != nil {
		return fmt.Errorf("ghost: craft STP: %w", err)
	}
	lldpPkt, err := craftGhostLLDP()
	if err != nil {
		return fmt.Errorf("ghost: craft LLDP: %w", err)
	}

	frames := [][]byte{stpPkt, lldpPkt}
	// Record the ghost-SA bytes for watch-leg attribution.
	ghostSABytes := GhostSA

	// Send frames from the attack leg.
	for i, frame := range frames {
		if err := deps.AttackLeg.Send(ctx, frame); err != nil {
			return fmt.Errorf("ghost: send frame %d: %w", i, err)
		}
	}

	// Observe the watch leg for traversal evidence. The two-leg fixture
	// harness feeds the watch leg with frames matching the attack leg's
	// output (proving traversal). We read a bounded number of frames and
	// attribute: a frame on the watch leg whose source MAC matches the
	// ghost SA is attack-leg-produced; a frame with a different source
	// MAC is ambient.
	observed := 0
	attackAttributed := 0
	ambientAttributed := 0
	ch := watchLeg.Receive(ctx)
	for range count + 1 { // read sent frames + possible ambient
		select {
		case frame, ok := <-ch:
			if !ok {
				goto done
			}
			observed++
			if isGhostFrame(frame.Data, ghostSABytes) {
				attackAttributed++
			} else {
				ambientAttributed++
			}
		case <-ctx.Done():
			goto done
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

	detail, _ := json.Marshal(ghostFinding{
		Action:     "ghost-traversal",
		GhostSA:    GhostSA.String(),
		Sentinel:   sentinel,
		Mode:       "traversal",
		Sent:       count,
		Observed:   observed,
		Traversal:  traversal,
		Attributed: attributed,
	})
	deps.Emitter.Finding("arp", detail)

	return nil
}

// isGhostFrame checks whether a frame's Ethernet source MAC matches the
// ghost SA — the traversal-attribution test.
func isGhostFrame(data []byte, ghostSA net.HardwareAddr) bool {
	if len(data) < 12 {
		return false
	}
	// Ethernet source MAC is bytes [6:12].
	return data[6] == ghostSA[0] && data[7] == ghostSA[1] &&
		data[8] == ghostSA[2] && data[9] == ghostSA[3] &&
		data[10] == ghostSA[4] && data[11] == ghostSA[5]
}

// craftGhostSTP builds a worst-priority STP BPDU with the ghost SA.
func craftGhostSTP() ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       stpDstMAC,
		SrcMAC:       GhostSA,
		EthernetType: layers.EthernetTypeLLC,
	}
	llc := &layers.LLC{
		DSAP:    0x42,
		SSAP:    0x42,
		Control: 3,
	}
	rootMac := net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01}
	payload := buildGhostSTPPayload(0xF000, rootMac)
	return craftDefault(eth, llc, gopacket.Payload(payload))
}

func buildGhostSTPPayload(rootPriority uint16, rootMac net.HardwareAddr) []byte {
	var buf []byte
	buf = append(buf, 0)    // protocol
	buf = append(buf, 0)    // version
	buf = append(buf, 0)    // bpdu type
	buf = append(buf, 0x7E) // bpdu flags
	buf = binary.BigEndian.AppendUint16(buf, rootPriority)
	buf = append(buf, rootMac...)
	buf = binary.BigEndian.AppendUint32(buf, 0) // path cost
	buf = binary.BigEndian.AppendUint16(buf, rootPriority)
	buf = append(buf, rootMac...)
	buf = binary.BigEndian.AppendUint16(buf, 0x8001) // port ID
	buf = binary.BigEndian.AppendUint16(buf, 0)      // message age
	buf = binary.BigEndian.AppendUint16(buf, 1)      // hello time
	buf = binary.BigEndian.AppendUint16(buf, 6)      // max age
	buf = binary.BigEndian.AppendUint16(buf, 4)      // forward delay
	return buf
}

// craftGhostLLDP builds a raw LLDP frame with the ghost SA and a sentinel.
func craftGhostLLDP() ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       lldpDstMAC,
		SrcMAC:       GhostSA,
		EthernetType: layers.EthernetTypeLinkLayerDiscovery,
	}
	payload := []byte("GHOST-TRAVERSAL sentinel-0x42")
	return craftDefault(eth, gopacket.Payload(payload))
}
