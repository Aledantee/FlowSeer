// Package fabric composes virtual switches, hosts, and cables into a Layer 2 network fabric.
package fabric

import (
	"maps"
	"slices"
	"strconv"
	"time"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// ConstructionSpec captures the topology and each switch's complete construction
// specification needed to construct an identical [Fabric]. Every switch specification's
// NodeID must equal its map key. Evidence is the catalog that cable and Uncabled
// evidence references resolve in; a reference it lacks makes the specification invalid.
type ConstructionSpec struct {
	Start         time.Time
	Switches      map[string]vswitch.ConstructionSpec
	Hosts         map[string]hostmodel.Config
	Reflectors    map[string]reflectormodel.Config
	Cables        []Cable
	Uncabled      []Uncabled
	PhyAssumption *cablemodel.PhyAssumption
	Evidence      analysis.EvidenceCatalog
}

// Clone returns an independent deep copy of the construction specification.
func (s ConstructionSpec) Clone() ConstructionSpec {
	cp := ConstructionSpec{
		Start:         s.Start,
		Uncabled:      cloneUncabled(s.Uncabled),
		PhyAssumption: s.PhyAssumption.Clone(),
		Evidence:      s.Evidence,
	}
	if s.Switches != nil {
		cp.Switches = make(map[string]vswitch.ConstructionSpec, len(s.Switches))
		for name, spec := range s.Switches {
			cp.Switches[name] = spec.Clone()
		}
	}
	if s.Hosts != nil {
		cp.Hosts = make(map[string]hostmodel.Config, len(s.Hosts))
		for name, host := range s.Hosts {
			cp.Hosts[name] = host.Clone()
		}
	}
	if s.Reflectors != nil {
		cp.Reflectors = make(map[string]reflectormodel.Config, len(s.Reflectors))
		for name, refl := range s.Reflectors {
			cp.Reflectors[name] = refl.Clone()
		}
	}
	if s.Cables != nil {
		cp.Cables = make([]Cable, len(s.Cables))
		for i, cable := range s.Cables {
			cp.Cables[i] = cable.Clone()
		}
	}

	return cp
}

// Config returns an independent fabric configuration assembled from the topology
// and per-switch configurations in the construction specification. The evidence
// catalog has no place in a Config and is left out.
func (s ConstructionSpec) Config() Config {
	cfg := Config{
		Start:         s.Start,
		Uncabled:      cloneUncabled(s.Uncabled),
		PhyAssumption: s.PhyAssumption.Clone(),
	}
	if s.Switches != nil {
		cfg.Switches = make(map[string]vswitch.Config, len(s.Switches))
		for name, spec := range s.Switches {
			cfg.Switches[name] = spec.Config.Clone()
		}
	}
	if s.Hosts != nil {
		cfg.Hosts = make(map[string]hostmodel.Config, len(s.Hosts))
		for name, host := range s.Hosts {
			cfg.Hosts[name] = host.Clone()
		}
	}
	if s.Reflectors != nil {
		cfg.Reflectors = make(map[string]reflectormodel.Config, len(s.Reflectors))
		for name, refl := range s.Reflectors {
			cfg.Reflectors[name] = refl.Clone()
		}
	}
	if s.Cables != nil {
		cfg.Cables = make([]Cable, len(s.Cables))
		for i, cable := range s.Cables {
			cfg.Cables[i] = cable.Clone()
		}
	}

	return cfg
}

// Normalize validates and returns an independent normalized construction specification.
func (s ConstructionSpec) Normalize() (ConstructionSpec, error) {
	return normalizeConstructionSpec(nil, s)
}

// Equal reports whether two valid construction specifications normalize identically,
// evidence catalogs included.
func (s ConstructionSpec) Equal(other ConstructionSpec) bool {
	a, err := s.Normalize()
	if err != nil {
		return false
	}
	b, err := other.Normalize()
	if err != nil {
		return false
	}

	if !equalNormalizedConfigs(a.Config(), b.Config()) || len(a.Switches) != len(b.Switches) {
		return false
	}
	if !slices.Equal(a.Evidence.Entries(), b.Evidence.Entries()) {
		return false
	}
	for name, spec := range a.Switches {
		otherSpec, ok := b.Switches[name]
		if !ok || !spec.Equal(otherSpec) {
			return false
		}
	}

	return true
}

// NewConstructionSpec validates cfg and returns its normalized construction
// specification with stable switch node identities.
func NewConstructionSpec(cfg Config) (ConstructionSpec, error) {
	spec := ConstructionSpec{
		Start:         cfg.Start,
		Hosts:         cfg.Hosts,
		Reflectors:    cfg.Reflectors,
		Cables:        cfg.Cables,
		Uncabled:      cfg.Uncabled,
		PhyAssumption: cfg.PhyAssumption,
	}
	if cfg.Switches != nil {
		spec.Switches = make(map[string]vswitch.ConstructionSpec, len(cfg.Switches))
		for name, switchConfig := range cfg.Switches {
			spec.Switches[name] = vswitch.ConstructionSpec{Config: switchConfig, NodeID: name}
		}
	}

	return spec.Normalize()
}

// New constructs a validated [Fabric] from the provided configuration, computing
// operational link states and negotiated speeds across all cables before instantiating
// the constituent virtual switches, then starts every protocol layer at Start.
func New(cfg Config) (*Fabric, error) {
	spec, err := NewConstructionSpec(cfg)
	if err != nil {
		return nil, err
	}

	return NewWithSpec(spec)
}

// NewWithSpec constructs a validated [Fabric] from the provided construction specification,
// preserving every switch's node identity, trust metadata, and preloaded forwarding database seeds.
func NewWithSpec(spec ConstructionSpec) (*Fabric, error) {
	fab, err := build(nil, spec)
	if err != nil {
		return nil, err
	}

	return fab, nil
}

// Spec returns a [ConstructionSpec] capturing the normalized topology and every
// per-switch construction input needed to reconstruct this fabric. Port operational
// states are the configured ones; the states the cables derive are in each switch's
// Ports and in [Fabric.Links].
func (f *Fabric) Spec() ConstructionSpec {
	top := f.topologyCopy()
	spec := ConstructionSpec{
		Start: top.start, Cables: top.cables, Uncabled: top.uncabled,
		PhyAssumption: top.assumption, Evidence: top.evidence,
		Switches: make(map[string]vswitch.ConstructionSpec), Hosts: make(map[string]hostmodel.Config), Reflectors: make(map[string]reflectormodel.Config),
	}
	for name, node := range top.nodes {
		switch d := node.device.(type) {
		case *vswitch.Switch:
			swSpec := d.Spec()
			swSpec.Config.Ports = node.ports.Clone()
			spec.Switches[name] = swSpec
		case *hostmodel.Host:
			spec.Hosts[name] = d.Spec()
		case *reflectormodel.Reflector:
			spec.Reflectors[name] = d.Spec()
		}
	}
	return spec
}

func normalizeConstructionSpec(cur *Fabric, spec ConstructionSpec) (ConstructionSpec, error) {
	owned := spec.Clone()
	names := make([]string, 0, len(owned.Switches))
	for name := range owned.Switches {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		switchSpec := owned.Switches[name]
		if switchSpec.NodeID != name {
			return ConstructionSpec{}, errs.New().
				Attr("switch", name).
				Attr("node_id", switchSpec.NodeID).
				Msgf("switch %q construction NodeID must equal its map key", name)
		}
	}
	for i, cable := range owned.Cables {
		if err := cablemodel.ValidateFault(cable.Fault); err != nil {
			return ConstructionSpec{}, errs.Wrapf(err, "cable %d fault", i)
		}
		if err := validateEvidence("cables."+strconv.Itoa(i), cable.Evidence, owned.Evidence); err != nil {
			return ConstructionSpec{}, err
		}
	}
	for i, entry := range owned.Uncabled {
		if err := validateEvidence("uncabled."+strconv.Itoa(i), entry.Evidence, owned.Evidence); err != nil {
			return ConstructionSpec{}, err
		}
	}

	cfg := owned.Config()
	// Uncabled and accepted multicast field paths name submitted positions,
	// which normalization's sort would otherwise replace.
	cabled := make(map[Endpoint]int, len(cfg.Cables)*2)
	for _, cable := range cfg.Cables {
		cabled[cable.A]++
		cabled[cable.B]++
	}
	if err := cfg.validateUncabled(cabled); err != nil {
		return ConstructionSpec{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Hosts)) {
		if err := cfg.Hosts[name].Accept.Validate("hosts." + name + ".accept"); err != nil {
			return ConstructionSpec{}, err
		}
	}
	if cur != nil {
		usedMACs := configuredMACs(cfg)
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
		for name, swCfg := range cfg.Switches {
			if swCfg.MAC != (netaddr.MAC{}) {
				continue
			}
			if current, ok := cur.Config().Switches[name]; ok {
				if mac, ok := carry(current.MAC); ok {
					swCfg.MAC = mac
					cfg.Switches[name] = swCfg
				}
			}
		}
		for name, host := range cfg.Hosts {
			if host.Address != (netaddr.MAC{}) {
				continue
			}
			if current, ok := cur.Config().Hosts[name]; ok {
				if mac, ok := carry(current.Address); ok {
					host.Address = mac
					cfg.Hosts[name] = host
				}
			}
		}
		for name, refl := range cfg.Reflectors {
			if refl.Address != (netaddr.MAC{}) {
				continue
			}
			if current, ok := cur.Config().Reflectors[name]; ok {
				if mac, ok := carry(current.Address); ok {
					refl.Address = mac
					cfg.Reflectors[name] = refl
				}
			}
		}
	}

	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return ConstructionSpec{}, err
	}
	owned.Start = cfg.Start
	owned.Hosts = cfg.Hosts
	owned.Reflectors = cfg.Reflectors
	owned.Cables = cfg.Cables
	owned.Uncabled = cfg.Uncabled
	owned.PhyAssumption = cfg.PhyAssumption
	for _, name := range names {
		switchSpec := owned.Switches[name]
		switchSpec.Config = cfg.Switches[name]
		for i := range switchSpec.Seeds {
			switchSpec.Seeds[i].LearnedAt = switchSpec.Seeds[i].LearnedAt.UTC()
		}
		normalized, err := switchSpec.Normalize()
		if err != nil {
			return ConstructionSpec{}, errs.Wrapf(err, "switch %q", name)
		}
		owned.Switches[name] = normalized
	}

	return owned, nil
}

