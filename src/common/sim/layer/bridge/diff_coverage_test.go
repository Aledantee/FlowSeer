package bridge_test

import (
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/trace"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// TestDiffCoversEveryConfigField verifies that every exported bridge.Config field reaches
// bridge.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "bridge")
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
	cfg := bridge.Config{}
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

func TestRetentionKeyDistinguishesPortAdminStatus(t *testing.T) {
	t.Parallel()
	cfg := bridge.Config{}
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", AdminStatus: port.Up})
	ports1, err := b1.Build()
	if err != nil {
		t.Fatalf("build ports1: %v", err)
	}
	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", AdminStatus: port.Down})
	ports2, err := b2.Build()
	if err != nil {
		t.Fatalf("build ports2: %v", err)
	}
	k1 := bridge.RetentionKey(cfg, layer.Env{Ports: ports1})
	k2 := bridge.RetentionKey(cfg, layer.Env{Ports: ports2})
	if k1 == k2 {
		t.Errorf("RetentionKey did not distinguish AdminUp and AdminDown:\nk1: %s\nk2: %s", k1, k2)
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

func TestDiffCollectionCorrespondence(t *testing.T) {
	cases := []struct {
		name string
		a, b bridge.Config
		want []simtest.ChangeExpectation
	}{
		{name: "flood_vlans/insert", a: bridge.Config{FloodVLANs: []vlan.ID{30, 10}}, b: bridge.Config{FloodVLANs: []vlan.ID{30, 20, 10}}, want: []simtest.ChangeExpectation{{Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "flood_vlans[\"20\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "flood_vlans/delete", a: bridge.Config{FloodVLANs: []vlan.ID{30, 20, 10}}, b: bridge.Config{FloodVLANs: []vlan.ID{30, 10}}, want: []simtest.ChangeExpectation{{Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "flood_vlans[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "flood_vlans/replace", a: bridge.Config{FloodVLANs: []vlan.ID{20, 10}}, b: bridge.Config{FloodVLANs: []vlan.ID{30, 10}}, want: []simtest.ChangeExpectation{{Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "flood_vlans[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}, {Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "flood_vlans[\"30\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "30"}}}},
		{name: "flood_vlans/reorder", a: bridge.Config{FloodVLANs: []vlan.ID{10, 20}}, b: bridge.Config{FloodVLANs: []vlan.ID{20, 10}}, want: []simtest.ChangeExpectation{}},
		{name: "flood_vlans/duplicate", a: bridge.Config{FloodVLANs: []vlan.ID{20, 10, 20}}, b: bridge.Config{FloodVLANs: []vlan.ID{10, 20}}, want: []simtest.ChangeExpectation{}},
		{name: "tagged_vlan_ids/insert", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{30, 10}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{30, 20, 10}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tagged_vlan_ids[\"20\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "tagged_vlan_ids/delete", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{30, 20, 10}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{30, 10}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tagged_vlan_ids[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "tagged_vlan_ids/replace", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{20, 10}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{30, 10}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tagged_vlan_ids[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}, {Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tagged_vlan_ids[\"30\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "30"}}}},
		{name: "tagged_vlan_ids/reorder", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{10, 20}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{20, 10}}}}}, want: []simtest.ChangeExpectation{}},
		{name: "tagged_vlan_ids/duplicate", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{20, 10, 20}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{10, 20}}}}}, want: []simtest.ChangeExpectation{}},
		{name: "untagged_vlan_ids/insert", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{30, 10}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{30, 20, 10}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "untagged_vlan_ids[\"20\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "untagged_vlan_ids/delete", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{30, 20, 10}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{30, 10}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "untagged_vlan_ids[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "untagged_vlan_ids/replace", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{20, 10}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{30, 10}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "untagged_vlan_ids[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}, {Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "untagged_vlan_ids[\"30\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "30"}}}},
		{name: "untagged_vlan_ids/reorder", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{10, 20}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{20, 10}}}}}, want: []simtest.ChangeExpectation{}},
		{name: "untagged_vlan_ids/duplicate", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{20, 10, 20}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Untagged: []vlan.ID{10, 20}}}}}, want: []simtest.ChangeExpectation{}},
		{name: "tunnel.customer_vids/insert", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{30, 10}}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{30, 20, 10}}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.customer_vids[\"20\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "tunnel.customer_vids/delete", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{30, 20, 10}}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{30, 10}}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.customer_vids[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}}},
		{name: "tunnel.customer_vids/replace", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{20, 10}}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{30, 10}}}}}}, want: []simtest.ChangeExpectation{{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.customer_vids[\"20\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}}, {Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.customer_vids[\"30\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "30"}}}},
		{name: "tunnel.customer_vids/reorder", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{10, 20}}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{20, 10}}}}}}, want: []simtest.ChangeExpectation{}},
		{name: "tunnel.customer_vids/duplicate", a: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{20, 10, 20}}}}}}, b: bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{10, 20}}}}}}, want: []simtest.ChangeExpectation{}},
		{name: "protected_ports/insert", a: bridge.Config{ProtectedPorts: []string{"1/1/3", "1/1/1"}}, b: bridge.Config{ProtectedPorts: []string{"1/1/3", "1/1/2", "1/1/1"}}, want: []simtest.ChangeExpectation{{Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "protected_ports[\"1/1/2\"]", To: &simtest.FactExpectation{TypeID: "bridge.port", Canonical: "1/1/2"}}}},
		{name: "protected_ports/delete", a: bridge.Config{ProtectedPorts: []string{"1/1/3", "1/1/2", "1/1/1"}}, b: bridge.Config{ProtectedPorts: []string{"1/1/3", "1/1/1"}}, want: []simtest.ChangeExpectation{{Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "protected_ports[\"1/1/2\"]", From: &simtest.FactExpectation{TypeID: "bridge.port", Canonical: "1/1/2"}}}},
		{name: "protected_ports/replace", a: bridge.Config{ProtectedPorts: []string{"1/1/2", "1/1/1"}}, b: bridge.Config{ProtectedPorts: []string{"1/1/3", "1/1/1"}}, want: []simtest.ChangeExpectation{{Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "protected_ports[\"1/1/2\"]", From: &simtest.FactExpectation{TypeID: "bridge.port", Canonical: "1/1/2"}}, {Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "protected_ports[\"1/1/3\"]", To: &simtest.FactExpectation{TypeID: "bridge.port", Canonical: "1/1/3"}}}},
		{name: "protected_ports/reorder", a: bridge.Config{ProtectedPorts: []string{"1/1/1", "1/1/2"}}, b: bridge.Config{ProtectedPorts: []string{"1/1/2", "1/1/1"}}, want: []simtest.ChangeExpectation{}},
		{name: "protected_ports/duplicate", a: bridge.Config{ProtectedPorts: []string{"1/1/2", "1/1/1", "1/1/2"}}, b: bridge.Config{ProtectedPorts: []string{"1/1/1", "1/1/2"}}, want: []simtest.ChangeExpectation{}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a, b := tc.a, tc.b
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := bridge.Diff(a, b)
				hits++
				if len(got) != len(tc.want) {
					t.Fatalf("normalized=%v: Diff records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
				}
				for i, expected := range tc.want {
					if !expected.Matches(got[i]) {
						t.Errorf("normalized=%v: Diff[%d] = %+v, want %+v", normalized, i, got[i], expected)
					}
				}
			}
		})
	}
	if hits != 50 {
		t.Errorf("concrete correspondence expectations = %d, want 50", hits)
	}
}
