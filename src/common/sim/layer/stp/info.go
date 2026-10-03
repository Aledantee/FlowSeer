package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// BlockReason names the guard holding a port out of the active topology. The
// empty value means no guard is holding the port, whatever its role and state.
type BlockReason string

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

// PortInfo summarizes the runtime spanning tree status of one port.
type PortInfo struct {
	// MSTID names the tree this snapshot belongs to: 0 for the CIST, the
	// instance identifier for an MSTI. It rides here so a trace fact can name
	// the blocking instance rather than leaving a reader to infer it.
	MSTID              bpdu.MSTID
	Role               bpdu.Role
	State              State
	BlockReason        BlockReason
	Priority           uint8
	PathCost           uint32
	DesignatedRoot     bpdu.BridgeID
	Designated         bpdu.BridgeID
	DesignatedPort     uint16
	DesignatedCost     uint32
	PointToPoint       bool
	Edge               bool
	ForwardTransitions uint64
	TxBPDUs            uint64
	RxBPDUs            uint64
	BadBPDUs           uint64
	SendRSTP           bool
}

// blockReason names the guard holding the port out of the active topology.
// bpduGuardDisabled and loopInconsistent are link-on-cist: they are written
// only on the CIST's port state, so every tree reads them through cistP
// rather than through its own copy, which for the CIST tree is the same
// object. pvidInconsistent is tree-owned, set on the VLAN whose SSTP BPDU
// disagreed about the link, so it reads from p. BPDU guard outranks the
// rest: it disables the port outright, so nothing below it can be the
// decisive reason. A PVID-inconsistent port is by definition receiving
// BPDUs, which is what clears loopInconsistent on every receive, so those
// two cannot both hold after a receive and the order between them only
// fixes what a reader sees should that stop being true.
func (l *Layer) blockReason(p, cistP *portState, link *linkRecord) BlockReason {
	switch {
	case link.bpduGuardDisabled:
		return BlockReasonBPDUGuard
	case p.pvidInconsistent:
		return BlockReasonPVIDInconsistent
	case p.loopInconsistent || (cistP != nil && cistP.loopInconsistent):
		return BlockReasonLoopInconsistent
	default:
		return ""
	}
}

// Learns reports whether the named port learns MAC addresses into the filtering
// database for the given VLAN. An untracked port always learns, and so does a
// VLAN with no tree of its own under PVST: the gate this feeds has no reason
// to hold a VLAN's traffic back for a tree that was never asked to run it.
func (l *Layer) Learns(port string, vid vlan.ID) bool {
	t, ok := l.treeFor(vid)
	if !ok {
		return true
	}

	p, ok := t.ports[port]
	if !ok {
		return true
	}

	return p.state == StateLearning || p.state == StateForwarding
}

// Forwards reports whether the named port forwards traffic carrying the given
// VLAN. An untracked port always forwards, and so does a VLAN with no tree of
// its own under PVST, for the same reason Learns does.
func (l *Layer) Forwards(port string, vid vlan.ID) bool {
	t, ok := l.treeFor(vid)
	if !ok {
		return true
	}

	p, ok := t.ports[port]
	if !ok {
		return true
	}

	return p.state == StateForwarding
}

// Root returns the elected root bridge identifier, the path cost to reach it,
// and the interface name of the root port. When this bridge is root, the root
// port name is empty.
func (l *Layer) Root() (bpdu.BridgeID, uint32, string) {
	t := l.cist()

	return t.rootID, t.rootPathCost, t.rootPort
}

// TopologyChanges returns the total number of detected topology changes and the
// timestamp of the most recent change.
func (l *Layer) TopologyChanges() (uint64, time.Time) {
	t := l.cist()

	return t.topologyChangeCount, t.lastTopologyChange
}

// BridgeID returns this bridge's identifier on the common tree, with the
// priority in effect. Outside PVST mode that is the bridge's only identifier;
// inside it, it is VLAN 1's, and every other VLAN's carries its own VLAN in
// the system-ID extension.
func (l *Layer) BridgeID() bpdu.BridgeID {
	return l.cist().bridgeID
}

// Times returns the max age, hello time, and forward delay in force: the root's
// values as received on the root port, or this bridge's own while it is root.
func (l *Layer) Times() (maxAge, hello, forwardDelay time.Duration) {
	return l.times(l.cist())
}

// times returns the timers in force for one tree.
func (l *Layer) times(t *tree) (maxAge, hello, forwardDelay time.Duration) {
	if t.rootPort != "" {
		if rp, ok := t.ports[t.rootPort]; ok && rp.rcvInfoValid {
			return rp.rcvMaxAge, rp.rcvHelloTime, rp.rcvForwardDelay
		}
	}

	return l.maxAge, l.helloTime, l.forwardDelay
}

