package fabric

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
)

// StopReason describes why a simulation run terminated.
// StopReason has no meaningful zero value; a run always sets one of the non-empty constants below.
type StopReason string

const (
	// StopNotRun indicates that the run was requested with a non-positive step budget.
	StopNotRun StopReason = "NotRun"
	// StopQueueDrained indicates that the arrival queue emptied before the budget was exhausted.
	StopQueueDrained StopReason = "QueueDrained"
	// StopBudget indicates that the step budget was exhausted.
	StopBudget StopReason = "Budget"
	// StopConverged indicates that the fingerprint remained unchanged across the required window of wake arrivals.
	StopConverged StopReason = "Converged"
	// StopOscillating indicates that the fingerprint sequence repeated a periodic cycle.
	StopOscillating StopReason = "Oscillating"
	// StopFault indicates that a scheduling-invariant breach halted simulation.
	StopFault StopReason = "Fault"
)

const (
	// IssueBudgetExhausted is the issue code raised when a run halts because its step budget was exhausted.
	IssueBudgetExhausted analysis.IssueCode = "budget-exhausted"
	// IssueSchedulingFault is the issue code raised when a scheduling fault halts simulation.
	IssueSchedulingFault analysis.IssueCode = "scheduling-fault"
	// IssueOscillating is the issue code raised when a run halts because the state is oscillating.
	IssueOscillating analysis.IssueCode = "oscillating"
)

// PendingWork summarizes remaining work across all queues at the time a run halted.
type PendingWork struct {
	Arrivals int
	Wakes    int
	Egress   int
	Journeys int
}

// ReplayContract identifies the fabric replay specification schema version.
const ReplayContract = "netsim-fabric/v1"

// ReplaySpec captures the construction specification and contract version needed to replay a run.
type ReplaySpec struct {
	Contract string
	Spec     ConstructionSpec
}

// RunResult captures the complete outcome of executing a simulation run.
type RunResult struct {
	Stop         StopReason
	Steps        int
	Clock        time.Time
	Pending      PendingWork
	Status       analysis.Status
	Issues       []analysis.Issue
	Err          error
	Replay       ReplaySpec
	Fingerprints []string
	Cycle        []string
}
