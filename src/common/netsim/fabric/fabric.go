// Package fabric composes virtual switches, hosts, and cables into a Layer 2 network fabric.
package fabric

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// Fabric is a set of switches and hosts joined by cables, with every port state decided by its cable.
//
// A Fabric is not safe for concurrent use.
type Fabric struct {
	cfg            Config
	links          []Link
	switches       map[string]*vswitch.Switch
	hostStacks     map[string]*routing.Layer
	byEnd          map[Endpoint]linkEndRef
	clock          time.Time
	queue          []Arrival
	wakes          map[string]time.Time
	nextFrameID    FrameID
	nextSeq        uint64
	journeys       map[FrameID]*Journey
	entered        map[FrameID]map[Endpoint]bool
	cableCrossings map[Endpoint]uint
	busyUntil      map[Endpoint]time.Time
	counters       map[Endpoint]*Counters
}

type linkEndRef struct {
	link *Link
	end  *LinkEnd
	peer *LinkEnd
}

// New constructs a validated [Fabric] from the provided configuration, computing
// operational link states and negotiated speeds across all cables before instantiating
// the constituent virtual switches, then starts every protocol layer at Start.
func New(cfg Config) (*Fabric, error) {
	fab, err := build(nil, cfg)
	if err != nil {
		return nil, err
	}
	fab.startLayers(fab.switchNames())

	return fab, nil
}

