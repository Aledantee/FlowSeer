package fabric

import (
	"fmt"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
)

// Comparison reports the simulation outcomes of running a common scenario on two fabrics.
type Comparison struct {
	Current     []Journey
	Expected    []Journey
	Disposition analysis.Disposition
	Difference  analysis.Difference
	Location    DifferenceLocation
	Replay      [2]ReplaySpec
	Steps       [2]int
	Err         error
	roots       [2][]FrameID
}

// Compare forks both fabrics internally, applies the scenario's scheduled actions
// (injections and faults) to the forks, runs each fork up to the step budget,
// and compares their behavioral observables.
// The caller's fabrics a and b are never stepped, injected, or learned into.
// New scenario journeys pair by injection ordinal across the two forks; pre-scenario
// journeys are excluded from comparison. Faults are scheduled topology events, not journeys.
// Comparison walks, per paired journey, State, Origin, ordered Entries (path hops,
// cable crossings, deliveries, and drops with reason and location), Deliveries,
// Protocol, and carried frame content, then the run's Stop reason, Status, Pending
// work, and Issues, then the final Snapshot behavioral state. Diagnostic metadata,
// evidence, semantic trace text, and raw convergence diagnostics never cause a Different.
// On Different, Difference names the first differing observable with both sides' values
// and Replay carries pre-action checkpoints for both fabrics ([0] for current,
// [1] for candidate) to reproduce the divergence. Actions before either fabric's
// clock are refused before either fork executes them.
func Compare(a, b *Fabric, scenario Scenario, budget int) Comparison {
	forkA := a.Fork()
	forkB := b.Fork()

	normSc := scenario.Clone()
	if normSc.Name == "" {
		normSc.Name = "compare-scenario"
	}
	if budget > 0 {
		normSc.Budget = budget
	}

	norm, err := normSc.Normalize()
	replaySpecA := ReplaySpec{
		checkpoint: a.Fork(),
		Scenario:   normSc.Clone(),
	}
	replaySpecB := ReplaySpec{
		checkpoint: b.Fork(),
		Scenario:   normSc.Clone(),
	}
	replaySpecs := [2]ReplaySpec{replaySpecA, replaySpecB}
	if err != nil {
		return Comparison{
			Disposition: analysis.Inconclusive,
			Replay:      replaySpecs,
			Err:         err,
		}
	}

	replaySpecA.Scenario = norm.Clone()
	replaySpecB.Scenario = norm.Clone()
	replaySpecs = [2]ReplaySpec{replaySpecA, replaySpecB}
	for _, f := range []*Fabric{forkA, forkB} {
		if err := f.validateActionTimes(norm.Actions); err != nil {
			return Comparison{Disposition: analysis.Inconclusive, Replay: replaySpecs, Err: err}
		}
	}

	runResA, injFIDsA, errA := runScenarioFork(forkA, norm.Budget, norm.Window, norm.Actions)
	if errA != nil {
		return Comparison{
			Disposition: analysis.Inconclusive,
			Replay:      replaySpecs,
			Err:         errA,
		}
	}

	runResB, injFIDsB, errB := runScenarioFork(forkB, norm.Budget, norm.Window, norm.Actions)
	if errB != nil {
		return Comparison{
			Disposition: analysis.Inconclusive,
			Replay:      replaySpecs,
			Err:         errB,
		}
	}

	allJourneysA := forkA.Report()
	allJourneysB := forkB.Report()

	scenarioJourneysA := collectScenarioJourneys(allJourneysA, injFIDsA)
	scenarioJourneysB := collectScenarioJourneys(allJourneysB, injFIDsB)

	diff, location, hasDiff := diffFabricRuns(
		norm.Actions,
		injFIDsA, injFIDsB,
		scenarioJourneysA, scenarioJourneysB,
		runResA, runResB,
		forkA.Snapshot(), forkB.Snapshot(),
	)

	var disp analysis.Disposition
	switch {
	case runResA.Status != analysis.Complete || runResB.Status != analysis.Complete:
		disp = analysis.Inconclusive
	case hasDiff:
		disp = analysis.Different
	default:
		disp = analysis.Equivalent
	}

	return Comparison{
		Current:     scenarioJourneysA,
		roots:       [2][]FrameID{injFIDsA, injFIDsB},
		Expected:    scenarioJourneysB,
		Disposition: disp,
		Difference:  diff,
		Location:    location,
		Replay:      replaySpecs,
		Steps:       [2]int{runResA.Steps, runResB.Steps},
	}
}

func runScenarioFork(f *Fabric, budget int, window int, actions []Action) (RunResult, []FrameID, error) {
	f.initRunState()

	f.enqueueActions(actions)

	if budget <= 0 {
		return f.buildRunResult(StopNotRun, 0, nil, nil, budget, len(actions)), nil, nil
	}

	if f.err != nil {
		return f.buildRunResult(StopFault, 0, nil, nil, budget, len(actions)), nil, nil
	}

	var (
		fingerprints            []string
		cycle                   []string
		stop                    StopReason
		steps                   int
		lastFingerprint         = f.Fingerprint()
		wakeArrivalsSinceChange int
		injectFIDs              []FrameID
	)

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
		entry, ok := f.Step()
		if !ok {
			if arr.Kind == ArrivalAction && f.err != nil {
				return RunResult{}, nil, f.err
			}
			if f.err != nil {
				stop = StopFault
			} else {
				stop = StopQueueDrained
			}
			break
		}
		if arr.Kind == ArrivalAction && arr.action.Kind == ActionInject {
			injectFIDs = append(injectFIDs, fid)
		}
		steps++

		fp := f.Fingerprint()
		fingerprints = append(fingerprints, fp)

		if fp != lastFingerprint {
			lastFingerprint = fp
			wakeArrivalsSinceChange = 0
		}
		if entry.Kind == EntryWake {
			wakeArrivalsSinceChange++
		}

		if window >= 2 && len(fingerprints) >= 2*2 {
			if foundCycle, ok := detectCycle(fingerprints, window); ok {
				stop = StopOscillating
				cycle = foundCycle
				break
			}
		}

		if window > 0 && wakeArrivalsSinceChange >= window && f.queueHoldsOnlyWakes() && !f.hasPendingJourneys() {
			stop = StopConverged
			break
		}

		if f.err != nil {
			stop = StopFault
			break
		}
	}

	if stop == "" {
		stop = StopBudget
	}

	return f.buildRunResult(stop, steps, fingerprints, cycle, budget, f.pendingActionCount()), injectFIDs, nil
}

