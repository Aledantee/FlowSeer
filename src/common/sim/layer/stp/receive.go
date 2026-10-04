package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// receiveMSTIs stores the MSTI records an internal BPDU carries into each
// named instance's port state, one instance at a time by the same
// same-source-or-superior rule the CIST uses, and carries each record's own
// topology change bit into that instance the way Receive carries the CIST's:
// unconditionally, not gated on superiority, since a change notification is
// evidence about the fabric rather than a claim this port might reject. A
// record for an instance this bridge does not configure is ignored: the
// fabric's bridges are not required to share the same instance set. The
// designated bridge and port a record implies reuse the sending bridge's own
// address and the CIST port identifier's index half, since MSTI bridge and
// port identifiers differ from the CIST's only in their priority nibble
// (clause 13.7).
func (l *Layer) syncTree(t *tree, rootPort string, flushes *[]layer.FlushTarget) {
	for _, otherName := range l.portNames {
		if otherName == rootPort {
			continue
		}
		otherP := t.ports[otherName]
		if otherP == nil {
			continue
		}
		otherLink := l.links[otherName]
		if otherP.role == bpdu.RoleDesignated && !otherLink.edge {
			otherP.agreed = false
			otherP.proposing = otherLink.pointToPoint && otherLink.sendRSTP
			if otherP.state != StateDiscarding {
				wasFwd := otherP.state == StateForwarding
				otherP.state = StateDiscarding
				if wasFwd {
					l.deactivatePort(t, otherP, flushes)
				}
			}
		}
	}
}

func (l *Layer) handleProposal(t *tree, p *portState, link *linkRecord, now time.Time, flushes *[]layer.FlushTarget) {
	l.syncTree(t, p.name, flushes)
	if p.role == bpdu.RoleRoot && link.pointToPoint && l.isSynced(t, p.name) && link.sendRSTP {
		if p.state != StateForwarding {
			p.state = StateForwarding
			p.forwardTransitions++
			if !link.edge {
				l.initiateTopologyChange(t, p, now, flushes, nil)
			}
		}
	}
}

func (l *Layer) recordAgreement(t *tree, p *portState, link *linkRecord, incoming priorityVector, role bpdu.Role, agreement bool, now time.Time, flushes *[]layer.FlushTarget, emissions *[]layer.Emission) {
	if !link.pointToPoint || !link.sendRSTP || !agreement {
		p.agreed = false
		return
	}

	portVec := designatedVector(t, p, link.external)
	cmp := compareVectors(incoming, portVec)
	switch role {
	case bpdu.RoleDesignated:
		p.agreed = cmp <= 0
	case bpdu.RoleRoot, bpdu.RoleAlternate, bpdu.RoleBackup:
		p.agreed = cmp >= 0
	default:
		p.agreed = false
	}
	if p.agreed {
		p.proposing = false
		if p.role == bpdu.RoleDesignated && p.state != StateForwarding {
			p.state = StateForwarding
			p.forwardTransitions++
			if !link.edge {
				l.initiateTopologyChange(t, p, now, flushes, emissions)
			}
		}
	}
}

func (l *Layer) cistPortVector(p *portState) priorityVector {
	cist := l.cist()
	link := l.links[p.name]
	if p.role == bpdu.RoleDesignated {
		return designatedVector(cist, p, link.external)
	}
	return rawVector(cist, p, link.external)
}

