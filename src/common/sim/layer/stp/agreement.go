package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// treeMessage is what one received BPDU conveys for one tree: the CIST's
// message is the BPDU itself, and an MSTI's is one of its records. The vector
// has the shape rawVector gives the stored vector of the same tree.
type treeMessage struct {
	role      bpdu.Role
	agreement bool
	vector    priorityVector
}

// recordAgreement sets the Agreement of tree t's port p from one message, as
// IEEE Std 802.1Q-2003 13.26.9 and 13.26.10 say for the CIST and an MSTI
// alike. The port agrees when the link is point-to-point, it sends RSTP, the
// message carries the Agreement flag, and the message conveys a Root,
// Alternate, or Backup role with a vector the same as or worse than the
// port's own, or a Designated role with one the same or better. Any other
// message it is run for clears the flag.
//
// Only a Designated port keeps an agreement. The flag is the answer to its
// own proposal, and a port that later becomes Designated must propose again.
func recordAgreement(t *tree, p *portState, lk *linkRecord, m treeMessage) {
	p.agreed = false
	if !m.agreement || !lk.pointToPoint || !lk.sendRSTP || p.role != bpdu.RoleDesignated {
		return
	}

	switch order := compareVectors(m.vector, designatedVector(t, p, lk.external)); m.role {
	case bpdu.RoleRoot, bpdu.RoleAlternate, bpdu.RoleBackup:
		p.agreed = order >= 0
	case bpdu.RoleDesignated:
		p.agreed = order <= 0
	case bpdu.RoleDisabled:
	}

	if p.agreed {
		p.proposing = false
	}
}

// mirrorAgreement gives every MSTI port the CIST port's agreement. A frame
// from another region carries no MSTI records, and its CIST message stands
// for every instance (13.26.9).
func (l *Layer) mirrorAgreement(cistP *portState) {
	for _, id := range l.treeOrder {
		if mp, ok := l.trees[id].ports[cistP.name]; ok && id != cistID {
			mp.agreed = cistP.agreed
		}
	}
}

// cistHolds reports whether the CIST message of b names the CIST root,
// external cost, and regional root that the named port holds. A Designated
// port holds the vector it offers, and any other port holds the one it
// stored. An MSTI agreement is recorded only when this is so (13.26.10 a).
func (l *Layer) cistHolds(name string, b bpdu.BPDU) bool {
	t := l.cist()
	p := t.ports[name]
	external := l.link(name).external

	held := rawVector(t, p, external)
	if p.role == bpdu.RoleDesignated || !p.rcvInfoValid {
		held = designatedVector(t, p, external)
	}

	return b.RootID == held.rootID && b.RootPathCost == held.externalRootPathCost &&
		b.RegionalRootID == held.regionalRootID
}

// answerProposals acts on every proposal b carries for the port: its own
// tree's, and under MSTP the CIST's again on a port facing another region
// (13.26.13) or the proposals of the records on a port inside the region. A
// proposal counts only when the message conveys a Designated role (P802.1aq
// D1.5 13.29.20). It reports whether the port owes its peer an agreement.
func (l *Layer) answerProposals(t *tree, p *portState, now time.Time, b bpdu.BPDU, internal bool, flushes *[]layer.FlushTarget) bool {
	proposed := b.Proposal() && b.Role() == bpdu.RoleDesignated
	answered := proposed && l.syncOnProposal(t, p, now, flushes)

	if l.mst == nil || t.id != cistID {
		return answered
	}

	if !internal {
		for _, id := range l.treeOrder {
			if id == cistID || !proposed {
				continue
			}
			if mp, ok := l.trees[id].ports[p.name]; ok {
				answered = l.syncOnProposal(l.trees[id], mp, now, flushes) || answered
			}
		}

		return answered
	}

	for _, rec := range b.MSTIs {
		mt, ok := l.trees[treeID(rec.MSTID)]
		if !ok || mt.id == cistID || rec.RemainingHops <= 1 {
			continue
		}
		flags := bpdu.BPDU{Flags: rec.Flags}
		if mp, ok := mt.ports[p.name]; ok && flags.Proposal() && flags.Role() == bpdu.RoleDesignated {
			answered = l.syncOnProposal(mt, mp, now, flushes) || answered
		}
	}

	return answered
}

// syncOnProposal blocks the other Designated ports of tree t when its Root or
// Alternate port p is proposed to, so that p can agree, and opens a Root port
// that is now in sync. It reports whether p is a port that agrees.
func (l *Layer) syncOnProposal(t *tree, p *portState, now time.Time, flushes *[]layer.FlushTarget) bool {
	if p.role != bpdu.RoleRoot && p.role != bpdu.RoleAlternate {
		return false
	}

	for _, otherName := range l.portNames {
		otherP, ok := t.ports[otherName]
		if !ok || otherName == p.name {
			continue
		}
		otherLk := l.link(otherName)
		if otherP.role != bpdu.RoleDesignated || otherLk.edge {
			continue
		}
		otherP.agreed = false
		otherP.proposing = otherLk.pointToPoint && otherLk.sendRSTP
		if otherP.state != StateDiscarding {
			wasFwd := otherP.state == StateForwarding
			otherP.state = StateDiscarding
			if wasFwd {
				l.raiseTopologyChange(t, otherP.name, now, flushes)
			}
		}
	}

	lk := l.link(p.name)
	if p.role == bpdu.RoleRoot && lk.pointToPoint && l.isSynced(t, p.name) && lk.sendRSTP && p.state != StateForwarding {
		p.state = StateForwarding
		p.forwardTransitions++
		if !lk.edge {
			l.raiseTopologyChange(t, p.name, now, flushes)
		}
	}

	return true
}
