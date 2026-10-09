package vswitch

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Child frames isolate their issues while returning their output to the parent.
type call struct {
	hold                     routing.HoldToken
	emissions                []emission
	copies                   []mirrorCopy
	drops                    []neighborDrop
	now                      time.Time
	commit                   bool
	lagRebalanceHits         map[string]lag.Selection
	mcastQueryUnobservedHits []mcastQueryUnobservedHit
	neighborUnresolvedHits   []neighborUnresolvedHit
	pvstBoundaryHits         map[pvstBoundaryHit]struct{}
}

type mirrorCopy struct {
	traffic.Copy
	Hold routing.HoldToken
}

func (s *Switch) beginCall(now time.Time, commit bool) (*call, func()) {
	parent := s.call
	c := &call{now: now, commit: commit}
	s.call = c
	return c, func() {
		s.call = parent
		if !c.commit {
			return
		}
		if parent != nil {
			parent.emissions = append(parent.emissions, c.emissions...)
			parent.copies = append(parent.copies, c.copies...)
			parent.drops = append(parent.drops, c.drops...)
		}
	}
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
	now   time.Time
}

// neighborUnresolvedHit names one interface and address a forward or peek
// call in progress found pending: a next hop with no resolved link-layer
// address yet, distinct from a definite miss.
type neighborUnresolvedHit struct {
	vrf   string
	iface string
	addr  netip.Addr
}

