package fabric

import (
	"fmt"
	"maps"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
)

// Run repeatedly invokes [Fabric.Step] until the arrival queue is empty, budget steps have executed,
// or a scheduling fault or convergence condition halts simulation.
// A non-positive budget executes zero steps and returns [StopNotRun].
// A positive window enables fingerprint convergence and cycle detection.
// A zero window computes no fingerprints.
func (f *Fabric) Run(options RunOptions) RunResult {
	return f.run(options.Budget, options.Window)
}

// Validate requires a positive step budget and a nonnegative observation window.
func (o RunOptions) Validate() error {
	if o.Budget <= 0 {
		return errs.Msgf("run budget must be positive, got %d", o.Budget)
	}
	if o.Window < 0 {
		return errs.Msgf("run window cannot be negative, got %d", o.Window)
	}
	return nil
}

// RunScenario validates and executes a declared scenario under options.
// It refuses any action before the fabric clock and retains a pre-action checkpoint for replay.
func (f *Fabric) RunScenario(s Scenario, options RunOptions) (RunResult, error) {
	if err := options.Validate(); err != nil {
		return RunResult{}, err
	}
	norm, err := s.Normalize()
	if err != nil {
		return RunResult{}, err
	}
	if err := norm.Validate(); err != nil {
		return RunResult{}, err
	}
	if err := f.validateActionTimes(norm.Actions); err != nil {
		return RunResult{}, err
	}

	checkpoint := f.Fork()

	res, err := f.runWithActions(options.Budget, options.Window, norm.Actions)
	res.Replay = ReplaySpec{
		checkpoint: checkpoint,
		Scenario:   norm.Clone(),
		Options:    options,
	}
	return res, err
}

// Replay executes the scenario on a fresh fork of its checkpoint.
// It refuses a zero replay, which has no checkpoint.
func Replay(spec ReplaySpec) (RunResult, error) {
	fab := spec.Checkpoint()
	if fab == nil {
		return RunResult{}, errs.Msg("replay refused: missing checkpoint")
	}
	return fab.RunScenario(spec.Scenario, spec.Options)
}

func (f *Fabric) validateActionTimes(actions []Action) error {
	for i, a := range actions {
		if a.At.Before(f.clock) {
			return errs.Msgf("action %d at %s precedes fabric clock %s", i, a.At, f.clock)
		}
	}
	return nil
}

func (f *Fabric) applyAction(a Action) error {
	switch a.Kind {
	case ActionInject:
		_, err := f.Inject(*a.Inject)
		return err
	case ActionFault:
		if a.At.After(f.clock) {
			f.clock = a.At
		}
		return f.SetFault(a.Fault.A, a.Fault.B, a.Fault.Fault)
	case ActionMcheck:
		if a.At.After(f.clock) {
			f.clock = a.At
		}
		return a.Mcheck.Operation.Apply(f, a.Mcheck.Node)
	case ActionRecord:
		rec := a.Record
		inj := Injection{
			At:     rec.At,
			Origin: rec.Origin,
			Frame:  *rec.Frame,
		}
		fid, err := f.Inject(inj)
		if err != nil {
			return err
		}
		if rec.OriginalLen > rec.CapturedLen {
			if j := f.journeys[fid]; j != nil {
				issue := analysis.Issue{
					Code:    IssueTruncatedRecord,
					Status:  analysis.Incomplete,
					Scope:   analysis.WholeScope(),
					Message: fmt.Sprintf("record from source %q truncated: captured %d octets of %d", rec.Source, rec.CapturedLen, rec.OriginalLen),
				}
				j.Metadata = analysis.NewMetadata(
					analysis.WholeScope(),
					append(j.Metadata.Issues(), issue),
					j.Metadata.Evidence(),
					j.Metadata.Assumptions(),
				)
			}
		}
		return nil
	case ActionConfigure:
		if a.At.After(f.clock) {
			f.clock = a.At
		}
		return a.Configure.Operation.Apply(f, a.Configure.Node)
	default:
		return errs.Msgf("unknown action kind %q", a.Kind)
	}
}