func (l *Layer) receiveMSTIs(now time.Time, port string, b bpdu.BPDU, heldCISTVec priorityVector, flushes *[]layer.FlushTarget) []bpdu.MSTID {
	link := l.links[port]
	cistConsistent := b.RootID == heldCISTVec.rootID &&
		b.RootPathCost == heldCISTVec.externalRootPathCost &&
		b.RegionalRootID == heldCISTVec.regionalRootID

	var proposals []bpdu.MSTID

	for _, rec := range b.MSTIs {
		mt, ok := l.trees[treeID(rec.MSTID)]
		if !ok {
			continue
		}
		mp, ok := mt.ports[port]
		if !ok {
			continue
		}

		recFlags := bpdu.BPDU{Flags: rec.Flags}
		if recFlags.TopologyChange() && !mp.cfg.RestrictedTCN && mp.tcActive {
			l.propagateTopologyChange(mt, port, now, flushes, nil)
		}

		recBridgeID := bpdu.BridgeID{
			Priority: (uint16(rec.BridgePriority) << 8) | uint16(rec.MSTID),
			Address:  b.BridgeID.Address,
		}
		recPortID := (uint16(rec.PortPriority&0xF0) << 8) | (b.PortID & 0x0FFF)

		incoming := priorityVector{
			rootID: rec.RegionalRootID, regionalRootID: rec.RegionalRootID,
			internalRootPathCost: rec.InternalRootPathCost, bridgeID: recBridgeID, portID: recPortID,
		}

		sameSource := mp.rcvInfoValid && mp.rcvBridgeID == recBridgeID && mp.rcvPortID == recPortID
		isSuperior := !mp.rcvInfoValid
		if mp.rcvInfoValid {
			stored := rawVector(mt, mp, link.external)
			if compareVectors(incoming, stored) < 0 {
				isSuperior = true
			}
		}
		if !sameSource && !isSuperior {
			if cistConsistent {
				l.recordAgreement(mt, mp, link, incoming, recFlags.Role(), recFlags.Agreement(), now, flushes, nil)
			} else {
				mp.agreed = false
			}
			continue
		}

		if rec.RemainingHops <= 1 {
			continue
		}

		rcvHello := b.HelloTime
		if rcvHello < time.Second {
			rcvHello = time.Second
		}

		mp.rcvInfoValid = true
		mp.rcvRootID = rec.RegionalRootID
		mp.rcvRootPathCost = rec.InternalRootPathCost
		mp.rcvBridgeID = recBridgeID
		mp.rcvPortID = recPortID
		mp.rcvRemainingHops = rec.RemainingHops
		mp.rcvHelloTime = rcvHello
		mp.rcvTime = now

		if cistConsistent {
			l.recordAgreement(mt, mp, link, incoming, recFlags.Role(), recFlags.Agreement(), now, flushes, nil)
			if recFlags.Proposal() && recFlags.Role() == bpdu.RoleDesignated {
				proposals = append(proposals, rec.MSTID)
			}
		} else {
			mp.agreed = false
		}
	}

	return proposals
}

