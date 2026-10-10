package phy_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/trace"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField verifies that every exported phy.Config field reaches
// phy.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "phy", simtest.PhyDiffAllowance())
}

func TestDiffCollectionCorrespondence(t *testing.T) {
	cases := []struct {
		name string
		a, b phy.Config
		want []simtest.ChangeExpectation
	}{
		{name: "supported_speeds_bps/insert", a: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{25000000000, 1000000000}}}}, b: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{25000000000, 10000000000, 1000000000}}}}, want: []simtest.ChangeExpectation{{Layer: "ethernet", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "supported_speeds_bps[\"10000000000\"]", To: &simtest.FactExpectation{TypeID: "phy.speed_bps", Canonical: "10000000000"}}}},
		{name: "supported_speeds_bps/delete", a: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{25000000000, 10000000000, 1000000000}}}}, b: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{25000000000, 1000000000}}}}, want: []simtest.ChangeExpectation{{Layer: "ethernet", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "supported_speeds_bps[\"10000000000\"]", From: &simtest.FactExpectation{TypeID: "phy.speed_bps", Canonical: "10000000000"}}}},
		{name: "supported_speeds_bps/replace", a: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{10000000000, 1000000000}}}}, b: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{25000000000, 1000000000}}}}, want: []simtest.ChangeExpectation{{Layer: "ethernet", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "supported_speeds_bps[\"10000000000\"]", From: &simtest.FactExpectation{TypeID: "phy.speed_bps", Canonical: "10000000000"}}, {Layer: "ethernet", Subject: trace.Subject{Kind: "port", Key: "1/1/1"}, Field: "supported_speeds_bps[\"25000000000\"]", To: &simtest.FactExpectation{TypeID: "phy.speed_bps", Canonical: "25000000000"}}}},
		{name: "supported_speeds_bps/reorder", a: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{1000000000, 10000000000}}}}, b: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{10000000000, 1000000000}}}}, want: []simtest.ChangeExpectation{}},
		{name: "supported_speeds_bps/duplicate", a: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{10000000000, 1000000000, 10000000000}}}}, b: phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": {SupportedSpeedsBPS: []uint64{1000000000, 10000000000}}}}, want: []simtest.ChangeExpectation{}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a, b := tc.a, tc.b
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := phy.Diff(a, b)
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