func validateEvidence(field string, refs []trace.EvidenceRef, catalog analysis.EvidenceCatalog) error {
	for j, ref := range refs {
		if _, ok := catalog.Lookup(ref); !ok {
			return errs.New().
				Attr("field", field+".evidence."+strconv.Itoa(j)).
				Attr("evidence", ref).
				Msgf("evidence reference %q is not in the evidence catalog", ref)
		}
	}

	return nil
}

func configuredMACs(cfg Config) map[netaddr.MAC]struct{} {
	used := make(map[netaddr.MAC]struct{})
	for _, swCfg := range cfg.Switches {
		if swCfg.MAC != (netaddr.MAC{}) {
			used[swCfg.MAC] = struct{}{}
		}
		if swCfg.STP != nil && swCfg.STP.Address != (netaddr.MAC{}) {
			used[swCfg.STP.Address] = struct{}{}
		}
		if swCfg.Routing != nil {
			for _, vrf := range swCfg.Routing.VRFs {
				for _, iface := range vrf.Interfaces {
					if iface.MAC != (netaddr.MAC{}) {
						used[iface.MAC] = struct{}{}
					}
				}
			}
		}
	}
	for _, host := range cfg.Hosts {
		if host.Address != (netaddr.MAC{}) {
			used[host.Address] = struct{}{}
		}
	}
	for _, refl := range cfg.Reflectors {
		if refl.Address != (netaddr.MAC{}) {
			used[refl.Address] = struct{}{}
		}
	}

	return used
}

