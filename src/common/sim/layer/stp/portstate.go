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

	// synced is the stored flag that decides whether a sync cuts a Designated
	// port. designatedVec is the vector the port last held as Designated, and
	// designatedVecSet is false until role assignment has recorded one.
	synced           bool
	designatedVec    priorityVector
	designatedVecSet bool

	tcWhile  time.Time
	tcActive bool
	tcAck    bool

	fwdDelayTimer time.Time

	// The deadlines use the tree's current timers, including times received
	// after the role assignment that records these instants.
	leftRootAt   time.Time
	leftBackupAt time.Time

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

func recentDeadline(start time.Time, delay time.Duration, now time.Time) time.Time {
	if start.IsZero() {
		return time.Time{}
	}
	deadline := start.Add(delay)
	if !deadline.After(now) {
		return time.Time{}
	}
	return deadline
}

func (l *Layer) recentRootDeadline(t *tree, p *portState, now time.Time) time.Time {
	_, _, delay := l.times(t)
	return recentDeadline(p.leftRootAt, delay, now)
}

func (l *Layer) recentBackupDeadline(t *tree, p *portState, now time.Time) time.Time {
	_, hello, _ := l.times(t)
	return recentDeadline(p.leftBackupAt, 2*hello, now)
}

func (p *portState) recordRoleChange(oldRole bpdu.Role, now time.Time) {
	if oldRole == bpdu.RoleRoot && p.role == bpdu.RoleDesignated {
		p.leftRootAt = now
	}
	if oldRole == bpdu.RoleBackup && p.role != bpdu.RoleBackup {
		p.leftBackupAt = now
	}
}

// txKey addresses one transmit budget. Outside PVST mode every tree resolves
// to cistID, which is what makes the budget bridge-global there.
type txKey struct {
	tree treeID
	port string
}

// portTx holds the BPDU transmit budget for one key. Outside PVST mode IEEE
// 802.1Q meters transmission per port, not per spanning tree instance, which
// is why this lives on Layer rather than inside a tree's per-port state. In
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
// only from the CIST and shares one budget per port. A PVST bridge emits one
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

// resetTransmit initializes both requests and clears the count and timers on
// the named port for its next enabled transmit pass.
func (l *Layer) resetTransmit(name string) {
	for _, id := range l.treeOrder {
		tx := l.tx(l.trees[id], name)
		tx.newInfo = true
		tx.newInfoMsti = true
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
