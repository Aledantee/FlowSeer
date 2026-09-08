package report

import (
	"cmp"
	"slices"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
)

// The report kinds this queue distinguishes. They are part of the key
// because an operation can owe more than one thing at once: a result and a
// refusal are different answers and neither supersedes the other.
const (
	kindResult          = "result"
	kindCheckpointAck   = "checkpoint_ack"
	kindHoldResolvedAck = "hold_resolved_ack"
	kindRefused         = "refused"
	kindOnboarded       = "onboarded"
	kindUnknown         = "unknown"
)

// keyOf names the operation and answer one report is about.
//
// Onboarded carries no sequence — it is about the device rather than about an
// operation — so it keys on the device alone and a second one supersedes the
// first. That is right: an edge sends it at every start, and two of them mean
// two starts, of which only the latest describes the device now.
func keyOf(report *integrationv1.ReportRequest) key {
	device := report.GetDeviceId()
	switch {
	case report.HasResult():
		return key{device: device, sequence: report.GetResult().GetSequence(), kind: kindResult}
	case report.HasCheckpointAck():
		return key{device: device, sequence: report.GetCheckpointAck().GetSequence(), kind: kindCheckpointAck}
	case report.HasHoldResolvedAck():
		return key{device: device, sequence: report.GetHoldResolvedAck().GetSequence(), kind: kindHoldResolvedAck}
	case report.HasRefused():
		return key{device: device, sequence: report.GetRefused().GetSequence(), kind: kindRefused}
	case report.HasOnboarded():
		return key{device: device, kind: kindOnboarded}
	default:
		// An arm this build does not know. Keyed so it cannot supersede
		// anything real, and kept rather than dropped: central asked for a
		// report and this is one.
		return key{device: device, kind: kindUnknown}
	}
}

// sequenceOf is the operation a report is about, or zero for one that is
// about the device.
func sequenceOf(k key) uint64 { return k.sequence }

// sortByAdmission orders keys oldest first, which is the order the edge made
// the reports and the order central applies them in.
func sortByAdmission(keys []key, pending map[key]entry) {
	slices.SortFunc(keys, func(a, b key) int {
		return cmp.Compare(pending[a].admitted, pending[b].admitted)
	})
}