// build makes the fabric without starting any protocol layer, so Derive can
// swap in cloned layers first.
func build(cur *Fabric, cfg Config) (*Fabric, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cloned := cfg.Clone()

	usedMACs := make(map[netaddr.MAC]struct{})
	for _, swCfg := range cloned.Switches {
		if swCfg.MAC != (netaddr.MAC{}) {
			usedMACs[swCfg.MAC] = struct{}{}
		}
		if swCfg.STP != nil && swCfg.STP.Address != (netaddr.MAC{}) {
			usedMACs[swCfg.STP.Address] = struct{}{}
		}
		if swCfg.Routing != nil {
			for _, vrf := range swCfg.Routing.VRFs {
				for _, iface := range vrf.Interfaces {
					if iface.MAC != (netaddr.MAC{}) {
						usedMACs[iface.MAC] = struct{}{}
					}
				}
			}
		}
	}
	for _, h := range cloned.Hosts {
		if h.Address != (netaddr.MAC{}) {
			usedMACs[h.Address] = struct{}{}
		}
	}

	// A node whose new configuration leaves its address zero keeps the one
	// cur assigned, unless the new configuration claims that address
	// explicitly elsewhere; then the node takes the next free one.
	if cur != nil {
		carry := func(assigned netaddr.MAC) (netaddr.MAC, bool) {
			if assigned == (netaddr.MAC{}) {
				return netaddr.MAC{}, false
			}
			if _, taken := usedMACs[assigned]; taken {
				return netaddr.MAC{}, false
			}
			usedMACs[assigned] = struct{}{}

			return assigned, true
		}
		for name, swCfg := range cloned.Switches {
			if swCfg.MAC != (netaddr.MAC{}) {
				continue
			}
			if curSw, ok := cur.cfg.Switches[name]; ok {
				if mac, ok := carry(curSw.MAC); ok {
					swCfg.MAC = mac
					cloned.Switches[name] = swCfg
				}
			}
		}
		for name, h := range cloned.Hosts {
			if h.Address != (netaddr.MAC{}) {
				continue
			}
			if curH, ok := cur.cfg.Hosts[name]; ok {
				if mac, ok := carry(curH.Address); ok {
					h.Address = mac
					cloned.Hosts[name] = h
				}
			}
		}
	}

	assignLocal := func() netaddr.MAC {
		for n := uint32(1); ; n++ {
			cand := netaddr.Local(n)
			if _, ok := usedMACs[cand]; !ok {
				usedMACs[cand] = struct{}{}
				return cand
			}
		}
	}

	swNames := make([]string, 0, len(cloned.Switches))
	for name := range cloned.Switches {
		swNames = append(swNames, name)
	}
	slices.Sort(swNames)
	for _, name := range swNames {
		swCfg := cloned.Switches[name]
		if swCfg.MAC == (netaddr.MAC{}) {
			swCfg.MAC = assignLocal()
			cloned.Switches[name] = swCfg
		}
	}

	hNames := make([]string, 0, len(cloned.Hosts))
	for name := range cloned.Hosts {
		hNames = append(hNames, name)
	}
	slices.Sort(hNames)
	for _, name := range hNames {
		h := cloned.Hosts[name]
		if h.Address == (netaddr.MAC{}) {
			h.Address = assignLocal()
			cloned.Hosts[name] = h
		}
	}

	// Sort cables by endpoint names to ensure stable ordering.
	slices.SortFunc(cloned.Cables, func(i, j Cable) int {
		if r := cmp.Compare(i.A.Node, j.A.Node); r != 0 {
			return r
		}
		if r := cmp.Compare(i.A.Port, j.A.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(i.B.Node, j.B.Node); r != 0 {
			return r
		}

		return cmp.Compare(i.B.Port, j.B.Port)
	})

	links := make([]Link, len(cloned.Cables))
	byEnd := make(map[Endpoint]linkEndRef, len(cloned.Cables)*2)

	for i := range cloned.Cables {
		cable := cloned.Cables[i]
		linkA, linkB := resolveLink(cable, cloned)

		links[i] = Link{
			Cable: cable,
			A:     linkA,
			B:     linkB,
		}

		byEnd[cable.A] = linkEndRef{link: &links[i], end: &links[i].A, peer: &links[i].B}
		byEnd[cable.B] = linkEndRef{link: &links[i], end: &links[i].B, peer: &links[i].A}
	}

	// Rebuild each switch's port table with oper states derived from the cables.
	switches := make(map[string]*vswitch.Switch, len(cloned.Switches))
	for name, swCfg := range cloned.Switches {
		b := port.NewBuilder()
		ports := swCfg.Ports.Ports()

		for _, p := range ports {
			if p.Kind != port.Lag {
				ep := Endpoint{Node: name, Port: p.Name}
				if ref, ok := byEnd[ep]; ok {
					p.OperStatus = ref.end.Oper
				} else {
					p.OperStatus = port.Down
				}
			}
			b.Add(p)
		}

		newTable, err := b.Build()
		if err != nil {
			return nil, errs.Wrapf(err, "rebuild ports for switch %q", name)
		}
		swCfg.Ports = newTable

		sw := vswitch.New(swCfg)
		switches[name] = sw

		memberPorts := sw.Ports().Ports()
		slices.SortFunc(memberPorts, func(a, b port.Port) int {
			return cmp.Compare(a.Name, b.Name)
		})
		for _, p := range memberPorts {
			if p.LagParent == "" {
				continue
			}
			ep := Endpoint{Node: name, Port: p.Name}
			if ref, ok := byEnd[ep]; ok {
				up := ref.end.Oper == port.Up
				p2p := portPointToPoint(cloned, name, p.Name, *ref.end, ref.peer.Endpoint)
				speed := ref.end.Speed.SpeedBPS
				sw.LinkChange(cloned.Start, p.Name, up, p2p, speed)
			}
		}

		swCfg := sw.Config()
		swCfg.Ports = sw.Ports()
		cloned.Switches[name] = swCfg
	}

	hostStacks := make(map[string]*routing.Layer, len(cloned.Hosts))
	for name, h := range cloned.Hosts {
		if h.IP != nil {
			rtCfg, _ := HostRoutingConfig(name, h)
			hostStacks[name] = routing.New(rtCfg)
		}
	}

	fab := &Fabric{
		cfg:            cloned,
		links:          links,
		switches:       switches,
		hostStacks:     hostStacks,
		byEnd:          byEnd,
		clock:          cloned.Start,
		wakes:          make(map[string]time.Time),
		nextFrameID:    1,
		nextSeq:        1,
		journeys:       make(map[FrameID]*Journey),
		entered:        make(map[FrameID]map[Endpoint]bool),
		cableCrossings: make(map[Endpoint]uint),
	}

	return fab, nil
}

func (f *Fabric) switchNames() []string {
	names := make([]string, 0, len(f.cfg.Switches))
	for name := range f.cfg.Switches {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

// startLayers tells the named switches' protocol layers their links at the
// fabric's clock, injects what they emit, and schedules their wakes. A layer
// that already knows a link ignores the report, so a cloned layer hears only
// the links that differ from the fabric it came from.
func (f *Fabric) startLayers(names []string) {
	for _, name := range names {
		swCfg := f.cfg.Switches[name]
		sw := f.switches[name]
		hasLag := false
		for _, p := range sw.Ports().Ports() {
			if p.Kind == port.Lag {
				hasLag = true
				break
			}
		}
		if swCfg.STP == nil && !hasLag {
			continue
		}

		ports := sw.Ports().Ports()
		slices.SortFunc(ports, func(a, b port.Port) int {
			return cmp.Compare(a.Name, b.Name)
		})

		for _, p := range ports {
			if p.Kind == port.Lag {
				continue
			}

			ep := Endpoint{Node: name, Port: p.Name}
			if ref, ok := f.byEnd[ep]; ok {
				up := ref.end.Oper == port.Up
				p2p := f.portPointToPoint(name, p.Name, *ref.end, ref.peer.Endpoint)
				speed := ref.end.Speed.SpeedBPS
				sw.LinkChange(f.clock, p.Name, up, p2p, speed)
			} else {
				p2p := f.uncabledPointToPoint(name, p.Name)
				sw.LinkChange(f.clock, p.Name, false, p2p, 0)
			}
		}

		for _, em := range sw.Drain() {
			f.injectEmission(f.clock, name, em)
		}
		f.scheduleWake(name)
	}
}

func (f *Fabric) linkEnd(node, portName string) (linkEndRef, bool) {
	ref, ok := f.byEnd[Endpoint{Node: node, Port: portName}]

	return ref, ok
}

// Links returns an independent deep copy of all resolved links in cable order.
func (f *Fabric) Links() []Link {
	if len(f.links) == 0 {
		return nil
	}
	cp := make([]Link, len(f.links))
	for i, l := range f.links {
		cp[i] = Link{
			Cable: l.Clone(),
			A:     l.A,
			B:     l.B,
		}
	}

	return cp
}

// Unlinked returns one link end per non-LAG port of the named switch that has
// no cable, Down with reason no-cable, in port name order. A port without a
// cable has no Link, so this is where that reason is carried. It returns nil
// for a node that is not a switch or a switch whose ports are all cabled.
func (f *Fabric) Unlinked(node string) []LinkEnd {
	swCfg, ok := f.cfg.Switches[node]
	if !ok {
		return nil
	}
	var unlinked []LinkEnd
	for _, p := range swCfg.Ports.Ports() {
		if p.Kind == port.Lag {
			continue
		}
		ep := Endpoint{Node: node, Port: p.Name}
		if _, ok := f.byEnd[ep]; !ok {
			unlinked = append(unlinked, LinkEnd{Endpoint: ep, Oper: port.Down, Reason: ReasonNoCable})
		}
	}
	slices.SortFunc(unlinked, func(a, b LinkEnd) int { return cmp.Compare(a.Port, b.Port) })

	return unlinked
}

// Switch returns the virtual switch with the given name, or nil if not found.
func (f *Fabric) Switch(name string) *vswitch.Switch {
	return f.switches[name]
}

// Config returns an independent deep copy of the fabric configuration.
func (f *Fabric) Config() Config {
	return f.cfg.Clone()
}

func resolveLink(cable Cable, cfg Config) (LinkEnd, LinkEnd) {
	ethA, adminA := endpointPhyAndAdmin(cable.A, cfg)
	ethB, adminB := endpointPhyAndAdmin(cable.B, cfg)

	forcedA := isForced(ethA)
	forcedB := isForced(ethB)
	bothForced := forcedA && forcedB

	endA := LinkEnd{Endpoint: cable.A}
	endB := LinkEnd{Endpoint: cable.B}

	if cable.Fault.Kind == FaultCut {
		endA.Oper = port.Down
		endA.Reason = ReasonCut
		endB.Oper = port.Down
		endB.Reason = ReasonCut

		return endA, endB
	}

	isDeadDirection := cable.Fault.Kind == FaultDeadAToB || cable.Fault.Kind == FaultDeadBToA
	if isDeadDirection && !bothForced {
		endA.Oper = port.Down
		endA.Reason = ReasonDeadDirection
		endB.Oper = port.Down
		endB.Reason = ReasonDeadDirection

		return endA, endB
	}

	if adminA == port.Down || adminB == port.Down {
		endA.Oper = port.Down
		endB.Oper = port.Down
		endA.Reason, endB.Reason = ReasonPeerDown, ReasonPeerDown
		if adminA == port.Down {
			endA.Reason = ReasonAdminDown
		}
		if adminB == port.Down {
			endB.Reason = ReasonAdminDown
		}

		return endA, endB
	}

	if forcedA && ethA.Setting.SpeedBPS > 0 && !reaches(cable.LengthMeters, cable.Medium, ethA.Setting.SpeedBPS) {
		endA.Oper = port.Down
		endA.Reason = ReasonReachExceeded
		endB.Oper = port.Down
		endB.Reason = ReasonReachExceeded

		return endA, endB
	}
	if forcedB && ethB.Setting.SpeedBPS > 0 && !reaches(cable.LengthMeters, cable.Medium, ethB.Setting.SpeedBPS) {
		endA.Oper = port.Down
		endA.Reason = ReasonReachExceeded
		endB.Oper = port.Down
		endB.Reason = ReasonReachExceeded

		return endA, endB
	}

	var candidates []uint64
	candidates = append(candidates, ethA.SupportedSpeedsBPS...)
	if forcedA && ethA.Setting.SpeedBPS > 0 {
		candidates = append(candidates, ethA.Setting.SpeedBPS)
	}
	candidates = append(candidates, ethB.SupportedSpeedsBPS...)
	if forcedB && ethB.Setting.SpeedBPS > 0 {
		candidates = append(candidates, ethB.Setting.SpeedBPS)
	}
	if cable.TopSpeedBPS > 0 {
		candidates = append(candidates, cable.TopSpeedBPS)
	}
	hasDeclared := len(ethA.SupportedSpeedsBPS) > 0 || (forcedA && ethA.Setting.SpeedBPS > 0) ||
		len(ethB.SupportedSpeedsBPS) > 0 || (forcedB && ethB.Setting.SpeedBPS > 0)
	if !hasDeclared {
		candidates = append(candidates, 1_000_000_000)
	}

	var maxCandidate uint64
	var reaching []uint64
	for _, s := range candidates {
		if s > maxCandidate {
			maxCandidate = s
		}
		if reaches(cable.LengthMeters, cable.Medium, s) {
			reaching = append(reaching, s)
		}
	}

	if len(reaching) == 0 {
		endA.Oper = port.Down
		endA.Reason = ReasonReachExceeded
		endB.Oper = port.Down
		endB.Reason = ReasonReachExceeded

		return endA, endB
	}

	capSpeed := slices.Max(reaching)
	top := capSpeed
	if cable.TopSpeedBPS != 0 {
		top = min(cable.TopSpeedBPS, capSpeed)
	} else if capSpeed >= maxCandidate {
		top = 0
	}

	negotiated := phy.Negotiate(ethA, ethB, top)
	if negotiated.SpeedBPS == 0 {
		endA.Oper = port.Down
		endA.Reason = phy.ReasonSpeedMismatch
		endA.Speed = negotiated
		endB.Oper = port.Down
		endB.Reason = phy.ReasonSpeedMismatch
		endB.Speed = negotiated

		return endA, endB
	}

	endA.Oper = port.Up
	endA.Speed = negotiated
	endB.Oper = port.Up
	endB.Speed = negotiated

	return endA, endB
}

func isForced(e phy.Ethernet) bool {
	return e.Setting != nil && !e.Setting.AutoNegotiation
}

func reaches(lengthMeters float64, m Medium, speed uint64) bool {
	// A length of 0 reaches every speed because Reach returns 0 for a speed
	// with no specification row, so 0 <= 0 holds.
	return lengthMeters <= m.Reach(speed)
}

func endpointPhyAndAdmin(ep Endpoint, cfg Config) (phy.Ethernet, port.LinkState) {
	if _, isHost := cfg.Hosts[ep.Node]; isHost {
		return phy.Ethernet{}, port.Up
	}
	swCfg := cfg.Switches[ep.Node]
	p, _ := swCfg.Ports.Port(ep.Port)
	admin := p.AdminStatus

	var eth phy.Ethernet
	if swCfg.Phy != nil && swCfg.Phy.Ethernet != nil {
		if e, ok := swCfg.Phy.Ethernet[ep.Port]; ok {
			eth = e
		}
	}

	return eth, admin
}

// Mcheck forces protocol migration checking on a switch port at the current
// fabric clock, then queues what the spanning tree layer emitted and its next
// wake, as every other switch call inside the run does. It returns an error
// when the node is not a switch.
func (f *Fabric) Mcheck(node, portName string) error {
	sw, ok := f.switches[node]
	if !ok {
		return errs.New().
			Attr("node", node).
			Msgf("node %q is not a switch", node)
	}
	f.initRunState()
	sw.Mcheck(f.clock, portName)
	for _, em := range sw.Drain() {
		f.injectEmission(f.clock, node, em)
	}
	f.scheduleWake(node)

	return nil
}

// SetFault modifies the declared fault on the cable connecting endpoints a and b at the
// current fabric clock, re-evaluating operational link states and notifying attached switches.
// It returns an error if no cable connects the specified endpoints or if the fault configuration is invalid.
func (f *Fabric) SetFault(a, b Endpoint, fault Fault) error {
	if err := validateFault(fault); err != nil {
		return err
	}

	idx := slices.IndexFunc(f.links, func(l Link) bool {
		c := l.Cable
		return (c.A == a && c.B == b) || (c.A == b && c.B == a)
	})
	if idx < 0 {
		return errs.New().
			Attr("endpointA", a).
			Attr("endpointB", b).
			Msgf("no cable found connecting endpoints %v and %v", a, b)
	}

	f.links[idx].Fault = fault.Clone()

	cfgIdx := slices.IndexFunc(f.cfg.Cables, func(c Cable) bool {
		return (c.A == a && c.B == b) || (c.A == b && c.B == a)
	})
	if cfgIdx >= 0 {
		f.cfg.Cables[cfgIdx].Fault = fault.Clone()
	}

	linkA, linkB := resolveLink(f.links[idx].Cable, f.cfg)
	f.links[idx].A = linkA
	f.links[idx].B = linkB

	type endInfo struct {
		end  LinkEnd
		peer LinkEnd
	}
	ends := []endInfo{
		{end: linkA, peer: linkB},
		{end: linkB, peer: linkA},
	}

	for _, info := range ends {
		sw := f.switches[info.end.Node]
		if sw == nil {
			continue
		}

		sw.SetOperStatus(info.end.Port, info.end.Oper)
		up := info.end.Oper == port.Up
		p2p := f.portPointToPoint(info.end.Node, info.end.Port, info.end, info.peer.Endpoint)
		speed := info.end.Speed.SpeedBPS
		sw.LinkChange(f.clock, info.end.Port, up, p2p, speed)

		for _, em := range sw.Drain() {
			f.injectEmission(f.clock, info.end.Node, em)
		}
		f.scheduleWake(info.end.Node)
	}

	return nil
}

func (f *Fabric) portPointToPoint(node, portName string, end LinkEnd, peer Endpoint) bool {
	return portPointToPoint(f.cfg, node, portName, end, peer)
}

func portPointToPoint(cfg Config, node, portName string, end LinkEnd, peer Endpoint) bool {
	swCfg := cfg.Switches[node]
	if swCfg.STP != nil {
		if pCfg, ok := swCfg.STP.Ports[portName]; ok {
			if pCfg.PointToPoint == stp.PointToPointForceTrue {
				return true
			}
			if pCfg.PointToPoint == stp.PointToPointForceFalse {
				return false
			}
		}
	}

	if end.Speed.Duplex != phy.Full {
		return false
	}
	if _, isHost := cfg.Hosts[peer.Node]; isHost {
		return true
	}
	if peerSw, isSw := cfg.Switches[peer.Node]; isSw {
		return peerSw.Bridge != nil
	}

	return false
}

func (f *Fabric) uncabledPointToPoint(node, portName string) bool {
	swCfg := f.cfg.Switches[node]
	if swCfg.STP != nil {
		if pCfg, ok := swCfg.STP.Ports[portName]; ok {
			if pCfg.PointToPoint == stp.PointToPointForceTrue {
				return true
			}
			if pCfg.PointToPoint == stp.PointToPointForceFalse {
				return false
			}
		}
	}

	return false
}
