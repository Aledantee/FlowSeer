package netmodel

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
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
// If in.Want is empty, the capability set is inferred from the facets and row kinds present:
// relay is always included, vlan if any interface carries a switchport facet, ethernet if
// any carries an Ethernet facet, poe if any carries a PoE facet or budget, lag if any
// interface is an aggregation, stp if bridge state is present, and routing if any interface
// carries an IP facet. If in.Want is non-empty, only the requested layers are built, and any
// present facet outside in.Want is omitted and recorded in [Report.Skipped]. When in.Want contains
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
func Load(now time.Time, src SourceContext, in Input) (Result, error) {
	if err := validateInterfaceNames(in.Interfaces); err != nil {
		return Result{}, err
	}
	c := newLoadContext(src)
	want, explicitRequest := c.requestedCapabilities(in.Want)
	ports, err := c.loadPorts(in.Interfaces)
	if err != nil {
		return Result{}, err
	}
	ownership, ifaceByName := prepareOwnership(in.Interfaces, ports)
	summary := summarizeFacets(in.Interfaces, ownership, want, explicitRequest)
	c.selectCapabilities(summary, want, explicitRequest, len(in.Budgets) > 0, in.BridgeState != nil, len(in.FilterSets) > 0)
	c.reportMissingSTP(in.BridgeState, in.STPPorts)
	c.reportUnwantedBridge(in.Interfaces, ownership)
	c.reportUnwantedPHY(in.Interfaces, in.Budgets)
	c.reportUnwantedSTP(in.BridgeState, in.STPPorts)
	c.reportUnwantedRouting(in.Interfaces, in.Addresses, in.Neighbors)
	c.reportUnwantedFilter(in.Interfaces, in.FilterSets)
	c.reportNoSwitchport(in.Interfaces)
	cfg := vswitch.Config{Ports: ports}
	cfg.Phy = c.loadPHY(in.Interfaces, in.Budgets)
	cfg.Bridge = c.loadBridge(in.Interfaces, in.VLANs, ownership)
	cfg.STP = c.loadSTP(in.BridgeState, in.STPPorts, ports, ownership)
	cfg.LAG = c.loadLAG(in.Interfaces, in.LACPAggregators, in.LACPPorts, ports, summary.hasLag)
	var contributions []vlan.ID
	cfg.Routing, contributions = c.loadRouting(in.Interfaces, in.Addresses, in.Neighbors, ifaceByName, ports)
	applyRoutingVLANs(cfg.Bridge, contributions)
	cfg.Filter = c.loadFilter(in.Interfaces, in.FilterSets, ownership, cfg.Routing != nil)
	normCfg := cfg.Normalize()
	if err := normCfg.Validate(); err != nil {
		return Result{}, errs.Wrap(err, "validate switch configuration")
	}

	seeds := c.loadFDB(now, in.FDB, normCfg)
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
