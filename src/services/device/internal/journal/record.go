package journal

import (
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

// OwedKind is which message central owes an edge for one sequence.
type OwedKind int

const (
	// OwedHoldResolved tells the edge to clear an abandoned or
	// desynchronized sequence's hold.
	OwedHoldResolved OwedKind = iota + 1
	// OwedExecute dispatches a mutation. Resume is set when the mutation's
	// phase has passed ADMITTED, so the edge admits it into recovery for a
	// command that may already have reached the device.
	OwedExecute
	// OwedRead dispatches a read; it rides the same ExecuteRequest as a
	// mutation but is a distinct row, keyed by its own sequence.
	OwedRead
	// OwedCheckpoint records POSSIBLY_APPLIED durably before the edge
	// submits.
	OwedCheckpoint
	// OwedTerminalAck hands the edge central's terminal disposition, the
	// barrier that frees the lane.
	OwedTerminalAck
)

// Owed is one message the record says central owes the edge.
type Owed struct {
	Kind        OwedKind
	Sequence    uint64
	Resume      bool                 // OwedExecute only
	Disposition accessv1.Disposition // OwedTerminalAck only
}

// permitsDispatch reports whether a block reason still lets central dispatch
// the mutation. The set is a permit-list on purpose: a reason added later
// blocks dispatch until someone lists it here, which is the safe default. A
// mutation with no block reason (the zero value) permits dispatch.
func permitsDispatch(reason accessv1.BlockReason) bool {
	switch reason {
	case accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED,
		accessv1.BlockReason_BLOCK_REASON_UNACKNOWLEDGED,
		accessv1.BlockReason_BLOCK_REASON_INDETERMINATE:
		return true
	default:
		// RECOVERY_HOLD, DESYNCHRONIZED, FIRMWARE_EPOCH_CHANGED,
		// CONFLICTING_READS, EDGE_STALE: nothing is dispatched until an
		// operator, the edge's return, or a fresh probe clears the block.
		return false
	}
}

// OwedRows derives every message the record owes the edge, at time now. Rows
// are independent and keyed by sequence; the derivation is total, so the
// outbox relay is a pure function of the record. A read whose deadline has
// passed owes nothing here — it is due for the sweep, which
// [Journal.SweepExpiredReads] performs by closing it with a deadline error.
// Until that sweep runs the read owes no row and is not stranded: the promise
// that it is closed is one the relay keeps by calling the sweep.
func OwedRows(record *storev1.DeviceLaneRecord, now time.Time) []Owed {
	var owed []Owed

	// The hold-resolution rows: independent sequences, each cleared by the
	// edge's HoldResolvedAck. More than one can be pending when a restore
	// admits a new intent while an abandoned sequence's hold is unacknowledged.
	for _, seq := range record.GetHoldResolutionPending() {
		owed = append(owed, Owed{Kind: OwedHoldResolved, Sequence: seq})
	}

	if m := record.GetMutation(); m != nil {
		seq := m.GetSequence()
		hasDisposition := m.HasDisposition()
		switch {
		case hasDisposition && record.GetDispatched():
			// A terminal disposition on a dispatched mutation owes the
			// terminal ack until the edge confirms it. A released
			// disposition (VERIFIED, REJECTED) keeps the mutation in the
			// record only until the edge reports RELEASED, so a present
			// mutation still owes; an abandonment keeps the mutation held
			// past the ack, so it owes only until the last reported phase
			// is ABANDONED, after which ResolveDesynchronization ends it.
			if m.GetDisposition() != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED ||
				record.GetLastReportedPhase() != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
				owed = append(owed, Owed{Kind: OwedTerminalAck, Sequence: seq, Disposition: m.GetDisposition()})
			}
		case hasDisposition:
			// A terminal disposition can only reach the record with dispatched
			// set: every path that writes one runs when the edge holds the
			// sequence (ReportVerified and ReportError follow admission), and
			// Dispose of an un-dispatched mutation closes the lane rather than
			// leaving a disposition here. So this arm is unreachable, kept as
			// the statement that a disposition without dispatch never persists.
		case !permitsDispatch(m.GetBlockReason()):
			// Blocked for an operator, the edge's return, or a fresh probe:
			// owes nothing; the terminator is not a row central sends.
		case !record.GetDispatchConfirmed():
			owed = append(owed, Owed{
				Kind:     OwedExecute,
				Sequence: seq,
				Resume:   m.GetPhase() != accessv1.OperationPhase_OPERATION_PHASE_ADMITTED,
			})
		case m.GetPhase() == accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED && !record.GetCheckpointConfirmed():
			owed = append(owed, Owed{Kind: OwedCheckpoint, Sequence: seq})
		}
	}

	for _, read := range record.GetOpenReads() {
		if read.HasOutcome() {
			continue // closed, awaiting removal
		}
		if deadline := read.GetDeadline(); deadline != nil && !deadline.AsTime().After(now) {
			continue // expired: due for the sweep, a promise SweepExpiredReads keeps
		}
		owed = append(owed, Owed{Kind: OwedRead, Sequence: read.GetSequence()})
	}

	return owed
}

// ExpiredReads returns the interface names of open reads whose deadline has
// passed with no outcome. It is the pure counterpart to
// [Journal.SweepExpiredReads]: it names what is due without writing, so a test
// or a caller can decide before the sweep records the deadline errors.
func ExpiredReads(record *storev1.DeviceLaneRecord, now time.Time) []string {
	var expired []string
	for name, read := range record.GetOpenReads() {
		if read.HasOutcome() {
			continue
		}
		if deadline := read.GetDeadline(); deadline != nil && !deadline.AsTime().After(now) {
			expired = append(expired, name)
		}
	}
	return expired
}

// TerminatorNamed reports whether the record's open mutation names a
// terminator that exists and can act on its sequence — the test that a
// nothing-owed mutation is bounded rather than stranded. Two arms, each an
// invocable RPC, not a label:
//
//   - A non-terminal mutation (no disposition) is ended by AbandonMutation,
//     which [Journal.Dispose] applies to any sequence whose mutation has no
//     disposition. When the mutation is blocked in recovery its bound is the
//     recovery horizon, from blocked_since, that expires into that same call.
//   - An abandoned mutation whose terminal ack the edge has confirmed
//     (INDETERMINATE_ABANDONED, last reported phase ABANDONED) is resolved by
//     ResolveDesynchronization, which [Journal.ResolveHold] applies to that
//     sequence.
//
// A terminal mutation that is not the confirmed-abandoned case owes its
// terminal ack instead, so it is not covered here.
func TerminatorNamed(record *storev1.DeviceLaneRecord) bool {
	m := record.GetMutation()
	if m == nil {
		return false
	}
	if !m.HasDisposition() {
		return true // AbandonMutation ends any non-terminal mutation
	}
	return m.GetDisposition() == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED &&
		record.GetLastReportedPhase() == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED
}
