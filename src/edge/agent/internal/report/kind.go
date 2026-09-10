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
		// An arm this build does not know. Kept rather than dropped —
		// something asked for a report and this is one — and Report gives it
		// a distinct sequence from the queue's own counter, so it supersedes
		// neither a real report nor another unknown one.
		return key{device: device, kind: kindUnknown}
	}
}

// closesOperation reports whether central, having taken this report, still
// needs the edge to remember the operation it is about.
//
// A terminal result and a refusal end it: central holds the answer and will
// not dispatch the sequence again for want of one. A progress report does
// not. The operation is still running, and releasing the memory of it here
// would let a re-dispatch admit the same sequence a second time and run it
// on the device twice — which is the whole of what that memory prevents.
//
// The two acknowledgements are the same case as progress: each answers a
// message central sent about an operation that is still open, and a terminal
// result or a refusal always follows. An Onboarded report is about the
// device rather than an operation, and an arm this build does not know
// carries no operation to release.
func closesOperation(k key, report *integrationv1.ReportRequest) bool {
	switch k.kind {
	case kindResult:
		return report.GetResult().WhichOutcome() != integrationv1.ExecuteResult_Progress_case
	case kindRefused:
		return true
	default:
		return false
	}
}

// sortByAdmission orders keys oldest first, which is the order the edge made
// the reports and the order central applies them in.
func sortByAdmission(keys []key, pending map[key]entry) {
	slices.SortFunc(keys, func(a, b key) int {
		return cmp.Compare(pending[a].admitted, pending[b].admitted)
	})
}