func (f *Fabric) run(budget int, window int) RunResult {
	res, _ := f.runWithActions(budget, window, nil)
	return res
}

func (f *Fabric) runWithActions(budget int, window int, actions []Action) (RunResult, error) {
	f.enqueueActions(actions)

	if budget <= 0 {
		return f.buildRunResult(StopNotRun, 0, nil, nil, budget, len(actions)), nil
	}

	if f.err != nil {
		return f.buildRunResult(StopFault, 0, nil, nil, budget, len(actions)), nil
	}

	var (
		fingerprints            []string
		cycle                   []string
		stop                    StopReason
		steps                   int
		lastFingerprint         string
		wakeArrivalsSinceChange int
		roots                   []runRoot
		actionOrdinal           int
		actionErr               error
	)
	if window > 0 {
		lastFingerprint = f.Fingerprint()
	}

	for steps < budget {
		if f.err != nil {
			stop = StopFault
			break
		}

		if len(f.queue) == 0 {
			stop = StopQueueDrained
			break
		}

		arr := f.queue[0]
		fid := f.nextFrameID
		ordinal := 0
		if arr.Kind == ArrivalStream {
			ordinal = f.attachments[arr.source].ordinal
		}
		entry, ok := f.Step()
		if !ok {
			if arr.Kind == ArrivalAction && f.err != nil {
				actionErr = f.err
			}
			if f.err != nil {
				stop = StopFault
			} else {
				stop = StopQueueDrained
			}
			break
		}
		steps++
		if arr.Kind == ArrivalAction && (arr.action.Kind == ActionInject || arr.action.Kind == ActionRecord) && f.nextFrameID > fid {
			root := runRoot{fid: fid, ordinal: actionOrdinal, injection: &InjectionLocation{Index: arr.action.Index, At: arr.At.Round(0).UTC()}}
			actionOrdinal++
			if arr.action.Inject != nil {
				root.flow, root.retention = arr.action.Inject.Flow, arr.action.Inject.Retention
			}
			roots = append(roots, root)
		}
		if arr.Kind == ArrivalStream {
			if f.nextFrameID > fid {
				att := f.attachments[arr.source].attachment
				roots = append(roots, runRoot{fid: fid, flow: att.Flow, retention: att.Retention, source: &SourceLocation{Attachment: arr.source, Frame: ordinal}})
			}
		}

		if window > 0 {
			fp := f.Fingerprint()
			fingerprints = append(fingerprints, fp)
			if fp != lastFingerprint {
				lastFingerprint = fp
				wakeArrivalsSinceChange = 0
			}
			if entry.Arrival == ArrivalWake {
				wakeArrivalsSinceChange++
			}

			if window >= 2 && len(fingerprints) >= 2*2 {
				if foundCycle, ok := detectCycle(fingerprints, window); ok {
					stop = StopOscillating
					cycle = foundCycle
					break
				}
			}

			if wakeArrivalsSinceChange >= window && f.queueHoldsOnlyWakes() && !f.hasPendingJourneys() && !f.sourcesPending() {
				stop = StopConverged
				break
			}
		}

		if f.err != nil {
			stop = StopFault
			break
		}
	}

	if stop == "" {
		stop = StopBudget
	}

	res := f.buildRunResult(stop, steps, fingerprints, cycle, budget, f.pendingActionCount())
	res.roots = roots
	return res, actionErr
}

func (f *Fabric) pendingActionCount() int {
	count := 0
	for _, arr := range f.queue {
		if arr.Kind == ArrivalAction {
			count++
		}
	}
	return count
}