// PortInfo returns runtime spanning tree information for the named port. If the
// port is not tracked by the layer, PortInfo returns a zero value.
func (l *Layer) PortInfo(port string) PortInfo {
	return l.portInfo(l.cist(), port)
}

// instancePortInfo returns runtime spanning tree information for the named
// port within the given MST instance. It returns a zero value when the
// instance or the port is not tracked by the layer, which is also what a
// plain RSTP bridge (no MST configured) answers for any nonzero MSTID.
func (l *Layer) instancePortInfo(mstid bpdu.MSTID, port string) PortInfo {
	t, ok := l.trees[treeID(mstid)]
	if !ok {
		return PortInfo{}
	}

	return l.portInfo(t, port)
}

// VLANPortInfo returns runtime spanning tree information for the named port
// within the tree that carries the given VLAN. On a bridge running one tree
// every VLAN answers alike, which is what makes this usable as the per-VLAN
// view in every mode; PVST is what makes the VLANs diverge. It returns a zero
// value when the VLAN has no tree of its own, which is also what
// instancePortInfo answers for an unknown MSTID.
func (l *Layer) VLANPortInfo(vid vlan.ID, port string) PortInfo {
	t, ok := l.treeFor(vid)
	if !ok {
		return PortInfo{}
	}

	return l.portInfo(t, port)
}

// TracksVLAN reports whether the layer runs a spanning tree for the given
// VLAN. Outside PVST mode this is always true, since the CIST carries every
// VLAN; under PVST it is true only for a VLAN with its own tree.
func (l *Layer) TracksVLAN(vid vlan.ID) bool {
	_, ok := l.treeFor(vid)

	return ok
}

// PVSTBoundary reports whether the named port faces a neighbor whose
// spanning tree this bridge cannot simulate per VLAN: an MSTP neighbor on a
// PVST bridge, or a PVST neighbor on one that is not. The mark survives
// until the link goes down, since only that can replace the neighbor.
// PVSTBoundary reports whether the named port faces a neighbor whose
// spanning tree this bridge cannot simulate per VLAN: an MSTP neighbor on a
// PVST bridge, or a PVST neighbor on one that is not. The mark survives
// until the link goes down, since only that can replace the neighbor.
func (l *Layer) PVSTBoundary(port string) bool {
	link, ok := l.links[port]
	if !ok {
		return false
	}

	return link.pvstBoundary
}

// PortLinked reports whether the layer would process a BPDU arriving on the
// named port rather than treat it as SSTPPortDown: the port is one this
// layer tracks and its link is currently up.
func (l *Layer) PortLinked(port string) bool {
	link, ok := l.links[port]

	return ok && link.up
}

// portInfo renders a PortInfo snapshot for one port within one tree. The
// designated fields resolve against that tree's own bridge and root, so an
// MSTI's designated cost reads as its internal cost to the regional root
// rather than the CIST's external cost.
func (l *Layer) portInfo(t *tree, port string) PortInfo {
	p, ok := t.ports[port]
	if !ok {
		return PortInfo{}
	}
	link := l.links[port]
	if link == nil {
		link = &linkRecord{}
	}
	cistP := l.cist().ports[port]

	var desigRoot, desig bpdu.BridgeID
	var desigPort uint16
	var desigCost uint32

	switch p.role {
	case bpdu.RoleDesignated:
		desigRoot = t.rootID
		desig = t.bridgeID
		desigPort = p.portID
		desigCost = t.rootPathCost
	case bpdu.RoleRoot, bpdu.RoleAlternate, bpdu.RoleBackup:
		if p.rcvInfoValid {
			desigRoot = p.rcvRootID
			desig = p.rcvBridgeID
			desigPort = p.rcvPortID
			desigCost = p.rcvRootPathCost
		}
	case bpdu.RoleDisabled:
	}

	return PortInfo{
		MSTID:              bpdu.MSTID(t.id),
		Role:               p.role,
		State:              p.state,
		BlockReason:        l.blockReason(p, cistP, link),
		Priority:           uint8(p.portID >> 8),
		PathCost:           p.pathCost,
		DesignatedRoot:     desigRoot,
		Designated:         desig,
		DesignatedPort:     desigPort,
		DesignatedCost:     desigCost,
		PointToPoint:       link.pointToPoint,
		Edge:               link.edge,
		ForwardTransitions: p.forwardTransitions,
		TxBPDUs:            p.txBPDUs,
		RxBPDUs:            link.rxBPDUs,
		BadBPDUs:           link.badBPDUs,
		SendRSTP:           link.sendRSTP,
	}
}

// BadBPDU records that a frame received on the named port could not be
// decoded as a BPDU. badBPDUs is link-owned, so it is bumped on the link
// record alone; every tree's PortInfo answers from that same copy. An
// untracked port is ignored.
func (l *Layer) BadBPDU(port string) {
	if link, ok := l.links[port]; ok {
		link.badBPDUs++
	}
}
