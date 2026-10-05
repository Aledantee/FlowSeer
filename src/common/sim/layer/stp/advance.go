package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// NextWake returns the earliest scheduled time at which the layer needs to be
// woken, and reports whether any timer is currently active.
func (l *Layer) NextWake() (time.Time, bool) {
	var next time.Time
	hasTimer := false

	update := func(at time.Time) {
		if at.IsZero() {
			return
		}
		if !hasTimer || at.Before(next) {
			next = at
			hasTimer = true
		}
	}

	for _, t := range l.trees {
		update(t.helloTimer)
		update(t.topologyChangeTimer)

		for _, p := range t.ports {
			update(p.fwdDelayTimer)
			if p.rcvInfoValid {
				update(p.rcvTime.Add(3 * p.rcvHelloTime))
			}
			// The edge delay is due only on a port that can still become an
			// edge, or a wake would be scheduled that changes nothing. Only
			// the CIST's port decides it (see Advance).
			lk := l.link(p.name)
			if t.id == cistID && p.cfg.AutoEdge && !lk.edge && lk.up && lk.sendRSTP && p.role == bpdu.RoleDesignated &&
				p.state == StateDiscarding && lk.pointToPoint && p.proposing {
				update(lk.edgeDelayWhile)
			}
			tx := l.tx(t, p.name)
			if (tx.pendingAgreement || tx.pendingDesignated) && !tx.tick.IsZero() {
				update(tx.tick)
			}
		}
	}

	return next, hasTimer
}

// Advance advances timer-driven state to now, firing due hellos, forward delays,
// topology change timers, and information age-outs.
func (l *Layer) Advance(now time.Time) layer.Effects {
	t := l.cist()

	var flushes []layer.FlushTarget
	var emissions []layer.Emission

	autoEdgeFired := false
	for _, name := range l.portNames {
		p := t.ports[name]
		lk := l.link(name)
		if p.cfg.AutoEdge && lk.sendRSTP && lk.up && p.role == bpdu.RoleDesignated &&
			p.state == StateDiscarding && lk.pointToPoint && p.proposing &&
			!lk.edgeDelayWhile.IsZero() && !lk.edgeDelayWhile.After(now) {
			// edge is on the link record, so an MSTI port reaches Forwarding
			// at the same wake as the CIST's through the recompute below, or
			// it would raise a topology change of its own for a flush
			// auto-edge exists to prevent.
			lk.edge = true
			lk.edgeDelayWhile = time.Time{}
			p.state = StateForwarding
			p.fwdDelayTimer = time.Time{}
			p.proposing = false
			p.forwardTransitions++
			autoEdgeFired = true
		}
	}

	// Both emission loops walk every tree, the way the forward-delay, age-out
	// and topology-change loops below already do. Outside PVST mode the walk
	// is behavior-neutral: treeOrder holds the CIST first, the budget is
	// shared, and an MSTI's own hello timer never runs because only the CIST
	// emits. Inside it, a per-VLAN tree's periodic hello would otherwise
	// never fire and a transmission held on it would never be released.
	for _, id := range l.treeOrder {
		mt := l.trees[id]

		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok {
				continue
			}
			tx := l.tx(mt, name)
			if !l.link(name).up || tx.tick.IsZero() || tx.tick.After(now) {
				continue
			}
			// A held kind belongs to the role that requested it; released
			// under another role it would be an agreement from a designated
			// port or a designated claim from a blocked one.
			if tx.pendingAgreement {
				tx.pendingAgreement = false
				if p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate {
					l.emit(mt, p, now, emissionAgreement, &emissions)
				}
			}
			if tx.pendingDesignated {
				tx.pendingDesignated = false
				if p.role == bpdu.RoleDesignated {
					l.emit(mt, p, now, emissionDesignated, &emissions)
				}
			}
		}

		if mt.helloTimer.IsZero() || mt.helloTimer.After(now) {
			continue
		}
		mt.helloTimer = now.Add(l.helloTime)
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if ok && l.link(name).up && p.role == bpdu.RoleDesignated {
				l.emit(mt, p, now, emissionDesignated, &emissions)
			}
		}
	}

	// The forward delay ladder runs per tree, since role and state are per
	// tree: an MSTI's own internal ports climb it independently of the CIST's.
	// A boundary port never sets fwdDelayTimer for a non-CIST tree (recompute
	// mirrors its state from the CIST outright), so this never double-drives
	// one.
	stateChanged := false
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok || p.fwdDelayTimer.IsZero() || p.fwdDelayTimer.After(now) {
				continue
			}
			p.fwdDelayTimer = time.Time{}
			switch p.role {
			case bpdu.RoleDesignated, bpdu.RoleRoot:
				switch p.state {
				case StateDiscarding:
					p.state = StateLearning
					p.fwdDelayTimer = now.Add(l.forwardDelay)
				case StateLearning:
					p.state = StateForwarding
					p.forwardTransitions++
					stateChanged = true
					if !l.link(name).edge {
						l.raiseTopologyChange(mt, p.name, now, &flushes)
					}
				case StateForwarding:
				}
			case bpdu.RoleDisabled, bpdu.RoleAlternate, bpdu.RoleBackup:
			}
		}
	}

	// Every tree's received information keeps the landed 3xHelloTime silence
	// bound regardless of internal or external classification; only the test
	// for accepting new information at Receive differs by hop count or
	// message age. Loop guard is a CIST-only concept: an MSTI's own role on
	// an internal port never gets to hold a segment open past its peer, and
	// on a boundary port it mirrors the CIST outright.
	agedOut := false
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok || !p.rcvInfoValid || p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
				continue
			}
			if id == cistID && p.loopGuardWatches(l.link(name)) &&
				(p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate || p.role == bpdu.RoleBackup) {
				p.loopInconsistent = true
			}
			p.rcvInfoValid = false
			agedOut = true
		}
	}

	// Every tree's topology change timer clears on its own schedule: an
	// MSTI's forward-delay ladder above can raise one independently of the
	// CIST's.
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		if !mt.topologyChangeTimer.IsZero() && !mt.topologyChangeTimer.After(now) {
			mt.topologyChangeTimer = time.Time{}
		}
	}

	if agedOut || stateChanged || autoEdgeFired {
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)
	}

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}
