// Package fabric composes virtual switches, hosts, and cables into a Layer 2 network fabric.
package fabric

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
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

// Fabric is a set of switches, hosts, and reflectors joined by cables, with every switch port's operational
// state decided by its cable, by an Uncabled entry, or left Unknown when neither names it.
//
// A Fabric is not safe for concurrent use.
type Fabric struct {
	cfg             Config
	evidence        analysis.EvidenceCatalog
	runtimeEvidence analysis.EvidenceCatalog
	links           []Link
	linkTrust       []linkTrust
	uncabled        map[Endpoint]Uncabled
	switches        map[string]*vswitch.Switch
	devices         map[string]device.Device
	byEnd           map[Endpoint]linkEndRef
	clock           time.Time
	stepped         bool
	queue           []Arrival
	attachments     []attachedSource
	pendingHosts    []pendingHostInjection
	wakes           map[string]time.Time
	dequeueItems    map[Endpoint]int
	wakeItems       map[string]int
	nextFrameID     FrameID
	nextSeq         uint64
	journeys        map[FrameID]*Journey
	entered         map[FrameID]map[Endpoint]bool
	cableCrossings  map[Endpoint]uint
	busyUntil       map[Endpoint]time.Time
	egress          map[Endpoint]*egressQueue
	counters        map[Endpoint]*Counters

	// unstatedBacked retains each endpoint's first queue-threshold evidence ref.
	unstatedBacked map[Endpoint]trace.EvidenceRef

	// metadataCache holds the value [Fabric.Metadata] last built. Its inputs
	// are fixed after construction except where [Fabric.SetFault] rewrites a
	// link and its trust, [Fabric.Configure] rebuilds a switch's ports and
	// links, or a queue with no stated buffer first backs up past one
	// maximum-size frame; each nils the cache. The value is immutable, so a
	// fork may share it.
	metadataCache *analysis.Metadata

	// inflight counts the arrivals of each frame that are still in the queue
	// or an egress queue. A frame settles, folding and possibly freeing its
	// journey, when its count reaches zero at the end of the call that
	// touched it.
	inflight map[FrameID]int
	// heldAggregates records what a freed aggregate journey left behind: a
	// switch held the frame for neighbor resolution, so the frame's FrameID
	// stays under the device of the frame's last entry, ascending, as a
	// placeholder a release can claim. The journey itself is freed, and a
	// placeholder carries no payload. A hold resolution abandons stays a
	// candidate, as a retained held journey does, so a later release on the
	// same device may name it and claim its placeholder.
	heldAggregates map[string][]FrameID
	// flows accumulates per-flow statistics as journeys settle.
	flows map[FlowID]*FlowStats
	// touched collects the frame IDs count changes touched during the current
	// Inject or Step, drained as it settles them.
	touched []FrameID

	// err is the first scheduling-invariant breach [Fabric.scheduleDequeue]
	// recorded. It is sticky: once set, [Fabric.Step] refuses to advance and
	// [Fabric.Err] reports it. A caller must read Err to see a fault, because
	// a short step count alone does not distinguish a fault from an empty
	// queue.
	err error
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
	if err := fab.startLayers(fab.switchNames()); err != nil {
		return nil, err
	}

	return fab, nil
}

