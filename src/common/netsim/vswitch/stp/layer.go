package stp

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Emission describes an Ethernet frame to transmit out a virtual switch port.
type Emission struct {
	Port  string
	Frame ethernet.Frame
}

// Effects lists frames to emit and ports whose learned forwarding table entries
// must be flushed as a result of a spanning tree transition.
type Effects struct {
	Emissions []Emission
	Flush     []string
}

// BlockReason names the guard holding a port out of the active topology. The
// empty value means no guard is holding the port, whatever its role and state.
type BlockReason string

const (
	// BlockReasonBPDUGuard marks a port BPDU guard disabled because a BPDU
	// arrived on it. Only a link down and up clears it.
	BlockReasonBPDUGuard BlockReason = "bpdu-guard"

	// BlockReasonLoopInconsistent marks a port whose received information
	// expired while it held a non-designated role, which loop guard keeps
	// discarding rather than letting it open a loop. The next BPDU clears it.
	BlockReasonLoopInconsistent BlockReason = "loop-inconsistent"
)

// PortInfo summarizes the runtime spanning tree status of one port.
type PortInfo struct {
	Role               Role
	State              State
	BlockReason        BlockReason
	Priority           uint8
	PathCost           uint32
	DesignatedRoot     BridgeID
	Designated         BridgeID
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

// Layer implements the Rapid Spanning Tree protocol layer for a virtual switch.
// It is a deterministic state machine driven by explicit time-stamped method calls.
// Layer is not safe for concurrent use.
type Layer struct {
	cfg      Config
	priority uint16
	address  netaddr.MAC
	bridgeID BridgeID

	helloTime    time.Duration
	maxAge       time.Duration
	forwardDelay time.Duration
	txHoldCount  uint8

	// portNames is the bridge-global port key set in the order every emit and
	// flush loop walks. A per-tree map iterated separately would reorder
	// Effects.Flush with no behavior change to point at.
	portNames []string

	trees     map[treeID]*tree
	vidToTree map[vlan.ID]treeID
}

type portState struct {
	name         string
	cfg          Port
	portID       uint16
	pathCost     uint32
	adminEdge    bool
	edge         bool
	pointToPoint bool
	up           bool

	role  Role
	state State

	// bpduGuardDisabled and loopInconsistent are the two guard outcomes that
	// hold a port out of the active topology. They are separate fields rather
	// than one reason because they clear on different events.
	bpduGuardDisabled bool
	loopInconsistent  bool

	proposing bool
	agreed    bool

	fwdDelayTimer time.Time

	rcvInfoValid    bool
	rcvRootID       BridgeID
	rcvRootPathCost uint32
	rcvBridgeID     BridgeID
	rcvPortID       uint16
	rcvMessageAge   time.Duration
	rcvMaxAge       time.Duration
	rcvHelloTime    time.Duration
	rcvForwardDelay time.Duration
	rcvTime         time.Time

	forwardTransitions uint64

	sendRSTP       bool
	mdelayWhile    time.Time
	edgeDelayWhile time.Time

	txCount           int
	txTick            time.Time
	pendingDesignated bool
	pendingAgreement  bool

	txBPDUs  uint64
	rxBPDUs  uint64
	badBPDUs uint64
}

func (p *portState) clone() *portState {
	cp := *p

	return &cp
}

// blockReason names the guard holding the port out of the active topology.
// BPDU guard outranks loop guard: it disables the port outright, where loop
// guard only denies it a forwarding role.
func (p *portState) blockReason() BlockReason {
	switch {
	case p.bpduGuardDisabled:
		return BlockReasonBPDUGuard
	case p.loopInconsistent:
		return BlockReasonLoopInconsistent
	default:
		return ""
	}
}

// loopGuardWatches reports whether loop guard applies to the port. Cisco and
// Arista both rule the guard out on an edge port and on a shared link, where a
// port that stops hearing BPDUs is not evidence of a unidirectional link.
func (p *portState) loopGuardWatches() bool {
	return p.cfg.LoopGuard && p.up && !p.edge && p.pointToPoint
}

type priorityVector struct {
	rootID       BridgeID
	rootPathCost uint32
	bridgeID     BridgeID
	portID       uint16
}

func compareVectors(a, b priorityVector) int {
	if a.rootID.Less(b.rootID) {
		return -1
	}
	if b.rootID.Less(a.rootID) {
		return 1
	}
	if a.rootPathCost != b.rootPathCost {
		return cmp.Compare(a.rootPathCost, b.rootPathCost)
	}
	if a.bridgeID.Less(b.bridgeID) {
		return -1
	}
	if b.bridgeID.Less(a.bridgeID) {
		return 1
	}

	return cmp.Compare(a.portID, b.portID)
}

// New constructs a spanning tree layer from the given configuration and port table.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, ports port.Table) (*Layer, error) {
	if err := cfg.Validate(ports); err != nil {
		return nil, err
	}
	return newLayer(cfg.Normalize()), nil
}

