// Package fabric composes virtual switches, hosts, and cables into a Layer 2 network fabric.
package fabric

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// ConstructionSpec captures the topology and each switch's complete construction
// specification needed to construct an identical [Fabric]. Every switch specification's
// NodeID must equal its map key. Evidence is the catalog that cable and Uncabled
// evidence references resolve in; a reference it lacks makes the specification invalid.
type ConstructionSpec struct {
	Start         time.Time
	Switches      map[string]vswitch.ConstructionSpec
	Hosts         map[string]Host
	Reflectors    map[string]Reflector
	Cables        []Cable
	Uncabled      []Uncabled
	PhyAssumption *PhyAssumption
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
		cp.Hosts = make(map[string]Host, len(s.Hosts))
		for name, host := range s.Hosts {
			cp.Hosts[name] = host.Clone()
		}
	}
	if s.Reflectors != nil {
		cp.Reflectors = make(map[string]Reflector, len(s.Reflectors))
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
		cfg.Hosts = make(map[string]Host, len(s.Hosts))
		for name, host := range s.Hosts {
			cfg.Hosts[name] = host.Clone()
		}
	}
	if s.Reflectors != nil {
		cfg.Reflectors = make(map[string]Reflector, len(s.Reflectors))
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
	cfg            Config
	evidence       analysis.EvidenceCatalog
	links          []Link
	linkTrust      []linkTrust
	uncabled       map[Endpoint]Uncabled
	switches       map[string]*vswitch.Switch
	hostStacks     map[string]*routing.Layer
	byEnd          map[Endpoint]linkEndRef
	clock          time.Time
	stepped        bool
	queue          []Arrival
	wakes          map[string]time.Time
	dequeueItems   map[Endpoint]int
	wakeItems      map[string]int
	nextFrameID    FrameID
	nextSeq        uint64
	journeys       map[FrameID]*Journey
	entered        map[FrameID]map[Endpoint]bool
	cableCrossings map[Endpoint]uint
	busyUntil      map[Endpoint]time.Time
	egress         map[Endpoint]*egressQueue
	counters       map[Endpoint]*Counters

	// unstatedBacked names the endpoints whose unstated-buffer egress queue
	// has backed up past one maximum-size frame, so [Fabric.Metadata] carries
	// the queue-buffer-unstated issue for them.
	unstatedBacked map[Endpoint]struct{}

	// metadataCache holds the value [Fabric.Metadata] last built. Its inputs
	// are fixed after construction except where [Fabric.SetFault] rewrites a
	// link and its trust, or a queue with no stated buffer first backs up past
	// one maximum-size frame; both nil the cache. The value is immutable, so a
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
	fab.startLayers(fab.switchNames())

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
		Hosts:         make(map[string]Host, len(f.cfg.Hosts)),
		Reflectors:    make(map[string]Reflector, len(f.cfg.Reflectors)),
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
	for name, refl := range f.cfg.Reflectors {
		spec.Reflectors[name] = refl.Clone()
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
		if err := validateFault(cable.Fault); err != nil {
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
		if err := cfg.Hosts[name].Accept.validate("hosts." + name + ".accept"); err != nil {
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
	for name, swCfg := range cloned.Switches {
		b := port.NewBuilder()
		ports := swCfg.Ports.Ports()

		for _, p := range ports {
			switch {
			case p.Kind != port.Lag:
				ep := Endpoint{Node: name, Port: p.Name}
				if ref, ok := byEnd[ep]; ok {
					p.OperStatus = ref.end.Oper
				} else {
					p.OperStatus = unlinkedEnd(ep, uncabled).Oper
				}
			case len(swCfg.Ports.Members(p.Name)) == 0:
				// A LAG with no member hears no link and would keep whatever
				// the configuration said.
				p.OperStatus = port.Down
			}
			b.Add(p)
		}

		newTable, err := b.Build()
		if err != nil {
			return nil, errs.Wrapf(err, "rebuild ports for switch %q", name)
		}
		swCfg.Ports = newTable

		swSpec := norm.Switches[name].Clone()
		swSpec.Config = swCfg
		sw, err := vswitch.NewWithSpec(swSpec)
		if err != nil {
			return nil, errs.Wrapf(err, "build switch %q", name)
		}
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
				p2p := portPointToPoint(cloned, name, p.Name, *ref.end, ref.peer.Endpoint)
				speed := ref.end.Speed.SpeedBPS
				sw.LinkChange(cloned.Start, p.Name, ref.end.Oper, p2p, speed)
			}
		}
	}

	hostStacks := make(map[string]*routing.Layer, len(cloned.Hosts))
	for name, h := range cloned.Hosts {
		if h.IP != nil {
			rtCfg, tbl := HostRoutingConfig(name, h)
			layer, err := routing.New(rtCfg, tbl, name)
			if err != nil {
				return nil, errs.Wrapf(err, "host %q routing", name)
			}
			hostStacks[name] = layer
		}
	}

	fab := &Fabric{
		cfg:            cloned,
		evidence:       norm.Evidence,
		links:          links,
		linkTrust:      trust,
		uncabled:       uncabled,
		switches:       switches,
		hostStacks:     hostStacks,
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

	return fab, nil
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
			switches[name] = sw.Fork()
		}
	}

	var hostStacks map[string]*routing.Layer
	if f.hostStacks != nil {
		hostStacks = make(map[string]*routing.Layer, len(f.hostStacks))
		for name, l := range f.hostStacks {
			hostStacks[name] = l.Clone()
		}
	}

	var queue []Arrival
	if len(f.queue) > 0 {
		queue = slices.Clone(f.queue)
	}

	var wakes map[string]time.Time
	if f.wakes != nil {
		wakes = maps.Clone(f.wakes)
	}

	var dequeueItems map[Endpoint]int
	if f.dequeueItems != nil {
		dequeueItems = maps.Clone(f.dequeueItems)
	}

	var wakeItems map[string]int
	if f.wakeItems != nil {
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

	var unstatedBacked map[Endpoint]struct{}
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
		cfg:            f.cfg.Clone(),
		evidence:       f.evidence,
		links:          links,
		linkTrust:      cloneLinkTrustSlice(f.linkTrust),
		uncabled:       uncabled,
		switches:       switches,
		hostStacks:     hostStacks,
		byEnd:          byEnd,
		clock:          f.clock,
		stepped:        f.stepped,
		queue:          queue,
		wakes:          wakes,
		dequeueItems:   dequeueItems,
		wakeItems:      wakeItems,
		nextFrameID:    f.nextFrameID,
		nextSeq:        f.nextSeq,
		journeys:       journeys,
		entered:        entered,
		cableCrossings: cableCrossings,
		busyUntil:      busyUntil,
		egress:         egress,
		counters:       counters,
		unstatedBacked: unstatedBacked,
		metadataCache:  f.metadataCache,
		inflight:       inflight,
		heldAggregates: heldAggregates,
		flows:          flows,
		touched:        nil,
		err:            f.err,
	}
}

