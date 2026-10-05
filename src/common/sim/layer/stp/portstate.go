package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
)

// linkRecord holds what is true of a physical link whatever tree runs over
// it: one record per port, owned by Layer. A tree's portState keeps only what
// that tree computes, so a link fact has no second copy to keep in step and
// no tree can read a stale one.
type linkRecord struct {
	up           bool
	pointToPoint bool
	edge         bool
	sendRSTP     bool

	// adminEdge is the configured edge status the link returns to whenever
	// auto-edge detection is lost or the link comes up.
	adminEdge bool

	// cost is the path cost the link and its admin configuration derive. A
	// tree that fixed its own cost keeps it in portState.pathCost; every
	// other tree takes this one.
	cost uint32

	// external marks a boundary port: one whose most recently received BPDU
	// carried no MST configuration identifier, or one from a different
	// region. Classification is a property of the link, not of a tree
	// running over it; every tree consults it through Layer.boundary.
	external bool

	// bpduGuardDisabled holds the port out of the active topology until the
	// link goes down and up.
	bpduGuardDisabled bool

	// pvstBoundary marks a port whose peer speaks a spanning tree this bridge
	// does not simulate per VLAN: an MST BPDU seen by a PVST bridge, or an
	// SSTP BPDU seen by a bridge that is not one. It is a statement about the
	// neighbor, not about a tree, and only a link transition can replace the
	// neighbor.
	pvstBoundary bool

	mdelayWhile    time.Time
	edgeDelayWhile time.Time

	// tcAck is the acknowledgment the next Configuration BPDU on the port
	// carries once. A topology change notification received on a Designated
	// port sets it, and one transmission of a Configuration or RST BPDU
	// clears it (IEEE Std 802.1Q-2003 Figure 13-13 and Figure 13-19).
	tcAck bool

	rxBPDUs  uint64
	badBPDUs uint64
}

type portState struct {
	name     string
	cfg      Port
	portID   uint16
	pathCost uint32

	// pathCostFixed marks an MSTI port whose path cost was configured
	// explicitly on the instance (InstancePort.PathCost nonzero), or the
	// CIST's own port state in PVST mode when VLAN 1's tree configures it. A
	// fixed cost stays put across a link change; an unfixed one tracks the
	// link record's cost, which link speed and admin configuration otherwise
	// drive.
	pathCostFixed bool

	// rcvRegionalRootID and rcvInternalRootPathCost hold the CIST's
	// region-internal received information, filled only when a BPDU arrived
	// internal (the link record's external is false). rcvRemainingHops holds
	// the hop count an internal BPDU (CIST or MSTI) carried, which internal
	// information ages by instead of message age.
	rcvRegionalRootID       bpdu.BridgeID
	rcvInternalRootPathCost uint32
	rcvRemainingHops        uint8

	role  bpdu.Role
	state State

	// loopInconsistent is the loop-guard outcome that holds a port out of the
	// active topology. Only the CIST's copy is armed, and every tree reads it
	// through the CIST.
	loopInconsistent bool

	// pvidInconsistent marks a port whose peer named a different VLAN than
	// the one an SSTP BPDU arrived on. Unlike the two guards above it is
	// written on the arrival VLAN's own tree rather than only on the CIST's,
	// because the disagreement is about one VLAN on the link and it is that
	// VLAN's traffic that must not cross.
	pvidInconsistent bool

	proposing bool
	agreed    bool

	fwdDelayTimer time.Time

	// tcWhile is the port's topology change timer for this tree, running
	// while it is after the current time. tcActive marks the port as part of
	// the tree's active topology for topology change: a non-edge Root or
	// Designated port that started forwarding and has not lost the role. Only
	// an active port acts on a received topology change (IEEE Std
	// 802.1Q-2003 Figure 13-19).
	tcWhile  time.Time
	tcActive bool

	rcvInfoValid    bool
	rcvRootID       bpdu.BridgeID
	rcvRootPathCost uint32
	rcvBridgeID     bpdu.BridgeID
	rcvPortID       uint16
	rcvMessageAge   time.Duration
	rcvMaxAge       time.Duration
	rcvHelloTime    time.Duration
	rcvForwardDelay time.Duration
	rcvTime         time.Time

	forwardTransitions uint64

	txBPDUs uint64
}

func (p *portState) clone() *portState {
	cp := *p

	return &cp
}

// txKey addresses one transmit budget. Outside PVST mode every tree resolves
// to cistID, which is what makes the budget bridge-global there.
type txKey struct {
	tree treeID
	port string
}

// portTx holds the BPDU transmit budget for one key. Outside PVST mode IEEE
// 802.1Q meters transmission per port, not per spanning tree instance, which
// is why this lives on Layer rather than inside a tree's per-port state; in
// PVST mode it is metered per VLAN's tree as well, which is what txKey's
// tree component addresses.
type portTx struct {
	count             int
	tick              time.Time
	pendingDesignated bool
	pendingAgreement  bool

	// pendingTopology holds a Root port's topology change transmission the
	// budget refused. topologyOwed marks one the call in progress still has to
	// send: a timer started on a Root port, which sends when it starts. Any
	// emission on the key clears it, since every BPDU carries the flag from the
	// timers running at that moment.
	pendingTopology bool
	topologyOwed    bool
}

func (tx *portTx) clone() *portTx {
	cp := *tx

	return &cp
}

// loopGuardWatches reports whether loop guard applies to the port. Cisco and
// Arista both rule the guard out on an edge port and on a shared link, where a
// port that stops hearing BPDUs is not evidence of a unidirectional link.
func (p *portState) loopGuardWatches(lk *linkRecord) bool {
	return p.cfg.LoopGuard && lk.up && !lk.edge && lk.pointToPoint
}

// txKeyFor addresses the transmit budget tree t spends on the named port.
// Outside PVST mode every tree resolves to the same key, since MSTP emits
// only from the CIST and shares one budget per port; a PVST bridge emits one
// BPDU per VLAN per port, so each tree meters its own.
func (l *Layer) txKeyFor(t *tree, name string) txKey {
	if l.pvst == nil {
		return txKey{tree: cistID, port: name}
	}

	return txKey{tree: t.id, port: name}
}

// tx returns the transmit budget tree t spends on the named port.
func (l *Layer) tx(t *tree, name string) *portTx {
	return l.portTx[l.txKeyFor(t, name)]
}
