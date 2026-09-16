package vswitch

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

const (
	protocolHopByHop  = 0
	protocolIGMP      = 2
	protocolICMPv6    = 58
	optionRouterAlert = 5
)

var (
	stpGroupAddress = netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}

	// sstpGroupAddress is where a PVST bridge sends its per-VLAN BPDUs. It is
	// taken from the stp package rather than re-declared the way
	// stpGroupAddress is, since the codec that builds those frames is what
	// owns the address.
	sstpGroupAddress = stp.GroupAddressSSTP

	allNodesAddress = netip.MustParseAddr("ff02::1")
)

// ConstructionSpec holds the normalized configuration, preloaded FDB seeds,
// stable node identity, and construction trust metadata needed to reproduce a
// [Switch]. NodeID keys node and port scopes; an empty value identifies an
// anonymous standalone switch. ConstructionSpec is safe for concurrent reads
// when its exported fields are not mutated.
type ConstructionSpec struct {
	Config   Config
	Seeds    []bridge.Seed
	NodeID   string
	Metadata analysis.Metadata
}

// Normalize validates and returns an independent construction specification
// with normalized configuration and forwarding database seeds. Seed timestamps
// are canonical UTC wall-clock instants without monotonic readings.
func (s ConstructionSpec) Normalize() (ConstructionSpec, error) {
	owned := s.Clone()
	// Traffic field paths name submitted slice positions, which sorting and
	// compaction would otherwise replace with normalized positions.
	if owned.Config.Traffic != nil {
		if err := owned.Config.Traffic.Validate(owned.Config.Ports.Normalize()); err != nil {
			return ConstructionSpec{}, err
		}
		if err := owned.Config.validateTrafficVLANs(); err != nil {
			return ConstructionSpec{}, err
		}
	}
	owned.Config = owned.Config.Normalize()
	if err := owned.Config.Validate(); err != nil {
		return ConstructionSpec{}, err
	}
	if len(owned.Seeds) > 0 && owned.Config.Bridge == nil {
		return ConstructionSpec{}, errs.New().
			Attr("field", "seeds").
			Msg("forwarding database seeds require bridge configuration")
	}
	if owned.Config.Bridge != nil {
		seeds, err := bridge.NormalizeSeeds(*owned.Config.Bridge, owned.Config.Ports, owned.Seeds)
		if err != nil {
			return ConstructionSpec{}, err
		}
		for i := range seeds {
			seeds[i].LearnedAt = seeds[i].LearnedAt.Round(0).UTC()
		}
		owned.Seeds = seeds
	}
	if err := validateConstructionMetadata(owned.NodeID, owned.Metadata); err != nil {
		return ConstructionSpec{}, err
	}

	return owned, nil
}

// Clone returns an independent deep copy of the construction specification.
func (s ConstructionSpec) Clone() ConstructionSpec {
	cp := ConstructionSpec{
		Config:   s.Config.Clone(),
		NodeID:   s.NodeID,
		Metadata: cloneMetadata(s.Metadata),
	}
	if len(s.Seeds) > 0 {
		cp.Seeds = slices.Clone(s.Seeds)
	}
	return cp
}

// Equal reports whether two construction specifications carry the same fields,
// treating seed timestamps at the same instant as equal.
func (s ConstructionSpec) Equal(other ConstructionSpec) bool {
	if s.NodeID != other.NodeID || !s.Config.Equal(other.Config) || !metadataEqual(s.Metadata, other.Metadata) {
		return false
	}
	if len(s.Seeds) != len(other.Seeds) {
		return false
	}
	for i := range s.Seeds {
		a, b := s.Seeds[i], other.Seeds[i]
		if a.FID != b.FID || a.MAC != b.MAC || a.Port != b.Port || a.Static != b.Static || !a.LearnedAt.Equal(b.LearnedAt) {
			return false
		}
	}
	return true
}

// ForwardResult combines the Ethernet bridge forwarding outcome with analysis
// trust metadata recording status, issues, evidence, and assumptions.
type ForwardResult struct {
	bridge.Result
	Metadata analysis.Metadata
}

// PowerResult combines the Power over Ethernet allocation outcome with analysis
// trust metadata.
type PowerResult struct {
	phy.Allocation
	Metadata analysis.Metadata
}

// Emission describes an Ethernet frame to transmit out a port or member port.
type Emission struct {
	Port  string
	Frame ethernet.Frame
}

// Switch simulates a network device composed of a port table and optional
// physical-layer, bridge, link aggregation, spanning tree, multicast snooping,
// layer 3 routing, and traffic subsystems.
// [Switch.Copies] returns and clears the mirror copies produced by the most recent
// [Switch.Forward]; [Switch.Peek] does not produce or change pending copies.
//
// A Switch is not safe for concurrent use.
type Switch struct {
	cfg            Config
	ports          port.Table
	bridge         *bridge.Bridge
	speeds         map[string]phy.Resolved
	power          phy.Allocation
	stp            *stp.Layer
	loopprotect    *loopprotect.Layer
	lag            *lag.Layer
	mcast          *mcast.Layer
	routing        *routing.Layer
	traffic        *traffic.Config
	buckets        map[string]*traffic.Bucket
	copies         []traffic.Copy
	emissions      []Emission
	portP2P        map[string]PointToPoint
	portSpeed      map[string]uint64
	protocolIssues map[string]analysis.Issue
	seeds          []bridge.Seed
	nodeID         string
	metadata       analysis.Metadata
	missingSTP     bool

	// operErr is the first oper-status fault [Switch.setOperStatus]
	// recorded. It is sticky: [Switch.Err] reports it, and it is set from
	// the forward-path callers ([Switch.LinkChange], [Switch.updateLagState])
	// that cannot return an error of their own.
	operErr error

	// lagRebalanceHits and mcastQueryUnobservedHits name, for the forward or
	// peek call in progress, the LAGs and multicast groups whose resolution
	// reported an unmodeled condition; wrapResult turns each into an
	// Incomplete issue. Both reset at the start of every forward call.
	lagRebalanceHits         map[string]lag.Selection
	mcastQueryUnobservedHits []mcastQueryUnobservedHit

	// pvstBoundaryHits names the (port, VLAN) pairs a journey crossed where
	// the spanning tree layer reports a neighbor whose per-VLAN trees it
	// cannot simulate. It resets with the other hit sets.
	pvstBoundaryHits map[pvstBoundaryHit]struct{}
}

// pvstBoundaryHit names one port and VLAN a journey crossed while the port
// faced a spanning tree neighbor this switch does not simulate per VLAN.
type pvstBoundaryHit struct {
	port string
	vid  vlan.ID
}

// mcastQueryUnobservedHit names one (VLAN, group) pair a forward call
// resolved while a "Send Q" obligation had gone unobserved past its last
// member query time.
type mcastQueryUnobservedHit struct {
	vid   vlan.ID
	group netip.Addr
}

// New constructs a [Switch] from the provided configuration, cloning the configuration,
// assigning missing MAC addresses, validating subsystem configurations, and initializing
// each present subsystem. When the base MAC is zero, New selects the first unused local MAC
// address; two standalone switches may pick the same address. A fabric assigns unique
// addresses across nodes. It returns an error if configuration validation fails.
func New(cfg Config) (*Switch, error) {
	return NewWithSpec(ConstructionSpec{Config: cfg})
}

// NewWithSpec constructs a [Switch] from an immutable deep clone of spec,
// restoring preloaded forwarding database seeds and construction trust metadata.
func NewWithSpec(spec ConstructionSpec) (*Switch, error) {
	norm, err := spec.Normalize()
	if err != nil {
		return nil, err
	}
	return newSwitch(norm.Config, norm.Seeds, norm.NodeID, norm.Metadata)
}

func newSwitch(norm Config, seeds []bridge.Seed, nodeID string, metadata analysis.Metadata) (*Switch, error) {
	sw := &Switch{
		cfg:      norm,
		ports:    norm.Ports.Clone(),
		seeds:    slices.Clone(seeds),
		nodeID:   nodeID,
		metadata: cloneMetadata(metadata),
	}

	if norm.Phy != nil {
		sw.speeds = norm.Phy.Resolve()
		sw.power = norm.Phy.Allocate()
	}

	if norm.Bridge != nil {
		b, err := bridge.New(*norm.Bridge, norm.Ports)
		if err != nil {
			return nil, err
		}
		b.SetFDBScope(protocolScope(nodeID, port.LayerRelay))
		stpScope := protocolScope(nodeID, port.LayerStp)
		if norm.STP == nil && metadataHasScopedContent(metadata, stpScope) {
			b.SetGate(nil, stpScope)
			sw.missingSTP = true
		}
		sw.bridge = b
	}
	if norm.Mcast != nil {
		m, err := mcast.New(*norm.Mcast, norm.Ports)
		if err != nil {
			return nil, err
		}
		sw.mcast = m
		if sw.bridge != nil {
			sw.bridge.SetGroupResolver(sw, protocolScope(nodeID, port.LayerMcast))
		}
	}

	var hasLag bool
	for _, p := range norm.Ports.Ports() {
		if p.Kind == port.Lag {
			hasLag = true
			break
		}
	}
	if hasLag {
		lagCfg := lag.Config{}
		if norm.LAG != nil {
			lagCfg = *norm.LAG
		}
		l, err := lag.New(lagCfg, norm.Ports, norm.MAC)
		if err != nil {
			return nil, err
		}
		sw.lag = l
		if sw.bridge != nil {
			sw.bridge.SetSelector(lagSelector{sw: sw}, protocolScope(nodeID, port.LayerLag))
		}
		for _, p := range norm.Ports.Ports() {
			if p.LagParent != "" && p.Forwards() {
				lCfg := lagCfg.LAGs[p.LagParent]
				if lCfg.LACP.Mode == lag.Off && lCfg.UpDelay == 0 {
					sw.lag.LinkChange(time.Time{}, p.Name, true)
				}
			}
		}
	}

	if norm.STP != nil {
		st, err := stp.New(*norm.STP, norm.Ports)
		if err != nil {
			return nil, err
		}
		sw.stp = st
		if sw.bridge != nil {
			sw.bridge.SetGate(sw.stp, protocolScope(nodeID, port.LayerStp))
		}
	}

	if norm.LoopProtect != nil {
		lp, err := loopprotect.New(*norm.LoopProtect, norm.Ports, norm.MAC)
		if err != nil {
			return nil, err
		}
		sw.loopprotect = lp
		if sw.bridge != nil {
			sw.bridge.SetGate(sw.loopprotect, protocolScope(nodeID, port.LayerLoopProtect))
		}
	}

	if norm.Routing != nil {
		rt, err := routing.New(*norm.Routing, norm.Ports, nodeID)
		if err != nil {
			return nil, err
		}
		sw.routing = rt
	}
	if norm.Traffic != nil {
		sw.traffic = norm.Traffic
		sw.buckets = make(map[string]*traffic.Bucket, len(norm.Traffic.Policers))
		for name, policer := range norm.Traffic.Policers {
			bucket, err := traffic.NewBucket(policer)
			if err != nil {
				return nil, fmt.Errorf("create policer bucket for %q: %w", name, err)
			}
			sw.buckets[name] = bucket
		}
	}

	if len(seeds) > 0 && sw.bridge != nil {
		if err := sw.bridge.Learn(seeds); err != nil {
			return nil, err
		}
	}

	sw.recomputeProtocolLinkIssues()

	return sw, nil
}

// Spec returns an independent [ConstructionSpec] capturing every construction
// input needed to reproduce this switch, including its node identity and trust
// metadata.
func (s *Switch) Spec() ConstructionSpec {
	spec := ConstructionSpec{
		Config:   s.cfg.Clone(),
		NodeID:   s.nodeID,
		Metadata: cloneMetadata(s.metadata),
	}
	if len(s.seeds) > 0 {
		spec.Seeds = slices.Clone(s.seeds)
	}
	return spec
}

// Config returns an independent deep copy of the switch configuration.
func (s *Switch) Config() Config {
	return s.cfg.Clone()
}

// Copies returns and clears mirror copies admitted to a forwarding output by the
// most recent call to [Switch.Forward]. Calls to [Switch.Peek] leave pending copies
// unchanged.
func (s *Switch) Copies() []traffic.Copy {
	copies := s.copies
	s.copies = nil

	return copies
}

// Police admits octets through the configured ingress policer for port. A port
// without a policer always admits the traffic.
func (s *Switch) Police(now time.Time, name string, octets int) bool {
	bucket, ok := s.buckets[name]
	if !ok {
		return true
	}

	return bucket.Admit(now, octets)
}

// QueueMaxRate returns the configured maximum bit rate for a port and priority.
func (s *Switch) QueueMaxRate(name string, pcp vlan.PCP) (uint64, bool) {
	if s.traffic == nil {
		return 0, false
	}

	return s.traffic.MaxRate(name, pcp)
}