func cloneLink(l Link) Link {
	cp := l
	cp.Cable = l.Clone()
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
		if swCfg.STP == nil && swCfg.LoopProtect == nil && !hasLag {
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
				p2p := f.portPointToPoint(name, p.Name, *ref.end, ref.peer.Endpoint)
				speed := ref.end.Speed.SpeedBPS
				sw.LinkChange(f.clock, p.Name, ref.end.Oper, p2p, speed)
			} else {
				p2p := f.uncabledPointToPoint(name, p.Name)
				oper := port.Down
				if p.OperStatus == port.Unknown {
					oper = port.Unknown
				}
				sw.LinkChange(f.clock, p.Name, oper, p2p, 0)
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
			Cable: l.Clone(),
			A:     l.A.clone(),
			B:     l.B.clone(),
		}
	}

	return cp
}

// Unlinked returns one link end per non-LAG port of the named switch that has
// no cable, in port name order: Down with reason no-cable for a port
// [Config.Uncabled] lists, Unknown with reason adjacency-unresolved for any
// other. A port without a cable has no Link, so this is where that reason is
// carried. It returns nil for a node that is not a switch or a switch whose
// ports are all cabled.
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
//   - one assumption per link [Config.PhyAssumption] filled.
//
// A link's scope key is its cable's [Diff] subject key. Issues cite the evidence
// references of the cable or Uncabled entry they rest on, resolved in the
// construction specification's evidence catalog.
//
// The result is cached: its inputs are fixed after construction except where
// [Fabric.SetFault] rewrites a link, or a queue with no stated buffer first
// backs up past one maximum-size frame. Both clear the cache.
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
			if p.Kind == port.Lag {
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
		issues = append(issues, queueBufferUnstatedIssue(ep))
	}

	md := analysis.NewMetadata(analysis.WholeScope(), issues, f.evidence, assumptions)
	f.metadataCache = &md

	return md
}

