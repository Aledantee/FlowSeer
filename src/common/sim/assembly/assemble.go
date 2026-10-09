// Package assembly constructs and reconfigures fabrics from device specifications.
// Functions over a fabric are not safe for concurrent use.
package assembly

import (
	"maps"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// ConstructionSpec captures the topology and each switch's complete construction
// specification needed to construct an identical [fabric.Fabric]. Every switch specification's
// NodeID must equal its map key. Evidence is the catalog that cable and fabric.Uncabled
// evidence references resolve in; a reference it lacks makes the specification invalid.
type ConstructionSpec struct {
	Start         time.Time
	Switches      map[string]vswitch.ConstructionSpec
	Hosts         map[string]hostmodel.Config
	Reflectors    map[string]reflectormodel.Config
	Cables        []fabric.Cable
	Uncabled      []fabric.Uncabled
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
		cp.Cables = make([]fabric.Cable, len(s.Cables))
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
		cfg.Cables = make([]fabric.Cable, len(s.Cables))
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

// New constructs a validated [fabric.Fabric] from the provided configuration, computing
// operational link states and negotiated speeds across all cables before instantiating
// the constituent virtual switches, then starts every protocol layer at Start.
func New(cfg Config) (*fabric.Fabric, error) {
	spec, err := NewConstructionSpec(cfg)
	if err != nil {
		return nil, err
	}

	return NewWithSpec(spec)
}

// NewWithSpec constructs a validated [fabric.Fabric] from the provided construction specification,
// preserving every switch's node identity, trust metadata, and preloaded forwarding database seeds.
func NewWithSpec(spec ConstructionSpec) (*fabric.Fabric, error) {
	fab, err := build(nil, spec)
	if err != nil {
		return nil, err
	}

	return fab, nil
}

// Spec returns a [ConstructionSpec] capturing the normalized topology and every
// per-switch construction input needed to reconstruct this fabric. Port operational
// states are the configured ones; the states the cables derive are in each switch's
// Ports and in [fabric.Fabric.Links].
func Spec(f *fabric.Fabric) ConstructionSpec {
	top := f.Config()
	spec := ConstructionSpec{
		Start: top.Start, Cables: top.Cables, Uncabled: top.Uncabled,
		PhyAssumption: top.PhyAssumption, Evidence: top.Evidence,
		Switches: make(map[string]vswitch.ConstructionSpec), Hosts: make(map[string]hostmodel.Config), Reflectors: make(map[string]reflectormodel.Config),
	}
	for name, node := range top.Nodes {
		switch d := f.Device(name).(type) {
		case *vswitch.Switch:
			swSpec := d.Spec()
			swSpec.Config.Ports = node.Ports.Clone()
			spec.Switches[name] = swSpec
		case *hostmodel.Host:
			spec.Hosts[name] = d.Spec()
		case *reflectormodel.Reflector:
			spec.Reflectors[name] = d.Spec()
		}
	}
	return spec
}

func normalizeConstructionSpec(cur *fabric.Fabric, spec ConstructionSpec) (ConstructionSpec, error) {
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
			if current, ok := Spec(cur).Config().Switches[name]; ok {
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
			if current, ok := Spec(cur).Config().Hosts[name]; ok {
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
			if current, ok := Spec(cur).Config().Reflectors[name]; ok {
				if mac, ok := carry(current.Address); ok {
					refl.Address = mac
					cfg.Reflectors[name] = refl
				}
			}
		}
	}

	// Validate normalizes its own copy to judge it, and judges the submitted fault,
	// accepted multicast list, and fabric.Uncabled entries, whose answers and field
	// paths the normalization below would change.
	if err := cfg.Validate(); err != nil {
		return ConstructionSpec{}, err
	}
	cfg = cfg.Normalize()
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

func build(cur *fabric.Fabric, spec ConstructionSpec) (*fabric.Fabric, error) {
	norm, err := normalizeConstructionSpec(cur, spec)
	if err != nil {
		return nil, err
	}
	cfg := norm.Config()
	top := configuredTopology(cfg)
	top.Evidence = norm.Evidence
	byEnd := make(map[fabric.Endpoint]linkEndRef)
	links := make([]fabric.Link, len(top.Cables))
	for i, c := range top.Cables {
		links[i] = resolveLink(c, top)
		byEnd[c.A] = linkEndRef{end: &links[i].A}
		byEnd[c.B] = linkEndRef{end: &links[i].B}
	}
	uncabled := make(map[fabric.Endpoint]fabric.Uncabled)
	for _, entry := range cfg.Uncabled {
		uncabled[entry.Endpoint] = entry
	}
	for _, name := range slices.Sorted(maps.Keys(top.Nodes)) {
		n := top.Nodes[name]
		if swSpec, ok := norm.Switches[name]; ok {
			swSpec.Config.Ports, err = rebuildSwitchPorts(name, swSpec.Config.Ports, byEnd, uncabled)
			if err != nil {
				return nil, err
			}
			if previous := curSwitch(cur, name); previous != nil {
				n.Device, n.Output, err = vswitch.Derive(previous, swSpec, cfg.Start)
			} else {
				n.Device, err = vswitch.NewWithSpec(swSpec)
			}
		} else if h, ok := norm.Hosts[name]; ok {
			n.Device, err = hostmodel.New(name, h)
		} else {
			n.Device, err = reflectormodel.New(name, norm.Reflectors[name])
		}
		if err != nil {
			return nil, errs.Wrapf(err, "build node %q", name)
		}
		top.Nodes[name] = n
	}
	return fabric.New(top)
}

func curSwitch(f *fabric.Fabric, name string) *vswitch.Switch {
	if f == nil {
		return nil
	}
	sw, _ := f.Device(name).(*vswitch.Switch)
	return sw
}

func configuredTopology(cfg Config) fabric.Config {
	top := fabric.Config{DefaultQueue: (*vswitch.Switch)(nil), Start: cfg.Start, Cables: cfg.Cables, Uncabled: cfg.Uncabled, PhyAssumption: cfg.PhyAssumption, Nodes: make(map[string]fabric.Node)}
	for name, sw := range cfg.Switches {
		n := fabric.Node{Ports: sw.Ports.Clone(), Physical: make(map[string]cablemodel.End), Repeats: sw.Bridge == nil, Snapshot: true}
		for _, p := range sw.Ports.Ports() {
			ep := fabric.Endpoint{Node: name, Port: p.Name}
			n.Physical[p.Name] = endpointPhyAndAdmin(ep, cfg)
		}
		top.Nodes[name] = n
	}
	for name := range cfg.Hosts {
		ep := fabric.Endpoint{Node: name}
		top.Nodes[name] = fabric.Node{Physical: map[string]cablemodel.End{"": endpointPhyAndAdmin(ep, cfg)}}
	}
	for name, r := range cfg.Reflectors {
		n := fabric.Node{Physical: make(map[string]cablemodel.End)}
		for portName := range r.Ports {
			n.Physical[portName] = endpointPhyAndAdmin(fabric.Endpoint{Node: name, Port: portName}, cfg)
		}
		top.Nodes[name] = n
	}
	return top
}

func rebuildSwitchPorts(name string, swPorts port.Table, byEnd map[fabric.Endpoint]linkEndRef, uncabled map[fabric.Endpoint]fabric.Uncabled) (port.Table, error) {
	b := port.NewBuilder()
	ports := swPorts.Ports()

	for _, p := range ports {
		switch {
		case p.Kind != port.LAG:
			ep := fabric.Endpoint{Node: name, Port: p.Name}
			if ref, ok := byEnd[ep]; ok {
				p.OperStatus = ref.end.Oper
			} else {
				if _, ok := uncabled[ep]; ok {
					p.OperStatus = port.Down
				} else {
					p.OperStatus = port.Unknown
				}
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

// Retention reports, for each switch, every layer including bridge and filter
// from the most recent Derive, Fork, or
// Configure that produced it; a Configure of one switch leaves the others' outcomes as they were.
func Retention(f *fabric.Fabric) map[string]vswitch.Retention {
	nodes := f.Config().Nodes
	ret := make(map[string]vswitch.Retention)
	for _, name := range slices.Sorted(maps.Keys(nodes)) {
		f.Inspect(name, func(d device.Device) {
			if sw, ok := d.(*vswitch.Switch); ok {
				ret[name] = sw.Retention()
			}
		})
	}
	return ret
}

func endpointPhyAndAdmin(ep fabric.Endpoint, cfg Config) cablemodel.End {
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
