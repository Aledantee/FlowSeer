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
// A zero VID means the frame leaves as the layer built it, untagged and with
// no VLAN membership check. A non-zero VID names the VLAN the frame rides, and
// the switch resolves the tag through the port's ordinary egress rules, which
// is where the native-versus-tagged decision for an SSTP BPDU is made.
type Emission struct {
	Port  string
	VID   vlan.ID
	Frame ethernet.Frame
}

// Effects lists frames to emit and ports whose learned forwarding table entries
// must be flushed as a result of a spanning tree transition.
type Effects struct {
	Emissions []Emission
	Flush     []FlushTarget
}

// FlushTarget names a port whose learned forwarding table entries must be
// flushed, and which FIDs on it are stale. An empty FIDs means every FID: a
// link going down leaves every entry on it stale regardless of tree, and a
// topology change raised from the CIST reaches VLANs no MSTI claims, a set
// this layer never enumerates.
type FlushTarget struct {
	Port string
	FIDs []vlan.ID
}

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
	MSTID              MSTID
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

	// treeVLANs lists, sorted and deduplicated, the VLANs each MSTI carries.
	// It has no entry for the CIST: the CIST forwards every VLAN no MSTI
	// claims, a set this layer never enumerates, so a CIST-raised flush
	// always carries every FID instead of a derived list.
	treeVLANs map[treeID][]vlan.ID

	// treeOrder lists the trees in the deterministic order every walk visits
	// them: the CIST first, then MSTIDs ascending. A map range would let
	// Effects order vary between runs of the same input, which the corpus's
	// deterministic-re-execution test would catch.
	treeOrder []treeID

	// portTx holds the transmit budget for every port. Outside PVST mode it
	// is bridge-global, keyed under cistID for the same reason portNames is:
	// the budget IEEE 802.1Q meters belongs to the port, not to a tree running
	// on it, and MSTP spends it once per port because only the CIST emits. A
	// PVST bridge emits once per VLAN per port, so there the budget is keyed
	// per tree as well, or a bridge carrying more VLANs than its hold count
	// would starve the VLANs that sort last. Layer.tx resolves the key.
	portTx map[txKey]*portTx

	// mst is the region configuration when this bridge runs MSTP, and nil
	// when it runs plain RSTP with the CIST as its only tree.
	mst *MST

	// pvst is the per-VLAN tree configuration when this bridge runs PVST, and
	// nil otherwise. Config.Validate refuses a configuration setting both mst
	// and pvst, so the two modes never overlap.
	pvst *PVST

	// configID is this bridge's own MST configuration identifier, computed
	// once from mst at construction. A received MST BPDU is internal when its
	// ConfigID equals this one.
	configID *ConfigID
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
	rcvRegionalRootID       BridgeID
	rcvInternalRootPathCost uint32
	rcvRemainingHops        uint8

	role  Role
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

