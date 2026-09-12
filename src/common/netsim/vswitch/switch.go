package vswitch

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
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
	allNodesAddress = netip.MustParseAddr("ff02::1")
)

// ConstructionSpec holds the normalized configuration and non-configuration
// inputs (such as observed or preloaded FDB seeds) needed to construct an identical [Switch].
type ConstructionSpec struct {
	Config Config
	Seeds  []bridge.Seed
}

// Clone returns an independent deep copy of the construction specification.
func (s ConstructionSpec) Clone() ConstructionSpec {
	cp := ConstructionSpec{
		Config: s.Config.Clone(),
	}
	if len(s.Seeds) > 0 {
		cp.Seeds = slices.Clone(s.Seeds)
	}
	return cp
}

// Equal reports whether two construction specifications are identical.
func (s ConstructionSpec) Equal(other ConstructionSpec) bool {
	if !s.Config.Equal(other.Config) {
		return false
	}
	if len(s.Seeds) != len(other.Seeds) {
		return false
	}
	for i := range s.Seeds {
		if s.Seeds[i] != other.Seeds[i] {
			return false
		}
	}
	return true
}

// ForwardResult combines the Ethernet bridge forwarding outcome with analysis
// trust metadata recording status, issues, and evidence.
type ForwardResult struct {
	bridge.Result
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
	cfg       Config
	ports     port.Table
	bridge    *bridge.Bridge
	speeds    map[string]phy.Resolved
	power     phy.Allocation
	stp       *stp.Layer
	lag       *lag.Layer
	mcast     *mcast.Layer
	routing   *routing.Layer
	traffic   *traffic.Config
	buckets   map[string]*traffic.Bucket
	copies    []traffic.Copy
	emissions []Emission
	portP2P   map[string]bool
	portSpeed map[string]uint64
	seeds     []bridge.Seed
}

// New constructs a [Switch] from the provided configuration, cloning the configuration,
// assigning missing MAC addresses, validating subsystem configurations, and initializing
// each present subsystem. When the base MAC is zero, New selects the first unused local MAC
// address; two standalone switches may pick the same address. A fabric assigns unique
// addresses across nodes. It returns an error if configuration validation fails.
func New(cfg Config) (*Switch, error) {
	return NewWithSpec(ConstructionSpec{Config: cfg})
}

// NewWithSpec constructs a [Switch] from the provided construction specification,
// restoring preloaded forwarding database seeds if any.
func NewWithSpec(spec ConstructionSpec) (*Switch, error) {
	norm := spec.Config.Normalize()
	if err := norm.Validate(); err != nil {
		return nil, err
	}
	return newSwitch(norm, spec.Seeds)
}

func newSwitch(norm Config, seeds []bridge.Seed) (*Switch, error) {
	sw := &Switch{
		cfg:   norm,
		ports: norm.Ports.Clone(),
		seeds: slices.Clone(seeds),
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
		sw.bridge = b
	}
	if norm.Mcast != nil {
		m, err := mcast.New(*norm.Mcast, norm.Ports)
		if err != nil {
			return nil, err
		}
		sw.mcast = m
		if sw.bridge != nil {
			sw.bridge.SetGroupResolver(sw)
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
			sw.bridge.SetSelector(sw.lag)
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
			sw.bridge.SetGate(sw.stp)
		}
	}

	if norm.Routing != nil {
		rt, err := routing.New(*norm.Routing, norm.Ports)
		if err != nil {
			return nil, err
		}
		sw.routing = rt
	}
	if norm.Traffic != nil {
		sw.traffic = norm.Traffic
		sw.buckets = make(map[string]*traffic.Bucket, len(norm.Traffic.Policers))
		for name, policer := range norm.Traffic.Policers {
			sw.buckets[name] = traffic.NewBucket(policer)
		}
	}

	if len(seeds) > 0 && sw.bridge != nil {
		sw.bridge.Learn(seeds)
	}

	return sw, nil
}

