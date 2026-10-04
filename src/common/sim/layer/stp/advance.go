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

	for _, id := range l.treeOrder {
		t := l.trees[id]
		update(t.helloTimer)

		for _, p := range t.ports {
			update(p.fwdDelayTimer)
			update(p.tcWhile)
			if p.rcvInfoValid {
				update(p.rcvTime.Add(3 * p.rcvHelloTime))
			}
			// The edge delay is due only on a port that can still become an
			// edge, or a wake would be scheduled that changes nothing.
			link := l.links[p.name]
			if p.cfg.AutoEdge && !link.edge && link.up && link.sendRSTP && p.role == bpdu.RoleDesignated &&
				p.state == StateDiscarding && link.pointToPoint && p.proposing {
				update(link.edgeDelayWhile)
			}
			tx := l.tx(t, p.name)
			if (tx.pendingAgreement || tx.pendingDesignated || tx.pendingTCN) && !tx.tick.IsZero() {
				update(tx.tick)
			}
		}
	}

	return next, hasTimer
}

// Advance advances timer-driven state to now, firing due hellos, forward delays,
// topology change timers, and information age-outs.
func (l *Layer) Advance(now time.Time) layer.Effects {
	var flushes []layer.FlushTarget
	var emissions []layer.Emission
	changes := newTopologyChangeEmissions()

	func() {
		agedOut := l.expireReceivedInfo(now)
		autoEdgeFired := l.advanceAutoEdge(now, &flushes)
		stateChanged := l.advanceForwardDelay(now, &flushes, changes)
		l.clearExpiredTCWhile(now)

		if agedOut || stateChanged || autoEdgeFired {
			emissions = append(emissions, l.recomputeAll(now, &flushes, changes)...)
		}

		l.releaseHeldTransmissions(now, &emissions, changes)
		l.sendDueHellos(now, &emissions, changes)
	}()
	l.emitTopologyChangeEmissions(now, changes, &emissions)

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

func (l *Layer) expireReceivedInfo(now time.Time) bool {
	agedOut := false
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok || !p.rcvInfoValid || p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
				continue
			}
			link := l.links[p.name]
			if (id == cistID || l.pvst != nil) && p.loopGuardWatches(link) &&
				(p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate || p.role == bpdu.RoleBackup) {
				p.loopInconsistent = true
			}
			p.rcvInfoValid = false
			agedOut = true
		}
	}

	return agedOut
}

func (l *Layer) advanceAutoEdge(now time.Time, flushes *[]layer.FlushTarget) bool {
	t := l.cist()
	autoEdgeFired := false
	for _, name := range l.portNames {
		p := t.ports[name]
		link := l.links[name]
		if p.cfg.AutoEdge && link.sendRSTP && link.up && p.role == bpdu.RoleDesignated &&
			p.state == StateDiscarding && link.pointToPoint && p.proposing &&
			!link.edgeDelayWhile.IsZero() && !link.edgeDelayWhile.After(now) {
			link.edge = true
			link.edgeDelayWhile = time.Time{}
			for _, id := range l.treeOrder {
				if tp, ok := l.trees[id].ports[name]; ok {
					if tp.state != StateForwarding {
						tp.state = StateForwarding
						tp.forwardTransitions++
					}
					tp.fwdDelayTimer = time.Time{}
					tp.proposing = false
					l.deactivatePort(l.trees[id], tp, flushes)
				}
			}
			autoEdgeFired = true
		}
	}

	return autoEdgeFired
}

func (l *Layer) advanceForwardDelay(now time.Time, flushes *[]layer.FlushTarget, changes *topologyChangeEmissions) bool {
	stateChanged := false
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		_, _, fwdDelay := l.times(mt)
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
					p.fwdDelayTimer = now.Add(fwdDelay)
				case StateLearning:
					p.state = StateForwarding
					p.forwardTransitions++
					stateChanged = true
					link := l.links[p.name]
					if !link.edge {
						l.initiateTopologyChange(mt, p, now, flushes, changes)
					}
				case StateForwarding:
				}
			case bpdu.RoleDisabled, bpdu.RoleAlternate, bpdu.RoleBackup:
			}
		}
	}

	return stateChanged
}

func (l *Layer) clearExpiredTCWhile(now time.Time) {
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			if p, ok := mt.ports[name]; ok {
				if !p.tcWhile.IsZero() && !p.tcWhile.After(now) {
					p.tcWhile = time.Time{}
				}
			}
		}
	}
}

func (l *Layer) releaseHeldTransmissions(now time.Time, emissions *[]layer.Emission, changes *topologyChangeEmissions) {
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok {
				continue
			}
			tx := l.tx(mt, name)
			link := l.links[name]
			if !link.up || tx.tick.IsZero() || tx.tick.After(now) {
				continue
			}
			if tx.pendingAgreement {
				tx.pendingAgreement = false
				if p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate {
					l.emit(mt, p, now, emissionAgreement, emissions, changes)
				}
			}
			if tx.pendingDesignated {
				tx.pendingDesignated = false
				if p.role == bpdu.RoleDesignated {
					l.emit(mt, p, now, emissionDesignated, emissions, changes)
				}
			}
			if tx.pendingTCN {
				tx.pendingTCN = false
				if !link.sendRSTP && p.role == bpdu.RoleRoot && !p.tcWhile.IsZero() && p.tcWhile.After(now) {
					l.emit(mt, p, now, emissionTCN, emissions, changes)
				}
			}
		}
	}
}

func (l *Layer) sendDueHellos(now time.Time, emissions *[]layer.Emission, changes *topologyChangeEmissions) {
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		if mt.helloTimer.IsZero() || mt.helloTimer.After(now) {
			continue
		}
		mt.helloTimer = now.Add(l.helloTime)
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			link := l.links[name]
			if !ok || !link.up {
				continue
			}
			if p.role == bpdu.RoleDesignated {
				l.emit(mt, p, now, emissionDesignated, emissions, changes)
			} else if (p.role == bpdu.RoleRoot && !p.tcWhile.IsZero() && p.tcWhile.After(now)) ||
				(l.mst != nil && l.rootTopologyChangeActive(name, now)) {
				if link.sendRSTP {
					l.emit(mt, p, now, emissionAgreement, emissions, changes)
				} else {
					l.emit(mt, p, now, emissionTCN, emissions, changes)
				}
			}
		}
	}
}
