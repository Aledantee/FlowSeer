package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// treeID identifies one spanning tree within the bridge. Rapid spanning tree
// has a single tree, so CIST is the only value constructed; MSTP gives the
// identifier the MSTID's range, and PVST the VLAN's. The two never share a
// Layer, so the ranges cannot collide.
type treeID uint16

// cistID is the Common and Internal Spanning Tree, the tree every VLAN maps to
// while the bridge runs one tree. In PVST mode it is VLAN 1's tree, which is
// the common tree a neighboring RSTP or MSTP bridge converges with.
const cistID treeID = 0

// tree holds the state one spanning tree computes over the bridge's ports: its
// elected root, the timers and counters that belong to the computation, and the
// per-port role, state, and received information.
//
// The port identifier and the port key set are deliberately not here. Both are
// bridge-global: the identifier appears on the wire and in PortInfo, and the key
// set fixes the iteration order that reaches the caller as Effects.Flush.
type tree struct {
	id treeID

	// vid is the VLAN this tree runs for in PVST mode, and zero in every
	// other mode. It rides on the tree because the VLAN is what an SSTP BPDU
	// carries in its trailing TLV, so emission needs it without a reverse
	// lookup through vidToTree.
	vid vlan.ID

	// bridgeID is this bridge's identifier for the tree. Outside PVST mode
	// the CIST's is the layer's own bridgeID; an MSTI can carry a different
	// one, and so can the CIST in PVST mode, where it carries VLAN 1 in the
	// system-ID extension.
	bridgeID BridgeID

	rootID       BridgeID
	rootPathCost uint32
	rootPort     string

	// regionalRootID and internalRootPathCost are the CIST's region-internal
	// components: the regional root within this bridge's MST region and the
	// internal cost to reach it. An MSTI tree leaves them zero, since its own
	// rootID and rootPathCost already carry the regional root and the cost to
	// it (clause 13.11's MSTI vector has no external component to keep them
	// apart from).
	regionalRootID       BridgeID
	internalRootPathCost uint32

	helloTimer time.Time

	topologyChangeCount uint64
	lastTopologyChange  time.Time
	topologyChangeTimer time.Time

	ports map[string]*portState
}

func (t *tree) clone() *tree {
	cp := *t
	cp.ports = make(map[string]*portState, len(t.ports))
	for k, v := range t.ports {
		cp.ports[k] = v.clone()
	}

	return &cp
}

// cist returns the Common and Internal Spanning Tree, which every bridge runs.
func (l *Layer) cist() *tree {
	return l.trees[cistID]
}

// treeFor returns the tree that carries the given VLAN, and whether one does.
// Outside PVST mode every VLAN maps to the CIST, since the bridge runs one
// tree for all of them, so the answer is always (CIST, true). Inside PVST
// mode the CIST is VLAN 1's tree and nothing else's: a VLAN with no entry in
// vidToTree has no tree on this bridge, and treeFor says so rather than
// answering with VLAN 1's, which a caller could otherwise mistake for that
// VLAN's own state.
func (l *Layer) treeFor(vid vlan.ID) (*tree, bool) {
	if id, ok := l.vidToTree[vid]; ok {
		return l.trees[id], true
	}

	if l.pvst != nil {
		return nil, false
	}

	return l.cist(), true
}
