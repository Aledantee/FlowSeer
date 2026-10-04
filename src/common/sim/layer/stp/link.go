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
	link, ok := l.links[port]
	if !ok || !link.up {
		return layer.Effects{}
	}

	link.sendRSTP = true
	link.mdelayWhile = now.Add(migrateTime)

	var flushes []layer.FlushTarget
	changes := newTopologyChangeEmissions()

	var emissions []layer.Emission
	func() {
		emissions = l.recomputeAll(now, &flushes, changes)

		t := l.cist()
		p := t.ports[port]
		if p != nil && p.role == bpdu.RoleDesignated && link.up {
			alreadyEmitted := false
			for _, e := range emissions {
				// Under PVST every tree can emit on this port; only a match on
				// this tree's own VID is evidence that this call's own recompute
				// already sent the CIST's proposal, not some other VLAN's.
				if e.Port == p.name && e.VID == t.vid {
					alreadyEmitted = true
					break
				}
			}
			if !alreadyEmitted && !l.tx(t, p.name).pendingDesignated {
				l.emit(t, p, now, emissionDesignated, &emissions, changes)
			}
		}
	}()
	l.emitTopologyChangeEmissions(now, changes, &emissions)

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// LinkChange records a physical or administrative link transition on a port.
// A port coming up point-to-point transmits a proposal immediately in the
// returned emissions. A link down clears received information and moves the
// port to Disabled.
func (l *Layer) LinkChange(now time.Time, port string, up, pointToPoint bool, speedBPS uint64) layer.Effects {
	t := l.cist()
	p, ok := t.ports[port]
	if !ok {
		return layer.Effects{}
	}
	link := l.links[port]

	var flushes []layer.FlushTarget
	var emissions []layer.Emission
	changes := newTopologyChangeEmissions()

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
			l.clearPending(port)

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

			// The entries learned on the dead port are the ones certainly stale
			// whatever tree they belong to; the topology change below flushes
			// every other port by its own tree's VLANs.
			flushes = append(flushes, layer.FlushTarget{Port: port})
			for _, id := range l.treeOrder {
				if tp, ok := l.trees[id].ports[port]; ok {
					l.deactivatePort(l.trees[id], tp, &flushes)
				}
			}

			emissions = append(emissions, l.recomputeAll(now, &flushes, changes)...)

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
		// handshake; a speed change updates pathCost on every unfixed tree and recomputes.
		if link.up && link.pointToPoint == p2p {
			if link.linkPathCost != linkCost {
				link.linkPathCost = linkCost
				for _, id := range l.treeOrder {
					tp := l.trees[id].ports[port]
					if tp != nil && !tp.pathCostFixed {
						tp.pathCost = linkCost
					}
				}
				emissions = append(emissions, l.recomputeAll(now, &flushes, changes)...)
			}

			return
		}

		link.linkPathCost = linkCost
		link.up = true
		link.pointToPoint = p2p
		link.sendRSTP = true
		link.mdelayWhile = now.Add(migrateTime)
		link.edge = link.adminEdge
		link.edgeDelayWhile = now.Add(l.edgeDelay(t, link))

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

		emissions = append(emissions, l.recomputeAll(now, &flushes, changes)...)

		if link.pointToPoint && !link.edge && p.role == bpdu.RoleDesignated && p.state == StateDiscarding {
			alreadyEmitted := false
			for _, e := range emissions {
				// Under PVST every tree can emit on this port; only a match on
				// this tree's own VID is evidence that this call's own recompute
				// already sent the CIST's proposal, not some other VLAN's.
				if e.Port == p.name && e.VID == t.vid {
					alreadyEmitted = true

					break
				}
			}
			if !alreadyEmitted && !l.tx(t, p.name).pendingDesignated {
				l.emit(t, p, now, emissionDesignated, &emissions, changes)
			}
		}
	}()
	l.emitTopologyChangeEmissions(now, changes, &emissions)

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// receiveLink runs the half of a receive that belongs to the link rather than
// to any one tree: BPDU guard, the loop-guard clear every BPDU earns, the
// protocol migration between RSTP and legacy STP, and the loss of auto-edge
// status. It runs once per received frame whatever tree the frame belongs to.
// done reports that the frame must not reach a tree at all, either because
// the guard just fired or because it had already disabled the port.
func (l *Layer) receiveLink(now time.Time, port string, b bpdu.BPDU, flushes *[]layer.FlushTarget, changes *topologyChangeEmissions) (emissions []layer.Emission, done bool) {
	t := l.cist()
	p := t.ports[port]
	link := l.links[port]

	// Any BPDU on the port is evidence the link carries traffic both ways,
	// which is the condition loop guard was waiting to see restored. This
	// runs before the BPDU guard checks below: a frame that trips or is held
	// by BPDU guard is still such evidence, and guard and loop guard clear on
	// independent events.
	for _, id := range l.treeOrder {
		tr := l.trees[id]
		if tp, ok := tr.ports[port]; ok {
			tp.loopInconsistent = false
		}
	}

	// BPDU guard exists to keep an unexpected bridge on an access port out of
	// the topology, so the frame that proves one is there disables the port
	// before anything reads the BPDU. Only a link down and up brings it back.
	if p.cfg.BPDUGuard && !link.bpduGuardDisabled {
		link.bpduGuardDisabled = true
		l.clearPending(port)
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

		// The entries learned on the port are the ones certainly stale,
		// whatever tree they belong to. The topology change itself is left
		// to recompute, which raises it from the same transition with the
		// same origin and timestamp; raising it here as well would count one
		// event twice.
		*flushes = append(*flushes, layer.FlushTarget{Port: port})

		return l.recomputeAll(now, flushes, changes), true
	}
	if link.bpduGuardDisabled {
		return nil, true
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
			tp.fwdDelayTimer = time.Time{}
			tp.proposing = link.pointToPoint && link.sendRSTP
		}
	}

	return nil, false
}
