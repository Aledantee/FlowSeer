package mcast_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/trace"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField verifies that every exported mcast.Config field reaches
// mcast.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "mcast")
}

func TestDiffCollectionCorrespondence(t *testing.T) {
	cases := []struct {
		name string
		a, b mcast.Config
		want []simtest.ChangeExpectation
	}{
		{name: "router_ports/insert", a: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/3", "1/1/1"}}}}, b: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/3", "1/1/2", "1/1/1"}}}}, want: []simtest.ChangeExpectation{{Layer: "mcast", Subject: trace.Subject{Kind: "vlan", Key: "10"}, Field: "router_ports[\"1/1/2\"]", To: &simtest.FactExpectation{TypeID: "mcast.router_port", Canonical: "1/1/2"}}}},
		{name: "router_ports/delete", a: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/3", "1/1/2", "1/1/1"}}}}, b: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/3", "1/1/1"}}}}, want: []simtest.ChangeExpectation{{Layer: "mcast", Subject: trace.Subject{Kind: "vlan", Key: "10"}, Field: "router_ports[\"1/1/2\"]", From: &simtest.FactExpectation{TypeID: "mcast.router_port", Canonical: "1/1/2"}}}},
		{name: "router_ports/replace", a: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/2", "1/1/1"}}}}, b: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/3", "1/1/1"}}}}, want: []simtest.ChangeExpectation{{Layer: "mcast", Subject: trace.Subject{Kind: "vlan", Key: "10"}, Field: "router_ports[\"1/1/2\"]", From: &simtest.FactExpectation{TypeID: "mcast.router_port", Canonical: "1/1/2"}}, {Layer: "mcast", Subject: trace.Subject{Kind: "vlan", Key: "10"}, Field: "router_ports[\"1/1/3\"]", To: &simtest.FactExpectation{TypeID: "mcast.router_port", Canonical: "1/1/3"}}}},
		{name: "router_ports/reorder", a: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/1", "1/1/2"}}}}, b: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/2", "1/1/1"}}}}, want: []simtest.ChangeExpectation{}},
		{name: "router_ports/duplicate", a: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/2", "1/1/1", "1/1/2"}}}}, b: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {RouterPorts: []string{"1/1/1", "1/1/2"}}}}, want: []simtest.ChangeExpectation{}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a, b := tc.a, tc.b
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := mcast.Diff(a, b)
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
	if hits != 10 {
		t.Errorf("concrete correspondence expectations = %d, want 10", hits)
	}
}
