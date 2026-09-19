package fabric

import (
	"bytes"
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

// Difference describes the first behavioral observable that differed between two
// fabric evaluations, naming the observable and the values observed on both sides.
type Difference struct {
	Observable string
	Current    string
	Expected   string
}

// String returns a human-readable representation of the difference, or empty if none.
func (d Difference) String() string {
	if d.Observable == "" {
		return ""
	}
	return d.Observable + ": current=" + d.Current + ", expected=" + d.Expected
}

// Comparison reports the simulation outcomes of running a common scenario on two fabrics.
type Comparison struct {
	Current     []Journey
	Expected    []Journey
	Disposition analysis.Disposition
	Difference  Difference
	Replay      [2]ReplaySpec
	Steps       [2]int
	Err         error
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
// and Replay carries immutable replay specifications for both fabrics ([0] for current,
// [1] for candidate) to reproduce the divergence.
func Compare(a, b *Fabric, scenario Scenario, budget int) Comparison {
	forkA := a.Fork()
	forkB := b.Fork()

	normSc := cloneScenarioForReplay(scenario)
	if normSc.Name == "" {
		normSc.Name = "compare-scenario"
	}
	if budget > 0 {
		normSc.Budget = budget
	}

	norm, err := normSc.Normalize()
	replaySpecA := ReplaySpec{
		Contract: ReplayContract,
		Spec:     a.Spec(),
		Scenario: normSc.Clone(),
	}
	replaySpecB := ReplaySpec{
		Contract: ReplayContract,
		Spec:     b.Spec(),
		Scenario: normSc.Clone(),
	}
	replaySpecA.Scenario.Spec = ConstructionSpec{}
	replaySpecB.Scenario.Spec = ConstructionSpec{}
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
	replaySpecA.Scenario.Spec = ConstructionSpec{}
	replaySpecB.Scenario.Spec = ConstructionSpec{}
	replaySpecs = [2]ReplaySpec{replaySpecA, replaySpecB}

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

	diff, hasDiff := diffFabricRuns(
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
		Expected:    scenarioJourneysB,
		Disposition: disp,
		Difference:  diff,
		Replay:      replaySpecs,
		Steps:       [2]int{runResA.Steps, runResB.Steps},
	}
}

func cloneScenarioForReplay(s Scenario) Scenario {
	cp := s.Clone()
	cp.Actions = make([]Action, len(s.Actions))
	for i, a := range s.Actions {
		aCp := a.Clone()
		if a.Inject != nil {
			injCp := *a.Inject
			injCp.Frame = cloneFrame(a.Inject.Frame)
			injCp.Packet = clonePacket(a.Inject.Packet)
			aCp.Inject = &injCp
		}
		cp.Actions[i] = aCp
	}
	return cp
}

func runScenarioFork(f *Fabric, budget int, window int, actions []Action) (RunResult, []FrameID, error) {
	f.initRunState()

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
		pendingActions          = actions
		injectFIDs              []FrameID
	)

	for steps < budget {
		if f.err != nil {
			stop = StopFault
			break
		}

		if len(pendingActions) > 0 {
			if len(f.queue) == 0 || !pendingActions[0].At.After(f.queue[0].At) {
				act := pendingActions[0]
				pendingActions = pendingActions[1:]
				if act.Kind == ActionInject {
					fid, err := f.Inject(*act.Inject)
					if err != nil {
						return RunResult{}, nil, err
					}
					injectFIDs = append(injectFIDs, fid)
				} else {
					if err := f.applyAction(act); err != nil {
						return RunResult{}, nil, err
					}
				}
				continue
			}
		}

		if len(f.queue) == 0 {
			stop = StopQueueDrained
			break
		}

		entry, ok := f.Step()
		if !ok {
			if f.err != nil {
				stop = StopFault
			} else {
				stop = StopQueueDrained
			}
			break
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

		if window > 0 && len(pendingActions) == 0 && wakeArrivalsSinceChange >= window && f.queueHoldsOnlyWakes() && !f.hasPendingJourneys() {
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

	return f.buildRunResult(stop, steps, fingerprints, cycle, budget, len(pendingActions)), injectFIDs, nil
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
			if (j.Origin.Kind == OriginMirror || j.Origin.Kind == OriginRelease) && descendants[j.Origin.Of] {
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

func diffFabricRuns(
	injFIDsA, injFIDsB []FrameID,
	journeysA, journeysB []Journey,
	runResA, runResB RunResult,
	snapA, snapB Snapshot,
) (Difference, bool) {
	if len(injFIDsA) != len(injFIDsB) {
		return Difference{
			Observable: "multiplicity",
			Current:    strconv.Itoa(len(injFIDsA)),
			Expected:   strconv.Itoa(len(injFIDsB)),
		}, true
	}
	for i := 0; i < len(injFIDsA); i++ {
		groupA := collectInjectionJourneys(journeysA, injFIDsA[i])
		groupB := collectInjectionJourneys(journeysB, injFIDsB[i])

		if len(groupA) != len(groupB) {
			return Difference{
				Observable: "multiplicity",
				Current:    strconv.Itoa(len(groupA)),
				Expected:   strconv.Itoa(len(groupB)),
			}, true
		}

		for k := 0; k < len(groupA); k++ {
			if diff, hasDiff := diffJourney(groupA[k], groupB[k]); hasDiff {
				return diff, true
			}
		}
	}

	// Protocol journeys emitted during the run are not compared per journey. A
	// protocol emission carries no provenance link to whether the scenario or a
	// pre-scenario timer produced it, so it cannot be attributed to the scenario
	// window across two independently aged fabrics. Protocol divergence surfaces
	// instead in the final-state comparison below, which reads each device's
	// spanning-tree roles, neighbors, and forwarding database.

	if runResA.Stop != runResB.Stop {
		return Difference{
			Observable: "status",
			Current:    string(runResA.Stop),
			Expected:   string(runResB.Stop),
		}, true
	}

	if runResA.Status != runResB.Status {
		return Difference{
			Observable: "status",
			Current:    string(runResA.Status),
			Expected:   string(runResB.Status),
		}, true
	}

	if runResA.Pending != runResB.Pending {
		return Difference{
			Observable: "status",
			Current:    fmt.Sprintf("%+v", runResA.Pending),
			Expected:   fmt.Sprintf("%+v", runResB.Pending),
		}, true
	}

	if !sameIssues(runResA.Issues, runResB.Issues) {
		return Difference{
			Observable: "issues",
			Current:    formatIssues(runResA.Issues),
			Expected:   formatIssues(runResB.Issues),
		}, true
	}

	if diff, hasDiff := diffSnapshot(snapA, snapB); hasDiff {
		return diff, true
	}

	return Difference{}, false
}

func collectInjectionJourneys(journeys []Journey, root FrameID) []Journey {
	var primary *Journey
	descendants := make(map[FrameID]bool)
	descendants[root] = true

	for i := range journeys {
		if journeys[i].FrameID == root {
			primary = &journeys[i]
		}
	}

	changed := true
	for changed {
		changed = false
		for i := range journeys {
			j := &journeys[i]
			if (j.Origin.Kind == OriginMirror || j.Origin.Kind == OriginRelease) && descendants[j.Origin.Of] {
				if !descendants[j.FrameID] {
					descendants[j.FrameID] = true
					changed = true
				}
			}
		}
	}

	var result []Journey
	if primary != nil {
		result = append(result, *primary)
	}
	for i := range journeys {
		j := &journeys[i]
		if j.FrameID != root && descendants[j.FrameID] {
			result = append(result, *j)
		}
	}
	if len(result) > 1 {
		slices.SortFunc(result[1:], func(a, b Journey) int {
			if c := cmp.Compare(a.Origin.Mirror, b.Origin.Mirror); c != 0 {
				return c
			}
			return cmp.Compare(a.FrameID, b.FrameID)
		})
	}

	return result
}

func diffJourney(jA, jB Journey) (Difference, bool) {
	if jA.State != jB.State {
		return Difference{
			Observable: "journey terminal",
			Current:    string(jA.State),
			Expected:   string(jB.State),
		}, true
	}

	if jA.Origin.Kind != jB.Origin.Kind {
		return Difference{
			Observable: "origin",
			Current:    string(jA.Origin.Kind),
			Expected:   string(jB.Origin.Kind),
		}, true
	}
	if jA.Origin.Mirror != jB.Origin.Mirror {
		return Difference{
			Observable: "origin",
			Current:    jA.Origin.Mirror,
			Expected:   jB.Origin.Mirror,
		}, true
	}

	if jA.Protocol != jB.Protocol {
		return Difference{
			Observable: "protocol",
			Current:    strconv.FormatBool(jA.Protocol),
			Expected:   strconv.FormatBool(jB.Protocol),
		}, true
	}

	minEntries := min(len(jA.Entries), len(jB.Entries))
	for k := 0; k < minEntries; k++ {
		eA := jA.Entries[k]
		eB := jB.Entries[k]

		if eA.Kind != eB.Kind {
			if eA.Kind == EntryDrop || eB.Kind == EntryDrop {
				return Difference{
					Observable: "journey terminal",
					Current:    string(eA.Kind),
					Expected:   string(eB.Kind),
				}, true
			}
			return Difference{
				Observable: "path",
				Current:    string(eA.Kind),
				Expected:   string(eB.Kind),
			}, true
		}

		if eA.Device != eB.Device || eA.Port != eB.Port {
			if eA.Kind == EntryDrop || eB.Kind == EntryDrop {
				return Difference{
					Observable: "drop location",
					Current:    formatEndpoint(eA.Device, eA.Port),
					Expected:   formatEndpoint(eB.Device, eB.Port),
				}, true
			}
			return Difference{
				Observable: "path",
				Current:    formatEndpoint(eA.Device, eA.Port),
				Expected:   formatEndpoint(eB.Device, eB.Port),
			}, true
		}

		if !sameCable(eA.Cable, eB.Cable) {
			return Difference{
				Observable: "path",
				Current:    formatCable(eA.Cable),
				Expected:   formatCable(eB.Cable),
			}, true
		}

		if eA.Reason != eB.Reason {
			return Difference{
				Observable: "drop reason",
				Current:    string(eA.Reason),
				Expected:   string(eB.Reason),
			}, true
		}

		if eA.PCP != eB.PCP {
			return Difference{
				Observable: "journey rewrite",
				Current:    strconv.Itoa(int(eA.PCP)),
				Expected:   strconv.Itoa(int(eB.PCP)),
			}, true
		}

		if !eA.At.Equal(eB.At) {
			return Difference{
				Observable: "timing",
				Current:    eA.At.Format(time.RFC3339Nano),
				Expected:   eB.At.Format(time.RFC3339Nano),
			}, true
		}
		if eA.Latency != eB.Latency {
			return Difference{
				Observable: "timing",
				Current:    eA.Latency.String(),
				Expected:   eB.Latency.String(),
			}, true
		}
		if eA.Serialization != eB.Serialization {
			return Difference{
				Observable: "timing",
				Current:    eA.Serialization.String(),
				Expected:   eB.Serialization.String(),
			}, true
		}
		if eA.Wait != eB.Wait {
			return Difference{
				Observable: "timing",
				Current:    eA.Wait.String(),
				Expected:   eB.Wait.String(),
			}, true
		}

		if eA.Result != nil && eB.Result != nil {
			if diff, hasDiff := diffHopResult(*eA.Result, *eB.Result); hasDiff {
				return diff, true
			}
		}
	}

	if len(jA.Entries) != len(jB.Entries) {
		return Difference{
			Observable: "path",
			Current:    fmt.Sprintf("%d entries", len(jA.Entries)),
			Expected:   fmt.Sprintf("%d entries", len(jB.Entries)),
		}, true
	}

	if len(jA.Deliveries) != len(jB.Deliveries) {
		return Difference{
			Observable: "multiplicity",
			Current:    strconv.Itoa(len(jA.Deliveries)),
			Expected:   strconv.Itoa(len(jB.Deliveries)),
		}, true
	}

	for k := 0; k < len(jA.Deliveries); k++ {
		dA := jA.Deliveries[k]
		dB := jB.Deliveries[k]

		if dA.Host != dB.Host {
			return Difference{
				Observable: "path",
				Current:    dA.Host,
				Expected:   dB.Host,
			}, true
		}
		if !dA.At.Equal(dB.At) {
			return Difference{
				Observable: "timing",
				Current:    dA.At.Format(time.RFC3339Nano),
				Expected:   dB.At.Format(time.RFC3339Nano),
			}, true
		}
		if dA.Frame.Dst != dB.Frame.Dst {
			return Difference{
				Observable: "journey terminal",
				Current:    dA.Frame.Dst.String(),
				Expected:   dB.Frame.Dst.String(),
			}, true
		}
		if dA.Frame.Src != dB.Frame.Src {
			return Difference{
				Observable: "journey terminal",
				Current:    dA.Frame.Src.String(),
				Expected:   dB.Frame.Src.String(),
			}, true
		}
		if dA.Frame.EtherType != dB.Frame.EtherType {
			return Difference{
				Observable: "journey terminal",
				Current:    dA.Frame.EtherType.String(),
				Expected:   dB.Frame.EtherType.String(),
			}, true
		}
		if !slices.Equal(dA.Frame.Tags, dB.Frame.Tags) {
			return Difference{
				Observable: "journey terminal",
				Current:    fmt.Sprint(dA.Frame.Tags),
				Expected:   fmt.Sprint(dB.Frame.Tags),
			}, true
		}
		if !bytes.Equal(dA.Frame.Payload, dB.Frame.Payload) {
			return Difference{
				Observable: "journey terminal",
				Current:    fmt.Sprintf("%x", dA.Frame.Payload),
				Expected:   fmt.Sprintf("%x", dB.Frame.Payload),
			}, true
		}
	}

	return Difference{}, false
}

func diffHopResult(rA, rB vswitch.ForwardResult) (Difference, bool) {
	if len(rA.Egress) != len(rB.Egress) {
		return Difference{
			Observable: "journey rewrite",
			Current:    fmt.Sprintf("%d egress frames", len(rA.Egress)),
			Expected:   fmt.Sprintf("%d egress frames", len(rB.Egress)),
		}, true
	}
	for i := range rA.Egress {
		egA := rA.Egress[i]
		egB := rB.Egress[i]
		if egA.Frame.Dst != egB.Frame.Dst {
			return Difference{
				Observable: "journey rewrite",
				Current:    egA.Frame.Dst.String(),
				Expected:   egB.Frame.Dst.String(),
			}, true
		}
		if egA.Frame.Src != egB.Frame.Src {
			return Difference{
				Observable: "journey rewrite",
				Current:    egA.Frame.Src.String(),
				Expected:   egB.Frame.Src.String(),
			}, true
		}
		if egA.Frame.EtherType != egB.Frame.EtherType {
			return Difference{
				Observable: "journey rewrite",
				Current:    egA.Frame.EtherType.String(),
				Expected:   egB.Frame.EtherType.String(),
			}, true
		}
		if !slices.Equal(egA.Frame.Tags, egB.Frame.Tags) {
			return Difference{
				Observable: "journey rewrite",
				Current:    fmt.Sprint(egA.Frame.Tags),
				Expected:   fmt.Sprint(egB.Frame.Tags),
			}, true
		}
		if !bytes.Equal(egA.Frame.Payload, egB.Frame.Payload) {
			return Difference{
				Observable: "journey rewrite",
				Current:    fmt.Sprintf("%x", egA.Frame.Payload),
				Expected:   fmt.Sprintf("%x", egB.Frame.Payload),
			}, true
		}
	}
	return Difference{}, false
}

func clonePacket(p *Packet) *Packet {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Payload != nil {
		cp.Payload = slices.Clone(p.Payload)
	}
	return &cp
}

func diffSnapshot(snapA, snapB Snapshot) (Difference, bool) {
	for _, name := range slices.Sorted(maps.Keys(snapA.Devices)) {
		devA := snapA.Devices[name]
		devB, ok := snapB.Devices[name]
		if !ok {
			return Difference{
				Observable: "final state",
				Current:    name,
				Expected:   "<absent>",
			}, true
		}
		if !sameEntries(devA.Entries, devB.Entries) {
			return Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: %d fdb entries", name, len(devA.Entries)),
				Expected:   fmt.Sprintf("%s: %d fdb entries", name, len(devB.Entries)),
			}, true
		}
		if !samePorts(devA.Ports, devB.Ports) {
			return Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: ports mismatch", name),
				Expected:   fmt.Sprintf("%s: ports mismatch", name),
			}, true
		}
		if !sameNeighbors(devA.Neighbors, devB.Neighbors) {
			return Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: neighbors mismatch", name),
				Expected:   fmt.Sprintf("%s: neighbors mismatch", name),
			}, true
		}
		if !maps.Equal(devA.Roles, devB.Roles) {
			return Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: stp roles mismatch", name),
				Expected:   fmt.Sprintf("%s: stp roles mismatch", name),
			}, true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(snapB.Devices)) {
		if _, ok := snapA.Devices[name]; !ok {
			return Difference{
				Observable: "final state",
				Current:    "<absent>",
				Expected:   name,
			}, true
		}
	}
	if !sameLinks(snapA.Links, snapB.Links) {
		return Difference{
			Observable: "final state",
			Current:    "links mismatch",
			Expected:   "links mismatch",
		}, true
	}
	return Difference{}, false
}

func sameEntries(a, b []bridge.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	mapA := make(map[string]bridge.Entry, len(a))
	for _, e := range a {
		mapA[fmt.Sprintf("%s/%d/%s", e.MAC, e.FID, e.Port)] = e
	}
	for _, e := range b {
		if _, ok := mapA[fmt.Sprintf("%s/%d/%s", e.MAC, e.FID, e.Port)]; !ok {
			return false
		}
	}
	return true
}

func samePorts(a, b []port.Port) bool {
	if len(a) != len(b) {
		return false
	}
	mapA := make(map[string]port.Port, len(a))
	for _, p := range a {
		mapA[p.Name] = p
	}
	for _, pB := range b {
		pA, ok := mapA[pB.Name]
		if !ok || pA.AdminStatus != pB.AdminStatus || pA.OperStatus != pB.OperStatus {
			return false
		}
	}
	return true
}

func sameNeighbors(a, b []routing.NeighborEntry) bool {
	if len(a) != len(b) {
		return false
	}
	mapA := make(map[string]routing.NeighborEntry, len(a))
	for _, n := range a {
		mapA[fmt.Sprintf("%s/%s", n.Interface, n.Addr)] = n
	}
	for _, nB := range b {
		nA, ok := mapA[fmt.Sprintf("%s/%s", nB.Interface, nB.Addr)]
		if !ok || nA.MAC != nB.MAC || nA.State != nB.State {
			return false
		}
	}
	return true
}

func sameLinks(a, b []Link) bool {
	if len(a) != len(b) {
		return false
	}
	mapA := make(map[string]Link, len(a))
	for _, l := range a {
		mapA[fmt.Sprintf("%s-%s", l.A.Canonical(), l.B.Canonical())] = l
	}
	for _, lB := range b {
		lA, ok := mapA[fmt.Sprintf("%s-%s", lB.A.Canonical(), lB.B.Canonical())]
		if !ok || lA.A.Speed.SpeedBPS != lB.A.Speed.SpeedBPS || lA.B.Speed.SpeedBPS != lB.B.Speed.SpeedBPS ||
			lA.A.Oper != lB.A.Oper || lA.B.Oper != lB.B.Oper || !sameFault(lA.Fault, lB.Fault) {
			return false
		}
	}
	return true
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

func sameIssues(a, b []analysis.Issue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Code != b[i].Code || a[i].Status != b[i].Status || a[i].Scope.Compare(b[i].Scope) != 0 {
			return false
		}
	}
	return true
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