// Spec returns a [ConstructionSpec] capturing the normalized topology and every
// per-switch construction input needed to reconstruct this fabric. Port operational
// states are the configured ones; the states the cables derive are in each switch's
// Ports and in [Fabric.Links].
func (f *Fabric) Spec() ConstructionSpec {
	spec := ConstructionSpec{
		Start:         f.cfg.Start,
		Switches:      make(map[string]vswitch.ConstructionSpec, len(f.switches)),
		Hosts:         make(map[string]hostmodel.Config, len(f.cfg.Hosts)),
		Reflectors:    make(map[string]reflectormodel.Config, len(f.cfg.Reflectors)),
		Cables:        make([]Cable, len(f.cfg.Cables)),
		Uncabled:      cloneUncabled(f.cfg.Uncabled),
		PhyAssumption: f.cfg.PhyAssumption.Clone(),
		Evidence:      f.evidence,
	}
	for name, sw := range f.switches {
		swSpec := sw.Spec()
		swSpec.Config.Ports = f.cfg.Switches[name].Ports.Clone()
		spec.Switches[name] = swSpec
	}
	for name, host := range f.cfg.Hosts {
		spec.Hosts[name] = host.Clone()
	}
	for name := range f.cfg.Reflectors {
		spec.Reflectors[name] = f.devices[name].(*reflectormodel.Reflector).Spec()
	}
	for i, cable := range f.cfg.Cables {
		spec.Cables[i] = cable.Clone()
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
			if current, ok := cur.cfg.Switches[name]; ok {
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
			if current, ok := cur.cfg.Hosts[name]; ok {
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
			if current, ok := cur.cfg.Reflectors[name]; ok {
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

// build makes the fabric without starting any protocol layer, so Derive can
// swap in cloned layers first.
func build(cur *Fabric, spec ConstructionSpec) (*Fabric, error) {
	norm, err := normalizeConstructionSpec(cur, spec)
	if err != nil {
		return nil, err
	}
	cloned := norm.Config()

	links := make([]Link, len(cloned.Cables))
	trust := make([]linkTrust, len(cloned.Cables))
	byEnd := make(map[Endpoint]linkEndRef, len(cloned.Cables)*2)

	for i := range cloned.Cables {
		links[i], trust[i] = resolveLink(cloned.Cables[i], cloned)

		byEnd[links[i].A.Endpoint] = linkEndRef{link: &links[i], end: &links[i].A, peer: &links[i].B}
		byEnd[links[i].B.Endpoint] = linkEndRef{link: &links[i], end: &links[i].B, peer: &links[i].A}
	}

	uncabled := make(map[Endpoint]Uncabled, len(cloned.Uncabled))
	for _, entry := range cloned.Uncabled {
		uncabled[entry.Endpoint] = entry
	}

	// Rebuild each switch's port table with oper states derived from the cables.
	switches := make(map[string]*vswitch.Switch, len(cloned.Switches))
	devices := make(map[string]device.Device, len(cloned.Switches))
	initialOutput := make(map[string]device.Output, len(cloned.Switches))
	for name, swCfg := range cloned.Switches {
		newTable, err := rebuildSwitchPorts(name, swCfg.Ports, byEnd, uncabled)
		if err != nil {
			return nil, err
		}
		swCfg.Ports = newTable

		swSpec := norm.Switches[name].Clone()
		swSpec.Config = swCfg
		sw, err := vswitch.NewWithSpec(swSpec)
		if err != nil {
			return nil, errs.Wrapf(err, "build switch %q", name)
		}
		switches[name] = sw
		devices[name] = sw

		out, err := notifyLAGMembers(sw, name, cloned, cloned.Start, byEnd)
		if err != nil {
			return nil, err
		}
		initialOutput[name] = out
	}

	for _, name := range slices.Sorted(maps.Keys(cloned.Hosts)) {
		h, err := hostmodel.New(name, cloned.Hosts[name])
		if err != nil {
			return nil, err
		}
		devices[name] = h
	}

	for _, name := range slices.Sorted(maps.Keys(cloned.Reflectors)) {
		refl, err := reflectormodel.New(name, cloned.Reflectors[name])
		if err != nil {
			return nil, err
		}
		for _, p := range refl.Ports().Ports() {
			ref := byEnd[Endpoint{Node: name, Port: p.Name}]
			if _, err := refl.LinkChange(cloned.Start, p.Name, device.Link{State: ref.end.Oper}); err != nil {
				return nil, err
			}
		}
		devices[name] = refl
	}

	fab := &Fabric{
		cfg:            cloned,
		evidence:       norm.Evidence,
		links:          links,
		linkTrust:      trust,
		uncabled:       uncabled,
		switches:       switches,
		devices:        devices,
		byEnd:          byEnd,
		clock:          cloned.Start,
		wakes:          make(map[string]time.Time),
		dequeueItems:   make(map[Endpoint]int),
		wakeItems:      make(map[string]int),
		nextFrameID:    1,
		nextSeq:        1,
		journeys:       make(map[FrameID]*Journey),
		entered:        make(map[FrameID]map[Endpoint]bool),
		cableCrossings: make(map[Endpoint]uint),
		inflight:       make(map[FrameID]int),
		heldAggregates: make(map[string][]FrameID),
	}

	for _, name := range fab.switchNames() {
		if cur == nil || cur.Switch(name) == nil {
			fab.applyOutput(fab.clock, name, initialOutput[name])
		}
	}
	return fab, nil
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

func notifyLAGMembers(sw *vswitch.Switch, name string, cfg Config, now time.Time, byEnd map[Endpoint]linkEndRef) (device.Output, error) {
	var output device.Output
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
			out, err := sw.LinkChange(now, p.Name, linkReport(cfg, *ref.end, ref.peer.Endpoint))
			if err != nil {
				return device.Output{}, err
			}
			output.Emissions = append(output.Emissions, out.Emissions...)
			output.Drops = append(output.Drops, out.Drops...)
		}
	}
	return output, nil
}

// Fork returns an independent execution copy of the fabric at its current simulation point.
// Construction inputs and immutable frames are shared; all simulation state is deep-copied.
func (f *Fabric) Fork() *Fabric {
	links := make([]Link, len(f.links))
	for i, l := range f.links {
		links[i] = cloneLink(l)
	}

	byEnd := make(map[Endpoint]linkEndRef, len(links)*2)
	for i := range links {
		byEnd[links[i].A.Endpoint] = linkEndRef{link: &links[i], end: &links[i].A, peer: &links[i].B}
		byEnd[links[i].B.Endpoint] = linkEndRef{link: &links[i], end: &links[i].B, peer: &links[i].A}
	}

	var uncabled map[Endpoint]Uncabled
	if f.uncabled != nil {
		uncabled = make(map[Endpoint]Uncabled, len(f.uncabled))
		for ep, u := range f.uncabled {
			uncabled[ep] = u.Clone()
		}
	}

	var switches map[string]*vswitch.Switch
	if f.switches != nil {
		switches = make(map[string]*vswitch.Switch, len(f.switches))
		for name, sw := range f.switches {
			switches[name] = sw.Fork().(*vswitch.Switch)
		}
	}

	var devices map[string]device.Device
	if f.devices != nil {
		devices = make(map[string]device.Device, len(f.devices))
		for name, d := range f.devices {
			if sw, ok := switches[name]; ok {
				devices[name] = sw
			} else {
				devices[name] = d.Fork()
			}
		}
	}

	var queue []Arrival
	if len(f.queue) > 0 {
		queue = slices.Clone(f.queue)
	}
	attachments := make([]attachedSource, len(f.attachments))
	for i, source := range f.attachments {
		attachments[i] = source
		attachments[i].attachment.Source = source.attachment.Source.Clone()
		attachments[i].frame = cloneFrame(source.frame)
	}
	pendingHosts := slices.Clone(f.pendingHosts)
	for i := range pendingHosts {
		pendingHosts[i].frame = cloneFrame(pendingHosts[i].frame)
	}

	var wakes map[string]time.Time
	if f.wakes != nil {
		wakes = maps.Clone(f.wakes)
	}

	var dequeueItems map[Endpoint]int
	if len(f.dequeueItems) > 0 {
		dequeueItems = maps.Clone(f.dequeueItems)
	}

	var wakeItems map[string]int
	if len(f.wakeItems) > 0 {
		wakeItems = maps.Clone(f.wakeItems)
	}

	var journeys map[FrameID]*Journey
	if f.journeys != nil {
		journeys = make(map[FrameID]*Journey, len(f.journeys))
		for id, j := range f.journeys {
			journeys[id] = j.shallowClone()
		}
	}

	var entered map[FrameID]map[Endpoint]bool
	if f.entered != nil {
		entered = make(map[FrameID]map[Endpoint]bool, len(f.entered))
		for id, eps := range f.entered {
			entered[id] = maps.Clone(eps)
		}
	}

	var cableCrossings map[Endpoint]uint
	if f.cableCrossings != nil {
		cableCrossings = maps.Clone(f.cableCrossings)
	}

	var busyUntil map[Endpoint]time.Time
	if f.busyUntil != nil {
		busyUntil = maps.Clone(f.busyUntil)
	}

	var egress map[Endpoint]*egressQueue
	if f.egress != nil {
		egress = make(map[Endpoint]*egressQueue, len(f.egress))
		for ep, q := range f.egress {
			egress[ep] = cloneEgressQueue(q, journeys)
		}
	}

	var unstatedBacked map[Endpoint]trace.EvidenceRef
	if len(f.unstatedBacked) > 0 {
		unstatedBacked = maps.Clone(f.unstatedBacked)
	}

	var inflight map[FrameID]int
	if f.inflight != nil {
		inflight = maps.Clone(f.inflight)
	}

	var heldAggregates map[string][]FrameID
	if len(f.heldAggregates) > 0 {
		heldAggregates = make(map[string][]FrameID, len(f.heldAggregates))
		for device, ids := range f.heldAggregates {
			heldAggregates[device] = slices.Clone(ids)
		}
	}

	var flows map[FlowID]*FlowStats
	if f.flows != nil {
		flows = make(map[FlowID]*FlowStats, len(f.flows))
		for id, stats := range f.flows {
			cp := stats.Clone()
			flows[id] = &cp
		}
	}

	var counters map[Endpoint]*Counters
	if f.counters != nil {
		counters = make(map[Endpoint]*Counters, len(f.counters))
		for ep, c := range f.counters {
			if c != nil {
				cloned := c.Clone()
				counters[ep] = &cloned
			}
		}
	}

	return &Fabric{
		cfg:             f.cfg.Clone(),
		evidence:        f.evidence,
		runtimeEvidence: f.runtimeEvidence,
		links:           links,
		linkTrust:       cloneLinkTrustSlice(f.linkTrust),
		uncabled:        uncabled,
		switches:        switches,
		devices:         devices,
		byEnd:           byEnd,
		clock:           f.clock,
		stepped:         f.stepped,
		queue:           queue,
		attachments:     attachments,
		pendingHosts:    pendingHosts,
		wakes:           wakes,
		dequeueItems:    dequeueItems,
		wakeItems:       wakeItems,
		nextFrameID:     f.nextFrameID,
		nextSeq:         f.nextSeq,
		journeys:        journeys,
		entered:         entered,
		cableCrossings:  cableCrossings,
		busyUntil:       busyUntil,
		egress:          egress,
		counters:        counters,
		unstatedBacked:  unstatedBacked,
		metadataCache:   f.metadataCache,
		inflight:        inflight,
		heldAggregates:  heldAggregates,
		flows:           flows,
		touched:         nil,
		err:             f.err,
	}
}

func cloneLink(l Link) Link {
	cp := l
	cp.Cable = l.Cable.Clone()
	cp.A = l.A.clone()
	cp.B = l.B.clone()
	return cp
}

func cloneLinkTrustSlice(trust []linkTrust) []linkTrust {
	if len(trust) == 0 {
		return nil
	}
	cp := make([]linkTrust, len(trust))
	for i, t := range trust {
		cp[i] = linkTrust{
			issues: slices.Clone(t.issues),
		}
		if t.assumption != nil {
			a := *t.assumption
			cp[i].assumption = &a
		}
	}
	return cp
}

func cloneEgressQueue(q *egressQueue, journeys map[FrameID]*Journey) *egressQueue {
	if q == nil {
		return nil
	}
	cp := *q
	for i := 0; i < 8; i++ {
		if len(q.pending[i]) > 0 {
			pending := make([]queued, len(q.pending[i]))
			for j, item := range q.pending[i] {
				pending[j] = item
				if journeys != nil && journeys[item.fid] != nil {
					pending[j].journey = journeys[item.fid]
				} else if item.journey != nil {
					pending[j].journey = item.journey.shallowClone()
				}
			}
			cp.pending[i] = pending
		}
	}
	return &cp
}

func (f *Fabric) switchNames() []string {
	names := make([]string, 0, len(f.cfg.Switches))
	for name := range f.cfg.Switches {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

// startLayers reports every port, with LAG members before their parents, and
// schedules each switch's next timer at the fabric's clock.
func (f *Fabric) startLayers(names []string) error {
	for _, name := range names {
		sw := f.switches[name]
		ports := sw.Ports().Ports()
		slices.SortFunc(ports, func(a, b port.Port) int {
			if (a.LagParent != "") != (b.LagParent != "") {
				if a.LagParent != "" {
					return -1
				}
				return 1
			}
			return cmp.Compare(a.Name, b.Name)
		})
		for _, p := range ports {
			link := device.Link{State: p.OperStatus, Duplex: device.DuplexHalf}
			if ref, ok := f.byEnd[Endpoint{Node: name, Port: p.Name}]; ok {
				link = linkReport(f.cfg, *ref.end, ref.peer.Endpoint)
			}
			out, err := sw.LinkChange(f.clock, p.Name, link)
			if err != nil {
				return err
			}
			f.applyOutput(f.clock, name, out)
		}
		f.scheduleWake(name)
	}
	return nil
}

func (f *Fabric) linkEnd(node, portName string) (linkEndRef, bool) {
	ref, ok := f.byEnd[Endpoint{Node: node, Port: portName}]

	return ref, ok
}

// Links returns an independent deep copy of all resolved links in cable order. Each
// link carries the facts resolution used: a medium and Ethernet facts that
// [Config.PhyAssumption] filled appear here and nowhere in [Fabric.Config].
func (f *Fabric) Links() []Link {
	if len(f.links) == 0 {
		return nil
	}
	cp := make([]Link, len(f.links))
	for i, l := range f.links {
		cp[i] = Link{
			Cable: l.Cable.Clone(),
			A:     l.A.clone(),
			B:     l.B.clone(),
		}
	}

	return cp
}

// unlinked returns one link end per non-LAG port of the named switch that has
// no cable, in port name order: Down with reason no-cable for a port
// [Config.Uncabled] lists, Unknown with reason adjacency-unresolved for any
// other. A port without a cable has no Link, so this is where that reason is
// carried. It returns nil for a node that is not a switch or a switch whose
// ports are all cabled.
func (f *Fabric) unlinked(node string) []LinkEnd {
	swCfg, ok := f.cfg.Switches[node]
	if !ok {
		return nil
	}
	var unlinked []LinkEnd
	for _, p := range swCfg.Ports.Ports() {
		if p.Kind == port.LAG {
			continue
		}
		ep := Endpoint{Node: node, Port: p.Name}
		if _, ok := f.byEnd[ep]; !ok {
			unlinked = append(unlinked, unlinkedEnd(ep, f.uncabled))
		}
	}
	slices.SortFunc(unlinked, func(a, b LinkEnd) int { return cmp.Compare(a.Port, b.Port) })

	return unlinked
}

func unlinkedEnd(ep Endpoint, uncabled map[Endpoint]Uncabled) LinkEnd {
	if _, ok := uncabled[ep]; ok {
		return LinkEnd{Endpoint: ep, Oper: port.Down, Reason: ReasonNoCable}
	}

	return LinkEnd{Endpoint: ep, Oper: port.Unknown, Reason: ReasonAdjacencyUnresolved}
}

// Metadata returns the fabric's trust metadata over the whole analysis as the
// links stand now, so a later [Fabric.SetFault] changes what it returns. It holds:
//   - an issue on the link's [analysis.LinkScope] for every link whose facts leave
//     it Unknown, coded with the link's reason and Unsupported when the negotiation
//     case is unmodeled, and a propagation-unknown issue for an operational link
//     over an unspecified medium without a Delay;
//   - an adjacency-unresolved issue on the port scope of every unresolved switch
//     port, and oper-status-conflict or observed-speed-conflict issues on the port
//     scope where an observation disagrees with what executes;
//   - a queue-buffer-unstated issue on the endpoint scope of every endpoint whose
//     egress queue states no buffer and has backed up past one maximum-size frame;
//   - one assumption per link [Config.PhyAssumption] filled.
//
// A link's scope key is its cable's [Diff] subject key. Issues cite the evidence
// references of the cable, Uncabled entry, or runtime queue crossing they rest
// on; Metadata merges the runtime catalog with the construction specification's
// evidence catalog so all cited references resolve.
//
// The result is cached: its inputs are fixed after construction except where
// [Fabric.SetFault] rewrites a link, [Fabric.Configure] rebuilds a switch, or a
// queue with no stated buffer first backs up past one maximum-size frame. Each
// clears the cache.
func (f *Fabric) Metadata() analysis.Metadata {
	if f.metadataCache != nil {
		return *f.metadataCache
	}

	var issues []analysis.Issue
	var assumptions []analysis.Assumption
	for _, trust := range f.linkTrust {
		issues = append(issues, trust.issues...)
		if trust.assumption != nil {
			assumptions = append(assumptions, *trust.assumption)
		}
	}

	for _, name := range f.switchNames() {
		for _, p := range f.cfg.Switches[name].Ports.Ports() {
			if p.Kind == port.LAG {
				continue
			}
			ep := Endpoint{Node: name, Port: p.Name}
			derived, evidence := f.derivedEnd(ep)
			if derived.Reason == ReasonAdjacencyUnresolved {
				issues = append(issues, analysis.Issue{
					Code:    analysis.IssueCode(ReasonAdjacencyUnresolved),
					Status:  analysis.Incomplete,
					Scope:   analysis.PortScope(name, p.Name),
					Message: fmt.Sprintf("no cable names port %q on switch %q and no uncabled entry lists it", p.Name, name),
				})
			}
			if observedOperStatus(p.OperStatus) && observedOperStatus(derived.Oper) && p.OperStatus != derived.Oper {
				issues = append(issues, analysis.Issue{
					Code:     IssueOperStatusConflict,
					Status:   analysis.Incomplete,
					Scope:    analysis.PortScope(name, p.Name),
					Message:  fmt.Sprintf("port %q on switch %q is configured %s but its cable derives %s", p.Name, name, p.OperStatus, derived.Oper),
					Evidence: evidence,
				})
			}
		}
	}

	for _, ep := range slices.SortedFunc(maps.Keys(f.unstatedBacked), compareEndpoint) {
		issues = append(issues, queueBufferUnstatedIssue(ep, f.unstatedBacked[ep]))
	}

	catalog := f.evidence
	for _, entry := range f.runtimeEvidence.Entries() {
		catalog, _ = catalog.Add(entry.Evidence)
	}
	md := analysis.NewMetadata(analysis.WholeScope(), issues, catalog, assumptions)
	f.metadataCache = &md

	return md
}

// queueBufferUnstatedIssue is the issue an endpoint raises once an unstated-buffer
// egress queue first backs up past one maximum-size frame.
func queueBufferUnstatedIssue(ep Endpoint, ref trace.EvidenceRef) analysis.Issue {
	return analysis.Issue{
		Code:     IssueQueueBufferUnstated,
		Status:   analysis.Incomplete,
		Scope:    endpointScope(ep),
		Message:  fmt.Sprintf("egress queue on node %q port %q states no buffer and has backed up past one maximum-size frame", ep.Node, ep.Port),
		Evidence: []trace.EvidenceRef{ref},
	}
}

// mergeRaised folds issues raised about a journey into base. It bypasses the
// dependency scope [Fabric.record] applies to an entry, so an issue reaches a
// journey even when no later hop would carry it.
func (f *Fabric) mergeRaised(base analysis.Metadata, issues []analysis.Issue) analysis.Metadata {
	if len(issues) == 0 {
		return base
	}

	return base.Merge(analysis.NewMetadata(analysis.WholeScope(), issues, f.evidence, nil))
}

// markQueueBufferUnstated records the first threshold crossing per physical
// endpoint before enqueue, retaining its evidence for later metadata reads.
func (f *Fabric) markQueueBufferUnstated(now time.Time, ep Endpoint, egressPort string, pcp vlan.PCP, fid FrameID, step trace.Step, journey *Journey) {
	if _, marked := f.unstatedBacked[ep]; marked {
		return
	}
	if f.unstatedBacked == nil {
		f.unstatedBacked = make(map[Endpoint]trace.EvidenceRef)
	}
	target := egressPort
	if egressPort == "" {
		target = ep.Node
		step.Subject = trace.Subject{Kind: "host", Key: trace.CompositeKey(ep.Node, strconv.Itoa(int(pcp)))}
	}
	fact := step.Inputs[0]
	context := "rule=" + strconv.Quote(string(step.RuleID)) +
		";physical_node=" + strconv.Quote(ep.Node) +
		";physical_port=" + strconv.Quote(ep.Port) +
		";egress_port=" + strconv.Quote(target) +
		";pcp=" + strconv.FormatUint(uint64(pcp), 10) +
		";frame_id=" + strconv.FormatUint(uint64(fid), 10) +
		";at=" + strconv.Quote(now.UTC().Format(time.RFC3339Nano)) +
		";" + fact.Canonical()
	var ref trace.EvidenceRef
	f.runtimeEvidence, ref = f.runtimeEvidence.Add(analysis.Evidence{
		Kind: "fabric.runtime", Origin: "egress-queue", Context: context,
	})
	f.unstatedBacked[ep] = ref
	f.metadataCache = nil
	step.Evidence = []trace.EvidenceRef{ref}
	f.record(journey, Entry{
		At: now, Kind: EntryQueueThreshold, Device: ep.Node, Port: ep.Port, PCP: pcp,
		Step: &step,
	})
}

// observedOperStatus reports whether a state says something definite: an unset
// or Unknown status observes nothing, so it cannot conflict.
func observedOperStatus(state port.LinkState) bool {
	return state == port.Up || state == port.Down
}

func (f *Fabric) derivedEnd(ep Endpoint) (LinkEnd, []trace.EvidenceRef) {
	if ref, ok := f.byEnd[ep]; ok {
		return *ref.end, ref.link.Cable.Evidence
	}

	return unlinkedEnd(ep, f.uncabled), f.uncabled[ep].Evidence
}

// Switch returns the virtual switch with the given name, or nil if not found.
func (f *Fabric) Switch(name string) *vswitch.Switch {
	return f.switches[name]
}

// Config returns an independent deep copy of the fabric configuration. Port operational states are
// the configured ones; the derived states are in each switch's Ports and in [Fabric.Links].
func (f *Fabric) Config() Config {
	return f.cfg.Clone()
}

// Retention reports, for each switch, every layer including bridge and filter
// from the most recent Derive, Fork, or
// Configure that produced it; a Configure of one switch leaves the others' outcomes as they were.
func (f *Fabric) Retention() map[string]vswitch.Retention {
	ret := make(map[string]vswitch.Retention, len(f.switches))
	for name, sw := range f.switches {
		ret[name] = sw.Retention()
	}
	return ret
}

// linkTrust holds what a link's resolution could not decide from stated facts.
type linkTrust struct {
	issues     []analysis.Issue
	assumption *analysis.Assumption
}

func resolveLink(c Cable, cfg Config) (Link, linkTrust) {
	a := endpointPhyAndAdmin(c.A, cfg)
	b := endpointPhyAndAdmin(c.B, cfg)
	resolved := c.Resolve(a, b, cfg.PhyAssumption, cableEndpointsFor(c).Canonical(), c.Evidence)
	c.Cable = resolved.Cable
	return Link{Cable: c, A: LinkEnd{Endpoint: c.A, EndState: resolved.A}, B: LinkEnd{Endpoint: c.B, EndState: resolved.B}}, linkTrust{issues: resolved.Issues, assumption: resolved.Assumption}
}

func endpointLabel(ep Endpoint) string {
	if ep.Port == "" {
		return strconv.Quote(ep.Node)
	}

	return strconv.Quote(ep.Node) + ":" + strconv.Quote(ep.Port)
}

// cableScope is the link scope of a cable, keyed by its Diff subject key.
func cableScope(c Cable) analysis.Scope {
	return analysis.LinkScope(cableEndpointsFor(c).Canonical())
}

// endpointScope is a switch end's port scope, or a host's node scope, since a
// host has one unnamed port.
func endpointScope(ep Endpoint) analysis.Scope {
	if ep.Port == "" {
		return analysis.NodeScope(ep.Node)
	}

	return analysis.PortScope(ep.Node, ep.Port)
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
	sw, ok := f.switches[node]
	if !ok {
		return errs.New().
			Attr("node", node).
			Msgf("node %q is not a switch", node)
	}
	f.initRunState()
	defer f.settleTouched()
	f.applyOutput(f.clock, node, sw.Mcheck(f.clock, portName))
	f.scheduleWake(node)

	return nil
}

// SetFault modifies the declared fault on the cable connecting endpoints a and b at the
// current fabric clock, re-evaluating operational link states and notifying attached switches.
// It returns an error if no cable connects the specified endpoints or if the fault configuration is invalid.
func (f *Fabric) SetFault(a, b Endpoint, fault cablemodel.Fault) error {
	defer f.settleTouched()
	if err := cablemodel.ValidateFault(fault); err != nil {
		return err
	}
	normalized := normalizedFault(fault)

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

	// Links and configured cables share an index, and byEnd points into the
	// link, so the link is overwritten in place.
	f.cfg.Cables[idx].Fault = normalized
	return f.relinkAt(idx)
}

func (f *Fabric) relinkAt(idx int) error {
	f.links[idx], f.linkTrust[idx] = resolveLink(f.cfg.Cables[idx], f.cfg)
	f.metadataCache = nil
	linkA, linkB := f.links[idx].A, f.links[idx].B

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

		out, err := sw.LinkChange(f.clock, info.end.Port, linkReport(f.cfg, info.end, info.peer.Endpoint))
		if err != nil {
			return errs.Wrapf(err, "update operational status for %s", info.end.Port)
		}
		f.applyOutput(f.clock, info.end.Node, out)
		f.scheduleWake(info.end.Node)
	}

	return nil
}

func linkReport(cfg Config, end LinkEnd, peer Endpoint) device.Link {
	_, host := cfg.Hosts[peer.Node]
	_, reflector := cfg.Reflectors[peer.Node]
	peerSwitch, sw := cfg.Switches[peer.Node]
	return device.Link{
		State: end.Oper, SpeedBPS: end.Speed.SpeedBPS,
		Duplex:     device.Duplex(end.Speed.DuplexA),
		PeerRelays: host || reflector || (sw && peerSwitch.Bridge != nil),
	}
}

func (f *Fabric) applyOutput(now time.Time, node string, out device.Output) {
	for _, em := range out.Emissions {
		f.injectEmission(now, node, em)
	}
	for _, drop := range out.Drops {
		f.recordNeighborFailure(now, node, drop)
	}
}
