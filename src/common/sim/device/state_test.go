package device

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestStateProjection(t *testing.T) {
	classified := map[string]bool{}
	for _, f := range stateFields {
		if classified[f.name] {
			t.Fatalf("duplicate class for %s", f.name)
		}
		classified[f.name] = true
	}
	typ := reflect.TypeFor[State]()
	for f := range typ.Fields() {
		if !classified[f.Name] {
			t.Errorf("State.%s has no class", f.Name)
		}
	}
	for field := range reflect.TypeFor[PortInfo]().Fields() {
		if _, ok := portInfoFields[field.Name]; !ok {
			t.Errorf("PortInfo.%s has no class", field.Name)
		}
	}
	base := func() State {
		return State{
			Entries: []Entry{{Port: "p"}}, Groups: map[vlan.ID][]GroupEntry{1: {{Port: "p"}}},
			RouterPorts: map[vlan.ID][]RouterPort{1: {{Port: "p"}}}, Ports: []port.Port{{Name: "p", OperStatus: port.Up}},
			Power: Allocation{Ports: map[string]PortAllocation{"p": {}}}, Roles: map[string]PortInfo{"p": {}},
			TreeRoles: map[TreeRef]map[string]PortInfo{{Kind: TreeCIST}: {"p": {}}},
			Neighbors: []NeighborEntry{{Interface: "p"}}, Counters: map[string]PortCounters{"p": {}},
		}
	}
	cases := []struct {
		name    string
		mutate  func(*State)
		changes bool
	}{
		{"Entries", func(s *State) { s.Entries[0].Port = "q" }, true},
		{"FDB origin", func(s *State) { s.Entries[0].Origin = Observed }, true},
		{"FDB lifetime", func(s *State) { s.Entries[0].Lifetime = Static }, true},
		{"Groups", func(s *State) { s.Groups[1][0].Port = "q" }, true},
		{"RouterPorts", func(s *State) { s.RouterPorts[1][0].Port = "q" }, true},
		{"Ports", func(s *State) { s.Ports[0].OperStatus = port.Down }, true},
		{"Power", func(s *State) { s.Power.Ports["p"] = PortAllocation{State: PowerDenied} }, true},
		{"Roles", func(s *State) { s.Roles["p"] = PortInfo{State: StateForwarding} }, true},
		{"TreeRoles", func(s *State) { s.TreeRoles[TreeRef{Kind: TreeCIST}]["p"] = PortInfo{State: StateForwarding} }, true},
		{"Neighbors", func(s *State) { s.Neighbors[0].State = NeighborReachable }, true},
		{"neighbor VRF", func(s *State) { s.Neighbors[0].VRF = "other" }, true},
		{"neighbor hold depth", func(s *State) { s.Neighbors[0].HoldDepth = 1 }, true},
		{"Counters", func(s *State) { s.Counters["p"] = PortCounters{InBytes: 1} }, false},
		{"RelayCounters", func(s *State) { s.RelayCounters.Learned = 1 }, false},
		{"ForwardTransitions", func(s *State) {
			s.Roles["p"] = PortInfo{ForwardTransitions: 1}
			s.TreeRoles[TreeRef{Kind: TreeCIST}]["p"] = PortInfo{ForwardTransitions: 1}
		}, false},
		{"TxBPDUs", func(s *State) {
			s.Roles["p"] = PortInfo{TxBPDUs: 1}
			s.TreeRoles[TreeRef{Kind: TreeCIST}]["p"] = PortInfo{TxBPDUs: 1}
		}, false},
		{"RxBPDUs", func(s *State) {
			s.Roles["p"] = PortInfo{RxBPDUs: 1}
			s.TreeRoles[TreeRef{Kind: TreeCIST}]["p"] = PortInfo{RxBPDUs: 1}
		}, false},
		{"BadBPDUs", func(s *State) {
			s.Roles["p"] = PortInfo{BadBPDUs: 1}
			s.TreeRoles[TreeRef{Kind: TreeCIST}]["p"] = PortInfo{BadBPDUs: 1}
		}, false},
	}
	reached := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := base(), base()
			tc.mutate(&b)
			if got := a.Fingerprint() != b.Fingerprint(); got != tc.changes {
				t.Errorf("fingerprint changed = %t, want %t", got, tc.changes)
			}
			field, current, expected := a.Diff(b)
			if got := field != ""; got != tc.changes {
				t.Errorf("diff changed = %t, want %t (%s)", got, tc.changes, field)
			}
			if tc.changes && current == expected {
				t.Errorf("diff values = %q and %q, want differing entries", current, expected)
			}
			reached++
		})
	}
	if reached != 18 {
		t.Fatalf("cases reaching expectations = %d, want 18", reached)
	}
}