// Spec returns a [ConstructionSpec] capturing the normalized configuration and
// preloaded forwarding database seeds needed to reconstruct this switch.
func (s *Switch) Spec() ConstructionSpec {
	spec := ConstructionSpec{
		Config: s.cfg.Clone(),
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

// Copies returns and clears mirror copies produced by the most recent call to
// [Switch.Forward]. Calls to [Switch.Peek] leave pending copies unchanged.
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

// Learn preloads the switch's bridge forwarding database with the provided seeds.
// It records the seeds in the switch's construction specification and is a no-op
// when the switch has no bridge relay.
func (s *Switch) Learn(seeds []bridge.Seed) {
	if len(seeds) > 0 {
		s.seeds = append(s.seeds, slices.Clone(seeds)...)
	}
	if s.bridge == nil {
		return
	}
	s.bridge.Learn(seeds)
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

// Power returns the Power over Ethernet budget distribution and port allocations.
func (s *Switch) Power() phy.Allocation {
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

	return cp
}

// Forward processes an arrival on an ingress port at the given time, updating
// the forwarding database if a bridge relay is present, and returns the forwarding result
// with analysis metadata.
func (s *Switch) Forward(now time.Time, ingress string, f ethernet.Frame) ForwardResult {
	return s.wrapResult(s.forward(now, ingress, f, true), ingress)
}

// Peek processes an arrival on an ingress port at the given time without mutating
// the forwarding database and returns the forwarding result with analysis metadata.
func (s *Switch) Peek(now time.Time, ingress string, f ethernet.Frame) ForwardResult {
	return s.wrapResult(s.forward(now, ingress, f, false), ingress)
}

func (s *Switch) wrapResult(res bridge.Result, ingress string) ForwardResult {
	scope := analysis.PortScope("", ingress)
	var issues []analysis.Issue

	p, ok := s.ports.Port(ingress)
	if ok {
		if p.AdminStatus == port.Unknown || p.OperStatus == port.Unknown {
			issues = append(issues, analysis.Issue{
				Code:    "unknown-operational-status",
				Status:  analysis.Incomplete,
				Scope:   scope,
				Message: fmt.Sprintf("port %q has unknown operational status", ingress),
			})
		} else if p.LagParent != "" {
			if parent, parentOk := s.ports.Port(p.LagParent); parentOk && (parent.AdminStatus == port.Unknown || parent.OperStatus == port.Unknown) {
				issues = append(issues, analysis.Issue{
					Code:    "unknown-operational-status",
					Status:  analysis.Incomplete,
					Scope:   scope,
					Message: fmt.Sprintf("port %q LAG parent %q has unknown operational status", ingress, p.LagParent),
				})
			}
		}
	}

	return ForwardResult{
		Result:   res,
		Metadata: analysis.NewMetadata(scope, issues, analysis.EvidenceCatalog{}, nil),
	}
}

func (s *Switch) forward(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	if mutate {
		s.copies = nil
	}
	if s.isMirrorOutputPort(ingress) {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  traffic.ReasonMirrorOutput,
				Steps: []trace.Step{{
					Layer:   port.LayerTraffic,
					Op:      trace.OpDrop,
					RuleID:  "traffic.mirror.output_drop",
					Subject: trace.Subject{Kind: "port", Key: ingress},
				}},
			},
			Ingress: ingress,
		}
	}

	if s.lag != nil && f.EtherType == ethernet.EtherTypeSlowProtocols && len(f.Payload) > 0 && f.Payload[0] == 1 {
		p, ok := s.ports.Port(ingress)
		if ok && p.LagParent != "" && p.Forwards() {
			return s.finishForward(ingress, f, s.interceptLACP(now, ingress, f, mutate), mutate)
		}
	}

	if s.stp != nil && f.Dst == stpGroupAddress {
		return s.finishForward(ingress, f, s.interceptBPDU(now, ingress, f, mutate), mutate)
	}

	if s.routing != nil {
		resolved, reason := s.ports.Receive(ingress)
		if resolved.Name != "" {
			if iface, ok := s.routing.ByPort(resolved.Name); ok {
				if reason != "" {
					res := bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  port.ReasonPortDown,
							Steps: []trace.Step{
								{
									Layer:   port.LayerRouting,
									Op:      trace.OpDrop,
									RuleID:  "port.status.down",
									Subject: trace.Subject{Kind: "port", Key: resolved.Name},
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
					return s.finishForward(ingress, f, res, mutate)
				}

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
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
					return s.finishForward(ingress, f, res, mutate)
				}

				pcp, dei := framePriority(f)
				routeRes := s.routing.Route(iface, f)
				res := s.assembleRouteResult(resolved.Name, 0, pcp, dei, nil, routeRes)
				return s.finishForward(ingress, f, res, mutate)
			}
		}
	}

	if s.bridge != nil {
		controlCandidate := multicastControlCandidate(f)
		in, res, ok := s.bridge.Ingress(now, ingress, f, mutate && !controlCandidate)
		if !ok {
			return s.finishForward(ingress, f, res, mutate)
		}

		if controlCandidate && s.mcast != nil && s.mcast.Snooped(in.FID) {
			res := s.forwardMulticastControl(now, ingress, f, in, mutate)
			return s.finishForward(ingress, f, res, mutate)
		}

		if s.routing != nil {
			if iface, ok := s.routing.ByVLAN(in.FID); ok && s.routing.Owns(iface, f) {
				if controlCandidate {
					in = s.commitBridgeLearning(now, ingress, f, in, mutate)
				}
				routeRes := s.routing.Route(iface, f)
				res := s.assembleRouteResult(in.Port, in.FID, in.PCP, in.DEI, in.Steps, routeRes)
				return s.finishForward(ingress, f, res, mutate)
			}
		}

		if controlCandidate {
			in = s.commitBridgeLearning(now, ingress, f, in, mutate)
		}

		return s.finishForward(ingress, f, s.bridge.Egress(in, f), mutate)
	}

	return s.finishForward(ingress, f, s.forwardHub(ingress, f), mutate)
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

	learned, _, ok := s.bridge.Ingress(now, ingress, f, true)
	if !ok || len(learned.Steps) <= len(in.Steps) {
		return in
	}
	in.Steps = append(in.Steps, learned.Steps[len(in.Steps):]...)

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
				in.Steps = append(in.Steps, multicastControlStep("mcast.control.unsupported", "igmp"))
				return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
			}

			return badMulticastControl(in)
		}

		in = s.commitBridgeLearning(now, ingress, f, in, mutate)
		in.Steps = append(in.Steps, multicastControlStep("mcast.control.admit", "igmp"))
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
			in.Steps = append(in.Steps, multicastControlStep("mcast.control.unsupported", "mld"))
			return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
		}

		return badMulticastControl(in)
	}

	in = s.commitBridgeLearning(now, ingress, f, in, mutate)
	in.Steps = append(in.Steps, multicastControlStep("mcast.control.admit", "mld"))
	if mutate {
		s.mcast.LearnMLD(now, in.FID, in.Port, hdr.Src, message)
	}
	if message.Type == mld.Query {
		return s.bridge.EgressTo(in, f, s.logicalPorts(), bridge.ReasonNoEgress)
	}

	return s.bridge.EgressTo(in, f, routerPortNames(s.mcast.RouterPorts(in.FID)), mcast.ReasonNoRouterPort)
}

