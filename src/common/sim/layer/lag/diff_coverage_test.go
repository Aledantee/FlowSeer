package lag_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestDiffCoversEveryConfigField verifies that every exported lag.Config field reaches
// lag.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "lag", simtest.LagDiffAllowance())
}

func TestDiffCoverageRejectsOmittedDelays(t *testing.T) {
	simtest.AssertMappedDiffRejectsOmittedFields(t, "lag", map[string]string{
		"lag.Config.LAGs[lag1].UpDelay":   "up_delay",
		"lag.Config.LAGs[lag1].DownDelay": "down_delay",
	})
}

func TestDiffDelaysAndMemberIdentity(t *testing.T) {
	cases := []struct {
		name string
		a, b lag.LAG
		want []simtest.ChangeExpectation
	}{
		{"up_delay", lag.LAG{UpDelay: 2 * time.Second}, lag.LAG{UpDelay: 3 * time.Second}, []simtest.ChangeExpectation{{Layer: "lag", Subject: trace.Subject{Kind: "lag", Key: "lag1"}, Field: "up_delay", From: &simtest.FactExpectation{TypeID: "lag.duration", Canonical: "2s"}, To: &simtest.FactExpectation{TypeID: "lag.duration", Canonical: "3s"}}}},
		{"down_delay", lag.LAG{DownDelay: time.Second}, lag.LAG{DownDelay: 2 * time.Second}, []simtest.ChangeExpectation{{Layer: "lag", Subject: trace.Subject{Kind: "lag", Key: "lag1"}, Field: "down_delay", From: &simtest.FactExpectation{TypeID: "lag.duration", Canonical: "1s"}, To: &simtest.FactExpectation{TypeID: "lag.duration", Canonical: "2s"}}}},
		{"member_rename", lag.LAG{Members: map[string]lag.Member{"1/1/1": {Priority: 128, Key: 1}}}, lag.LAG{Members: map[string]lag.Member{"1/1/2": {Priority: 128, Key: 1}}}, []simtest.ChangeExpectation{
			{Layer: "lag", Subject: trace.Subject{Kind: "lag_member", Key: `"lag1"/"1/1/1"`}, Field: "", From: &simtest.FactExpectation{TypeID: "lag.member", Canonical: "priority=128,key=1"}},
			{Layer: "lag", Subject: trace.Subject{Kind: "lag_member", Key: `"lag1"/"1/1/2"`}, Field: "", To: &simtest.FactExpectation{TypeID: "lag.member", Canonical: "priority=128,key=1"}},
		}},
	}
	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, normalized := range []bool{false, true} {
				a := lag.Config{LAGs: map[string]lag.LAG{"lag1": tc.a}}
				b := lag.Config{LAGs: map[string]lag.LAG{"lag1": tc.b}}
				if normalized {
					a, b = a.Normalize(layer.Env{}), b.Normalize(layer.Env{})
				}
				got := lag.Diff(a, b)
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
	if hits != 6 {
		t.Errorf("concrete delay/identity expectations = %d, want 6", hits)
	}
}
