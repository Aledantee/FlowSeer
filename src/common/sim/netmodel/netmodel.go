package netmodel

import (
	"time"

	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Evidence kinds used by netmodel.
const (
	EvidenceKindConfig   analysis.EvidenceKind = "netmodel.config"
	EvidenceKindState    analysis.EvidenceKind = "netmodel.state"
	EvidenceKindDefault  analysis.EvidenceKind = "netmodel.default"
	EvidenceKindSkipped  analysis.EvidenceKind = "netmodel.skipped"
	EvidenceKindConflict analysis.EvidenceKind = "netmodel.conflict"
)

// Load translates typed network model interfaces, VLANs, FDB entries, PoE budgets,
// spanning tree states, link aggregation and LACP states, interface addresses, and
// neighbor entries into a virtual switch construction specification, loading report,
// and analysis trust metadata.
//
// Source context src provides caller-owned origin and context for evidence references
// without importing transport provenance types.
//
// If want is empty, the capability set is inferred from the facets and row kinds present:
// relay is always included, vlan if any interface carries a switchport facet, ethernet if
// any carries an Ethernet facet, poe if any carries a PoE facet or budget, lag if any
// interface is an aggregation, stp if bridge state is present, and routing if any interface
// carries an IP facet. If want is non-empty, only the requested layers are built, and any
// present facet outside want is omitted and recorded in [Report.Skipped]. When want contains
// stp, relay is implied. When a routed interface loads, a VLAN interface, a routed port, or
// a sub-interface, vlan and relay are implied so the relay can leave the routed port out.
// When routing is not wanted, every IP facet, address row, and neighbor row is recorded as
// skipped. When routing ends up with no interfaces (every IP facet skipped),
// the routing configuration is left nil and routing is dropped from capabilities with its
// source removed, so [vswitch.Config.Validate] does not refuse an empty VRF.
//
// Load does not assume its caller ran protovalidate: a value the schema itself forbids,
// such as a reserved VLAN ID, is treated the same as any other invalid device report and
// turned into an issue rather than trusted as pre-checked.
//
// Load returns an error only for conditions that prevent constructing a switch at all:
// an empty interface slice, a duplicate or empty interface name, or a LAG parent reference
// that is invalid or refers to a non-LAG interface. All other omissions, defaults, conflicts,
// and uncertainty are captured in the returned [Result] with non-Complete readiness metadata.
func Load(
	now time.Time,
	src SourceContext,
	ifaces []*interfacev1.Interface,
	vlans []*switchingv1.Vlan,
	fdb []*switchingv1.FdbEntry,
	budgets []*phyv1.PseBudget,
	bridgeState *stpv1.BridgeState,
	stpPorts []*stpv1.PortState,
	lacpAggregators []*lacpv1.AggregatorState,
	lacpPorts []*lacpv1.PortState,
	addrs []*ipv1.InterfaceAddress,
	neighbors []*ipv1.NeighborEntry,
	filterSets []*filterv1.FilterRuleSet,
	want []trace.Layer,
) (Result, error) {
	if err := validateInterfaceNames(ifaces); err != nil {
		return Result{}, err
	}
	c := newLoadContext(src)
	want, explicitRequest := c.requestedCapabilities(want)
	ports, err := c.loadPorts(ifaces)
	if err != nil {
		return Result{}, err
	}
	ownership, ifaceByName := prepareOwnership(ifaces, ports)
	summary := summarizeFacets(ifaces, ownership, want, explicitRequest)
	c.selectCapabilities(summary, want, explicitRequest, len(budgets) > 0, bridgeState != nil, len(filterSets) > 0)
	c.reportMissingSTP(bridgeState, stpPorts)
	c.reportUnwantedBridge(ifaces, ownership)
	c.reportUnwantedPHY(ifaces, budgets)
	c.reportUnwantedSTP(bridgeState, stpPorts)
	c.reportUnwantedRouting(ifaces, addrs, neighbors)
	c.reportUnwantedFilter(ifaces, filterSets)
	c.reportNoSwitchport(ifaces)
	cfg := vswitch.Config{Ports: ports}
	cfg.Phy = c.loadPHY(ifaces, budgets)
	cfg.Bridge = c.loadBridge(ifaces, vlans, ownership)
	cfg.STP = c.loadSTP(bridgeState, stpPorts, ports, ownership)
	cfg.LAG = c.loadLAG(ifaces, lacpAggregators, lacpPorts, ports, summary.hasLag)
	var contributions []vlan.ID
	cfg.Routing, contributions = c.loadRouting(ifaces, addrs, neighbors, ifaceByName, ports)
	applyRoutingVLANs(cfg.Bridge, contributions)
	cfg.Filter = c.loadFilter(ifaces, filterSets, ownership, cfg.Routing != nil)
	normCfg := cfg.Normalize()
	if err := normCfg.Validate(); err != nil {
		return Result{}, errs.Wrap(err, "validate switch configuration")
	}

	seeds := c.loadFDB(now, fdb, normCfg)
	return c.finishLoad(normCfg, seeds)
}

func applyRoutingVLANs(bridgeCfg *bridge.Config, contributions []vlan.ID) {
	if bridgeCfg == nil || bridgeCfg.VLAN == nil {
		return
	}
	for _, vid := range contributions {
		if _, exists := bridgeCfg.VLAN.Table[vid]; !exists {
			bridgeCfg.VLAN.Table[vid] = ""
		}
	}
}