func build(cur *Fabric, spec ConstructionSpec) (*Fabric, error) {
	norm, err := normalizeConstructionSpec(cur, spec)
	if err != nil {
		return nil, err
	}
	cfg := norm.Config()
	top := configuredTopology(cfg)
	top.evidence = norm.Evidence
	byEnd := make(map[Endpoint]linkEndRef)
	links := make([]Link, len(top.cables))
	for i, c := range top.cables {
		links[i], _ = resolveLink(c, top)
		byEnd[c.A] = linkEndRef{link: &links[i], end: &links[i].A, peer: &links[i].B}
		byEnd[c.B] = linkEndRef{link: &links[i], end: &links[i].B, peer: &links[i].A}
	}
	uncabled := make(map[Endpoint]Uncabled)
	for _, entry := range cfg.Uncabled {
		uncabled[entry.Endpoint] = entry
	}
	for _, name := range slices.Sorted(maps.Keys(top.nodes)) {
		n := top.nodes[name]
		if swSpec, ok := norm.Switches[name]; ok {
			swSpec.Config.Ports, err = rebuildSwitchPorts(name, swSpec.Config.Ports, byEnd, uncabled)
			if err != nil {
				return nil, err
			}
			if previous := curSwitch(cur, name); previous != nil {
				n.device, n.output, err = vswitch.Derive(previous, swSpec, cfg.Start)
			} else {
				n.device, err = vswitch.NewWithSpec(swSpec)
			}
		} else if h, ok := norm.Hosts[name]; ok {
			n.device, err = hostmodel.New(name, h)
		} else {
			n.device, err = reflectormodel.New(name, norm.Reflectors[name])
		}
		if err != nil {
			return nil, errs.Wrapf(err, "build node %q", name)
		}
		top.nodes[name] = n
	}
	return newTopology(top)
}

