package fabric

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/flow"
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
	// StopActionRefused indicates that a scenario action was refused, such as an injection at an unknown node.
	// The result carries the refusal as its error, the fabric is not faulted, and actions after it did not fire.
	StopActionRefused StopReason = "ActionRefused"
)

const (
	// IssueStreamFault reports a source frame that cannot enter the run.
	IssueStreamFault analysis.IssueCode = "stream-fault"
	// IssueBudgetExhausted is the issue code raised when a run halts because its step budget was exhausted.
	IssueBudgetExhausted analysis.IssueCode = "budget-exhausted"
	// IssueSchedulingFault is the issue code raised when a scheduling fault halts simulation.
	IssueSchedulingFault analysis.IssueCode = "scheduling-fault"
	// IssueOscillating is the issue code raised when a run halts because the state is oscillating.
	IssueOscillating analysis.IssueCode = "oscillating"
	// IssueActionsDropped is the issue code raised when a run halts before all timed scenario actions have fired.
	IssueActionsDropped analysis.IssueCode = "actions-dropped"
)

// PendingWork summarizes remaining work across all queues at the time a run halted.
type PendingWork struct {
	// Inputs counts queued host transmissions, stream frames, and scenario actions.
	Inputs   int
	Arrivals int
	Wakes    int
	Egress   int
	Journeys int
}

// ReplaySpec holds an in-memory checkpoint taken before its scenario runs.
// Replay forks the checkpoint on every call, preserving live state across repeated runs.
// Concurrent replays are safe while Scenario is unchanged.
type ReplaySpec struct {
	checkpoint *Fabric
	Scenario   Scenario
	Options    RunOptions
}

// Checkpoint returns an independent fork of the pre-action fabric, or nil for a zero replay.
// Changing the returned fabric does not change subsequent replays.
func (s ReplaySpec) Checkpoint() *Fabric {
	if s.checkpoint == nil {
		return nil
	}
	return s.checkpoint.Fork()
}

// RunResult captures the complete outcome of executing a simulation run.
// Replay is populated and replayable only for RunScenario and Replay; plain Run leaves Replay zero.
// Err is the scheduling fault that halted the run, or the error of the scenario action it refused.
type RunResult struct {
	Stop           StopReason
	Steps          int
	Clock          time.Time
	Pending        PendingWork
	Status         analysis.Status
	Issues         []analysis.Issue
	Err            error
	Replay         ReplaySpec
	Fingerprints   []string
	Cycle          []string
	DroppedActions int
	roots          []runRoot
}

type runRoot struct {
	fid       FrameID
	ordinal   int
	flow      flow.ID
	retention JourneyRetention
	injection *InjectionLocation
	source    *SourceLocation
}
