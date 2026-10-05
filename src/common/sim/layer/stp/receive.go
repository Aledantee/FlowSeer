package stp

import (
	"slices"
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

// minReceivedHelloTime is the shortest Hello Time the layer ages received
// information by. P802.1aq/D1.5 13.29.21 raises a smaller one to the minimum
// of IEEE 802.1D Table 17-1, which was not read, so this is 1 second, the
// lowest value UNH-IOL RSTP.op.4.3 Part B sends as valid.
const minReceivedHelloTime = time.Second

// receivedHelloTime raises a received Hello Time below the minimum to the
// minimum, so a BPDU that announces none does not expire the moment it
// arrives.
func receivedHelloTime(d time.Duration) time.Duration {
	return max(d, minReceivedHelloTime)
}

// receiveMSTIs stores the MSTI records an internal BPDU carries into each
// named instance's port state, one instance at a time by the same
// same-source-or-superior rule the CIST uses, and returns the instances whose
// record sets the topology change bit. The bit is reported unconditionally, not
// gated on superiority, since a change notification is evidence about the
// fabric rather than a claim this port might reject. A
// record for an instance this bridge does not configure is ignored: the
// fabric's bridges are not required to share the same instance set. The
// designated bridge and port a record implies reuse the sending bridge's own
// address and the low 12 bits of the CIST port identifier, since MSTI bridge and
// port identifiers differ from the CIST's only in their priority nibble
// (clause 13.7). Each record also records its tree's agreement the way the
// CIST does, when the CIST message of the same BPDU names the vectors the
// port holds (13.26.10).
func (l *Layer) receiveMSTIs(now time.Time, port string, b bpdu.BPDU) (flagged []treeID) {
	lk := l.link(port)
	held := l.cistHolds(port, b)

	for _, rec := range b.MSTIs {
		mt, ok := l.trees[treeID(rec.MSTID)]
		if !ok {
			continue
		}
		mp, ok := mt.ports[port]
		if !ok {
			continue
		}

		if (bpdu.BPDU{Flags: rec.Flags}).TopologyChange() {
			flagged = append(flagged, mt.id)
		}

		recBridgeID := bpdu.BridgeID{
			Priority: (uint16(rec.BridgePriority&0xF0) << 8) | uint16(rec.MSTID),
			Address:  b.BridgeID.Address,
		}
		recPortID := (uint16(rec.PortPriority&0xF0) << 8) | (b.PortID & 0x0FFF)

		incoming := priorityVector{
			rootID: rec.RegionalRootID, regionalRootID: rec.RegionalRootID,
			internalRootPathCost: rec.InternalRootPathCost, bridgeID: recBridgeID, portID: recPortID,
		}

		if rec.RemainingHops <= 1 {
			continue
		}

		flags := bpdu.BPDU{Flags: rec.Flags}
		recordAgreement(mt, mp, lk, treeMessage{
			role: flags.Role(), agreement: flags.Agreement() && held, vector: incoming,
		})

		sameSource := mp.rcvInfoValid && mp.rcvBridgeID == recBridgeID && mp.rcvPortID == recPortID
		isSuperior := !mp.rcvInfoValid
		if mp.rcvInfoValid {
			stored := rawVector(mt, mp, lk.external)
			if compareVectors(incoming, stored) < 0 {
				isSuperior = true
			}
		}
		if !sameSource && !isSuperior {
			continue
		}

		mp.rcvInfoValid = true
		mp.rcvRootID = rec.RegionalRootID
		mp.rcvRootPathCost = rec.InternalRootPathCost
		mp.rcvBridgeID = recBridgeID
		mp.rcvPortID = recPortID
		mp.rcvRemainingHops = rec.RemainingHops
		mp.rcvHelloTime = receivedHelloTime(b.HelloTime)
		mp.rcvTime = now
	}

	return flagged
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
		// A TCN counts for the CIST and every MSTI (13.26.19). Roles settle
		// first, since only a port that is active after them acts on it.
		trees := []treeID{cistID}
		if l.mst != nil {
			trees = slices.Clone(l.treeOrder)
		}
		emissions = append(emissions, l.recomputeTrees(now, &flushes)...)
		l.applyTopologyChange(now, port, topologyNotice{trees: trees, tcn: true}, &flushes)
		l.drainTopology(now, &emissions)

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
	// A BPDU is internal when it names this bridge's own region: an MST BPDU
	// (ConfigID set) whose configuration identifier equals this bridge's. An
	// RST or Configuration BPDU, and an MST BPDU from a different region, are
	// external. The classification is written only on the CIST's port state
	// because it is a property of the link, not of a tree running over it;
	// boundary reads it from the link record regardless of which tree's
	// applyBPDU call observed the frame.
	internal := l.mst != nil && b.ConfigID != nil && *b.ConfigID == *l.configID
	lk := l.link(p.name)

	if exhausted(b, internal) {
		if t.id == cistID {
			lk.external = !internal
		}

		return l.recomputeAll(now, flushes)
	}

	incoming := incomingVector(t, internal, b)
	l.storeInformation(t, p, lk, incoming, internal, b, now)

	recordAgreement(t, p, lk, treeMessage{role: b.Role(), agreement: b.Agreement(), vector: incoming})
	var flagged []treeID
	switch {
	case internal:
		flagged = l.receiveMSTIs(now, p.name, b)
	case l.mst != nil && t.id == cistID:
		l.mirrorAgreement(p)
	}

	// The topology change facts are acted on after the roles settle, since a
	// port ignores them unless it is active, and before the answer below, so
	// the answer carries the flag a started timer sets.
	emissions := l.recomputeTrees(now, flushes)
	l.applyTopologyChange(now, p.name, l.receivedTopology(t, internal, b, flagged), flushes)

	if l.answerProposals(t, p, now, b, internal, flushes) {
		l.emit(t, p, now, emissionAgreement, &emissions)
	} else if p.role == bpdu.RoleDesignated && !b.Agreement() &&
		compareVectors(incoming, designatedVector(t, p, lk.external)) > 0 {
		l.emit(t, p, now, emissionDesignated, &emissions)
	}
	l.drainTopology(now, &emissions)

	return emissions
}

// exhausted reports whether the information b carries has reached its bound
// and must be discarded rather than stored, so that a BPDU naming a root that
// no longer exists stops refreshing the timer on every hop and the port's own
// information ages out. Internal information ages by hop count, re-originated
// one hop short of what was received. IEEE 802.1Q treats message age as a hop
// count bounded by the max age the BPDU itself carries, not by this bridge's
// configured one: the received value is the root's, and the fabric builds
// bridges with differing timers.
func exhausted(b bpdu.BPDU, internal bool) bool {
	if internal {
		return b.RemainingHops <= 1
	}

	return b.MessageAge+time.Second > b.MaxAge
}

// incomingVector builds the vector b conveys for tree t in the same shape
// rawVector gives the stored vector it is compared against: a non-CIST tree
// carries its cost in internalRootPathCost, not externalRootPathCost, and a
// vector built with the cost in the wrong slot compares against a different
// component than the one it belongs next to.
func incomingVector(t *tree, internal bool, b bpdu.BPDU) priorityVector {
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

	return incoming
}

// storeInformation keeps what b conveys on port p of tree t when it comes
// from the source the port already hears or is better than what it holds.
func (l *Layer) storeInformation(t *tree, p *portState, lk *linkRecord, incoming priorityVector, internal bool, b bpdu.BPDU, now time.Time) {
	sameSource := p.rcvInfoValid && (b.BridgeID == p.rcvBridgeID && b.PortID == p.rcvPortID)
	isSuperior := !p.rcvInfoValid || compareVectors(incoming, rawVector(t, p, lk.external)) < 0

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

	if !sameSource && !isSuperior {
		return
	}

	p.rcvInfoValid = true
	p.rcvRootID = b.RootID
	p.rcvRootPathCost = b.RootPathCost
	p.rcvBridgeID = b.BridgeID
	p.rcvPortID = b.PortID
	p.rcvMessageAge = b.MessageAge
	p.rcvMaxAge = b.MaxAge
	p.rcvHelloTime = receivedHelloTime(b.HelloTime)
	p.rcvForwardDelay = b.ForwardDelay
	p.rcvTime = now
	if internal {
		p.rcvRegionalRootID = b.RegionalRootID
		p.rcvInternalRootPathCost = b.InternalRootPathCost
		p.rcvRemainingHops = b.RemainingHops

		return
	}

	// A port classified external carries no internal-only state: a stale
	// regional root, internal cost, or hop count left over from an earlier
	// internal BPDU would otherwise survive the flip and this bridge would
	// re-originate a decreasing hop count instead of MaxHops.
	p.rcvRegionalRootID = bpdu.BridgeID{}
	p.rcvInternalRootPathCost = 0
	p.rcvRemainingHops = 0
}
