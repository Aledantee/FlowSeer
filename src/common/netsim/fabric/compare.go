package fabric

import (
	"bytes"
	"maps"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Comparison reports the simulation outcomes of running a common scenario on two fabrics.
type Comparison struct {
	Current  []Journey
	Expected []Journey
	Same     bool
	Steps    [2]int
}

// Compare executes the scenario injections on both fabrics up to the step budget,
// comparing deliveries and drop reasons per frame. Both fabrics must be unused:
// journeys pair by frame id, which each fabric assigns from its own count, so a
// fabric that already injected is refused rather than compared on ids that no
// longer line up. An injection either fabric refuses is returned as the error,
// because a scenario one side never ran compares nothing. Compare consumes both
// fabrics: their clocks advance, counters accumulate, and dynamic entries learn
// and age.
func Compare(a, b *Fabric, scenario []Injection, budget int) (Comparison, error) {
	if len(a.journeys) > 0 || len(b.journeys) > 0 {
		return Comparison{}, errs.New().
			Attr("current_frames", len(a.journeys)).
			Attr("expected_frames", len(b.journeys)).
			Msg("compare needs two fabrics that have not injected")
	}
	for _, inj := range scenario {
		if _, err := a.Inject(inj); err != nil {
			return Comparison{}, errs.Wrap(err, "inject into the current fabric")
		}
		if _, err := b.Inject(inj); err != nil {
			return Comparison{}, errs.Wrap(err, "inject into the expected fabric")
		}
	}

	stepsA := a.Run(budget).Steps
	stepsB := b.Run(budget).Steps

	repA := a.Report()
	repB := b.Report()

	return Comparison{
		Current:  repA,
		Expected: repB,
		Same:     sameJourneys(repA, repB),
		Steps:    [2]int{stepsA, stepsB},
	}, nil
}

func sameJourneys(a, b []Journey) bool {
	if len(a) != len(b) {
		return false
	}
	mapB := make(map[FrameID]Journey, len(b))
	for _, j := range b {
		mapB[j.FrameID] = j
	}

	for _, jA := range a {
		jB, ok := mapB[jA.FrameID]
		if !ok {
			return false
		}
		if !sameDeliveries(jA.Deliveries, jB.Deliveries) {
			return false
		}
		if !sameDropReasons(jA.Entries, jB.Entries) {
			return false
		}
	}

	return true
}

func sameDeliveries(a, b []Delivery) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Host != b[i].Host {
			return false
		}
		if a[i].Frame.EtherType != b[i].Frame.EtherType {
			return false
		}
		if !slices.Equal(a[i].Frame.Tags, b[i].Frame.Tags) {
			return false
		}
		if !bytes.Equal(a[i].Frame.Payload, b[i].Frame.Payload) {
			return false
		}
	}

	return true
}

func sameDropReasons(a, b []Entry) bool {
	return maps.Equal(collectDropReasons(a), collectDropReasons(b))
}

func collectDropReasons(entries []Entry) map[trace.Reason]bool {
	set := make(map[trace.Reason]bool)
	for _, e := range entries {
		if e.Reason != "" {
			set[e.Reason] = true
		}
	}

	return set
}