// Ports returns a copy of the switch port table.
func (s *Switch) Ports() port.Table {
	return s.ports.Clone()
}

// Entries returns the active forwarding database entries in sorted order,
// or nil if the switch does not have a bridge relay.
func (s *Switch) Entries() []bridge.Entry {
	if s.bridge == nil {
		return nil
	}

	return s.bridge.Entries()
}

// Groups returns the multicast membership entries for vid.
func (s *Switch) Groups(vid vlan.ID) []mcast.Entry {
	if s.mcast == nil {
		return nil
	}

	return s.mcast.Groups(vid)
}

// RouterPorts returns the multicast router ports for vid.
func (s *Switch) RouterPorts(vid vlan.ID) []mcast.RouterPort {
	if s.mcast == nil {
		return nil
	}

	return s.mcast.RouterPorts(vid)
}

// Learn validates and preloads the switch's bridge forwarding database with the
// provided seeds. It records normalized seeds in the construction specification.
// Invalid or duplicate seeds leave the switch unchanged.
func (s *Switch) Learn(seeds []bridge.Seed) error {
	if len(seeds) == 0 {
		return nil
	}
	if s.bridge == nil || s.cfg.Bridge == nil {
		return errs.New().
			Attr("field", "seeds").
			Msg("forwarding database seeds require bridge configuration")
	}

	combined := append(slices.Clone(s.seeds), seeds...)
	normalized, err := bridge.NormalizeSeeds(*s.cfg.Bridge, s.ports, combined)
	if err != nil {
		return err
	}
	if err := s.bridge.Learn(seeds); err != nil {
		return err
	}
	s.seeds = normalized

	return nil
}

// Forget removes a forwarding database entry with the given FID and MAC address from
// the switch's bridge relay, reporting whether an entry was present. It returns false
// when the switch has no bridge relay.
func (s *Switch) Forget(fid vlan.ID, mac netaddr.MAC) bool {
	if s.bridge == nil {
		return false
	}

	return s.bridge.Forget(fid, mac)
}

// RelayCounters returns the forwarding database lifecycle counters from the switch's
// bridge relay, or a zero-value Counters struct if the switch has no bridge relay.
func (s *Switch) RelayCounters() bridge.Counters {
	if s.bridge == nil {
		return bridge.Counters{}
	}

	return s.bridge.Counters()
}

// Speeds returns the resolved physical link speeds and duplex modes keyed by
// port name, or nil if the Ethernet capability is absent.
func (s *Switch) Speeds() map[string]phy.Resolved {
	if s.speeds == nil {
		return nil
	}
	cp := make(map[string]phy.Resolved, len(s.speeds))
	for k, v := range s.speeds {
		cp[k] = v
	}

	return cp
}

// Power returns the Power over Ethernet budget distribution and port allocations with analysis metadata.
func (s *Switch) Power() PowerResult {
	cp := phy.Allocation{}
	if s.power.Ports != nil {
		cp.Ports = make(map[string]phy.PortAllocation, len(s.power.Ports))
		for k, v := range s.power.Ports {
			cp.Ports[k] = v
		}
	}
	if s.power.Groups != nil {
		cp.Groups = make(map[string]phy.GroupAllocation, len(s.power.Groups))
		for k, v := range s.power.Groups {
			cp.Groups[k] = v
		}
	}

	var issues []analysis.Issue
	portNames := make([]string, 0, len(cp.Ports))
	for name := range cp.Ports {
		portNames = append(portNames, name)
	}
	slices.Sort(portNames)

	for _, portName := range portNames {
		pa := cp.Ports[portName]
		if pa.State == phy.PowerUnknown {
			scope := analysis.FieldScope(analysis.NodeScope(s.nodeID), "poe", portName)
			issues = append(issues, analysis.Issue{
				Code:    "poe-demand-unknown",
				Status:  analysis.Incomplete,
				Scope:   scope,
				Message: fmt.Sprintf("port %q has unknown poe power demand", portName),
			})
		}
	}

	scope := analysis.FieldScope(analysis.NodeScope(s.nodeID), "poe")
	meta := analysis.NewMetadata(scope, issues, analysis.EvidenceCatalog{}, nil)

	return PowerResult{
		Allocation: cp,
		Metadata:   meta,
	}
}

// Forward processes an arrival on an ingress port at the given time, updating
// the forwarding database if a bridge relay is present, and returns the forwarding result
// with analysis metadata.
func (s *Switch) Forward(now time.Time, ingress string, f ethernet.Frame) ForwardResult {
	return s.wrapResult(s.forward(now, ingress, f, true))
}

// Peek processes an arrival on an ingress port at the given time without mutating
// the forwarding database and returns the forwarding result with analysis metadata.
func (s *Switch) Peek(now time.Time, ingress string, f ethernet.Frame) ForwardResult {
	return s.wrapResult(s.forward(now, ingress, f, false))
}

// ComposeForwardResult attaches switch-owned trust metadata to a forwarding result
// produced before the bridge pipeline. dependencyPorts names the port state that
// could have changed the result; a member port also depends on its logical LAG.
func (s *Switch) ComposeForwardResult(res bridge.Result, dependencyPorts ...string) ForwardResult {
	for _, name := range dependencyPorts {
		s.forwardingDependencies(name).consult(&res)
	}

	return s.wrapResult(res)
}

func (s *Switch) wrapResult(res bridge.Result) ForwardResult {
	var issues []runtimeIssue
	consultedScopes := res.ConsultedScopes()

	for _, p := range res.ConsultedPorts() {
		scope := analysis.PortScope(s.nodeID, p.Name)
		consultedScopes = append(consultedScopes, scope)
		if p.AdminStatus == port.Unknown || p.OperStatus == port.Unknown {
			issues = append(issues, runtimeIssue{
				issue: analysis.Issue{
					Code:    "unknown-operational-status",
					Status:  analysis.Incomplete,
					Scope:   scope,
					Message: fmt.Sprintf("port %q has unknown operational status", p.Name),
				},
				facts: []trace.Fact{port.ForwardingFact(p.Name, p, p.Forwards(), "")},
			})
		}
	}
	consultedScopes = canonicalScopes(consultedScopes)
	if len(s.protocolIssues) > 0 {
		sortedPortNames := make([]string, 0, len(s.protocolIssues))
		for name := range s.protocolIssues {
			sortedPortNames = append(sortedPortNames, name)
		}
		slices.Sort(sortedPortNames)
		for _, portName := range sortedPortNames {
			portScope := analysis.PortScope(s.nodeID, portName)
			if slices.ContainsFunc(consultedScopes, portScope.Overlaps) {
				issue := s.protocolIssues[portName]
				if !slices.ContainsFunc(issues, func(r runtimeIssue) bool {
					return r.issue.Code == issue.Code && r.issue.Scope.Compare(issue.Scope) == 0
				}) {
					p, _ := s.ports.Port(portName)
					issues = append(issues, runtimeIssue{
						issue: issue,
						facts: []trace.Fact{port.ForwardingFact(portName, p, p.Forwards(), "")},
					})
				}
			}
		}
	}
	for _, scope := range consultedScopes {
		if s.metadata.Scope().Contains(scope) {
			continue
		}
		issues = append(issues, runtimeIssue{
			issue: analysis.Issue{
				Code:    "forwarding-dependency-outside-loaded-scope",
				Status:  analysis.Incomplete,
				Scope:   scope,
				Message: fmt.Sprintf("scope %s lies outside loaded analysis scope %s", scope, s.metadata.Scope()),
			},
			facts: []trace.Fact{runtimeFact{
				typeID:    "analysis.loaded-scope",
				canonical: s.metadata.Scope().String(),
			}},
		})
	}
	issues = append(issues, s.lagRebalanceIssues()...)
	issues = append(issues, s.mcastQueryUnobservedIssues()...)
	issues = append(issues, s.pvstBoundaryIssues()...)

	return ForwardResult{
		Result:   res,
		Metadata: forwardingMetadata(s.nodeID, s.metadata, consultedScopes, issues),
	}
}

// lagRebalanceIssues raises lag-rebalance-unmodeled for each LAG the forward
// or peek call in progress actually used with a balanced selection old enough
// that unmodeled rebalancing could have moved it.
func (s *Switch) lagRebalanceIssues() []runtimeIssue {
	if len(s.lagRebalanceHits) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.lagRebalanceHits))
	for name := range s.lagRebalanceHits {
		names = append(names, name)
	}
	slices.Sort(names)

	issues := make([]runtimeIssue, 0, len(names))
	for _, name := range names {
		issues = append(issues, runtimeIssue{
			issue: analysis.Issue{
				Code:    IssueLAGRebalanceUnmodeled,
				Status:  analysis.Incomplete,
				Scope:   s.aggregatorScope(name),
				Message: fmt.Sprintf("LAG %q selection is balanced and old enough that unmodeled rebalancing could have moved it", name),
			},
			facts: s.lagConstructionEvidence(name),
		})
	}

	return issues
}

func (s *Switch) lagConstructionEvidence(lagName string) []trace.Fact {
	var evidence []trace.Fact
	if p, ok := s.ports.Port(lagName); ok {
		evidence = append(evidence, port.ForwardingFact(lagName, p, p.Forwards(), ""))
	}
	for _, m := range s.ports.Members(lagName) {
		evidence = append(evidence, port.ForwardingFact(m.Name, m, m.Forwards(), ""))
	}

	return evidence
}

// mcastQueryUnobservedIssues raises mcast-query-unobserved for each (VLAN,
// group) pair the forward or peek call in progress resolved while an
// expected query had gone unobserved past its last member query time.
func (s *Switch) mcastQueryUnobservedIssues() []runtimeIssue {
	if len(s.mcastQueryUnobservedHits) == 0 {
		return nil
	}
	seen := make(map[mcastQueryUnobservedHit]struct{}, len(s.mcastQueryUnobservedHits))
	issues := make([]runtimeIssue, 0, len(s.mcastQueryUnobservedHits))
	for _, hit := range s.mcastQueryUnobservedHits {
		if _, ok := seen[hit]; ok {
			continue
		}
		seen[hit] = struct{}{}
		issues = append(issues, runtimeIssue{
			issue: analysis.Issue{
				Code:    IssueMcastQueryUnobserved,
				Status:  analysis.Incomplete,
				Scope:   mcastGroupScope(s.nodeID, hit.vid, hit.group),
				Message: fmt.Sprintf("multicast group %s on vlan %d has an expected query that was never observed", hit.group, hit.vid),
			},
			facts: s.mcastConstructionEvidence(hit.vid),
		})
	}
	slices.SortFunc(issues, func(a, b runtimeIssue) int { return a.issue.Scope.Compare(b.issue.Scope) })

	return issues
}

// recordPVSTBoundaries notes every port of the journey in progress that faces
// a spanning tree neighbor whose per-VLAN trees this switch cannot simulate,
// so pvstBoundaryIssues can report the VLANs that go unheard across it.
//
// VLAN 1 is excluded because it is the one VLAN that does converge across the
// boundary: a PVST bridge sends VLAN 1's tree to the IEEE bridge group
// address as well, and that frame reaches an RSTP or MSTP neighbor's CIST
// unchanged. Every other VLAN's tree stops at the port.
func (s *Switch) recordPVSTBoundaries(res bridge.Result) {
	if s.stp == nil || res.FID == 0 || res.FID == 1 {
		return
	}

	record := func(name string) {
		if name == "" || !s.stp.PVSTBoundary(name) {
			return
		}
		if s.pvstBoundaryHits == nil {
			s.pvstBoundaryHits = make(map[pvstBoundaryHit]struct{})
		}
		s.pvstBoundaryHits[pvstBoundaryHit{port: name, vid: res.FID}] = struct{}{}
	}

	record(res.Ingress)
	for _, egress := range res.Egress {
		record(egress.Port)
	}
}

