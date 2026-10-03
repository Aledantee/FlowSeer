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
	t := l.cist()
	p, ok := t.ports[port]
	if !ok || !p.up {
		return layer.Effects{}
	}

	p.sendRSTP = true
	p.mdelayWhile = now.Add(migrateTime)

	// sendRSTP is link-replicated: every other tree's emit gate reads its own
	// copy, and only a sync carries this migration check onto it. Without
	// this, a non-CIST tree that receiveLink had already forced to version 0
	// would stay silenced after an operator forces the CIST back to RSTP.
	l.syncInstancePorts(port, p)

	var flushes []layer.FlushTarget

	emissions := l.recomputeAll(now, &flushes)

	if p.role == bpdu.RoleDesignated && p.up {
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
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// syncInstancePorts carries a physical link property change on the CIST's
// port cistP onto every other tree's port state of the same name: the whole
// link-replicated linkState, assigned in one statement so a field added to
// it later propagates without a copy line of its own, plus the link-derived
// path cost for an instance that left its own unconfigured. These are link
// properties, not per-instance ones, so every tree tracks its own copy to
// give recompute one shape of portState to read regardless of tree;
// instanceRemainingHops and the boundary role rule are what actually let
// instances diverge.
func (l *Layer) syncInstancePorts(name string, cistP *portState) {
	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		mp, ok := l.trees[id].ports[name]
		if !ok {
			continue
		}

		mp.linkState = cistP.linkState
		if !mp.pathCostFixed {
			mp.pathCost = cistP.linkPathCost
		}
		if !cistP.up {
			mp.role = bpdu.RoleDisabled
			mp.state = StateDiscarding
			mp.rcvInfoValid = false
			mp.pvidInconsistent = false
		}
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

	var flushes []layer.FlushTarget
	var emissions []layer.Emission

	l.armHelloTimers(now)

	if !up {
		if !p.up {
			return layer.Effects{}
		}
		oldState := p.state
		p.up = false
		p.role = bpdu.RoleDisabled
		p.state = StateDiscarding
		p.rcvInfoValid = false
		p.agreed = false
		p.proposing = false
		l.clearPending(p.name)
		p.fwdDelayTimer = time.Time{}
		// Both guard states clear here, which is what makes a link down and up
		// the recovery for BPDU guard. A port that comes back up holds no
		// expired information, so loop guard has nothing to trigger on either.
		// The PVST boundary mark clears for a different reason: it names the
		// protocol the neighbor speaks, and only a link transition can put a
		// different neighbor there.
		p.bpduGuardDisabled = false
		p.loopInconsistent = false
		p.pvidInconsistent = false
		p.pvstBoundary = false

		// The entries learned on the dead port are the ones certainly stale
		// whatever tree they belong to; the topology change below flushes
		// every other port by its own tree's VLANs.
		flushes = append(flushes, layer.FlushTarget{Port: p.name})
		if oldState == StateForwarding && !p.edge {
			l.raiseTopologyChange(t, p.name, now, &flushes)
		}

		l.syncInstancePorts(port, p)

		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{
			Emissions: emissions,
			Flush:     flushes,
		}
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
	// A cost the tree configured for itself stays put across a link change,
	// the way syncInstancePorts already leaves a fixed instance cost alone.
	// Only PVST sets this on the CIST's own port state, by configuring VLAN
	// 1's path cost on the tree that occupies the CIST slot.
	cost := linkCost
	if p.pathCostFixed {
		cost = p.pathCost
	}
	// A report of the state the port already has is not a transition: two
	// callers may describe the same link, and re-entering a port that is up
	// would restart its handshake for nothing. A speed change alone is not
	// that either, but it must still reach every tree's own path cost: the
	// link-derived cost is written and synced without disturbing the role,
	// agreement, migration, edge, or forward-delay state the full handshake
	// below would reset.
	if p.up && p.pointToPoint == p2p && p.pathCost == cost {
		if p.linkPathCost != linkCost {
			p.linkPathCost = linkCost
			l.syncInstancePorts(port, p)
			emissions = append(emissions, l.recomputeAll(now, &flushes)...)
		}

		return layer.Effects{
			Emissions: emissions,
			Flush:     flushes,
		}
	}
	p.linkPathCost = linkCost

	p.up = true
	p.pointToPoint = p2p
	p.pathCost = cost

	p.role = bpdu.RoleDesignated
	p.agreed = false
	p.sendRSTP = true
	p.mdelayWhile = now.Add(migrateTime)

	p.edge = p.adminEdge
	p.edgeDelayWhile = now.Add(l.edgeDelay(t, p))

	p.proposing = p.pointToPoint && !p.edge && p.sendRSTP

	if p.edge {
		p.state = StateForwarding
		p.forwardTransitions++
	} else {
		p.state = StateDiscarding
		if !p.pointToPoint {
			p.fwdDelayTimer = now.Add(l.forwardDelay)
		}
	}

	l.syncInstancePorts(port, p)

	emissions = append(emissions, l.recomputeAll(now, &flushes)...)

	if p.pointToPoint && !p.edge && p.role == bpdu.RoleDesignated && p.state == StateDiscarding {
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
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// receiveLink runs the half of a receive that belongs to the link rather than
// to any one tree: BPDU guard, the loop-guard clear every BPDU earns, the
// protocol migration between RSTP and legacy STP, and the loss of auto-edge
// status. It runs once per received frame whatever tree the frame belongs to,
// against the CIST's port state, which is where those link properties live.
// done reports that the frame must not reach a tree at all, either because
// the guard just fired or because it had already disabled the port.
func (l *Layer) receiveLink(now time.Time, p *portState, b bpdu.BPDU, flushes *[]layer.FlushTarget) (emissions []layer.Emission, done bool) {
	t := l.cist()

	// Any BPDU on the port is evidence the link carries traffic both ways,
	// which is the condition loop guard was waiting to see restored. This
	// runs before the BPDU guard checks below: a frame that trips or is held
	// by BPDU guard is still such evidence, and guard and loop guard clear on
	// independent events.
	p.loopInconsistent = false

	// BPDU guard exists to keep an unexpected bridge on an access port out of
	// the topology, so the frame that proves one is there disables the port
	// before anything reads the BPDU. Only a link down and up brings it back.
	if p.cfg.BPDUGuard && !p.bpduGuardDisabled {
		p.bpduGuardDisabled = true
		p.rcvInfoValid = false
		p.agreed = false
		p.proposing = false
		l.clearPending(p.name)
		p.fwdDelayTimer = time.Time{}

		// The entries learned on the port are the ones certainly stale,
		// whatever tree they belong to. The topology change itself is left
		// to recompute, which raises it from the same transition with the
		// same origin and timestamp; raising it here as well would count one
		// event twice.
		*flushes = append(*flushes, layer.FlushTarget{Port: p.name})

		return l.recomputeAll(now, flushes), true
	}
	if p.bpduGuardDisabled {
		return nil, true
	}

	if (b.Type == bpdu.TypeConfiguration || b.Type == bpdu.TypeTopologyChangeNotification) && p.sendRSTP && !p.mdelayWhile.After(now) {
		p.sendRSTP = false
		p.mdelayWhile = now.Add(migrateTime)
	} else if b.Type == bpdu.TypeRapid && !p.sendRSTP && !p.mdelayWhile.After(now) {
		p.sendRSTP = true
		p.mdelayWhile = now.Add(migrateTime)
	}

	wasAutoEdge := p.edge && !p.adminEdge
	p.edge = p.adminEdge
	p.edgeDelayWhile = now.Add(l.edgeDelay(t, p))

	if wasAutoEdge {
		if p.state == StateForwarding {
			l.raiseTopologyChange(t, p.name, now, flushes)
		}
		p.state = StateDiscarding
		p.fwdDelayTimer = time.Time{}
		p.proposing = p.pointToPoint && p.sendRSTP
	}

	// edge and sendRSTP are link-replicated: whatever this receive changed on
	// the CIST's copy — an auto-edge loss above, or a migration a few lines
	// up — must reach every other tree's own copy before the frame is judged,
	// or a property this function exists to hold link-wide is invisible to
	// every tree but the CIST's.
	l.syncInstancePorts(p.name, p)

	return nil, false
}
