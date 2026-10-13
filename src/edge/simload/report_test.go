package simload

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/flow"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestReportSortsFlowsAndPreservesSimulatorIssues(t *testing.T) {
	metadata := analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{{
		Code:    "queue-full",
		Status:  analysis.Incomplete,
		Scope:   analysis.NodeScope("sw1"),
		Message: "the queue is bounded",
	}}, analysis.EvidenceCatalog{}, nil)
	flows := map[flow.ID]flow.Stats{
		2: {Offered: 2, Held: 1, Delivered: map[string]uint64{"host": 1}, Metadata: metadata},
		1: {Offered: 1, Delivered: map[string]uint64{"host": 1}, Metadata: metadata},
	}
	lab := Observation{Flows: map[flow.ID]FlowObservation{
		2: {Sent: 2, UniqueReceived: 1, Missing: 1},
		1: {Sent: 1, UniqueReceived: 1},
	}}

	report, err := NewReport(flows, metadata, lab, "host")
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Simulator.Flows[0].ID; got != 1 {
		t.Fatalf("first simulator flow ID = %d, want 1", got)
	}
	if report.Simulator.Status != "incomplete" || len(report.Simulator.Issues) != 1 {
		t.Fatalf("simulator summary = %+v", report.Simulator)
	}
	if got := report.Simulator.Flows[1].HeldAtEnd; got != 1 {
		t.Fatalf("end-of-run held count = %d, want 1", got)
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

func TestReportKeepsLabMissingOutsideSimulatorLoss(t *testing.T) {
	metadata := analysis.NewMetadata(analysis.WholeScope(), nil, analysis.EvidenceCatalog{}, nil)
	flows := map[flow.ID]flow.Stats{7: {Offered: 4, Delivered: map[string]uint64{"host": 4}}}
	lab := Observation{Flows: map[flow.ID]FlowObservation{7: {Sent: 4, UniqueReceived: 3, Missing: 1}}}
	report, err := NewReport(flows, metadata, lab, "host")
	if err != nil {
		t.Fatal(err)
	}
	if report.Simulator.Flows[0].Lost != 0 || len(report.Simulator.Flows[0].Drops) != 0 || report.Normalized[0].SimulatorUnreceived != 0 || report.Normalized[0].LabUnreceived != 1 {
		t.Fatalf("report conflated live missing with simulator loss: %+v", report)
	}
	projected, err := NewReportFromSimulator(report.Simulator, lab, "host")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projected, report) {
		t.Fatalf("projected report = %+v, want %+v", projected, report)
	}
}

func TestReportPreservesScopedFlowTrust(t *testing.T) {
	whole := analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{{Code: "unrelated", Status: analysis.Unsupported, Scope: analysis.NodeScope("other"), Message: "other node", Evidence: []trace.EvidenceRef{"whole-evidence"}}}, analysis.EvidenceCatalog{}, nil)
	cases := []struct {
		name       string
		status     analysis.Status
		wantStatus string
		journey    string
		wantScope  string
		evidence   trace.EvidenceRef
		copies     map[string]uint64
	}{
		{name: "incomplete", status: analysis.Incomplete, wantStatus: "incomplete", journey: "root", wantScope: `journey["root"]`, evidence: "root-evidence"},
		{name: "exhausted", status: analysis.Exhausted, wantStatus: "exhausted", journey: "root", wantScope: `journey["root"]`, evidence: "root-evidence"},
		{name: "unstable", status: analysis.Unstable, wantStatus: "unstable", journey: "root", wantScope: `journey["root"]`, evidence: "root-evidence"},
		{name: "unsupported", status: analysis.Unsupported, wantStatus: "unsupported", journey: "root", wantScope: `journey["root"]`, evidence: "root-evidence"},
		{name: "folded copy", status: analysis.Incomplete, wantStatus: "incomplete", journey: "copy", wantScope: `journey["copy"]`, evidence: "copy-evidence", copies: map[string]uint64{"mirror": 1}},
	}
	checked := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scoped := analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{{Code: "flow-dependency", Status: tc.status, Scope: analysis.JourneyScope(tc.journey), Message: "journey dependency", Evidence: []trace.EvidenceRef{tc.evidence}}}, analysis.EvidenceCatalog{}, nil)
			flows := map[flow.ID]flow.Stats{
				1: {Offered: 1, Delivered: map[string]uint64{"host": 1}},
				2: {Offered: 2, Delivered: map[string]uint64{"host": 1}, Metadata: scoped, Copies: tc.copies},
			}
			report, err := NewReport(flows, whole, Observation{}, "host")
			if err != nil {
				t.Fatal(err)
			}
			if got := report.Simulator.Flows[0]; got.Status != "complete" || len(got.Issues) != 0 {
				t.Fatalf("complete flow inherited unrelated trust: %+v", got)
			}
			wantFlowIssue := IssueReport{Code: "flow-dependency", Status: tc.wantStatus, Scope: tc.wantScope, Message: "journey dependency", Evidence: []trace.EvidenceRef{tc.evidence}}
			if got := report.Simulator.Flows[1]; got.Status != tc.wantStatus || !reflect.DeepEqual(got.Issues, []IssueReport{wantFlowIssue}) || !reflect.DeepEqual(got.Copies, tc.copies) {
				t.Fatalf("flow trust = %+v, want %s with %+v and copies %v", got, tc.wantStatus, wantFlowIssue, tc.copies)
			}
			wantWholeIssue := IssueReport{Code: "unrelated", Status: "unsupported", Scope: `node["other"]`, Message: "other node", Evidence: []trace.EvidenceRef{"whole-evidence"}}
			if report.Simulator.Status != "unsupported" || !reflect.DeepEqual(report.Simulator.Issues, []IssueReport{wantWholeIssue}) {
				t.Fatalf("whole summary = %+v", report.Simulator)
			}
			checked++
		})
	}
	if checked != 5 {
		t.Fatalf("checked %d trust cases, want 5", checked)
	}
}