func (s *Switch) wrapResult(c *call, res bridge.Result) forwardResult {
	var issues []runtimeIssue
	consultedScopes := res.ConsultedScopes()

	for _, p := range res.ConsultedPorts() {
		scope := analysis.PortScope(s.nodeID, p.Name)
		consultedScopes = append(consultedScopes, scope)
		if p.AdminStatus == port.Unknown || p.OperStatus == port.Unknown {
			issues = append(issues, runtimeIssue{
				issue: analysis.Issue{
					Code:    IssueUnknownOperationalStatus,
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
				Code:    IssueForwardingDependencyOutsideLoadedScope,
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
	issues = append(issues, s.lagRebalanceIssues(c)...)
	issues = append(issues, s.mcastQueryUnobservedIssues(c)...)
	issues = append(issues, s.neighborUnresolvedIssues(c)...)
	issues = append(issues, s.pvstBoundaryIssues(c)...)

	return forwardResult{
		Result:   res,
		Metadata: forwardingMetadata(s.nodeID, s.metadata, consultedScopes, issues),
	}
}

// lagRebalanceIssues raises lag-rebalance-unmodeled for each LAG the forward
// or peek call in progress actually used with a balanced selection old enough
// that unmodeled rebalancing could have moved it.
func (s *Switch) lagRebalanceIssues(c *call) []runtimeIssue {
	if len(c.lagRebalanceHits) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.lagRebalanceHits))
	for name := range c.lagRebalanceHits {
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
func (s *Switch) mcastQueryUnobservedIssues(c *call) []runtimeIssue {
	if len(c.mcastQueryUnobservedHits) == 0 {
		return nil
	}
	seen := make(map[mcastQueryUnobservedHit]struct{}, len(c.mcastQueryUnobservedHits))
	issues := make([]runtimeIssue, 0, len(c.mcastQueryUnobservedHits))
	for _, hit := range c.mcastQueryUnobservedHits {
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
			facts: s.mcastConstructionEvidence(hit.vid, hit.now),
		})
	}
	slices.SortFunc(issues, func(a, b runtimeIssue) int { return a.issue.Scope.Compare(b.issue.Scope) })

	return issues
}

// neighborUnresolvedIssues raises neighbor-unresolved for each interface and
// address a forward or peek call in progress found pending: a next hop
// netsim never asked about, as opposed to one it knows has no answer. The
// hit mechanism mirrors mcastQueryUnobservedIssues above.
func (s *Switch) neighborUnresolvedIssues(c *call) []runtimeIssue {
	if len(c.neighborUnresolvedHits) == 0 {
		return nil
	}
	seen := make(map[neighborUnresolvedHit]struct{}, len(c.neighborUnresolvedHits))
	issues := make([]runtimeIssue, 0, len(c.neighborUnresolvedHits))
	for _, hit := range c.neighborUnresolvedHits {
		if _, ok := seen[hit]; ok {
			continue
		}
		seen[hit] = struct{}{}
		issues = append(issues, runtimeIssue{
			issue: analysis.Issue{
				Code:    IssueNeighborUnresolved,
				Status:  analysis.Incomplete,
				Scope:   routing.NeighborLookupScope(s.nodeID, hit.vrf, hit.iface, hit.addr),
				Message: fmt.Sprintf("neighbor %s on interface %q has not been resolved", hit.addr, hit.iface),
			},
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
		if s.call.pvstBoundaryHits == nil {
			s.call.pvstBoundaryHits = make(map[pvstBoundaryHit]struct{})
		}
		s.call.pvstBoundaryHits[pvstBoundaryHit{port: name, vid: res.FID}] = struct{}{}
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
func (s *Switch) pvstBoundaryIssues(c *call) []runtimeIssue {
	if len(c.pvstBoundaryHits) == 0 {
		return nil
	}

	hits := make([]pvstBoundaryHit, 0, len(c.pvstBoundaryHits))
	for hit := range c.pvstBoundaryHits {
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
	return analysis.ProtocolScope(nodeID, string(stp.LayerName), fmt.Sprintf("%s/%d", portName, vid))
}

func (s *Switch) mcastConstructionEvidence(vid vlan.ID, now time.Time) []trace.Fact {
	if s.mcast == nil {
		return nil
	}
	var evidence []trace.Fact
	for _, r := range s.mcast.RouterPorts(vid, now) {
		if p, ok := s.ports.Port(r.Port); ok {
			evidence = append(evidence, port.ForwardingFact(r.Port, p, p.Forwards(), ""))
		}
	}

	return evidence
}

// mcastGroupScope names the analysis scope for one multicast group's
// forwarding state on a VLAN.
func mcastGroupScope(nodeID string, vid vlan.ID, group netip.Addr) analysis.Scope {
	return analysis.ProtocolScope(nodeID, string(mcast.LayerName), fmt.Sprintf("%d/%s", vid, group))
}

// IssueUnknownOperationalStatus indicates that a consulted port has an unknown
// administrative or operational state.
const IssueUnknownOperationalStatus analysis.IssueCode = "unknown-operational-status"

// IssueForwardingDependencyOutsideLoadedScope indicates that a result consulted
// a dependency outside the switch's loaded analysis scope.
const IssueForwardingDependencyOutsideLoadedScope analysis.IssueCode = "forwarding-dependency-outside-loaded-scope"

// IssuePoEDemandUnknown indicates that a port's Power over Ethernet demand is
// unknown, so its allocation cannot be determined.
const IssuePoEDemandUnknown analysis.IssueCode = "poe-demand-unknown"

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

// IssueNeighborUnresolved indicates that a forward or peek's routing
// resolution depends on a next hop's neighbor entry that is newly or still
// Incomplete: netsim has not observed an answer yet, which is not the same
// claim as [routing.ReasonNeighborMiss]'s definite failure.
const IssueNeighborUnresolved analysis.IssueCode = "neighbor-unresolved"

// IssuePVSTBoundary indicates that a journey crossed a port where per-VLAN
// spanning tree meets a protocol that runs one tree for many VLANs, so the
// journey's own VLAN has no tree agreed across the link. VLAN 1 is the
// exception and raises nothing: it converges over the IEEE-addressed BPDU
// both sides exchange.
const IssuePVSTBoundary analysis.IssueCode = "stp-pvst-boundary"

// recordLAGSelection notes that the forward or peek call in progress used
// lagName's selection sel, so wrapResult can raise lag-rebalance-unmodeled
// for that LAG when sel reports the condition.
func (s *Switch) recordLAGSelection(lagName string, sel lag.Selection) {
	if !sel.RebalanceUnmodeled {
		return
	}
	if s.call.lagRebalanceHits == nil {
		s.call.lagRebalanceHits = make(map[string]lag.Selection)
	}
	s.call.lagRebalanceHits[lagName] = sel
}

// recordMcastQueryUnobserved notes that the forward or peek call in progress
// resolved (vid, group) while its expected query had gone unobserved, so
// wrapResult can raise mcast-query-unobserved for that group.
func (s *Switch) recordMcastQueryUnobserved(vid vlan.ID, group netip.Addr, now time.Time) {
	s.call.mcastQueryUnobservedHits = append(s.call.mcastQueryUnobservedHits, mcastQueryUnobservedHit{vid: vid, group: group, now: now})
}

// recordNeighborUnresolved notes that the forward or peek call in progress
// resolved (vrf, iface, addr) as pending, so wrapResult can raise
// neighbor-unresolved for that lookup.
func (s *Switch) recordNeighborUnresolved(vrf, iface string, addr netip.Addr) {
	s.call.neighborUnresolvedHits = append(s.call.neighborUnresolvedHits, neighborUnresolvedHit{vrf: vrf, iface: iface, addr: addr})
}

func (s *Switch) recomputeProtocolLinkIssues() {
	s.protocolIssues = make(map[string]analysis.Issue)

	if s.ports.Len() > 0 {
		for _, p := range s.ports.Ports() {
			if p.Kind != port.LAG {
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
	var stpPorts []string
	stpAffected := false
	for name, pCfg := range s.cfg.STP.Ports {
		p, _ := s.ports.Port(name)
		stpPorts = append(stpPorts, p.Name)
		if p.OperStatus == port.Unknown {
			stpAffected = true
			continue
		}
		forced := pCfg.PointToPoint == stp.PointToPointForceTrue || pCfg.PointToPoint == stp.PointToPointForceFalse
		if p.OperStatus == port.Up && !forced && s.portP2P[p.Name] == device.PointToPointUnknown {
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
