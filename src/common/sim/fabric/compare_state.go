package fabric

import (
	"fmt"
	"maps"
	"slices"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
)

func diffSnapshot(snapA, snapB Snapshot) (analysis.Difference, DifferenceLocation, bool) {
	for _, name := range slices.Sorted(maps.Keys(snapA.Devices)) {
		devA := snapA.Devices[name]
		devB, ok := snapB.Devices[name]
		if !ok {
			return analysis.Difference{
				Observable: "final state",
				Current:    name,
				Expected:   "<absent>",
			}, DifferenceLocation{Area: DifferenceSnapshot, Subject: name, Field: "Device"}, true
		}
		if field, current, expected := devA.Diff(devB); field != "" {
			return analysis.Difference{Observable: "final state", Current: name + ": " + current, Expected: name + ": " + expected}, DifferenceLocation{Area: DifferenceSnapshot, Subject: name, Field: field}, true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(snapB.Devices)) {
		if _, ok := snapA.Devices[name]; !ok {
			return analysis.Difference{
				Observable: "final state",
				Current:    "<absent>",
				Expected:   name,
			}, DifferenceLocation{Area: DifferenceSnapshot, Subject: name, Field: "Device"}, true
		}
	}
	if field := linkField(snapA.Links, snapB.Links); field != "" {
		return analysis.Difference{
			Observable: "final state",
			Current:    "links mismatch",
			Expected:   "links mismatch",
		}, DifferenceLocation{Area: DifferenceSnapshot, Subject: "fabric", Field: "Links." + field}, true
	}
	return analysis.Difference{}, DifferenceLocation{}, false
}

func linkField(a, b []Link) string {
	if len(a) != len(b) {
		return "Length"
	}
	key := func(l Link) string { return fmt.Sprintf("%s-%s", l.A.Canonical(), l.B.Canonical()) }
	byKey := make(map[string]Link, len(a))
	for _, l := range a {
		byKey[key(l)] = l
	}
	for _, l := range b {
		q, ok := byKey[key(l)]
		prefix := "[" + key(l) + "]"
		if !ok {
			return prefix
		}
		if q.A.Speed.SpeedBPS != l.A.Speed.SpeedBPS {
			return prefix + ".A.Speed.SpeedBPS"
		}
		if q.B.Speed.SpeedBPS != l.B.Speed.SpeedBPS {
			return prefix + ".B.Speed.SpeedBPS"
		}
		if q.A.Oper != l.A.Oper {
			return prefix + ".A.Oper"
		}
		if q.B.Oper != l.B.Oper {
			return prefix + ".B.Oper"
		}
		if field := faultField(q.Cable.Fault, l.Cable.Fault); field != "" {
			return prefix + ".Fault." + field
		}
	}
	return ""
}
