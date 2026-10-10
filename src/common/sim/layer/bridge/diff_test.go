package bridge_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestDiffIgnoresSetOrderAndTheAgingDefault(t *testing.T) {
	a := bridge.Config{VLAN: &bridge.VLAN{
		Table:       map[vlan.ID]string{10: "", 20: ""},
		Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{10, 20}}},
	}}
	b := bridge.Config{AgingTime: bridge.DefaultAgingTime, VLAN: &bridge.VLAN{
		Table:       map[vlan.ID]string{10: "", 20: ""},
		Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{20, 10}}},
	}}
	if changes := bridge.Diff(a, b); len(changes) != 0 {
		t.Errorf("Diff = %+v, want no change between configurations that build the same bridge", changes)
	}
}

func TestDiffDistinguishesVLANAwareness(t *testing.T) {
	t.Parallel()

	changes := bridge.Diff(bridge.Config{}, bridge.Config{VLAN: &bridge.VLAN{}})
	if len(changes) != 1 {
		t.Fatalf("len(Diff()) = %d, want 1: %+v", len(changes), changes)
	}
	if got := changes[0]; got.Field != "vlan.presence" || got.From.TypeID() != "bridge.bool" || got.To.TypeID() != "bridge.bool" || got.From.Canonical() != "false" || got.To.Canonical() != "true" {
		t.Errorf("Diff()[0] = %+v, want vlan.presence false -> true", got)
	}
}

func TestDiff(t *testing.T) {
	cfgA := bridge.Config{
		AgingTime: 300 * time.Second,
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10",
				20: "vlan20",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
				},
				"1/1/2": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
				},
			},
		},
	}

	cfgB := bridge.Config{
		AgingTime: 600 * time.Second,
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10-renamed",
				30: "vlan30",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/2": {
					PVID:             mustVLAN(20),
					Tagged:           []vlan.ID{30},
					Untagged:         []vlan.ID{20},
					IngressFiltering: true,
					Admission:        bridge.TaggedOnly,
				},
				"1/1/3": {
					PVID:     mustVLAN(30),
					Untagged: []vlan.ID{30},
				},
			},
		},
	}

	changes := bridge.Diff(cfgA, cfgB)

	hasChange := func(field string, kind, key string) bool {
		for _, ch := range changes {
			if ch.Field == field && ch.Subject.Kind == kind && ch.Subject.Key == key {
				return true
			}
		}

		return false
	}

	if !hasChange("aging_time", "bridge", "") {
		t.Errorf("missing aging_time change")
	}
	if !hasChange("name", "vlan", "10") {
		t.Errorf("missing vlan 10 name rename")
	}
	if !hasChange("", "vlan", "20") {
		t.Errorf("missing vlan 20 removal")
	}
	if !hasChange("", "vlan", "30") {
		t.Errorf("missing vlan 30 addition")
	}
	if !hasChange("", "port", "1/1/1") {
		t.Errorf("missing port 1/1/1 removal")
	}
	if !hasChange("", "port", "1/1/3") {
		t.Errorf("missing port 1/1/3 addition")
	}
	if !hasChange("pvid", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 pvid change")
	}
	if !hasChange(`untagged_vlan_ids["10"]`, "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 untagged_vlan_ids change")
	}
	if !hasChange(`untagged_vlan_ids["20"]`, "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 untagged VLAN addition")
	}
	if !hasChange(`tagged_vlan_ids["30"]`, "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 tagged_vlan_ids change")
	}
	if !hasChange("ingress_filtering", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 ingress_filtering change")
	}
	if !hasChange("frame_admission", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 frame_admission change")
	}
}

func TestDiffReportsMaxEntriesChange(t *testing.T) {
	a := bridge.Config{MaxEntries: 0}
	b := bridge.Config{MaxEntries: 2}
	changes := bridge.Diff(a, b)
	if len(changes) != 1 {
		t.Fatalf("Diff returned %d changes, want 1", len(changes))
	}
	ch := changes[0]
	if ch.Field != "max_entries" || ch.From.TypeID() != "bridge.int" || ch.To.TypeID() != "bridge.int" || ch.From.Canonical() != "0" || ch.To.Canonical() != "2" || ch.Layer != bridge.LayerName {
		t.Errorf("Diff change = %+v, want max_entries From: 0 To: 2 at LayerRelay", ch)
	}
}

