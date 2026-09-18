package bridge_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported bridge.Config field
// reaches bridge.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	pvid := vlan.ID(10)
	seed := bridge.Config{
		AgingTime:      300 * time.Second,
		MaxEntries:     1024,
		FloodVLANs:     []vlan.ID{10},
		ProtectedPorts: []string{"1/1/1"},
		ForwardBPDU:    true,
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "ten"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					PVID:             &pvid,
					Tagged:           []vlan.ID{20},
					Untagged:         []vlan.ID{10},
					IngressFiltering: true,
					// TaggedOnly, not the All zero-value default: perturbing to "" must
					// read as a real change, not normalize back to the seed's own value.
					Admission: bridge.TaggedOnly,
					Tunnel: &bridge.Tunnel{
						VID:          30,
						CustomerVIDs: []vlan.ID{100},
						TPID:         0x88a8,
					},
					PriorityTags: bridge.PriorityTagsIfNonzero,
				},
			},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, bridge.Config.Normalize, bridge.Diff, nil)
}
