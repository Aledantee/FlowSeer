package search

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

func TestAlignEquivalentReturnsFalse(t *testing.T) {
	t.Parallel()

	cur, _, curTwin, _, h1MAC, h2MAC := makeTestFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	scenario := []fabric.Injection{
		{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				Tags:      []vlan.Tag{{VID: 10}},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("test"),
			},
		},
	}

	cmp := fabric.Compare(cur, curTwin, Candidate{Scenario: scenario}.ToScenario(10), 10)
	if cmp.Disposition != analysis.Equivalent {
		t.Fatalf("cmp.Disposition = %v, want Equivalent", cmp.Disposition)
	}

	idx, ok := Align(cmp)
	if ok {
		t.Errorf("Align returned ok = true, want false for Equivalent comparison (idx = %d)", idx)
	}
	if idx != 0 {
		t.Errorf("Align returned idx = %d, want 0", idx)
	}
}

func TestAlignNamesFirstDifferingTraceEntry(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, h1MAC, h2MAC := makeTestFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	scenario := []fabric.Injection{
		{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				Tags:      []vlan.Tag{{VID: 10}},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("diff-test"),
			},
		},
	}

	cmp := fabric.Compare(cur, cand, Candidate{Scenario: scenario}.ToScenario(10), 10)
	if cmp.Disposition != analysis.Different {
		t.Fatalf("cmp.Disposition = %v, want Different", cmp.Disposition)
	}

	idx, ok := Align(cmp)
	if !ok {
		t.Fatalf("Align returned ok = false, want true for Different comparison")
	}

	// In the journey:
	// Entry 0 is the Injection at host h1 (identical on both)
	// Entry 1 is the Cable Crossing from h1 to sw1:1/1/1 (identical on both)
	// Entry 2 is the switch Hop at sw1 (where cur forwards and cand drops due to VLAN filter)
	if idx != 2 {
		t.Errorf("Align returned entry index %d, want 2 (first differing switch hop)", idx)
	}
}