func multicastControlStep(ruleID trace.RuleID, proto string) trace.Step {
	return trace.Step{
		Layer:   port.LayerMcast,
		Op:      trace.OpClassify,
		RuleID:  ruleID,
		Subject: trace.Subject{Kind: "protocol", Key: proto},
	}
}

func badMulticastControl(in bridge.Ingress) bridge.Result {
	steps := slices.Clone(in.Steps)
	steps = append(steps, trace.Step{
		Layer:   port.LayerMcast,
		Op:      trace.OpDrop,
		RuleID:  "mcast.control.bad",
		Subject: trace.Subject{Kind: "port", Key: in.Port},
	})

	return bridge.Result{
		Trace:   trace.Trace{Outcome: trace.Dropped, Reason: mcast.ReasonBadControl, Steps: steps},
		Ingress: in.Port,
		FID:     in.FID,
	}
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

// Resolve selects multicast members and router ports for an eligible IP group frame.
func (s *Switch) Resolve(vid vlan.ID, f ethernet.Frame) ([]string, bool) {
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

	ports, registered := s.mcast.Resolve(vid, hdr.Dst)
	if registered {
		return ports, true
	}
	if s.cfg.Mcast.Floods(vid) {
		return nil, false
	}

	return ports, true
}

func (s *Switch) finishForward(ingress string, received ethernet.Frame, res bridge.Result, mutate bool) bridge.Result {
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
		})
	}
	if reserved && !transmitted {
		res.Outcome = trace.Dropped
		res.Reason = traffic.ReasonMirrorOutput
	}

	if mutate {
		var vlans *bridge.VLAN
		if s.cfg.Bridge != nil {
			vlans = s.cfg.Bridge.VLAN
		}
		resolvedIngress := res.Ingress
		if resolvedIngress == "" {
			resolvedIngress = ingress
		}
		s.copies = traffic.Copies(*s.traffic, vlans, resolvedIngress, res.FID, received, res.Egress)
	}

	return res
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
	ingressPort string,
	ingressFID vlan.ID,
	ingressPCP vlan.PCP,
	ingressDEI bool,
	ingressSteps []trace.Step,
	routeRes routing.Result,
) bridge.Result {
	if routeRes.Reason != "" {
		outcome := trace.Dropped
		if routeRes.Reason == routing.ReasonNotRouted {
			outcome = trace.Consumed
		}
		steps := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps))
		steps = append(steps, ingressSteps...)
		steps = append(steps, routeRes.Steps...)

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: outcome,
				Reason:  routeRes.Reason,
				Steps:   steps,
			},
			Ingress: ingressPort,
			FID:     ingressFID,
		}
	}

	// Route names only an interface of its own table, so the lookup cannot miss.
	egressIface, _ := s.routing.Interface(routeRes.Interface)

	if egressIface.VLAN != 0 {
		stepsSoFar := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps))
		stepsSoFar = append(stepsSoFar, ingressSteps...)
		stepsSoFar = append(stepsSoFar, routeRes.Steps...)

		bridgeIn := bridge.Ingress{
			Port:  "",
			FID:   egressIface.VLAN,
			PCP:   ingressPCP,
			DEI:   ingressDEI,
			Steps: stepsSoFar,
		}
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
		steps = append(steps, trace.Step{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID("port.status." + string(txReason)),
			Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
		})
		return bridge.Result{
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
	}

	p, _ := s.ports.Port(egressIface.Port)
	if p.Kind == port.Lag {
		mem, ok := s.SelectMember(egressIface.Port, routeRes.Frame, 0)
		if !ok {
			steps = append(steps, trace.Step{
				Layer:   port.LayerRouting,
				Op:      trace.OpDrop,
				RuleID:  "lag.egress.no_member",
				Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
			})
			return bridge.Result{
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
		}
		member = mem
	}

	steps = append(steps, trace.Step{
		Layer:   port.LayerRouting,
		Op:      trace.OpTransmit,
		RuleID:  "routing.transmit",
		Subject: trace.Subject{Kind: "port", Key: egressIface.Port},
	})
	return bridge.Result{
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

func (s *Switch) forwardHub(ingress string, f ethernet.Frame) bridge.Result {
	var res bridge.Result
	res.Outcome = trace.Dropped
	res.FID = 0
	pcp, _ := framePriority(f)

	p, ok := s.ports.Port(ingress)
	if !ok {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpDrop,
			RuleID:  "port.status.not_found",
			Subject: trace.Subject{Kind: "port", Key: ingress},
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
		})

		return res
	}
	res.Ingress = resolved.Name

	if !p.Forwards() || !resolved.Forwards() {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpDrop,
			RuleID:  "port.status.down",
			Subject: trace.Subject{Kind: "port", Key: ingress},
		})

		return res
	}

	var candidates []port.Port
	for _, cand := range s.ports.Ports() {
		if cand.LagParent != "" || cand.Name == res.Ingress || !cand.Forwards() {
			continue
		}
		candidates = append(candidates, cand)
	}

	if len(candidates) == 0 {
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
			})

			continue
		}

		var member string
		if cand.Kind == port.Lag {
			mem, ok := s.SelectMember(cand.Name, f, 0)
			if !ok {
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
				})

				continue
			}
			member = mem
		}

		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerPort,
			Op:      trace.OpReplicate,
			RuleID:  "port.hub.replicate",
			Subject: trace.Subject{Kind: "port", Key: cand.Name},
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

