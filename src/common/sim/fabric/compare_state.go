package fabric

import (
	"fmt"
	"maps"
	"slices"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
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
		if field := entryField(devA.Entries, devB.Entries); field != "" {
			return analysis.Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: %d fdb entries", name, len(devA.Entries)),
				Expected:   fmt.Sprintf("%s: %d fdb entries", name, len(devB.Entries)),
			}, DifferenceLocation{Area: DifferenceSnapshot, Subject: name, Field: "Entries." + field}, true
		}
		if field := portField(devA.Ports, devB.Ports); field != "" {
			return analysis.Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: ports mismatch", name),
				Expected:   fmt.Sprintf("%s: ports mismatch", name),
			}, DifferenceLocation{Area: DifferenceSnapshot, Subject: name, Field: "Ports." + field}, true
		}
		if field := neighborField(devA.Neighbors, devB.Neighbors); field != "" {
			return analysis.Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: neighbors mismatch", name),
				Expected:   fmt.Sprintf("%s: neighbors mismatch", name),
			}, DifferenceLocation{Area: DifferenceSnapshot, Subject: name, Field: "Neighbors." + field}, true
		}
		if !maps.Equal(devA.Roles, devB.Roles) {
			return analysis.Difference{
				Observable: "final state",
				Current:    fmt.Sprintf("%s: stp roles mismatch", name),
				Expected:   fmt.Sprintf("%s: stp roles mismatch", name),
			}, DifferenceLocation{Area: DifferenceSnapshot, Subject: name, Field: "Roles." + roleField(devA.Roles, devB.Roles)}, true
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

func entryField(a, b []bridge.Entry) string {
	if len(a) != len(b) {
		return "Length"
	}
	keys := make(map[string]bool, len(a))
	key := func(e bridge.Entry) string { return trace.CompositeKey(e.MAC.String(), fmt.Sprint(e.FID), e.Port) }
	for _, e := range a {
		keys[key(e)] = true
	}
	for _, e := range b {
		if !keys[key(e)] {
			return "[" + key(e) + "]"
		}
	}
	return ""
}

func portField(a, b []port.Port) string {
	if len(a) != len(b) {
		return "Length"
	}
	byName := make(map[string]port.Port, len(a))
	for _, p := range a {
		byName[p.Name] = p
	}
	for _, p := range b {
		q, ok := byName[p.Name]
		prefix := fmt.Sprintf("[%q]", p.Name)
		if !ok {
			return prefix
		}
		if q.AdminStatus != p.AdminStatus {
			return prefix + ".AdminStatus"
		}
		if q.OperStatus != p.OperStatus {
			return prefix + ".OperStatus"
		}
	}
	return ""
}

func neighborField(a, b []routing.NeighborEntry) string {
	if len(a) != len(b) {
		return "Length"
	}
	key := func(n routing.NeighborEntry) string { return trace.CompositeKey(n.Interface, n.Addr.String()) }
	byKey := make(map[string]routing.NeighborEntry, len(a))
	for _, n := range a {
		byKey[key(n)] = n
	}
	for _, n := range b {
		q, ok := byKey[key(n)]
		prefix := "[" + key(n) + "]"
		if !ok {
			return prefix
		}
		if q.MAC != n.MAC {
			return prefix + ".MAC"
		}
		if q.State != n.State {
			return prefix + ".State"
		}
	}
	return ""
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
		if field := faultField(q.Fault, l.Fault); field != "" {
			return prefix + ".Fault." + field
		}
	}
	return ""
}

func roleField(a, b map[string]stp.PortInfo) string {
	for _, name := range slices.Sorted(maps.Keys(a)) {
		p, ok := b[name]
		prefix := fmt.Sprintf("[%q]", name)
		if !ok {
			return prefix
		}
		q := a[name]
		if q.Tree.Kind != p.Tree.Kind {
			return prefix + ".Tree.Kind"
		}
		if q.Tree.ID != p.Tree.ID {
			return prefix + ".Tree.ID"
		}
		if q.Role != p.Role {
			return prefix + ".Role"
		}
		if q.State != p.State {
			return prefix + ".State"
		}
		if q.BlockReason != p.BlockReason {
			return prefix + ".BlockReason"
		}
		if q.Priority != p.Priority {
			return prefix + ".Priority"
		}
		if q.PathCost != p.PathCost {
			return prefix + ".PathCost"
		}
		if q.DesignatedRoot.Priority != p.DesignatedRoot.Priority {
			return prefix + ".DesignatedRoot.Priority"
		}
		if q.DesignatedRoot.Address != p.DesignatedRoot.Address {
			return prefix + ".DesignatedRoot.Address"
		}
		if q.Designated.Priority != p.Designated.Priority {
			return prefix + ".Designated.Priority"
		}
		if q.Designated.Address != p.Designated.Address {
			return prefix + ".Designated.Address"
		}
		if q.DesignatedPort != p.DesignatedPort {
			return prefix + ".DesignatedPort"
		}
		if q.DesignatedCost != p.DesignatedCost {
			return prefix + ".DesignatedCost"
		}
		if q.PointToPoint != p.PointToPoint {
			return prefix + ".PointToPoint"
		}
		if q.Edge != p.Edge {
			return prefix + ".Edge"
		}
		if q.ForwardTransitions != p.ForwardTransitions {
			return prefix + ".ForwardTransitions"
		}
		if q.TxBPDUs != p.TxBPDUs {
			return prefix + ".TxBPDUs"
		}
		if q.RxBPDUs != p.RxBPDUs {
			return prefix + ".RxBPDUs"
		}
		if q.BadBPDUs != p.BadBPDUs {
			return prefix + ".BadBPDUs"
		}
		if q.SendRSTP != p.SendRSTP {
			return prefix + ".SendRSTP"
		}
	}
	for _, name := range slices.Sorted(maps.Keys(b)) {
		if _, ok := a[name]; !ok {
			return fmt.Sprintf("[%q]", name)
		}
	}
	return ""
}