// blockReason names the guard holding the port out of the active topology.
// BPDU guard outranks the rest: it disables the port outright, so nothing
// below it can be the decisive reason. A PVID-inconsistent port is by
// definition receiving BPDUs, which is what clears loopInconsistent on every
// receive, so those two cannot both hold after a receive and the order
// between them only fixes what a reader sees should that stop being true.
func (p *portState) blockReason() BlockReason {
	switch {
	case p.bpduGuardDisabled:
		return BlockReasonBPDUGuard
	case p.pvidInconsistent:
		return BlockReasonPVIDInconsistent
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
	rootID               BridgeID
	externalRootPathCost uint32
	regionalRootID       BridgeID
	internalRootPathCost uint32
	bridgeID             BridgeID
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

	// In PVST mode VLAN 1's tree occupies the CIST slot: in PVST+ it is the
	// common spanning tree a neighboring RSTP or MSTP bridge converges with,
	// and Root, PortInfo, TopologyChanges and Times all answer from l.cist(),
	// so putting it there keeps every bridge-level accessor answering about
	// the common tree in all three modes.
	cistVID := vlan.ID(0)
	cistBridgeID := bridgeID
	if cfg.PVST != nil {
		cistVID = 1
		cistBridgeID = pvstBridgeID(cfg.PVST.Trees[1], cistVID, prio, cfg.Address)
	}

	cist := &tree{
		id:           cistID,
		vid:          cistVID,
		bridgeID:     cistBridgeID,
		rootID:       cistBridgeID,
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
		treeVLANs:    make(map[treeID][]vlan.ID),
		treeOrder:    []treeID{cistID},
		portTx:       make(map[txKey]*portTx, len(sortedNames)),
		mst:          cfg.MST,
		pvst:         cfg.PVST,
	}

	if cfg.MST != nil {
		cid := cfg.MST.ConfigID()
		l.configID = &cid
	}

	// VLAN 1's own per-port settings apply to the CIST in PVST mode, since
	// that slot is VLAN 1's tree.
	var cistTreePorts map[string]InstancePort
	if cfg.PVST != nil {
		cistTreePorts = cfg.PVST.Trees[1].Ports
	}

	for i, name := range sortedNames {
		pCfg := cfg.Ports[name]
		portPrio := effectivePortPriority(pCfg.Priority, pCfg.PriorityPresent)

		cost := pCfg.PathCost
		if cost == 0 {
			cost = DefaultPathCost(0)
		}
		linkCost := cost
		fixed := false

		if treePort, ok := cistTreePorts[name]; ok {
			if treePort.PriorityPresent {
				portPrio = treePort.Priority
			}
			if treePort.PathCost != 0 {
				cost = treePort.PathCost
				fixed = true
			}
		}

		cist.ports[name] = &portState{
			name:          name,
			cfg:           pCfg,
			portID:        (uint16(portPrio) << 8) | uint16(i+1),
			pathCost:      cost,
			linkPathCost:  linkCost,
			pathCostFixed: fixed,
			adminEdge:     pCfg.AdminEdge,
			edge:          pCfg.AdminEdge,
			pointToPoint:  false,
			up:            false,
			role:          RoleDisabled,
			state:         StateDiscarding,
			sendRSTP:      true,
		}
	}

	if cfg.MST != nil {
		for _, mstid := range sortedMSTIDs(cfg.MST.Instances) {
			inst := cfg.MST.Instances[mstid]
			l.addTree(treeID(mstid), 0, mstiBridgeID(inst, mstid, cfg.Address), inst.Ports, sortedNames)
			l.treeOrder = append(l.treeOrder, treeID(mstid))
			vids := slices.Clone(inst.VLANs)
			slices.Sort(vids)
			l.treeVLANs[treeID(mstid)] = slices.Compact(vids)
			for _, vid := range inst.VLANs {
				l.vidToTree[vid] = treeID(mstid)
			}
		}
	}

	if cfg.PVST != nil {
		// VLAN 1 is registered like every other VLAN rather than left to
		// treeFor's fallback: in PVST mode its tree carries exactly VLAN 1,
		// so a topology change on it stales VLAN 1 alone, where the CIST's
		// empty FID set in MSTP means every VLAN no MSTI claims.
		l.vidToTree[1] = cistID
		l.treeVLANs[cistID] = []vlan.ID{1}

		for _, vid := range sortedVLANIDs(cfg.PVST.Trees) {
			if vid == 1 {
				continue
			}
			cfgTree := cfg.PVST.Trees[vid]
			l.addTree(treeID(vid), vid, pvstBridgeID(cfgTree, vid, prio, cfg.Address), cfgTree.Ports, sortedNames)
			l.treeOrder = append(l.treeOrder, treeID(vid))
			l.treeVLANs[treeID(vid)] = []vlan.ID{vid}
			l.vidToTree[vid] = treeID(vid)
		}
	}

	for _, id := range l.treeOrder {
		for _, name := range sortedNames {
			key := l.txKeyFor(l.trees[id], name)
			if _, ok := l.portTx[key]; !ok {
				l.portTx[key] = &portTx{}
			}
		}
	}

	return l
}

// mstiBridgeID is an MST instance's own bridge identifier: the instance
// priority in the most significant 4 bits and the MSTID in the low 12 bits of
// the system-ID extension (MSTP clause 13.7).
func mstiBridgeID(inst Instance, mstid MSTID, address netaddr.MAC) BridgeID {
	return BridgeID{
		Priority: (inst.Priority & 0xF000) | uint16(mstid),
		Address:  address,
	}
}

// pvstBridgeID is a per-VLAN tree's own bridge identifier, carrying the VLAN
// in the system-ID extension the way an MSTI carries its MSTID. That is what
// Config.Validate's multiple-of-4096 rule on a tree priority reserves the low
// 12 bits for, and what a PVST+ capture shows on the wire. A tree that names
// no priority of its own falls back to the bridge's, which PVST.Normalize has
// already filled in for a Layer built through New.
func pvstBridgeID(t Tree, vid vlan.ID, bridgePriority uint16, address netaddr.MAC) BridgeID {
	priority := bridgePriority
	if t.PriorityPresent {
		priority = t.Priority
	}

	return BridgeID{
		Priority: (priority & 0xF000) | uint16(vid),
		Address:  address,
	}
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

// addTree builds and registers one tree beside the CIST: an MST instance's or
// a PVST VLAN's. treePorts carries that tree's own per-port overrides. Each
// port's identifier reuses the CIST's index half so it stays bridge-global;
// only its priority half, and its path cost, can differ per tree.
func (l *Layer) addTree(id treeID, vid vlan.ID, bridgeID BridgeID, treePorts map[string]InstancePort, sortedNames []string) {
	t := &tree{
		id:           id,
		vid:          vid,
		bridgeID:     bridgeID,
		rootID:       bridgeID,
		rootPathCost: 0,
		rootPort:     "",
		ports:        make(map[string]*portState, len(sortedNames)),
	}

	for i, name := range sortedNames {
		pCfg := l.cfg.Ports[name]
		treePort, hasTreePort := treePorts[name]

		portPrio := effectivePortPriority(pCfg.Priority, pCfg.PriorityPresent)
		if hasTreePort && treePort.PriorityPresent {
			portPrio = treePort.Priority
		}

		cost := DefaultPathCost(0)
		fixed := false
		if hasTreePort && treePort.PathCost != 0 {
			cost = treePort.PathCost
			fixed = true
		}

		t.ports[name] = &portState{
			name:          name,
			cfg:           pCfg,
			portID:        (uint16(portPrio) << 8) | uint16(i+1),
			pathCost:      cost,
			pathCostFixed: fixed,
			adminEdge:     pCfg.AdminEdge,
			edge:          pCfg.AdminEdge,
			pointToPoint:  false,
			up:            false,
			role:          RoleDisabled,
			state:         StateDiscarding,
			sendRSTP:      true,
		}
	}

	l.trees[id] = t
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
		treeVLANs:    make(map[treeID][]vlan.ID, len(l.treeVLANs)),
		treeOrder:    slices.Clone(l.treeOrder),
		portTx:       make(map[txKey]*portTx, len(l.portTx)),
	}

	cp.cfg.Ports = make(map[string]Port, len(l.cfg.Ports))
	for k, v := range l.cfg.Ports {
		cp.cfg.Ports[k] = v
	}
	if l.mst != nil {
		mst := l.mst.Clone()
		cp.mst = &mst
		cp.cfg.MST = &mst
	}
	if l.pvst != nil {
		pvst := l.pvst.Clone()
		cp.pvst = &pvst
		cp.cfg.PVST = &pvst
	}
	if l.configID != nil {
		cid := *l.configID
		cp.configID = &cid
	}
	for id, t := range l.trees {
		cp.trees[id] = t.clone()
	}
	for vid, id := range l.vidToTree {
		cp.vidToTree[vid] = id
	}
	for id, vids := range l.treeVLANs {
		cp.treeVLANs[id] = slices.Clone(vids)
	}
	for key, tx := range l.portTx {
		cp.portTx[key] = tx.clone()
	}

	return cp
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

// BridgeID returns this bridge's identifier on the common tree, with the
// priority in effect. Outside PVST mode that is the bridge's only identifier;
// inside it, it is VLAN 1's, and every other VLAN's carries its own VLAN in
// the system-ID extension.
func (l *Layer) BridgeID() BridgeID {
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

// InstancePortInfo returns runtime spanning tree information for the named
// port within the given MST instance. It returns a zero value when the
// instance or the port is not tracked by the layer, which is also what a
// plain RSTP bridge (no MST configured) answers for any nonzero MSTID.
func (l *Layer) InstancePortInfo(mstid MSTID, port string) PortInfo {
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
// InstancePortInfo answers for an unknown MSTID.
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
	p, ok := l.cist().ports[port]
	if !ok {
		return false
	}

	return p.pvstBoundary
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

	var desigRoot, desig BridgeID
	var desigPort uint16
	var desigCost uint32

	switch p.role {
	case RoleDesignated:
		desigRoot = t.rootID
		desig = t.bridgeID
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
		MSTID:              MSTID(t.id),
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

// BadBPDU records that a frame received on the named port could not be
// decoded as a BPDU. It is recorded against every tree running on the port,
// since a frame that fails to decode is bad evidence for every instance
// alike. An untracked port is ignored.
func (l *Layer) BadBPDU(port string) {
	for _, id := range l.treeOrder {
		if p, ok := l.trees[id].ports[port]; ok {
			p.badBPDUs++
		}
	}
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

	var flushes []FlushTarget

	emissions := l.recomputeAll(now, &flushes)

	if p.role == RoleDesignated && p.up {
		alreadyEmitted := false
		for _, e := range emissions {
			// Under PVST every tree can emit on this port; only a match on
			// this tree's own VID is evidence that this call's own recompute
			// already sent the CIST's proposal, not some other VLAN's.
			if e.Port == p.name && e.VID == t.vid {
				alreadyEmitted = true
				break
			}
		}
		if !alreadyEmitted && !l.tx(t, p.name).pendingDesignated {
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
			tx := l.tx(t, p.name)
			if (tx.pendingAgreement || tx.pendingDesignated) && !tx.tick.IsZero() {
				update(tx.tick)
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
	tx := l.tx(t, p.name)

	for tx.count > 0 && !tx.tick.After(now) {
		tx.count--
		tx.tick = tx.tick.Add(time.Second)
	}
	if tx.count == 0 {
		tx.tick = time.Time{}
	}

	if tx.count < int(l.txHoldCount) {
		var bpdu BPDU
		switch kind {
		case emissionDesignated:
			proposal := p.pointToPoint && p.state == StateDiscarding && !p.agreed && p.sendRSTP
			bpdu = l.makeBPDU(t, p, now, proposal)
		case emissionAgreement:
			bpdu = l.makeAgreementBPDU(t, p, now)
		}

		built, err := l.frames(t, bpdu)
		if err != nil {
			// MST.Validate rejects a region with more instances than one
			// BPDU can carry, so this is unreachable for a Layer built
			// through New; the handling exists so that a future caller
			// building a Layer another way degrades to sending nothing
			// rather than to sending an empty frame.
			return
		}

		for _, f := range built {
			*emissions = append(*emissions, Emission{Port: p.name, VID: f.vid, Frame: f.frame})
		}
		p.txBPDUs++
		wasZero := tx.count == 0
		tx.count++
		if wasZero {
			tx.tick = now.Add(time.Second)
		}
	} else {
		switch kind {
		case emissionDesignated:
			tx.pendingDesignated = true
		case emissionAgreement:
			tx.pendingAgreement = true
		}
	}
}

// taggedFrame is one frame an emission puts on the wire, with the VLAN it
// rides. A zero vid leaves the frame untagged and unchecked against the
// port's VLAN membership.
type taggedFrame struct {
	vid   vlan.ID
	frame ethernet.Frame
}

// frames builds the wire form of one BPDU for tree t. Outside PVST mode that
// is the single IEEE-addressed frame, untagged. Inside it, every tree sends
// its BPDU to the SSTP address on its own VLAN, and VLAN 1's tree sends a
// second, IEEE-addressed and untagged, which is the one an RSTP or MSTP
// neighbor converges with. The two frames are one transmission and spend one
// budget slot between them.
func (l *Layer) frames(t *tree, b BPDU) ([]taggedFrame, error) {
	if l.pvst == nil {
		frame, err := Encode(b, l.address)
		if err != nil {
			return nil, err
		}

		return []taggedFrame{{frame: frame}}, nil
	}

	sstp, err := EncodeSSTP(b, t.vid, l.address)
	if err != nil {
		return nil, err
	}
	built := []taggedFrame{{vid: t.vid, frame: sstp}}

	if t.id != cistID {
		return built, nil
	}

	ieee, err := Encode(b, l.address)
	if err != nil {
		return nil, err
	}

	return append(built, taggedFrame{frame: ieee}), nil
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

func (l *Layer) raiseTopologyChange(t *tree, originPort string, now time.Time, flushes *[]FlushTarget) {
	t.topologyChangeCount++
	t.lastTopologyChange = now
	t.topologyChangeTimer = now.Add(l.helloTime + time.Second)

	fids := l.treeVLANs[t.id]

	for _, name := range l.portNames {
		if name == originPort {
			continue
		}
		mergeFlushTarget(flushes, name, fids)
	}
}

// mergeFlushTarget records that port needs its fids flushed, merging into an
// existing target for the same port rather than appending a second one. The
// merge always widens toward "every FID": an existing empty FIDs absorbs a
// narrower fids unchanged, and a narrower existing FIDs is widened to empty
// when fids itself is empty. The result stays sorted and free of duplicates
// so Effects.Flush is deterministic.
func mergeFlushTarget(flushes *[]FlushTarget, port string, fids []vlan.ID) {
	for i := range *flushes {
		target := &(*flushes)[i]
		if target.Port != port {
			continue
		}
		if len(target.FIDs) == 0 {
			return
		}
		if len(fids) == 0 {
			target.FIDs = nil

			return
		}
		merged := make([]vlan.ID, 0, len(target.FIDs)+len(fids))
		merged = append(merged, target.FIDs...)
		merged = append(merged, fids...)
		slices.Sort(merged)
		target.FIDs = slices.Compact(merged)

		return
	}

	var vids []vlan.ID
	if len(fids) > 0 {
		vids = slices.Clone(fids)
	}
	*flushes = append(*flushes, FlushTarget{Port: port, FIDs: vids})
}

// boundary reports whether the named port is a boundary port: the CIST's most
// recently received BPDU on it carried no MST configuration identifier, or
// one from a different region. It answers false for a port the CIST does not
// track, which for a plain RSTP bridge with no MSTI trees is moot since this
// is only ever consulted from one.
func (l *Layer) boundary(name string) bool {
	p, ok := l.cist().ports[name]
	if !ok {
		return false
	}

	return p.external
}

// syncInstancePorts carries a physical link property change on the CIST's
// port cistP onto every MSTI's port state of the same name: up, point to
// point, and, for an instance that left its path cost unconfigured, the
// path cost too. These are link properties, not per-instance ones, so an
// MSTI only tracks its own copy to give recompute one shape of portState to
// read regardless of tree; instanceRemainingHops and the boundary role rule
// are what actually let instances diverge.
func (l *Layer) syncInstancePorts(name string, cistP *portState) {
	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		mp, ok := l.trees[id].ports[name]
		if !ok {
			continue
		}

		mp.up = cistP.up
		mp.pointToPoint = cistP.pointToPoint
		mp.edge = cistP.edge
		if !mp.pathCostFixed {
			mp.pathCost = cistP.linkPathCost
		}
		if !cistP.up {
			mp.role = RoleDisabled
			mp.state = StateDiscarding
			mp.rcvInfoValid = false
			mp.pvidInconsistent = false
		}
	}
}

// armHelloTimers starts the periodic hello on every tree that drives its own
// emission: the CIST alone outside PVST mode, since an MSTI's information
// rides the CIST's BPDU, and every VLAN's tree inside it. A tree whose hello
// timer stays zero never reaches Wake's hello loop, which is what keeps that
// loop's walk over every tree behavior-neutral for RSTP and MSTP.
func (l *Layer) armHelloTimers(now time.Time) {
	for _, id := range l.treeOrder {
		if l.pvst == nil && id != cistID {
			continue
		}
		if t := l.trees[id]; t.helloTimer.IsZero() {
			t.helloTimer = now.Add(l.helloTime)
		}
	}
}

// clearPending drops every tree's held transmission on the named port. A port
// whose link just went down, or that BPDU guard just disabled, must not
// release a BPDU it was holding when the budget next frees up.
func (l *Layer) clearPending(name string) {
	for _, id := range l.treeOrder {
		tx := l.tx(l.trees[id], name)
		tx.pendingDesignated = false
		tx.pendingAgreement = false
	}
}

// recomputeAll runs recompute for every tree in deterministic order, the CIST
// first and then the other trees ascending, and aggregates the emissions.
// Under MSTP only the CIST emits: an MSTI's recompute is told not to, so the
// per-port transmit budget is spent once per port rather than once per
// instance, and the MSTI records ride the CIST's own BPDU. Under PVST every
// tree emits, because each VLAN's BPDU is a frame of its own metered against
// that tree's own budget.
func (l *Layer) recomputeAll(now time.Time, flushes *[]FlushTarget) []Emission {
	var emissions []Emission

	for _, id := range l.treeOrder {
		emissions = append(emissions, l.recompute(l.trees[id], now, flushes, l.pvst != nil || id == cistID)...)
	}

	return emissions
}

// instanceRemainingHops computes the hop count an MSTI tree originates with:
// the region's MaxHops when this bridge is the instance's own regional root,
// and otherwise one fewer than its root port received.
func (l *Layer) instanceRemainingHops(t *tree) uint8 {
	maxHops := effectiveMaxHops(l.mst.MaxHops)

	isRegionalRoot := t.rootID == t.bridgeID
	if t.id == cistID {
		isRegionalRoot = t.regionalRootID == t.bridgeID
	}
	if isRegionalRoot {
		return maxHops
	}

	rp, ok := t.ports[t.rootPort]
	if !ok || rp.rcvRemainingHops == 0 {
		return maxHops
	}

	return rp.rcvRemainingHops - 1
}

// gatherMSTIRecords builds one MSTI record per configured instance for
// transmission on port p, carrying that instance's current regional root,
// internal cost, and per-instance bridge and port priority. It is called only
// while building the CIST's own BPDU: an MST bridge always emits its MSTI
// records alongside the CIST, on every up port, boundary ports included,
// because a port is classified internal or external only on reception.
func (l *Layer) gatherMSTIRecords(now time.Time, p *portState) []MSTIRecord {
	var recs []MSTIRecord

	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		mstid := MSTID(id)
		mt := l.trees[id]
		mp, ok := mt.ports[p.name]
		if !ok {
			continue
		}

		// Flags reuses the CIST's own role/learning/forwarding bit layout
		// (SetRole/SetLearning/SetForwarding/SetTopologyChange), applied to
		// this instance port's role and state and this instance's own
		// topology change timer rather than the CIST's, so the record says
		// what the sender's per-instance port is doing and lets a peer
		// reconverge that instance without waiting on its filtering database
		// to age out. A zero-value BPDU used only to borrow its bit-setting
		// methods never gets encoded itself.
		var flags BPDU
		flags.SetRole(mp.role)
		flags.SetLearning(mp.state == StateLearning || mp.state == StateForwarding)
		flags.SetForwarding(mp.state == StateForwarding)
		if !mt.topologyChangeTimer.IsZero() && mt.topologyChangeTimer.After(now) {
			flags.SetTopologyChange(true)
		}

		recs = append(recs, MSTIRecord{
			MSTID:                mstid,
			Flags:                flags.Flags,
			RegionalRootID:       mt.rootID,
			InternalRootPathCost: mt.rootPathCost,
			BridgePriority:       uint8(mt.bridgeID.Priority >> 12),
			PortPriority:         uint8(mp.portID >> 8),
			RemainingHops:        l.instanceRemainingHops(mt),
		})
	}

	return recs
}

// receiveMSTIs stores the MSTI records an internal BPDU carries into each
// named instance's port state, one instance at a time by the same
// same-source-or-superior rule the CIST uses, and carries each record's own
// topology change bit into that instance the way Receive carries the CIST's:
// unconditionally, not gated on superiority, since a change notification is
// evidence about the fabric rather than a claim this port might reject. A
// record for an instance this bridge does not configure is ignored: the
// fabric's bridges are not required to share the same instance set. The
// designated bridge and port a record implies reuse the sending bridge's own
// address and the CIST port identifier's index half, since MSTI bridge and
// port identifiers differ from the CIST's only in their priority nibble
// (clause 13.7).
func (l *Layer) receiveMSTIs(now time.Time, port string, b BPDU, flushes *[]FlushTarget) {
	for _, rec := range b.MSTIs {
		mt, ok := l.trees[treeID(rec.MSTID)]
		if !ok {
			continue
		}
		mp, ok := mt.ports[port]
		if !ok {
			continue
		}

		if (BPDU{Flags: rec.Flags}).TopologyChange() && !mp.cfg.RestrictedTCN {
			mt.topologyChangeTimer = now.Add(l.helloTime + time.Second)
			fids := l.treeVLANs[mt.id]
			for _, name := range l.portNames {
				if name != port {
					mergeFlushTarget(flushes, name, fids)
				}
			}
		}

		recBridgeID := BridgeID{
			Priority: (uint16(rec.BridgePriority) << 12) | uint16(rec.MSTID),
			Address:  b.BridgeID.Address,
		}
		recPortID := (uint16(rec.PortPriority) << 8) | (b.PortID & 0x00FF)

		incoming := priorityVector{
			rootID: rec.RegionalRootID, regionalRootID: rec.RegionalRootID,
			internalRootPathCost: rec.InternalRootPathCost, bridgeID: recBridgeID, portID: recPortID,
		}

		sameSource := mp.rcvInfoValid && mp.rcvBridgeID == recBridgeID && mp.rcvPortID == recPortID
		isSuperior := !mp.rcvInfoValid
		if mp.rcvInfoValid {
			stored := rawVector(mt, mp)
			if compareVectors(incoming, stored) < 0 {
				isSuperior = true
			}
		}
		if !sameSource && !isSuperior {
			continue
		}

		if rec.RemainingHops <= 1 {
			continue
		}

		mp.rcvInfoValid = true
		mp.rcvRootID = rec.RegionalRootID
		mp.rcvRootPathCost = rec.InternalRootPathCost
		mp.rcvBridgeID = recBridgeID
		mp.rcvPortID = recPortID
		mp.rcvRemainingHops = rec.RemainingHops
		mp.rcvHelloTime = b.HelloTime
		mp.rcvTime = now
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

	// The tree's own bridge identifier, not the layer's: under PVST every
	// tree carries its VLAN in the system-ID extension, and a BPDU sent under
	// the layer's identifier would never match the receiving tree's Backup
	// test (rcvBridgeID == t.bridgeID). Outside PVST the CIST's identifier is
	// the layer's, and only the CIST ever builds a BPDU.
	b := BPDU{
		RootID:       t.rootID,
		RootPathCost: t.rootPathCost,
		BridgeID:     t.bridgeID,
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

	// Only the CIST drives emission (see recomputeAll), so this is also the
	// one place that attaches the region's configuration identifier and every
	// instance's MSTI record. t is always the CIST here. The MST shape is
	// version 3, so it is withheld on a port that has migrated to legacy STP
	// (sendRSTP false, which already forced Version 0 and Configuration
	// above): Encode picks the MST shape whenever ConfigID is set regardless
	// of Version and Type, and a legacy peer needs a Configuration BPDU, not
	// version 3.
	if l.mst != nil && p.sendRSTP {
		cid := *l.configID
		b.ConfigID = &cid
		b.RegionalRootID = t.regionalRootID
		b.InternalRootPathCost = t.internalRootPathCost
		b.RemainingHops = l.instanceRemainingHops(t)
		b.MSTIs = l.gatherMSTIRecords(now, p)
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

// recompute runs one tree's root election and role and state assignment. On
// a boundary port, an MSTI tree (t.id != cistID) takes the CIST port's role
// and state outright rather than computing its own, which is the boundary
// role rule (netsim reports the CIST's Root where the standard would say
// Master; no separate Role value exists for it). emit gates the proposal
// emissions a root change triggers: only the CIST emits, so an MSTI's caller
// passes false and recompute returns no emissions for it.
func (l *Layer) recompute(t *tree, now time.Time, flushes *[]FlushTarget, emit bool) []Emission {
	var emissions []Emission

	oldRootID := t.rootID
	oldRootCost := t.rootPathCost
	oldRootPort := t.rootPort

	bestVector := priorityVector{
		rootID:         t.bridgeID,
		regionalRootID: t.bridgeID,
		bridgeID:       t.bridgeID,
	}
	bestPort := ""
	bestRcvPortID := uint16(0)

	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok || !p.up || p.bpduGuardDisabled || !p.rcvInfoValid {
			continue
		}
		// Restricted role denies the port the root role, and loop guard holds a
		// port whose information expired out of the tree so it reconverges
		// around it. A PVID-inconsistent port carries a peer that disagrees
		// about which VLAN the link is, so its vector describes a different
		// VLAN's tree. None may contribute the bridge's root vector.
		if p.cfg.RestrictedRole || p.loopInconsistent || p.pvidInconsistent {
			continue
		}
		if !p.rcvTime.Add(3 * p.rcvHelloTime).After(now) {
			continue
		}

		cand := candidateVector(t, p)

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
		t.rootID = t.bridgeID
		t.rootPathCost = 0
		t.rootPort = ""
		if t.id == cistID {
			t.regionalRootID = t.bridgeID
			t.internalRootPathCost = 0
		}
	} else {
		t.rootPort = bestPort
		if t.id == cistID {
			t.rootID = bestVector.rootID
			t.rootPathCost = bestVector.externalRootPathCost
			t.regionalRootID = bestVector.regionalRootID
			t.internalRootPathCost = bestVector.internalRootPathCost
		} else {
			t.rootID = bestVector.regionalRootID
			t.rootPathCost = bestVector.internalRootPathCost
		}
	}

	for _, name := range l.portNames {
		p, ok := t.ports[name]
		if !ok {
			continue
		}

		// The boundary role rule is MSTP's: an MSTI on a port facing another
		// region follows the CIST rather than running its own election. It
		// must not reach a PVST bridge, where l.mst is nil by construction
		// and the first ordinary VLAN 1 hello marks every port external
		// (Receive sets external from the internal classification, which
		// needs l.mst). Ungated, every non-VLAN-1 tree would copy VLAN 1's
		// role on every port instead of electing its own root.
		if l.mst != nil && t.id != cistID && l.boundary(name) {
			cistP := l.cist().ports[name]
			oldRole := p.role
			p.role = cistP.role
			if p.role != oldRole && p.role != RoleDesignated {
				p.agreed = false
			}

			continue
		}

		// bpduGuardDisabled and loopInconsistent are bridge-global properties
		// of the port, like the internal/external classification: only the
		// CIST's copy is ever written (Receive's guard branch, and Wake's
		// loop-guard arm), so an MSTI reads them from the CIST's port state
		// the way it already reaches across for l.boundary.
		cistP := l.cist().ports[name]

		oldRole := p.role
		switch {
		case !p.up || cistP.bpduGuardDisabled:
			p.role = RoleDisabled
		case cistP.loopInconsistent:
			// A loop-inconsistent port is Alternate and never Designated: a
			// port that stopped hearing its designated peer is the one that
			// would open a loop by claiming the segment.
			p.role = RoleAlternate
		case p.pvidInconsistent:
			// Read from this tree's own port state, not the CIST's: PVID
			// inconsistency is set on the arrival VLAN's tree, so consulting
			// cistP would read a field no tree but VLAN 1's ever has set and
			// disable the check for every VLAN it exists to protect.
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
		p, ok := t.ports[name]
		if !ok {
			continue
		}
		oldState := p.state

		// The state half of the boundary role rule, gated for the same reason
		// its role half above is: on a PVST bridge every port reads external,
		// and a VLAN's tree would mirror VLAN 1's state over the state its
		// own election just decided.
		if l.mst != nil && t.id != cistID && l.boundary(name) {
			cistP := l.cist().ports[name]
			p.state = cistP.state
			// A port that just flipped internal to boundary may still carry
			// a live timer from its internal role and state ladder; a
			// boundary port never drives its own state, so nothing else
			// clears it. Left set, the next Wake would advance the port on
			// a timer behind a state this branch already mirrored, raising
			// a topology change with nothing behind it.
			p.fwdDelayTimer = time.Time{}
			if oldState != StateForwarding && p.state == StateForwarding {
				p.forwardTransitions++
			}

			continue
		}

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

	if emit && (t.rootID != oldRootID || t.rootPathCost != oldRootCost || t.rootPort != oldRootPort) {
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
		desig := designatedVector(t, p)
		rcv := rawVector(t, p)
		if compareVectors(rcv, desig) < 0 {
			if p.rcvBridgeID == t.bridgeID {
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

	var flushes []FlushTarget
	var emissions []Emission

	l.armHelloTimers(now)

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
		l.clearPending(p.name)
		p.fwdDelayTimer = time.Time{}
		// Both guard states clear here, which is what makes a link down and up
		// the recovery for BPDU guard. A port that comes back up holds no
		// expired information, so loop guard has nothing to trigger on either.
		// The PVST boundary mark clears for a different reason: it names the
		// protocol the neighbor speaks, and only a link transition can put a
		// different neighbor there.
		p.bpduGuardDisabled = false
		p.loopInconsistent = false
		p.pvidInconsistent = false
		p.pvstBoundary = false

		// The entries learned on the dead port are the ones certainly stale
		// whatever tree they belong to; the topology change below flushes
		// every other port by its own tree's VLANs.
		flushes = append(flushes, FlushTarget{Port: p.name})
		if oldState == StateForwarding && !p.edge {
			l.raiseTopologyChange(t, p.name, now, &flushes)
		}

		l.syncInstancePorts(port, p)

		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

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
	p.linkPathCost = cost
	// A cost the tree configured for itself stays put across a link change,
	// the way syncInstancePorts already leaves a fixed instance cost alone.
	// Only PVST sets this on the CIST's own port state, by configuring VLAN
	// 1's path cost on the tree that occupies the CIST slot.
	if p.pathCostFixed {
		cost = p.pathCost
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

	l.syncInstancePorts(port, p)

	emissions = append(emissions, l.recomputeAll(now, &flushes)...)

	if p.pointToPoint && !p.edge && p.role == RoleDesignated && p.state == StateDiscarding {
		alreadyEmitted := false
		for _, e := range emissions {
			// Under PVST every tree can emit on this port; only a match on
			// this tree's own VID is evidence that this call's own recompute
			// already sent the CIST's proposal, not some other VLAN's.
			if e.Port == p.name && e.VID == t.vid {
				alreadyEmitted = true

				break
			}
		}
		if !alreadyEmitted && !l.tx(t, p.name).pendingDesignated {
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// receiveLink runs the half of a receive that belongs to the link rather than
// to any one tree: BPDU guard, the loop-guard clear every BPDU earns, the
// protocol migration between RSTP and legacy STP, and the loss of auto-edge
// status. It runs once per received frame whatever tree the frame belongs to,
// against the CIST's port state, which is where those link properties live.
// done reports that the frame must not reach a tree at all, either because
// the guard just fired or because it had already disabled the port.
func (l *Layer) receiveLink(now time.Time, p *portState, b BPDU, flushes *[]FlushTarget) (emissions []Emission, done bool) {
	t := l.cist()

	// BPDU guard exists to keep an unexpected bridge on an access port out of
	// the topology, so the frame that proves one is there disables the port
	// before anything reads the BPDU. Only a link down and up brings it back.
	if p.cfg.BPDUGuard && !p.bpduGuardDisabled {
		p.bpduGuardDisabled = true
		p.rcvInfoValid = false
		p.agreed = false
		p.proposing = false
		l.clearPending(p.name)
		p.fwdDelayTimer = time.Time{}

		// The entries learned on the port are the ones certainly stale,
		// whatever tree they belong to. The topology change itself is left
		// to recompute, which raises it from the same transition with the
		// same origin and timestamp; raising it here as well would count one
		// event twice.
		*flushes = append(*flushes, FlushTarget{Port: p.name})

		return l.recomputeAll(now, flushes), true
	}
	if p.bpduGuardDisabled {
		return nil, true
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
			l.raiseTopologyChange(t, p.name, now, flushes)
		}
		p.state = StateDiscarding
		p.fwdDelayTimer = time.Time{}
		p.proposing = p.pointToPoint && p.sendRSTP
		// edge is a bridge-global link property (see syncInstancePorts): a
		// port losing auto-edge status must clear it on every instance too,
		// or an MSTI keeps treating the port as an edge after the CIST no
		// longer does.
		l.syncInstancePorts(p.name, p)
	}

	return nil, false
}

// Receive processes an incoming BPDU received on a port. It applies the BPDU
// to the CIST, which is the tree an IEEE-addressed BPDU belongs to in every
// mode: plain RSTP has only that tree, an MSTP bridge distributes its MSTI
// records from it, and a PVST bridge keeps VLAN 1's tree there. An SSTP BPDU
// goes to ReceiveSSTP instead, which is the only entry point that classifies
// a BPDU by VLAN.
func (l *Layer) Receive(now time.Time, port string, b BPDU) Effects {
	t := l.cist()
	p, ok := t.ports[port]
	if !ok || !p.up {
		return Effects{}
	}

	p.rxBPDUs++

	l.armHelloTimers(now)

	var flushes []FlushTarget

	// An MST BPDU on a PVST bridge is the boundary this layer reports rather
	// than models: its RST prefix still drives VLAN 1's tree below, but no
	// other VLAN's tree hears anything from that neighbor.
	if l.pvst != nil && b.ConfigID != nil {
		p.pvstBoundary = true
	}

	emissions, done := l.receiveLink(now, p, b, &flushes)
	if done {
		return Effects{Emissions: emissions, Flush: flushes}
	}

	if b.Type == BPDUTypeTopologyChangeNotification {
		// Restricted TCN stops the change here. Setting the timer would carry
		// the flag out on this bridge's own BPDUs, which is the propagation the
		// guard denies, so neither the timer nor the flush runs.
		if !p.cfg.RestrictedTCN {
			t.topologyChangeTimer = now.Add(l.helloTime + time.Second)
			for _, name := range l.portNames {
				if name != port {
					mergeFlushTarget(&flushes, name, l.treeVLANs[t.id])
				}
			}
		}

		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return Effects{
			Emissions: emissions,
			Flush:     flushes,
		}
	}

	emissions = append(emissions, l.applyBPDU(t, p, now, b, &flushes)...)

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}

// ReceiveSSTP processes an SSTP BPDU received on a port, applying it to the
// tree of arrivalVID, the VLAN the switch classified the frame into. tlvVID is
// the VLAN the BPDU itself names in its trailing TLV. Both are needed because
// the disagreement between them is what the PVID check exists to catch: when
// they differ the two ends of the link disagree about what it carries, and the
// arrival VLAN is held discarding on that port until a consistent BPDU
// arrives.
//
// On a bridge that does not run PVST the BPDU is counted and the port marked
// a PVST boundary, but nothing is applied: that bridge's CIST does not run
// this VLAN's tree, so feeding the vector into it would elect a root from a
// tree it is not running. The neighbor relationship still converges, because
// a PVST+ bridge sends VLAN 1's tree to the IEEE address as well.
func (l *Layer) ReceiveSSTP(now time.Time, port string, arrivalVID, tlvVID vlan.ID, b BPDU) Effects {
	cistP, ok := l.cist().ports[port]
	if !ok || !cistP.up {
		return Effects{}
	}

	cistP.rxBPDUs++

	var flushes []FlushTarget

	// receiveLink is the link-level half of a receive: BPDU guard, the
	// loop-guard clear, protocol migration, and auto-edge loss all belong to
	// the port whatever tree the frame names, so a bridge that does not run
	// this VLAN's tree must still run it before turning the frame away.
	emissions, done := l.receiveLink(now, cistP, b, &flushes)

	if l.pvst == nil {
		cistP.pvstBoundary = true

		return Effects{Emissions: emissions, Flush: flushes}
	}

	l.armHelloTimers(now)

	if done {
		return Effects{Emissions: emissions, Flush: flushes}
	}
	// receiveLink may have changed link properties every tree tracks its own
	// copy of, and the tree this BPDU belongs to is about to read them.
	l.syncInstancePorts(port, cistP)

	t, ok := l.treeFor(arrivalVID)
	if !ok {
		return Effects{Emissions: emissions, Flush: flushes}
	}

	p, ok := t.ports[port]
	if !ok {
		return Effects{Emissions: emissions, Flush: flushes}
	}

	// Cisco blocks the traffic of the VLAN the frame arrived on, not of the
	// VLAN the peer named: the arrival VLAN is the one whose local traffic
	// would cross a link the two ends disagree about.
	if tlvVID != arrivalVID {
		p.pvidInconsistent = true
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)

		return Effects{Emissions: emissions, Flush: flushes}
	}
	p.pvidInconsistent = false

	emissions = append(emissions, l.applyBPDU(t, p, now, b, &flushes)...)

	return Effects{Emissions: emissions, Flush: flushes}
}

// applyBPDU applies one received BPDU to one tree: the classification, the
// information it carries, the agreement and topology-change flags it sets,
// and the proposal handshake it answers. Receive runs it on the CIST, which
// is where an IEEE-addressed BPDU belongs in every mode; ReceiveSSTP runs it
// on the tree of the VLAN an SSTP BPDU arrived on. The link-level half of a
// receive, which runs once per frame whatever tree it belongs to, stays with
// the two callers.
func (l *Layer) applyBPDU(t *tree, p *portState, now time.Time, b BPDU, flushes *[]FlushTarget) []Emission {
	var emissions []Emission

	// A BPDU is internal when it names this bridge's own region: an MST BPDU
	// (ConfigID set) whose configuration identifier equals this bridge's. An
	// RST or Configuration BPDU, and an MST BPDU from a different region, are
	// external. The classification lives on the CIST port state because it
	// is a property of the link, not of a tree running over it.
	internal := l.mst != nil && b.ConfigID != nil && *b.ConfigID == *l.configID

	if internal {
		// Internal information ages by hop count, re-originated one hop
		// short of what was received; a record that has already reached the
		// bound is discarded rather than stored, so a BPDU naming a regional
		// root that no longer exists stops refreshing on every hop and the
		// port's own information ages out.
		if b.RemainingHops <= 1 {
			p.external = !internal
			emissions = append(emissions, l.recomputeAll(now, flushes)...)

			return emissions
		}
	} else {
		// IEEE 802.1Q treats message age as a hop count bounded by the max age
		// the BPDU itself carries, not by this bridge's configured one: the
		// received value is the root's, and the fabric builds bridges with
		// differing timers. Information that has reached the bound is
		// discarded rather than stored, so a BPDU naming a root that no
		// longer exists stops refreshing the timer on every hop and the
		// port's own information ages out.
		if b.MessageAge+time.Second > b.MaxAge {
			p.external = !internal
			emissions = append(emissions, l.recomputeAll(now, flushes)...)

			return emissions
		}
	}

	// Built in the same shape rawVector gives the stored vector it is compared
	// against: a non-CIST tree carries its cost in internalRootPathCost, not
	// externalRootPathCost, and a vector built with the cost in the wrong slot
	// compares against a different component than the one it belongs next to.
	incoming := priorityVector{rootID: b.RootID, bridgeID: b.BridgeID, portID: b.PortID}
	switch {
	case t.id == cistID && internal:
		incoming.externalRootPathCost = b.RootPathCost
		incoming.regionalRootID = b.RegionalRootID
		incoming.internalRootPathCost = b.InternalRootPathCost
	case t.id == cistID:
		incoming.externalRootPathCost = b.RootPathCost
		incoming.regionalRootID = b.RootID
	default:
		incoming.regionalRootID = b.RootID
		incoming.internalRootPathCost = b.RootPathCost
	}

	sameSource := p.rcvInfoValid && (b.BridgeID == p.rcvBridgeID && b.PortID == p.rcvPortID)
	isSuperior := false
	if !p.rcvInfoValid {
		isSuperior = true
	} else {
		stored := rawVector(t, p)
		if compareVectors(incoming, stored) < 0 {
			isSuperior = true
		}
	}

	// The classification updates only now, after the stored vector above was
	// built against what the port currently holds under its old
	// classification. Assigning it earlier would compare that stored
	// information as though it already carried this BPDU's classification,
	// which can invert the superiority verdict for the one BPDU that flips
	// internal to external or back.
	p.external = !internal

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
		if internal {
			p.rcvRegionalRootID = b.RegionalRootID
			p.rcvInternalRootPathCost = b.InternalRootPathCost
			p.rcvRemainingHops = b.RemainingHops
		} else {
			// A port classified external carries no internal-only state: a
			// stale regional root, internal cost, or hop count left over from
			// an earlier internal BPDU would otherwise survive the flip and
			// this bridge would re-originate a decreasing hop count instead
			// of MaxHops.
			p.rcvRegionalRootID = BridgeID{}
			p.rcvInternalRootPathCost = 0
			p.rcvRemainingHops = 0
		}
	}

	if internal {
		l.receiveMSTIs(now, p.name, b, flushes)
	}

	if p.role == RoleDesignated && b.Agreement() {
		p.agreed = true
		p.proposing = false
		if p.state != StateForwarding {
			p.state = StateForwarding
			p.forwardTransitions++
			if !p.edge {
				l.raiseTopologyChange(t, p.name, now, flushes)
			}
		}
	}

	if b.TopologyChange() && !p.cfg.RestrictedTCN {
		t.topologyChangeTimer = now.Add(l.helloTime + time.Second)
		for _, name := range l.portNames {
			if name != p.name {
				mergeFlushTarget(flushes, name, l.treeVLANs[t.id])
			}
		}
	}

	emissions = append(emissions, l.recomputeAll(now, flushes)...)

	if b.Proposal() && (p.role == RoleRoot || p.role == RoleAlternate) {
		for _, otherName := range l.portNames {
			if otherName == p.name {
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
						l.raiseTopologyChange(t, otherP.name, now, flushes)
					}
				}
			}
		}

		if p.role == RoleRoot && p.pointToPoint && l.isSynced(t, p.name) && p.sendRSTP {
			if p.state != StateForwarding {
				p.state = StateForwarding
				p.forwardTransitions++
				if !p.edge {
					l.raiseTopologyChange(t, p.name, now, flushes)
				}
			}
		}

		l.emit(t, p, now, emissionAgreement, &emissions)
	} else if p.role == RoleDesignated && !b.Agreement() {
		if compareVectors(incoming, designatedVector(t, p)) > 0 {
			l.emit(t, p, now, emissionDesignated, &emissions)
		}
	}

	return emissions
}

// Wake advances timer-driven state to now, firing due hellos, forward delays,
// topology change timers, and information age-outs.
func (l *Layer) Wake(now time.Time) Effects {
	t := l.cist()

	var flushes []FlushTarget
	var emissions []Emission

	autoEdgeFired := false
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
			// edge is a bridge-global link property (see syncInstancePorts):
			// an MSTI port must reach Forwarding at the same wake as the
			// CIST's, or it raises a topology change of its own for a flush
			// auto-edge exists to prevent.
			l.syncInstancePorts(name, p)
			autoEdgeFired = true
		}
	}

	// Both emission loops walk every tree, the way the forward-delay, age-out
	// and topology-change loops below already do. Outside PVST mode the walk
	// is behavior-neutral: treeOrder holds the CIST first, the budget is
	// shared, and an MSTI's own hello timer never runs because only the CIST
	// emits. Inside it, a per-VLAN tree's periodic hello would otherwise
	// never fire and a transmission held on it would never be released.
	for _, id := range l.treeOrder {
		mt := l.trees[id]

		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok {
				continue
			}
			tx := l.tx(mt, name)
			if !p.up || tx.tick.IsZero() || tx.tick.After(now) {
				continue
			}
			// A held kind belongs to the role that requested it; released
			// under another role it would be an agreement from a designated
			// port or a designated claim from a blocked one.
			if tx.pendingAgreement {
				tx.pendingAgreement = false
				if p.role == RoleRoot || p.role == RoleAlternate {
					l.emit(mt, p, now, emissionAgreement, &emissions)
				}
			}
			if tx.pendingDesignated {
				tx.pendingDesignated = false
				if p.role == RoleDesignated {
					l.emit(mt, p, now, emissionDesignated, &emissions)
				}
			}
		}

		if mt.helloTimer.IsZero() || mt.helloTimer.After(now) {
			continue
		}
		mt.helloTimer = now.Add(l.helloTime)
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if ok && p.up && p.role == RoleDesignated {
				l.emit(mt, p, now, emissionDesignated, &emissions)
			}
		}
	}

	// The forward delay ladder runs per tree, since role and state are per
	// tree: an MSTI's own internal ports climb it independently of the CIST's.
	// A boundary port never sets fwdDelayTimer for a non-CIST tree (recompute
	// mirrors its state from the CIST outright), so this never double-drives
	// one.
	stateChanged := false
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok || p.fwdDelayTimer.IsZero() || p.fwdDelayTimer.After(now) {
				continue
			}
			p.fwdDelayTimer = time.Time{}
			switch p.role {
			case RoleDesignated, RoleRoot:
				switch p.state {
				case StateDiscarding:
					p.state = StateLearning
					p.fwdDelayTimer = now.Add(l.forwardDelay)
				case StateLearning:
					p.state = StateForwarding
					p.forwardTransitions++
					stateChanged = true
					if !p.edge {
						l.raiseTopologyChange(mt, p.name, now, &flushes)
					}
				case StateForwarding:
				}
			case RoleDisabled, RoleAlternate, RoleBackup:
			}
		}
	}

	// Every tree's received information keeps the landed 3xHelloTime silence
	// bound regardless of internal or external classification; only the test
	// for accepting new information at Receive differs by hop count or
	// message age. Loop guard is a CIST-only concept: an MSTI's own role on
	// an internal port never gets to hold a segment open past its peer, and
	// on a boundary port it mirrors the CIST outright.
	agedOut := false
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		for _, name := range l.portNames {
			p, ok := mt.ports[name]
			if !ok || !p.rcvInfoValid || p.rcvTime.Add(3*p.rcvHelloTime).After(now) {
				continue
			}
			if id == cistID && p.loopGuardWatches() &&
				(p.role == RoleRoot || p.role == RoleAlternate || p.role == RoleBackup) {
				p.loopInconsistent = true
			}
			p.rcvInfoValid = false
			agedOut = true
		}
	}

	// Every tree's topology change timer clears on its own schedule: an
	// MSTI's forward-delay ladder above can raise one independently of the
	// CIST's.
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		if !mt.topologyChangeTimer.IsZero() && !mt.topologyChangeTimer.After(now) {
			mt.topologyChangeTimer = time.Time{}
		}
	}

	if agedOut || stateChanged || autoEdgeFired {
		emissions = append(emissions, l.recomputeAll(now, &flushes)...)
	}

	return Effects{
		Emissions: emissions,
		Flush:     flushes,
	}
}