func curSwitch(f *Fabric, name string) *vswitch.Switch {
	if f == nil {
		return nil
	}
	return f.Switch(name)
}

func configuredTopology(cfg Config) topology {
	top := topology{defaultQueue: (*vswitch.Switch)(nil), start: cfg.Start, cables: cfg.Cables, uncabled: cfg.Uncabled, assumption: cfg.PhyAssumption, nodes: make(map[string]builtNode)}
	for name, sw := range cfg.Switches {
		n := builtNode{ports: sw.Ports.Clone(), physical: make(map[string]cablemodel.End), repeats: sw.Bridge == nil, snapshot: true}
		for _, p := range sw.Ports.Ports() {
			ep := Endpoint{Node: name, Port: p.Name}
			n.physical[p.Name] = endpointPhyAndAdmin(ep, cfg)
		}
		top.nodes[name] = n
	}
	for name := range cfg.Hosts {
		ep := Endpoint{Node: name}
		top.nodes[name] = builtNode{physical: map[string]cablemodel.End{"": endpointPhyAndAdmin(ep, cfg)}}
	}
	for name, r := range cfg.Reflectors {
		n := builtNode{physical: make(map[string]cablemodel.End)}
		for portName := range r.Ports {
			n.physical[portName] = endpointPhyAndAdmin(Endpoint{Node: name, Port: portName}, cfg)
		}
		top.nodes[name] = n
	}
	return top
}

func rebuildSwitchPorts(name string, swPorts port.Table, byEnd map[Endpoint]linkEndRef, uncabled map[Endpoint]Uncabled) (port.Table, error) {
	b := port.NewBuilder()
	ports := swPorts.Ports()

	for _, p := range ports {
		switch {
		case p.Kind != port.LAG:
			ep := Endpoint{Node: name, Port: p.Name}
			if ref, ok := byEnd[ep]; ok {
				p.OperStatus = ref.end.Oper
			} else {
				p.OperStatus = unlinkedEnd(ep, uncabled).Oper
			}
		case len(swPorts.Members(p.Name)) == 0:
			// A LAG with no member hears no link and would keep whatever
			// the configuration said.
			p.OperStatus = port.Down
		}
		b.Add(p)
	}

	newTable, err := b.Build()
	if err != nil {
		return port.Table{}, errs.Wrapf(err, "rebuild ports for switch %q", name)
	}

	return newTable, nil
}

// Switch returns the virtual switch with the given name, or nil if not found.
func (f *Fabric) Switch(name string) *vswitch.Switch {
	sw, _ := f.nodeDevice(name).(*vswitch.Switch)
	return sw
}

// Config returns an independent deep copy of the fabric configuration. Port operational states are
// the configured ones; the derived states are in each switch's Ports and in [Fabric.Links].
func (f *Fabric) Config() Config {
	return f.Spec().Config()
}

// Retention reports, for each switch, every layer including bridge and filter
// from the most recent Derive, Fork, or
// Configure that produced it; a Configure of one switch leaves the others' outcomes as they were.
func (f *Fabric) Retention() map[string]vswitch.Retention {
	ret := make(map[string]vswitch.Retention, len(f.switchNames()))
	for _, name := range f.switchNames() {
		sw := f.Switch(name)
		ret[name] = sw.Retention()
	}
	return ret
}

func endpointPhyAndAdmin(ep Endpoint, cfg Config) cablemodel.End {
	end := cablemodel.End{Admin: port.Up, Label: endpointLabel(ep), Scope: endpointScope(ep)}
	if host, ok := cfg.Hosts[ep.Node]; ok {
		end.Ethernet = host.Ethernet
		return end
	}
	if refl, ok := cfg.Reflectors[ep.Node]; ok {
		end.Ethernet = refl.Ports[ep.Port]
		return end
	}
	swCfg := cfg.Switches[ep.Node]
	p, _ := swCfg.Ports.Port(ep.Port)
	end.Admin = p.AdminStatus
	if swCfg.Phy != nil {
		end.Ethernet = swCfg.Phy.Ethernet[ep.Port]
	}
	return end
}

// mcheck forces protocol migration checking on a switch port at the current
// fabric clock, then queues what the spanning tree layer emitted and its next
// wake, as every other switch call inside the run does. It returns an error
// when the node is not a switch.
func (f *Fabric) mcheck(node, portName string) error {
	sw := f.Switch(node)
	ok := sw != nil
	if !ok {
		return errs.New().
			Attr("node", node).
			Msgf("node %q is not a switch", node)
	}
	return f.operate(node, func(d device.Device, now time.Time) (device.Output, error) {
		return d.(*vswitch.Switch).Mcheck(now, portName), nil
	})
}