// queueBufferUnstatedIssue is the issue an endpoint raises once an unstated-buffer
// egress queue first backs up past one maximum-size frame.
func queueBufferUnstatedIssue(ep Endpoint) analysis.Issue {
	return analysis.Issue{
		Code:    IssueQueueBufferUnstated,
		Status:  analysis.Incomplete,
		Scope:   endpointScope(ep),
		Message: fmt.Sprintf("egress queue on node %q port %q states no buffer and has backed up past one maximum-size frame", ep.Node, ep.Port),
	}
}

// mergeRaised folds issues raised about a journey into base. It bypasses the
// dependency scope [Fabric.record] applies to an entry, so an issue reaches a
// journey even when no later hop would carry it.
func (f *Fabric) mergeRaised(base analysis.Metadata, issues []analysis.Issue) analysis.Metadata {
	if len(issues) == 0 {
		return base
	}

	return mergeMetadata(base, analysis.NewMetadata(analysis.WholeScope(), issues, f.evidence, nil))
}

// markQueueBufferUnstated records the first time an endpoint's unstated-buffer
// egress queue backs up past threshold octets: it marks the endpoint so
// [Fabric.Metadata] reports the issue, folds the same issue into the crossing
// frame's journey, and clears the metadata cache. depth is the queue depth with
// the frame just enqueued counted. A queue that crosses on two PCPs marks its
// endpoint once.
func (f *Fabric) markQueueBufferUnstated(ep Endpoint, depth, threshold uint64, journey *Journey) {
	if depth <= threshold {
		return
	}
	if _, marked := f.unstatedBacked[ep]; marked {
		return
	}
	if f.unstatedBacked == nil {
		f.unstatedBacked = make(map[Endpoint]struct{})
	}
	f.unstatedBacked[ep] = struct{}{}

	if journey != nil {
		journey.Metadata = f.mergeRaised(journey.Metadata, []analysis.Issue{queueBufferUnstatedIssue(ep)})
	}
	f.metadataCache = nil
}

// observedOperStatus reports whether a state says something definite: an unset
// or Unknown status observes nothing, so it cannot conflict.
func observedOperStatus(state port.LinkState) bool {
	return state == port.Up || state == port.Down
}

