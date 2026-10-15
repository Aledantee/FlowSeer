// vlanenum.go implements the VLAN enumeration check. Impact none, restore
// none, affects none: it sends nothing and listens on the attack leg for
// 802.1Q-tagged frames for the window, collecting the VLAN ids it sees.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type vlanEnumFinding struct {
	VLANs  []uint16 `json:"vlans"`
	Count  int      `json:"count"`
	Method string   `json:"method"`
}

// RunVlanEnum collects the tagged frames on the attack leg for the window
// and reads no target. The verdict is confirmed with the VLAN ids seen as
// the inventory, or pending with "no tagged frames in <window>" when none
// arrived, with no finding. A frame without a readable 802.1Q header is
// skipped. Cancellation returns ctx.Err(). A terminal receive error comes
// back wrapped, with no partial finding.
func RunVlanEnum(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	tagged, obs, err := check.Collect(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Lazy)
		return pkt.Layer(layers.LayerTypeDot1Q) != nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "vlanenum: receive")
	}

	vlanSet := make(map[uint16]bool)
	for _, data := range tagged {
		pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Lazy)
		if q, ok := pkt.Layer(layers.LayerTypeDot1Q).(*layers.Dot1Q); ok {
			vlanSet[q.VLANIdentifier] = true
		}
	}
	if len(vlanSet) == 0 {
		return check.Pending(fmt.Sprintf("no tagged frames in %v", deps.Observe)), nil
	}

	vlans := make([]uint16, 0, len(vlanSet))
	for v := range vlanSet {
		vlans = append(vlans, v)
	}
	sort.Slice(vlans, func(i, j int) bool { return vlans[i] < vlans[j] })

	detail, _ := json.Marshal(vlanEnumFinding{
		VLANs:  vlans,
		Count:  len(vlans),
		Method: "passive-trunk-capture",
	})
	deps.Emitter.Finding("vlan", detail)

	ids := make([]string, len(vlans))
	for i, v := range vlans {
		ids[i] = strconv.Itoa(int(v))
	}
	return check.Confirmed("VLANs "+strings.Join(ids, ", "), obs.Elapsed), nil
}
