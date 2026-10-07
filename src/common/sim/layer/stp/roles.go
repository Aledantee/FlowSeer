package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func (l *Layer) isSynced(t *tree, rootPort string) bool {
	for name, p := range t.ports {
		if name == rootPort {
			continue
		}
		link := l.links[name]
		if p.role == bpdu.RoleDesignated && !link.edge {
			if !p.synced {
				return false
			}
		}
	}

	return true
}

// boundary reports whether the named port is a boundary port: the CIST's most
// recently stored BPDU on it carried no MST configuration identifier, or
// one from a different region. It answers false for a port the CIST does not
// track, which for a plain RSTP bridge with no MSTI trees is moot since this
// is only ever consulted from one.
func (l *Layer) boundary(name string) bool {
	link, ok := l.links[name]
	if !ok {
		return false
	}

	return link.external
}

// armHelloTimers starts the periodic hello on every tree that drives its own
// transmission: the CIST alone outside PVST mode, since an MSTI's information
// rides the CIST's BPDU, and every VLAN's tree inside it. Each port's
// transmit record owns the timer, which setHelloRequests reads in transmit.
func (l *Layer) armHelloTimers(now time.Time) {
	for _, id := range l.treeOrder {
		if l.pvst == nil && id != cistID {
			continue
		}
		t := l.trees[id]
		for _, name := range l.portNames {
			if !l.links[name].up {
				continue
			}
			tx := l.tx(t, name)
			if tx.helloWhen.IsZero() {
				tx.helloWhen = now.Add(l.helloTime)
			}
		}
	}
}

// recomputeAll runs recompute for every tree in deterministic order, the CIST
// first and then the other trees ascending. The later transmit pass owns
// frame construction. Under MSTP only the CIST transmits, so the
// per-port transmit budget is spent once per port rather than once per
// instance, and the MSTI records ride the CIST's own BPDU. Under PVST every
// tree transmits, because each VLAN's BPDU is a frame of its own metered
// against that tree's own budget.
func (l *Layer) recomputeAll(now time.Time, flushes *[]layer.FlushTarget) {
	for _, id := range l.treeOrder {
		l.recompute(l.trees[id], now, flushes)
	}
}

// recompute runs one tree's root election and role and state assignment. On
// a boundary port, an MSTI tree (t.id != cistID) takes the CIST port's role
// and bounds its state by the CIST's, which is the boundary
// role rule (netsim reports the CIST's Root where the standard would say
// Master). No separate Role value exists for it. A root change requests new
// information on eligible designated ports. The later transmit pass decides
// whether that information is carried by the CIST or by an MSTI record.
func (l *Layer) recompute(t *tree, now time.Time, flushes *[]layer.FlushTarget) {
	oldRootID := t.rootID
	oldRootCost := t.rootPathCost
	oldRootPort := t.rootPort

	l.electRoot(t, now)
	l.assignRoles(t, now)
	l.updatePortStates(t, now, flushes)

	if t.rootID != oldRootID || t.rootPathCost != oldRootCost || t.rootPort != oldRootPort {
		for _, name := range l.portNames {
			p := t.ports[name]
			link := l.links[name]
			if link.up && p.role == bpdu.RoleDesignated && link.pointToPoint && p.state == StateDiscarding && !p.agreed {
				l.requestNewInfo(t, p)
			}
		}
	}
}

func (l *Layer) electRoot(t *tree, now time.Time) {
	bestVector := priorityVector{
		rootID:         t.bridgeID,
		regionalRootID: t.bridgeID,
		bridgeID:       t.bridgeID,
	}
	bestPort := ""
	bestRcvPortID := uint16(0)

	for _, name := range l.portNames {
		p, ok := t.ports[name]
		link, hasLink := l.links[name]
		if !ok || !hasLink || !link.up || !p.rcvInfoValid {
			continue
		}
		// bpduGuardDisabled is link-owned. Outside PVST, loopInconsistent is
		// written on the CIST's copy and every tree reads it through cistP. Under
		// PVST each tree tracks its own loop guard inconsistency. Restricted role
		// denies the port the root role, and pvidInconsistent is tree-owned: a peer
		// that disagrees about which VLAN the link carries describes a different
		// VLAN's tree, so it reads from p. None of the four may contribute the
		// bridge's root vector.
		cistP, ok := l.cist().ports[name]
		isLoopInconsistent := p.loopInconsistent || (l.pvst == nil && cistP != nil && cistP.loopInconsistent)
		if !ok || link.bpduGuardDisabled || p.cfg.RestrictedRole || isLoopInconsistent || p.pvidInconsistent {
			continue
		}
		if !p.rcvTime.Add(3 * p.rcvHelloTime).After(now) {
			continue
		}

		cand := candidateVector(t, p, link.external)

		if bestPort == "" {
			if compareVectors(cand, bestVector) < 0 {
				bestVector = cand
				bestPort = name
				bestRcvPortID = p.portID
			}
		} else {
			diff := compareVectors(cand, bestVector)
			if diff < 0 || (diff == 0 && p.portID < bestRcvPortID) {
				bestVector = cand
				bestPort = name
				bestRcvPortID = p.portID
			}
		}
	}

	if bestPort == "" {
		t.rootID = t.bridgeID
		t.rootPathCost = 0
		t.rootPort = ""
		if t.id == cistID {
			t.regionalRootID = t.bridgeID
			t.internalRootPathCost = 0
		}
	} else {
		t.rootPort = bestPort
		if t.id == cistID {
			t.rootID = bestVector.rootID
			t.rootPathCost = bestVector.externalRootPathCost
			t.regionalRootID = bestVector.regionalRootID
			t.internalRootPathCost = bestVector.internalRootPathCost
		} else {
			t.rootID = bestVector.regionalRootID
			t.rootPathCost = bestVector.internalRootPathCost
		}
	}
}

