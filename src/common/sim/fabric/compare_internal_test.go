package fabric

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
)

func TestDiffFabricRunsOneSidedJourneyPresence(t *testing.T) {
	root := Journey{FrameID: 10, Origin: JourneyOrigin{Kind: OriginInjection}}
	mirrorA := Journey{FrameID: 20, Origin: JourneyOrigin{Kind: OriginMirror, Of: 10, Mirror: "span-a"}}
	mirrorB := Journey{FrameID: 30, Origin: JourneyOrigin{Kind: OriginMirror, Of: 10, Mirror: "span-b"}}
	release := Journey{FrameID: 40, Origin: JourneyOrigin{Kind: OriginReflection, Of: 10}}
	cases := []struct {
		name    string
		a, b    []Journey
		lineage []LineageStep
	}{
		{"different mirror names", []Journey{root, mirrorA}, []Journey{root, mirrorB}, []LineageStep{{Kind: OriginInjection}, {Kind: OriginMirror, Mirror: "span-a"}}},
		{"mirror and reflection", []Journey{root, mirrorA}, []Journey{root, release}, []LineageStep{{Kind: OriginInjection}, {Kind: OriginMirror, Mirror: "span-a"}}},
		{"extra mirror", []Journey{root, mirrorA}, []Journey{root}, []LineageStep{{Kind: OriginInjection}, {Kind: OriginMirror, Mirror: "span-a"}}},
		{"extra reflection", []Journey{root, release}, []Journey{root}, []LineageStep{{Kind: OriginInjection}, {Kind: OriginReflection}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var differences []analysis.Difference
			for _, side := range []struct {
				name, current, expected string
				a, b                    []Journey
			}{
				{"current", "true", "false", tc.a, tc.b},
				{"expected", "false", "true", tc.b, tc.a},
			} {
				t.Run(side.name, func(t *testing.T) {
					action := locationAction(1)
					diff, location, found := diffFabricRuns([]Action{action}, []FrameID{10}, []FrameID{10}, side.a, side.b, RunResult{}, RunResult{}, Snapshot{}, Snapshot{})
					wantDiff := analysis.Difference{Observable: "multiplicity", Current: side.current, Expected: side.expected}
					if !found || diff != wantDiff {
						t.Errorf("difference = %+v, found %t, want %+v, true", diff, found, wantDiff)
					}
					wantLocation := DifferenceLocation{Area: DifferenceJourney, Injection: new(InjectionLocation{Index: 1, At: action.At}), Lineage: tc.lineage, Subject: "journey", Field: "Journey"}
					if !reflect.DeepEqual(location, wantLocation) {
						t.Errorf("location = %+v, want %+v", location, wantLocation)
					}
					differences = append(differences, diff)
				})
			}
			if differences[0] == differences[1] {
				t.Errorf("swapped differences = %+v and %+v, want different side presence", differences[0], differences[1])
			}
		})
	}
}

func TestDiffJourneyIgnoresQueueThresholdButKeepsBehavior(t *testing.T) {
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	current := Journey{
		State:  JourneyDelivered,
		Origin: JourneyOrigin{Kind: OriginInjection},
		Entries: []Entry{
			{At: at, Kind: EntryInjection, Device: "h1"},
			{At: at.Add(time.Millisecond), Kind: EntryDelivery, Device: "h2"},
		},
		Deliveries: []Delivery{{Host: "h2", At: at.Add(time.Millisecond)}},
	}
	withQueue := current
	withQueue.Entries = []Entry{
		current.Entries[0],
		{At: at.Add(time.Microsecond), Kind: EntryQueueThreshold, Device: "sw1", Port: "1/1/2"},
		current.Entries[1],
	}
	if diff, _, found := diffJourney(current, withQueue); found {
		t.Errorf("diagnostic queue entry caused difference %+v", diff)
	}

	changedDelivery := withQueue
	changedDelivery.Deliveries = []Delivery{{Host: "h3", At: at.Add(time.Millisecond)}}
	if diff, _, found := diffJourney(current, changedDelivery); !found || diff.Observable != "path" {
		t.Errorf("changed delivery difference = %+v, found %t, want path", diff, found)
	}

	changedDrop := withQueue
	changedDrop.Entries = append([]Entry(nil), withQueue.Entries...)
	changedDrop.Entries[2] = Entry{At: at.Add(time.Millisecond), Kind: EntryDrop, Device: "sw1", Port: "1/1/2"}
	if diff, _, found := diffJourney(current, changedDrop); !found || diff.Observable != "journey terminal" {
		t.Errorf("changed drop difference = %+v, found %t, want journey terminal", diff, found)
	}
}
