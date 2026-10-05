package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
)

// linkRecord holds the physical and administrative link properties for one port,
// owned by Layer rather than replicated per tree.
type linkRecord struct {
	up                bool
	pointToPoint      bool
	edge              bool
	sendRSTP          bool
	adminEdge         bool
	linkPathCost      uint32
	external          bool
	bpduGuardDisabled bool
	pvstBoundary      bool
	mdelayWhile       time.Time
	edgeDelayWhile    time.Time
	rxBPDUs           uint64
	badBPDUs          uint64
}

type portState struct {
	name          string
	cfg           Port
	portID        uint16
	pathCost      uint32
	pathCostFixed bool

	rcvRegionalRootID       bpdu.BridgeID
	rcvInternalRootPathCost uint32
	rcvRemainingHops        uint8

	role  bpdu.Role
	state State

	loopInconsistent bool
	pvidInconsistent bool

	proposing bool
	agreed    bool

	tcWhile  time.Time
	tcActive bool
	tcAck    bool

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
	txBPDUs            uint64
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
	newInfo     bool
	newInfoMsti bool
	count       int
	tick        time.Time
	helloWhen   time.Time
}

func (tx *portTx) clone() *portTx {
	cp := *tx

	return &cp
}

// loopGuardWatches reports whether loop guard applies to the port. Cisco and
// Arista both rule the guard out on an edge port and on a shared link, where a
// port that stops hearing BPDUs is not evidence of a unidirectional link.
func (p *portState) loopGuardWatches(link *linkRecord) bool {
	return p.cfg.LoopGuard && link.up && !link.edge && link.pointToPoint
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

// resetTransmit drops the count and timers on the named port while retaining
// pending requests for the port-up transmit pass.
func (l *Layer) resetTransmit(name string) {
	for _, id := range l.treeOrder {
		tx := l.tx(l.trees[id], name)
		tx.count = 0
		tx.tick = time.Time{}
		tx.helloWhen = time.Time{}
	}
}

// requestNewInfo records that tree t has information to transmit on p. MSTP
// shares one frame for the CIST and its instance records, while PVST gives
// every VLAN tree its own frame and budget.
func (l *Layer) requestNewInfo(t *tree, p *portState) {
	tx := l.tx(t, p.name)
	if l.pvst == nil && t.id != cistID {
		tx.newInfoMsti = true
		return
	}
	tx.newInfo = true
}