func (l *Layer) assignRoles(t *tree, now time.Time) {
	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok {
			continue
		}
		link := l.links[name]

		// The boundary role rule is MSTP's: an MSTI on a port facing another
		// region follows the CIST rather than running its own election. It
		// must not reach a PVST bridge, where l.mst is nil by construction
		// and the first ordinary VLAN 1 hello marks every port external.
		if l.mst != nil && t.id != cistID && l.boundary(name) {
			cistP, ok := l.cist().ports[name]
			if !ok {
				cistP = &portState{}
			}
			oldRole := p.role
			p.role = cistP.role
			p.recordRoleChange(oldRole, now)
			if p.role != oldRole {
				p.agreed = false
			}
			l.trackDesignatedVector(t, p, oldRole, link.external)

			continue
		}

		cistP, ok := l.cist().ports[name]
		if !ok {
			cistP = &portState{}
		}

		oldRole := p.role
		switch {
		case !link.up || link.bpduGuardDisabled:
			p.role = bpdu.RoleDisabled
		case p.loopInconsistent || (l.pvst == nil && cistP.loopInconsistent):
			// A loop-inconsistent port is Alternate and never Designated: a
			// port that stopped hearing its designated peer is the one that
			// would open a loop by claiming the segment.
			p.role = bpdu.RoleAlternate
		case p.pvidInconsistent:
			// Read from this tree's own port state, not the CIST's: PVID
			// inconsistency is set on the arrival VLAN's tree, so consulting
			// cistP would read a field no tree but VLAN 1's ever has set and
			// disable the check for every VLAN it exists to protect.
			p.role = bpdu.RoleAlternate
		case name == t.rootPort:
			p.role = bpdu.RoleRoot
		default:
			p.role = l.designatedOrBlocked(t, p, now)
		}
		p.recordRoleChange(oldRole, now)
		// An agreement belongs to the role that earned it.
		if p.role != oldRole {
			p.agreed = false
		}
		l.trackDesignatedVector(t, p, oldRole, link.external)
		if t.id == cistID && p.role == bpdu.RoleDesignated && oldRole != bpdu.RoleDesignated && p.proposing {
			link.edgeDelayWhile = now.Add(l.edgeDelay(t, link))
		}
	}
}

// trackDesignatedVector keeps the agreement and the synced flag of a Designated
// port across a change of its designated priority vector. A vector that is
// worse than the one the port last held costs it the agreement, since its
// peer gave the agreement to the better vector, and a port that then forwards
// without an agreement is no longer synced. A port that takes the Designated
// role from another one starts over: synced only while Discarding.
func (l *Layer) trackDesignatedVector(t *tree, p *portState, oldRole bpdu.Role, external bool) {
	if p.role != bpdu.RoleDesignated {
		return
	}

	vec := designatedVector(t, p, external)
	switch {
	case oldRole != bpdu.RoleDesignated:
		p.synced = p.state == StateDiscarding
	case !p.designatedVecSet || vec == p.designatedVec:
	default:
		if compareVectors(vec, p.designatedVec) > 0 {
			p.agreed = false
		}
		p.synced = p.agreed || p.state == StateDiscarding
	}
	p.designatedVec = vec
	p.designatedVecSet = true
}

