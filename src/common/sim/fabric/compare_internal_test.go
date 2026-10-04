package fabric

import (
	"testing"
	"time"
)

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
	if diff, found := diffJourney(current, withQueue); found {
		t.Errorf("diagnostic queue entry caused difference %+v", diff)
	}

	changedDelivery := withQueue
	changedDelivery.Deliveries = []Delivery{{Host: "h3", At: at.Add(time.Millisecond)}}
	if diff, found := diffJourney(current, changedDelivery); !found || diff.Observable != "path" {
		t.Errorf("changed delivery difference = %+v, found %t, want path", diff, found)
	}

	changedDrop := withQueue
	changedDrop.Entries = append([]Entry(nil), withQueue.Entries...)
	changedDrop.Entries[2] = Entry{At: at.Add(time.Millisecond), Kind: EntryDrop, Device: "sw1", Port: "1/1/2"}
	if diff, found := diffJourney(current, changedDrop); !found || diff.Observable != "journey terminal" {
		t.Errorf("changed drop difference = %+v, found %t, want journey terminal", diff, found)
	}
}