func collectScenarioJourneys(allJourneys []Journey, roots []FrameID) []Journey {
	descendants := make(map[FrameID]bool, len(roots))
	for _, root := range roots {
		descendants[root] = true
	}

	changed := true
	for changed {
		changed = false
		for i := range allJourneys {
			j := &allJourneys[i]
			if (j.Origin.Kind == OriginMirror || j.Origin.Kind == OriginReflection || j.Origin.Kind == OriginRelease) && descendants[j.Origin.Of] {
				if !descendants[j.FrameID] {
					descendants[j.FrameID] = true
					changed = true
				}
			}
		}
	}

	var result []Journey
	for i := range allJourneys {
		if descendants[allJourneys[i].FrameID] {
			result = append(result, allJourneys[i])
		}
	}
	return result
}

func diffFabricRuns(actions []Action, injFIDsA, injFIDsB []FrameID, journeysA, journeysB []Journey, runResA, runResB RunResult, snapA, snapB Snapshot) (analysis.Difference, DifferenceLocation, bool) {
	if len(injFIDsA) != len(injFIDsB) {
		return analysis.Difference{Observable: "multiplicity", Current: strconv.Itoa(len(injFIDsA)), Expected: strconv.Itoa(len(injFIDsB))}, DifferenceLocation{Area: DifferenceRun, Subject: "run", Field: "Injections.Length"}, true
	}
	for _, pair := range pairJourneys(actions, injFIDsA, injFIDsB, journeysA, journeysB) {
		location := pair.location
		if pair.current < 0 || pair.expected < 0 {
			location.Field = "Journey"
			return analysis.Difference{Observable: "multiplicity", Current: strconv.FormatBool(pair.current >= 0), Expected: strconv.FormatBool(pair.expected >= 0)}, location, true
		}
		if diff, field, found := diffJourney(journeysA[pair.current], journeysB[pair.expected]); found {
			location.Field = field
			return diff, location, true
		}
	}
	// Protocol journeys emitted during the run are not compared per journey. A
	// protocol emission carries no provenance link to whether the scenario or a
	// pre-scenario timer produced it, so it cannot be attributed to the scenario
	// window across two independently aged fabrics. Protocol divergence surfaces
	// instead in the final-state comparison below, which reads each device's
	// spanning-tree roles, neighbors, and forwarding database.

	if runResA.Stop != runResB.Stop {
		return analysis.Difference{
			Observable: "status",
			Current:    string(runResA.Stop),
			Expected:   string(runResB.Stop),
		}, DifferenceLocation{Area: DifferenceRun, Subject: "run", Field: "Stop"}, true
	}

	if runResA.Status != runResB.Status {
		return analysis.Difference{
			Observable: "status",
			Current:    string(runResA.Status),
			Expected:   string(runResB.Status),
		}, DifferenceLocation{Area: DifferenceRun, Subject: "run", Field: "Status"}, true
	}

	if runResA.Pending != runResB.Pending {
		return analysis.Difference{
			Observable: "status",
			Current:    fmt.Sprintf("%+v", runResA.Pending),
			Expected:   fmt.Sprintf("%+v", runResB.Pending),
		}, DifferenceLocation{Area: DifferenceRun, Subject: "run", Field: pendingField(runResA.Pending, runResB.Pending)}, true
	}

	if !analysis.SameIssues(runResA.Issues, runResB.Issues) {
		return analysis.Difference{
			Observable: "issues",
			Current:    formatIssues(runResA.Issues),
			Expected:   formatIssues(runResB.Issues),
		}, DifferenceLocation{Area: DifferenceRun, Subject: "run", Field: issueField(runResA.Issues, runResB.Issues)}, true
	}

	if diff, location, hasDiff := diffSnapshot(snapA, snapB); hasDiff {
		return diff, location, true
	}

	return analysis.Difference{}, DifferenceLocation{}, false
}

func sameCable(a, b *Cable) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.A == b.A && a.B == b.B && a.LengthMeters == b.LengthMeters && a.Medium == b.Medium && sameFault(a.Fault, b.Fault)
}

func formatIssues(issues []analysis.Issue) string {
	var codes []string
	for _, iss := range issues {
		codes = append(codes, string(iss.Code))
	}
	return fmt.Sprint(codes)
}

func formatEndpoint(dev, p string) string {
	if p == "" {
		return dev
	}
	return dev + "/" + p
}

func formatCable(c *Cable) string {
	if c == nil {
		return "<none>"
	}
	return fmt.Sprintf("%s:%s--%s:%s", c.A.Node, c.A.Port, c.B.Node, c.B.Port)
}