// pvstBoundaryIssues raises stp-pvst-boundary for each port and VLAN the
// journey in progress crossed at a boundary between per-VLAN spanning tree
// and a protocol that runs one tree for many VLANs.
//
// The scope names the port and VLAN together as one protocol instance rather
// than nesting a VLAN scope inside a port scope. Scope containment is a key
// prefix test, and the bridge consults the port's own spanning tree scope on
// every gated frame, so a nested scope would be contained by it and a VLAN 1
// journey through the port would pick up another VLAN's issue.
func (s *Switch) pvstBoundaryIssues() []runtimeIssue {
	if len(s.pvstBoundaryHits) == 0 {
		return nil
	}

	hits := make([]pvstBoundaryHit, 0, len(s.pvstBoundaryHits))
	for hit := range s.pvstBoundaryHits {
		hits = append(hits, hit)
	}
	slices.SortFunc(hits, func(a, b pvstBoundaryHit) int {
		if c := cmp.Compare(a.port, b.port); c != 0 {
			return c
		}

		return cmp.Compare(a.vid, b.vid)
	})

	issues := make([]runtimeIssue, 0, len(hits))
	for _, hit := range hits {
		p, _ := s.ports.Port(hit.port)
		issues = append(issues, runtimeIssue{
			issue: analysis.Issue{
				Code:    IssuePVSTBoundary,
				Status:  analysis.Unsupported,
				Scope:   pvstBoundaryScope(s.nodeID, hit.port, hit.vid),
				Message: fmt.Sprintf("port %q faces a spanning tree neighbor that does not run a tree for vlan %d", hit.port, hit.vid),
			},
			facts: []trace.Fact{port.ForwardingFact(hit.port, p, p.Forwards(), "")},
		})
	}

	return issues
}

// pvstBoundaryScope names the analysis scope for one port and VLAN at a
// per-VLAN spanning tree boundary.
func pvstBoundaryScope(nodeID, portName string, vid vlan.ID) analysis.Scope {
	return analysis.ProtocolScope(nodeID, string(port.LayerStp), fmt.Sprintf("%s/%d", portName, vid))
}

func (s *Switch) mcastConstructionEvidence(vid vlan.ID) []trace.Fact {
	if s.mcast == nil {
		return nil
	}
	var evidence []trace.Fact
	for _, r := range s.mcast.RouterPorts(vid) {
		if p, ok := s.ports.Port(r.Port); ok {
			evidence = append(evidence, port.ForwardingFact(r.Port, p, p.Forwards(), ""))
		}
	}

	return evidence
}

// mcastGroupScope names the analysis scope for one multicast group's
// forwarding state on a VLAN.
func mcastGroupScope(nodeID string, vid vlan.ID, group netip.Addr) analysis.Scope {
	return analysis.ProtocolScope(nodeID, string(port.LayerMcast), fmt.Sprintf("%d/%s", vid, group))
}

func (s *Switch) forward(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	s.lagRebalanceHits = nil
	s.mcastQueryUnobservedHits = nil
	s.pvstBoundaryHits = nil
	if mutate {
		s.copies = nil
	}
	if s.isMirrorOutputPort(ingress) {
		mirror := s.mirrorForOutput(ingress)
		res := bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  traffic.ReasonMirrorOutput,
				Steps: []trace.Step{{
					Layer:   port.LayerTraffic,
					Op:      trace.OpDrop,
					RuleID:  "traffic.mirror.output_drop",
					Subject: trace.Subject{Kind: "port", Key: ingress},
					Inputs:  []trace.Fact{traffic.MirrorDecisionFact(mirror, ingress, frameOctets(f), traffic.ReasonMirrorOutput)},
				}},
			},
			Ingress: ingress,
		}
		s.forwardingDependencies(ingress).consult(&res)
		return res
	}

	if s.lag != nil && f.EtherType == ethernet.EtherTypeSlowProtocols && len(f.Payload) > 0 && f.Payload[0] == 1 {
		p, ok := s.ports.Port(ingress)
		if ok && p.LagParent != "" && p.Forwards() {
			res := s.interceptLACP(now, ingress, f, mutate)
			res.ConsultScopes(s.aggregatorScope(p.LagParent))
			res.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, port.LayerLag), "ports", ingress,
			))
			s.forwardingDependencies(ingress).consult(&res)
			return s.finishForward(now, ingress, f, res, mutate)
		}
	}

	if s.stp != nil && f.Dst == stpGroupAddress {
		res := s.interceptBPDU(now, ingress, f, mutate)
		if res.Reason != port.ReasonPortDown {
			res.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, port.LayerStp), "ports", res.Ingress,
			))
		}
		s.forwardingDependencies(ingress).consult(&res)
		return s.finishForward(now, ingress, f, res, mutate)
	}

	if s.stp != nil && f.Dst == sstpGroupAddress {
		res := s.interceptSSTP(now, ingress, f, mutate)
		if res.Reason != port.ReasonPortDown {
			res.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, port.LayerStp), "ports", res.Ingress,
			))
		}
		s.forwardingDependencies(ingress).consult(&res)
		return s.finishForward(now, ingress, f, res, mutate)
	}

	if s.loopprotect != nil && f.Dst == loopprotect.GroupAddress {
		if res, handled := s.interceptLoopProtect(now, ingress, f, mutate); handled {
			s.forwardingDependencies(ingress).consult(&res)
			return s.finishForward(now, ingress, f, res, mutate)
		}
	}

	var routingScopes []analysis.Scope
	if s.routing == nil && metadataHasScopedContent(s.metadata, routing.VRFScope(s.nodeID, routing.DefaultVRF)) {
		resolved, ok := s.ports.Resolve(ingress)
		name := ingress
		if ok {
			name = resolved.Name
		}
		routingScopes = append(routingScopes, routing.PortLookupScope(s.nodeID, routing.DefaultVRF, name))
	}

	if s.routing != nil {
		receive := s.ports.Receive(ingress)
		resolved := receive.Resolved
		if resolved.Name != "" {
			portLookupScopes := s.routing.PortLookupScopes(resolved.Name)
			routingScopes = append(routingScopes, portLookupScopes...)
			if iface, ok := s.routing.ByPort(resolved.Name); ok {
				if receive.Reason != "" {
					res := bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  port.ReasonPortDown,
							Steps: []trace.Step{
								{
									Layer:   port.LayerRouting,
									Op:      trace.OpDrop,
									RuleID:  "port.status.down",
									Subject: trace.Subject{Kind: "port", Key: receive.Decisive},
									Inputs:  receive.ForwardingFacts(),
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
					s.forwardingDependencies(ingress).consult(&res)
					res.ConsultScopes(portLookupScopes...)
					return s.finishForward(now, ingress, f, res, mutate)
				}

				ownershipScope := s.routing.InterfaceOwnershipScope(iface)
				if !s.routing.Owns(iface, f) {
					res := bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  routing.ReasonNotBridged,
							Steps: []trace.Step{
								{
									Layer:   port.LayerRouting,
									Op:      trace.OpDrop,
									RuleID:  "routing.not_bridged",
									Subject: trace.Subject{Kind: "port", Key: resolved.Name},
									Inputs:  []trace.Fact{port.ForwardingFact(resolved.Name, resolved, true, routing.ReasonNotBridged)},
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
					s.forwardingDependencies(ingress).consult(&res)
					res.ConsultScopes(append(portLookupScopes, ownershipScope)...)
					return s.finishForward(now, ingress, f, res, mutate)
				}

				pcp, dei := framePriority(f)
				routeRes := s.routing.Route(iface, f)
				res := s.assembleRouteResult(now, resolved.Name, 0, pcp, dei, nil, routeRes, mutate)
				s.forwardingDependencies(ingress).consult(&res)
				res.ConsultScopes(append(portLookupScopes, ownershipScope)...)
				return s.finishForward(now, ingress, f, res, mutate)
			}
		}
	}

	if s.bridge != nil {
		controlCandidate := multicastControlCandidate(f)
		in, res, ok := s.bridge.Ingress(now, ingress, f, mutate && !controlCandidate, mutate)
		if !ok {
			res.ConsultScopes(routingScopes...)
			if s.missingSTP && (f.Dst == stpGroupAddress || f.Dst == sstpGroupAddress) {
				res.ConsultScopes(analysis.FieldScope(
					protocolScope(s.nodeID, port.LayerStp), "ports", res.Ingress,
				))
			}
			return s.finishForward(now, ingress, f, res, mutate)
		}
		in.ConsultScopes(routingScopes...)

		if controlCandidate && s.mcast != nil {
			in.ConsultScopes(analysis.FieldScope(
				protocolScope(s.nodeID, port.LayerMcast),
				"vlans", fmt.Sprint(in.FID),
			))
			if s.mcast.Snooped(in.FID) {
				res := s.forwardMulticastControl(now, ingress, f, in, mutate)
				return s.finishForward(now, ingress, f, res, mutate)
			}
		}

		if s.routing != nil {
			vlanLookupScopes := s.routing.VLANLookupScopes(in.FID)
			in.ConsultScopes(vlanLookupScopes...)
			if iface, ok := s.routing.ByVLAN(in.FID); ok {
				in.ConsultScopes(s.routing.InterfaceOwnershipScope(iface))
				if s.routing.Owns(iface, f) {
					if controlCandidate {
						in = s.commitBridgeLearning(now, ingress, f, in, mutate)
					}
					routeRes := s.routing.Route(iface, f)
					res := s.assembleRouteResult(now, in.Port, in.FID, in.PCP, in.DEI, in.Steps, routeRes, mutate)
					res.Consult(in.ConsultedPorts()...)
					res.ConsultScopes(in.ConsultedScopes()...)
					return s.finishForward(now, ingress, f, res, mutate)
				}
			}
		} else if metadataHasScopedContent(s.metadata, routing.VRFScope(s.nodeID, routing.DefaultVRF)) {
			in.ConsultScopes(routing.VLANLookupScope(s.nodeID, routing.DefaultVRF, in.FID))
		}

		if controlCandidate {
			in = s.commitBridgeLearning(now, ingress, f, in, mutate)
		}

		return s.finishForward(now, ingress, f, s.bridge.Egress(in, f), mutate)
	}

	res := s.forwardHub(now, ingress, f, mutate)
	res.ConsultScopes(routingScopes...)
	return s.finishForward(now, ingress, f, res, mutate)
}

func multicastControlCandidate(f ethernet.Frame) bool {
	switch f.EtherType {
	case ethernet.EtherTypeIPv4:
		if len(f.Payload) < ip.V4HeaderLen {
			return false
		}
		headerLength := int(f.Payload[0]&0x0f) * 4
		return headerLength >= ip.V4HeaderLen && len(f.Payload) >= headerLength && f.Payload[9] == protocolIGMP
	case ethernet.EtherTypeIPv6:
		if len(f.Payload) < ip.V6HeaderLen {
			return false
		}
		return f.Payload[6] == protocolHopByHop && len(f.Payload) > ip.V6HeaderLen && f.Payload[ip.V6HeaderLen] == protocolICMPv6
	default:
		return false
	}
}

func (s *Switch) commitBridgeLearning(now time.Time, ingress string, f ethernet.Frame, in bridge.Ingress, mutate bool) bridge.Ingress {
	if !mutate {
		return in
	}

	learned, _, ok := s.bridge.Ingress(now, ingress, f, true, true)
	if !ok {
		return in
	}
	in.Consult(learned.ConsultedPorts()...)
	in.ConsultScopes(learned.ConsultedScopes()...)
	if len(learned.Steps) > len(in.Steps) {
		in.Steps = append(in.Steps, learned.Steps[len(in.Steps):]...)
	}

	return in
}

func (s *Switch) forwardMulticastControl(
	now time.Time,
	ingress string,
	f ethernet.Frame,
	in bridge.Ingress,
	mutate bool,
) bridge.Result {
	hdr, payload, err := ip.Decode(f.Payload)
	if err != nil {
		return badMulticastControl(in)
	}

	if hdr.V4 != nil {
		if hdr.HopLimit != 1 || !hdr.Dst.IsMulticast() {
			return badMulticastControl(in)
		}
		message, err := igmp.Decode(payload)
		if err != nil {
			if errors.Is(err, igmp.ErrUnsupported) {
				in = s.commitBridgeLearning(now, ingress, f, in, mutate)
				in.Steps = append(in.Steps, multicastControlStep("mcast.control.unsupported", "igmp", false, "unsupported", nil))
				return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
			}

			return badMulticastControl(in)
		}

		in = s.commitBridgeLearning(now, ingress, f, in, mutate)
		in.Steps = append(in.Steps, multicastControlStep(
			"mcast.control.admit", "igmp", true, "", mcast.IGMPControlMessageFact(hdr.Src, message),
		))
		if mutate {
			s.mcast.Learn(now, in.FID, in.Port, hdr.Src, message)
		}
		if message.Type == igmp.Query {
			return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
		}

		return s.bridge.EgressTo(in, f, routerPortNames(s.mcast.RouterPorts(in.FID)), mcast.ReasonNoRouterPort)
	}

	if hdr.HopLimit != 1 || !hdr.Src.IsLinkLocalUnicast() || !hasRouterAlert(payload) {
		return badMulticastControl(in)
	}
	message, err := mld.Decode(hdr, payload)
	if err != nil {
		if errors.Is(err, mld.ErrUnsupported) {
			in = s.commitBridgeLearning(now, ingress, f, in, mutate)
			in.Steps = append(in.Steps, multicastControlStep("mcast.control.unsupported", "mld", false, "unsupported", nil))
			return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
		}

		return badMulticastControl(in)
	}

	in = s.commitBridgeLearning(now, ingress, f, in, mutate)
	in.Steps = append(in.Steps, multicastControlStep(
		"mcast.control.admit", "mld", true, "", mcast.MLDControlMessageFact(hdr.Src, message),
	))
	if mutate {
		s.mcast.LearnMLD(now, in.FID, in.Port, hdr.Src, message)
	}
	if message.Type == mld.Query {
		return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
	}

	return s.bridge.EgressTo(in, f, routerPortNames(s.mcast.RouterPorts(in.FID)), mcast.ReasonNoRouterPort)
}

func multicastControlStep(ruleID trace.RuleID, proto string, admitted bool, reason trace.Reason, message trace.Fact) trace.Step {
	return trace.Step{
		Layer:   port.LayerMcast,
		Op:      trace.OpClassify,
		RuleID:  ruleID,
		Subject: trace.Subject{Kind: "protocol", Key: proto},
		Inputs:  traceFacts(message),
		Outputs: []trace.Fact{mcast.ControlDecisionFact(proto, admitted, reason)},
	}
}

func badMulticastControl(in bridge.Ingress) bridge.Result {
	steps := slices.Clone(in.Steps)
	steps = append(steps, trace.Step{
		Layer:   port.LayerMcast,
		Op:      trace.OpDrop,
		RuleID:  "mcast.control.bad",
		Subject: trace.Subject{Kind: "port", Key: in.Port},
		Outputs: []trace.Fact{mcast.ControlDecisionFact("", false, mcast.ReasonBadControl)},
	})

	res := bridge.Result{
		Trace:   trace.Trace{Outcome: trace.Dropped, Reason: mcast.ReasonBadControl, Steps: steps},
		Ingress: in.Port,
		FID:     in.FID,
	}
	res.Consult(in.ConsultedPorts()...)
	res.ConsultScopes(in.ConsultedScopes()...)

	return res
}

func hasRouterAlert(payload []byte) bool {
	if len(payload) < 2 || payload[0] != protocolICMPv6 {
		return false
	}
	headerLength := (int(payload[1]) + 1) * 8
	if len(payload) < headerLength {
		return false
	}

	found := false
	for i := 2; i < headerLength; {
		if payload[i] == 0 {
			i++
			continue
		}
		if i+1 >= headerLength {
			return false
		}
		optionLength := int(payload[i+1])
		if i+2+optionLength > headerLength {
			return false
		}
		if payload[i] == optionRouterAlert {
			if optionLength != 2 || payload[i+2] != 0 || payload[i+3] != 0 || found {
				return false
			}
			found = true
		}
		i += 2 + optionLength
	}

	return found
}

func (s *Switch) logicalPorts() []string {
	ports := make([]string, 0, s.ports.Len())
	for _, p := range s.ports.Ports() {
		if p.LagParent == "" {
			ports = append(ports, p.Name)
		}
	}

	return ports
}

func routerPortNames(routers []mcast.RouterPort) []string {
	ports := make([]string, len(routers))
	for i, router := range routers {
		ports[i] = router.Port
	}

	return ports
}

// Resolve selects multicast members and router ports for an eligible IP group
// frame as of now, filtering admitted member ports by the frame's IP source.
func (s *Switch) Resolve(now time.Time, vid vlan.ID, f ethernet.Frame) ([]string, bool) {
	if s.mcast == nil || s.cfg.Mcast == nil {
		return nil, false
	}
	if _, ok := s.cfg.Mcast.VLANs[vid]; !ok {
		return nil, false
	}
	if f.EtherType != ethernet.EtherTypeIPv4 && f.EtherType != ethernet.EtherTypeIPv6 {
		return nil, false
	}

	hdr, _, err := ip.Decode(f.Payload)
	if err != nil || !hdr.Dst.IsMulticast() {
		return nil, false
	}
	if f.EtherType == ethernet.EtherTypeIPv4 && hdr.V4 == nil ||
		f.EtherType == ethernet.EtherTypeIPv6 && hdr.V6 == nil {
		return nil, false
	}
	if hdr.Dst.Is4() {
		addr := hdr.Dst.As4()
		if addr[0] == 224 && addr[1] == 0 && addr[2] == 0 {
			return nil, false
		}
	} else {
		addr := hdr.Dst.As16()
		scope := addr[1] & 0x0f
		if hdr.Dst == allNodesAddress || scope == 0 || scope == 1 {
			return nil, false
		}
	}

	ports, registered, pending := s.mcast.Resolve(vid, hdr.Dst, hdr.Src, now)
	if registered {
		if pending {
			s.recordMcastQueryUnobserved(vid, hdr.Dst)
		}
		return ports, true
	}
	if s.cfg.Mcast.Floods(vid) {
		return nil, false
	}

	return ports, true
}

// MembershipFact records the multicast membership lookup used by bridge
// replication, naming the frame's IP source: ports is already that source's
// admitted egress set, so the fact makes explicit which source produced it.
func (s *Switch) MembershipFact(now time.Time, vid vlan.ID, f ethernet.Frame, ports []string, decided bool) trace.Fact {
	if s.mcast == nil {
		return newMembershipFact(vid, netip.Addr{}, netip.Addr{}, ports, false, decided)
	}
	hdr, _, err := ip.Decode(f.Payload)
	if err != nil {
		return newMembershipFact(vid, netip.Addr{}, netip.Addr{}, ports, false, decided)
	}
	_, registered, _ := s.mcast.Resolve(vid, hdr.Dst, hdr.Src, now)

	return newMembershipFact(vid, hdr.Dst, hdr.Src, ports, registered, decided)
}

func (s *Switch) finishForward(now time.Time, ingress string, received ethernet.Frame, res bridge.Result, mutate bool) bridge.Result {
	s.recordPVSTBoundaries(res)

	if s.traffic == nil {
		return res
	}

	reserved := false
	transmitted := false
	for i, egress := range res.Egress {
		if !s.isMirrorOutputPort(egress.Port) {
			if egress.Dropped == "" {
				transmitted = true
			}
			continue
		}

		reserved = true
		res.Egress[i] = bridge.Egress{
			Port:    egress.Port,
			Frame:   egress.Frame,
			PCP:     egress.PCP,
			Dropped: traffic.ReasonMirrorOutput,
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerTraffic,
			Op:      trace.OpDrop,
			RuleID:  "traffic.mirror.egress_drop",
			Subject: trace.Subject{Kind: "port", Key: egress.Port},
			Inputs:  []trace.Fact{traffic.MirrorDecisionFact(s.mirrorForOutput(egress.Port), egress.Port, frameOctets(egress.Frame), traffic.ReasonMirrorOutput)},
		})
	}
	if reserved && !transmitted {
		res.Outcome = trace.Dropped
		res.Reason = traffic.ReasonMirrorOutput
	}

	var vlans *bridge.VLAN
	if s.cfg.Bridge != nil {
		vlans = s.cfg.Bridge.VLAN
	}
	resolvedIngress := res.Ingress
	if resolvedIngress == "" {
		resolvedIngress = ingress
	}
	copies := traffic.Copies(*s.traffic, vlans, resolvedIngress, res.FID, received, res.Egress)
	copies = s.readyMirrorCopies(now, &res, copies, mutate)
	if mutate {
		s.copies = copies
	}

	return res
}

func (s *Switch) readyMirrorCopies(now time.Time, res *bridge.Result, copies []traffic.Copy, mutate bool) []traffic.Copy {
	ready := copies[:0]
	for _, copy := range copies {
		output, ok := s.ports.Port(copy.Port)
		if !ok {
			continue
		}
		s.egressDependencies(copy.Port).consult(res)

		reason := trace.Reason("")
		var selection trace.Fact
		if !output.Forwards() {
			reason = port.ReasonPortDown
		} else if output.Kind == port.Lag {
			res.ConsultScopes(s.aggregatorScope(copy.Port))
			sel, fact := s.selectMemberWithFact(now, copy.Port, copy.Frame, copy.VLAN, mutate)
			selection = fact
			if !sel.OK {
				reason = bridge.ReasonNoMember
			} else if memberPort, exists := s.ports.Port(sel.Member); !exists || !memberPort.Forwards() {
				reason = port.ReasonPortDown
			} else {
				copy.Member = sel.Member
			}
		}

		decision := traffic.MirrorDecisionFact(copy.Mirror, copy.Port, frameOctets(copy.Frame), reason)
		inputs := traceFacts(selection)
		if reason != "" {
			inputs = append(inputs,
				port.ForwardingFact(copy.Port, output, false, reason),
				decision,
			)
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerTraffic,
				Op:      trace.OpDrop,
				RuleID:  traffic.RuleMirrorCopyDrop,
				Subject: trace.Subject{Kind: "port", Key: copy.Port},
				Inputs:  inputs,
			})

			continue
		}

		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerTraffic,
			Op:      trace.OpReplicate,
			RuleID:  traffic.RuleMirrorCopy,
			Subject: trace.Subject{Kind: "port", Key: copy.Port},
			Inputs:  inputs,
			Outputs: []trace.Fact{decision},
		})
		ready = append(ready, copy)
	}

	return ready
}