func TestReportFromSimulatorKeepsMatchingAndSimulatorOnlyFlows(t *testing.T) {
	simulator := SimulatorReport{Status: "complete", Flows: []SimulatorFlow{
		{ID: 2, Offered: 3, Delivered: map[string]uint64{"host": 1}, Status: "complete"},
		{ID: 1, Offered: 2, Delivered: map[string]uint64{"host": 3}, Status: "complete"},
	}}
	lab := Observation{Flows: map[flow.ID]FlowObservation{1: {Sent: 2, UniqueReceived: 1, Missing: 1}}, Malformed: 4, InterfaceDrops: 5}
	report, err := NewReportFromSimulator(simulator, lab, "host")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Simulator.Flows) != 2 || report.Simulator.Flows[0].ID != 1 || report.Simulator.Flows[1].ID != 2 || simulator.Flows[0].ID != 2 {
		t.Fatalf("simulator rows = %+v, input = %+v", report.Simulator.Flows, simulator.Flows)
	}
	want := []NormalizedFlow{
		{ID: 1, Destination: "host", SimulatorOffered: 2, SimulatorDelivered: 3, SimulatorUnreceived: 0, LabOffered: 2, LabDelivered: 1, LabUnreceived: 1},
		{ID: 2, Destination: "host", SimulatorOffered: 3, SimulatorDelivered: 1, SimulatorUnreceived: 2},
	}
	if !reflect.DeepEqual(report.Normalized, want) {
		t.Fatalf("normalized = %+v, want %+v", report.Normalized, want)
	}
	wantLab := ObservationReport{Flows: []ObservedFlow{{ID: 1, FlowObservation: FlowObservation{Sent: 2, UniqueReceived: 1, Missing: 1}}}, Malformed: 4, InterfaceDrops: 5}
	if !reflect.DeepEqual(report.Lab, wantLab) {
		t.Fatalf("lab = %+v, want %+v", report.Lab, wantLab)
	}
}

func TestReportRejectsLabOnlyFlows(t *testing.T) {
	lab := Observation{Flows: map[flow.ID]FlowObservation{1: {Sent: 1}, 2: {Sent: 1}}}
	simulator := SimulatorReport{Flows: []SimulatorFlow{{ID: 1, Offered: 1}}}
	t.Run("projected", func(t *testing.T) {
		report, err := NewReportFromSimulator(simulator, lab, "host")
		if err == nil || err.Error() != "lab flow ID 2 is unknown to the simulator input" || !reflect.DeepEqual(report, Report{}) {
			t.Fatalf("projected report = %+v, error = %v, want zero report and unknown flow 2 error", report, err)
		}
	})
	t.Run("statistics", func(t *testing.T) {
		report, err := NewReport(map[flow.ID]flow.Stats{1: {Offered: 1}}, analysis.Metadata{}, lab, "host")
		if err == nil || err.Error() != "lab flow ID 2 is unknown to the simulator input" || !reflect.DeepEqual(report, Report{}) {
			t.Fatalf("report = %+v, error = %v, want zero report and unknown flow 2 error", report, err)
		}
	})
}