func (f *Fabric) derivedEnd(ep Endpoint) (LinkEnd, []trace.EvidenceRef) {
	if ref, ok := f.byEnd[ep]; ok {
		return *ref.end, ref.link.Evidence
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

// Retention reports the per-switch layer retention outcome of the most recent Derive or Fork.
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

// resolveLink derives a cable's link in the order fault, administrative state,
// reach, negotiation, and the observed rule, after filling unreported facts from
// the configuration's physical assumption.
func resolveLink(cable Cable, cfg Config) (Link, linkTrust) {
	ethA, adminA := endpointPhyAndAdmin(cable.A, cfg)
	ethB, adminB := endpointPhyAndAdmin(cable.B, cfg)

	key := cableEndpointsFor(cable).Canonical()
	scope := analysis.LinkScope(key)
	var trust linkTrust
	if assumed := cfg.PhyAssumption; assumed != nil {
		var filled []string
		if cable.Medium == MediumUnspecified && assumed.Medium != MediumUnspecified {
			cable.Medium = assumed.Medium
			filled = append(filled, "medium")
		}
		ethA = assumed.fill(ethA, endpointLabel(cable.A), &filled)
		ethB = assumed.fill(ethB, endpointLabel(cable.B), &filled)
		if len(filled) > 0 {
			trust.assumption = &analysis.Assumption{
				Scope:     scope,
				Statement: "assumed physical facts: " + strings.Join(filled, ", "),
			}
		}
	}

	link := Link{
		Cable: cable,
		A:     LinkEnd{Endpoint: cable.A, Ethernet: ethA},
		B:     LinkEnd{Endpoint: cable.B, Ethernet: ethB},
	}
	unknown := func(reason trace.Reason, status analysis.Status, speed phy.Link) (Link, linkTrust) {
		link.setBoth(port.Unknown, reason, speed)
		trust.issues = append(trust.issues, analysis.Issue{
			Code:     analysis.IssueCode(reason),
			Status:   status,
			Scope:    scope,
			Message:  fmt.Sprintf("link %q is unknown: %s", key, reason),
			Evidence: cable.Evidence,
		})

		return link, trust
	}
	down := func(reason trace.Reason, speed phy.Link) (Link, linkTrust) {
		link.setBoth(port.Down, reason, speed)

		return link, trust
	}

	if cable.Fault.Kind == FaultCut {
		return down(ReasonCut, phy.Link{})
	}

	// Two ends that both disable auto-negotiation still pass frames the dead
	// direction does not carry; an end whose mode is unreported might be either.
	if cable.Fault.Kind == FaultDeadAToB || cable.Fault.Kind == FaultDeadBToA {
		switch {
		case isAuto(ethA) || isAuto(ethB):
			return down(ReasonDeadDirection, phy.Link{})
		case !isForced(ethA) || !isForced(ethB):
			return unknown(phy.ReasonCapabilityUnknown, analysis.Incomplete, phy.Link{})
		}
	}

	if adminA == port.Down || adminB == port.Down {
		link.setBoth(port.Down, ReasonPeerDown, phy.Link{})
		if adminA == port.Down {
			link.A.Reason = ReasonAdminDown
		}
		if adminB == port.Down {
			link.B.Reason = ReasonAdminDown
		}

		return link, trust
	}

	if adminA == port.Unknown || adminB == port.Unknown {
		return unknown(trace.Reason("unknown-operational-status"), analysis.Incomplete, phy.Link{})
	}

	for _, eth := range []phy.Ethernet{ethA, ethB} {
		if isForced(eth) && eth.Setting.SpeedBPS > 0 && cable.reach(eth.Setting.SpeedBPS) == ReachExceeded {
			return down(ReasonReachExceeded, phy.Link{})
		}
	}

	candidates := negotiableSpeeds(ethA, ethB, cable.TopSpeedBPS)
	remaining := slices.DeleteFunc(slices.Clone(candidates), func(speed uint64) bool {
		return cable.reach(speed) == ReachExceeded
	})
	if len(candidates) > 0 && len(remaining) == 0 {
		return down(ReasonReachExceeded, phy.Link{})
	}
	top := cable.TopSpeedBPS
	if len(remaining) < len(candidates) {
		top = slices.Max(remaining)
	}

	negotiated := phy.Negotiate(ethA, ethB, top)
	if negotiated.State == phy.LinkResolved && negotiated.Source != phy.SourceObserved &&
		slices.ContainsFunc(remaining, func(speed uint64) bool {
			return speed >= negotiated.SpeedBPS && cable.reach(speed) == ReachUnknown
		}) {
		negotiated = observedLink(ethA, ethB)
		if negotiated.State != phy.LinkResolved {
			return unknown(ReasonReachUnknown, analysis.Incomplete, phy.Link{State: phy.LinkUnknown, Reason: ReasonReachUnknown})
		}
	}

	switch negotiated.State {
	case phy.LinkResolved:
	case phy.LinkFailed:
		return down(negotiated.Reason, negotiated)
	case phy.LinkUnsupported:
		return unknown(negotiated.Reason, analysis.Unsupported, negotiated)
	default:
		return unknown(negotiated.Reason, analysis.Incomplete, negotiated)
	}

	link.setBoth(port.Up, "", negotiated)
	if cable.Medium == MediumUnspecified && cable.Delay == nil && cable.LengthMeters > 0 {
		trust.issues = append(trust.issues, analysis.Issue{
			Code:     IssuePropagationUnknown,
			Status:   analysis.Incomplete,
			Scope:    scope,
			Message:  fmt.Sprintf("link %q has an unspecified medium and no delay", key),
			Evidence: cable.Evidence,
		})
	}
	if negotiated.Source == phy.SourceObserved {
		return link, trust
	}
	for _, end := range []LinkEnd{link.A, link.B} {
		observed := end.Ethernet.Observed
		if observed == nil || observed.SpeedBPS == 0 || observed.SpeedBPS == negotiated.SpeedBPS {
			continue
		}
		trust.issues = append(trust.issues, analysis.Issue{
			Code:   IssueObservedSpeedConflict,
			Status: analysis.Incomplete,
			Scope:  endpointScope(end.Endpoint),
			Message: fmt.Sprintf("%s observed %d b/s but its link negotiated %d b/s",
				endpointLabel(end.Endpoint), observed.SpeedBPS, negotiated.SpeedBPS),
			Evidence: cable.Evidence,
		})
	}

	return link, trust
}

// setBoth gives both ends a state and reason, with speed as end A sees it.
func (l *Link) setBoth(oper port.LinkState, reason trace.Reason, speed phy.Link) {
	l.A.Oper, l.A.Reason, l.A.Speed = oper, reason, speed
	l.B.Oper, l.B.Reason, l.B.Speed = oper, reason, speed
	l.B.Speed.DuplexA, l.B.Speed.DuplexB = speed.DuplexB, speed.DuplexA
}

// negotiableSpeeds returns the speeds negotiation could select at or below top
// (0 is unlimited): the forced speeds when an end forces one, otherwise the
// speeds both ends report.
func negotiableSpeeds(a, b phy.Ethernet, top uint64) []uint64 {
	var speeds []uint64
	for _, eth := range []phy.Ethernet{a, b} {
		if isForced(eth) && eth.Setting.SpeedBPS > 0 {
			speeds = append(speeds, eth.Setting.SpeedBPS)
		}
	}
	if len(speeds) == 0 {
		for _, speed := range a.SupportedSpeedsBPS {
			if slices.Contains(b.SupportedSpeedsBPS, speed) {
				speeds = append(speeds, speed)
			}
		}
	}

	return slices.DeleteFunc(speeds, func(speed uint64) bool {
		return top != 0 && speed > top
	})
}

// observedLink applies phy's observed rule on its own. phy owns the rule and
// reaches it only through ends whose mode is unreported, so the settings are
// dropped here.
func observedLink(a, b phy.Ethernet) phy.Link {
	a.Setting, b.Setting = nil, nil

	return phy.Negotiate(a, b, 0)
}

func (c Cable) reach(speedBPS uint64) ReachState {
	state, _ := c.Medium.Reach(c.LengthMeters, speedBPS)

	return state
}

func isForced(e phy.Ethernet) bool {
	return e.Setting != nil && !e.Setting.AutoNegotiation
}

func isAuto(e phy.Ethernet) bool {
	return e.Setting != nil && e.Setting.AutoNegotiation
}

// fill returns eth with the facts its source did not report taken from the
// assumption, appending the name of each filled fact under label.
func (a *PhyAssumption) fill(eth phy.Ethernet, label string, filled *[]string) phy.Ethernet {
	eth = eth.Clone()
	if len(eth.SupportedSpeedsBPS) == 0 && len(a.Ethernet.SupportedSpeedsBPS) > 0 {
		eth.SupportedSpeedsBPS = slices.Clone(a.Ethernet.SupportedSpeedsBPS)
		*filled = append(*filled, label+" supported_speeds_bps")
	}
	if eth.AutoNegotiationSupported == phy.CapabilityUnknown && a.Ethernet.AutoNegotiationSupported != phy.CapabilityUnknown {
		eth.AutoNegotiationSupported = a.Ethernet.AutoNegotiationSupported
		*filled = append(*filled, label+" auto_negotiation_supported")
	}
	if eth.Setting == nil && a.Ethernet.Setting != nil {
		eth.Setting = a.Ethernet.Setting.Clone()
		*filled = append(*filled, label+" setting")
	}

	return eth
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

func endpointPhyAndAdmin(ep Endpoint, cfg Config) (phy.Ethernet, port.LinkState) {
	if host, isHost := cfg.Hosts[ep.Node]; isHost {
		return host.Ethernet, port.Up
	}
	if refl, isReflector := cfg.Reflectors[ep.Node]; isReflector {
		return refl.Ports[ep.Port], port.Up
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
	defer f.settleTouched()
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
	defer f.settleTouched()
	if err := validateFault(fault); err != nil {
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

		if err := sw.SetOperStatus(info.end.Port, info.end.Oper); err != nil {
			return errs.Wrapf(err, "update operational status for %s", info.end.Port)
		}
		p2p := f.portPointToPoint(info.end.Node, info.end.Port, info.end, info.peer.Endpoint)
		speed := info.end.Speed.SpeedBPS
		sw.LinkChange(f.clock, info.end.Port, info.end.Oper, p2p, speed)

		for _, em := range sw.Drain() {
			f.injectEmission(f.clock, info.end.Node, em)
		}
		f.scheduleWake(info.end.Node)
	}

	return nil
}

func (f *Fabric) portPointToPoint(node, portName string, end LinkEnd, peer Endpoint) vswitch.PointToPoint {
	return portPointToPoint(f.cfg, node, portName, end, peer)
}

func portPointToPoint(cfg Config, node, portName string, end LinkEnd, peer Endpoint) vswitch.PointToPoint {
	swCfg := cfg.Switches[node]
	if swCfg.STP != nil {
		if pCfg, ok := swCfg.STP.Ports[portName]; ok {
			if pCfg.PointToPoint == stp.PointToPointForceTrue {
				return vswitch.PointToPointTrue
			}
			if pCfg.PointToPoint == stp.PointToPointForceFalse {
				return vswitch.PointToPointFalse
			}
		}
	}

	if end.Speed.DuplexA == "" || end.Speed.DuplexA == phy.Unknown {
		return vswitch.PointToPointUnknown
	}
	if end.Speed.DuplexA != phy.Full {
		return vswitch.PointToPointFalse
	}
	if _, isHost := cfg.Hosts[peer.Node]; isHost {
		return vswitch.PointToPointTrue
	}
	if _, isReflector := cfg.Reflectors[peer.Node]; isReflector {
		return vswitch.PointToPointTrue
	}
	if peerSw, isSw := cfg.Switches[peer.Node]; isSw {
		if peerSw.Bridge != nil {
			return vswitch.PointToPointTrue
		}
	}

	return vswitch.PointToPointFalse
}

func (f *Fabric) uncabledPointToPoint(node, portName string) vswitch.PointToPoint {
	swCfg := f.cfg.Switches[node]
	if swCfg.STP != nil {
		if pCfg, ok := swCfg.STP.Ports[portName]; ok {
			if pCfg.PointToPoint == stp.PointToPointForceTrue {
				return vswitch.PointToPointTrue
			}
			if pCfg.PointToPoint == stp.PointToPointForceFalse {
				return vswitch.PointToPointFalse
			}
		}
	}

	return vswitch.PointToPointFalse
}
