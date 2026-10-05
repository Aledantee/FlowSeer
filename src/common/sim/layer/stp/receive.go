package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

const (
	// BlockReasonBPDUGuard marks a port BPDU guard disabled because a BPDU
	// arrived on it. Only a link down and up clears it.
	BlockReasonBPDUGuard BlockReason = "bpdu-guard"

	// BlockReasonPVIDInconsistent marks a port whose peer disagrees about
	// which VLAN a link carries: an SSTP BPDU arrived naming a VLAN other
	// than the one the switch classified the frame into. The next consistent
	// BPDU on the arrival VLAN clears it.
	BlockReasonPVIDInconsistent BlockReason = "pvid-inconsistent"

	// BlockReasonLoopInconsistent marks a port whose received information
	// expired while it held a non-designated role, which loop guard keeps
	// discarding rather than letting it open a loop. The next BPDU clears it.
	BlockReasonLoopInconsistent BlockReason = "loop-inconsistent"
)

// BadBPDU records that a frame received on the named port could not be
// decoded as a BPDU. The count belongs to the link, so every tree's PortInfo
// answers from the same record. An untracked port is ignored.
func (l *Layer) BadBPDU(port string) {
	if lk := l.link(port); lk != nil {
		lk.badBPDUs++
	}
}

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
func (l *Layer) receiveMSTIs(now time.Time, port string, b bpdu.BPDU, flushes *[]layer.FlushTarget) {
	for _, rec := range b.MSTIs {
		mt, ok := l.trees[treeID(rec.MSTID)]
		if !ok {
			continue
		}
		mp, ok := mt.ports[port]
		if !ok {
			continue
		}

		if (bpdu.BPDU{Flags: rec.Flags}).TopologyChange() && !mp.cfg.RestrictedTCN {
			mt.topologyChangeTimer = now.Add(l.helloTime + time.Second)
			fids := l.treeVLANs[mt.id]
			for _, name := range l.portNames {
				if name != port {
					mergeFlushTarget(flushes, name, fids)
				}
			}
		}

		recBridgeID := bpdu.BridgeID{
			Priority: (uint16(rec.BridgePriority) << 12) | uint16(rec.MSTID),
			Address:  b.BridgeID.Address,
		}
		recPortID := (uint16(rec.PortPriority) << 8) | (b.PortID & 0x00FF)

		incoming := priorityVector{
			rootID: rec.RegionalRootID, regionalRootID: rec.RegionalRootID,
			internalRootPathCost: rec.InternalRootPathCost, bridgeID: recBridgeID, portID: recPortID,
		}

		sameSource := mp.rcvInfoValid && mp.rcvBridgeID == recBridgeID && mp.rcvPortID == recPortID
		isSuperior := !mp.rcvInfoValid
		if mp.rcvInfoValid {
			stored := rawVector(mt, mp, l.link(port).external)
			if compareVectors(incoming, stored) < 0 {
				isSuperior = true
			}
		}
		if !sameSource && !isSuperior {
			continue
		}

		if rec.RemainingHops <= 1 {
			continue
		}

		mp.rcvInfoValid = true
		mp.rcvRootID = rec.RegionalRootID
		mp.rcvRootPathCost = rec.InternalRootPathCost
		mp.rcvBridgeID = recBridgeID
		mp.rcvPortID = recPortID
		mp.rcvRemainingHops = rec.RemainingHops
		mp.rcvHelloTime = b.HelloTime
		mp.rcvTime = now
	}
}