func TestDiffReportsFloodVLANsProtectedPortsAndForwardBPDU(t *testing.T) {
	t.Run("flood_vlans order change reports nothing", func(t *testing.T) {
		a := bridge.Config{FloodVLANs: []vlan.ID{10, 20}}
		b := bridge.Config{FloodVLANs: []vlan.ID{20, 10}}
		if changes := bridge.Diff(a, b); len(changes) != 0 {
			t.Errorf("Diff = %+v, want no changes when only flood_vlans order changes", changes)
		}
	})

	t.Run("flood_vlans change is reported sorted", func(t *testing.T) {
		a := bridge.Config{FloodVLANs: []vlan.ID{20, 10}}
		b := bridge.Config{FloodVLANs: []vlan.ID{30, 10}}
		changes := bridge.Diff(a, b)
		assertBridgeChanges(t, changes, []simtest.ChangeExpectation{
			{Layer: "relay", Subject: trace.Subject{Kind: "bridge"}, Field: `flood_vlans["20"]`, From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}},
			{Layer: "relay", Subject: trace.Subject{Kind: "bridge"}, Field: `flood_vlans["30"]`, To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "30"}},
		})
	})

	t.Run("protected_ports order change reports nothing", func(t *testing.T) {
		a := bridge.Config{ProtectedPorts: []string{"1/1/2", "1/1/1"}}
		b := bridge.Config{ProtectedPorts: []string{"1/1/1", "1/1/2"}}
		if changes := bridge.Diff(a, b); len(changes) != 0 {
			t.Errorf("Diff = %+v, want no changes when only protected_ports order changes", changes)
		}
	})

	t.Run("protected_ports change is reported sorted", func(t *testing.T) {
		a := bridge.Config{ProtectedPorts: []string{"1/1/2", "1/1/1"}}
		b := bridge.Config{ProtectedPorts: []string{"1/1/3", "1/1/1"}}
		changes := bridge.Diff(a, b)
		assertBridgeChanges(t, changes, []simtest.ChangeExpectation{
			{Layer: "relay", Subject: trace.Subject{Kind: "bridge"}, Field: `protected_ports["1/1/2"]`, From: &simtest.FactExpectation{TypeID: "bridge.port", Canonical: "1/1/2"}},
			{Layer: "relay", Subject: trace.Subject{Kind: "bridge"}, Field: `protected_ports["1/1/3"]`, To: &simtest.FactExpectation{TypeID: "bridge.port", Canonical: "1/1/3"}},
		})
	})

	t.Run("forward_bpdu change is reported", func(t *testing.T) {
		a := bridge.Config{ForwardBPDU: false}
		b := bridge.Config{ForwardBPDU: true}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "forward_bpdu" || ch.From.TypeID() != "bridge.bool" || ch.To.TypeID() != "bridge.bool" || ch.From.Canonical() != "false" || ch.To.Canonical() != "true" || ch.Layer != bridge.LayerName {
			t.Errorf("change = %+v, want forward_bpdu From: false To: true at LayerRelay", ch)
		}
	})
}

func TestDiffTunnelAndPriorityTags(t *testing.T) {
	t.Run("tunnel added", func(t *testing.T) {
		a := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {},
			},
		}}
		b := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10}},
			},
		}}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "tunnel.presence" || ch.Layer != bridge.LayerNameVLAN {
			t.Errorf("change = %+v, want field tunnel.presence at LayerVLAN", ch)
		}
		if ch.From != nil {
			t.Errorf("From = %v, want nil", ch.From)
		}
		if ch.To == nil || ch.To.TypeID() != "bridge.tunnel" || ch.To.Canonical() != "vid=10;tpid=34984;customer_vids=[]" {
			t.Errorf("To = %+v (%T), want a complete tunnel snapshot", ch.To, ch.To)
		}
	})

	t.Run("customer list order change reports nothing", func(t *testing.T) {
		a := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10, CustomerVIDs: []vlan.ID{100, 200}}},
			},
		}}
		b := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10, CustomerVIDs: []vlan.ID{200, 100}}},
			},
		}}
		if changes := bridge.Diff(a, b); len(changes) != 0 {
			t.Errorf("Diff = %+v, want no changes when only customer VIDs order changes", changes)
		}
	})

	t.Run("priority_tags change is reported", func(t *testing.T) {
		a := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PriorityTags: bridge.PriorityTagsNever},
			},
		}}
		b := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PriorityTags: bridge.PriorityTagsAlways},
			},
		}}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "priority_tags" || ch.From != bridge.PriorityTagsNever || ch.To != bridge.PriorityTagsAlways {
			t.Errorf("change = %+v, want priority_tags From: Never To: Always", ch)
		}
	})
}

