package stp

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
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

// PortInfo summarizes the runtime spanning tree status of one port.
type PortInfo struct {
	Role               Role
	State              State
	Priority           uint8
	PathCost           uint32
	DesignatedRoot     BridgeID
	Designated         BridgeID
	DesignatedPort     uint16
	DesignatedCost     uint32
	PointToPoint       bool
	Edge               bool
	ForwardTransitions uint64
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

	ports map[string]*portState

	helloTimer time.Time

	rootID       BridgeID
	rootPathCost uint32
	rootPort     string

	topologyChangeCount uint64
	lastTopologyChange  time.Time
	topologyChangeTimer time.Time
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
}

func (p *portState) clone() *portState {
	cp := *p

	return &cp
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

// New constructs a spanning tree layer from the given configuration and port
// table. A port identifier combines the 8-bit port priority in the high byte
// with the 1-based index of the port in Ports' sorted key order in the low byte.
func New(cfg Config, ports port.Table) *Layer {
	prio := effectivePriority(cfg.Priority)
	hello := effectiveHelloTime(cfg.HelloTime)
	maxAge := effectiveMaxAge(cfg.MaxAge)
	fwdDelay := effectiveForwardDelay(cfg.ForwardDelay)

	bridgeID := BridgeID{Priority: prio, Address: cfg.Address}

	l := &Layer{
		cfg:          cfg,
		priority:     prio,
		address:      cfg.Address,
		bridgeID:     bridgeID,
		helloTime:    hello,
		maxAge:       maxAge,
		forwardDelay: fwdDelay,
		ports:        make(map[string]*portState, len(cfg.Ports)),
		rootID:       bridgeID,
		rootPathCost: 0,
		rootPort:     "",
	}

	sortedNames := sortedKeys(cfg.Ports)
	for i, name := range sortedNames {
		pCfg := cfg.Ports[name]
		portPrio := effectivePortPriority(pCfg.Priority)
		portID := (uint16(portPrio) << 8) | uint16(i+1)

		cost := pCfg.PathCost
		if cost == 0 {
			cost = DefaultPathCost(0)
		}

		l.ports[name] = &portState{
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
		}
	}

	return l
}

// Clone creates an independent deep copy of the spanning tree layer, preserving
// all ports, timers, and elected roles.
func (l *Layer) Clone() *Layer {
	cp := &Layer{
		cfg:                 l.cfg,
		priority:            l.priority,
		address:             l.address,
		bridgeID:            l.bridgeID,
		helloTime:           l.helloTime,
		maxAge:              l.maxAge,
		forwardDelay:        l.forwardDelay,
		ports:               make(map[string]*portState, len(l.ports)),
		helloTimer:          l.helloTimer,
		rootID:              l.rootID,
		rootPathCost:        l.rootPathCost,
		rootPort:            l.rootPort,
		topologyChangeCount: l.topologyChangeCount,
		lastTopologyChange:  l.lastTopologyChange,
		topologyChangeTimer: l.topologyChangeTimer,
	}

	cp.cfg.Ports = make(map[string]Port, len(l.cfg.Ports))
	for k, v := range l.cfg.Ports {
		cp.cfg.Ports[k] = v
	}
	for k, v := range l.ports {
		cp.ports[k] = v.clone()
	}

	return cp
}

// Learns reports whether the named port learns MAC addresses into the filtering database.
// An untracked port always learns.
func (l *Layer) Learns(port string) bool {
	p, ok := l.ports[port]
	if !ok {
		return true
	}

	return p.state == StateLearning || p.state == StateForwarding
}

// Forwards reports whether the named port forwards traffic. An untracked port always forwards.
func (l *Layer) Forwards(port string) bool {
	p, ok := l.ports[port]
	if !ok {
		return true
	}

	return p.state == StateForwarding
}

// Root returns the elected root bridge identifier, the path cost to reach it,
// and the interface name of the root port. When this bridge is root, the root
// port name is empty.
func (l *Layer) Root() (BridgeID, uint32, string) {
	return l.rootID, l.rootPathCost, l.rootPort
}

// TopologyChanges returns the total number of detected topology changes and the
// timestamp of the most recent change.
func (l *Layer) TopologyChanges() (uint64, time.Time) {
	return l.topologyChangeCount, l.lastTopologyChange
}

// BridgeID returns this bridge's identifier with the priority in effect.
func (l *Layer) BridgeID() BridgeID {
	return l.bridgeID
}

// Times returns the max age, hello time, and forward delay in force: the root's
// values as received on the root port, or this bridge's own while it is root.
func (l *Layer) Times() (maxAge, hello, forwardDelay time.Duration) {
	if l.rootPort != "" {
		if rp, ok := l.ports[l.rootPort]; ok && rp.rcvInfoValid {
			return rp.rcvMaxAge, rp.rcvHelloTime, rp.rcvForwardDelay
		}
	}

	return l.maxAge, l.helloTime, l.forwardDelay
}

// PortInfo returns runtime spanning tree information for the named port. If the
// port is not tracked by the layer, PortInfo returns a zero value.
func (l *Layer) PortInfo(port string) PortInfo {
	p, ok := l.ports[port]
	if !ok {
		return PortInfo{}
	}

	var desigRoot, desig BridgeID
	var desigPort uint16
	var desigCost uint32

	switch p.role {
	case RoleDesignated:
		desigRoot = l.rootID
		desig = l.bridgeID
		desigPort = p.portID
		desigCost = l.rootPathCost
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
		Priority:           uint8(p.portID >> 8),
		PathCost:           p.pathCost,
		DesignatedRoot:     desigRoot,
		Designated:         desig,
		DesignatedPort:     desigPort,
		DesignatedCost:     desigCost,
		PointToPoint:       p.pointToPoint,
		Edge:               p.edge,
		ForwardTransitions: p.forwardTransitions,
	}
}

// NextWake returns the earliest scheduled time at which the layer needs to be
// woken, and reports whether any timer is currently active.
func (l *Layer) NextWake() (time.Time, bool) {
	var next time.Time
	hasTimer := false

	update := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if !hasTimer || t.Before(next) {
			next = t
			hasTimer = true
		}
	}

	update(l.helloTimer)
	update(l.topologyChangeTimer)

	for _, p := range l.ports {
		update(p.fwdDelayTimer)
		if p.rcvInfoValid {
			update(p.rcvTime.Add(3 * p.rcvHelloTime))
		}
	}

	return next, hasTimer
}