func (l *Layer) updatePortStates(t *tree, now time.Time, flushes *[]layer.FlushTarget) {
	_, _, fwdDelay := l.times(t)

	// A Designated port is synced while it is Discarding, holds an agreement,
	// or is an edge, and stays synced after it leaves Discarding.
	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok {
			continue
		}
		if !p.leftBackupAt.IsZero() && l.recentBackupDeadline(t, p, now).IsZero() {
			p.leftBackupAt = time.Time{}
		}
		if p.role == bpdu.RoleDisabled || p.role == bpdu.RoleAlternate || p.role == bpdu.RoleBackup ||
			(l.mst != nil && t.id != cistID && l.boundary(name)) {
			p.clearRecentRoot()
		}
		if p.role == bpdu.RoleDesignated && (p.state == StateDiscarding || p.agreed || l.links[name].edge) {
			p.synced = true
			p.clearRecentRoot()
		}
	}

	if root := t.ports[t.rootPort]; root != nil && root.role == bpdu.RoleRoot && root.state != StateForwarding &&
		(l.mst == nil || t.id == cistID || !l.boundary(root.name)) {
		for _, name := range l.portNames {
			p := t.ports[name]
			link := l.links[name]
			if p == nil || p.role != bpdu.RoleDesignated || link.edge || p.state == StateDiscarding ||
				(l.mst != nil && t.id != cistID && l.boundary(name)) || (!p.pendingRootDeadline && p.recentRootDeadline(now).IsZero()) {
				continue
			}
			p.state = StateDiscarding
			p.fwdDelayTimer = now.Add(fwdDelay)
			p.agreed = false
			p.synced = true
			p.clearRecentRoot()
			p.proposing = link.pointToPoint && link.sendRSTP
			if p.proposing {
				l.requestNewInfo(t, p)
			}
			if t.id == cistID && p.proposing {
				link.edgeDelayWhile = now.Add(l.edgeDelay(t, link))
			}
		}
	}

	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok {
			continue
		}
		oldState := p.state
		link := l.links[name]

		// The state half of the boundary role rule, gated for the same reason
		// its role half above is: on a PVST bridge every port reads external,
		// and a VLAN's tree would mirror VLAN 1's state over the state its
		// own election just decided.
		if l.mst != nil && t.id != cistID && l.boundary(name) {
			cistP, ok := l.cist().ports[name]
			if !ok {
				cistP = &portState{}
			}
			if p.role != bpdu.RoleDesignated || p.agreed || link.edge || p.boundarySyncState == StateForwarding {
				p.boundarySyncState = ""
			}
			p.state = cistP.state
			if p.boundarySyncState != "" {
				if p.boundarySyncState == StateDiscarding || cistP.state == StateForwarding {
					p.state = p.boundarySyncState
				}
				if p.fwdDelayTimer.IsZero() || p.fwdDelayTimer.After(now.Add(fwdDelay)) {
					p.fwdDelayTimer = now.Add(fwdDelay)
				}
			} else {
				p.fwdDelayTimer = time.Time{}
			}
			if oldState != StateForwarding && p.state == StateForwarding {
				p.forwardTransitions++
				if !link.edge && (p.role == bpdu.RoleRoot || p.role == bpdu.RoleDesignated) {
					l.detectTopologyChange(t, p, now, flushes)
				}
			}
			if link.edge || (p.role != bpdu.RoleRoot && p.role != bpdu.RoleDesignated) {
				l.deactivatePort(t, p, flushes)
			}

			continue
		}
		p.boundarySyncState = ""

		switch p.role {
		case bpdu.RoleDisabled, bpdu.RoleAlternate, bpdu.RoleBackup:
			p.state = StateDiscarding
			p.fwdDelayTimer = time.Time{}
			l.deactivatePort(t, p, flushes)
		case bpdu.RoleRoot:
			if link.pointToPoint && l.recentBackupDeadline(t, p, now).IsZero() && link.sendRSTP {
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			} else if p.state == StateDiscarding && (p.fwdDelayTimer.IsZero() || p.fwdDelayTimer.After(now.Add(fwdDelay))) {
				p.fwdDelayTimer = now.Add(fwdDelay)
			}
		case bpdu.RoleDesignated:
			switch {
			case link.edge:
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
				l.deactivatePort(t, p, flushes)
			case link.pointToPoint && p.agreed:
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			default:
				// Without an agreement the port still forwards after two
				// forward delays, so a peer that never answers, a host for
				// one, does not leave the port dark for the run.
				if p.state == StateDiscarding && (p.fwdDelayTimer.IsZero() || p.fwdDelayTimer.After(now.Add(fwdDelay))) {
					p.fwdDelayTimer = now.Add(fwdDelay)
				}
			}
		}

		if oldState != StateForwarding && p.state == StateForwarding {
			p.forwardTransitions++
			if !link.edge && (p.role == bpdu.RoleRoot || p.role == bpdu.RoleDesignated) {
				l.detectTopologyChange(t, p, now, flushes)
			}
		}
	}
}

// designatedOrBlocked decides the role of a port that is up and not the root
// port: Alternate when a better bridge is designated on its segment, Backup
// when that bridge is this one through another port, else Designated.
func (l *Layer) designatedOrBlocked(t *tree, p *portState, now time.Time) bpdu.Role {
	if p.rcvInfoValid && p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
		ext := l.links[p.name].external
		desig := designatedVector(t, p, ext)
		rcv := rawVector(t, p, ext)
		if compareVectors(rcv, desig) < 0 {
			if p.rcvBridgeID == t.bridgeID {
				return bpdu.RoleBackup
			}

			return bpdu.RoleAlternate
		}
	}

	return bpdu.RoleDesignated
}