func newLayer(cfg Config) *Layer {
	prio := effectivePriority(cfg.Priority, cfg.PriorityPresent)
	hello := effectiveHelloTime(cfg.HelloTime)
	maxAge := effectiveMaxAge(cfg.MaxAge)
	fwdDelay := effectiveForwardDelay(cfg.ForwardDelay)

	bridgeID := BridgeID{Priority: prio, Address: cfg.Address}

	holdCount := cfg.TxHoldCount
	if holdCount == 0 {
		holdCount = DefaultTxHoldCount
	}

	// The port identifier is bridge-global: it is derived from the index in the
	// sorted port names, appears on the wire, and must not vary by tree.
	sortedNames := sortedKeys(cfg.Ports)

	cist := &tree{
		id:           cistID,
		rootID:       bridgeID,
		rootPathCost: 0,
		rootPort:     "",
		ports:        make(map[string]*portState, len(cfg.Ports)),
	}

	l := &Layer{
		cfg:          cfg,
		priority:     prio,
		address:      cfg.Address,
		bridgeID:     bridgeID,
		helloTime:    hello,
		maxAge:       maxAge,
		forwardDelay: fwdDelay,
		txHoldCount:  holdCount,
		portNames:    sortedNames,
		trees:        map[treeID]*tree{cistID: cist},
		vidToTree:    make(map[vlan.ID]treeID),
	}

	for i, name := range sortedNames {
		pCfg := cfg.Ports[name]
		portPrio := effectivePortPriority(pCfg.Priority, pCfg.PriorityPresent)
		portID := (uint16(portPrio) << 8) | uint16(i+1)

		cost := pCfg.PathCost
		if cost == 0 {
			cost = DefaultPathCost(0)
		}

		cist.ports[name] = &portState{
			name:         name,
			cfg:          pCfg,
			portID:       portID,
			pathCost:     cost,
			adminEdge:    pCfg.AdminEdge,
			edge:         pCfg.AdminEdge,
			pointToPoint: false,
			up:           false,
			role:         RoleDisabled,
			state:        StateDiscarding,
			sendRSTP:     true,
		}
	}

	return l
}

// Clone creates an independent deep copy of the spanning tree layer, preserving
// all ports, timers, and elected roles.
func (l *Layer) Clone() *Layer {
	cp := &Layer{
		cfg:          l.cfg,
		priority:     l.priority,
		address:      l.address,
		bridgeID:     l.bridgeID,
		helloTime:    l.helloTime,
		maxAge:       l.maxAge,
		forwardDelay: l.forwardDelay,
		txHoldCount:  l.txHoldCount,
		portNames:    slices.Clone(l.portNames),
		trees:        make(map[treeID]*tree, len(l.trees)),
		vidToTree:    make(map[vlan.ID]treeID, len(l.vidToTree)),
	}

	cp.cfg.Ports = make(map[string]Port, len(l.cfg.Ports))
	for k, v := range l.cfg.Ports {
		cp.cfg.Ports[k] = v
	}
	for id, t := range l.trees {
		cp.trees[id] = t.clone()
	}
	for vid, id := range l.vidToTree {
		cp.vidToTree[vid] = id
	}

	return cp
}

// Learns reports whether the named port learns MAC addresses into the filtering
// database for the given VLAN. An untracked port always learns. While the bridge
// runs one tree every VLAN answers alike; the parameter is what lets that stop
// being true without moving the seam again.
func (l *Layer) Learns(port string, vid vlan.ID) bool {
	p, ok := l.treeFor(vid).ports[port]
	if !ok {
		return true
	}

	return p.state == StateLearning || p.state == StateForwarding
}

// Forwards reports whether the named port forwards traffic carrying the given
// VLAN. An untracked port always forwards.
func (l *Layer) Forwards(port string, vid vlan.ID) bool {
	p, ok := l.treeFor(vid).ports[port]
	if !ok {
		return true
	}

	return p.state == StateForwarding
}

// Root returns the elected root bridge identifier, the path cost to reach it,
// and the interface name of the root port. When this bridge is root, the root
// port name is empty.
func (l *Layer) Root() (BridgeID, uint32, string) {
	t := l.cist()

	return t.rootID, t.rootPathCost, t.rootPort
}

