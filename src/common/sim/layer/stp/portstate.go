package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
)

// linkState holds the properties of a physical link that are true for every
// tree running over the port, whatever VLAN that tree carries. portState
// embeds it and syncInstancePorts assigns it whole onto every other tree's
// port, so a field added here propagates to every tree by construction
// instead of needing its own copy statement remembered at every call site.
type linkState struct {
	up           bool
	pointToPoint bool
	edge         bool
	sendRSTP     bool
}

type portState struct {
	name      string
	cfg       Port
	portID    uint16
	pathCost  uint32
	adminEdge bool

	linkState

	// linkPathCost is the cost the link and its admin configuration derive,
	// kept apart from pathCost so an instance-level override never overwrites
	// it. In PVST mode VLAN 1's own per-port cost overrides the CIST's
	// pathCost, since that slot is VLAN 1's tree; syncInstancePorts must still
	// carry the link-derived cost, not VLAN 1's, onto every other VLAN's tree.
	// Outside PVST mode the two are always equal.
	linkPathCost uint32

	// pathCostFixed marks an MSTI port whose path cost was configured
	// explicitly on the instance (InstancePort.PathCost nonzero), or the
	// CIST's own port state in PVST mode when VLAN 1's tree configures it. A
	// fixed cost stays put across a link change; an unfixed one tracks the
	// CIST port's cost, which link speed and admin configuration otherwise
	// drive.
	pathCostFixed bool

	// external marks a boundary port: one whose most recently received BPDU
	// carried no MST configuration identifier, or one from a different
	// region. It lives on the CIST port state because classification is a
	// property of the link, not of a tree running over it; every other tree
	// consults it through Layer.boundary.
	external bool

	// rcvRegionalRootID and rcvInternalRootPathCost hold the CIST's
	// region-internal received information, filled only when a BPDU arrived
	// internal (external is false). rcvRemainingHops holds the hop count an
	// internal BPDU (CIST or MSTI) carried, which internal information ages
	// by instead of message age.
	rcvRegionalRootID       bpdu.BridgeID
	rcvInternalRootPathCost uint32
	rcvRemainingHops        uint8

	role  bpdu.Role
	state State

	// bpduGuardDisabled and loopInconsistent are the two guard outcomes that
	// hold a port out of the active topology. They are separate fields rather
	// than one reason because they clear on different events.
	bpduGuardDisabled bool
	loopInconsistent  bool

	// pvidInconsistent marks a port whose peer named a different VLAN than
	// the one an SSTP BPDU arrived on. Unlike the two guards above it is
	// written on the arrival VLAN's own tree rather than only on the CIST's,
	// because the disagreement is about one VLAN on the link and it is that
	// VLAN's traffic that must not cross.
	pvidInconsistent bool

	// pvstBoundary marks a port whose peer speaks a spanning tree this bridge
	// does not simulate per VLAN: an MST BPDU seen by a PVST bridge, or an
	// SSTP BPDU seen by a bridge that is not one. It lives on the CIST port
	// state because it is a statement about the neighbor, not about a tree,
	// and only a link transition can replace the neighbor.
	pvstBoundary bool

	proposing bool
	agreed    bool

	fwdDelayTimer time.Time

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

	mdelayWhile    time.Time
	edgeDelayWhile time.Time

	txBPDUs  uint64
	rxBPDUs  uint64
	badBPDUs uint64
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
}

func (tx *portTx) clone() *portTx {
	cp := *tx

	return &cp
}

// loopGuardWatches reports whether loop guard applies to the port. Cisco and
// Arista both rule the guard out on an edge port and on a shared link, where a
// port that stops hearing BPDUs is not evidence of a unidirectional link.
func (p *portState) loopGuardWatches() bool {
	return p.cfg.LoopGuard && p.up && !p.edge && p.pointToPoint
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
