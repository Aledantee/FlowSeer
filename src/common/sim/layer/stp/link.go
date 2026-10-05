package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// Mcheck triggers protocol migration checking on the named port, forcing it
// to transmit RSTP BPDUs and restarting the migration delay. If the port is
// unknown or down, Mcheck has no effect.
func (l *Layer) Mcheck(now time.Time, port string) layer.Effects {
	lk := l.link(port)
	if lk == nil || !lk.up {
		return layer.Effects{}
	}

	t := l.cist()
	p := t.ports[port]

	lk.sendRSTP = true
	lk.mdelayWhile = now.Add(migrateTime)

	var flushes []layer.FlushTarget

	emissions := l.recomputeAll(now, &flushes)

	if p.role == bpdu.RoleDesignated {
		l.proposeIfSilent(t, p, now, &emissions)
	}

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// proposeIfSilent emits the CIST's designated BPDU on p unless the
// emissions of the call in progress already carry one for this tree, or one
// is already held back by the transmit budget. Under PVST every tree can emit
// on a port; only a match on this tree's own VID is evidence that the call's
// own recompute already sent the CIST's proposal, not some other VLAN's.
func (l *Layer) proposeIfSilent(t *tree, p *portState, now time.Time, emissions *[]layer.Emission) {
	for _, e := range *emissions {
		if e.Port == p.name && e.VID == t.vid {
			return
		}
	}
	if !l.tx(t, p.name).pendingDesignated {
		l.emit(t, p, now, emissionDesignated, emissions)
	}
}

// applyLinkCost gives every tree that did not fix a cost of its own the cost
// the link record derives.
func (l *Layer) applyLinkCost(name string, lk *linkRecord) {
	for _, id := range l.treeOrder {
		if tp, ok := l.trees[id].ports[name]; ok && !tp.pathCostFixed {
			tp.pathCost = lk.cost
		}
	}
}

// dropHandshake clears, on every tree, the handshake state, the forward-delay
// timer, and the received information the named port holds, and drops the
// transmissions it was holding. With disable it also sends each tree's port to
// Disabled and Discarding and clears the guard marks a tree holds, which is
// what a dead link leaves behind.
func (l *Layer) dropHandshake(name string, disable bool) {
	for _, id := range l.treeOrder {
		tp, ok := l.trees[id].ports[name]
		if !ok {
			continue
		}
		tp.rcvInfoValid = false
		tp.agreed = false
		tp.proposing = false
		tp.fwdDelayTimer = time.Time{}
		if disable {
			tp.role = bpdu.RoleDisabled
			tp.state = StateDiscarding
			tp.pvidInconsistent = false
			tp.loopInconsistent = false
		}
	}

	l.clearPending(name)
}

// armHelloTimers starts the periodic hello on every tree that drives its own
// emission: the CIST alone outside PVST mode, since an MSTI's information
// rides the CIST's BPDU, and every VLAN's tree inside it. A tree whose hello
// timer stays zero never reaches Advance's hello loop, which is what keeps that
// loop's walk over every tree behavior-neutral for RSTP and MSTP.
func (l *Layer) armHelloTimers(now time.Time) {
	for _, id := range l.treeOrder {
		if l.pvst == nil && id != cistID {
			continue
		}
		if t := l.trees[id]; t.helloTimer.IsZero() {
			t.helloTimer = now.Add(l.helloTime)
		}
	}
}

// clearPending drops every tree's held transmission on the named port. A port
// whose link just went down, or that BPDU guard just disabled, must not
// release a BPDU it was holding when the budget next frees up.
func (l *Layer) clearPending(name string) {
	for _, id := range l.treeOrder {
		tx := l.tx(l.trees[id], name)
		tx.pendingDesignated = false
		tx.pendingAgreement = false
		tx.pendingTopology = false
		tx.topologyOwed = false
	}
}

// LinkChange records a physical or administrative link transition on a port.
// A port coming up point-to-point transmits a proposal immediately in the
// returned emissions. A link down clears received information and moves the
// port to Disabled. A report that only changes the link-derived cost of a
// port that is already up moves the cost and leaves every handshake alone.
func (l *Layer) LinkChange(now time.Time, port string, up, pointToPoint bool, speedBPS uint64) layer.Effects {
	lk := l.link(port)
	if lk == nil {
		return layer.Effects{}
	}

	l.armHelloTimers(now)

	p := l.cist().ports[port]
	if !up {
		return l.linkDown(now, lk, p)
	}

	p2p := pointToPoint
	switch p.cfg.PointToPoint {
	case PointToPointForceTrue:
		p2p = true
	case PointToPointForceFalse:
		p2p = false
	}
	linkCost := p.cfg.PathCost
	if linkCost == 0 {
		linkCost = defaultPathCost(speedBPS)
	}

	// A report of the state the port already has is not a transition: two
	// callers may describe the same link, and re-entering a port that is up
	// would restart its handshake for nothing. A speed change alone is not
	// one either.
	if lk.up && lk.pointToPoint == p2p {
		return l.updateLinkCost(now, lk, p.name, linkCost)
	}

	return l.linkUp(now, lk, p, p2p, linkCost)
}

// linkDown takes the named port's link down on every tree. The entries learned
// on the dead port are the ones certainly stale whatever tree they belong to,
// so the port is flushed for every FID. Going down is not a topology change:
// no other port is flushed for it.
func (l *Layer) linkDown(now time.Time, lk *linkRecord, p *portState) layer.Effects {
	if !lk.up {
		return layer.Effects{}
	}

	lk.up = false

	// Both guard states clear here, which is what makes a link down and up
	// the recovery for BPDU guard. A port that comes back up holds no
	// expired information, so loop guard has nothing to trigger on either.
	// The PVST boundary mark clears for a different reason: it names the
	// protocol the neighbor speaks, and only a link transition can put a
	// different neighbor there.
	lk.bpduGuardDisabled = false
	lk.pvstBoundary = false
	l.dropHandshake(p.name, true)

	// A port that goes down leaves the active topology in the recompute below
	// and raises no topology change of its own.
	flushes := []layer.FlushTarget{{Port: p.name}}

	return layer.Effects{
		Emissions: l.recomputeAll(now, &flushes),
		Flush:     flushes,
	}
}

// updateLinkCost records a new link-derived cost for a port that stays up
// and recomputes. It resets no role, agreement, migration, edge, or
// forward-delay state, which the full handshake in linkUp would.
func (l *Layer) updateLinkCost(now time.Time, lk *linkRecord, name string, cost uint32) layer.Effects {
	if lk.cost == cost {
		return layer.Effects{}
	}

	lk.cost = cost
	l.applyLinkCost(name, lk)

	var flushes []layer.FlushTarget

	return layer.Effects{
		Emissions: l.recomputeAll(now, &flushes),
		Flush:     flushes,
	}
}

// linkUp brings the named port's link up, or re-enters it after a change of
// point-to-point status, and starts the CIST's handshake: the other trees
// elect their roles from the recompute that follows.
func (l *Layer) linkUp(now time.Time, lk *linkRecord, p *portState, p2p bool, linkCost uint32) layer.Effects {
	t := l.cist()

	lk.cost = linkCost
	lk.up = true
	lk.pointToPoint = p2p
	l.applyLinkCost(p.name, lk)

	p.role = bpdu.RoleDesignated
	p.agreed = false
	lk.sendRSTP = true
	lk.mdelayWhile = now.Add(migrateTime)

	lk.edge = lk.adminEdge
	lk.edgeDelayWhile = now.Add(l.edgeDelay(t, lk))

	p.proposing = lk.pointToPoint && !lk.edge && lk.sendRSTP

	if lk.edge {
		p.state = StateForwarding
		p.forwardTransitions++
	} else {
		p.state = StateDiscarding
		if !lk.pointToPoint {
			p.fwdDelayTimer = now.Add(l.forwardDelayOf(t))
		}
	}

	var flushes []layer.FlushTarget

	emissions := l.recomputeAll(now, &flushes)

	if lk.pointToPoint && !lk.edge && p.role == bpdu.RoleDesignated && p.state == StateDiscarding {
		l.proposeIfSilent(t, p, now, &emissions)
	}

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// receiveLink runs the half of a receive that belongs to the link rather than
// to any one tree: BPDU guard, the protocol migration between RSTP and legacy
// STP, and the loss of auto-edge status. It runs once per received frame
// whatever tree the frame belongs to. p is the CIST's port state. The
// loop-guard clear is not here, since under PVST it belongs to the tree the
// frame is applied to. done reports that the frame must not reach a tree at
// all, either because the guard just fired or because it had already disabled
// the port.
func (l *Layer) receiveLink(now time.Time, p *portState, b bpdu.BPDU, flushes *[]layer.FlushTarget) (emissions []layer.Emission, done bool) {
	lk := l.link(p.name)

	// BPDU guard exists to keep an unexpected bridge on an access port out of
	// the topology, so the frame that proves one is there disables the port
	// before anything reads the BPDU. Only a link down and up brings it back.
	if p.cfg.BPDUGuard && !lk.bpduGuardDisabled {
		lk.bpduGuardDisabled = true
		l.dropHandshake(p.name, false)

		// The entries learned on the port are the ones certainly stale,
		// whatever tree they belong to. Disabling the port raises no topology
		// change: recompute takes it out of the active topology.
		*flushes = append(*flushes, layer.FlushTarget{Port: p.name})

		return l.recomputeAll(now, flushes), true
	}
	if lk.bpduGuardDisabled {
		return nil, true
	}

	if (b.Type == bpdu.TypeConfiguration || b.Type == bpdu.TypeTopologyChangeNotification) && lk.sendRSTP && !lk.mdelayWhile.After(now) {
		lk.sendRSTP = false
		lk.mdelayWhile = now.Add(migrateTime)
	} else if b.Type == bpdu.TypeRapid && !lk.sendRSTP && !lk.mdelayWhile.After(now) {
		lk.sendRSTP = true
		lk.mdelayWhile = now.Add(migrateTime)
	}

	wasAutoEdge := lk.edge && !lk.adminEdge
	lk.edge = lk.adminEdge
	lk.edgeDelayWhile = now.Add(l.edgeDelay(l.cist(), lk))

	if wasAutoEdge {
		l.loseAutoEdge(lk, p.name)
	}

	return nil, false
}

// loseAutoEdge returns the named port to Discarding and proposing on every
// tree. A Designated port that already forwards keeps its state through
// recompute, so each tree must be sent back here or only the CIST would
// stop forwarding. Each tree also drops its agreement: a port that keeps one
// does not propose, and recompute would open it again. The port was an edge,
// so it was in no tree's active topology and leaving Forwarding raises no
// topology change.
func (l *Layer) loseAutoEdge(lk *linkRecord, name string) {
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		tp, ok := mt.ports[name]
		if !ok {
			continue
		}
		tp.state = StateDiscarding
		tp.fwdDelayTimer = time.Time{}
		tp.agreed = false
		tp.proposing = lk.pointToPoint && lk.sendRSTP
	}
}
