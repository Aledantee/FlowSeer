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
			if l.pvst == nil && id != cistID {
				continue
			}
			tx := l.tx(t, p.name)
			if !tx.helloWhen.IsZero() && l.helloWouldRequest(t, p, tx.helloWhen) {
				update(tx.helloWhen)
			}
			if (tx.newInfo || tx.newInfoMsti) && tx.count >= int(l.txHoldCount) && !tx.tick.IsZero() && l.transmitRequested(p, tx) {
				update(tx.tick)
			}
		}
	}

	return next, hasTimer
}

// Advance advances timer-driven state to now, firing due hellos, forward delays,
// topology change timers, and information age-outs.
func (l *Layer) Advance(now time.Time) layer.Effects {
	l.settleHelloTimers(now)
	var flushes []layer.FlushTarget
	var emissions []layer.Emission

	func() {
		agedOut := l.expireReceivedInfo(now)
		autoEdgeFired := l.advanceAutoEdge(now, &flushes)
		stateChanged := l.advanceForwardDelay(now, &flushes)
		l.clearExpiredTCWhile(now)

		if agedOut || stateChanged || autoEdgeFired {
			l.recomputeAll(now, &flushes)
		}
	}()
	l.transmit(now, &emissions)

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

func (l *Layer) advanceForwardDelay(now time.Time, flushes *[]layer.FlushTarget) bool {
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
						l.detectTopologyChange(mt, p, now, flushes)
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

func (l *Layer) helloWouldRequest(t *tree, p *portState, at time.Time) bool {
	if t.id == cistID || (l.pvst != nil && l.links[p.name].sendRSTP) {
		if p.role == bpdu.RoleDesignated || (p.role == bpdu.RoleRoot && activeAt(p.tcWhile, at)) {
			return true
		}
	}
	if l.pvst == nil && t.id == cistID {
		for _, id := range l.treeOrder {
			if id == cistID {
				continue
			}
			mp := l.trees[id].ports[p.name]
			if (mp.role == bpdu.RoleDesignated || (mp.role == bpdu.RoleRoot && activeAt(mp.tcWhile, at))) && !l.mstiMasterPort(p.name) {
				return true
			}
		}
	}

	return false
}
