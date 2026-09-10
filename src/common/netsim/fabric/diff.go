package fabric

import (
	"cmp"
	"slices"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
)

// Diff computes the differences between two fabric configurations, reporting switch differences,
// cable additions, removals, and modifications, and host additions, removals, and moves.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change

	swNames := make(map[string]struct{})
	for name := range a.Switches {
		swNames[name] = struct{}{}
	}
	for name := range b.Switches {
		swNames[name] = struct{}{}
	}
	sortedSw := make([]string, 0, len(swNames))
	for name := range swNames {
		sortedSw = append(sortedSw, name)
	}
	slices.Sort(sortedSw)

	for _, name := range sortedSw {
		swA, inA := a.Switches[name]
		swB, inB := b.Switches[name]
		switch {
		case inA && !inB:
			changes = append(changes, trace.Change{
				Layer: Layer,
				Subject: trace.Subject{
					Kind: "switch",
					Key:  name,
				},
				Field: "",
				From:  swA,
				To:    nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer: Layer,
				Subject: trace.Subject{
					Kind: "switch",
					Key:  name,
				},
				Field: "",
				From:  nil,
				To:    swB,
			})
		case inA && inB:
			swChanges := vswitch.Diff(swA, swB)
			for _, ch := range swChanges {
				ch.Subject.Key = name + "/" + ch.Subject.Key
				changes = append(changes, ch)
			}
		}
	}

	cablesA := sortedCables(a.Cables)
	cablesB := sortedCables(b.Cables)

	mapA := make(map[string]Cable, len(cablesA))
	for _, c := range cablesA {
		mapA[cableKey(c)] = c
	}
	mapB := make(map[string]Cable, len(cablesB))
	for _, c := range cablesB {
		mapB[cableKey(c)] = c
	}

	for _, cA := range cablesA {
		key := cableKey(cA)
		cB, exists := mapB[key]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "",
				From:    cA,
				To:      nil,
			})
			continue
		}

		if cA.LengthMeters != cB.LengthMeters {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "length",
				From:    cA.LengthMeters,
				To:      cB.LengthMeters,
			})
		}
		if cA.TopSpeedBPS != cB.TopSpeedBPS {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "top_speed",
				From:    cA.TopSpeedBPS,
				To:      cB.TopSpeedBPS,
			})
		}
		if !sameFault(cA.Fault, cB.Fault) {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "fault",
				From:    cA.Fault,
				To:      cB.Fault,
			})
		}
	}

	for _, cB := range cablesB {
		key := cableKey(cB)
		if _, exists := mapA[key]; !exists {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "",
				From:    nil,
				To:      cB,
			})
		}
	}

	hostNames := make(map[string]struct{})
	for name := range a.Hosts {
		hostNames[name] = struct{}{}
	}
	for name := range b.Hosts {
		hostNames[name] = struct{}{}
	}
	sortedHosts := make([]string, 0, len(hostNames))
	for name := range hostNames {
		sortedHosts = append(sortedHosts, name)
	}
	slices.Sort(sortedHosts)

	for _, name := range sortedHosts {
		hA, inA := a.Hosts[name]
		hB, inB := b.Hosts[name]
		switch {
		case inA && !inB:
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   "",
				From:    hA,
				To:      nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   "",
				From:    nil,
				To:      hB,
			})
		case inA && inB:
			epA, okA := hostEndpoint(name, a.Cables)
			epB, okB := hostEndpoint(name, b.Cables)
			if okA && okB && epA != epB {
				changes = append(changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "port",
					From:    epA,
					To:      epB,
				})
			}
			if hA.Address != hB.Address {
				changes = append(changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "address",
					From:    hA.Address,
					To:      hB.Address,
				})
			}
			if !sameVLAN(hA.VLAN, hB.VLAN) {
				var fromVal, toVal any
				if hA.VLAN != nil {
					fromVal = *hA.VLAN
				}
				if hB.VLAN != nil {
					toVal = *hB.VLAN
				}
				changes = append(changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "vlan",
					From:    fromVal,
					To:      toVal,
				})
			}
		}
	}

	return changes
}

func sortedCables(cables []Cable) []Cable {
	if len(cables) == 0 {
		return nil
	}
	cp := make([]Cable, len(cables))
	copy(cp, cables)
	slices.SortFunc(cp, func(i, j Cable) int {
		if r := cmp.Compare(i.A.Node, j.A.Node); r != 0 {
			return r
		}
		if r := cmp.Compare(i.A.Port, j.A.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(i.B.Node, j.B.Node); r != 0 {
			return r
		}
		return cmp.Compare(i.B.Port, j.B.Port)
	})
	return cp
}

func cableKey(c Cable) string {
	return c.A.Node + ":" + c.A.Port + "-" + c.B.Node + ":" + c.B.Port
}

func sameFault(a, b Fault) bool {
	kindA := a.Kind
	if kindA == "" {
		kindA = FaultNone
	}
	kindB := b.Kind
	if kindB == "" {
		kindB = FaultNone
	}
	return kindA == kindB && a.N == b.N && slices.Equal(a.Sequence, b.Sequence)
}

func sameVLAN(a, b *vlan.ID) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a != nil && b != nil && *a != *b {
		return false
	}
	return true
}

func hostEndpoint(name string, cables []Cable) (Endpoint, bool) {
	for _, c := range cables {
		if c.A.Node == name {
			return c.B, true
		}
		if c.B.Node == name {
			return c.A, true
		}
	}
	return Endpoint{}, false
}
