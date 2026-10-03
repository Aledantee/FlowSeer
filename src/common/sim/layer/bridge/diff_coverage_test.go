package bridge_test

import (
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// TestDiffCoversEveryConfigField verifies that every exported bridge.Config field reaches
// bridge.Diff.
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

	normalize := func(c bridge.Config) bridge.Config { return c.Normalize(layer.Env{}) }
	simtest.AssertDiffCoversConfig(t, seed, normalize, bridge.Diff, nil)
}

// TestRetentionKeyCoversEveryConfigField verifies that every exported bridge.Config field
// affects bridge.RetentionKey.
func TestRetentionKeyCoversEveryConfigField(t *testing.T) {
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
					Admission:        bridge.TaggedOnly,
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

	keyFn := func(c bridge.Config) string {
		k := bridge.RetentionKey(c, layer.Env{})
		if idx := strings.IndexByte(k, '\n'); idx != -1 {
			return k[:idx]
		}
		return k
	}
	simtest.AssertRetentionKeyCoversConfig(t, seed, keyFn, nil)
}

func TestRetentionKeyDistinguishesPortOperStatus(t *testing.T) {
	t.Parallel()
	cfg := bridge.Config{ProtectedPorts: []string{"1/1/1"}}
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", OperStatus: port.Up})
	ports1, err := b1.Build()
	if err != nil {
		t.Fatalf("build ports1: %v", err)
	}
	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", OperStatus: port.Down})
	ports2, err := b2.Build()
	if err != nil {
		t.Fatalf("build ports2: %v", err)
	}
	k1 := bridge.RetentionKey(cfg, layer.Env{Ports: ports1})
	k2 := bridge.RetentionKey(cfg, layer.Env{Ports: ports2})
	if k1 == k2 {
		t.Errorf("RetentionKey did not distinguish OperUp and OperDown:\nk1: %s\nk2: %s", k1, k2)
	}
}

func TestRetentionKeyZeroConfigMatchesDefaultConfig(t *testing.T) {
	t.Parallel()
	env := layer.Env{}
	k0 := bridge.RetentionKey(bridge.Config{}, env)
	kDef := bridge.RetentionKey(bridge.Config{AgingTime: bridge.DefaultAgingTime}, env)
	if k0 != kDef {
		t.Errorf("RetentionKey(Config{}, env) = %q, want %q", k0, kDef)
	}
}