func TestBridgeSnapshotFactsAreLosslessAndImmutable(t *testing.T) {
	t.Parallel()

	taggedA := []vlan.ID{10}
	configForSwitchport := func(tagged []vlan.ID) bridge.Config {
		return bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{
			"1/1/1": {Tagged: tagged},
		}}}
	}
	base := bridge.Config{VLAN: &bridge.VLAN{}}
	factA := bridge.Diff(base, configForSwitchport(taggedA))[0].To
	factB := bridge.Diff(base, configForSwitchport([]vlan.ID{20}))[0].To
	if factA.TypeID() != "bridge.switchport" {
		t.Errorf("TypeID() = %q, want bridge.switchport", factA.TypeID())
	}
	if factA.Canonical() == factB.Canonical() {
		t.Errorf("different switchports share canonical form %q", factA.Canonical())
	}
	before := factA.Canonical()
	taggedA[0] = 30
	if got := factA.Canonical(); got != before {
		t.Errorf("switchport fact changed after source mutation: got %q, want %q", got, before)
	}

	customersA := []vlan.ID{100}
	configForTunnel := func(customers []vlan.ID) bridge.Config {
		return bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{
			"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10, CustomerVIDs: customers}},
		}}}
	}
	tunnelFactA := bridge.Diff(base, configForTunnel(customersA))[0].To
	tunnelFactB := bridge.Diff(base, configForTunnel([]vlan.ID{200}))[0].To
	if tunnelFactA.TypeID() != "bridge.switchport" {
		t.Errorf("TypeID() = %q, want bridge.switchport", tunnelFactA.TypeID())
	}
	if tunnelFactA.Canonical() == tunnelFactB.Canonical() {
		t.Errorf("different tunnel switchports share canonical form %q", tunnelFactA.Canonical())
	}
	before = tunnelFactA.Canonical()
	customersA[0] = 300
	if got := tunnelFactA.Canonical(); got != before {
		t.Errorf("tunnel switchport fact changed after source mutation: got %q, want %q", got, before)
	}

	directA := configForTunnel([]vlan.ID{100})
	directB := configForTunnel([]vlan.ID{200})
	retained := bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {}}}}
	directFactA := bridge.Diff(retained, directA)[0].To
	directFactB := bridge.Diff(retained, directB)[0].To
	if directFactA.TypeID() != "bridge.tunnel" {
		t.Errorf("TypeID() = %q, want bridge.tunnel", directFactA.TypeID())
	}
	if directFactA.Canonical() == directFactB.Canonical() {
		t.Errorf("different tunnels share canonical form %q", directFactA.Canonical())
	}
}

func assertBridgeChanges(t *testing.T, got []trace.Change, want []simtest.ChangeExpectation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Diff records = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, expected := range want {
		if !expected.Matches(got[i]) {
			t.Errorf("Diff[%d] = %+v, want %+v", i, got[i], expected)
		}
	}
}

func TestDiffRetainedTunnelMembers(t *testing.T) {
	a := bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30, CustomerVIDs: []vlan.ID{100}, TPID: 0}}}}}
	b := bridge.Config{MaxEntries: 2, VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 31, CustomerVIDs: []vlan.ID{200}, TPID: 33024}}}}}
	want := []simtest.ChangeExpectation{
		{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.vid", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "30"}, To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "31"}},
		{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.customer_vids[\"100\"]", From: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "100"}},
		{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.customer_vids[\"200\"]", To: &simtest.FactExpectation{TypeID: "bridge.vlan_id", Canonical: "200"}},
		{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.tpid", From: &simtest.FactExpectation{TypeID: "bridge.tpid", Canonical: "34984"}, To: &simtest.FactExpectation{TypeID: "bridge.tpid", Canonical: "33024"}},
		{Layer: "relay", Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "max_entries", From: &simtest.FactExpectation{TypeID: "bridge.int", Canonical: "0"}, To: &simtest.FactExpectation{TypeID: "bridge.int", Canonical: "2"}},
	}
	hits := 0
	for _, normalized := range []bool{false, true} {
		from, to := a, b
		if normalized {
			from, to = from.Normalize(layer.Env{}), to.Normalize(layer.Env{})
		}
		hits++
		assertBridgeChanges(t, bridge.Diff(from, to), want)
	}
	if hits != 2 {
		t.Errorf("concrete tunnel expectations = %d, want 2", hits)
	}
}

func TestDiffEffectiveTPID(t *testing.T) {
	cases := []struct {
		name string
		to   uint16
		want []simtest.ChangeExpectation
	}{
		{name: "default", to: 34984},
		{name: "active", to: 33024, want: []simtest.ChangeExpectation{
			{Layer: "vlan", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "tunnel.tpid", From: &simtest.FactExpectation{TypeID: "bridge.tpid", Canonical: "34984"}, To: &simtest.FactExpectation{TypeID: "bridge.tpid", Canonical: "33024"}},
		}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := bridge.Config{VLAN: &bridge.VLAN{Switchports: map[string]bridge.Switchport{"1/1/1": {Tunnel: &bridge.Tunnel{VID: 30}}}}}
			b := a.Clone()
			b.VLAN.Switchports["1/1/1"].Tunnel.TPID = tc.to
			hits++
			assertBridgeChanges(t, bridge.Diff(a, b), tc.want)
		})
	}
	if hits != 2 {
		t.Errorf("concrete TPID expectations = %d, want 2", hits)
	}
}
