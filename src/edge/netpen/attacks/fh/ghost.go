// ghost.go implements the ghost-VLAN traversal check. Impact none, restore
// none, affects none: the two probe frames are sent and watched for, and
// nothing on the device or the hosts changes with them.

package fh

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
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

// RunGhost sends two ghost-SA frames and watches the required watch leg
// for the window. Impact none, restore none, affects none: the frames
// carry a reserved group MAC as their source, which conformant silicon
// drops, and install nothing where they arrive. The behavior reads no
// target: the ghost source address and the sentinel are probe content.
// The verdict is confirmed when a frame with the ghost source address
// crosses to the watch leg within the window and resisted when the window
// ends without one, whatever else the watch leg carried. Cancellation
// during the observation returns ctx.Err(). A receive error comes back
// wrapped and emits no finding. Attribution matches the source MAC, which
// no other sender on the segment uses lawfully.
func RunGhost(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	// The runner guarantees a non-nil WatchLeg for WatchRequired
	// behaviors, so this is not re-checked here.
	watchLeg := deps.WatchLeg

	sentinel := "sentinel-0x42"

	stpPkt, err := craftGhostSTP()
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "ghost: craft STP")
	}
	lldpPkt, err := craftGhostLLDP()
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "ghost: craft LLDP")
	}

	frames := [][]byte{stpPkt, lldpPkt}
	for i, frame := range frames {
		if err := deps.AttackLeg.Send(ctx, frame); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "ghost: send frame %d", i)
		}
	}

	crossed, obs, err := check.First(ctx, watchLeg, deps.Observe, func(data []byte) bool {
		return isGhostFrame(data, GhostSA)
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "ghost: receive watch frame")
	}

	observed := obs.Skipped
	traversal := "not-forwarded"
	attributed := "none"
	if crossed != nil {
		observed++
		traversal = "forwarded"
		attributed = "attack-only"
		if obs.Skipped > 0 {
			attributed = "attack-distinguished-from-ambient"
		}
	}

	detail, _ := json.Marshal(ghostFinding{
		Action:     "ghost-traversal",
		GhostSA:    GhostSA.String(),
		Sentinel:   sentinel,
		Mode:       "traversal",
		Sent:       len(frames),
		Observed:   observed,
		Traversal:  traversal,
		Attributed: attributed,
	})
	deps.Emitter.Finding("arp", detail)

	if crossed != nil {
		return check.Confirmed("a ghost frame crossed to the watch leg", obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("0 ghost frames in %s", obs.Elapsed.Round(time.Millisecond)), obs.Elapsed), nil
}

// isGhostFrame reports whether a frame's Ethernet source MAC is the ghost
// SA, the attribution a crossing rests on.
func isGhostFrame(data []byte, ghostSA net.HardwareAddr) bool {
	return len(data) >= 14 && bytes.Equal(data[6:12], ghostSA)
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
	return craft.Default(eth, llc, gopacket.Payload(payload))
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
	return craft.Default(eth, gopacket.Payload(payload))
}
