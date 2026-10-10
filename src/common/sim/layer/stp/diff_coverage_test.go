package stp_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestDiffCoversEveryConfigField verifies that every exported stp.Config field reaches
// stp.Diff. MST and PVST are mutually exclusive at construction (Config.Validate), which
// this fixture never calls; Diff and Normalize take the raw struct, so seeding both
// together reaches every leaf of each in one pass.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "stp")
}

func TestDiffCoverageRejectsOmittedPointToPoint(t *testing.T) {
	simtest.AssertMappedDiffRejectsOmittedFields(t, "stp", map[string]string{
		"stp.Config.Ports[1/1/1].PointToPoint": "admin_point_to_point",
	})
}

func TestDiffVLANOccurrenceCorrespondence(t *testing.T) {
	remove10 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: "1"}, Field: `vlans["10"]`, From: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "10"}}
	add10 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: "1"}, Field: `vlans["10"]`, To: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "10"}}
	add20 := simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "mst_instance", Key: "1"}, Field: `vlans["20"]`, To: &simtest.FactExpectation{TypeID: "stp.mst.vlan_id", Canonical: "20"}}
	cases := []struct {
		name string
		a, b []vlan.ID
		want []simtest.ChangeExpectation
	}{
		{"insert", []vlan.ID{10, 10}, []vlan.ID{10, 10, 10}, []simtest.ChangeExpectation{add10}},
		{"delete", []vlan.ID{10, 10}, []vlan.ID{10}, []simtest.ChangeExpectation{remove10}},
		{"replace_both", []vlan.ID{10, 10}, []vlan.ID{20, 20}, []simtest.ChangeExpectation{remove10, remove10, add20, add20}},
		{"reorder", []vlan.ID{10, 20}, []vlan.ID{20, 10}, []simtest.ChangeExpectation{}},
		{"deduplicate", []vlan.ID{10, 10, 20}, []vlan.ID{10, 20}, []simtest.ChangeExpectation{remove10}},
		{"remove_two", []vlan.ID{10, 10, 10}, []vlan.ID{10}, []simtest.ChangeExpectation{remove10, remove10}},
		{"add_two", []vlan.ID{10}, []vlan.ID{10, 10, 10}, []simtest.ChangeExpectation{add10, add10}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a := stp.Config{MST: &stp.MST{Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: tc.a}}}}
				b := stp.Config{MST: &stp.MST{Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: tc.b}}}}
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := stp.Diff(a, b)
				hits++
				if len(got) != len(tc.want) {
					t.Fatalf("normalized=%v: Diff records = %d, want %d: %+v", normalized, len(got), len(tc.want), got)
				}
				for i, want := range tc.want {
					if !want.Matches(got[i]) {
						t.Errorf("normalized=%v: Diff[%d] = %+v, want %+v", normalized, i, got[i], want)
					}
				}
			}
		})
	}
	if hits != 14 {
		t.Errorf("concrete occurrence expectations = %d, want 14", hits)
	}
}

func TestDiffCapabilityPresence(t *testing.T) {
	cases := []struct {
		name string
		a, b stp.Config
		want simtest.ChangeExpectation
	}{
		{"mst_add", stp.Config{}, stp.Config{MST: &stp.MST{}}, simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "bridge"}, Field: "mst.presence", From: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "false"}, To: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "true"}}},
		{"mst_remove", stp.Config{MST: &stp.MST{}}, stp.Config{}, simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "bridge"}, Field: "mst.presence", From: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "true"}, To: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "false"}}},
		{"pvst_add", stp.Config{}, stp.Config{PVST: &stp.PVST{}}, simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "bridge"}, Field: "pvst.presence", From: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "false"}, To: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "true"}}},
		{"pvst_remove", stp.Config{PVST: &stp.PVST{}}, stp.Config{}, simtest.ChangeExpectation{Layer: "stp", Subject: trace.Subject{Kind: "bridge"}, Field: "pvst.presence", From: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "true"}, To: &simtest.FactExpectation{TypeID: "stp.bool", Canonical: "false"}}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a, b := tc.a, tc.b
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := stp.Diff(a, b)
				hits++
				if len(got) != 1 || !tc.want.Matches(got[0]) {
					t.Errorf("normalized=%v: Diff = %+v, want %+v", normalized, got, tc.want)
				}
			}
		})
	}
	if hits != 8 {
		t.Errorf("concrete presence expectations = %d, want 8", hits)
	}
}