func (s *Switch) isMirrorOutputPort(name string) bool {
	if s.traffic == nil {
		return false
	}
	for _, mirror := range s.traffic.Mirrors {
		if mirror.OutputPort == name {
			return true
		}
	}

	return false
}

func (s *Switch) mirrorForOutput(name string) string {
	if s.traffic == nil {
		return ""
	}
	for _, mirror := range s.traffic.Mirrors {
		if mirror.OutputPort == name {
			return mirror.Name
		}
	}

	return ""
}

func frameOctets(f ethernet.Frame) int {
	return 14 + len(f.Payload) + 4*len(f.Tags)
}

func traceFacts(values ...trace.Fact) []trace.Fact {
	result := make([]trace.Fact, 0, len(values))
	for _, value := range values {
		if value != nil {
			result = append(result, value)
		}
	}

	return result
}

func framePriority(f ethernet.Frame) (vlan.PCP, bool) {
	if len(f.Tags) == 0 {
		return 0, false
	}
	outer := f.Tags[0]
	if outer.TPID != 0 && outer.TPID != uint16(ethernet.EtherTypeDot1Q) {
		return 0, false
	}

	return outer.PCP, outer.DEI
}

func (s *Switch) assembleRouteResult(
	now time.Time,
	ingressPort string,
	ingressFID vlan.ID,
	ingressPCP vlan.PCP,
	ingressDEI bool,
	ingressSteps []trace.Step,
	routeRes routing.Result,
	mutate bool,
) bridge.Result {
	routeScopes := routeRes.ConsultedScopes()
	if routeRes.Reason != "" {
		outcome := trace.Dropped
		if routeRes.Reason == routing.ReasonNotRouted {
			outcome = trace.Consumed
		}
		steps := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps))
		steps = append(steps, ingressSteps...)
		steps = append(steps, routeRes.Steps...)

		res := bridge.Result{
			Trace: trace.Trace{
				Outcome: outcome,
				Reason:  routeRes.Reason,
				Steps:   steps,
			},
			Ingress: ingressPort,
			FID:     ingressFID,
		}
		if routeRes.Interface != "" {
			s.routingForwardingDependencies(routeRes.Interface).consult(&res)
		}
		res.ConsultScopes(routeScopes...)

		return res
	}

	// Route names only an interface of its own table, so the lookup cannot miss.
	egressIface, _ := s.routing.Interface(routeRes.Interface)

	if egressIface.VLAN != 0 {
		stepsSoFar := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps))
		stepsSoFar = append(stepsSoFar, ingressSteps...)
		stepsSoFar = append(stepsSoFar, routeRes.Steps...)

		bridgeIn := bridge.Ingress{
			Port:   "",
			FID:    egressIface.VLAN,
			PCP:    ingressPCP,
			DEI:    ingressDEI,
			Steps:  stepsSoFar,
			Now:    now,
			Commit: mutate,
		}
		bridgeIn.ConsultScopes(routeScopes...)
		res := s.bridge.Egress(bridgeIn, routeRes.Frame)
		res.FID = egressIface.VLAN
		res.Ingress = ingressPort
		return res
	}

	steps := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps)+1)
	steps = append(steps, ingressSteps...)
	steps = append(steps, routeRes.Steps...)

	member, txReason := s.ports.Transmit(egressIface.Port, len(routeRes.Frame.Payload))
	if txReason != "" {
		egressPort, _ := s.ports.Port(egressIface.Port)
		steps = append(steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID("port.status." + string(txReason)),
			Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
			Inputs:  []trace.Fact{port.ForwardingFact(egressIface.Port, egressPort, false, txReason)},
			Outputs: []trace.Fact{routing.EgressFact(routeRes.Interface, egressIface.Port, member, txReason)},
		})
		res := bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  txReason,
				Steps:   steps,
			},
			Ingress: ingressPort,
			FID:     0,
			Egress: []bridge.Egress{
				{
					Port:    egressIface.Port,
					Member:  member,
					Frame:   routeRes.Frame,
					PCP:     ingressPCP,
					Dropped: txReason,
				},
			},
		}
		s.egressDependencies(egressIface.Port).consult(&res)
		res.ConsultScopes(routeScopes...)
		return res
	}

	p, _ := s.ports.Port(egressIface.Port)
	var selection trace.Fact
	resultScopes := routeScopes
	if p.Kind == port.Lag {
		resultScopes = append(resultScopes, s.aggregatorScope(egressIface.Port))
		sel, fact := s.selectMemberWithFact(now, egressIface.Port, routeRes.Frame, 0, mutate)
		selection = fact
		if !sel.OK {
			steps = append(steps, trace.Step{
				Layer:   port.LayerRouting,
				Op:      trace.OpDrop,
				RuleID:  "lag.egress.no_member",
				Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
				Inputs:  []trace.Fact{selection},
				Outputs: []trace.Fact{routing.EgressFact(routeRes.Interface, egressIface.Port, "", bridge.ReasonNoMember)},
			})
			res := bridge.Result{
				Trace: trace.Trace{
					Outcome: trace.Dropped,
					Reason:  bridge.ReasonNoMember,
					Steps:   steps,
				},
				Ingress: ingressPort,
				FID:     0,
				Egress: []bridge.Egress{
					{
						Port:    egressIface.Port,
						Frame:   routeRes.Frame,
						PCP:     ingressPCP,
						Dropped: bridge.ReasonNoMember,
					},
				},
			}
			s.egressDependencies(egressIface.Port).consult(&res)
			res.ConsultScopes(resultScopes...)
			return res
		}
		member = sel.Member
	}

	steps = append(steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpTransmit,
		RuleID:  "routing.transmit",
		Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
		Inputs:  traceFacts(selection),
		Outputs: []trace.Fact{routing.EgressFact(routeRes.Interface, egressIface.Port, member, "")},
	})
	res := bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Forwarded,
			Steps:   steps,
		},
		Ingress: ingressPort,
		FID:     0,
		Egress: []bridge.Egress{
			{
				Port:   egressIface.Port,
				Member: member,
				Frame:  routeRes.Frame,
				PCP:    ingressPCP,
			},
		},
	}
	s.egressDependencies(egressIface.Port).consult(&res)
	res.ConsultScopes(resultScopes...)
	return res
}

