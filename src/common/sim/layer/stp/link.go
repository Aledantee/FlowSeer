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
	l.settleHelloTimers(now)
	link, ok := l.links[port]
	if !ok || !link.up {
		return layer.Effects{}
	}

	link.sendRSTP = true
	link.mdelayWhile = now.Add(migrateTime)
	if p := l.cist().ports[port]; p != nil && p.proposing && p.state == StateDiscarding &&
		p.cfg.AutoEdge && !link.edge && link.edgeDelayWhile.Before(now) {
		link.edgeDelayWhile = now
	}

	var flushes []layer.FlushTarget
	var emissions []layer.Emission
	func() {
		l.recomputeAll(now, &flushes)

		t := l.cist()
		p := t.ports[port]
		if p != nil && p.role == bpdu.RoleDesignated && link.up {
			l.requestNewInfo(t, p)
		}
	}()
	l.transmit(now, &emissions)

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// LinkChange records a physical or administrative link transition on a port.
// A port coming up transmits one transmission per transmit record in the returned
// emissions. A link down clears received information and moves the port to
// Disabled.
func (l *Layer) LinkChange(now time.Time, port string, up, pointToPoint bool, speedBPS uint64) layer.Effects {
	l.settleHelloTimers(now)
	t := l.cist()
	p, ok := t.ports[port]
	if !ok {
		return layer.Effects{}
	}
	link := l.links[port]

	var flushes []layer.FlushTarget
	var emissions []layer.Emission

	l.armHelloTimers(now)

	func() {
		if !up {
			if !link.up {
				return
			}
			link.up = false
			link.bpduGuardDisabled = false
			link.pvstBoundary = false
			link.edgeDelayWhile = time.Time{}
			link.mdelayWhile = time.Time{}
			l.resetTransmit(port)

			for _, id := range l.treeOrder {
				tp := l.trees[id].ports[port]
				if tp == nil {
					continue
				}
				tp.role = bpdu.RoleDisabled
				tp.state = StateDiscarding
				tp.rcvInfoValid = false
				tp.agreed = false
				tp.tcAck = false
				tp.proposing = false
				tp.fwdDelayTimer = time.Time{}
				tp.pvidInconsistent = false
				tp.loopInconsistent = false
			}

			// Entries learned on the dead port are stale whatever tree they
			// belong to.
			flushes = append(flushes, layer.FlushTarget{Port: port})
			for _, id := range l.treeOrder {
				if tp, ok := l.trees[id].ports[port]; ok {
					l.deactivatePort(l.trees[id], tp, &flushes)
				}
			}

			l.recomputeAll(now, &flushes)

			return
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

		// A port already up with unchanged point-to-point status does not restart its
		// handshake. A speed change updates pathCost on every unfixed tree and recomputes.
		if link.up && link.pointToPoint == p2p {
			if link.linkPathCost != linkCost {
				link.linkPathCost = linkCost
				for _, id := range l.treeOrder {
					tp := l.trees[id].ports[port]
					if tp != nil && !tp.pathCostFixed {
						tp.pathCost = linkCost
					}
				}
				l.recomputeAll(now, &flushes)
			}

			return
		}

		if link.up {
			for _, id := range l.treeOrder {
				tx := l.tx(l.trees[id], port)
				tx.newInfo = true
				tx.newInfoMsti = true
			}
		}
		link.linkPathCost = linkCost
		link.up = true
		link.pointToPoint = p2p
		link.sendRSTP = true
		link.mdelayWhile = now.Add(migrateTime)
		link.edge = link.adminEdge
		link.edgeDelayWhile = time.Time{}

		for _, id := range l.treeOrder {
			tp := l.trees[id].ports[port]
			if tp == nil {
				continue
			}
			l.deactivatePort(l.trees[id], tp, &flushes)
			tp.fwdDelayTimer = time.Time{}
			if !tp.pathCostFixed {
				tp.pathCost = linkCost
			}
			tp.role = bpdu.RoleDesignated
			tp.agreed = false
			tp.proposing = link.pointToPoint && !link.edge && link.sendRSTP
			if id == cistID && tp.proposing {
				link.edgeDelayWhile = now.Add(l.edgeDelay(t, link))
			}
			if link.edge {
				tp.state = StateForwarding
				tp.forwardTransitions++
			} else {
				tp.state = StateDiscarding
				if !link.pointToPoint {
					_, _, fwdDelay := l.times(l.trees[id])
					tp.fwdDelayTimer = now.Add(fwdDelay)
				}
			}
		}

		l.armHelloTimers(now)
		l.recomputeAll(now, &flushes)
	}()
	l.transmit(now, &emissions)

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// receiveLink runs the half of a receive that belongs to the link rather than
// to any one tree: BPDU guard, the protocol migration between RSTP and legacy
// STP, and the loss of auto-edge status. It runs once per received frame
// whatever tree the frame belongs to. Loop-guard marks stay with the receive
// entry point because each entry point has a different tree to recover.
// done reports that the frame must not reach a tree at all, either because
// the guard just fired or because it had already disabled the port.
func (l *Layer) receiveLink(now time.Time, port string, b bpdu.BPDU, flushes *[]layer.FlushTarget) (done bool) {
	t := l.cist()
	p := t.ports[port]
	link := l.links[port]

	// BPDU guard exists to keep an unexpected bridge on an access port out of
	// the topology, so the frame that proves one is there disables the port
	// before anything reads the BPDU. Only a link down and up brings it back.
	if p.cfg.BPDUGuard && !link.bpduGuardDisabled {
		link.bpduGuardDisabled = true
		l.resetTransmit(port)
		for _, id := range l.treeOrder {
			tr := l.trees[id]
			if tp, ok := tr.ports[port]; ok {
				tp.rcvInfoValid = false
				tp.agreed = false
				tp.tcAck = false
				tp.proposing = false
				tp.fwdDelayTimer = time.Time{}
			}
		}

		// Entries learned on the disabled port are stale whatever tree they
		// belong to.
		*flushes = append(*flushes, layer.FlushTarget{Port: port})

		l.recomputeAll(now, flushes)

		return true
	}
	if link.bpduGuardDisabled {
		return true
	}

	if (b.Type == bpdu.TypeConfiguration || b.Type == bpdu.TypeTopologyChangeNotification) && link.sendRSTP && !link.mdelayWhile.After(now) {
		link.sendRSTP = false
		link.mdelayWhile = now.Add(migrateTime)
	} else if b.Type == bpdu.TypeRapid && !link.sendRSTP && !link.mdelayWhile.After(now) {
		link.sendRSTP = true
		link.mdelayWhile = now.Add(migrateTime)
	}

	wasAutoEdge := link.edge && !link.adminEdge
	link.edge = link.adminEdge
	link.edgeDelayWhile = now.Add(l.edgeDelay(t, link))

	if wasAutoEdge {
		for _, id := range l.treeOrder {
			tr := l.trees[id]
			tp, ok := tr.ports[port]
			if !ok {
				continue
			}
			l.deactivatePort(tr, tp, flushes)
			tp.state = StateDiscarding
			tp.agreed = false
			tp.fwdDelayTimer = time.Time{}
			tp.proposing = link.pointToPoint && link.sendRSTP
		}
	}

	return false
}
