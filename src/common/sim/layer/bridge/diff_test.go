package bridge_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
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
	if got := changes[0]; got.Field != "vlan_awareness" || got.From.TypeID() != "bridge.bool" || got.To.TypeID() != "bridge.bool" || got.From.Canonical() != "false" || got.To.Canonical() != "true" {
		t.Errorf("Diff()[0] = %+v, want vlan_awareness false -> true", got)
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
	if !hasChange("untagged_vlan_ids", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 untagged_vlan_ids change")
	}
	if !hasChange("tagged_vlan_ids", "port", "1/1/2") {
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
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "flood_vlans" || ch.Layer != bridge.LayerName {
			t.Errorf("change = %+v, want flood_vlans at LayerRelay", ch)
		}
		wantFrom := []vlan.ID{10, 20}
		wantTo := []vlan.ID{10, 30}
		if !trace.EqualFact(ch.From, bridge.VLANsFact(wantFrom)) || !trace.EqualFact(ch.To, bridge.VLANsFact(wantTo)) {
			t.Errorf("From = %v, To = %v, want From = %v, To = %v", ch.From, ch.To, wantFrom, wantTo)
		}
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
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "protected_ports" || ch.Layer != bridge.LayerName {
			t.Errorf("change = %+v, want protected_ports at LayerRelay", ch)
		}
		wantFrom := []string{"1/1/1", "1/1/2"}
		wantTo := []string{"1/1/1", "1/1/3"}
		if !trace.EqualFact(ch.From, bridge.StringsFact(wantFrom)) || !trace.EqualFact(ch.To, bridge.StringsFact(wantTo)) {
			t.Errorf("From = %v, To = %v, want From = %v, To = %v", ch.From, ch.To, wantFrom, wantTo)
		}
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
		if ch.Field != "tunnel" || ch.Layer != bridge.LayerNameVLAN {
			t.Errorf("change = %+v, want field tunnel at LayerVLAN", ch)
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
	directFactA := bridge.Diff(directA, directB)[0].From
	directFactB := bridge.Diff(directA, directB)[0].To
	if directFactA.TypeID() != "bridge.tunnel" {
		t.Errorf("TypeID() = %q, want bridge.tunnel", directFactA.TypeID())
	}
	if directFactA.Canonical() == directFactB.Canonical() {
		t.Errorf("different tunnels share canonical form %q", directFactA.Canonical())
	}
}
