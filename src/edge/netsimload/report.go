// Package netsimload runs finite offered-load sources on an edge host and
// compares their wire observations with simulator flow statistics.
package netsimload

import (
	"encoding/json"
	"io"
	"sort"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

const reportContract = "netsimload/report/v1"

// Report combines simulator flow outcomes, lab observations, and the
// normalized destination-host comparison. All slices are emitted in stable
// order so the report is suitable for review and machine comparison.
type Report struct {
	Contract    string            `json:"contract"`
	Simulator   SimulatorReport   `json:"simulator"`
	Lab         ObservationReport `json:"lab"`
	Normalized  []NormalizedFlow  `json:"normalized"`
	Limitations []string          `json:"limitations"`
}

// SimulatorReport is the public-accessor projection of a fabric run.
type SimulatorReport struct {
	Status string          `json:"status"`
	Issues []IssueReport   `json:"issues"`
	Flows  []SimulatorFlow `json:"flows"`
}

// SimulatorFlow keeps raw fabric counters and its per-flow trust metadata.
type SimulatorFlow struct {
	ID         fabric.FlowID       `json:"id"`
	Offered    uint64              `json:"offered"`
	Delivered  map[string]uint64   `json:"delivered"`
	Drops      map[string]uint64   `json:"drops"`
	Lost       uint64              `json:"lost"`
	Unresolved uint64              `json:"unresolved"`
	Rejected   uint64              `json:"rejected"`
	Held       uint64              `json:"held"`
	Copies     map[string]uint64   `json:"copies"`
	Latency    fabric.LatencyStats `json:"latency"`
	Status     string              `json:"status"`
	Issues     []IssueReport       `json:"issues"`
}

// ObservationReport is the JSON-friendly, sorted form of Observation.
type ObservationReport struct {
	Flows          []ObservedFlow `json:"flows"`
	Malformed      uint64         `json:"malformed"`
	InterfaceDrops uint64         `json:"interface_drops"`
}

// ObservedFlow is one sorted lab flow row.
type ObservedFlow struct {
	ID fabric.FlowID `json:"id"`
	FlowObservation
}

// NormalizedFlow compares offered, delivered, and unreceived counts without
// claiming that a lab sequence gap has a simulator drop reason.
type NormalizedFlow struct {
	ID                  fabric.FlowID `json:"id"`
	Destination         string        `json:"destination"`
	SimulatorOffered    uint64        `json:"simulator_offered"`
	SimulatorDelivered  uint64        `json:"simulator_delivered"`
	SimulatorUnreceived uint64        `json:"simulator_unreceived"`
	LabOffered          uint64        `json:"lab_offered"`
	LabDelivered        uint64        `json:"lab_delivered"`
	LabUnreceived       uint64        `json:"lab_unreceived"`
}

// IssueReport preserves the simulator's issue code, status, scope, message,
// and evidence references in a JSON shape whose scope is human-readable.
type IssueReport struct {
	Code     string `json:"code"`
	Status   string `json:"status"`
	Scope    string `json:"scope"`
	Message  string `json:"message"`
	Evidence any    `json:"evidence,omitempty"`
}

// NewReport projects simulator flows and metadata into a deterministic report.
// destination is the named simulator host used for the normalized delivery
// row; it is not inferred from whichever map key happens to sort first.
func NewReport(flows map[fabric.FlowID]fabric.FlowStats, metadata analysis.Metadata, lab Observation, destination string) Report {
	ids := make([]fabric.FlowID, 0, len(flows))
	for id := range flows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	simulator := SimulatorReport{Status: metadata.Status().String(), Issues: issueReports(metadata.Issues())}
	normalized := make([]NormalizedFlow, 0, len(ids))
	for _, id := range ids {
		stats := flows[id]
		flowMetadata := stats.Metadata
		if flowMetadata.Scope() == analysis.WholeScope() && len(flowMetadata.Issues()) == 0 && flowMetadata.Status() == analysis.Complete {
			flowMetadata = metadata
		}
		simulator.Flows = append(simulator.Flows, simulatorFlow(id, stats, flowMetadata))

		labFlow := lab.Flows[id]
		delivered := stats.Delivered[destination]
		unreceived := stats.Offered - delivered
		if delivered > stats.Offered {
			unreceived = 0
		}
		normalized = append(normalized, NormalizedFlow{
			ID:                  id,
			Destination:         destination,
			SimulatorOffered:    stats.Offered,
			SimulatorDelivered:  delivered,
			SimulatorUnreceived: unreceived,
			LabOffered:          labFlow.Sent,
			LabDelivered:        labFlow.UniqueReceived,
			LabUnreceived:       labFlow.Missing,
		})
	}

	return Report{
		Contract:   reportContract,
		Simulator:  simulator,
		Lab:        observationReport(lab),
		Normalized: normalized,
		Limitations: []string{
			"software submission timestamps measure userspace handoff to the kernel, not physical NIC departure",
			"receive timestamps measure capture delivery to userspace",
			"sequence gaps do not identify a switch drop reason or cable loss",
		},
	}
}

// WriteJSON writes one indented report followed by a newline.
func (r Report) WriteJSON(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

func simulatorFlow(id fabric.FlowID, stats fabric.FlowStats, metadata analysis.Metadata) SimulatorFlow {
	delivered := copyCounts(stats.Delivered)
	drops := make(map[string]uint64, len(stats.Drops))
	for reason, count := range stats.Drops {
		drops[string(reason)] = count
	}
	copies := copyCounts(stats.Copies)

	return SimulatorFlow{
		ID:         id,
		Offered:    stats.Offered,
		Delivered:  delivered,
		Drops:      drops,
		Lost:       stats.Lost,
		Unresolved: stats.Unresolved,
		Rejected:   stats.Rejected,
		Held:       stats.Held,
		Copies:     copies,
		Latency:    stats.Latency,
		Status:     metadata.Status().String(),
		Issues:     issueReports(metadata.Issues()),
	}
}

func observationReport(observation Observation) ObservationReport {
	ids := SortedFlowIDs(observation)
	flows := make([]ObservedFlow, 0, len(ids))
	for _, id := range ids {
		flows = append(flows, ObservedFlow{ID: id, FlowObservation: observation.Flows[id]})
	}

	return ObservationReport{Flows: flows, Malformed: observation.Malformed, InterfaceDrops: observation.InterfaceDrops}
}

func issueReports(issues []analysis.Issue) []IssueReport {
	if len(issues) == 0 {
		return nil
	}
	reports := make([]IssueReport, 0, len(issues))
	for _, issue := range issues {
		reports = append(reports, IssueReport{
			Code:     issue.Code.String(),
			Status:   issue.Status.String(),
			Scope:    issue.Scope.String(),
			Message:  issue.Message,
			Evidence: issue.Evidence,
		})
	}
	return reports
}

func copyCounts(counts map[string]uint64) map[string]uint64 {
	if len(counts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]uint64, len(counts))
	for _, key := range keys {
		out[key] = counts[key]
	}
	return out
}