func (s *Switch) interceptLACP(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	pdu, err := lacp.Decode(f)
	if err != nil {
		if mutate {
			s.lag.BadLACPDU(ingress)
		}

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

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{
				{
					Layer:   port.LayerLag,
					Op:      trace.OpClassify,
					RuleID:  "lag.lacpdu.admit",
					Subject: trace.Subject{Kind: "port", Key: ingress},
				},
			},
		},
		Ingress: ingress,
	}
}

func (s *Switch) interceptBPDU(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	// The relay's first checks apply to a BPDU too: a dead or unknown port
	// received nothing, and a trace saying Consumed there would hide a BPDU
	// that died on a cut cable.
	resolved, reason := s.ports.Receive(ingress)
	if reason != "" {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  port.ReasonPortDown,
				Steps: []trace.Step{{
					Layer:   port.LayerStp,
					Op:      trace.OpDrop,
					RuleID:  "port.status.down",
					Subject: trace.Subject{Kind: "port", Key: resolved.Name},
				}},
			},
			Ingress: resolved.Name,
		}
	}
	resolvedPort := resolved.Name

	bpdu, err := stp.Decode(f)
	if err != nil {
		if mutate {
			s.stp.BadBPDU(resolvedPort)
		}

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

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{
				{
					Layer:   port.LayerStp,
					Op:      trace.OpClassify,
					RuleID:  "stp.bpdu.admit",
					Subject: trace.Subject{Kind: "port", Key: resolvedPort},
				},
			},
		},
		Ingress: resolvedPort,
	}
}

