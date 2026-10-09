package fabric_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
)

func TestInsertedActionReportsOne(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := fabric.Scenario{Name: "insert"}
	for i := range 5 {
		a.Actions = append(a.Actions, fabric.Action{ID: fmt.Sprintf("existing-%d", i), At: at.Add(time.Duration(i+1) * time.Second), Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: fmt.Sprintf("h%d", i)}}})
	}
	b := a.Clone()
	b.Actions = append([]fabric.Action{{ID: "inserted", At: at, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "new"}}}}, b.Actions...)
	changes, err := fabric.DiffScenarios(a, b)
	if err != nil || len(changes) != 1 || changes[0].Field != "action" || changes[0].Subject.Key != "inserted" {
		t.Fatalf("inserted action changes = %+v, error %v, want one action", changes, err)
	}
}

func TestDiffScenariosEveryInjectionField(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := fabric.Scenario{Name: "fields", Actions: []fabric.Action{{ID: "scenario_identity_test-3", At: at, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h"}, Packet: &fabric.Packet{To: netip.MustParseAddr("192.0.2.1"), Protocol: 17, Payload: []byte("a")}, Flow: 1}}}}
	cases := []struct {
		name   string
		mutate func(*fabric.Injection)
	}{
		{"Packet", func(i *fabric.Injection) { i.Packet.Payload[0] = 'b' }},
		{"Packet destination", func(i *fabric.Injection) { i.Packet.To = netip.MustParseAddr("192.0.2.2") }},
		{"Packet protocol", func(i *fabric.Injection) { i.Packet.Protocol = 6 }},
		{"Packet presence", func(i *fabric.Injection) { i.Packet = nil }},
		{"Retention", func(i *fabric.Injection) { i.Retention = fabric.RetainAggregate }},
		{"Flow", func(i *fabric.Injection) { i.Flow = 2 }},
	}
	checked := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := a.Clone()
			if changes, err := fabric.DiffScenarios(a, b); err != nil || len(changes) != 0 {
				t.Fatalf("unchanged clone diff = %+v, error %v", changes, err)
			}
			tc.mutate(b.Actions[0].Inject)
			changes, err := fabric.DiffScenarios(a, b)
			if err != nil || len(changes) != 1 || changes[0].Field != "inject" || changes[0].From.Canonical() == changes[0].To.Canonical() {
				t.Fatalf("%s changes = %+v, error %v, want one distinct injection summary", tc.name, changes, err)
			}
			checked++
		})
	}
	if checked != 6 {
		t.Fatalf("checked %d injection cases, want 6", checked)
	}
}

func TestActionSummaryNamesLoss(t *testing.T) {
	a := fabric.Action{ID: "scenario_identity_test-4", At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Kind: fabric.ActionFault, Fault: &fabric.FaultAction{A: fabric.Endpoint{Node: "a"}, B: fabric.Endpoint{Node: "b"}, Fault: cablemodel.Fault{Kind: cablemodel.FaultLoseEveryNth, N: 2}}}
	b := a.Clone()
	b.Fault.Fault.N = 3
	changes := a.Diff(b)
	if len(changes) != 1 || !strings.Contains(changes[0].From.Canonical(), "n=2") || !strings.Contains(changes[0].To.Canonical(), "n=3") {
		t.Fatalf("loss changes = %+v, want interval 2 against 3", changes)
	}
}

func TestRunOptionsValidation(t *testing.T) {
	cases := []struct {
		name    string
		options fabric.RunOptions
		valid   bool
	}{
		{"zero budget", fabric.RunOptions{}, false},
		{"negative budget", fabric.RunOptions{Budget: -5}, false},
		{"negative window", fabric.RunOptions{Budget: 10, Window: -1}, false},
		{"zero window", fabric.RunOptions{Budget: 10}, true},
		{"positive window", fabric.RunOptions{Budget: 10, Window: 3}, true},
	}
	checked := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.options.Validate(); (err == nil) != tc.valid {
				t.Fatalf("Validate(%+v) = %v, want valid=%t", tc.options, err, tc.valid)
			}
			checked++
		})
	}
	if checked != 5 {
		t.Fatalf("checked %d options, want 5", checked)
	}
}

func TestScenarioRequiresUniqueActionIDs(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := fabric.Action{ID: "one", At: at, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h"}}}
	s := fabric.Scenario{Name: "identity", Actions: []fabric.Action{a}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Actions[0].ID = ""
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "action id cannot be empty") {
		t.Fatalf("empty ID error = %v", err)
	}
	s.Actions[0].ID = "one"
	a.At = at.Add(time.Second)
	s.Actions = append(s.Actions, a)
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate action id") {
		t.Fatalf("duplicate ID at different time error = %v", err)
	}
}
