package stp

import (
	"cmp"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
)

// priorityVector is the six-component spanning tree priority vector IEEE
// 802.1Q compares to elect roots and designated ports. An RSTP tree, and the
// CIST on a boundary port, set regionalRootID from rootID and leave
// externalRootPathCost at zero, which collapses the comparison to the
// four-component RSTP order (root, cost, bridge, port) with the cost living
// in the external slot. The CIST on an internal port populates all six
// components instead, comparing regional root and internal cost ahead of
// bridge and port the way clause 13.11 requires within a region. An MSTI
// sets rootID from regionalRootID and leaves the external cost at zero,
// which collapses it to clause 13.11's four-component MSTI order. None of
// this needs a branch in compareVectors itself: the constant or shared
// leading components are lexicographically neutral.
type priorityVector struct {
	rootID               bpdu.BridgeID
	externalRootPathCost uint32
	regionalRootID       bpdu.BridgeID
	internalRootPathCost uint32
	bridgeID             bpdu.BridgeID
	portID               uint16
}

func compareVectors(a, b priorityVector) int {
	if a.rootID.Less(b.rootID) {
		return -1
	}
	if b.rootID.Less(a.rootID) {
		return 1
	}
	if a.externalRootPathCost != b.externalRootPathCost {
		return cmp.Compare(a.externalRootPathCost, b.externalRootPathCost)
	}
	if a.regionalRootID.Less(b.regionalRootID) {
		return -1
	}
	if b.regionalRootID.Less(a.regionalRootID) {
		return 1
	}
	if a.internalRootPathCost != b.internalRootPathCost {
		return cmp.Compare(a.internalRootPathCost, b.internalRootPathCost)
	}
	if a.bridgeID.Less(b.bridgeID) {
		return -1
	}
	if b.bridgeID.Less(a.bridgeID) {
		return 1
	}

	return cmp.Compare(a.portID, b.portID)
}

// candidateVector builds the priority vector port p offers tree t towards
// root election, from its received information and its own path cost. A CIST
// port adds the cost to the external or the internal slot depending on
// whether it is a boundary port; an MSTI port always adds it to the internal
// slot and mirrors rootID from regionalRootID, which is clause 13.11's MSTI
// vector order (see priorityVector).
func candidateVector(t *tree, p *portState) priorityVector {
	cand := priorityVector{
		rootID:   p.rcvRootID,
		bridgeID: p.rcvBridgeID,
		portID:   p.rcvPortID,
	}

	switch {
	case t.id == cistID && p.external:
		// A bridge whose CIST root port is a boundary port terminates the
		// region for this vector: it IS the CIST regional root here, not a
		// name borrowed from the peer's region, so the regional root mirrors
		// this tree's own bridge identifier and the internal cost stays zero.
		cand.externalRootPathCost = p.rcvRootPathCost + p.pathCost
		cand.regionalRootID = t.bridgeID
	case t.id == cistID:
		cand.externalRootPathCost = p.rcvRootPathCost
		cand.regionalRootID = p.rcvRegionalRootID
		cand.internalRootPathCost = p.rcvInternalRootPathCost + p.pathCost
	default:
		cand.regionalRootID = p.rcvRootID
		cand.internalRootPathCost = p.rcvRootPathCost + p.pathCost
	}

	return cand
}

// rawVector builds the priority vector port p received, in the same shape as
// candidateVector but without adding p's own path cost: designatedOrBlocked
// compares what the peer is claiming for the segment against what this
// bridge would claim, and neither side's own link cost belongs in that
// comparison.
func rawVector(t *tree, p *portState) priorityVector {
	switch {
	case t.id == cistID && p.external:
		return priorityVector{
			rootID: p.rcvRootID, externalRootPathCost: p.rcvRootPathCost,
			regionalRootID: p.rcvRootID, bridgeID: p.rcvBridgeID, portID: p.rcvPortID,
		}
	case t.id == cistID:
		return priorityVector{
			rootID: p.rcvRootID, externalRootPathCost: p.rcvRootPathCost,
			regionalRootID: p.rcvRegionalRootID, internalRootPathCost: p.rcvInternalRootPathCost,
			bridgeID: p.rcvBridgeID, portID: p.rcvPortID,
		}
	default:
		return priorityVector{
			rootID: p.rcvRootID, regionalRootID: p.rcvRootID, internalRootPathCost: p.rcvRootPathCost,
			bridgeID: p.rcvBridgeID, portID: p.rcvPortID,
		}
	}
}

// designatedVector builds the priority vector tree t itself offers on port p
// once its root is elected, in the same shape rawVector gives a received one,
// so the two compare directly.
func designatedVector(t *tree, p *portState) priorityVector {
	switch {
	case t.id == cistID && p.external:
		return priorityVector{
			rootID: t.rootID, externalRootPathCost: t.rootPathCost,
			regionalRootID: t.rootID, bridgeID: t.bridgeID, portID: p.portID,
		}
	case t.id == cistID:
		return priorityVector{
			rootID: t.rootID, externalRootPathCost: t.rootPathCost,
			regionalRootID: t.regionalRootID, internalRootPathCost: t.internalRootPathCost,
			bridgeID: t.bridgeID, portID: p.portID,
		}
	default:
		return priorityVector{
			rootID: t.rootID, regionalRootID: t.rootID, internalRootPathCost: t.rootPathCost,
			bridgeID: t.bridgeID, portID: p.portID,
		}
	}
}