func (l *Layer) isSynced(rootPort string) bool {
	for name, p := range l.ports {
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

func (l *Layer) raiseTopologyChange(originPort string, now time.Time, flushes *[]string) {
	l.topologyChangeCount++
	l.lastTopologyChange = now
	l.topologyChangeTimer = now.Add(l.helloTime + time.Second)

	for _, name := range sortedKeys(l.ports) {
		if name == originPort {
			continue
		}
		if !slices.Contains(*flushes, name) {
			*flushes = append(*flushes, name)
		}
	}
}

func (l *Layer) makeBPDU(p *portState, now time.Time, proposal bool) BPDU {
	var msgAge time.Duration
	maxAge, hello, fwdDelay := l.Times()

	if l.rootPort != "" {
		if rp, ok := l.ports[l.rootPort]; ok && rp.rcvInfoValid {
			msgAge = rp.rcvMessageAge + time.Second
		}
	}

	b := BPDU{
		RootID:       l.rootID,
		RootPathCost: l.rootPathCost,
		BridgeID:     l.bridgeID,
		PortID:       p.portID,
		MessageAge:   msgAge,
		MaxAge:       maxAge,
		HelloTime:    hello,
		ForwardDelay: fwdDelay,
	}

	b.SetRole(p.role)
	b.SetProposal(proposal)
	b.SetLearning(p.state == StateLearning || p.state == StateForwarding)
	b.SetForwarding(p.state == StateForwarding)
	if !l.topologyChangeTimer.IsZero() && l.topologyChangeTimer.After(now) {
		b.SetTopologyChange(true)
	}

	return b
}

func (l *Layer) makeAgreementBPDU(p *portState, now time.Time) BPDU {
	b := l.makeBPDU(p, now, false)
	b.SetRole(p.role)
	b.SetAgreement(true)
	b.SetProposal(false)
	b.SetLearning(p.state == StateLearning || p.state == StateForwarding)
	b.SetForwarding(p.state == StateForwarding)

	return b
}

func (l *Layer) recompute(now time.Time, flushes *[]string) []Emission {
	var emissions []Emission

	oldRootID := l.rootID
	oldRootCost := l.rootPathCost
	oldRootPort := l.rootPort

	bestVector := priorityVector{
		rootID:       l.bridgeID,
		rootPathCost: 0,
		bridgeID:     l.bridgeID,
		portID:       0,
	}
	bestPort := ""
	bestRcvPortID := uint16(0)

	for _, name := range sortedKeys(l.ports) {
		p := l.ports[name]
		if !p.up || !p.rcvInfoValid {
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
		l.rootID = l.bridgeID
		l.rootPathCost = 0
		l.rootPort = ""
	} else {
		l.rootID = bestVector.rootID
		l.rootPathCost = bestVector.rootPathCost
		l.rootPort = bestPort
	}

	for _, name := range sortedKeys(l.ports) {
		p := l.ports[name]
		oldRole := p.role
		switch {
		case !p.up:
			p.role = RoleDisabled
		case name == l.rootPort:
			p.role = RoleRoot
		default:
			p.role = l.designatedOrBlocked(p, now)
		}
		// An agreement belongs to the Designated role that earned it; a port
		// that leaves the role and comes back must propose again, or it would
		// forward without a handshake on a link whose peer never agreed.
		if p.role != oldRole && p.role != RoleDesignated {
			p.agreed = false
		}
	}

	for _, name := range sortedKeys(l.ports) {
		p := l.ports[name]
		oldState := p.state

		switch p.role {
		case RoleDisabled, RoleAlternate, RoleBackup:
			p.state = StateDiscarding
			p.fwdDelayTimer = time.Time{}
		case RoleRoot:
			if p.pointToPoint && l.isSynced(p.name) {
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
				l.raiseTopologyChange(p.name, now, flushes)
			}
		} else if oldState == StateForwarding && p.state != StateForwarding {
			if !p.edge {
				l.raiseTopologyChange(p.name, now, flushes)
			}
		}
	}

	if l.rootID != oldRootID || l.rootPathCost != oldRootCost || l.rootPort != oldRootPort {
		for _, name := range sortedKeys(l.ports) {
			p := l.ports[name]
			if p.up && p.role == RoleDesignated && p.pointToPoint && p.state == StateDiscarding && !p.agreed {
				bpdu := l.makeBPDU(p, now, true)
				emissions = append(emissions, Emission{Port: p.name, Frame: Encode(bpdu, l.address)})
			}
		}
	}

	return emissions
}

// designatedOrBlocked decides the role of a port that is up and not the root
// port: Alternate when a better bridge is designated on its segment, Backup
// when that bridge is this one through another port, else Designated.
func (l *Layer) designatedOrBlocked(p *portState, now time.Time) Role {
	if p.rcvInfoValid && p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
		desig := priorityVector{
			rootID:       l.rootID,
			rootPathCost: l.rootPathCost,
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
	p, ok := l.ports[port]
	if !ok {
		return Effects{}
	}

	var flushes []string
	var emissions []Emission

	if l.helloTimer.IsZero() {
		l.helloTimer = now.Add(l.helloTime)
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
		p.fwdDelayTimer = time.Time{}

		// The entries learned on the dead port are the ones certainly stale;
		// the topology change below flushes every other port.
		flushes = append(flushes, p.name)
		if oldState == StateForwarding && !p.edge {
			l.raiseTopologyChange(p.name, now, &flushes)
		}

		subEmissions := l.recompute(now, &flushes)
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
	p.proposing = p.pointToPoint && !p.edge

	if p.edge {
		p.state = StateForwarding
		p.forwardTransitions++
	} else {
		p.state = StateDiscarding
		if !p.pointToPoint {
			p.fwdDelayTimer = now.Add(l.forwardDelay)
		}
	}

	subEmissions := l.recompute(now, &flushes)
	emissions = append(emissions, subEmissions...)

	if p.pointToPoint && !p.edge && p.role == RoleDesignated && p.state == StateDiscarding {
		alreadyEmitted := false
		for _, e := range emissions {
			if e.Port == p.name {
				alreadyEmitted = true

				break
			}
		}
		if !alreadyEmitted {
			bpdu := l.makeBPDU(p, now, true)
			emissions = append(emissions, Emission{Port: p.name, Frame: Encode(bpdu, l.address)})
		}
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// Receive processes an incoming BPDU received on a port.
func (l *Layer) Receive(now time.Time, port string, b BPDU) Effects {
	p, ok := l.ports[port]
	if !ok || !p.up {
		return Effects{}
	}

	if l.helloTimer.IsZero() {
		l.helloTimer = now.Add(l.helloTime)
	}

	var flushes []string
	var emissions []Emission

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
				l.raiseTopologyChange(p.name, now, &flushes)
			}
		}
	}

	if b.TopologyChange() {
		l.topologyChangeTimer = now.Add(l.helloTime + time.Second)
		for _, name := range sortedKeys(l.ports) {
			if name != port && !slices.Contains(flushes, name) {
				flushes = append(flushes, name)
			}
		}
	}

	subEmissions := l.recompute(now, &flushes)
	emissions = append(emissions, subEmissions...)

	if b.Proposal() && (p.role == RoleRoot || p.role == RoleAlternate) {
		for _, otherName := range sortedKeys(l.ports) {
			if otherName == port {
				continue
			}
			otherP := l.ports[otherName]
			if otherP.role == RoleDesignated && !otherP.edge {
				otherP.agreed = false
				otherP.proposing = otherP.pointToPoint
				if otherP.state != StateDiscarding {
					wasFwd := otherP.state == StateForwarding
					otherP.state = StateDiscarding
					if wasFwd {
						l.raiseTopologyChange(otherP.name, now, &flushes)
					}
				}
			}
		}

		if p.role == RoleRoot && p.pointToPoint && l.isSynced(p.name) {
			if p.state != StateForwarding {
				p.state = StateForwarding
				p.forwardTransitions++
				if !p.edge {
					l.raiseTopologyChange(p.name, now, &flushes)
				}
			}
		}

		agree := l.makeAgreementBPDU(p, now)
		emissions = append(emissions, Emission{Port: port, Frame: Encode(agree, l.address)})
	} else if p.role == RoleDesignated && !b.Agreement() {
		desig := priorityVector{
			rootID:       l.rootID,
			rootPathCost: l.rootPathCost,
			bridgeID:     l.bridgeID,
			portID:       p.portID,
		}
		if compareVectors(incoming, desig) > 0 {
			bpdu := l.makeBPDU(p, now, p.pointToPoint && p.state == StateDiscarding && !p.agreed)
			emissions = append(emissions, Emission{Port: port, Frame: Encode(bpdu, l.address)})
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
	var flushes []string
	var emissions []Emission

	if !l.helloTimer.IsZero() && !l.helloTimer.After(now) {
		l.helloTimer = now.Add(l.helloTime)
		for _, name := range sortedKeys(l.ports) {
			p := l.ports[name]
			if p.up && p.role == RoleDesignated {
				proposal := p.pointToPoint && p.state == StateDiscarding && !p.agreed
				bpdu := l.makeBPDU(p, now, proposal)
				emissions = append(emissions, Emission{Port: p.name, Frame: Encode(bpdu, l.address)})
			}
		}
	}

	stateChanged := false
	for _, name := range sortedKeys(l.ports) {
		p := l.ports[name]
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
						l.raiseTopologyChange(p.name, now, &flushes)
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
						l.raiseTopologyChange(p.name, now, &flushes)
					}
				case StateForwarding:
				}
			case RoleDisabled, RoleAlternate, RoleBackup:
			}
		}
	}

	agedOut := false
	for _, name := range sortedKeys(l.ports) {
		p := l.ports[name]
		if p.rcvInfoValid && !p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
			p.rcvInfoValid = false
			agedOut = true
		}
	}

	if !l.topologyChangeTimer.IsZero() && !l.topologyChangeTimer.After(now) {
		l.topologyChangeTimer = time.Time{}
	}

	if agedOut || stateChanged {
		subEmissions := l.recompute(now, &flushes)
		emissions = append(emissions, subEmissions...)
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}