func (s *Switch) routingForwardingDependencies(name string) forwardingDependencies {
	iface, ok := s.routing.Interface(name)
	if !ok {
		return forwardingDependencies{}
	}
	if iface.Port != "" {
		return s.forwardingDependencies(iface.Port)
	}
	if _, ok := s.ports.Port(name); !ok {
		return forwardingDependencies{}
	}

	return s.forwardingDependencies(name)
}

// Age removes dynamic forwarding database entries older than the configured
// aging time relative to now. It is a no-op when the switch has no bridge subsystem.
func (s *Switch) Age(now time.Time) {
	if s.bridge != nil {
		s.bridge.Age(now)
	}
	if s.mcast != nil {
		s.mcast.Age(now)
	}
}

func (s *Switch) forwardHub(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	var res bridge.Result
	res.Outcome = trace.Dropped
	res.FID = 0
	s.forwardingDependencies(ingress).consult(&res)
	pcp, _ := framePriority(f)

	p, ok := s.ports.Port(ingress)
	if !ok {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpDrop,
			RuleID:  "port.status.not_found",
			Subject: trace.Subject{Kind: "port", Key: ingress},
			Outputs: []trace.Fact{port.ForwardingFact(ingress, port.Port{}, false, port.ReasonPortDown)},
		})

		return res
	}
	res.Ingress = p.Name
	if !p.Forwards() {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpDrop,
			RuleID:  "port.status.down",
			Subject: trace.Subject{Kind: "port", Key: p.Name},
			Inputs:  []trace.Fact{port.ForwardingFact(ingress, p, false, port.ReasonPortDown)},
		})

		return res
	}
	resolved, ok := s.ports.Resolve(ingress)
	if !ok {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpDrop,
			RuleID:  "port.lag.parent_not_found",
			Subject: trace.Subject{Kind: "port", Key: ingress},
			Inputs:  []trace.Fact{port.ForwardingFact(ingress, p, false, port.ReasonPortDown)},
		})

		return res
	}
	res.Ingress = resolved.Name

	if !resolved.Forwards() {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpDrop,
			RuleID:  "port.status.down",
			Subject: trace.Subject{Kind: "port", Key: resolved.Name},
			Inputs:  []trace.Fact{port.ForwardingFact(ingress, p, true, ""), port.ForwardingFact(resolved.Name, resolved, false, port.ReasonPortDown)},
		})

		return res
	}

	var candidates []port.Port
	var eligible int
	var unavailable []trace.Fact
	for _, cand := range s.ports.Ports() {
		if cand.LagParent != "" || cand.Name == res.Ingress {
			continue
		}
		eligible++
		s.egressDependencies(cand.Name).consult(&res)
		if !cand.Forwards() {
			unavailable = append(unavailable, port.ForwardingFact(cand.Name, cand, false, port.ReasonPortDown))
			continue
		}
		candidates = append(candidates, cand)
	}

	if len(candidates) == 0 {
		res.Reason = bridge.ReasonNoEgress
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpDrop,
			RuleID:  "port.hub.no_egress",
			Subject: trace.Subject{Kind: "port", Key: res.Ingress},
			Inputs:  unavailable,
			Outputs: []trace.Fact{port.HubEgressFact(eligible, 0, bridge.ReasonNoEgress)},
		})
		return res
	}

	var transmitted int
	for _, cand := range candidates {
		if cand.MTU > 0 && len(f.Payload) > cand.MTU {
			res.Egress = append(res.Egress, bridge.Egress{
				Port:    cand.Name,
				Frame:   f,
				PCP:     pcp,
				Dropped: port.ReasonMTUExceeded,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerPort,
				Op:      trace.OpDrop,
				RuleID:  "port.status.mtu-exceeded",
				Subject: trace.Subject{Kind: "port", Key: cand.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(cand.Name, cand, false, port.ReasonMTUExceeded)},
			})

			continue
		}

		var member string
		var selection trace.Fact
		if cand.Kind == port.Lag {
			res.ConsultScopes(s.aggregatorScope(cand.Name))
			sel, fact := s.selectMemberWithFact(now, cand.Name, f, 0, mutate)
			selection = fact
			if !sel.OK {
				res.Egress = append(res.Egress, bridge.Egress{
					Port:    cand.Name,
					Frame:   f,
					PCP:     pcp,
					Dropped: bridge.ReasonNoMember,
				})
				res.Steps = append(res.Steps, trace.Step{
					Layer:   port.LayerPort,
					Op:      trace.OpDrop,
					RuleID:  "lag.egress.no_member",
					Subject: trace.Subject{Kind: "port", Key: cand.Name},
					Inputs:  []trace.Fact{selection},
				})

				continue
			}
			member = sel.Member
		}

		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpReplicate,
			RuleID:  "port.hub.replicate",
			Subject: trace.Subject{Kind: "port", Key: cand.Name},
			Inputs:  traceFacts(selection),
			Outputs: []trace.Fact{port.ForwardingFact(cand.Name, cand, true, "")},
		})
		res.Egress = append(res.Egress, bridge.Egress{
			Port:   cand.Name,
			Member: member,
			Frame:  f,
			PCP:    pcp,
		})
		transmitted++
	}

	if transmitted > 0 {
		res.Outcome = trace.Flooded
	} else {
		res.Outcome = trace.Dropped
		if len(res.Egress) > 0 {
			res.Reason = res.Egress[0].Dropped
		} else {
			res.Reason = port.ReasonMTUExceeded
		}
	}

	return res
}

type forwardingDependencies struct {
	ports  []port.Port
	scopes []analysis.Scope
}

func (dependencies forwardingDependencies) consult(res *bridge.Result) {
	res.Consult(dependencies.ports...)
	res.ConsultScopes(dependencies.scopes...)
}

func (s *Switch) forwardingDependencies(name string) forwardingDependencies {
	p, ok := s.ports.Port(name)
	if !ok {
		return forwardingDependencies{ports: []port.Port{(port.Port{Name: name}).Normalize()}}
	}
	path := []port.Port{p}
	var scopes []analysis.Scope
	if !p.Forwards() || p.LagParent == "" {
		return forwardingDependencies{ports: path}
	}
	parent, exists := s.ports.Port(p.LagParent)
	if !exists {
		return forwardingDependencies{ports: path}
	}
	path = append(path, parent)
	if parent.Forwards() {
		scopes = append(scopes, s.aggregatorScope(p.LagParent))
	}

	return forwardingDependencies{ports: path, scopes: scopes}
}

func (s *Switch) egressDependencies(name string) forwardingDependencies {
	dependencies := s.forwardingDependencies(name)
	if len(dependencies.ports) != 1 || dependencies.ports[0].Kind != port.Lag || !dependencies.ports[0].Forwards() {
		return dependencies
	}
	dependencies.ports = append(dependencies.ports, s.ports.Members(name)...)

	return dependencies
}

func (s *Switch) aggregatorScope(name string) analysis.Scope {
	return analysis.FieldScope(protocolScope(s.nodeID, port.LayerLag), "aggregators", name)
}

func (s *Switch) interceptLACP(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	before := s.lag.PortInfo(ingress)
	pdu, err := lacp.Decode(f)
	if err != nil {
		if mutate {
			s.lag.BadLACPDU(ingress)
		}
		after := s.lag.PortInfo(ingress)

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  lag.ReasonUnsupportedLACPDU,
				Steps: []trace.Step{
					{
						Layer:   port.LayerLag,
						Op:      trace.OpDrop,
						RuleID:  "lag.lacpdu.unsupported",
						Subject: trace.Subject{Kind: "port", Key: ingress},
						Inputs:  []trace.Fact{lag.LACPDecodeFact(f, false, lag.ReasonUnsupportedLACPDU)},
						Outputs: []trace.Fact{lag.MemberTransitionFact(ingress, "bad-lacpdu", before, after)},
					},
				},
			},
			Ingress: ingress,
		}
	}

	if mutate {
		fx := s.lag.Receive(now, ingress, pdu)
		s.applyLAGEffects(now, fx)
	}
	after := s.lag.PortInfo(ingress)

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{
				{
					Layer:   port.LayerLag,
					Op:      trace.OpClassify,
					RuleID:  "lag.lacpdu.admit",
					Subject: trace.Subject{Kind: "port", Key: ingress},
					Inputs:  []trace.Fact{lag.LACPDecodeFact(f, true, "")},
					Outputs: []trace.Fact{lag.LACPDecisionFact(pdu, before, after)},
				},
			},
		},
		Ingress: ingress,
	}
}

// interceptLoopProtect handles a frame addressed to the loop-protection probe
// group. It reports handled=false for a probe this switch did not originate,
// or for a frame that does not decode as a probe at all: both fall through to
// the ordinary relay path unclassified, where an unrecognized destination
// floods as unregistered multicast. A probe this switch did originate is
// classified through the bridge's ordinary VLAN and gate pipeline first, so a
// gated ingress port still denies it exactly as it would deny any other
// frame; only once that pipeline admits it does this apply the port's
// configured loop-protection action.
func (s *Switch) interceptLoopProtect(now time.Time, ingress string, f ethernet.Frame, mutate bool) (bridge.Result, bool) {
	probe, err := loopprotect.Decode(f)
	if err != nil || probe.OriginMAC != s.cfg.MAC {
		return bridge.Result{}, false
	}

	in, res, ok := s.bridge.Ingress(now, ingress, f, false, false)
	if !ok {
		return res, true
	}

	before := s.loopprotect.PortInfo(probe.Port)
	if mutate {
		ret := loopprotect.Return{
			VID:                in.FID,
			SameUntaggedDomain: s.sameUntaggedDomain(probe.Port, probe.VID, in.FID),
		}
		fx := s.loopprotect.Receive(now, ret, probe)
		s.applyLoopProtectEffects(fx)
	}
	after := s.loopprotect.PortInfo(probe.Port)

	steps := append([]trace.Step(nil), in.Steps...)
	steps = append(steps, trace.Step{
		Layer:   port.LayerLoopProtect,
		Op:      trace.OpClassify,
		RuleID:  "loopprotect.probe.return",
		Subject: trace.Subject{Kind: "port", Key: probe.Port},
		Inputs:  []trace.Fact{loopProtectProbeFact(probe)},
		Outputs: []trace.Fact{loopProtectReturnFact(probe, in.FID, before, after)},
	})
	if before.Action != after.Action {
		steps = append(steps, trace.Step{
			Layer:   port.LayerLoopProtect,
			Op:      trace.OpFilter,
			RuleID:  "loopprotect.port.block",
			Subject: trace.Subject{Kind: "port", Key: probe.Port},
			Outputs: []trace.Fact{loopProtectTransitionFact(probe.Port, before, after)},
		})
	}

	result := bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps:   steps,
		},
		Ingress: in.Port,
		FID:     in.FID,
	}
	result.Consult(in.ConsultedPorts()...)
	result.ConsultScopes(in.ConsultedScopes()...)
	result.ConsultScopes(analysis.FieldScope(
		protocolScope(s.nodeID, port.LayerLoopProtect), "ports", probe.Port,
	))

	return result, true
}