// Receive processes an incoming BPDU received on a port. It applies the BPDU
// to the CIST, which is the tree an IEEE-addressed BPDU belongs to in every
// mode: plain RSTP has only that tree, an MSTP bridge distributes its MSTI
// records from it, and a PVST bridge keeps VLAN 1's tree there. An SSTP BPDU
// goes to ReceiveSSTP instead, which is the only entry point that classifies
// a BPDU by VLAN.
func (l *Layer) Receive(now time.Time, port string, b bpdu.BPDU) layer.Effects {
	t := l.cist()
	p, ok := t.ports[port]
	link := l.links[port]
	if !ok || link == nil || !link.up {
		return layer.Effects{}
	}

	link.rxBPDUs++

	l.armHelloTimers(now)

	var flushes []layer.FlushTarget

	// An MST BPDU on a PVST bridge is the boundary this layer reports rather
	// than models: its RST prefix still drives VLAN 1's tree below, but no
	// other VLAN's tree hears anything from that neighbor.
	if l.pvst != nil && b.ConfigID != nil {
		link.pvstBoundary = true
	}

	emissions, done := l.receiveLink(now, port, b, &flushes)
	if done {
		return layer.Effects{Emissions: emissions, Flush: flushes}
	}

	if b.Type == bpdu.TypeTopologyChangeNotification {
		if p.tcActive {
			if p.role == bpdu.RoleDesignated {
				p.tcAck = true
				l.emit(t, p, now, emissionDesignated, &emissions)
			}
			if !p.cfg.RestrictedTCN {
				p.tcWhile = now.Add(l.tcWhileDuration(t, p))
				t.topologyChangeCount++
				t.lastTopologyChange = now
				l.propagateTopologyChange(t, p.name, now, &flushes, &emissions)
				for _, id := range l.treeOrder {
					mt := l.trees[id]
					if mt.id == cistID {
						continue
					}
					if mp := mt.ports[port]; mp != nil && mp.tcActive {
						mp.tcWhile = now.Add(l.tcWhileDuration(mt, mp))
						mt.topologyChangeCount++
						mt.lastTopologyChange = now
						l.propagateTopologyChange(mt, port, now, &flushes, &emissions)
					}
				}
			}
		}

		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{
			Emissions: emissions,
			Flush:     flushes,
		}
	}

	emissions = append(emissions, l.applyBPDU(t, p, now, b, &flushes)...)

	return layer.Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// SSTPArrival describes how the switch classified one SSTP BPDU before
// handing it to ReceiveSSTP. ArrivalVID is the VLAN the switch classified the
// frame into; TLVVID is the VLAN the BPDU's own trailing TLV names, which the
// PVID check compares against ArrivalVID. Admitted is the bridge's ingress
// admission answer for ArrivalVID on this port: the layer holds no VLAN
// table of its own, so it takes that answer as given rather than deriving a
// second one beside the bridge's.
type SSTPArrival struct {
	ArrivalVID vlan.ID
	TLVVID     vlan.ID
	Admitted   bool
}

// SSTPOutcome names what ReceiveSSTP did with one SSTP BPDU, beyond the
// Effects it returns alongside.
type SSTPOutcome string

const (
	// SSTPApplied means the BPDU was applied to the tree of
	// SSTPArrival.ArrivalVID.
	SSTPApplied SSTPOutcome = "applied"
	// SSTPGuarded means BPDU guard fired or already held the port disabled;
	// the frame was not applied to any tree.
	SSTPGuarded SSTPOutcome = "bpdu-guard"
	// SSTPBoundary means this bridge does not run PVST, so its CIST does not
	// run the VLAN the BPDU named; the port is marked a PVST boundary and
	// nothing is applied.
	SSTPBoundary SSTPOutcome = "pvst-boundary"
	// SSTPNotAdmitted means the bridge does not admit ArrivalVID on this
	// port; the frame was not applied to any tree.
	SSTPNotAdmitted SSTPOutcome = "vlan-not-admitted"
	// SSTPUntrackedVLAN means this bridge runs PVST but has no tree for
	// ArrivalVID; the frame was not applied to any tree.
	SSTPUntrackedVLAN SSTPOutcome = "vlan-untracked"
	// SSTPPVIDInconsistent means TLVVID disagreed with ArrivalVID; the
	// arrival VLAN's port is held discarding rather than applied.
	SSTPPVIDInconsistent SSTPOutcome = "pvid-inconsistent"
	// SSTPPortDown means the port is not one the layer tracks, or is held
	// down; the BPDU was not processed at all.
	SSTPPortDown SSTPOutcome = "port-down"
)

// ReceiveSSTP processes an SSTP BPDU received on a port. The link half of a
// receive — BPDU guard, the loop-guard clear, protocol migration, and
// auto-edge loss — always runs before anything below decides what happens to
// a tree, whatever that decision turns out to be: a caller that could skip
// the link half by declining to call this function is the hole BPDU guard
// exists to close. syncInstancePorts carries whatever the link half changed
// to every tree before the frame is judged, so the tree of
// arrival.ArrivalVID sees an up-to-date link even though the change was made
// on the CIST's port state. The returned SSTPOutcome describes the tree half
// alone: what, if anything, happened to arrival.ArrivalVID's own tree.
func (l *Layer) ReceiveSSTP(now time.Time, port string, arrival SSTPArrival, b bpdu.BPDU) (layer.Effects, SSTPOutcome) {
	link, ok := l.links[port]
	if !ok || !link.up {
		return layer.Effects{}, SSTPPortDown
	}

	link.rxBPDUs++

	var flushes []layer.FlushTarget

	// receiveLink is the link-level half of a receive: BPDU guard, the
	// loop-guard clear, protocol migration, and auto-edge loss all belong to
	// the port whatever tree the frame names, so it runs whatever this bridge
	// goes on to decide about the tree half below.
	emissions, done := l.receiveLink(now, port, b, &flushes)

	// The mark is a statement about the neighbor, not about this frame's
	// outcome, so it is set whenever this bridge does not run PVST even when
	// the guard above just fired.
	if l.pvst == nil {
		link.pvstBoundary = true
	}

	if done {
		return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPGuarded
	}

	l.armHelloTimers(now)

	if l.pvst == nil {
		// This bridge's CIST does not run the BPDU's VLAN, so feeding the
		// vector into it would elect a root from a tree it is not running.
		// The neighbor relationship still converges, because a PVST+ bridge
		// sends VLAN 1's tree to the IEEE address as well.
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPBoundary
	}

	if !arrival.Admitted {
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPNotAdmitted
	}

	t, ok := l.treeFor(arrival.ArrivalVID)
	if !ok {
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPUntrackedVLAN
	}

	p, ok := t.ports[port]
	if !ok {
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPUntrackedVLAN
	}

	// Cisco blocks the traffic of the VLAN the frame arrived on, not of the
	// VLAN the peer named: the arrival VLAN is the one whose local traffic
	// would cross a link the two ends disagree about.
	if arrival.TLVVID != arrival.ArrivalVID {
		p.pvidInconsistent = true
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPPVIDInconsistent
	}
	p.pvidInconsistent = false

	emissions = append(emissions, l.applyBPDU(t, p, now, b, &flushes)...)

	return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPApplied
}

// applyBPDU applies one received BPDU to one tree: the classification, the
// information it carries, the agreement and topology-change flags it sets,
// and the proposal handshake it answers. Receive runs it on the CIST, which
// is where an IEEE-addressed BPDU belongs in every mode; ReceiveSSTP runs it
// on the tree of the VLAN an SSTP BPDU arrived on. The link-level half of a
// receive, which runs once per frame whatever tree it belongs to, stays with
// the two callers.
func (l *Layer) applyBPDU(t *tree, p *portState, now time.Time, b bpdu.BPDU, flushes *[]layer.FlushTarget) []layer.Emission {
	var emissions []layer.Emission
	link := l.links[p.name]

	// A BPDU is internal when it names this bridge's own region: an MST BPDU
	// (ConfigID set) whose configuration identifier equals this bridge's. An
	// RST or Configuration BPDU, and an MST BPDU from a different region, are
	// external. The classification is written only on the CIST's port state
	// because it is a property of the link, not of a tree running over it;
	// boundary reads it through l.cist() regardless of which tree's applyBPDU
	// call observed the frame.
	internal := l.mst != nil && b.ConfigID != nil && *b.ConfigID == *l.configID

	if internal {
		// Internal information ages by hop count, re-originated one hop
		// short of what was received; a record that has already reached the
		// bound is discarded rather than stored, so a BPDU naming a regional
		// root that no longer exists stops refreshing on every hop and the
		// port's own information ages out.
		if b.RemainingHops <= 1 {
			emissions = append(emissions, l.recomputeAll(now, flushes)...)

			return emissions
		}
	} else {
		// IEEE 802.1Q treats message age as a hop count bounded by the max age
		// the BPDU itself carries, not by this bridge's configured one: the
		// received value is the root's, and the fabric builds bridges with
		// differing timers. Information that has reached the bound is
		// discarded rather than stored, so a BPDU naming a root that no
		// longer exists stops refreshing the timer on every hop and the
		// port's own information ages out.
		if b.MessageAge+time.Second > b.MaxAge {
			emissions = append(emissions, l.recomputeAll(now, flushes)...)

			return emissions
		}
	}

	// Built in the same shape rawVector gives the stored vector it is compared
	// against: a non-CIST tree carries its cost in internalRootPathCost, not
	// externalRootPathCost, and a vector built with the cost in the wrong slot
	// compares against a different component than the one it belongs next to.
	incoming := priorityVector{rootID: b.RootID, bridgeID: b.BridgeID, portID: b.PortID}
	switch {
	case t.id == cistID && internal:
		incoming.externalRootPathCost = b.RootPathCost
		incoming.regionalRootID = b.RegionalRootID
		incoming.internalRootPathCost = b.InternalRootPathCost
	case t.id == cistID:
		incoming.externalRootPathCost = b.RootPathCost
		incoming.regionalRootID = b.RootID
	default:
		incoming.regionalRootID = b.RootID
		incoming.internalRootPathCost = b.RootPathCost
	}

	sameSource := p.rcvInfoValid && (b.BridgeID == p.rcvBridgeID && b.PortID == p.rcvPortID)
	isSuperior := false
	if !p.rcvInfoValid {
		isSuperior = true
	} else {
		stored := rawVector(t, p, link.external)
		if compareVectors(incoming, stored) < 0 {
			isSuperior = true
		}
	}

	heldCISTVec := l.cistPortVector(p)

	if sameSource || isSuperior {
		// The classification updates only when received information is
		// stored (IEEE 802.1Q clause 13.24.10). Assigning it earlier or
		// on an inferior BPDU would rewrite how stored vectors are read.
		// It is written only for the CIST's own call.
		if t.id == cistID {
			link.external = !internal
		}
		p.loopInconsistent = false
		l.recordReceivedBPDU(p, b, internal, now)
	}

	var mstiProposals []bpdu.MSTID
	if internal {
		mstiProposals = l.receiveMSTIs(now, p.name, b, heldCISTVec, flushes)
	}

	l.recordAgreement(t, p, link, incoming, b.Role(), b.Agreement(), now, flushes, &emissions)
	if l.mst != nil && link.external {
		for _, id := range l.treeOrder {
			mt := l.trees[id]
			if mt.id == cistID {
				continue
			}
			if mp, ok := mt.ports[p.name]; ok {
				mp.agreed = p.agreed
				if mp.agreed {
					mp.proposing = false
					if mp.role == bpdu.RoleDesignated && mp.state != StateForwarding {
						mp.state = StateForwarding
						mp.forwardTransitions++
						if !link.edge {
							l.initiateTopologyChange(mt, mp, now, flushes, &emissions)
						}
					}
				}
			}
		}
	}

	l.propagateReceivedTC(t, p, link, b, now, flushes, &emissions)

	emissions = append(emissions, l.recomputeAll(now, flushes)...)

	if l.answerProposals(t, p, link, b, mstiProposals, now, flushes) {
		l.emit(t, p, now, emissionAgreement, &emissions)
	} else if p.role == bpdu.RoleDesignated && !b.Agreement() {
		if compareVectors(incoming, designatedVector(t, p, link.external)) > 0 {
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return emissions
}

func (l *Layer) propagateReceivedTC(t *tree, p *portState, link *linkRecord, b bpdu.BPDU, now time.Time, flushes *[]layer.FlushTarget, emissions *[]layer.Emission) {
	if b.TopologyChangeAck() && p.role == bpdu.RoleRoot {
		p.tcWhile = time.Time{}
	}
	if b.TopologyChange() && !p.cfg.RestrictedTCN && p.tcActive {
		l.propagateTopologyChange(t, p.name, now, flushes, emissions)
		if l.mst != nil && link.external {
			for _, id := range l.treeOrder {
				mt := l.trees[id]
				if mt.id == cistID {
					continue
				}
				if mp := mt.ports[p.name]; mp != nil && mp.tcActive {
					l.propagateTopologyChange(mt, p.name, now, flushes, emissions)
				}
			}
		}
	}
}

func (l *Layer) recordReceivedBPDU(p *portState, b bpdu.BPDU, internal bool, now time.Time) {
	p.rcvInfoValid = true
	p.rcvRootID = b.RootID
	p.rcvRootPathCost = b.RootPathCost
	p.rcvBridgeID = b.BridgeID
	p.rcvPortID = b.PortID
	p.rcvMessageAge = b.MessageAge
	p.rcvMaxAge = b.MaxAge
	rcvHello := b.HelloTime
	if rcvHello < time.Second {
		rcvHello = time.Second
	}
	p.rcvHelloTime = rcvHello
	p.rcvForwardDelay = b.ForwardDelay
	p.rcvTime = now
	if internal {
		p.rcvRegionalRootID = b.RegionalRootID
		p.rcvInternalRootPathCost = b.InternalRootPathCost
		p.rcvRemainingHops = b.RemainingHops
	} else {
		// A port classified external carries no internal-only state: a
		// stale regional root, internal cost, or hop count left over from
		// an earlier internal BPDU would otherwise survive the flip and
		// this bridge would re-originate a decreasing hop count instead
		// of MaxHops.
		p.rcvRegionalRootID = bpdu.BridgeID{}
		p.rcvInternalRootPathCost = 0
		p.rcvRemainingHops = 0
	}
}

func (l *Layer) answerProposals(t *tree, p *portState, link *linkRecord, b bpdu.BPDU, mstiProposals []bpdu.MSTID, now time.Time, flushes *[]layer.FlushTarget) bool {
	answered := false
	if b.Proposal() && b.Role() == bpdu.RoleDesignated && (p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate) {
		answered = true
		l.handleProposal(t, p, link, now, flushes)
		if l.mst != nil && link.external {
			for _, id := range l.treeOrder {
				mt := l.trees[id]
				if mt.id == cistID {
					continue
				}
				if mp, ok := mt.ports[p.name]; ok && (mp.role == bpdu.RoleRoot || mp.role == bpdu.RoleAlternate) {
					l.handleProposal(mt, mp, link, now, flushes)
				}
			}
		}
	}
	for _, mstid := range mstiProposals {
		if mt, ok := l.trees[treeID(mstid)]; ok {
			if mp, ok := mt.ports[p.name]; ok && (mp.role == bpdu.RoleRoot || mp.role == bpdu.RoleAlternate) {
				l.handleProposal(mt, mp, link, now, flushes)
				answered = true
			}
		}
	}
	return answered
}
