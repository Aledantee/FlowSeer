package netsimload

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

func TestReportSortsFlowsAndPreservesSimulatorIssues(t *testing.T) {
	metadata := analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{{
		Code:    "queue-full",
		Status:  analysis.Incomplete,
		Scope:   analysis.NodeScope("sw1"),
		Message: "the queue is bounded",
	}}, analysis.EvidenceCatalog{}, nil)
	flows := map[fabric.FlowID]fabric.FlowStats{
		2: {Offered: 2, Delivered: map[string]uint64{"host": 1}, Metadata: metadata},
		1: {Offered: 1, Delivered: map[string]uint64{"host": 1}, Metadata: metadata},
	}
	lab := Observation{Flows: map[fabric.FlowID]FlowObservation{
		2: {Sent: 2, UniqueReceived: 1, Missing: 1},
		1: {Sent: 1, UniqueReceived: 1},
	}}

	report := NewReport(flows, metadata, lab, "host")
	if got := report.Simulator.Flows[0].ID; got != 1 {
		t.Fatalf("first simulator flow ID = %d, want 1", got)
	}
	if report.Simulator.Status != "incomplete" || len(report.Simulator.Issues) != 1 {
		t.Fatalf("simulator summary = %+v", report.Simulator)
	}
	if report.Normalized[1].LabUnreceived != 1 {
		t.Fatalf("normalized lab gap = %+v", report.Normalized[1])
	}

	var first, second bytes.Buffer
	if err := report.WriteJSON(&first); err != nil {
		t.Fatalf("first WriteJSON: %v", err)
	}
	if err := report.WriteJSON(&second); err != nil {
		t.Fatalf("second WriteJSON: %v", err)
	}
	if first.String() != second.String() || !strings.Contains(first.String(), `"queue-full"`) {
		t.Fatalf("report JSON is not stable or lost issue: %s", first.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(first.Bytes(), &decoded); err != nil {
		t.Fatalf("report JSON: %v", err)
	}
}
