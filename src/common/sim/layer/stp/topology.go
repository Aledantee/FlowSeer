package stp

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// Topology change follows the Topology Change state machine of IEEE Std
// 802.1Q-2003 (13.35, Figure 13-19) for each port of each tree. A port is
// active when it is a Root or Designated port, is not an edge port, and has
// started forwarding. Only an active port detects a change, acts on a received
// one, and keeps a timer. The sources differ from the published text in the
// timer, which P802.1aq/D1.5 13.29.11 sets to HelloTime plus one second on a
// port that sends RSTP.

// tcRunning reports whether the port's topology change timer runs at now.
func (p *portState) tcRunning(now time.Time) bool {
	return p.tcWhile.After(now)
}

// startTc starts the port's timer for tree t unless it already runs, the way
// newTcWhile does (IEEE Std 802.1Q-2003 13.26.6, P802.1aq/D1.5 13.29.11). A
// port that sends RSTP counts HelloTime plus one second. One that does not
// counts Max Age plus Forward Delay of the root's times. A Root port whose
// timer starts owes its peer a BPDU carrying the change, and sends it before
// the call returns.
func (l *Layer) startTc(t *tree, p *portState, now time.Time) {
	if p.tcRunning(now) {
		return
	}

	if l.link(p.name).sendRSTP {
		p.tcWhile = now.Add(l.helloTime + time.Second)
	} else {
		maxAge, _, forwardDelay := l.times(t)
		p.tcWhile = now.Add(maxAge + forwardDelay)
	}

	if p.role == bpdu.RoleRoot {
		l.oweTopologyChange(t, p.name)
	}
}

// propagateTopologyChange starts the timer of every other active port of tree
// t and flushes it, which setTcPropTree and the PROPAGATING state do (13.26.20).
// A port outside the active topology ignores the change.
func (l *Layer) propagateTopologyChange(t *tree, origin string, now time.Time, flushes *[]layer.FlushTarget) {
	fids := l.treeVLANs[t.id]

	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if name == origin || !ok || !p.tcActive {
			continue
		}
		l.startTc(t, p, now)
		mergeFlushTarget(flushes, name, fids)
	}
}

// settleTopology moves the ports of tree t into and out of the active
// topology after a role or state change. A port that lost its role, became an
// edge, or went down is flushed and leaves with its timer and nothing else. A
// non-edge Root or Designated port that is forwarding and not yet active
// detects a topology change: it becomes active, starts its timer, and
// propagates to the other active ports.
func (l *Layer) settleTopology(t *tree, now time.Time, flushes *[]layer.FlushTarget) {
	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok || !p.tcActive {
			continue
		}
		if !activeRole(p.role) || l.link(name).edge {
			p.tcActive = false
			p.tcWhile = time.Time{}
			mergeFlushTarget(flushes, name, l.treeVLANs[t.id])
		}
	}

	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok || p.tcActive || !activeRole(p.role) || l.link(name).edge || p.state != StateForwarding {
			continue
		}
		p.tcActive = true
		t.topologyChangeCount++
		t.lastTopologyChange = now
		l.startTc(t, p, now)
		l.propagateTopologyChange(t, name, now, flushes)
	}
}

// activeRole reports whether a port with the role can be active.
func activeRole(r bpdu.Role) bool {
	return r == bpdu.RoleRoot || r == bpdu.RoleDesignated
}

// topologyNotice is what one received BPDU says about topology change.
type topologyNotice struct {
	// trees lists the trees a topology change reached, the CIST first.
	trees []treeID
	// tcn marks a notification, which also starts the CIST port's timer.
	tcn bool
	// ack marks an acknowledgment of the CIST port's own notification.
	ack bool
}

// receivedTopology says which trees the topology change facts of b reach, as
// IEEE Std 802.1Q-2003 13.26.19 sets rcvdTc: a flag from outside the region
// reaches the CIST and every MSTI, and one from inside the CIST if the CST
// message sets it and each MSTI whose record does, listed in flagged.
func (l *Layer) receivedTopology(t *tree, internal bool, b bpdu.BPDU, flagged []treeID) topologyNotice {
	n := topologyNotice{ack: t.id == cistID && b.TopologyChangeAck()}

	switch {
	case internal:
		if b.TopologyChange() {
			n.trees = append(n.trees, t.id)
		}
		n.trees = append(n.trees, flagged...)
	case b.TopologyChange() && l.mst != nil && t.id == cistID:
		n.trees = slices.Clone(l.treeOrder)
	case b.TopologyChange():
		n.trees = []treeID{t.id}
	}

	return n
}

// applyTopologyChange acts on a notice received on the named port. A port that
// is not active in a tree ignores it for that tree (Figure 13-19 INACTIVE).
// On an active port a change propagates to the tree's other active ports,
// and a notification also starts the CIST port's timer and, on a Designated
// port, owes the peer an acknowledgment. A port that restricts topology
// change propagates nothing and starts no timer, and still acknowledges.
func (l *Layer) applyTopologyChange(now time.Time, port string, n topologyNotice, flushes *[]layer.FlushTarget) {
	if cp := l.cist().ports[port]; n.ack && cp.tcActive {
		cp.tcWhile = time.Time{}
	}

	lk := l.link(port)
	for _, id := range n.trees {
		t := l.trees[id]
		p, ok := t.ports[port]
		if !ok || !p.tcActive {
			continue
		}

		notified := n.tcn && id == cistID
		if notified && p.role == bpdu.RoleDesignated {
			lk.tcAck = true
		}
		if p.cfg.RestrictedTCN {
			continue
		}
		if notified {
			l.startTc(t, p, now)
		}
		l.propagateTopologyChange(t, port, now, flushes)
	}
}

// oweTopologyChange marks that the port must send a BPDU carrying a change
// before the call returns. A port facing a bridge that does not send RSTP
// owes a notification for the CIST alone.
func (l *Layer) oweTopologyChange(t *tree, name string) {
	if l.link(name).sendRSTP || t.id == cistID {
		l.tx(t, name).topologyOwed = true
	}
}

// drainTopology sends what oweTopologyChange marked. Outside PVST mode only
// the CIST's BPDU leaves a port, and carries every tree's flag in its MSTI
// records.
func (l *Layer) drainTopology(now time.Time, emissions *[]layer.Emission) {
	for _, id := range l.treeOrder {
		t := l.trees[id]
		for _, name := range l.portNames {
			tx := l.tx(t, name)
			if tx == nil || !tx.topologyOwed {
				continue
			}
			tx.topologyOwed = false

			et := t
			if l.pvst == nil {
				et = l.cist()
			}
			if ep, ok := et.ports[name]; ok && l.link(name).up {
				l.emit(et, ep, now, emissionTopology, emissions)
			}
		}
	}
}

// rootReports reports whether the Root port of the named port's trees has a
// topology change to report: a timer runs on it. A BPDU on emitting tree et
// covers the trees it carries, which is the CIST and every MSTI outside PVST
// mode, and the CIST alone toward a bridge that does not send RSTP.
func (l *Layer) rootReports(et *tree, name string, now time.Time) bool {
	running := func(t *tree) bool {
		p, ok := t.ports[name]

		return ok && p.role == bpdu.RoleRoot && p.tcRunning(now)
	}

	switch {
	case l.pvst != nil:
		return running(et)
	case !l.link(name).sendRSTP:
		return running(l.cist())
	}

	for _, id := range l.treeOrder {
		if running(l.trees[id]) {
			return true
		}
	}

	return false
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
