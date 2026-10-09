package fabric

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// DifferenceArea identifies the behavioral walk that found a difference.
// Values are safe for concurrent reads.
type DifferenceArea string

const (
	// DifferenceJourney names an injection or one of its descendants.
	DifferenceJourney DifferenceArea = "journey"
	// DifferenceRun names execution stop state.
	DifferenceRun DifferenceArea = "run"
	// DifferenceSnapshot names final device or link state.
	DifferenceSnapshot DifferenceArea = "snapshot"
)

// InjectionLocation is stable across action removal. At is UTC without monotonic time.
// Values are safe for concurrent reads.
type InjectionLocation struct {
	Index int
	At    time.Time
}

// LineageStep identifies a child within its parent's same-kind, same-name children.
// Occurrence is zero-based. Values are safe for concurrent reads.
type LineageStep struct {
	Kind       JourneyOriginKind
	Mirror     string
	Occurrence int
}

// DifferenceLocation names an exact behavioral field. Only journey locations carry
// Injection and Lineage. Field entry ordinals exclude queue diagnostics.
// Treat its slices and pointer as immutable during concurrent reads.
type DifferenceLocation struct {
	Area      DifferenceArea
	Injection *InjectionLocation
	Lineage   []LineageStep
	Subject   string
	Field     string
}

type journeyPair struct {
	location          DifferenceLocation
	current, expected int
}

type lineageJourney struct {
	index   int
	lineage []LineageStep
	key     string
}

func injectionLineages(journeys []Journey, root FrameID) []lineageJourney {
	var result []lineageJourney
	var visit func(FrameID, []LineageStep)
	visit = func(id FrameID, lineage []LineageStep) {
		for i, j := range journeys {
			if j.FrameID == id {
				result = append(result, lineageJourney{i, slices.Clone(lineage), lineageKey(lineage)})
				break
			}
		}
		var children []int
		for i, j := range journeys {
			if j.Origin.Of == id && (j.Origin.Kind == OriginMirror || j.Origin.Kind == OriginReflection || j.Origin.Kind == OriginRelease) {
				children = append(children, i)
			}
		}
		slices.SortFunc(children, func(a, b int) int { return cmp.Compare(journeys[a].FrameID, journeys[b].FrameID) })
		counts := make(map[struct {
			kind   JourneyOriginKind
			mirror string
		}]int)
		for _, i := range children {
			j := journeys[i]
			key := struct {
				kind   JourneyOriginKind
				mirror string
			}{j.Origin.Kind, j.Origin.Mirror}
			child := append(slices.Clone(lineage), LineageStep{j.Origin.Kind, j.Origin.Mirror, counts[key]})
			counts[key]++
			visit(j.FrameID, child)
		}
	}
	visit(root, []LineageStep{{Kind: OriginInjection}})
	return result
}

func pairJourneys(actions []Action, rootsA, rootsB []FrameID, a, b []Journey) []journeyPair {
	var injections []Action
	for _, action := range actions {
		if action.Kind == ActionInject {
			injections = append(injections, action)
		}
	}
	var pairs []journeyPair
	for ordinal := 0; ordinal < min(len(rootsA), len(rootsB), len(injections)); ordinal++ {
		groupA, groupB := injectionLineages(a, rootsA[ordinal]), injectionLineages(b, rootsB[ordinal])
		action := injections[ordinal]
		injection := InjectionLocation{Index: action.Index, At: action.At.Round(0).UTC()}
		byA, byB := make(map[string]lineageJourney), make(map[string]lineageJourney)
		var keys []string
		for _, j := range groupA {
			byA[j.key] = j
			keys = append(keys, j.key)
		}
		for _, j := range groupB {
			byB[j.key] = j
			if _, ok := byA[j.key]; !ok {
				keys = append(keys, j.key)
			}
		}
		slices.Sort(keys)
		var common, missing []journeyPair
		for _, key := range keys {
			ja, oka := byA[key]
			jb, okb := byB[key]
			lineage := ja.lineage
			if !oka {
				lineage = jb.lineage
			}
			p := journeyPair{location: DifferenceLocation{Area: DifferenceJourney, Injection: new(injection), Lineage: slices.Clone(lineage), Subject: "journey"}, current: -1, expected: -1}
			if oka {
				p.current = ja.index
			}
			if okb {
				p.expected = jb.index
			}
			if oka && okb {
				common = append(common, p)
			} else {
				missing = append(missing, p)
			}
		}
		pairs = append(pairs, missing...)
		pairs = append(pairs, common...)
	}
	return pairs
}

func differingEndpointField(a, b Entry) string {
	if a.Device != b.Device {
		return "Device"
	}
	return "Port"
}

func cableField(a, b *Cable) string {
	if a == nil || b == nil {
		return "Presence"
	}
	if a.A.Node != b.A.Node {
		return "A.Node"
	}
	if a.A.Port != b.A.Port {
		return "A.Port"
	}
	if a.B.Node != b.B.Node {
		return "B.Node"
	}
	if a.B.Port != b.B.Port {
		return "B.Port"
	}
	if a.LengthMeters != b.LengthMeters {
		return "LengthMeters"
	}
	if a.Medium != b.Medium {
		return "Medium"
	}
	return "Fault." + faultField(a.Fault, b.Fault)
}

func faultField(a, b cablemodel.Fault) string {
	if a.Kind != b.Kind {
		return "Kind"
	}
	if a.N != b.N {
		return "N"
	}
	if !slices.Equal(a.Sequence, b.Sequence) {
		return "Sequence"
	}
	return ""
}

func lineageKey(lineage []LineageStep) string {
	var parts []string
	for _, step := range lineage {
		parts = append(parts, string(step.Kind), step.Mirror, fmt.Sprint(step.Occurrence))
	}
	return trace.CompositeKey(parts...)
}

func pendingField(a, b PendingWork) string {
	if a.Arrivals != b.Arrivals {
		return "Pending.Arrivals"
	}
	if a.Wakes != b.Wakes {
		return "Pending.Wakes"
	}
	if a.Egress != b.Egress {
		return "Pending.Egress"
	}
	return "Pending.Journeys"
}

func issueField(a, b []analysis.Issue) string {
	if len(a) != len(b) {
		return "Issues.Length"
	}
	for i := range a {
		prefix := fmt.Sprintf("Issues[%d].", i)
		if a[i].Code != b[i].Code {
			return prefix + "Code"
		}
		if a[i].Status != b[i].Status {
			return prefix + "Status"
		}
		if a[i].Scope.Compare(b[i].Scope) != 0 {
			return prefix + "Scope"
		}
	}
	return ""
}
