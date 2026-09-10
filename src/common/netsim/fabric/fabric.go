// Package fabric composes virtual switches, hosts, and cables into a Layer 2 network fabric.
package fabric

import (
	"cmp"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Fabric orchestrates switches, hosts, and interconnecting cables into an integrated Layer 2 network.
//
// A Fabric is not safe for concurrent use.
type Fabric struct {
	cfg      Config
	links    []Link
	switches map[string]*vswitch.Switch
	byEnd    map[Endpoint]linkEndRef
}

type linkEndRef struct {
	link *Link
	end  *LinkEnd
	peer *LinkEnd
}

// New constructs a validated [Fabric] from the provided configuration, computing
// operational link states and negotiated speeds across all cables before instantiating
// the constituent virtual switches.
func New(cfg Config) (*Fabric, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cloned := cfg.Clone()

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

		newOper := make(map[string]port.LinkState, len(ports))
		for _, p := range ports {
			if p.Kind != port.Lag {
				ep := Endpoint{Node: name, Port: p.Name}
				if ref, ok := byEnd[ep]; ok {
					newOper[p.Name] = ref.end.Oper
				} else {
					newOper[p.Name] = port.Down
				}
			}
		}

		for _, p := range ports {
			if p.Kind == port.Lag {
				lagUp := false
				for _, mem := range swCfg.Ports.Members(p.Name) {
					if newOper[mem.Name] == port.Up {
						lagUp = true
						break
					}
				}
				if lagUp {
					newOper[p.Name] = port.Up
				} else {
					newOper[p.Name] = port.Down
				}
			}
		}

		for _, p := range ports {
			p.OperStatus = newOper[p.Name]
			b.Add(p)
		}

		newTable, err := b.Build()
		if err != nil {
			return nil, errs.Wrapf(err, "rebuild ports for switch %q", name)
		}
		swCfg.Ports = newTable

		switches[name] = vswitch.New(swCfg)
	}

	return &Fabric{
		cfg:      cloned,
		links:    links,
		switches: switches,
		byEnd:    byEnd,
	}, nil
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

// Unlinked returns the names of all non-LAG ports on the specified switch
// that have no cable attached, sorted alphabetically. If the node is not a switch
// or all its ports are cabled, Unlinked returns nil.
func (f *Fabric) Unlinked(node string) []string {
	swCfg, ok := f.cfg.Switches[node]
	if !ok {
		return nil
	}
	var unlinked []string
	for _, p := range swCfg.Ports.Ports() {
		if p.Kind == port.Lag {
			continue
		}
		ep := Endpoint{Node: node, Port: p.Name}
		if _, ok := f.byEnd[ep]; !ok {
			unlinked = append(unlinked, p.Name)
		}
	}
	if len(unlinked) == 0 {
		return nil
	}
	slices.Sort(unlinked)

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
		endA.Reason = ReasonPeerDown
		endB.Oper = port.Down
		endB.Reason = ReasonPeerDown

		return endA, endB
	}

	negotiated := phy.Negotiate(ethA, ethB, cable.TopSpeedBPS)
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
