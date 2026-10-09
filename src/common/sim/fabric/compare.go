package fabric

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
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
// (injections, records, faults, configure, and mcheck) to the forks, runs each fork up to the step budget,
// and compares their behavioral observables.
// The caller's fabrics a and b are never stepped, injected, or learned into.
// Injected and recorded roots pair by injection ordinal. Attached frames pair by
// attachment and frame ordinal, including frames present on only one side.
// Pre-scenario journeys are excluded. Faults are topology events, not journeys.
// A settled aggregate pairs by what the scenario added to its flow's statistics on each side.
// A latency extreme or hold count that either side had before the scenario cannot be
// separated from what the scenario added, so it is not compared. Equal statistics cannot
// prove journey equivalence, so the comparison is Inconclusive without another difference.
// Comparison walks, per paired journey, State, Origin, ordered Entries (path hops,
// cable crossings, deliveries, and drops with reason and location), Deliveries,
// Protocol, and carried frame content, then the run's Stop reason, Status, Pending
// work, and Issues, then the final Snapshot behavioral state. Diagnostic metadata,
// evidence, semantic trace text, and raw convergence diagnostics never cause a Different.
// On Different, Difference names the first differing observable with both sides' values
// and Replay carries pre-action checkpoints for both fabrics ([0] for current,
// [1] for candidate) to reproduce the divergence. Actions before either fabric's
// clock are refused before either fork executes them.
func Compare(a, b *Fabric, scenario Scenario, options RunOptions) Comparison {
	forkA := a.Fork()
	forkB := b.Fork()

	normSc := scenario.Clone()
	if normSc.Name == "" {
		normSc.Name = "compare-scenario"
	}
	norm, err := normSc.Normalize()
	if err == nil {
		err = options.Validate()
	}
	replaySpecA := ReplaySpec{
		checkpoint: a.Fork(),
		Scenario:   normSc.Clone(),
		Options:    options,
	}
	replaySpecB := ReplaySpec{
		checkpoint: b.Fork(),
		Scenario:   normSc.Clone(),
		Options:    options,
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

	preA, preB := forkA.Flows(), forkB.Flows()
	runResA, errA := forkA.runWithActions(options.Budget, options.Window, norm.Actions)
	runResB, errB := forkB.runWithActions(options.Budget, options.Window, norm.Actions)
	injFIDsA, injFIDsB := rootIDs(runResA.roots), rootIDs(runResB.roots)

	allJourneysA := forkA.Report()
	allJourneysB := forkB.Report()

	scenarioJourneysA := collectScenarioJourneys(allJourneysA, injFIDsA)
	scenarioJourneysB := collectScenarioJourneys(allJourneysB, injFIDsB)

	diff, location, hasDiff, uncertain := diffRunRoots(runResA.roots, runResB.roots, scenarioJourneysA, scenarioJourneysB, forkA.Flows(), forkB.Flows(), preA, preB)
	if !hasDiff {
		diff, location, hasDiff = diffFabricRuns(nil, nil, nil, nil, nil, runResA, runResB, forkA.Snapshot(), forkB.Snapshot())
	}

	var disp analysis.Disposition
	switch {
	case errA != nil || errB != nil || runResA.Status != analysis.Complete || runResB.Status != analysis.Complete:
		disp = analysis.Inconclusive
	case hasDiff:
		disp = analysis.Different
	case uncertain:
		disp = analysis.Inconclusive
	default:
		disp = analysis.Equivalent
	}

	runErr := errA
	if runErr == nil {
		runErr = errB
	}
	return Comparison{
		Current:     scenarioJourneysA,
		roots:       [2][]FrameID{actionRootIDs(runResA.roots), actionRootIDs(runResB.roots)},
		Expected:    scenarioJourneysB,
		Disposition: disp,
		Difference:  diff,
		Location:    location,
		Replay:      replaySpecs,
		Steps:       [2]int{runResA.Steps, runResB.Steps},
		Err:         runErr,
	}
}

func rootIDs(roots []runRoot) []FrameID {
	var ids []FrameID
	for _, root := range roots {
		ids = append(ids, root.fid)
	}
	return ids
}

func actionRootIDs(roots []runRoot) []FrameID {
	var ids []FrameID
	for _, root := range roots {
		if root.injection != nil {
			ids = append(ids, root.fid)
		}
	}
	return ids
}

type rootKey struct {
	source       bool
	index, frame int
}

func (r runRoot) key() rootKey {
	if r.source != nil {
		return rootKey{source: true, index: r.source.Attachment, frame: r.source.Frame}
	}
	return rootKey{index: r.ordinal}
}

func diffRunRoots(a, b []runRoot, journeysA, journeysB []Journey, flowsA, flowsB, preA, preB map[FlowID]FlowStats) (analysis.Difference, DifferenceLocation, bool, bool) {
	byA, byB := make(map[rootKey]runRoot), make(map[rootKey]runRoot)
	for _, r := range a {
		byA[r.key()] = r
	}
	for _, r := range b {
		byB[r.key()] = r
	}
	keys := slices.Collect(maps.Keys(byA))
	for key := range byB {
		if _, ok := byA[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b rootKey) int {
		if a.source != b.source {
			if a.source {
				return 1
			}
			return -1
		}
		if c := cmp.Compare(a.index, b.index); c != 0 {
			return c
		}
		return cmp.Compare(a.frame, b.frame)
	})
	uncertain := false
	for _, key := range keys {
		ra, oka := byA[key]
		rb, okb := byB[key]
		root := ra
		if !oka {
			root = rb
		}
		location := DifferenceLocation{Area: DifferenceJourney, Injection: root.injection, Source: root.source, Subject: "journey"}
		if !oka || !okb {
			location.Field = "Root"
			return analysis.Difference{Observable: "multiplicity", Current: strconv.FormatBool(oka), Expected: strconv.FormatBool(okb)}, location, true, uncertain
		}
		retainedA := slices.ContainsFunc(journeysA, func(j Journey) bool { return j.FrameID == ra.fid })
		retainedB := slices.ContainsFunc(journeysB, func(j Journey) bool { return j.FrameID == rb.fid })
		if (!retainedA && ra.retention == RetainAggregate) || (!retainedB && rb.retention == RetainAggregate) {
			if diff, field, found := diffFlowStats(flowsA[ra.flow], flowsB[rb.flow], preA[ra.flow], preB[rb.flow]); found {
				location.Field = "Flow." + field
				return diff, location, true, uncertain
			}
			uncertain = true
			continue
		}
		action := Action{ID: "compare-1", Kind: ActionInject}
		if root.injection != nil {
			action.Index, action.At = root.injection.Index, root.injection.At
		}
		for _, pair := range pairJourneys([]Action{action}, []FrameID{ra.fid}, []FrameID{rb.fid}, journeysA, journeysB) {
			location.Lineage = pair.location.Lineage
			if pair.current < 0 || pair.expected < 0 {
				location.Field = "Journey"
				return analysis.Difference{Observable: "multiplicity", Current: strconv.FormatBool(pair.current >= 0), Expected: strconv.FormatBool(pair.expected >= 0)}, location, true, uncertain
			}
			if diff, field, found := diffJourney(journeysA[pair.current], journeysB[pair.expected]); found {
				location.Field = field
				return diff, location, true, uncertain
			}
		}
	}
	return analysis.Difference{}, DifferenceLocation{}, false, uncertain
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
			if (j.Origin.Kind == OriginMirror || j.Origin.Kind == OriginReflection) && descendants[j.Origin.Of] {
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
