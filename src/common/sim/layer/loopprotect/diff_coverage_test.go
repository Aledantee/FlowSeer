package loopprotect_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/trace"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField verifies that every exported loopprotect.Config field
// reaches loopprotect.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "loopprotect")
}

func TestDiffCollectionCorrespondence(t *testing.T) {
	cases := []struct {
		name string
		a, b loopprotect.Config
		want []simtest.ChangeExpectation
	}{
		{name: "vlans/insert", a: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{30, 10}}}}, b: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{30, 20, 10}}}}, want: []simtest.ChangeExpectation{{Layer: "loopprotect", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "vlans[\"20\"]", To: &simtest.FactExpectation{TypeID: "loopprotect.vlan_id", Canonical: "20"}}}},
		{name: "vlans/delete", a: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{30, 20, 10}}}}, b: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{30, 10}}}}, want: []simtest.ChangeExpectation{{Layer: "loopprotect", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "vlans[\"20\"]", From: &simtest.FactExpectation{TypeID: "loopprotect.vlan_id", Canonical: "20"}}}},
		{name: "vlans/replace", a: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20, 10}}}}, b: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{30, 10}}}}, want: []simtest.ChangeExpectation{{Layer: "loopprotect", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "vlans[\"20\"]", From: &simtest.FactExpectation{TypeID: "loopprotect.vlan_id", Canonical: "20"}}, {Layer: "loopprotect", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "vlans[\"30\"]", To: &simtest.FactExpectation{TypeID: "loopprotect.vlan_id", Canonical: "30"}}}},
		{name: "vlans/reorder", a: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10, 20}}}}, b: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20, 10}}}}, want: []simtest.ChangeExpectation{}},
		{name: "vlans/duplicate", a: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20, 10, 20}}}}, b: loopprotect.Config{Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10, 20}}}}, want: []simtest.ChangeExpectation{}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a, b := tc.a, tc.b
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := loopprotect.Diff(a, b)
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