func detectCycle(fps []string, window int) ([]string, bool) {
	n := len(fps)
	maxPeriod := window
	for p := 2; p <= maxPeriod && 2*p <= n; p++ {
		match := true
		for i := 0; i < p; i++ {
			if fps[n-2*p+i] != fps[n-p+i] {
				match = false
				break
			}
		}
		if match {
			allSame := true
			first := fps[n-p]
			for i := 1; i < p; i++ {
				if fps[n-p+i] != first {
					allSame = false
					break
				}
			}
			if !allSame {
				cycle := make([]string, p)
				copy(cycle, fps[n-p:n])
				return cycle, true
			}
		}
	}
	return nil, false
}

func (f *Fabric) queueHoldsOnlyWakes() bool {
	if len(f.queue) == 0 {
		return false
	}
	for _, arr := range f.queue {
		if arr.Kind != ArrivalWake {
			return false
		}
	}
	for _, q := range f.egress {
		if q != nil && q.pendingCount() > 0 {
			return false
		}
	}
	return true
}

func (f *Fabric) buildRunResult(stop StopReason, steps int, fingerprints, cycle []string, budget int, droppedActions int) RunResult {
	var issues []analysis.Issue
	for _, id := range slices.Sorted(maps.Keys(f.journeys)) {
		issues = append(issues, f.journeys[id].Metadata.Issues()...)
	}
	for _, id := range slices.Sorted(maps.Keys(f.flows)) {
		issues = append(issues, f.flows[id].Metadata.Issues()...)
	}
	var uniqueIssues []analysis.Issue
	for _, iss := range issues {
		if !slices.ContainsFunc(uniqueIssues, func(kept analysis.Issue) bool { return analysis.SameIssue(kept, iss) }) {
			uniqueIssues = append(uniqueIssues, iss)
		}
	}

	if stop == StopBudget {
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueBudgetExhausted,
			Status:  analysis.Exhausted,
			Scope:   analysis.WholeScope(),
			Message: fmt.Sprintf("step budget %d exhausted", budget),
		})
	}
	if droppedActions > 0 {
		noun := "actions"
		if droppedActions == 1 {
			noun = "action"
		}
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueActionsDropped,
			Status:  analysis.Exhausted,
			Scope:   analysis.WholeScope(),
			Message: fmt.Sprintf("%d timed %s did not fire before the run stopped", droppedActions, noun),
		})
	}
	if f.err != nil {
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueSchedulingFault,
			Status:  analysis.Unsupported,
			Scope:   analysis.WholeScope(),
			Message: f.err.Error(),
		})
	}
	if stop == StopOscillating {
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueOscillating,
			Status:  analysis.Unstable,
			Scope:   analysis.WholeScope(),
			Message: "fingerprint sequence oscillating in periodic cycle",
		})
	}

	status := analysis.Summarize(uniqueIssues)
	canonicalIssues := analysis.CanonicalIssues(uniqueIssues)

	return RunResult{
		Stop:           stop,
		Steps:          steps,
		Clock:          f.clock,
		Pending:        f.pendingWork(),
		Status:         status,
		Issues:         canonicalIssues,
		Err:            f.err,
		Replay:         ReplaySpec{},
		Fingerprints:   fingerprints,
		Cycle:          cycle,
		DroppedActions: droppedActions,
	}
}

func (f *Fabric) hasPendingJourneys() bool {
	return len(f.inflight) != 0
}

func (f *Fabric) pendingWork() PendingWork {
	var arrivals, wakes, egress, journeys, inputs int
	for _, arr := range f.queue {
		switch arr.Kind {
		case ArrivalFrame:
			arrivals++
		case ArrivalWake:
			wakes++
		case ArrivalHost, ArrivalStream, ArrivalAction:
			inputs++
		}
	}
	for _, q := range f.egress {
		if q != nil {
			egress += q.pendingCount()
		}
	}
	journeys = len(f.inflight)
	return PendingWork{
		Arrivals: arrivals,
		Inputs:   inputs,
		Wakes:    wakes,
		Egress:   egress,
		Journeys: journeys,
	}
}