// Receive processes an incoming BPDU received on a port. It applies the BPDU
// to the CIST, which is the tree an IEEE-addressed BPDU belongs to in every
// mode: plain RSTP has only that tree, an MSTP bridge distributes its MSTI
// records from it, and a PVST bridge keeps VLAN 1's tree there. An SSTP BPDU
// goes to ReceiveSSTP instead, which is the only entry point that classifies
// a BPDU by VLAN.
func (l *Layer) Receive(now time.Time, port string, b bpdu.BPDU) layer.Effects {
	t := l.cist()
	lk := l.link(port)
	if lk == nil || !lk.up {
		return layer.Effects{}
	}

	p := t.ports[port]
	lk.rxBPDUs++

	l.armHelloTimers(now)

	var flushes []layer.FlushTarget

	// An MST BPDU on a PVST bridge is the boundary this layer reports rather
	// than models: its RST prefix still drives VLAN 1's tree below, but no
	// other VLAN's tree hears anything from that neighbor.
	if l.pvst != nil && b.ConfigID != nil {
		lk.pvstBoundary = true
	}

	emissions, done := l.receiveLink(now, p, b, &flushes)
	if done {
		return layer.Effects{Emissions: emissions, Flush: flushes}
	}

	if b.Type == bpdu.TypeTopologyChangeNotification {
		// Restricted TCN stops the change here. Setting the timer would carry
		// the flag out on this bridge's own BPDUs, which is the propagation the
		// guard denies, so neither the timer nor the flush runs.
		if !p.cfg.RestrictedTCN {
			t.topologyChangeTimer = now.Add(l.helloTime + time.Second)
			for _, name := range l.portNames {
				if name != port {
					mergeFlushTarget(&flushes, name, l.treeVLANs[t.id])
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
// exists to close. Every return after the link half recomputes roles, because
// the link half can change what every tree's role depends on (the loop-guard
// clear above all) whether or not the frame reaches a tree. The returned
// SSTPOutcome describes the tree half alone: what, if anything, happened to
// arrival.ArrivalVID's own tree.
func (l *Layer) ReceiveSSTP(now time.Time, port string, arrival SSTPArrival, b bpdu.BPDU) (layer.Effects, SSTPOutcome) {
	lk := l.link(port)
	if lk == nil || !lk.up {
		return layer.Effects{}, SSTPPortDown
	}

	cistP := l.cist().ports[port]
	lk.rxBPDUs++

	var flushes []layer.FlushTarget

	// receiveLink is the link-level half of a receive: BPDU guard, the
	// loop-guard clear, protocol migration, and auto-edge loss all belong to
	// the port whatever tree the frame names, so it runs whatever this bridge
	// goes on to decide about the tree half below.
	emissions, done := l.receiveLink(now, cistP, b, &flushes)

	// The mark is a statement about the neighbor, not about this frame's
	// outcome, so it is set whenever this bridge does not run PVST even when
	// the guard above just fired.
	if l.pvst == nil {
		lk.pvstBoundary = true
	}

	if done {
		return layer.Effects{Emissions: emissions, Flush: flushes}, SSTPGuarded
	}

	l.armHelloTimers(now)

	// untouched ends a receive that applies the BPDU to no tree: the link
	// half may still have moved a mark roles depend on.
	untouched := func(outcome SSTPOutcome) (layer.Effects, SSTPOutcome) {
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return layer.Effects{Emissions: emissions, Flush: flushes}, outcome
	}

	if l.pvst == nil {
		// This bridge's CIST does not run the BPDU's VLAN, so feeding the
		// vector into it would elect a root from a tree it is not running.
		// The neighbor relationship still converges, because a PVST+ bridge
		// sends VLAN 1's tree to the IEEE address as well.
		return untouched(SSTPBoundary)
	}

	if !arrival.Admitted {
		return untouched(SSTPNotAdmitted)
	}

	t, ok := l.treeFor(arrival.ArrivalVID)
	if !ok {
		return untouched(SSTPUntrackedVLAN)
	}

	p, ok := t.ports[port]
	if !ok {
		return untouched(SSTPUntrackedVLAN)
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

	// A BPDU is internal when it names this bridge's own region: an MST BPDU
	// (ConfigID set) whose configuration identifier equals this bridge's. An
	// RST or Configuration BPDU, and an MST BPDU from a different region, are
	// external. The classification is written only on the CIST's port state
	// because it is a property of the link, not of a tree running over it;
	// boundary reads it from the link record regardless of which tree's
	// applyBPDU call observed the frame.
	internal := l.mst != nil && b.ConfigID != nil && *b.ConfigID == *l.configID
	lk := l.link(p.name)

	if internal {
		// Internal information ages by hop count, re-originated one hop
		// short of what was received; a record that has already reached the
		// bound is discarded rather than stored, so a BPDU naming a regional
		// root that no longer exists stops refreshing on every hop and the
		// port's own information ages out.
		if b.RemainingHops <= 1 {
			if t.id == cistID {
				lk.external = !internal
			}
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
			if t.id == cistID {
				lk.external = !internal
			}
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
		stored := rawVector(t, p, lk.external)
		if compareVectors(incoming, stored) < 0 {
			isSuperior = true
		}
	}

	// The classification updates only now, after the stored vector above was
	// built against what the port currently holds under its old
	// classification. Assigning it earlier would compare that stored
	// information as though it already carried this BPDU's classification,
	// which can invert the superiority verdict for the one BPDU that flips
	// internal to external or back. It is written only for the CIST's own
	// call: a non-CIST tree's applyBPDU (PVST, an SSTP arrival on a VLAN
	// other than 1) would otherwise leave a copy on a port boundary never
	// reads.
	if t.id == cistID {
		lk.external = !internal
	}

	if sameSource || isSuperior {
		p.rcvInfoValid = true
		p.rcvRootID = b.RootID
		p.rcvRootPathCost = b.RootPathCost
		p.rcvBridgeID = b.BridgeID
		p.rcvPortID = b.PortID
		p.rcvMessageAge = b.MessageAge
		p.rcvMaxAge = b.MaxAge
		p.rcvHelloTime = b.HelloTime
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

	if internal {
		l.receiveMSTIs(now, p.name, b, flushes)
	}

	if p.role == bpdu.RoleDesignated && b.Agreement() {
		p.agreed = true
		p.proposing = false
		if p.state != StateForwarding {
			p.state = StateForwarding
			p.forwardTransitions++
			if !lk.edge {
				l.raiseTopologyChange(t, p.name, now, flushes)
			}
		}
	}

	if b.TopologyChange() && !p.cfg.RestrictedTCN {
		t.topologyChangeTimer = now.Add(l.helloTime + time.Second)
		for _, name := range l.portNames {
			if name != p.name {
				mergeFlushTarget(flushes, name, l.treeVLANs[t.id])
			}
		}
	}

	emissions = append(emissions, l.recomputeAll(now, flushes)...)

	if b.Proposal() && (p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate) {
		for _, otherName := range l.portNames {
			if otherName == p.name {
				continue
			}
			otherP := t.ports[otherName]
			otherLk := l.link(otherName)
			if otherP.role == bpdu.RoleDesignated && !otherLk.edge {
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
		}

		if p.role == bpdu.RoleRoot && lk.pointToPoint && l.isSynced(t, p.name) && lk.sendRSTP {
			if p.state != StateForwarding {
				p.state = StateForwarding
				p.forwardTransitions++
				if !lk.edge {
					l.raiseTopologyChange(t, p.name, now, flushes)
				}
			}
		}

		l.emit(t, p, now, emissionAgreement, &emissions)
	} else if p.role == bpdu.RoleDesignated && !b.Agreement() {
		if compareVectors(incoming, designatedVector(t, p, lk.external)) > 0 {
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return emissions
}
