package routing_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported routing.Config field
// reaches routing.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	seed := routing.Config{
		VRFs: map[string]routing.VRF{
			"default": {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN:     10,
						Port:     "1/1/1",
						MAC:      netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
					},
				},
				Routes: []routing.Route{
					{
						Prefix:     netip.MustParsePrefix("10.0.20.0/24"),
						NextHop:    netip.MustParseAddr("10.0.10.7"),
						Interface:  "vlan10",
						Preference: 1,
						Metric:     10,
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.10.7"),
						MAC:       netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
					},
				},
				NeighborPolicy: routing.NeighborPolicy{
					// NeighborDisabled, not the zero-value NeighborObserved default:
					// perturbing to "" must read as a real change, not normalize back
					// to the seed's own value.
					Mode:              routing.NeighborDisabled,
					ReachableTime:     30 * time.Second,
					ResolutionTimeout: 3 * time.Second,
					HoldDepth:         4,
				},
			},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, routing.Config.Normalize, routing.Diff, nil)
}
