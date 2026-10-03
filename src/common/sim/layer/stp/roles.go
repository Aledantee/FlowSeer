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
			if p.state != StateDiscarding && !p.agreed {
				return false
			}
		}
	}

	return true
}

// boundary reports whether the named port is a boundary port: the CIST's most
// recently received BPDU on it carried no MST configuration identifier, or
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

// recomputeAll runs recompute for every tree in deterministic order, the CIST
// first and then the other trees ascending, and aggregates the emissions.
// Under MSTP only the CIST emits: an MSTI's recompute is told not to, so the
// per-port transmit budget is spent once per port rather than once per
// instance, and the MSTI records ride the CIST's own BPDU. Under PVST every
// tree emits, because each VLAN's BPDU is a frame of its own metered against
// that tree's own budget.
func (l *Layer) recomputeAll(now time.Time, flushes *[]layer.FlushTarget) []layer.Emission {
	var emissions []layer.Emission

	for _, id := range l.treeOrder {
		emissions = append(emissions, l.recompute(l.trees[id], now, flushes, l.pvst != nil || id == cistID)...)
	}

	return emissions
}

// recompute runs one tree's root election and role and state assignment. On
// a boundary port, an MSTI tree (t.id != cistID) takes the CIST port's role
// and state outright rather than computing its own, which is the boundary
// role rule (netsim reports the CIST's Root where the standard would say
// Master; no separate Role value exists for it). emit gates the proposal
// emissions a root change triggers: only the CIST emits, so an MSTI's caller
// passes false and recompute returns no emissions for it.
func (l *Layer) recompute(t *tree, now time.Time, flushes *[]layer.FlushTarget, emit bool) []layer.Emission {
	var emissions []layer.Emission

	oldRootID := t.rootID
	oldRootCost := t.rootPathCost
	oldRootPort := t.rootPort

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
		// bpduGuardDisabled and loopInconsistent are link-on-cist: only the
		// CIST's copy is ever written, so every tree's root election reads
		// them through cistP the way the role switch below already does. A
		// guard that fires on the CIST must hold every MSTI's election too,
		// not only the CIST's own. Restricted role denies the port the root
		// role, and pvidInconsistent is tree-owned: a peer that disagrees
		// about which VLAN the link is describes a different VLAN's tree, so
		// it reads from p. None of the four may contribute the bridge's root
		// vector. Every tree is built from l.portNames, so the CIST always
		// has a matching port for any port a tree tracks; a missing one here
		// only guards that invariant, not a case this simulator reaches.
		cistP, ok := l.cist().ports[name]
		if !ok || link.bpduGuardDisabled || p.cfg.RestrictedRole || (cistP != nil && cistP.loopInconsistent) || p.pvidInconsistent {
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

	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok {
			continue
		}
		link := l.links[name]

		// The boundary role rule is MSTP's: an MSTI on a port facing another
		// region follows the CIST rather than running its own election. It
		// must not reach a PVST bridge, where l.mst is nil by construction
		// and the first ordinary VLAN 1 hello marks every port external
		// (Receive sets external from the internal classification, which
		// needs l.mst). Ungated, every non-VLAN-1 tree would copy VLAN 1's
		// role on every port instead of electing its own root.
		if l.mst != nil && t.id != cistID && l.boundary(name) {
			// Every tree is built from l.portNames, so the CIST always has a
			// matching port for any port a tree tracks; the zero portState
			// below only guards that invariant, not a case this simulator
			// reaches.
			cistP, ok := l.cist().ports[name]
			if !ok {
				cistP = &portState{}
			}
			oldRole := p.role
			p.role = cistP.role
			if p.role != oldRole && p.role != bpdu.RoleDesignated {
				p.agreed = false
			}

			continue
		}

		// bpduGuardDisabled and loopInconsistent are bridge-global properties
		// of the port, like the internal/external classification: only the
		// CIST's copy is ever written (Receive's guard branch, and Advance's
		// loop-guard arm), so an MSTI reads them from the CIST's port state
		// the way it already reaches across for l.boundary. Every tree is
		// built from l.portNames, so the CIST always has a matching port for
		// any port a tree tracks; the zero portState below only guards that
		// invariant, not a case this simulator reaches.
		cistP, ok := l.cist().ports[name]
		if !ok {
			cistP = &portState{}
		}

		oldRole := p.role
		switch {
		case !link.up || link.bpduGuardDisabled:
			p.role = bpdu.RoleDisabled
		case cistP.loopInconsistent:
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
		// An agreement belongs to the Designated role that earned it; a port
		// that leaves the role and comes back must propose again, or it would
		// forward without a handshake on a link whose peer never agreed.
		if p.role != oldRole && p.role != bpdu.RoleDesignated {
			p.agreed = false
		}
		// The edge delay counts from the moment the port could become an
		// edge; a port that returns to Designated with the timer long past
		// would otherwise report a wake in the past.
		if t.id == cistID && p.role == bpdu.RoleDesignated && oldRole != bpdu.RoleDesignated && p.cfg.AutoEdge {
			link.edgeDelayWhile = now.Add(l.edgeDelay(t, link))
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
			// Every tree is built from l.portNames, so the CIST always has a
			// matching port for any port a tree tracks; the zero portState
			// below only guards that invariant, not a case this simulator
			// reaches.
			cistP, ok := l.cist().ports[name]
			if !ok {
				cistP = &portState{}
			}
			p.state = cistP.state
			// A port that just flipped internal to boundary may still carry
			// a live timer from its internal role and state ladder; a
			// boundary port never drives its own state, so nothing else
			// clears it. Left set, the next Advance would advance the port on
			// a timer behind a state this branch already mirrored, raising
			// a topology change with nothing behind it.
			p.fwdDelayTimer = time.Time{}
			if oldState != StateForwarding && p.state == StateForwarding {
				p.forwardTransitions++
			}

			continue
		}

		switch p.role {
		case bpdu.RoleDisabled, bpdu.RoleAlternate, bpdu.RoleBackup:
			p.state = StateDiscarding
			p.fwdDelayTimer = time.Time{}
		case bpdu.RoleRoot:
			if link.pointToPoint && l.isSynced(t, p.name) && link.sendRSTP {
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			} else if p.state == StateDiscarding && p.fwdDelayTimer.IsZero() {
				// No agreement path: the forward delay ladder carries the port
				// through Learning and Forwarding as on a shared link.
				p.fwdDelayTimer = now.Add(l.forwardDelay)
			}
		case bpdu.RoleDesignated:
			switch {
			case link.edge:
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			case link.pointToPoint && p.agreed:
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			default:
				// Without an agreement the port still forwards after two
				// forward delays, so a peer that never answers, a host for
				// one, does not leave the port dark for the run.
				if p.state == StateDiscarding && p.fwdDelayTimer.IsZero() {
					p.fwdDelayTimer = now.Add(l.forwardDelay)
				}
			}
		}

		if oldState != StateForwarding && p.state == StateForwarding {
			p.forwardTransitions++
			if !link.edge {
				l.raiseTopologyChange(t, p.name, now, flushes)
			}
		} else if oldState == StateForwarding && p.state != StateForwarding {
			if !link.edge {
				l.raiseTopologyChange(t, p.name, now, flushes)
			}
		}
	}

	if emit && (t.rootID != oldRootID || t.rootPathCost != oldRootCost || t.rootPort != oldRootPort) {
		for _, name := range l.portNames {
			p := t.ports[name]
			link := l.links[name]
			if link.up && p.role == bpdu.RoleDesignated && link.pointToPoint && p.state == StateDiscarding && !p.agreed {
				l.emit(t, p, now, emissionDesignated, &emissions)
			}
		}
	}

	return emissions
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
