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

type topologyChangeEmissions struct {
	ports map[treeID]map[string]struct{}
}

func topologyChangeTimerActive(p *portState, now time.Time) bool {
	return !p.tcWhile.IsZero() && p.tcWhile.After(now)
}

func newTopologyChangeEmissions() *topologyChangeEmissions {
	return &topologyChangeEmissions{ports: make(map[treeID]map[string]struct{})}
}

func (c *topologyChangeEmissions) add(l *Layer, t *tree, port string) {
	if c == nil {
		return
	}

	id := t.id
	if l.pvst == nil {
		id = cistID
	}
	ports := c.ports[id]
	if ports == nil {
		ports = make(map[string]struct{})
		c.ports[id] = ports
	}
	ports[port] = struct{}{}
}

func (l *Layer) emitTopologyChangeEmissions(now time.Time, changes *topologyChangeEmissions, emissions *[]layer.Emission) {
	if changes == nil {
		return
	}

	for _, id := range l.treeOrder {
		ports := changes.ports[id]
		if len(ports) == 0 {
			continue
		}
		t := l.trees[id]
		for _, name := range l.portNames {
			if _, ok := ports[name]; !ok {
				continue
			}
			p := t.ports[name]
			if p == nil {
				continue
			}
			alreadyEmitted := false
			for _, emission := range *emissions {
				if l.emissionCoversTopologyChange(t, p, now, emission) {
					alreadyEmitted = true
					break
				}
			}
			if alreadyEmitted {
				continue
			}
			l.emitRootTC(t, p, now, emissions)
		}
	}
}

func (l *Layer) emissionCoversTopologyChange(t *tree, p *portState, now time.Time, emission layer.Emission) bool {
	if emission.Port != p.name {
		return false
	}
	if l.pvst == nil && emission.VID != 0 || l.pvst != nil && emission.VID != t.vid {
		return false
	}

	var b bpdu.BPDU
	var err error
	if l.pvst != nil {
		b, _, err = bpdu.DecodeSSTP(emission.Frame)
	} else {
		b, err = bpdu.Decode(emission.Frame)
	}
	if err != nil {
		return false
	}
	if b.Type == bpdu.TypeTopologyChangeNotification {
		return true
	}
	if b.TopologyChange() != topologyChangeTimerActive(p, now) {
		return false
	}
	if l.mst == nil || t.id != cistID {
		return true
	}

	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		mp := l.trees[id].ports[p.name]
		want := topologyChangeTimerActive(mp, now)
		got := false
		for _, record := range b.MSTIs {
			if treeID(record.MSTID) == id {
				got = (bpdu.BPDU{Flags: record.Flags}).TopologyChange()
				break
			}
		}
		if got != want {
			return false
		}
	}

	return true
}

// initiateTopologyChange marks p active, increments the tree's topology change
// count, starts p's own tcWhile timer, records a root-port emission when needed,
// and propagates to other active ports on tree t.
func (l *Layer) initiateTopologyChange(t *tree, p *portState, now time.Time, flushes *[]layer.FlushTarget, changes *topologyChangeEmissions) {
	p.tcActive = true
	t.topologyChangeCount++
	t.lastTopologyChange = now
	p.tcWhile = now.Add(l.tcWhileDuration(t, p))
	if p.role == bpdu.RoleRoot && l.links[p.name].up {
		changes.add(l, t, p.name)
	}
	l.propagateTopologyChange(t, p.name, now, flushes, changes)
}

// propagateTopologyChange starts tcWhile on every active non-edge port on tree t
// other than originPort, flushes that port's learned entries, and records an
// emission obligation when the port is the root port.
func (l *Layer) propagateTopologyChange(t *tree, originPort string, now time.Time, flushes *[]layer.FlushTarget, changes *topologyChangeEmissions) {
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
		if p.role == bpdu.RoleRoot && link.up {
			changes.add(l, t, p.name)
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
