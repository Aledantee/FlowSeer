// vlanenum.go implements the VLAN enumeration behavior. It is a passive,
// non-destructive attack: it listens on a trunk port for 802.1Q-tagged
// frames and extracts the set of active VLAN IDs from the captured traffic.
//
// The behavior reads frames from the attack leg's RX channel, decodes each
// as Ethernet → Dot1Q, and collects the VLAN IDs it sees. It emits a single
// finding listing the enumerated VLANs.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type vlanEnumFinding struct {
	VLANs  []uint16 `json:"vlans"`
	Count  int      `json:"count"`
	Method string   `json:"method"`
}

// RunVlanEnum passively enumerates VLANs from tagged frames on the attack
// leg. It reads frames until the context is canceled or a short idle
// timeout elapses with no new VLANs, then reports the set.
func RunVlanEnum(ctx context.Context, deps runner.Deps) error {
	const idleTimeout = 500 * time.Millisecond

	rx := deps.AttackLeg.Receive(ctx)
	vlanSet := make(map[uint16]bool)
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			goto done
		case <-ctx.Done():
			goto done
		case frame, ok := <-rx:
			if !ok {
				goto done
			}
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(idleTimeout)
			if frame.Err != nil {
				continue
			}
			pkt := gopacket.NewPacket(frame.Data, layers.LayerTypeEthernet, gopacket.Lazy)
			dot1q := pkt.Layer(layers.LayerTypeDot1Q)
			if dot1q == nil {
				continue
			}
			q, ok := dot1q.(*layers.Dot1Q)
			if !ok {
				continue
			}
			vlanSet[q.VLANIdentifier] = true
		}
	}

done:
	vlans := make([]uint16, 0, len(vlanSet))
	for v := range vlanSet {
		vlans = append(vlans, v)
	}
	sort.Slice(vlans, func(i, j int) bool { return vlans[i] < vlans[j] })

	if len(vlans) == 0 {
		return fmt.Errorf("vlanenum: no tagged frames observed")
	}

	detail, _ := json.Marshal(vlanEnumFinding{
		VLANs:  vlans,
		Count:  len(vlans),
		Method: "passive-trunk-capture",
	})
	deps.Emitter.Finding("vlan", detail)

	return nil
}