// TopologyChanges returns the total number of detected topology changes and the
// timestamp of the most recent change.
func (l *Layer) TopologyChanges() (uint64, time.Time) {
	t := l.cist()

	return t.topologyChangeCount, t.lastTopologyChange
}

// BridgeID returns this bridge's identifier with the priority in effect.
func (l *Layer) BridgeID() BridgeID {
	return l.bridgeID
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
	t := l.cist()
	p, ok := t.ports[port]
	if !ok {
		return PortInfo{}
	}

	var desigRoot, desig BridgeID
	var desigPort uint16
	var desigCost uint32

	switch p.role {
	case RoleDesignated:
		desigRoot = t.rootID
		desig = l.bridgeID
		desigPort = p.portID
		desigCost = t.rootPathCost
	case RoleRoot, RoleAlternate, RoleBackup:
		if p.rcvInfoValid {
			desigRoot = p.rcvRootID
			desig = p.rcvBridgeID
			desigPort = p.rcvPortID
			desigCost = p.rcvRootPathCost
		}
	case RoleDisabled:
	}

	return PortInfo{
		Role:               p.role,
		State:              p.state,
		BlockReason:        p.blockReason(),
		Priority:           uint8(p.portID >> 8),
		PathCost:           p.pathCost,
		DesignatedRoot:     desigRoot,
		Designated:         desig,
		DesignatedPort:     desigPort,
		DesignatedCost:     desigCost,
		PointToPoint:       p.pointToPoint,
		Edge:               p.edge,
		ForwardTransitions: p.forwardTransitions,
		TxBPDUs:            p.txBPDUs,
		RxBPDUs:            p.rxBPDUs,
		BadBPDUs:           p.badBPDUs,
		SendRSTP:           p.sendRSTP,
	}
}

// BadBPDU records that a frame received on the named port could not be decoded as a BPDU.
// An untracked port is ignored.
func (l *Layer) BadBPDU(port string) {
	p, ok := l.cist().ports[port]
	if !ok {
		return
	}

	p.badBPDUs++
}