type loopProtectDecisionFact string

func (f loopProtectDecisionFact) TypeID() string    { return "vswitch.loopprotect_decision" }
func (f loopProtectDecisionFact) Canonical() string { return string(f) }

// loopProtectProbeFact returns an immutable snapshot of a returned probe's payload.
func loopProtectProbeFact(probe loopprotect.Probe) trace.Fact {
	return loopProtectDecisionFact("origin=" + probe.OriginMAC.String() +
		";sequence=" + strconv.FormatUint(uint64(probe.Sequence), 10) +
		";sent_vid=" + strconv.FormatUint(uint64(probe.VID), 10) +
		";port=" + strconv.Quote(probe.Port))
}

// loopProtectReturnFact returns an immutable snapshot of a probe's return,
// carrying both the VLAN it was sent on and the VLAN it was classified into
// so an inter-VLAN loop is visible in the trace.
func loopProtectReturnFact(probe loopprotect.Probe, returnedVID vlan.ID, before, after loopprotect.PortInfo) trace.Fact {
	return loopProtectDecisionFact("port=" + strconv.Quote(probe.Port) +
		";sent_vid=" + strconv.FormatUint(uint64(probe.VID), 10) +
		";returned_vid=" + strconv.FormatUint(uint64(returnedVID), 10) +
		";before=" + loopProtectPortInfoSnapshot(before) +
		";after=" + loopProtectPortInfoSnapshot(after))
}

// loopProtectTransitionFact returns an immutable snapshot of a loop-protection
// port action transition.
func loopProtectTransitionFact(portName string, before, after loopprotect.PortInfo) trace.Fact {
	return loopProtectDecisionFact("port=" + strconv.Quote(portName) +
		";before=" + loopProtectPortInfoSnapshot(before) +
		";after=" + loopProtectPortInfoSnapshot(after))
}

func loopProtectPortInfoSnapshot(info loopprotect.PortInfo) string {
	return "{action=" + strconv.Quote(string(info.Action)) +
		";inter_vlan=" + strconv.FormatBool(info.InterVLAN) +
		";recurrences=" + strconv.FormatUint(info.Recurrences, 10) + "}"
}

func (s *Switch) interceptBPDU(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	// The relay's first checks apply to a BPDU too: a dead or unknown port
	// received nothing, and a trace saying Consumed there would hide a BPDU
	// that died on a cut cable.
	receive := s.ports.Receive(ingress)
	if receive.Reason != "" {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  port.ReasonPortDown,
				Steps: []trace.Step{{
					Layer:   port.LayerStp,
					Op:      trace.OpDrop,
					RuleID:  "port.status.down",
					Subject: trace.Subject{Kind: "port", Key: receive.Decisive},
					Inputs:  receive.ForwardingFacts(),
				}},
			},
			Ingress: receive.Resolved.Name,
		}
	}
	resolvedPort := receive.Resolved.Name
	before := s.stp.PortInfo(resolvedPort)

	bpdu, err := stp.Decode(f)
	if err != nil {
		if mutate {
			s.stp.BadBPDU(resolvedPort)
		}
		after := s.stp.PortInfo(resolvedPort)

		reason := stp.ReasonUnsupportedBPDU
		if r, ok := errs.Attributes(err)["reason"].(trace.Reason); ok {
			reason = r
		}

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  reason,
				Steps: []trace.Step{
					{
						Layer:   port.LayerStp,
						Op:      trace.OpDrop,
						RuleID:  trace.RuleID("stp.bpdu." + string(reason)),
						Subject: trace.Subject{Kind: "port", Key: resolvedPort},
						Inputs:  []trace.Fact{stp.BPDUDecodeFact(f, false, reason)},
						Outputs: []trace.Fact{stp.PortTransitionFact(resolvedPort, "bad-bpdu", before, after)},
					},
				},
			},
			Ingress: resolvedPort,
		}
	}

	if mutate {
		fx := s.stp.Receive(now, resolvedPort, bpdu)
		s.applySTPEffects(fx)
	}
	after := s.stp.PortInfo(resolvedPort)

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{
				{
					Layer:   port.LayerStp,
					Op:      trace.OpClassify,
					RuleID:  "stp.bpdu.admit",
					Subject: trace.Subject{Kind: "port", Key: resolvedPort},
					Inputs:  []trace.Fact{stp.BPDUDecodeFact(f, true, "")},
					Outputs: []trace.Fact{stp.BPDUDecisionFact(bpdu, before, after)},
				},
			},
		},
		Ingress: resolvedPort,
	}
}

// interceptSSTP handles a frame addressed to the per-VLAN BPDU group. Unlike
// an IEEE-addressed BPDU, an SSTP BPDU means nothing without the VLAN it
// arrived on: the tree it belongs to is chosen by that VLAN, and the check
// that the peer agrees about the link compares it with the VLAN the BPDU
// itself names.
//
// The VLAN is resolved from the frame's own tag, or the port's untagged VLAN
// when it carries none, rather than through the bridge's ingress pipeline the
// way a loop-protection probe is. A probe may be denied by a gated port; a
// BPDU may not. The ports a spanning tree holds discarding are exactly the
// ones whose blocking depends on continuing to hear their peer, so running a
// BPDU through the gate the tree itself set would drop the frames that keep
// the topology converged. This is why interceptSSTP does not consult the
// bridge's ingress rules for the spanning tree gate. It still refuses a
// frame whose resolved VLAN the ingress port does not carry: bypassing the
// gate must not also bypass the bridge's own notion of which VLANs a port
// speaks, or a BPDU tagged for a VLAN the port never joined could rewrite
// another VLAN's topology. A tag whose VID is 0 is a priority tag, not a
// VLAN selection, so it resolves the same as an untagged frame.
func (s *Switch) interceptSSTP(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	receive := s.ports.Receive(ingress)
	if receive.Reason != "" {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  port.ReasonPortDown,
				Steps: []trace.Step{{
					Layer:   port.LayerStp,
					Op:      trace.OpDrop,
					RuleID:  "port.status.down",
					Subject: trace.Subject{Kind: "port", Key: receive.Decisive},
					Inputs:  receive.ForwardingFacts(),
				}},
			},
			Ingress: receive.Resolved.Name,
		}
	}
	resolvedPort := receive.Resolved.Name
	before := s.stp.PortInfo(resolvedPort)

	bpdu, tlvVID, err := stp.DecodeSSTP(f)
	if err != nil {
		if mutate {
			s.stp.BadBPDU(resolvedPort)
		}
		after := s.stp.PortInfo(resolvedPort)

		reason := stp.ReasonUnsupportedBPDU
		if r, ok := errs.Attributes(err)["reason"].(trace.Reason); ok {
			reason = r
		}

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  reason,
				Steps: []trace.Step{{
					Layer:   port.LayerStp,
					Op:      trace.OpDrop,
					RuleID:  trace.RuleID("stp.sstp." + string(reason)),
					Subject: trace.Subject{Kind: "port", Key: resolvedPort},
					Inputs:  []trace.Fact{stp.BPDUDecodeFact(f, false, reason)},
					Outputs: []trace.Fact{stp.PortTransitionFact(resolvedPort, "bad-bpdu", before, after)},
				}},
			},
			Ingress: resolvedPort,
		}
	}

	arrivalVID := s.untaggedVID(resolvedPort)
	if len(f.Tags) > 0 && f.Tags[0].VID != 0 {
		arrivalVID = f.Tags[0].VID
	}

	if s.cfg.Bridge.VLAN != nil {
		sw, ok := s.cfg.Bridge.VLAN.Switchports[resolvedPort]
		if !ok || !sw.CarriesVID(arrivalVID) {
			if mutate {
				s.stp.BadBPDU(resolvedPort)
			}
			after := s.stp.PortInfo(resolvedPort)

			return bridge.Result{
				Trace: trace.Trace{
					Outcome: trace.Dropped,
					Reason:  stp.ReasonUnsupportedBPDU,
					Steps: []trace.Step{{
						Layer:   port.LayerStp,
						Op:      trace.OpDrop,
						RuleID:  trace.RuleID("stp.sstp." + string(stp.ReasonUnsupportedBPDU)),
						Subject: trace.Subject{Kind: "port", Key: resolvedPort},
						Inputs:  []trace.Fact{stp.BPDUDecodeFact(f, false, stp.ReasonUnsupportedBPDU)},
						Outputs: []trace.Fact{stp.PortTransitionFact(resolvedPort, "bad-bpdu", before, after)},
					}},
				},
				Ingress: resolvedPort,
			}
		}
	}

	before = s.stp.VLANPortInfo(arrivalVID, resolvedPort)
	if mutate {
		s.applySTPEffects(s.stp.ReceiveSSTP(now, resolvedPort, arrivalVID, tlvVID, bpdu))
	}
	after := s.stp.VLANPortInfo(arrivalVID, resolvedPort)

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{{
				Layer:   port.LayerStp,
				Op:      trace.OpClassify,
				RuleID:  "stp.sstp.admit",
				Subject: trace.Subject{Kind: "port", Key: resolvedPort},
				Inputs: []trace.Fact{
					stp.BPDUDecodeFact(f, true, ""),
					sstpVLANFact(tlvVID, arrivalVID),
				},
				Outputs: []trace.Fact{stp.BPDUDecisionFact(bpdu, before, after)},
			}},
		},
		Ingress: resolvedPort,
		FID:     arrivalVID,
	}
}

// sstpVLANFact records the two VLANs an SSTP BPDU is judged against: the one
// its trailing TLV names and the one the bridge classified the frame into. A
// trace that carries both is what makes a PVID inconsistency readable, since
// the disagreement between them is the whole finding.
func sstpVLANFact(tlvVID, arrivalVID vlan.ID) trace.Fact {
	return runtimeFact{
		typeID:    "stp.sstp.vlans",
		canonical: fmt.Sprintf("tlv=%d,arrival=%d,consistent=%t", tlvVID, arrivalVID, tlvVID == arrivalVID),
	}
}

// Start initializes the protocol layers with the current link state of every port in the table.
// On a switch without spanning tree, link aggregation, or loop protection, Start is a no-op.
func (s *Switch) Start(now time.Time) {
	if s.stp == nil && s.lag == nil && s.loopprotect == nil {
		return
	}
	if s.portP2P == nil {
		s.portP2P = make(map[string]PointToPoint)
	}
	if s.portSpeed == nil {
		s.portSpeed = make(map[string]uint64)
	}

	for _, p := range s.ports.Ports() {
		if p.LagParent != "" {
			speed := s.linkSpeed(p)
			s.portP2P[p.Name] = PointToPointTrue
			s.portSpeed[p.Name] = speed
			if s.lag != nil {
				fx := s.lag.LinkChange(now, p.Name, p.Forwards())
				s.applyLAGEffects(now, fx)
			}
		}
	}
	for _, p := range s.ports.Ports() {
		if p.Kind == port.Lag {
			s.updateLagState(now, p.Name)
		}
	}

	for _, p := range s.ports.Ports() {
		if p.LagParent != "" || p.Kind == port.Lag {
			continue
		}
		p2p := true
		if s.cfg.STP != nil {
			if pCfg, ok := s.cfg.STP.Ports[p.Name]; ok {
				if pCfg.PointToPoint == stp.PointToPointForceFalse {
					p2p = false
				}
			}
		}
		speed := s.linkSpeed(p)
		s.portP2P[p.Name] = PointToPointFalse
		if p2p {
			s.portP2P[p.Name] = PointToPointTrue
		}
		s.portSpeed[p.Name] = speed

		if s.stp != nil {
			fx := s.stp.LinkChange(now, p.Name, p.Forwards(), p2p, speed)
			s.applySTPEffects(fx)
		}
		if s.loopprotect != nil {
			fx := s.loopprotect.LinkChange(now, p.Name, p.Forwards())
			s.applyLoopProtectEffects(fx)
		}
	}
	s.recomputeProtocolLinkIssues()
}

