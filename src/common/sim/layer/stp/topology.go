package stp

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// tcWhileDuration returns the topology change timer duration for port p on
// tree t: HelloTime plus one second on a port that sends RSTP, and Max Age plus
// Forward Delay of the root's times on one that does not.
func (l *Layer) tcWhileDuration(t *tree, p *portState) time.Duration {
	link := l.links[p.name]
	if link.sendRSTP {
		return l.helloTime + time.Second
	}
	maxAge, _, fwdDelay := l.times(t)

	return maxAge + fwdDelay
}

func (l *Layer) rootTopologyChangeActive(port string, now time.Time) bool {
	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		p := l.trees[id].ports[port]
		if p != nil && p.role == bpdu.RoleRoot && p.tcActive && !p.tcWhile.IsZero() && p.tcWhile.After(now) {
			return true
		}
	}

	return false
}

// initiateTopologyChange marks p active, increments the tree's topology change
// count, starts p's own tcWhile timer, emits on p if p is the root port, and
// propagates to other active ports on tree t.
func (l *Layer) initiateTopologyChange(t *tree, p *portState, now time.Time, flushes *[]layer.FlushTarget, emissions *[]layer.Emission) {
	p.tcActive = true
	t.topologyChangeCount++
	t.lastTopologyChange = now
	p.tcWhile = now.Add(l.tcWhileDuration(t, p))
	l.propagateTopologyChange(t, p.name, now, flushes, emissions)
}

// propagateTopologyChange starts tcWhile on every active non-edge port on tree t
// other than originPort, flushes that port's learned entries, and emits toward
// the root when the port is the root port.
func (l *Layer) propagateTopologyChange(t *tree, originPort string, now time.Time, flushes *[]layer.FlushTarget, emissions *[]layer.Emission) {
	fids := l.treeVLANs[t.id]
	for _, name := range l.portNames {
		if name == originPort {
			continue
		}
		p, ok := t.ports[name]
		if !ok || !p.tcActive {
			continue
		}
		link := l.links[name]
		if link.edge {
			continue
		}
		p.tcWhile = now.Add(l.tcWhileDuration(t, p))
		if flushes != nil {
			mergeFlushTarget(flushes, name, fids)
		}
		if p.role == bpdu.RoleRoot && emissions != nil {
			l.emitRootTC(t, p, now, emissions)
		}
	}
}

// deactivatePort is called when a port loses its role, becomes an edge, or goes
// down. It flushes the port's learned entries, stops its timer, leaves active,
// and raises nothing.
func (l *Layer) deactivatePort(t *tree, p *portState, flushes *[]layer.FlushTarget) {
	wasActive := p.tcActive
	p.tcActive = false
	p.tcWhile = time.Time{}
	if wasActive && flushes != nil {
		mergeFlushTarget(flushes, p.name, l.treeVLANs[t.id])
	}
}

// mergeFlushTarget records that port needs its fids flushed, merging into an
// existing target for the same port rather than appending a second one. The
// merge always widens toward "every FID": an existing empty FIDs absorbs a
// narrower fids unchanged, and a narrower existing FIDs is widened to empty
// when fids itself is empty. The result stays sorted and free of duplicates
// so Effects.Flush is deterministic.
func mergeFlushTarget(flushes *[]layer.FlushTarget, port string, fids []vlan.ID) {
	for i := range *flushes {
		target := &(*flushes)[i]
		if target.Port != port {
			continue
		}
		if len(target.FIDs) == 0 {
			return
		}
		if len(fids) == 0 {
			target.FIDs = nil

			return
		}
		merged := make([]vlan.ID, 0, len(target.FIDs)+len(fids))
		merged = append(merged, target.FIDs...)
		merged = append(merged, fids...)
		slices.Sort(merged)
		target.FIDs = slices.Compact(merged)

		return
	}

	var vids []vlan.ID
	if len(fids) > 0 {
		vids = slices.Clone(fids)
	}
	*flushes = append(*flushes, layer.FlushTarget{Port: port, FIDs: vids})
}