// Start initializes the protocol layers with the current link state of every port in the table.
// On a switch without spanning tree or link aggregation, Start is a no-op.
func (s *Switch) Start(now time.Time) {
	if s.stp == nil && s.lag == nil {
		return
	}
	if s.portP2P == nil {
		s.portP2P = make(map[string]bool)
		s.portSpeed = make(map[string]uint64)
	}

	for _, p := range s.ports.Ports() {
		if p.LagParent != "" {
			speed := s.linkSpeed(p)
			p2p := true
			s.portP2P[p.Name] = p2p
			s.portSpeed[p.Name] = speed
			if s.lag != nil {
				fx := s.lag.LinkChange(now, p.Name, p.Forwards())
				s.applyLAGEffects(now, fx)
			}
			s.updateLagState(now, p.LagParent)
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
		s.portP2P[p.Name] = p2p
		s.portSpeed[p.Name] = speed

		if s.stp != nil {
			fx := s.stp.LinkChange(now, p.Name, p.Forwards(), p2p, speed)
			s.applySTPEffects(fx)
		}
	}
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
		s.bridge.FlushPorts(fx.Flush)
	}
	for _, em := range fx.Emissions {
		s.emissions = append(s.emissions, Emission(em))
	}
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
	anyUp := false
	for _, m := range s.ports.Members(lagName) {
		if m.OperStatus != port.Down {
			anyUp = true
			break
		}
	}
	lagOper := port.Down
	if anyUp {
		lagOper = port.Up
	}
	s.SetOperStatus(lagName, lagOper)

	if s.stp != nil {
		var enabledMembers []string
		if s.lag != nil {
			info := s.lag.Info(lagName)
			enabledMembers = info.Enabled
		}
		lagUp := len(enabledMembers) > 0

		var highestSpeed uint64
		lagP2P := len(enabledMembers) > 0
		for _, memName := range enabledMembers {
			memSpeed := s.portSpeed[memName]
			if memSpeed == 0 && s.speeds != nil {
				memSpeed = s.speeds[memName].SpeedBPS
			}
			highestSpeed = max(highestSpeed, memSpeed)

			memP2P := true
			if p2p, ok := s.portP2P[memName]; ok {
				memP2P = p2p
			}
			if !memP2P {
				lagP2P = false
			}
		}
		if s.cfg.STP != nil && s.cfg.STP.Ports != nil {
			if pCfg, ok := s.cfg.STP.Ports[lagName]; ok && pCfg.PointToPoint == stp.PointToPointForceFalse {
				lagP2P = false
			}
		}
		if s.portP2P != nil {
			s.portP2P[lagName] = lagP2P
			s.portSpeed[lagName] = highestSpeed
		}
		fxSTP := s.stp.LinkChange(now, lagName, lagUp, lagP2P, highestSpeed)
		s.applySTPEffects(fxSTP)
	}
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

// LinkChange notifies the protocol layers of a link transition on the named port.
func (s *Switch) LinkChange(now time.Time, portName string, up, pointToPoint bool, speed uint64) {
	if s.stp == nil && s.lag == nil {
		return
	}
	if s.portP2P == nil {
		s.portP2P = make(map[string]bool)
		s.portSpeed = make(map[string]uint64)
	}

	p, ok := s.ports.Port(portName)
	if ok && p.LagParent != "" {
		s.portP2P[portName] = pointToPoint
		s.portSpeed[portName] = speed

		oper := port.Down
		if up {
			oper = port.Up
		}
		s.SetOperStatus(portName, oper)

		if s.lag != nil {
			fx := s.lag.LinkChange(now, portName, up)
			s.applyLAGEffects(now, fx)
		}
		s.updateLagState(now, p.LagParent)
		return
	}

	resolvedPort := portName
	if p, ok := s.ports.Resolve(portName); ok {
		resolvedPort = p.Name
	}
	s.portP2P[resolvedPort] = pointToPoint
	s.portSpeed[resolvedPort] = speed

	oper := port.Down
	if up {
		oper = port.Up
	}
	s.SetOperStatus(resolvedPort, oper)

	if s.stp != nil {
		fx := s.stp.LinkChange(now, resolvedPort, up, pointToPoint, speed)
		s.applySTPEffects(fx)
	}
}

// SelectMember chooses an enabled member of the named LAG to carry the given frame.
// It returns false if the LAG is unknown, has no layer, or has no enabled member.
func (s *Switch) SelectMember(lagName string, f ethernet.Frame, vid vlan.ID) (string, bool) {
	if s.lag == nil {
		return "", false
	}

	return s.lag.Select(lagName, f, vid)
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
func (s *Switch) SetOperStatus(portName string, state port.LinkState) {
	builder := port.NewBuilder()
	for _, p := range s.ports.Ports() {
		if p.Name == portName {
			p.OperStatus = state
		}
		builder.Add(p)
	}
	// Only OperStatus changed on a table that already validated, so the
	// rebuild cannot fail.
	if tbl, err := builder.Build(); err == nil {
		s.ports = tbl
	}

	if s.bridge != nil {
		s.bridge.SetOperStatus(portName, state)
	}
}