// linkSpeed is the resolved speed of a port, or of a LAG's fastest member,
// since a LAG has no physical layer of its own.
func (s *Switch) linkSpeed(p port.Port) uint64 {
	if s.speeds == nil {
		return 0
	}
	if p.Kind != port.Lag {
		return s.speeds[p.Name].SpeedBPS
	}
	var best uint64
	for _, m := range s.ports.Members(p.Name) {
		best = max(best, s.speeds[m.Name].SpeedBPS)
	}

	return best
}

func (s *Switch) applySTPEffects(fx stp.Effects) {
	if len(fx.Flush) > 0 && s.bridge != nil {
		targets := make([]bridge.FlushTarget, len(fx.Flush))
		for i, t := range fx.Flush {
			targets[i] = bridge.FlushTarget{Port: t.Port, FIDs: t.FIDs}
		}
		s.bridge.Flush(targets)
	}
	for _, em := range fx.Emissions {
		// A zero VID is a frame the layer built whole: an IEEE-addressed BPDU
		// rides the wire untagged and unchecked against VLAN membership, on a
		// trunk with no native VLAN as much as anywhere else. A non-zero VID
		// is a per-VLAN BPDU, which leaves exactly as any other frame the
		// switch originates on that VLAN would: tagged where the VLAN is
		// tagged, untagged where it is the port's untagged VLAN, and not at
		// all where the port does not carry it.
		if em.VID == 0 {
			s.emissions = append(s.emissions, Emission{Port: em.Port, Frame: em.Frame})

			continue
		}
		egress, ok := s.bridge.OriginateFrame(em.Port, em.VID, em.Frame)
		if !ok {
			continue
		}
		s.emissions = append(s.emissions, Emission{Port: em.Port, Frame: egress})
	}
}

// applyLoopProtectEffects puts the layer's probes on the wire and flushes the
// bridge's learned entries on the ports fx.Flush names, mirroring
// applySTPEffects's own flush. A probe leaves only where an ordinary frame
// would: the port has to be operationally forwarding, and a spanning tree
// running on the same switch has to forward the VLAN over it, so a port the
// tree already holds discarding cannot report a loop the tree has broken.
// What the loop-protection layer itself says about the port is deliberately
// not consulted, which is what lets a blocked port keep probing and a
// LoopCleared recovery see the loop persist.
func (s *Switch) applyLoopProtectEffects(fx loopprotect.Effects) {
	if len(fx.Flush) > 0 && s.bridge != nil {
		targets := make([]bridge.FlushTarget, len(fx.Flush))
		for i, t := range fx.Flush {
			targets[i] = bridge.FlushTarget{Port: t.Port, FIDs: t.FIDs}
		}
		s.bridge.Flush(targets)
	}
	for _, em := range fx.Emissions {
		p, ok := s.ports.Port(em.Port)
		if !ok || !p.Forwards() {
			continue
		}

		vid := em.VID
		if vid == 0 {
			vid = s.untaggedVID(em.Port)
		}
		if s.stp != nil && !s.stp.Forwards(em.Port, vid) {
			continue
		}

		// The payload must name the VLAN the probe actually rides. em.VID is
		// 0 for a port with no configured VLANs, encoded that way because
		// the layer does not know the port's PVID; now that vid is resolved,
		// re-encode so the wire frame agrees with what the switch classifies
		// a returning copy into, instead of leaving the payload at VID 0 and
		// reporting a false inter-VLAN loop when it returns.
		probe := em.Probe
		probe.VID = vid
		frame := loopprotect.Encode(probe, probe.OriginMAC)

		egress, ok := s.bridge.OriginateFrame(em.Port, vid, frame)
		if !ok {
			continue
		}
		s.emissions = append(s.emissions, Emission{Port: em.Port, Frame: egress})
	}
}

// untaggedVID is the VLAN a frame the switch originates on the named port
// carries when the probe did not name one: the port's PVID on a VLAN-aware
// bridge, falling back to the port's tunnel VID when it has no PVID, and
// VLAN 0 on a bridge with no VLAN configuration, which is the single
// forwarding domain the relay uses there.
func (s *Switch) untaggedVID(name string) vlan.ID {
	if s.cfg.Bridge == nil || s.cfg.Bridge.VLAN == nil {
		return 0
	}
	sw, ok := s.cfg.Bridge.VLAN.Switchports[name]
	if !ok {
		return 0
	}
	if sw.PVID != nil {
		return *sw.PVID
	}
	if sw.Tunnel != nil {
		return sw.Tunnel.VID
	}

	return 0
}

// sameUntaggedDomain reports whether the named port is an untagged member of
// both sent and classified, so a loop-protection probe crossing those two
// VLANs on that port says nothing about the VLANs being joined. IEEE
// 802.1Q makes untagged egress a per-VLAN port set (dot1qVlanStaticUntaggedPorts)
// while ingress classification is a per-port PVID scalar
// (dot1qPvid), so a port can legitimately untag on egress for several VLANs
// while classifying ingress into only one of them (vendors call this
// "asymmetric VLAN," used for a shared uplink to many tenant VLANs). An
// untagged frame carries no VLAN tag at all, so the VID the probe recorded
// and the VID it classified into are both local bookkeeping — this port's
// own choices, not evidence of anything on the wire joining the two VLANs.
// A bridge with no VLAN configuration has no switchports, so the question
// does not arise there.
func (s *Switch) sameUntaggedDomain(name string, sent, classified vlan.ID) bool {
	if s.cfg.Bridge == nil || s.cfg.Bridge.VLAN == nil {
		return false
	}
	sw, ok := s.cfg.Bridge.VLAN.Switchports[name]
	if !ok {
		return false
	}

	return slices.Contains(sw.Untagged, sent) && slices.Contains(sw.Untagged, classified)
}

func (s *Switch) applyLAGEffects(now time.Time, fx lag.Effects) {
	for _, em := range fx.Emissions {
		s.emissions = append(s.emissions, Emission(em))
	}
	for _, lagName := range fx.Changed {
		s.updateLagState(now, lagName)
	}
}

func (s *Switch) updateLagState(now time.Time, lagName string) {
	// The row follows the members' own rows, not the layer's delayed
	// link: the relay refuses a LAG whose members are all down, and the row
	// must say the same.
	lagOper := aggregateOperStatus(s.ports.Members(lagName))
	s.setOperStatus(lagName, lagOper)

	var enabledMembers []string
	if s.lag != nil {
		info := s.lag.Info(lagName)
		enabledMembers = info.Enabled
	}
	lagUp := len(enabledMembers) > 0

	if s.stp != nil {
		var highestSpeed uint64
		lagP2P := len(enabledMembers) > 0
		for _, memName := range enabledMembers {
			memSpeed := s.portSpeed[memName]
			if memSpeed == 0 && s.speeds != nil {
				memSpeed = s.speeds[memName].SpeedBPS
			}
			highestSpeed = max(highestSpeed, memSpeed)

			if p2p, ok := s.portP2P[memName]; ok && p2p != PointToPointTrue {
				lagP2P = false
			}
		}
		if s.cfg.STP != nil && s.cfg.STP.Ports != nil {
			if pCfg, ok := s.cfg.STP.Ports[lagName]; ok && pCfg.PointToPoint == stp.PointToPointForceFalse {
				lagP2P = false
			}
		}
		if s.portP2P != nil {
			s.portP2P[lagName] = PointToPointFalse
			if lagP2P {
				s.portP2P[lagName] = PointToPointTrue
			}
			s.portSpeed[lagName] = highestSpeed
		}
		fxSTP := s.stp.LinkChange(now, lagName, lagUp, lagP2P, highestSpeed)
		s.applySTPEffects(fxSTP)
	}
	if s.loopprotect != nil {
		fx := s.loopprotect.LinkChange(now, lagName, lagUp)
		s.applyLoopProtectEffects(fx)
	}
}

func aggregateOperStatus(members []port.Port) port.LinkState {
	possible := false
	for _, member := range members {
		if member.AdminStatus == port.Up && member.OperStatus == port.Up {
			return port.Up
		}
		if member.AdminStatus != port.Down && member.OperStatus != port.Down {
			possible = true
		}
	}
	if possible {
		return port.Unknown
	}

	return port.Down
}

// Drain returns and clears pending frame emissions produced by the protocol layers.
func (s *Switch) Drain() []Emission {
	em := s.emissions
	s.emissions = nil

	return em
}

// Roles returns the runtime spanning tree status for each configured port,
// or nil if the spanning tree layer is absent.
func (s *Switch) Roles() map[string]stp.PortInfo {
	if s.stp == nil || s.cfg.STP == nil {
		return nil
	}
	roles := make(map[string]stp.PortInfo, len(s.cfg.STP.Ports))
	for name := range s.cfg.STP.Ports {
		roles[name] = s.stp.PortInfo(name)
	}

	return roles
}

// Root returns the elected root bridge identifier, the path cost to reach it,
// and the interface name of the root port, or zero values if the spanning tree
// layer is absent.
func (s *Switch) Root() (stp.BridgeID, uint32, string) {
	if s.stp == nil {
		return stp.BridgeID{}, 0, ""
	}

	return s.stp.Root()
}

// TopologyChanges returns the total count of detected topology changes and the
// timestamp of the most recent change, or zero values if the spanning tree layer is absent.
func (s *Switch) TopologyChanges() (uint64, time.Time) {
	if s.stp == nil {
		return 0, time.Time{}
	}

	return s.stp.TopologyChanges()
}

// BridgeID returns the spanning tree bridge identifier with the priority in
// effect, or a zero value if the spanning tree layer is absent.
func (s *Switch) BridgeID() stp.BridgeID {
	if s.stp == nil {
		return stp.BridgeID{}
	}

	return s.stp.BridgeID()
}

// Times returns the spanning tree max age, hello time, and forward delay in
// force, or zero values if the spanning tree layer is absent.
func (s *Switch) Times() (maxAge, hello, forwardDelay time.Duration) {
	if s.stp == nil {
		return 0, 0, 0
	}

	return s.stp.Times()
}

// Wake advances the protocol layers to now, firing due timers, flushing bridge entries,
// and triggering periodic transmissions.
// On a switch without spanning tree or link aggregation, Wake is a no-op.
func (s *Switch) Wake(now time.Time) {
	if s.stp != nil {
		fx := s.stp.Wake(now)
		s.applySTPEffects(fx)
	}
	if s.lag != nil {
		fx := s.lag.Wake(now)
		s.applyLAGEffects(now, fx)
	}
	if s.loopprotect != nil {
		fx := s.loopprotect.Wake(now)
		s.applyLoopProtectEffects(fx)
	}
}

// NextWake returns the earliest scheduled time at which the switch needs to be woken,
// and reports whether any timer is currently active across spanning tree and link aggregation.
func (s *Switch) NextWake() (time.Time, bool) {
	var (
		earliest time.Time
		hasTimer bool
	)

	update := func(t time.Time, ok bool) {
		if !ok || t.IsZero() {
			return
		}
		if !hasTimer || t.Before(earliest) {
			earliest = t
			hasTimer = true
		}
	}

	if s.stp != nil {
		update(s.stp.NextWake())
	}
	if s.lag != nil {
		update(s.lag.NextWake())
	}
	if s.loopprotect != nil {
		update(s.loopprotect.NextWake())
	}

	return earliest, hasTimer
}

// Mcheck triggers protocol migration checking on the named port, forcing it to
// transmit RSTP BPDUs and restarting the migration delay.
// On a switch without spanning tree configuration, Mcheck is a no-op.
func (s *Switch) Mcheck(now time.Time, port string) {
	if s.stp == nil {
		return
	}
	resolvedPort := port
	if p, ok := s.ports.Resolve(port); ok {
		resolvedPort = p.Name
	}
	fx := s.stp.Mcheck(now, resolvedPort)
	s.applySTPEffects(fx)
}

// ClearLoopProtect manually lifts the loop-protection action applied to the
// named port. It reports whether the port was tracked by the loop-protection
// layer and had an action applied. On a switch without loop-protection
// configuration, ClearLoopProtect is a no-op that reports false.
func (s *Switch) ClearLoopProtect(now time.Time, port string) bool {
	if s.loopprotect == nil {
		return false
	}

	return s.loopprotect.Clear(now, port)
}

// PointToPoint represents the operational point-to-point status of a link.
type PointToPoint string

const (
	// PointToPointUnknown indicates point-to-point status cannot be determined
	// (for example, due to an unknown or unreported duplex).
	PointToPointUnknown PointToPoint = "Unknown"

	// PointToPointTrue indicates the port operates as a point-to-point link.
	PointToPointTrue PointToPoint = "True"

	// PointToPointFalse indicates the port operates as shared media.
	PointToPointFalse PointToPoint = "False"
)

