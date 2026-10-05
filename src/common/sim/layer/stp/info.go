package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// BlockReason names the guard holding a port out of the active topology. The
// empty value means no guard is holding the port, whatever its role and state.
type BlockReason string

// TreeKind says what the identifier of a spanning tree names. An MSTI's number
// and a PVST VLAN's share a range, so the number alone does not tell two trees
// apart.
type TreeKind string

const (
	// TreeCIST is the common and internal spanning tree of a bridge that does
	// not run PVST. Its identifier is 0.
	TreeCIST TreeKind = "cist"
	// TreeMSTI is a multiple spanning tree instance, identified by its MSTID.
	TreeMSTI TreeKind = "msti"
	// TreeVLAN is the tree of one VLAN under PVST, identified by the VLAN. The
	// tree of VLAN 1 is the one that takes the CIST's place there.
	TreeVLAN TreeKind = "vlan"
)

// TreeRef identifies the spanning tree a PortInfo describes.
type TreeRef struct {
	Kind TreeKind
	ID   uint16
}

// treeRef names tree t by what this bridge runs it for.
func (l *Layer) treeRef(t *tree) TreeRef {
	switch {
	case l.pvst != nil:
		return TreeRef{Kind: TreeVLAN, ID: uint16(t.vid)}
	case t.id == cistID:
		return TreeRef{Kind: TreeCIST}
	default:
		return TreeRef{Kind: TreeMSTI, ID: uint16(t.id)}
	}
}

// PortInfo summarizes the runtime spanning tree status of one port.
type PortInfo struct {
	// Tree names the tree this snapshot belongs to. It rides here so a trace
	// fact can name the blocking tree rather than leaving a reader to infer
	// it.
	Tree               TreeRef
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
// bpduGuardDisabled is a link fact, read from the port's link record.
// loopInconsistent is read through loopMark: the CIST's port state carries it
// for every tree outside PVST, and each tree carries its own under PVST.
// pvidInconsistent is tree-owned, set on the VLAN whose SSTP BPDU disagreed
// about the link, so it reads from p. BPDU guard outranks the rest: it
// disables the port outright, so nothing below it can be the decisive reason.
// A PVID-inconsistent BPDU is applied to no tree, so it clears no loop-guard
// mark, and both can hold at once. The order between them only fixes what a
// reader sees then.
func (l *Layer) blockReason(p *portState, lk *linkRecord) BlockReason {
	switch {
	case lk.bpduGuardDisabled:
		return BlockReasonBPDUGuard
	case p.pvidInconsistent:
		return BlockReasonPVIDInconsistent
	case l.loopMark(p).loopInconsistent:
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

// Times returns the max age and forward delay in force: the root's values as
// received on the root port, or this bridge's own while it is root. The hello
// time is always this bridge's own, since Hello Time is a per-bridge value the
// root does not impose (P802.1aq/D1.5 Table 13-5, UNH-IOL RSTP.op.4.3).
func (l *Layer) Times() (maxAge, hello, forwardDelay time.Duration) {
	return l.times(l.cist())
}

// times returns the timers in force for one tree. Max age and forward delay
// follow the root (P802.1aq/D1.5 13.28.9 and 13.29.33 f, draft text), and the
// hello time is the bridge's own. Outside PVST an MSTI runs on the CIST's
// times: an MSTI record carries no timers, and 13.28.9 takes FwdDelay from the
// CIST's designatedTimes. A PVST VLAN's tree stores its own root's.
func (l *Layer) times(t *tree) (maxAge, hello, forwardDelay time.Duration) {
	if l.pvst == nil {
		t = l.cist()
	}
	if t.rootPort != "" {
		if rp, ok := t.ports[t.rootPort]; ok && rp.rcvInfoValid {
			return rp.rcvMaxAge, l.helloTime, rp.rcvForwardDelay
		}
	}

	return l.maxAge, l.helloTime, l.forwardDelay
}

// forwardDelayOf returns the Forward Delay in force for tree t, the step of
// the forward-delay ladder.
func (l *Layer) forwardDelayOf(t *tree) time.Duration {
	_, _, forwardDelay := l.times(t)

	return forwardDelay
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
func (l *Layer) PVSTBoundary(port string) bool {
	lk := l.link(port)

	return lk != nil && lk.pvstBoundary
}

// PortLinked reports whether the layer would process a BPDU arriving on the
// named port rather than treat it as SSTPPortDown: the port is one this
// layer tracks and its link record currently holds the link up. ReceiveSSTP
// makes exactly this check before doing anything else with a frame, and it
// is the one read-only distinction the other accessors do not make directly:
// a PortInfo snapshot carries no link bit, and it renders a port whose link
// went down through the same Disabled and Discarding values a port that
// never came up shows. A caller can still recover the answer from a
// snapshot, but only by re-deriving this layer's own role and guard rules —
// that a down port clears its guards, so a Disabled port reporting BPDU
// guard is up — which is the coupling this accessor exists to spare it.
func (l *Layer) PortLinked(port string) bool {
	lk := l.link(port)

	return lk != nil && lk.up
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
	// The link facts and counters come from the port's link record. Every
	// tree is built from l.portNames, so it always exists for any port a
	// tree tracks; the zero value below only guards that invariant, not a
	// case this simulator reaches.
	lk := l.link(port)
	if lk == nil {
		lk = &linkRecord{}
	}

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
		Tree:               l.treeRef(t),
		Role:               p.role,
		State:              p.state,
		BlockReason:        l.blockReason(p, lk),
		Priority:           uint8(p.portID >> 8),
		PathCost:           p.pathCost,
		DesignatedRoot:     desigRoot,
		Designated:         desig,
		DesignatedPort:     desigPort,
		DesignatedCost:     desigCost,
		PointToPoint:       lk.pointToPoint,
		Edge:               lk.edge,
		ForwardTransitions: p.forwardTransitions,
		TxBPDUs:            p.txBPDUs,
		RxBPDUs:            lk.rxBPDUs,
		BadBPDUs:           lk.badBPDUs,
		SendRSTP:           lk.sendRSTP,
	}
}
