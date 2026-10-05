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

		for _, p := range t.ports {
			update(p.fwdDelayTimer)
			update(p.tcWhile)
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
			if (tx.pendingAgreement || tx.pendingDesignated || tx.pendingTopology) && !tx.tick.IsZero() {
				update(tx.tick)
			}
		}
	}

	return next, hasTimer
}

// Advance advances timer-driven state to now, firing due hellos, forward delays,
// topology change timers, and information age-outs. Received information
// expires, the forward-delay ladder steps, and roles are recomputed before a
// held BPDU is released or a hello is sent: every transmit transition waits
// for the port information to settle (IEEE Std 802.1Q-2003 Figure 13-13
// qualifies each by selected && !updtInfo), so a BPDU sent at the instant the
// root's information expires names the bridge's new root, not the old one.
func (l *Layer) Advance(now time.Time) layer.Effects {
	var flushes []layer.FlushTarget
	var emissions []layer.Emission

	changed := l.detectAutoEdge(now)
	if l.expireInformation(now) {
		changed = true
	}
	if l.stepForwardDelay(now) {
		changed = true
	}
	l.clearSpentTopologyTimers(now)

	if changed {
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)
	}

	l.emitDue(now, &emissions)

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// detectAutoEdge makes an edge of every auto-edge port whose edge delay ran
// out with no answer to its proposal, and reports whether one was made.
func (l *Layer) detectAutoEdge(now time.Time) bool {
	t := l.cist()
	fired := false

	for _, name := range l.portNames {
		p := t.ports[name]
		lk := l.link(name)
		if p.cfg.AutoEdge && lk.sendRSTP && lk.up && p.role == bpdu.RoleDesignated &&
			p.state == StateDiscarding && lk.pointToPoint && p.proposing &&
			!lk.edgeDelayWhile.IsZero() && !lk.edgeDelayWhile.After(now) {
			// edge is on the link record, so an MSTI port reaches Forwarding
			// at the same wake as the CIST's through the recompute that
			// follows, and no tree counts the port as active.
			lk.edge = true
			lk.edgeDelayWhile = time.Time{}
			p.state = StateForwarding
			p.fwdDelayTimer = time.Time{}
			p.proposing = false
			p.forwardTransitions++
			fired = true
		}
	}

	return fired
}

// expireInformation drops every tree's received information that has been
// silent for 3 hello times and reports whether any was dropped. The silence
// bound holds whatever the internal or external classification, and only the
// test for accepting new information at Receive differs by hop count or
// message age. Loop guard arms where the information lived: on the tree's own
// port under PVST, and on the CIST's alone otherwise, where an MSTI's own role
// on an internal port reads it and a boundary port mirrors the CIST outright.
func (l *Layer) expireInformation(now time.Time) bool {
	expired := false

	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok || !p.rcvInfoValid || p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
				continue
			}
			if (l.pvst != nil || id == cistID) && p.loopGuardWatches(l.link(name)) &&
				(p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate || p.role == bpdu.RoleBackup) {
				p.loopInconsistent = true
			}
			p.rcvInfoValid = false
			expired = true
		}
	}

	return expired
}

// stepForwardDelay climbs the forward-delay ladder of every port whose timer
// is due, by the Forward Delay in force for its tree, and reports whether a
// port reached Forwarding. The ladder runs per tree, since role and state are
// per tree: an MSTI's own internal ports climb it independently of the CIST's.
// A boundary port never sets fwdDelayTimer for a non-CIST tree (recompute
// mirrors its state from the CIST outright), so this never double-drives one.
func (l *Layer) stepForwardDelay(now time.Time) bool {
	forwarded := false

	for _, id := range l.treeOrder {
		mt := l.trees[id]
		forwardDelay := l.forwardDelayOf(mt)
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
					p.fwdDelayTimer = now.Add(forwardDelay)
				case StateLearning:
					p.state = StateForwarding
					p.forwardTransitions++
					forwarded = true
				case StateForwarding:
				}
			case bpdu.RoleDisabled, bpdu.RoleAlternate, bpdu.RoleBackup:
			}
		}
	}

	return forwarded
}

// clearSpentTopologyTimers zeroes every port's topology change timer that ran
// out, so it stops reporting a wake. Every port's timer clears on its own
// schedule.
func (l *Layer) clearSpentTopologyTimers(now time.Time) {
	for _, mt := range l.trees {
		for _, p := range mt.ports {
			if !p.tcWhile.IsZero() && !p.tcRunning(now) {
				p.tcWhile = time.Time{}
			}
		}
	}
}

// emitDue releases the BPDUs the transmit budget held and sends the hello of
// every tree whose timer is due. Both walk every tree, the way the loops
// above do. Outside PVST mode the walk is behavior-neutral: treeOrder holds
// the CIST first, the budget is shared, and an MSTI's own hello timer never
// runs because only the CIST emits. Inside it, a per-VLAN tree's periodic
// hello would otherwise never fire and a transmission held on it would never
// be released.
func (l *Layer) emitDue(now time.Time, emissions *[]layer.Emission) {
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
					l.emit(mt, p, now, emissionAgreement, emissions)
				}
			}
			if tx.pendingDesignated {
				tx.pendingDesignated = false
				if p.role == bpdu.RoleDesignated {
					l.emit(mt, p, now, emissionDesignated, emissions)
				}
			}
			if tx.pendingTopology {
				tx.pendingTopology = false
				if l.rootReports(mt, name, now) {
					l.emit(mt, p, now, emissionTopology, emissions)
				}
			}
		}

		if mt.helloTimer.IsZero() || mt.helloTimer.After(now) {
			continue
		}
		mt.helloTimer = now.Add(l.helloTime)
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok || !l.link(name).up {
				continue
			}
			// A Root port repeats a running topology change at each hello,
			// where a Designated port sends its ordinary hello.
			switch {
			case p.role == bpdu.RoleDesignated:
				l.emit(mt, p, now, emissionDesignated, emissions)
			case l.rootReports(mt, name, now):
				l.emit(mt, p, now, emissionTopology, emissions)
			}
		}
	}
}
