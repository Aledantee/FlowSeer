package fabric

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
)

// Snapshot captures the current simulation clock, queued arrivals and egress frames, link states, device states,
// egress depths, and Busy, the endpoints whose transmission ends after the clock.
func (f *Fabric) Snapshot() Snapshot {
	var q []Arrival
	if len(f.queue) > 0 {
		q = slices.Clone(f.queue)
		slices.SortFunc(q, compareArrival)
		for i := range q {
			q[i].index = 0
		}
	}

	links := f.Links()

	devices := make(map[string]device.State, len(f.switchNames()))
	for _, name := range f.switchNames() {
		state := f.nodeDevice(name).State(f.clock)
		state.Counters = make(map[string]device.PortCounters)
		for portName, counters := range f.snapshotCounters(name) {
			state.Counters[portName] = device.PortCounters(counters)
		}
		devices[name] = state
	}

	var busy map[Endpoint]time.Time
	for ep, until := range f.busyUntil {
		if until.After(f.clock) {
			if busy == nil {
				busy = make(map[Endpoint]time.Time)
			}
			busy[ep] = until
		}
	}
	var queued map[Endpoint]int
	var egressDepths map[Endpoint]map[vlan.PCP]EgressDepth
	for ep, eq := range f.egress {
		if count := eq.pendingCount(); count > 0 {
			if queued == nil {
				queued = make(map[Endpoint]int)
			}
			queued[ep] = count
		}
		for pcp := range eq.depth {
			if eq.depth[pcp] == 0 && eq.peak[pcp] == 0 {
				continue
			}
			if egressDepths == nil {
				egressDepths = make(map[Endpoint]map[vlan.PCP]EgressDepth)
			}
			if egressDepths[ep] == nil {
				egressDepths[ep] = make(map[vlan.PCP]EgressDepth)
			}
			egressDepths[ep][vlan.PCP(pcp)] = EgressDepth{Depth: eq.depth[pcp], Peak: eq.peak[pcp]}
		}
	}

	return Snapshot{
		Clock:        f.clock,
		Queue:        q,
		Queued:       queued,
		Links:        links,
		Devices:      devices,
		Busy:         busy,
		EgressDepths: egressDepths,
	}
}