// IssueProtocolLinkUnknown indicates that a protocol layer (such as STP or LAG)
// computed port role, state, or membership while a link operational state or
// point-to-point duplex status was unknown.
const IssueProtocolLinkUnknown analysis.IssueCode = "protocol-link-unknown"

// IssueLAGRebalanceUnmodeled indicates that a forward's LAG selection is a
// balanced-mode bucket assignment old enough that measured-load rebalancing
// could have moved it, which netsim does not model.
const IssueLAGRebalanceUnmodeled analysis.IssueCode = "lag-rebalance-unmodeled"

// IssueMcastQueryUnobserved indicates that a forward's multicast resolution
// depends on a group-specific or group-and-source-specific query that the
// router state tables call for but that was never observed within the last
// member query time, though the VLAN has a router port.
const IssueMcastQueryUnobserved analysis.IssueCode = "mcast-query-unobserved"

// IssuePVSTBoundary indicates that a journey crossed a port where per-VLAN
// spanning tree meets a protocol that runs one tree for many VLANs, so the
// journey's own VLAN has no tree agreed across the link. VLAN 1 is the
// exception and raises nothing: it converges over the IEEE-addressed BPDU
// both sides exchange.
const IssuePVSTBoundary analysis.IssueCode = "stp-pvst-boundary"

// LinkChange notifies the protocol layers of a link transition on the named port.
//
// An invalid operational state records a fault on the switch, readable through
// [Switch.Err]; LinkChange returns nothing, so that is the only channel for it.
func (s *Switch) LinkChange(now time.Time, portName string, state port.LinkState, pointToPoint PointToPoint, speed uint64) {
	if s.portP2P == nil {
		s.portP2P = make(map[string]PointToPoint)
	}
	if s.portSpeed == nil {
		s.portSpeed = make(map[string]uint64)
	}
	// An unset value is a report that says nothing about the link, so it is
	// unknown rather than shared media.
	if pointToPoint == "" {
		pointToPoint = PointToPointUnknown
	}

	up := state == port.Up
	p2p := pointToPoint == PointToPointTrue

	p, ok := s.ports.Port(portName)
	if ok && p.LagParent != "" {
		s.portP2P[portName] = pointToPoint
		s.portSpeed[portName] = speed

		s.setOperStatus(portName, state)

		if s.lag != nil {
			fx := s.lag.LinkChange(now, portName, up)
			s.applyLAGEffects(now, fx)
		}
		s.updateLagState(now, p.LagParent)
		s.recomputeProtocolLinkIssues()
		return
	}

	resolvedPort := portName
	if p, ok := s.ports.Resolve(portName); ok {
		resolvedPort = p.Name
	}
	s.portP2P[resolvedPort] = pointToPoint
	s.portSpeed[resolvedPort] = speed

	s.setOperStatus(resolvedPort, state)

	if s.stp != nil {
		fx := s.stp.LinkChange(now, resolvedPort, up, p2p, speed)
		s.applySTPEffects(fx)
	}
	if s.loopprotect != nil {
		fx := s.loopprotect.LinkChange(now, resolvedPort, up)
		s.applyLoopProtectEffects(fx)
	}
	s.recomputeProtocolLinkIssues()
}

// SelectMember commits an enabled member choice for the named LAG to carry f
// at vid, recording the choice as the layer's new bucket assignment or
// last-active member: a fabric transmission or another real transmission the
// switch makes outside the bridge pipeline. It returns false if the LAG is
// unknown, has no layer, or has no enabled member.
func (s *Switch) SelectMember(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID) (string, bool) {
	sel := s.selectOrPeekMember(now, lagName, f, vid, true)

	return sel.Member, sel.OK
}

// PeekMember computes the same choice SelectMember would make without
// committing it.
func (s *Switch) PeekMember(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID) (string, bool) {
	sel := s.selectOrPeekMember(now, lagName, f, vid, false)

	return sel.Member, sel.OK
}

// selectOrPeekMember chooses a member of the named LAG to carry f at vid,
// committing the choice when commit is true, and records the selection for
// the lag-rebalance-unmodeled issue when it reports that condition.
func (s *Switch) selectOrPeekMember(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID, commit bool) lag.Selection {
	if s.lag == nil {
		return lag.Selection{}
	}

	var sel lag.Selection
	if commit {
		sel = s.lag.Select(now, lagName, f, vid)
	} else {
		sel = s.lag.Peek(now, lagName, f, vid)
	}
	if sel.OK {
		s.recordLAGSelection(lagName, sel)
	}

	return sel
}

// selectMemberWithFact behaves as selectOrPeekMember, additionally returning
// the semantic fact describing the selection for a caller building its own
// trace step, such as hub flooding or routed LAG egress.
func (s *Switch) selectMemberWithFact(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID, commit bool) (lag.Selection, trace.Fact) {
	sel := s.selectOrPeekMember(now, lagName, f, vid, commit)
	if s.lag == nil {
		return sel, nil
	}

	return sel, s.lag.SelectionFact(lagName, f, vid, sel)
}

// recordLAGSelection notes that the forward or peek call in progress used
// lagName's selection sel, so wrapResult can raise lag-rebalance-unmodeled
// for that LAG when sel reports the condition.
func (s *Switch) recordLAGSelection(lagName string, sel lag.Selection) {
	if !sel.RebalanceUnmodeled {
		return
	}
	if s.lagRebalanceHits == nil {
		s.lagRebalanceHits = make(map[string]lag.Selection)
	}
	s.lagRebalanceHits[lagName] = sel
}

// recordMcastQueryUnobserved notes that the forward or peek call in progress
// resolved (vid, group) while its expected query had gone unobserved, so
// wrapResult can raise mcast-query-unobserved for that group.
func (s *Switch) recordMcastQueryUnobserved(vid vlan.ID, group netip.Addr) {
	s.mcastQueryUnobservedHits = append(s.mcastQueryUnobservedHits, mcastQueryUnobservedHit{vid: vid, group: group})
}

// lagSelector adapts a switch's LAG layer to [bridge.Selector] and
// [bridge.semanticSelector], recording each selection so forward metadata can
// raise the rebalance-unmodeled issue for the LAGs a journey actually used.
type lagSelector struct {
	sw *Switch
}

func (a lagSelector) Select(now time.Time, lagName string, commit bool, f ethernet.Frame, vid vlan.ID) bridge.Selection {
	sel := a.sw.selectOrPeekMember(now, lagName, f, vid, commit)

	return bridge.Selection{
		Member:             sel.Member,
		OK:                 sel.OK,
		Bucket:             sel.Bucket,
		Prior:              sel.Prior,
		Cause:              string(sel.Cause),
		RebalanceUnmodeled: sel.RebalanceUnmodeled,
	}
}

func (a lagSelector) SelectionFact(lagName string, f ethernet.Frame, vid vlan.ID, sel bridge.Selection) trace.Fact {
	return a.sw.lag.SelectionFact(lagName, f, vid, lag.Selection{
		Member:             sel.Member,
		OK:                 sel.OK,
		Bucket:             sel.Bucket,
		Prior:              sel.Prior,
		Cause:              lag.SelectionCause(sel.Cause),
		RebalanceUnmodeled: sel.RebalanceUnmodeled,
	})
}

// LagInfo returns the runtime aggregation status of the named LAG,
// or a zero-value Info if the aggregation layer is absent.
func (s *Switch) LagInfo(lagName string) lag.Info {
	if s.lag == nil {
		return lag.Info{}
	}

	return s.lag.Info(lagName)
}

// MemberInfo returns the runtime aggregation status of the named member port,
// or a zero-value MemberInfo if the aggregation layer is absent.
func (s *Switch) MemberInfo(member string) lag.MemberInfo {
	if s.lag == nil {
		return lag.MemberInfo{}
	}

	return s.lag.PortInfo(member)
}

// SetOperStatus updates the operational link state of the named port in the
// switch's port table and the relay's. It tells the spanning tree layer
// nothing: only the caller knows whether a member's change moves its LAG or
// what the link's speed and kind are, so the caller follows with LinkChange.
// SetOperStatus returns a structured validation error when state is outside
// the [port.LinkState] domain and leaves both tables unchanged.
func (s *Switch) SetOperStatus(portName string, state port.LinkState) error {
	if err := validateOperStatus(portName, state); err != nil {
		return err
	}

	builder := port.NewBuilder()
	for _, p := range s.ports.Ports() {
		if p.Name == portName {
			p.OperStatus = state
		}
		builder.Add(p)
	}
	tbl, err := builder.Build()
	if err != nil {
		return err
	}

	if s.bridge != nil {
		if err := s.bridge.SetOperStatus(portName, state); err != nil {
			return err
		}
	}
	s.ports = tbl
	s.recomputeProtocolLinkIssues()

	return nil
}

// setOperStatus applies an oper-status change on the forward path and records
// the first failure on the sticky [Switch.operErr] instead of panicking. Its
// callers — updateLagState and LinkChange — run under the switch's core
// forwarding API (Forward, Peek), which returns no error, so a rejected oper
// status (a caller bug, not a state a topology can express) is surfaced through
// [Switch.Err] rather than taking the process down.
func (s *Switch) setOperStatus(portName string, state port.LinkState) {
	if err := s.SetOperStatus(portName, state); err != nil {
		s.recordOperFault(err)
	}
}

// recordOperFault keeps the first oper-status fault. Later faults are dropped
// so [Switch.Err] reports the one that started the trouble.
func (s *Switch) recordOperFault(err error) {
	if s.operErr == nil {
		s.operErr = err
	}
}

// Err reports the first oper-status fault [Switch.LinkChange] or
// [Switch.updateLagState] recorded, or nil if none occurred. A caller that
// drives link transitions reads it to learn that a transition named an invalid
// operational state, which those methods cannot return directly.
func (s *Switch) Err() error {
	return s.operErr
}

func validateOperStatus(portName string, state port.LinkState) error {
	switch state {
	case "", port.Unknown, port.Up, port.Down:
		return nil
	default:
		return errs.New().
			Attr("field", "ports."+portName+".oper_status").
			Attr("name", portName).
			Attr("oper_status", state).
			Msgf("port %q has invalid oper status %q", portName, state)
	}
}

func (s *Switch) recomputeProtocolLinkIssues() {
	s.protocolIssues = make(map[string]analysis.Issue)

	if s.ports.Len() > 0 {
		for _, p := range s.ports.Ports() {
			if p.Kind != port.Lag {
				continue
			}
			lagName := p.Name
			members := s.ports.Members(lagName)
			hasUnknownMember := false
			for _, m := range members {
				if m.OperStatus == port.Unknown {
					hasUnknownMember = true
					break
				}
			}
			if hasUnknownMember {
				s.protocolIssues[lagName] = analysis.Issue{
					Code:    IssueProtocolLinkUnknown,
					Status:  analysis.Incomplete,
					Scope:   analysis.PortScope(s.nodeID, lagName),
					Message: fmt.Sprintf("protocol layer computed LAG %q with unknown member link", lagName),
				}
				for _, m := range members {
					s.protocolIssues[m.Name] = analysis.Issue{
						Code:    IssueProtocolLinkUnknown,
						Status:  analysis.Incomplete,
						Scope:   analysis.PortScope(s.nodeID, m.Name),
						Message: fmt.Sprintf("protocol layer computed LAG %q member %q with unknown link", lagName, m.Name),
					}
				}
			}
		}
	}

	if s.stp == nil {
		return
	}
	// Spanning tree runs on every port that is not a LAG member, configured
	// or not, so one unknown input among them can change any of their roles.
	var stpPorts []string
	stpAffected := false
	for _, p := range s.ports.Ports() {
		if p.LagParent != "" {
			continue
		}
		stpPorts = append(stpPorts, p.Name)
		if p.OperStatus == port.Unknown {
			stpAffected = true
			continue
		}
		forced := false
		if s.cfg.STP != nil {
			if pCfg, ok := s.cfg.STP.Ports[p.Name]; ok {
				forced = pCfg.PointToPoint == stp.PointToPointForceTrue || pCfg.PointToPoint == stp.PointToPointForceFalse
			}
		}
		if p.OperStatus == port.Up && !forced && s.portP2P[p.Name] == PointToPointUnknown {
			stpAffected = true
		}
	}
	if !stpAffected {
		return
	}
	for _, name := range stpPorts {
		s.protocolIssues[name] = analysis.Issue{
			Code:    IssueProtocolLinkUnknown,
			Status:  analysis.Incomplete,
			Scope:   analysis.PortScope(s.nodeID, name),
			Message: fmt.Sprintf("protocol layer computed spanning tree port %q with unknown link state or duplex", name),
		}
	}
}
