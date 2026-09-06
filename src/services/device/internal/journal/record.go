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

// blockingReasons are the block reasons that forbid dispatching a mutation:
// it is held for an operator or for discovery, not owed to the edge.
func isBlocking(reason accessv1.BlockReason) bool {
	switch reason {
	case accessv1.BlockReason_BLOCK_REASON_DESYNCHRONIZED,
		accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD,
		accessv1.BlockReason_BLOCK_REASON_EDGE_STALE:
		return true
	default:
		return false
	}
}

// Owed derives every message the record owes the edge, at time now. Rows
// are independent and keyed by sequence; the derivation is total, so the
// outbox relay is a pure function of the record. A read whose deadline has
// passed owes nothing here — it is swept by [ExpiredReads] in the same CAS
// write that records it failed.
func OwedRows(record *storev1.DeviceLaneRecord, now time.Time) []Owed {
	var owed []Owed

	// The hold-resolution row: an independent sequence, cleared by the
	// edge's HoldResolvedAck.
	if record.HasHoldResolutionPending() {
		owed = append(owed, Owed{Kind: OwedHoldResolved, Sequence: record.GetHoldResolutionPending()})
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
			// is ABANDONED, after which the operator terminates it.
			if !(m.GetDisposition() == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED &&
				record.GetLastReportedPhase() == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED) {
				owed = append(owed, Owed{Kind: OwedTerminalAck, Sequence: seq, Disposition: m.GetDisposition()})
			}
		case hasDisposition:
			// dispatched unset: central disposed it without the edge ever
			// holding it, and closes it in the same write, so nothing is
			// owed. Such a state never persists.
		case isBlocking(m.GetBlockReason()):
			// Held for an operator (DESYNCHRONIZED, RECOVERY_HOLD) or for
			// discovery (EDGE_STALE): owes nothing; the operator or the
			// epoch refresh terminates it.
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
			continue // expired, due for the sweep
		}
		owed = append(owed, Owed{Kind: OwedRead, Sequence: read.GetSequence()})
	}

	return owed
}

// ExpiredReads returns the interface names of open reads whose deadline has
// passed with no outcome, which the relay closes with a deadline error.
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

// OperatorTerminated reports whether the record's mutation is one only
// ResolveDesynchronization can end: a held reconciliation intent
// (DESYNCHRONIZED, no disposition) or an abandoned mutation whose terminal
// ack the edge has confirmed (ABANDONED reported). Both owe nothing by
// design; the invariant test uses this to tell an intentional
// nothing-owed state from a stranded one.
func OperatorTerminated(record *storev1.DeviceLaneRecord) bool {
	m := record.GetMutation()
	if m == nil {
		return false
	}
	if m.GetBlockReason() == accessv1.BlockReason_BLOCK_REASON_DESYNCHRONIZED && !m.HasDisposition() {
		return true
	}
	if m.HasDisposition() &&
		m.GetDisposition() == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED &&
		record.GetLastReportedPhase() == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		return true
	}
	return false
}