// Mcheck triggers protocol migration checking on the named port, forcing it
// to transmit RSTP BPDUs and restarting the migration delay. If the port is
// unknown or down, Mcheck has no effect.
func (l *Layer) Mcheck(now time.Time, port string) Effects {
	t := l.cist()
	p, ok := t.ports[port]
	if !ok || !p.up {
		return Effects{}
	}

	p.sendRSTP = true
	p.mdelayWhile = now.Add(MigrateTime)

	var flushes []string
	var emissions []Emission

	subEmissions := l.recompute(t, now, &flushes)
	emissions = append(emissions, subEmissions...)

	if p.role == RoleDesignated && p.up {
		alreadyEmitted := false
		for _, e := range emissions {
			if e.Port == p.name {
				alreadyEmitted = true
				break
			}
		}
		if !alreadyEmitted && !p.pendingDesignated {
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// NextWake returns the earliest scheduled time at which the layer needs to be
// woken, and reports whether any timer is currently active.
func (l *Layer) NextWake() (time.Time, bool) {
	var next time.Time
	hasTimer := false

	update := func(at time.Time) {
		if at.IsZero() {
			return
		}
		if !hasTimer || at.Before(next) {
			next = at
			hasTimer = true
		}
	}

	for _, t := range l.trees {
		update(t.helloTimer)
		update(t.topologyChangeTimer)

		for _, p := range t.ports {
			update(p.fwdDelayTimer)
			if p.rcvInfoValid {
				update(p.rcvTime.Add(3 * p.rcvHelloTime))
			}
			// The edge delay is due only on a port that can still become an
			// edge, or a wake would be scheduled that changes nothing.
			if p.cfg.AutoEdge && !p.edge && p.up && p.sendRSTP && p.role == RoleDesignated &&
				p.state == StateDiscarding && p.pointToPoint && p.proposing {
				update(p.edgeDelayWhile)
			}
			if (p.pendingAgreement || p.pendingDesignated) && !p.txTick.IsZero() {
				update(p.txTick)
			}
		}
	}

	return next, hasTimer
}

type emissionKind uint8

const (
	emissionDesignated emissionKind = iota
	emissionAgreement
)

func (l *Layer) emit(t *tree, p *portState, now time.Time, kind emissionKind, emissions *[]Emission) {
	for p.txCount > 0 && !p.txTick.After(now) {
		p.txCount--
		p.txTick = p.txTick.Add(time.Second)
	}
	if p.txCount == 0 {
		p.txTick = time.Time{}
	}

	if p.txCount < int(l.txHoldCount) {
		var bpdu BPDU
		switch kind {
		case emissionDesignated:
			proposal := p.pointToPoint && p.state == StateDiscarding && !p.agreed && p.sendRSTP
			bpdu = l.makeBPDU(t, p, now, proposal)
		case emissionAgreement:
			bpdu = l.makeAgreementBPDU(t, p, now)
		}

		*emissions = append(*emissions, Emission{Port: p.name, Frame: Encode(bpdu, l.address)})
		p.txBPDUs++
		wasZero := p.txCount == 0
		p.txCount++
		if wasZero {
			p.txTick = now.Add(time.Second)
		}
	} else {
		switch kind {
		case emissionDesignated:
			p.pendingDesignated = true
		case emissionAgreement:
			p.pendingAgreement = true
		}
	}
}

// edgeDelay is the time without a BPDU after which a port may be detected as
// an edge: MigrateTime on a point-to-point link, the max age in force on a
// shared one.
func (l *Layer) edgeDelay(t *tree, p *portState) time.Duration {
	if p.pointToPoint {
		return MigrateTime
	}
	maxAge, _, _ := l.times(t)

	return maxAge
}

func (l *Layer) isSynced(t *tree, rootPort string) bool {
	for name, p := range t.ports {
		if name == rootPort {
			continue
		}
		if p.role == RoleDesignated && !p.edge {
			if p.state != StateDiscarding && !p.agreed {
				return false
			}
		}
	}

	return true
}

func (l *Layer) raiseTopologyChange(t *tree, originPort string, now time.Time, flushes *[]string) {
	t.topologyChangeCount++
	t.lastTopologyChange = now
	t.topologyChangeTimer = now.Add(l.helloTime + time.Second)

	for _, name := range l.portNames {
		if name == originPort {
			continue
		}
		if !slices.Contains(*flushes, name) {
			*flushes = append(*flushes, name)
		}
	}
}

func (l *Layer) makeBPDU(t *tree, p *portState, now time.Time, proposal bool) BPDU {
	var msgAge time.Duration
	maxAge, hello, fwdDelay := l.times(t)

	if t.rootPort != "" {
		if rp, ok := t.ports[t.rootPort]; ok && rp.rcvInfoValid {
			msgAge = rp.rcvMessageAge + time.Second
		}
	}

	b := BPDU{
		RootID:       t.rootID,
		RootPathCost: t.rootPathCost,
		BridgeID:     l.bridgeID,
		PortID:       p.portID,
		MessageAge:   msgAge,
		MaxAge:       maxAge,
		HelloTime:    hello,
		ForwardDelay: fwdDelay,
	}

	if !p.sendRSTP {
		b.Version = 0
		b.Type = BPDUTypeConfiguration
		proposal = false
	} else {
		b.Version = 2
		b.Type = BPDUTypeRapid
	}

	b.SetRole(p.role)
	b.SetProposal(proposal)
	b.SetLearning(p.state == StateLearning || p.state == StateForwarding)
	b.SetForwarding(p.state == StateForwarding)
	if !t.topologyChangeTimer.IsZero() && t.topologyChangeTimer.After(now) {
		b.SetTopologyChange(true)
	}

	return b
}

func (l *Layer) makeAgreementBPDU(t *tree, p *portState, now time.Time) BPDU {
	b := l.makeBPDU(t, p, now, false)
	b.SetRole(p.role)
	if p.sendRSTP {
		b.SetAgreement(true)
	}
	b.SetProposal(false)
	b.SetLearning(p.state == StateLearning || p.state == StateForwarding)
	b.SetForwarding(p.state == StateForwarding)

	return b
}

func (l *Layer) recompute(t *tree, now time.Time, flushes *[]string) []Emission {
	var emissions []Emission

	oldRootID := t.rootID
	oldRootCost := t.rootPathCost
	oldRootPort := t.rootPort

	bestVector := priorityVector{
		rootID:       l.bridgeID,
		rootPathCost: 0,
		bridgeID:     l.bridgeID,
		portID:       0,
	}
	bestPort := ""
	bestRcvPortID := uint16(0)

	for _, name := range l.portNames {
		p := t.ports[name]
		if !p.up || p.bpduGuardDisabled || !p.rcvInfoValid {
			continue
		}
		// Restricted role denies the port the root role, and loop guard holds a
		// port whose information expired out of the tree so it reconverges
		// around it. Neither may contribute the bridge's root vector.
		if p.cfg.RestrictedRole || p.loopInconsistent {
			continue
		}
		if !p.rcvTime.Add(3 * p.rcvHelloTime).After(now) {
			continue
		}

		cand := priorityVector{
			rootID:       p.rcvRootID,
			rootPathCost: p.rcvRootPathCost + p.pathCost,
			bridgeID:     p.rcvBridgeID,
			portID:       p.rcvPortID,
		}

		if bestPort == "" {
			if compareVectors(cand, bestVector) < 0 {
				bestVector = cand
				bestPort = name
				bestRcvPortID = p.portID
			}
		} else {
			diff := compareVectors(cand, bestVector)
			if diff < 0 || (diff == 0 && p.portID < bestRcvPortID) {
				bestVector = cand
				bestPort = name
				bestRcvPortID = p.portID
			}
		}
	}

	if bestPort == "" {
		t.rootID = l.bridgeID
		t.rootPathCost = 0
		t.rootPort = ""
	} else {
		t.rootID = bestVector.rootID
		t.rootPathCost = bestVector.rootPathCost
		t.rootPort = bestPort
	}

	for _, name := range l.portNames {
		p := t.ports[name]
		oldRole := p.role
		switch {
		case !p.up || p.bpduGuardDisabled:
			p.role = RoleDisabled
		case p.loopInconsistent:
			// A loop-inconsistent port is Alternate and never Designated: a
			// port that stopped hearing its designated peer is the one that
			// would open a loop by claiming the segment.
			p.role = RoleAlternate
		case name == t.rootPort:
			p.role = RoleRoot
		default:
			p.role = l.designatedOrBlocked(t, p, now)
		}
		// An agreement belongs to the Designated role that earned it; a port
		// that leaves the role and comes back must propose again, or it would
		// forward without a handshake on a link whose peer never agreed.
		if p.role != oldRole && p.role != RoleDesignated {
			p.agreed = false
		}
		// The edge delay counts from the moment the port could become an
		// edge; a port that returns to Designated with the timer long past
		// would otherwise report a wake in the past.
		if p.role == RoleDesignated && oldRole != RoleDesignated && p.cfg.AutoEdge {
			p.edgeDelayWhile = now.Add(l.edgeDelay(t, p))
		}
	}

	for _, name := range l.portNames {
		p := t.ports[name]
		oldState := p.state

		switch p.role {
		case RoleDisabled, RoleAlternate, RoleBackup:
			p.state = StateDiscarding
			p.fwdDelayTimer = time.Time{}
		case RoleRoot:
			if p.pointToPoint && l.isSynced(t, p.name) && p.sendRSTP {
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			} else if p.state == StateDiscarding && p.fwdDelayTimer.IsZero() {
				// No agreement path: the forward delay ladder carries the port
				// through Learning and Forwarding as on a shared link.
				p.fwdDelayTimer = now.Add(l.forwardDelay)
			}
		case RoleDesignated:
			switch {
			case p.edge:
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			case p.pointToPoint && p.agreed:
				p.state = StateForwarding
				p.fwdDelayTimer = time.Time{}
			default:
				// Without an agreement the port still forwards after two
				// forward delays, so a peer that never answers, a host for
				// one, does not leave the port dark for the run.
				if p.state == StateDiscarding && p.fwdDelayTimer.IsZero() {
					p.fwdDelayTimer = now.Add(l.forwardDelay)
				}
			}
		}

		if oldState != StateForwarding && p.state == StateForwarding {
			p.forwardTransitions++
			if !p.edge {
				l.raiseTopologyChange(t, p.name, now, flushes)
			}
		} else if oldState == StateForwarding && p.state != StateForwarding {
			if !p.edge {
				l.raiseTopologyChange(t, p.name, now, flushes)
			}
		}
	}

	if t.rootID != oldRootID || t.rootPathCost != oldRootCost || t.rootPort != oldRootPort {
		for _, name := range l.portNames {
			p := t.ports[name]
			if p.up && p.role == RoleDesignated && p.pointToPoint && p.state == StateDiscarding && !p.agreed {
				l.emit(t, p, now, emissionDesignated, &emissions)
			}
		}
	}

	return emissions
}

// designatedOrBlocked decides the role of a port that is up and not the root
// port: Alternate when a better bridge is designated on its segment, Backup
// when that bridge is this one through another port, else Designated.
func (l *Layer) designatedOrBlocked(t *tree, p *portState, now time.Time) Role {
	if p.rcvInfoValid && p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
		desig := priorityVector{
			rootID:       t.rootID,
			rootPathCost: t.rootPathCost,
			bridgeID:     l.bridgeID,
			portID:       p.portID,
		}
		rcv := priorityVector{
			rootID:       p.rcvRootID,
			rootPathCost: p.rcvRootPathCost,
			bridgeID:     p.rcvBridgeID,
			portID:       p.rcvPortID,
		}
		if compareVectors(rcv, desig) < 0 {
			if p.rcvBridgeID == l.bridgeID {
				return RoleBackup
			}

			return RoleAlternate
		}
	}

	return RoleDesignated
}

// LinkChange records a physical or administrative link transition on a port.
// A port coming up point-to-point transmits a proposal immediately in the
// returned emissions. A link down clears received information and moves the
// port to Disabled.
func (l *Layer) LinkChange(now time.Time, port string, up, pointToPoint bool, speedBPS uint64) Effects {
	t := l.cist()
	p, ok := t.ports[port]
	if !ok {
		return Effects{}
	}

	var flushes []string
	var emissions []Emission

	if t.helloTimer.IsZero() {
		t.helloTimer = now.Add(l.helloTime)
	}

	if !up {
		if !p.up {
			return Effects{}
		}
		oldState := p.state
		p.up = false
		p.role = RoleDisabled
		p.state = StateDiscarding
		p.rcvInfoValid = false
		p.agreed = false
		p.proposing = false
		p.pendingDesignated = false
		p.pendingAgreement = false
		p.fwdDelayTimer = time.Time{}
		// Both guard states clear here, which is what makes a link down and up
		// the recovery for BPDU guard. A port that comes back up holds no
		// expired information, so loop guard has nothing to trigger on either.
		p.bpduGuardDisabled = false
		p.loopInconsistent = false

		// The entries learned on the dead port are the ones certainly stale;
		// the topology change below flushes every other port.
		flushes = append(flushes, p.name)
		if oldState == StateForwarding && !p.edge {
			l.raiseTopologyChange(t, p.name, now, &flushes)
		}

		subEmissions := l.recompute(t, now, &flushes)
		emissions = append(emissions, subEmissions...)

		return Effects{
			Emissions: emissions,
			Flush:     flushes,
		}
	}

	p2p := pointToPoint
	switch p.cfg.PointToPoint {
	case PointToPointForceTrue:
		p2p = true
	case PointToPointForceFalse:
		p2p = false
	}
	cost := p.cfg.PathCost
	if cost == 0 {
		cost = DefaultPathCost(speedBPS)
	}
	// A report of the state the port already has is not a transition: two
	// callers may describe the same link, and re-entering a port that is up
	// would restart its handshake for nothing.
	if p.up && p.pointToPoint == p2p && p.pathCost == cost {
		return Effects{}
	}

	p.up = true
	p.pointToPoint = p2p
	p.pathCost = cost

	p.role = RoleDesignated
	p.agreed = false
	p.sendRSTP = true
	p.mdelayWhile = now.Add(MigrateTime)

	p.edge = p.adminEdge
	p.edgeDelayWhile = now.Add(l.edgeDelay(t, p))

	p.proposing = p.pointToPoint && !p.edge && p.sendRSTP

	if p.edge {
		p.state = StateForwarding
		p.forwardTransitions++
	} else {
		p.state = StateDiscarding
		if !p.pointToPoint {
			p.fwdDelayTimer = now.Add(l.forwardDelay)
		}
	}

	subEmissions := l.recompute(t, now, &flushes)
	emissions = append(emissions, subEmissions...)

	if p.pointToPoint && !p.edge && p.role == RoleDesignated && p.state == StateDiscarding {
		alreadyEmitted := false
		for _, e := range emissions {
			if e.Port == p.name {
				alreadyEmitted = true

				break
			}
		}
		if !alreadyEmitted && !p.pendingDesignated {
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// Receive processes an incoming BPDU received on a port.
func (l *Layer) Receive(now time.Time, port string, b BPDU) Effects {
	t := l.cist()
	p, ok := t.ports[port]
	if !ok || !p.up {
		return Effects{}
	}

	p.rxBPDUs++

	if t.helloTimer.IsZero() {
		t.helloTimer = now.Add(l.helloTime)
	}

	var flushes []string
	var emissions []Emission

	// BPDU guard exists to keep an unexpected bridge on an access port out of
	// the topology, so the frame that proves one is there disables the port
	// before anything reads the BPDU. Only a link down and up brings it back.
	if p.cfg.BPDUGuard && !p.bpduGuardDisabled {
		p.bpduGuardDisabled = true
		p.rcvInfoValid = false
		p.agreed = false
		p.proposing = false
		p.pendingDesignated = false
		p.pendingAgreement = false
		p.fwdDelayTimer = time.Time{}

		// The entries learned on the port are the ones certainly stale. The
		// topology change itself is left to recompute, which raises it from the
		// same transition with the same origin and timestamp; raising it here
		// as well would count one event twice.
		flushes = append(flushes, p.name)

		emissions = append(emissions, l.recompute(t, now, &flushes)...)

		return Effects{Emissions: emissions, Flush: flushes}
	}
	if p.bpduGuardDisabled {
		return Effects{}
	}

	// Any BPDU on the port is evidence the link carries traffic both ways,
	// which is the condition loop guard was waiting to see restored.
	p.loopInconsistent = false

	if (b.Type == BPDUTypeConfiguration || b.Type == BPDUTypeTopologyChangeNotification) && p.sendRSTP && !p.mdelayWhile.After(now) {
		p.sendRSTP = false
		p.mdelayWhile = now.Add(MigrateTime)
	} else if b.Type == BPDUTypeRapid && !p.sendRSTP && !p.mdelayWhile.After(now) {
		p.sendRSTP = true
		p.mdelayWhile = now.Add(MigrateTime)
	}

	wasAutoEdge := p.edge && !p.adminEdge
	p.edge = p.adminEdge
	p.edgeDelayWhile = now.Add(l.edgeDelay(t, p))

	if wasAutoEdge {
		if p.state == StateForwarding {
			l.raiseTopologyChange(t, p.name, now, &flushes)
		}
		p.state = StateDiscarding
		p.fwdDelayTimer = time.Time{}
		p.proposing = p.pointToPoint && p.sendRSTP
	}

	if b.Type == BPDUTypeTopologyChangeNotification {
		// Restricted TCN stops the change here. Setting the timer would carry
		// the flag out on this bridge's own BPDUs, which is the propagation the
		// guard denies, so neither the timer nor the flush runs.
		if !p.cfg.RestrictedTCN {
			t.topologyChangeTimer = now.Add(l.helloTime + time.Second)
			for _, name := range l.portNames {
				if name != port && !slices.Contains(flushes, name) {
					flushes = append(flushes, name)
				}
			}
		}

		subEmissions := l.recompute(t, now, &flushes)
		emissions = append(emissions, subEmissions...)

		return Effects{
			Emissions: emissions,
			Flush:     flushes,
		}
	}

	// IEEE 802.1Q treats message age as a hop count bounded by the max age the
	// BPDU itself carries, not by this bridge's configured one: the received
	// value is the root's, and the fabric builds bridges with differing timers.
	// Information that has reached the bound is discarded rather than stored,
	// so a BPDU naming a root that no longer exists stops refreshing the timer
	// on every hop and the port's own information ages out.
	if b.MessageAge+time.Second > b.MaxAge {
		subEmissions := l.recompute(t, now, &flushes)
		emissions = append(emissions, subEmissions...)

		return Effects{
			Emissions: emissions,
			Flush:     flushes,
		}
	}

	incoming := priorityVector{
		rootID:       b.RootID,
		rootPathCost: b.RootPathCost,
		bridgeID:     b.BridgeID,
		portID:       b.PortID,
	}

	sameSource := p.rcvInfoValid && (b.BridgeID == p.rcvBridgeID && b.PortID == p.rcvPortID)
	isSuperior := false
	if !p.rcvInfoValid {
		isSuperior = true
	} else {
		stored := priorityVector{
			rootID:       p.rcvRootID,
			rootPathCost: p.rcvRootPathCost,
			bridgeID:     p.rcvBridgeID,
			portID:       p.rcvPortID,
		}
		if compareVectors(incoming, stored) < 0 {
			isSuperior = true
		}
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
	}

	if p.role == RoleDesignated && b.Agreement() {
		p.agreed = true
		p.proposing = false
		if p.state != StateForwarding {
			p.state = StateForwarding
			p.forwardTransitions++
			if !p.edge {
				l.raiseTopologyChange(t, p.name, now, &flushes)
			}
		}
	}

	if b.TopologyChange() && !p.cfg.RestrictedTCN {
		t.topologyChangeTimer = now.Add(l.helloTime + time.Second)
		for _, name := range l.portNames {
			if name != port && !slices.Contains(flushes, name) {
				flushes = append(flushes, name)
			}
		}
	}

	subEmissions := l.recompute(t, now, &flushes)
	emissions = append(emissions, subEmissions...)

	if b.Proposal() && (p.role == RoleRoot || p.role == RoleAlternate) {
		for _, otherName := range l.portNames {
			if otherName == port {
				continue
			}
			otherP := t.ports[otherName]
			if otherP.role == RoleDesignated && !otherP.edge {
				otherP.agreed = false
				otherP.proposing = otherP.pointToPoint && otherP.sendRSTP
				if otherP.state != StateDiscarding {
					wasFwd := otherP.state == StateForwarding
					otherP.state = StateDiscarding
					if wasFwd {
						l.raiseTopologyChange(t, otherP.name, now, &flushes)
					}
				}
			}
		}

		if p.role == RoleRoot && p.pointToPoint && l.isSynced(t, p.name) && p.sendRSTP {
			if p.state != StateForwarding {
				p.state = StateForwarding
				p.forwardTransitions++
				if !p.edge {
					l.raiseTopologyChange(t, p.name, now, &flushes)
				}
			}
		}

		l.emit(t, p, now, emissionAgreement, &emissions)
	} else if p.role == RoleDesignated && !b.Agreement() {
		desig := priorityVector{
			rootID:       t.rootID,
			rootPathCost: t.rootPathCost,
			bridgeID:     l.bridgeID,
			portID:       p.portID,
		}
		if compareVectors(incoming, desig) > 0 {
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// Wake advances timer-driven state to now, firing due hellos, forward delays,
// topology change timers, and information age-outs.
func (l *Layer) Wake(now time.Time) Effects {
	t := l.cist()

	var flushes []string
	var emissions []Emission

	for _, name := range l.portNames {
		p := t.ports[name]
		if p.cfg.AutoEdge && p.sendRSTP && p.up && p.role == RoleDesignated &&
			p.state == StateDiscarding && p.pointToPoint && p.proposing &&
			!p.edgeDelayWhile.IsZero() && !p.edgeDelayWhile.After(now) {
			p.edge = true
			p.edgeDelayWhile = time.Time{}
			p.state = StateForwarding
			p.fwdDelayTimer = time.Time{}
			p.proposing = false
			p.forwardTransitions++
		}
	}

	for _, name := range l.portNames {
		p := t.ports[name]
		if !p.up || p.txTick.IsZero() || p.txTick.After(now) {
			continue
		}
		// A held kind belongs to the role that requested it; released under
		// another role it would be an agreement from a designated port or a
		// designated claim from a blocked one.
		if p.pendingAgreement {
			p.pendingAgreement = false
			if p.role == RoleRoot || p.role == RoleAlternate {
				l.emit(t, p, now, emissionAgreement, &emissions)
			}
		}
		if p.pendingDesignated {
			p.pendingDesignated = false
			if p.role == RoleDesignated {
				l.emit(t, p, now, emissionDesignated, &emissions)
			}
		}
	}

	if !t.helloTimer.IsZero() && !t.helloTimer.After(now) {
		t.helloTimer = now.Add(l.helloTime)
		for _, name := range l.portNames {
			p := t.ports[name]
			if p.up && p.role == RoleDesignated {
				l.emit(t, p, now, emissionDesignated, &emissions)
			}
		}
	}

	stateChanged := false
	for _, name := range l.portNames {
		p := t.ports[name]
		if !p.fwdDelayTimer.IsZero() && !p.fwdDelayTimer.After(now) {
			p.fwdDelayTimer = time.Time{}
			switch p.role {
			case RoleDesignated:
				switch p.state {
				case StateDiscarding:
					p.state = StateLearning
					p.fwdDelayTimer = now.Add(l.forwardDelay)
				case StateLearning:
					p.state = StateForwarding
					p.forwardTransitions++
					stateChanged = true
					if !p.edge {
						l.raiseTopologyChange(t, p.name, now, &flushes)
					}
				case StateForwarding:
				}
			case RoleRoot:
				switch p.state {
				case StateDiscarding:
					p.state = StateLearning
					p.fwdDelayTimer = now.Add(l.forwardDelay)
				case StateLearning:
					p.state = StateForwarding
					p.forwardTransitions++
					stateChanged = true
					if !p.edge {
						l.raiseTopologyChange(t, p.name, now, &flushes)
					}
				case StateForwarding:
				}
			case RoleDisabled, RoleAlternate, RoleBackup:
			}
		}
	}

	agedOut := false
	for _, name := range l.portNames {
		p := t.ports[name]
		if p.rcvInfoValid && !p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
			// Loop guard triggers on silence: the port held a non-designated
			// role and heard nothing for three hello times. A port whose peer
			// keeps sending BPDUs too old to store is not covered, because any
			// received BPDU clears the state before this runs again.
			if p.loopGuardWatches() &&
				(p.role == RoleRoot || p.role == RoleAlternate || p.role == RoleBackup) {
				p.loopInconsistent = true
			}
			p.rcvInfoValid = false
			agedOut = true
		}
	}

	if !t.topologyChangeTimer.IsZero() && !t.topologyChangeTimer.After(now) {
		t.topologyChangeTimer = time.Time{}
	}

	if agedOut || stateChanged {
		subEmissions := l.recompute(t, now, &flushes)
		emissions = append(emissions, subEmissions...)
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}
