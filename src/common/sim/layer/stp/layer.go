package stp

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// LayerName identifies the Rapid Spanning Tree Protocol layer.
const LayerName trace.Layer = "stp"

// Rule constants produced by stp.
const (
	RuleBPDUAdmit           trace.RuleID = "stp.bpdu.admit"
	RuleSSTPVLANNotAdmitted trace.RuleID = "stp.sstp.vlan-not-admitted"
	RuleSSTPVLANUntracked   trace.RuleID = "stp.sstp.vlan-untracked"
	RuleSSTPAdmit           trace.RuleID = "stp.sstp.admit"
	RuleStatusDown          trace.RuleID = "port.status.down"
	RuleBPDUPrefix                       = "stp.bpdu."
	RuleSSTPPrefix                       = "stp.sstp."
)

// Layer implements the Rapid Spanning Tree protocol layer for a virtual switch.
// It is a deterministic state machine driven by explicit time-stamped method calls.
// Layer is not safe for concurrent use.
type Layer struct {
	cfg      Config
	priority uint16
	address  netaddr.MAC
	bridgeID bpdu.BridgeID

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

	// links holds the physical and administrative link state for each port.
	links map[string]*linkRecord

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
	configID *bpdu.ConfigID
}

// New constructs a spanning tree layer from the given configuration and environment.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, env layer.Env) (*Layer, error) {
	if err := cfg.Validate(env); err != nil {
		return nil, err
	}
	return newLayer(cfg.Normalize(env)), nil
}

func newLayer(cfg Config) *Layer {
	prio := effectivePriority(cfg.Priority, cfg.PriorityPresent)
	hello := effectiveHelloTime(cfg.HelloTime)
	maxAge := effectiveMaxAge(cfg.MaxAge)
	fwdDelay := effectiveForwardDelay(cfg.ForwardDelay)

	bridgeID := bpdu.BridgeID{Priority: prio, Address: cfg.Address}

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
		links:        make(map[string]*linkRecord, len(sortedNames)),
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
			cost = defaultPathCost(0)
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

		l.links[name] = &linkRecord{
			adminEdge:    pCfg.AdminEdge,
			edge:         pCfg.AdminEdge,
			sendRSTP:     true,
			linkPathCost: linkCost,
		}

		cist.ports[name] = &portState{
			name:          name,
			cfg:           pCfg,
			portID:        (uint16(portPrio) << 8) | uint16(i+1),
			pathCost:      cost,
			pathCostFixed: fixed,
			role:          bpdu.RoleDisabled,
			state:         StateDiscarding,
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
func mstiBridgeID(inst Instance, mstid bpdu.MSTID, address netaddr.MAC) bpdu.BridgeID {
	return bpdu.BridgeID{
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
func pvstBridgeID(t Tree, vid vlan.ID, bridgePriority uint16, address netaddr.MAC) bpdu.BridgeID {
	priority := bridgePriority
	if t.PriorityPresent {
		priority = t.Priority
	}

	return bpdu.BridgeID{
		Priority: (priority & 0xF000) | uint16(vid),
		Address:  address,
	}
}

// addTree builds and registers one tree beside the CIST: an MST instance's or
// a PVST VLAN's. treePorts carries that tree's own per-port overrides. Each
// port's identifier reuses the CIST's index half so it stays bridge-global;
// only its priority half, and its path cost, can differ per tree.
func (l *Layer) addTree(id treeID, vid vlan.ID, bridgeID bpdu.BridgeID, treePorts map[string]InstancePort, sortedNames []string) {
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

		// The starting cost is the bridge port's own configured cost, the
		// same fallback newLayer derives the CIST's from, not
		// defaultPathCost(0) outright: a port configured with an explicit
		// cost must read it before the first LinkChange ever runs, not just
		// after.
		linkCost := pCfg.PathCost
		if linkCost == 0 {
			linkCost = defaultPathCost(0)
		}
		cost := linkCost
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
			role:          bpdu.RoleDisabled,
			state:         StateDiscarding,
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
		portNames:    l.portNames,
		trees:        make(map[treeID]*tree, len(l.trees)),
		vidToTree:    make(map[vlan.ID]treeID, len(l.vidToTree)),
		treeVLANs:    make(map[treeID][]vlan.ID, len(l.treeVLANs)),
		treeOrder:    l.treeOrder,
		portTx:       make(map[txKey]*portTx, len(l.portTx)),
		links:        make(map[string]*linkRecord, len(l.links)),
	}

	records := make([]linkRecord, len(l.links))
	i := 0
	for k, v := range l.links {
		records[i] = *v
		cp.links[k] = &records[i]
		i++
	}

	cp.cfg.Ports = l.cfg.Ports
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

// RetentionKey returns a canonical encoding of every normalized input the layer's
// runtime state depends on: its own configuration as Diff sees it, port link states,
// and resolved phy speeds.
func RetentionKey(cfg Config, env layer.Env) string {
	if len(cfg.Ports) == 0 && cfg.Address == (netaddr.MAC{}) && cfg.MST == nil && cfg.PVST == nil &&
		cfg.Priority == 0 && cfg.HelloTime == 0 && cfg.MaxAge == 0 && cfg.ForwardDelay == 0 && cfg.TxHoldCount == 0 {
		return ""
	}
	norm := cfg.Normalize(env)
	var b strings.Builder
	b.WriteString("config=")
	fmt.Fprintf(&b, "addr=%s;prio=%d;prio_pres=%t;hello=%s;max_age=%s;fwd_delay=%s;tx_hold=%d;",
		norm.Address, norm.Priority, norm.PriorityPresent, norm.HelloTime, norm.MaxAge, norm.ForwardDelay, norm.TxHoldCount)
	portNames := sortedKeys(norm.Ports)
	for _, name := range portNames {
		p := norm.Ports[name]
		fmt.Fprintf(&b, "p:%s:%s;", name, p.Canonical())
	}
	if norm.MST != nil {
		b.WriteString(";mst=")
		b.WriteString(norm.MST.Canonical())
	}
	if norm.PVST != nil {
		b.WriteString(";pvst=")
		b.WriteString(norm.PVST.Canonical())
	}

	b.WriteString("\nport-state=")
	for _, name := range portNames {
		if pt, ok := env.Ports.Port(name); ok {
			fmt.Fprintf(&b, "%s:admin=%s,oper=%s;", name, pt.AdminStatus, pt.OperStatus)
		} else {
			fmt.Fprintf(&b, "%s:absent;", name)
		}
	}

	b.WriteString("\nresolved-speed=")
	for _, name := range portNames {
		sp := env.Speeds[name]
		fmt.Fprintf(&b, "%s:%d;", name, sp)
	}

	return b.String()
}
