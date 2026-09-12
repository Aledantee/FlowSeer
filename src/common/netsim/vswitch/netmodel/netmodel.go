package netmodel

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// Evidence kinds used by netmodel.
const (
	EvidenceKindConfig   analysis.EvidenceKind = "netmodel.config"
	EvidenceKindState    analysis.EvidenceKind = "netmodel.state"
	EvidenceKindDefault  analysis.EvidenceKind = "netmodel.default"
	EvidenceKindSkipped  analysis.EvidenceKind = "netmodel.skipped"
	EvidenceKindConflict analysis.EvidenceKind = "netmodel.conflict"
)

// Issue codes used by netmodel.
const (
	IssueMissingAdminStatus       analysis.IssueCode = "netmodel.interface.missing_admin_status"
	IssueInvalidAdminStatus       analysis.IssueCode = "netmodel.interface.invalid_admin_status"
	IssueMissingOperStatus        analysis.IssueCode = "netmodel.interface.missing_oper_status"
	IssueInvalidOperStatus        analysis.IssueCode = "netmodel.interface.invalid_oper_status"
	IssueSkippedLayerNotWanted    analysis.IssueCode = "netmodel.skipped.layer_not_wanted"
	IssueSkippedMissingFacet      analysis.IssueCode = "netmodel.skipped.missing_facet"
	IssueSkippedUnsupportedFacet  analysis.IssueCode = "netmodel.skipped.unsupported_facet"
	IssueSkippedLagMember         analysis.IssueCode = "netmodel.switchport.lag_member"
	IssueSkippedInterfaceRouted   analysis.IssueCode = "netmodel.switchport.interface_routed"
	IssueUnsupportedPowerClass    analysis.IssueCode = "netmodel.poe.unsupported_power_class"
	IssueTunnelWithoutPvid        analysis.IssueCode = "netmodel.switchport.tunnel_without_pvid"
	IssueInvalidBridgePriority    analysis.IssueCode = "netmodel.stp.invalid_bridge_priority"
	IssueMissingBridgeAddress     analysis.IssueCode = "netmodel.stp.missing_bridge_address"
	IssueInvalidTxHoldCount       analysis.IssueCode = "netmodel.stp.invalid_tx_hold_count"
	IssueInvalidPortPriority      analysis.IssueCode = "netmodel.stp.invalid_port_priority"
	IssueUnknownPort              analysis.IssueCode = "netmodel.skipped.unknown_port"
	IssueUnsupportedInterfaceKind analysis.IssueCode = "netmodel.routing.unsupported_interface_kind"
	IssueMissingIPFacet           analysis.IssueCode = "netmodel.routing.missing_ip_facet"
	IssueMissingNeighborMAC       analysis.IssueCode = "netmodel.routing.missing_neighbor_mac"
	IssueInvalidMAC               analysis.IssueCode = "netmodel.address.invalid_mac"
	IssueInvalidIPAddress         analysis.IssueCode = "netmodel.routing.invalid_ip_address"
	IssueInvalidNeighborAddress   analysis.IssueCode = "netmodel.routing.invalid_neighbor_address"
	IssueInvalidPrefix            analysis.IssueCode = "netmodel.routing.invalid_prefix"
	IssueInvalidFDBKind           analysis.IssueCode = "netmodel.fdb.invalid_kind"
	IssueInvalidFDBStatus         analysis.IssueCode = "netmodel.fdb.invalid_status"
	IssueInvalidVlanID            analysis.IssueCode = "netmodel.vlan.invalid_id"
	IssueConflictFDB              analysis.IssueCode = "netmodel.fdb.conflict"
	IssueConflictSTPPort          analysis.IssueCode = "netmodel.stp.conflict"
	IssueConflictLACP             analysis.IssueCode = "netmodel.lacp.conflict"
	IssueConflictBudget           analysis.IssueCode = "netmodel.poe.conflict"
	IssueConflictVlan             analysis.IssueCode = "netmodel.vlan.conflict"
	IssueConflictAddress          analysis.IssueCode = "netmodel.routing.address_conflict"
	IssueConflictNeighbor         analysis.IssueCode = "netmodel.routing.conflict"
)

type factKey struct {
	id      string
	display string
	scope   analysis.Scope
}

type factConflict struct {
	key    factKey
	values []string
}

func resolveFacts[T any](rows []T, identify func(T) (factKey, string, bool)) (map[string]T, []factConflict) {
	keys := make(map[string]factKey)
	groups := make(map[string]map[string]T)
	for _, row := range rows {
		key, value, ok := identify(row)
		if !ok {
			continue
		}
		keys[key.id] = key
		if groups[key.id] == nil {
			groups[key.id] = make(map[string]T)
		}
		groups[key.id][value] = row
	}

	resolved := make(map[string]T)
	var conflicts []factConflict
	for _, id := range sortedKeys(groups) {
		variants := groups[id]
		values := sortedKeys(variants)
		if len(values) == 1 {
			resolved[id] = variants[values[0]]
			continue
		}
		conflicts = append(conflicts, factConflict{key: keys[id], values: values})
	}
	return resolved, conflicts
}

func (c factConflict) detail() string {
	return "reported values disagree: " + strings.Join(c.values, "; ")
}

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
// stp, relay is implied. When a routed interface loads, a VLAN interface or a routed port,
// vlan and relay are implied so the relay can leave the routed port out. When routing is
// not wanted, every IP facet, address row, and neighbor row is recorded as skipped. When routing ends up with no interfaces (every IP facet skipped),
// the routing configuration is left nil and routing is dropped from capabilities with its
// source removed, so [vswitch.Config.Validate] does not refuse an empty VRF.
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
	want []port.Layer,
) (Result, error) {
	if len(ifaces) == 0 {
		return Result{}, errs.New().Msg("interface list cannot be empty")
	}

	var rootScope analysis.Scope
	if src.DeviceID != "" {
		rootScope = analysis.NodeScope(src.DeviceID)
	} else {
		rootScope = analysis.WholeScope()
	}

	portScope := func(portName string) analysis.Scope {
		if src.DeviceID != "" {
			return analysis.PortScope(src.DeviceID, portName)
		}
		return analysis.PortScope("", portName)
	}

	catalog := analysis.EvidenceCatalog{}
	var issues []analysis.Issue
	var assumptions []analysis.Assumption

	addEvidence := func(kind analysis.EvidenceKind, detail string) trace.EvidenceRef {
		origin := src.Origin
		if origin == "" {
			if src.DeviceID != "" {
				origin = src.DeviceID
			} else {
				origin = "netmodel"
			}
		}
		contextStr := src.Context
		if detail != "" {
			if contextStr != "" {
				contextStr = contextStr + "; " + detail
			} else {
				contextStr = detail
			}
		}
		var ref trace.EvidenceRef
		catalog, ref = catalog.Add(analysis.Evidence{
			Kind:    kind,
			Origin:  origin,
			Context: contextStr,
		})
		return ref
	}

	report := Report{
		CapabilitySources: make(map[port.Layer]string),
	}

	addDefault := func(portName, field, value string) {
		var scope analysis.Scope
		if portName != "" {
			scope = portScope(portName)
		} else {
			scope = rootScope
		}
		detail := fmt.Sprintf("default %s=%s", field, value)
		if portName != "" {
			detail = fmt.Sprintf("port %s default %s=%s", portName, field, value)
		}
		ref := addEvidence(EvidenceKindDefault, detail)

		report.Defaults = append(report.Defaults, Default{
			Scope:    scope,
			Port:     portName,
			Field:    field,
			Value:    value,
			Evidence: []trace.EvidenceRef{ref},
		})

		assumptions = append(assumptions, analysis.Assumption{
			Scope:     scope,
			Statement: fmt.Sprintf("default value applied for %s: %s", field, value),
			Evidence:  []trace.EvidenceRef{ref},
		})
	}

	addSkipped := func(portName, what, why string, status analysis.Status, code analysis.IssueCode) {
		var scope analysis.Scope
		if portName != "" {
			scope = portScope(portName)
		} else {
			scope = rootScope
		}
		detail := fmt.Sprintf("skipped %s: %s", what, why)
		if portName != "" {
			detail = fmt.Sprintf("port %s skipped %s: %s", portName, what, why)
		}
		ref := addEvidence(EvidenceKindSkipped, detail)

		report.Skipped = append(report.Skipped, Skipped{
			Scope:    scope,
			Port:     portName,
			What:     what,
			Why:      why,
			Evidence: []trace.EvidenceRef{ref},
		})

		if status > analysis.Complete {
			msg := fmt.Sprintf("%s omitted: %s", what, why)
			if portName != "" {
				msg = fmt.Sprintf("port %q %s omitted: %s", portName, what, why)
			}
			issues = append(issues, analysis.Issue{
				Code:     code,
				Status:   status,
				Scope:    scope,
				Message:  msg,
				Evidence: []trace.EvidenceRef{ref},
			})
		}
	}

	addConflict := func(scope analysis.Scope, what, key, detail string, code analysis.IssueCode) {
		ref := addEvidence(EvidenceKindConflict, fmt.Sprintf("conflict in %s for %s: %s", what, key, detail))

		report.Conflicts = append(report.Conflicts, Conflict{
			Scope:    scope,
			What:     what,
			Key:      key,
			Detail:   detail,
			Evidence: []trace.EvidenceRef{ref},
		})

		issues = append(issues, analysis.Issue{
			Code:     code,
			Status:   analysis.Unstable,
			Scope:    scope,
			Message:  fmt.Sprintf("conflicting %s for %s: %s", what, key, detail),
			Evidence: []trace.EvidenceRef{ref},
		})
	}

	portBuilder := port.NewBuilder()
	for _, iface := range ifaces {
		p := port.Port{
			Name: iface.GetName(),
		}
		if iface.HasIfIndex() {
			p.IfIndex = iface.GetIfIndex()
		}
		switch {
		case iface.GetPhysical() != nil:
			p.Kind = port.Physical
			if iface.GetPhysical().HasLagParent() {
				p.LagParent = iface.GetPhysical().GetLagParent()
			}
		case iface.GetLag() != nil:
			p.Kind = port.Lag
		default:
			p.Kind = port.Other
		}

		if iface.HasMtu() {
			if iface.GetMtu() == 0 {
				addDefault(iface.GetName(), "mtu", "0")
				p.MTU = 0
			} else {
				p.MTU = int(iface.GetMtu())
			}
		}

		switch {
		case !iface.HasAdminStatus() || iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED:
			p.AdminStatus = port.Unknown
			ref := addEvidence(EvidenceKindState, fmt.Sprintf("interface %s admin_status unspecified", iface.GetName()))
			issues = append(issues, analysis.Issue{
				Code:     IssueMissingAdminStatus,
				Status:   analysis.Incomplete,
				Scope:    portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has unspecified or missing administrative status", iface.GetName()),
				Evidence: []trace.EvidenceRef{ref},
			})
		case iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_UP:
			p.AdminStatus = port.Up
		case iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_DOWN ||
			iface.GetAdminStatus() == interfacev1.AdminStatus_ADMIN_STATUS_TESTING:
			p.AdminStatus = port.Down
		default:
			p.AdminStatus = port.Unknown
			ref := addEvidence(EvidenceKindState, fmt.Sprintf("interface %s admin_status unrecognized value %d", iface.GetName(), iface.GetAdminStatus()))
			issues = append(issues, analysis.Issue{
				Code:     IssueInvalidAdminStatus,
				Status:   analysis.Incomplete,
				Scope:    portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has invalid or unrecognized administrative status value %d", iface.GetName(), iface.GetAdminStatus()),
				Evidence: []trace.EvidenceRef{ref},
			})
		}

		switch {
		case !iface.HasOperStatus() ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UNKNOWN:
			p.OperStatus = port.Unknown
			ref := addEvidence(EvidenceKindState, fmt.Sprintf("interface %s oper_status unspecified or unknown", iface.GetName()))
			issues = append(issues, analysis.Issue{
				Code:     IssueMissingOperStatus,
				Status:   analysis.Incomplete,
				Scope:    portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has unspecified or missing operational status", iface.GetName()),
				Evidence: []trace.EvidenceRef{ref},
			})
		case iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_UP:
			p.OperStatus = port.Up
		case iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_DOWN ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_TESTING ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_DORMANT ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_NOT_PRESENT ||
			iface.GetOperStatus() == interfacev1.OperStatus_OPER_STATUS_LOWER_LAYER_DOWN:
			p.OperStatus = port.Down
		default:
			p.OperStatus = port.Unknown
			ref := addEvidence(EvidenceKindState, fmt.Sprintf("interface %s oper_status unrecognized value %d", iface.GetName(), iface.GetOperStatus()))
			issues = append(issues, analysis.Issue{
				Code:     IssueInvalidOperStatus,
				Status:   analysis.Incomplete,
				Scope:    portScope(iface.GetName()),
				Message:  fmt.Sprintf("interface %q has invalid or unrecognized operational status value %d", iface.GetName(), iface.GetOperStatus()),
				Evidence: []trace.EvidenceRef{ref},
			})
		}

		portBuilder.Add(p)
	}

	ports, err := portBuilder.Build()
	if err != nil {
		return Result{}, errs.Wrap(err, "build port table")
	}

	var (
		hasSwitchportFacet bool
		hasEthernetFacet   bool
		hasPoeFacet        bool
		hasLag             bool
		hasIPFacet         bool
		hasRoutedIface     bool
	)

	for _, iface := range ifaces {
		if iface.GetIp() != nil {
			hasIPFacet = true
			// A routed interface needs a relay that can leave it out, which
			// only a VLAN-aware relay has.
			if iface.GetVlan() != nil || iface.GetPhysical() != nil || iface.GetLag() != nil {
				hasRoutedIface = true
			}
		}
		if iface.GetPhysical() != nil {
			if iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				hasEthernetFacet = true
				if copper := iface.GetPhysical().GetEthernet().GetCopper(); copper != nil {
					if copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil {
						hasPoeFacet = true
					}
				}
			}
			if iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
				routingWanted := len(want) == 0 || slices.Contains(want, port.LayerRouting)
				if !routingWanted || iface.GetIp() == nil {
					hasSwitchportFacet = true
				}
			}
		} else if iface.GetLag() != nil {
			hasLag = true
			if iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
				routingWanted := len(want) == 0 || slices.Contains(want, port.LayerRouting)
				if !routingWanted || iface.GetIp() == nil {
					hasSwitchportFacet = true
				}
			}
		}
	}

	if len(want) == 0 {
		report.Capabilities = append(report.Capabilities, port.LayerRelay)
		report.CapabilitySources[port.LayerRelay] = "always"

		if hasSwitchportFacet {
			report.Capabilities = append(report.Capabilities, port.LayerVlan)
			report.CapabilitySources[port.LayerVlan] = "inferred:switchport"
		}
		if hasEthernetFacet {
			report.Capabilities = append(report.Capabilities, port.LayerEthernet)
			report.CapabilitySources[port.LayerEthernet] = "inferred:ethernet"
		}
		if hasPoeFacet {
			report.Capabilities = append(report.Capabilities, port.LayerPoe)
			report.CapabilitySources[port.LayerPoe] = "inferred:poe"
		} else if len(budgets) > 0 {
			report.Capabilities = append(report.Capabilities, port.LayerPoe)
			report.CapabilitySources[port.LayerPoe] = "inferred:pse_budget"
		}
		if hasLag {
			report.Capabilities = append(report.Capabilities, port.LayerLag)
			report.CapabilitySources[port.LayerLag] = "inferred:lag"
		}
		if bridgeState != nil {
			report.Capabilities = append(report.Capabilities, port.LayerStp)
			report.CapabilitySources[port.LayerStp] = "inferred:stp"
		}
		if hasIPFacet {
			report.Capabilities = append(report.Capabilities, port.LayerRouting)
			report.CapabilitySources[port.LayerRouting] = "inferred:ip"
		}
		if hasRoutedIface && !slices.Contains(report.Capabilities, port.LayerVlan) {
			report.Capabilities = append(report.Capabilities, port.LayerVlan)
			report.CapabilitySources[port.LayerVlan] = "implied:routing"
		}
	} else {
		report.Capabilities = make([]port.Layer, len(want))
		copy(report.Capabilities, want)
		for _, l := range want {
			report.CapabilitySources[l] = "wanted"
		}
		if slices.Contains(want, port.LayerVlan) && !slices.Contains(want, port.LayerRelay) {
			report.Capabilities = append(report.Capabilities, port.LayerRelay)
			report.CapabilitySources[port.LayerRelay] = "implied:vlan"
		}
		if slices.Contains(want, port.LayerStp) && !slices.Contains(want, port.LayerRelay) && !slices.Contains(report.Capabilities, port.LayerRelay) {
			report.Capabilities = append(report.Capabilities, port.LayerRelay)
			report.CapabilitySources[port.LayerRelay] = "implied:stp"
		}
		if hasLag && !slices.Contains(want, port.LayerLag) {
			report.Capabilities = append(report.Capabilities, port.LayerLag)
			report.CapabilitySources[port.LayerLag] = "present:lag"
		}
		if slices.Contains(want, port.LayerRouting) && hasRoutedIface {
			if !slices.Contains(report.Capabilities, port.LayerVlan) {
				report.Capabilities = append(report.Capabilities, port.LayerVlan)
				report.CapabilitySources[port.LayerVlan] = "implied:routing"
			}
			if !slices.Contains(report.Capabilities, port.LayerRelay) {
				report.Capabilities = append(report.Capabilities, port.LayerRelay)
				report.CapabilitySources[port.LayerRelay] = "implied:routing"
			}
		}
	}

	isWanted := func(l port.Layer) bool {
		return slices.Contains(report.Capabilities, l)
	}

	if !isWanted(port.LayerVlan) {
		for _, iface := range ifaces {
			hasSw := false
			if iface.GetPhysical() != nil && iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
				hasSw = true
			} else if iface.GetLag() != nil && iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
				hasSw = true
			}
			if hasSw {
				why := "layer not wanted"
				if isWanted(port.LayerRouting) && iface.GetIp() != nil {
					why = "interface is routed"
				}
				addSkipped(iface.GetName(), "switchport", why, analysis.Incomplete, IssueSkippedLayerNotWanted)
			}
		}
	}

	if !isWanted(port.LayerEthernet) {
		for _, iface := range ifaces {
			if iface.GetPhysical() != nil && iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				addSkipped(iface.GetName(), "ethernet", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
			}
		}
	}

	if !isWanted(port.LayerPoe) {
		for _, iface := range ifaces {
			if iface.GetPhysical() != nil && iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				if copper := iface.GetPhysical().GetEthernet().GetCopper(); copper != nil {
					if copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil {
						addSkipped(iface.GetName(), "poe", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
					}
				}
			}
		}
		for _, b := range budgets {
			groupStr := strconv.FormatUint(uint64(b.GetPseGroup()), 10)
			addSkipped(groupStr, "pse_budget", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
	}

	if !isWanted(port.LayerStp) {
		if bridgeState != nil {
			addSkipped("", "stp_bridge", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
		for _, ps := range stpPorts {
			addSkipped(ps.GetInterfaceName(), "stp_port", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
	}

	if !isWanted(port.LayerRouting) {
		for _, iface := range ifaces {
			if iface.GetIp() != nil {
				addSkipped(iface.GetName(), "ip", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
			}
		}
		for _, addr := range addrs {
			if addr == nil {
				continue
			}
			addSkipped(addr.GetInterfaceName(), "ip_address", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
		for _, n := range neighbors {
			if n == nil {
				continue
			}
			addSkipped(n.GetInterfaceName(), "ip_neighbor", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
	}

	for _, iface := range ifaces {
		hasSw := false
		if iface.GetPhysical() != nil && iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
			hasSw = true
		} else if iface.GetLag() != nil && iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
			hasSw = true
		}
		if !hasSw {
			report.NoSwitchport = append(report.NoSwitchport, iface.GetName())
		}
	}

	cfg := vswitch.Config{
		Ports: ports,
	}

	if isWanted(port.LayerEthernet) || isWanted(port.LayerPoe) {
		phyCfg := &phy.Config{}
		if isWanted(port.LayerEthernet) {
			phyCfg.Ethernet = make(map[string]phy.Ethernet)
			for _, iface := range ifaces {
				if iface.GetPhysical() == nil || !iface.GetPhysical().HasEthernet() || iface.GetPhysical().GetEthernet() == nil {
					continue
				}
				ef := iface.GetPhysical().GetEthernet()
				eth := phy.Ethernet{}
				if ef.HasCapabilities() && ef.GetCapabilities() != nil {
					caps := ef.GetCapabilities()
					if len(caps.GetSupportedSpeedsBps()) > 0 {
						eth.SupportedSpeedsBPS = make([]uint64, len(caps.GetSupportedSpeedsBps()))
						copy(eth.SupportedSpeedsBPS, caps.GetSupportedSpeedsBps())
					}
					if caps.HasAutoNegotiationSupported() {
						eth.AutoNegotiationSupported = caps.GetAutoNegotiationSupported()
					}
				}
				if ef.HasAppliedAutoNegotiation() && ef.GetAppliedAutoNegotiation() != nil {
					an := ef.GetAppliedAutoNegotiation()
					if an.HasEnabled() {
						eth.Setting = &phy.Setting{
							AutoNegotiation: an.GetEnabled(),
						}
					}
				}
				var obs *phy.Observed
				speed := uint64(0)
				if ef.HasActiveSpeedBps() {
					speed = ef.GetActiveSpeedBps()
				}
				duplex := phy.Unknown
				if ef.HasActiveDuplex() {
					switch ef.GetActiveDuplex() {
					case phyv1.EthernetDuplex_ETHERNET_DUPLEX_FULL:
						duplex = phy.Full
					case phyv1.EthernetDuplex_ETHERNET_DUPLEX_HALF:
						duplex = phy.Half
					}
				}
				if speed > 0 || duplex != phy.Unknown {
					obs = &phy.Observed{
						SpeedBPS: speed,
						Duplex:   duplex,
					}
				}
				eth.Observed = obs
				phyCfg.Ethernet[iface.GetName()] = eth
			}
		}

		if isWanted(port.LayerPoe) {
			poe := &phy.PoE{
				Groups: make(map[string]phy.Group, len(budgets)),
				Ports:  make(map[string]phy.PsePort),
			}
			budgetRows, budgetConflicts := resolveFacts(budgets, func(b *phyv1.PseBudget) (factKey, string, bool) {
				if b == nil {
					return factKey{}, "", false
				}
				groupStr := strconv.FormatUint(uint64(b.GetPseGroup()), 10)
				value := "power_milliwatts=unreported"
				if b.HasPowerMilliwatts() {
					value = "power_milliwatts=" + strconv.FormatUint(uint64(b.GetPowerMilliwatts()), 10)
				}
				return factKey{id: groupStr, display: groupStr, scope: rootScope}, value, true
			})
			for _, conflict := range budgetConflicts {
				addConflict(conflict.key.scope, "pse_budget", conflict.key.display, conflict.detail(), IssueConflictBudget)
			}
			for _, groupStr := range sortedKeys(budgetRows) {
				b := budgetRows[groupStr]
				var power uint32
				if b.HasPowerMilliwatts() {
					power = b.GetPowerMilliwatts()
				} else {
					addDefault(groupStr, "power_milliwatts", "0")
				}
				poe.Groups[groupStr] = phy.Group{
					PowerMilliwatts: power,
				}
			}

			for _, iface := range ifaces {
				if iface.GetPhysical() == nil || !iface.GetPhysical().HasEthernet() || iface.GetPhysical().GetEthernet() == nil {
					continue
				}
				copper := iface.GetPhysical().GetEthernet().GetCopper()
				if copper == nil {
					continue
				}
				hasPoeInfo := copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil
				if !hasPoeInfo {
					continue
				}
				detail := copper.GetPoeDetail()
				if detail == nil || !detail.HasPseGroup() {
					addSkipped(iface.GetName(), "poe", "missing poe_detail", analysis.Incomplete, IssueSkippedMissingFacet)
					continue
				}
				groupStr := strconv.FormatUint(uint64(detail.GetPseGroup()), 10)
				if _, exists := poe.Groups[groupStr]; !exists {
					addSkipped(iface.GetName(), "poe", "pse group budget is missing or conflicting", analysis.Incomplete, IssueSkippedMissingFacet)
					continue
				}
				addDefault(iface.GetName(), "max_class", "8")
				psePort := phy.PsePort{
					Group:    groupStr,
					MaxClass: 8,
					Enabled:  true,
				}
				if ps := copper.GetPoeSettings(); ps != nil {
					if ps.HasEnabled() {
						psePort.Enabled = ps.GetEnabled()
					}
					if ps.HasPowerLimitMilliwatts() {
						lim := ps.GetPowerLimitMilliwatts()
						psePort.Limit = &lim
					}
					if ps.HasPriority() && ps.GetPriority() != phyv1.PoePriority_POE_PRIORITY_UNSPECIFIED {
						switch ps.GetPriority() {
						case phyv1.PoePriority_POE_PRIORITY_CRITICAL:
							psePort.Priority = phy.PriorityCritical
						case phyv1.PoePriority_POE_PRIORITY_HIGH:
							psePort.Priority = phy.PriorityHigh
						case phyv1.PoePriority_POE_PRIORITY_LOW:
							psePort.Priority = phy.PriorityLow
						}
					} else {
						addDefault(iface.GetName(), "priority", "")
					}
				} else {
					addDefault(iface.GetName(), "priority", "")
				}
				if copper.GetPoe() != nil && copper.GetPoe().HasPowerClass() {
					if class := copper.GetPoe().GetPowerClass(); class > 8 {
						addSkipped(iface.GetName(), "power_class", "class above 8", analysis.Unsupported, IssueUnsupportedPowerClass)
					} else {
						psePort.PDClass = phy.Class(uint8(class))
					}
				}
				poe.Ports[iface.GetName()] = psePort
			}
			phyCfg.PoE = poe
		}

		if phyCfg.Ethernet != nil || phyCfg.PoE != nil {
			cfg.Phy = phyCfg
		}
	}

	if isWanted(port.LayerRelay) {
		bridgeCfg := &bridge.Config{
			AgingTime: 300 * time.Second,
		}
		addDefault("", "aging_time", "300s")

		if isWanted(port.LayerVlan) {
			vlanCfg := &bridge.VLAN{
				Table:       make(map[vlan.ID]string, len(vlans)),
				Switchports: make(map[string]bridge.Switchport),
			}
			vlanRows, vlanConflicts := resolveFacts(vlans, func(v *switchingv1.Vlan) (factKey, string, bool) {
				if v == nil {
					return factKey{}, "", false
				}
				vid := vlan.ID(v.GetId())
				if !vid.Valid() {
					addSkipped("", "vlan", fmt.Sprintf("invalid vlan id %d", v.GetId()), analysis.Unsupported, IssueInvalidVlanID)
					return factKey{}, "", false
				}
				value := "name=unreported"
				if v.HasName() {
					value = fmt.Sprintf("name=%q", v.GetName())
				}
				key := strconv.FormatUint(uint64(vid), 10)
				return factKey{id: key, display: key, scope: rootScope}, value, true
			})
			for _, conflict := range vlanConflicts {
				addConflict(conflict.key.scope, "vlan", conflict.key.display, conflict.detail(), IssueConflictVlan)
			}
			for _, key := range sortedKeys(vlanRows) {
				v := vlanRows[key]
				vid := vlan.ID(v.GetId())
				vlanCfg.Table[vid] = v.GetName()
			}

			for _, iface := range ifaces {
				var (
					swFacet  *switchingv1.SwitchportFacet
					isMember bool
				)
				if iface.GetPhysical() != nil {
					if iface.GetPhysical().HasLagParent() {
						isMember = true
					}
					if iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
						swFacet = iface.GetPhysical().GetSwitchport()
					}
				} else if iface.GetLag() != nil {
					if iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
						swFacet = iface.GetLag().GetSwitchport()
					}
				}

				if swFacet == nil {
					continue
				}
				if isMember {
					addSkipped(iface.GetName(), "switchport", "lag member", analysis.Incomplete, IssueSkippedLagMember)
					continue
				}
				if isWanted(port.LayerRouting) && iface.GetIp() != nil {
					addSkipped(iface.GetName(), "switchport", "interface is routed", analysis.Incomplete, IssueSkippedInterfaceRouted)
					continue
				}

				if swFacet.GetMode() == switchingv1.SwitchportMode_SWITCHPORT_MODE_DOT1Q_TUNNEL {
					if !swFacet.HasPvid() {
						addSkipped(iface.GetName(), "switchport", "tunnel without pvid", analysis.Unsupported, IssueTunnelWithoutPvid)
						continue
					}

					pvid := vlan.ID(swFacet.GetPvid())
					var customerVIDs []vlan.ID
					if len(swFacet.GetTaggedVlanIds()) > 0 {
						customerVIDs = make([]vlan.ID, len(swFacet.GetTaggedVlanIds()))
						for i, id := range swFacet.GetTaggedVlanIds() {
							customerVIDs[i] = vlan.ID(id)
						}
						slices.Sort(customerVIDs)
					}

					sw := bridge.Switchport{
						Tunnel: &bridge.Tunnel{
							VID:          pvid,
							CustomerVIDs: customerVIDs,
						},
					}

					if swFacet.HasIngressFiltering() {
						sw.IngressFiltering = swFacet.GetIngressFiltering()
					} else {
						sw.IngressFiltering = false
						addDefault(iface.GetName(), "ingress_filtering", "false")
					}

					addDefault(iface.GetName(), "qinq_ethtype", "0x88A8")

					if len(swFacet.GetUntaggedVlanIds()) > 0 {
						addSkipped(iface.GetName(), "untagged_vlan_ids", "tunnel port", analysis.Incomplete, IssueSkippedLayerNotWanted)
					}

					vlanCfg.Switchports[iface.GetName()] = sw

					if _, exists := vlanCfg.Table[pvid]; !exists {
						vlanCfg.Table[pvid] = ""
					}

					continue
				}

				sw := bridge.Switchport{}
				if swFacet.HasPvid() {
					vid := vlan.ID(swFacet.GetPvid())
					sw.PVID = &vid
				} else if len(swFacet.GetUntaggedVlanIds()) == 1 {
					vid := vlan.ID(swFacet.GetUntaggedVlanIds()[0])
					sw.PVID = &vid
					addDefault(iface.GetName(), "pvid", strconv.FormatUint(uint64(vid), 10))
				}

				if len(swFacet.GetTaggedVlanIds()) > 0 {
					sw.Tagged = make([]vlan.ID, len(swFacet.GetTaggedVlanIds()))
					for i, id := range swFacet.GetTaggedVlanIds() {
						sw.Tagged[i] = vlan.ID(id)
					}
					slices.Sort(sw.Tagged)
				}

				if len(swFacet.GetUntaggedVlanIds()) > 0 {
					sw.Untagged = make([]vlan.ID, len(swFacet.GetUntaggedVlanIds()))
					for i, id := range swFacet.GetUntaggedVlanIds() {
						sw.Untagged[i] = vlan.ID(id)
					}
					slices.Sort(sw.Untagged)
				}

				if swFacet.HasIngressFiltering() {
					sw.IngressFiltering = swFacet.GetIngressFiltering()
				} else {
					sw.IngressFiltering = false
					addDefault(iface.GetName(), "ingress_filtering", "false")
				}

				if swFacet.HasFrameAdmission() && swFacet.GetFrameAdmission() != switchingv1.FrameAdmission_FRAME_ADMISSION_UNSPECIFIED {
					switch swFacet.GetFrameAdmission() {
					case switchingv1.FrameAdmission_FRAME_ADMISSION_ALL:
						sw.Admission = bridge.All
					case switchingv1.FrameAdmission_FRAME_ADMISSION_TAGGED_ONLY:
						sw.Admission = bridge.TaggedOnly
					case switchingv1.FrameAdmission_FRAME_ADMISSION_UNTAGGED_AND_PRIORITY_TAGGED_ONLY:
						sw.Admission = bridge.UntaggedAndPriorityTaggedOnly
					}
				} else {
					sw.Admission = bridge.All
					addDefault(iface.GetName(), "frame_admission", "ALL")
				}

				vlanCfg.Switchports[iface.GetName()] = sw

				for _, vid := range sw.Tagged {
					if _, exists := vlanCfg.Table[vid]; !exists {
						vlanCfg.Table[vid] = ""
					}
				}
				for _, vid := range sw.Untagged {
					if _, exists := vlanCfg.Table[vid]; !exists {
						vlanCfg.Table[vid] = ""
					}
				}
				if sw.PVID != nil {
					if _, exists := vlanCfg.Table[*sw.PVID]; !exists {
						vlanCfg.Table[*sw.PVID] = ""
					}
				}
			}

			bridgeCfg.VLAN = vlanCfg
		}

		cfg.Bridge = bridgeCfg
	}

	if isWanted(port.LayerStp) && bridgeState != nil {
		bridgeAddress := bridgeState.GetBridgeId().GetAddress()
		mac, validBridgeAddress := parseEUI48(bridgeAddress)
		prio := bridgeState.GetBridgeId().GetPriority()

		var (
			bridgeWhy    string
			bridgeCode   analysis.IssueCode
			bridgeStatus analysis.Status
		)
		switch {
		case bridgeAddress == nil:
			bridgeWhy = "bridge id has no address"
			bridgeCode = IssueMissingBridgeAddress
			bridgeStatus = analysis.Unsupported
		case !validBridgeAddress || mac.IsGroup():
			bridgeWhy = "bridge id address is not a usable six-octet individual EUI-48 address"
			bridgeCode = IssueInvalidMAC
			bridgeStatus = analysis.Incomplete
		case mac == (netaddr.MAC{}):
			bridgeWhy = "bridge id has no address"
			bridgeCode = IssueMissingBridgeAddress
			bridgeStatus = analysis.Unsupported
		case prio >= 65536 || prio%4096 != 0:
			bridgeWhy = "bridge priority is not a multiple of 4096 below 65536"
			bridgeCode = IssueInvalidBridgePriority
			bridgeStatus = analysis.Unsupported
		}
		if bridgeWhy != "" {
			addSkipped("", "stp_bridge", bridgeWhy, bridgeStatus, bridgeCode)
		} else {
			stpCfg := stp.Config{
				Priority: uint16(prio),
				Address:  mac,
				Ports:    make(map[string]stp.Port),
			}

			if bridgeState.GetBridgeHelloTime() != nil {
				stpCfg.HelloTime = bridgeState.GetBridgeHelloTime().AsDuration()
			}
			if bridgeState.GetBridgeMaxAge() != nil {
				stpCfg.MaxAge = bridgeState.GetBridgeMaxAge().AsDuration()
			}
			if bridgeState.GetBridgeForwardDelay() != nil {
				stpCfg.ForwardDelay = bridgeState.GetBridgeForwardDelay().AsDuration()
			}
			if bridgeState.HasTxHoldCount() {
				if v := bridgeState.GetTxHoldCount(); v < 1 || v > 10 {
					addSkipped("", "stp_tx_hold_count", "outside 1 through 10", analysis.Unsupported, IssueInvalidTxHoldCount)
				} else {
					stpCfg.TxHoldCount = uint8(v)
				}
			} else {
				addDefault("", "tx_hold_count", "6")
			}

			stpPortRows, stpPortConflicts := resolveFacts(stpPorts, func(ps *stpv1.PortState) (factKey, string, bool) {
				if ps == nil {
					return factKey{}, "", false
				}
				portName := ps.GetInterfaceName()
				p, ok := ports.Port(portName)
				if !ok {
					addSkipped(portName, "stp_port", "absent from port table", analysis.Incomplete, IssueUnknownPort)
					return factKey{}, "", false
				}
				if p.LagParent != "" {
					addSkipped(portName, "stp_port", "port is a LAG member", analysis.Incomplete, IssueSkippedLagMember)
					return factKey{}, "", false
				}
				if ps.GetPriority() > 255 {
					addSkipped(portName, "stp_port", "priority above 255", analysis.Unsupported, IssueInvalidPortPriority)
					return factKey{}, "", false
				}
				adminPathCost := "unreported"
				if ps.HasAdminPathCost() {
					adminPathCost = strconv.FormatUint(uint64(ps.GetAdminPathCost()), 10)
				}
				value := fmt.Sprintf(
					"priority=%d, admin_path_cost=%s, admin_edge=%t, auto_edge=%t, point_to_point=%d",
					ps.GetPriority(), adminPathCost, ps.GetAdminEdge(), ps.GetAutoEdge(), ps.GetPointToPoint(),
				)
				return factKey{id: portName, display: portName, scope: portScope(portName)}, value, true
			})
			for _, conflict := range stpPortConflicts {
				addConflict(conflict.key.scope, "stp_port", conflict.key.display, conflict.detail(), IssueConflictSTPPort)
			}
			for _, portName := range sortedKeys(stpPortRows) {
				ps := stpPortRows[portName]

				var adminPathCost uint32
				if ps.HasAdminPathCost() {
					adminPathCost = ps.GetAdminPathCost()
				} else {
					addDefault(portName, "admin_path_cost", "0")
				}

				var p2p stp.PointToPointMode
				switch ps.GetPointToPoint() {
				case stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_TRUE:
					p2p = stp.PointToPointForceTrue
				case stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_FALSE:
					p2p = stp.PointToPointForceFalse
				default:
					p2p = stp.PointToPointAuto
				}

				stpCfg.Ports[portName] = stp.Port{
					Priority:     uint8(ps.GetPriority()),
					PathCost:     adminPathCost,
					AdminEdge:    ps.GetAdminEdge(),
					AutoEdge:     ps.GetAutoEdge(),
					PointToPoint: p2p,
				}
			}

			cfg.STP = &stpCfg
		}
	}

	aggByPort, aggConflicts := resolveFacts(lacpAggregators, func(agg *lacpv1.AggregatorState) (factKey, string, bool) {
		if agg == nil {
			return factKey{}, "", false
		}
		name := agg.GetInterfaceName()
		p, ok := ports.Port(name)
		if !ok {
			addSkipped(name, "lacp_port", "absent from port table", analysis.Incomplete, IssueUnknownPort)
			return factKey{}, "", false
		}
		if p.Kind != port.Lag {
			addSkipped(name, "lacp_port", "port is not a LAG", analysis.Incomplete, IssueSkippedUnsupportedFacet)
			return factKey{}, "", false
		}
		systemID := "unreported"
		if agg.GetSystemId() != nil {
			mac, ok := parseEUI48(agg.GetSystemId())
			if !ok || mac.IsGroup() {
				addSkipped(name, "lacp_aggregator", "system_id is not a usable six-octet individual EUI-48 address", analysis.Incomplete, IssueInvalidMAC)
				return factKey{}, "", false
			}
			systemID = mac.String()
		}
		value := fmt.Sprintf(
			"mode=%d, fast=%t, system_priority=%d, system_id=%s, key=%d, fallback=%t",
			agg.GetMode(), agg.GetFast(), agg.GetSystemPriority(), systemID, agg.GetKey(), agg.GetFallbackActiveBackup(),
		)
		return factKey{id: name, display: name, scope: portScope(name)}, value, true
	})
	for _, conflict := range aggConflicts {
		addConflict(conflict.key.scope, "lacp_aggregator", conflict.key.display, conflict.detail(), IssueConflictLACP)
	}

	portStateByMember, lacpPortConflicts := resolveFacts(lacpPorts, func(ps *lacpv1.PortState) (factKey, string, bool) {
		if ps == nil {
			return factKey{}, "", false
		}
		name := ps.GetInterfaceName()
		p, ok := ports.Port(name)
		if !ok {
			addSkipped(name, "lacp_port", "absent from port table", analysis.Incomplete, IssueUnknownPort)
			return factKey{}, "", false
		}
		if p.LagParent == "" {
			addSkipped(name, "lacp_port", "port is not a LAG member", analysis.Incomplete, IssueSkippedLagMember)
			return factKey{}, "", false
		}
		value := fmt.Sprintf("port_priority=%d, key=%d", ps.GetPortPriority(), ps.GetKey())
		return factKey{id: name, display: name, scope: portScope(name)}, value, true
	})
	for _, conflict := range lacpPortConflicts {
		addConflict(conflict.key.scope, "lacp_port_state", conflict.key.display, conflict.detail(), IssueConflictLACP)
	}

	if hasLag {
		lagCfg := &lag.Config{
			LAGs: make(map[string]lag.LAG),
		}

		for _, iface := range ifaces {
			if iface.GetLag() == nil {
				continue
			}
			lagName := iface.GetName()
			facet := iface.GetLag().GetAggregation()
			lagItem := lag.LAG{}

			if facet != nil && facet.HasBondMode() && facet.GetBondMode() != switchingv1.BondMode_BOND_MODE_UNSPECIFIED {
				switch facet.GetBondMode() {
				case switchingv1.BondMode_BOND_MODE_ACTIVE_BACKUP:
					lagItem.Mode = lag.ActiveBackup
				case switchingv1.BondMode_BOND_MODE_BALANCE_SLB:
					lagItem.Mode = lag.BalanceSLB
				case switchingv1.BondMode_BOND_MODE_BALANCE_TCP:
					lagItem.Mode = lag.BalanceTCP
				default:
					lagItem.Mode = lag.ActiveBackup
					addDefault(lagName, "bond_mode", "active-backup")
				}
			} else {
				lagItem.Mode = lag.ActiveBackup
				addDefault(lagName, "bond_mode", "active-backup")
			}

			if facet != nil && facet.HasUpDelay() {
				lagItem.UpDelay = facet.GetUpDelay().AsDuration()
			} else {
				lagItem.UpDelay = 0
				addDefault(lagName, "up_delay", "0")
			}

			if facet != nil && facet.HasDownDelay() {
				lagItem.DownDelay = facet.GetDownDelay().AsDuration()
			} else {
				lagItem.DownDelay = 0
				addDefault(lagName, "down_delay", "0")
			}

			if facet != nil && facet.HasHashBasis() {
				lagItem.HashBasis = facet.GetHashBasis()
			}

			if facet != nil && facet.HasPrimaryInterfaceName() {
				lagItem.Primary = facet.GetPrimaryInterfaceName()
			}

			if facet != nil && facet.HasMinimumActiveLinks() {
				lagItem.MinLinks = int(facet.GetMinimumActiveLinks())
			}

			if agg, ok := aggByPort[lagName]; ok {
				lacpCfg := lag.LACPConfig{
					Fast:           agg.GetFast(),
					SystemPriority: uint16(agg.GetSystemPriority()),
					Key:            uint16(agg.GetKey()),
					Fallback:       agg.GetFallbackActiveBackup(),
				}
				switch agg.GetMode() {
				case lacpv1.LacpMode_LACP_MODE_ACTIVE:
					lacpCfg.Mode = lag.Active
				case lacpv1.LacpMode_LACP_MODE_PASSIVE:
					lacpCfg.Mode = lag.Passive
				case lacpv1.LacpMode_LACP_MODE_OFF, lacpv1.LacpMode_LACP_MODE_UNSPECIFIED:
					lacpCfg.Mode = lag.Off
				default:
					lacpCfg.Mode = lag.Off
				}
				if agg.GetSystemId() != nil {
					lacpCfg.SystemID, _ = parseEUI48(agg.GetSystemId())
				}
				lagItem.LACP = lacpCfg
			} else {
				lagItem.LACP.Mode = lag.Off
				addDefault(lagName, "lacp", "off")
			}

			members := ports.Members(lagName)
			if len(members) > 0 {
				lagItem.Members = make(map[string]lag.Member, len(members))
				for _, mem := range members {
					m := lag.Member{}
					if ps, ok := portStateByMember[mem.Name]; ok {
						m.Priority = uint16(ps.GetPortPriority())
						m.Key = uint16(ps.GetKey())
					}
					lagItem.Members[mem.Name] = m
				}
			}

			lagCfg.LAGs[lagName] = lagItem
		}

		cfg.LAG = lagCfg
	}

	if isWanted(port.LayerRouting) {
		vrf := routing.VRF{
			Interfaces: make(map[string]routing.Interface),
		}

		for _, iface := range ifaces {
			if iface.GetIp() == nil {
				continue
			}

			var (
				vlanID      vlan.ID
				portName    string
				isSupported = true
			)

			switch {
			case iface.GetVlan() != nil:
				vlanID = vlan.ID(iface.GetVlan().GetVlanId())
				if cfg.Bridge != nil && cfg.Bridge.VLAN != nil && vlanID.Valid() {
					if _, exists := cfg.Bridge.VLAN.Table[vlanID]; !exists {
						cfg.Bridge.VLAN.Table[vlanID] = ""
					}
				}
			case iface.GetPhysical() != nil || iface.GetLag() != nil:
				portName = iface.GetName()
			default:
				isSupported = false
			}

			if !isSupported {
				addSkipped(iface.GetName(), "ip", "unsupported interface kind", analysis.Unsupported, IssueUnsupportedInterfaceKind)
				continue
			}

			ifaceMAC, validIfaceMAC := parseMAC(iface.GetMac())
			switch {
			case iface.GetMac() == nil:
				ifaceMAC = netaddr.MAC{}
				addDefault(iface.GetName(), "mac", "device base address")
			case validIfaceMAC && ifaceMAC != (netaddr.MAC{}) && !ifaceMAC.IsGroup():
			default:
				ifaceMAC = netaddr.MAC{}
				addSkipped(iface.GetName(), "interface_mac", "not a usable six-octet individual EUI-48 address", analysis.Incomplete, IssueInvalidMAC)
				addDefault(iface.GetName(), "mac", "device base address")
			}

			vrf.Interfaces[iface.GetName()] = routing.Interface{
				VLAN: vlanID,
				Port: portName,
				MAC:  ifaceMAC,
			}
		}

		addressRows, addressConflicts := resolveFacts(addrs, func(addr *ipv1.InterfaceAddress) (factKey, string, bool) {
			if addr == nil {
				return factKey{}, "", false
			}
			name := addr.GetInterfaceName()
			if _, ok := vrf.Interfaces[name]; !ok {
				addSkipped(name, "ip_address", "interface carries no ip facet", analysis.Incomplete, IssueMissingIPFacet)
				return factKey{}, "", false
			}

			ip, ok := parseIP(addr.GetAddress())
			if !ok {
				addSkipped(name, "ip_address", "address is not a valid IPv4 or IPv6 address", analysis.Incomplete, IssueInvalidIPAddress)
				return factKey{}, "", false
			}
			length, ok := parsePrefix(addr.GetPrefix(), ip)
			if !ok {
				addSkipped(name, "ip_address", "prefix is invalid, uses another family, or does not contain the address", analysis.Incomplete, IssueInvalidPrefix)
				return factKey{}, "", false
			}
			key := name + "\x00" + ip.String()
			prefix := netip.PrefixFrom(ip, length)
			return factKey{id: key, display: name + "/" + ip.String(), scope: portScope(name)}, prefix.String(), true
		})
		for _, conflict := range addressConflicts {
			addConflict(conflict.key.scope, "ip_address", conflict.key.display, conflict.detail(), IssueConflictAddress)
		}
		for _, key := range sortedKeys(addressRows) {
			addr := addressRows[key]
			name := addr.GetInterfaceName()
			iface := vrf.Interfaces[name]
			ip, _ := parseIP(addr.GetAddress())
			length, _ := parsePrefix(addr.GetPrefix(), ip)
			iface.Prefixes = append(iface.Prefixes, netip.PrefixFrom(ip, length))
			vrf.Interfaces[name] = iface
		}

		neighborRows, neighborConflicts := resolveFacts(neighbors, func(n *ipv1.NeighborEntry) (factKey, string, bool) {
			if n == nil {
				return factKey{}, "", false
			}
			name := n.GetInterfaceName()
			mac, okMAC := parseMAC(n.GetMac())
			if n.GetMac() == nil {
				addSkipped(name, "ip_neighbor", "neighbor has no mac", analysis.Incomplete, IssueMissingNeighborMAC)
				return factKey{}, "", false
			}
			if !okMAC || mac == (netaddr.MAC{}) || mac.IsGroup() {
				addSkipped(name, "ip_neighbor", "mac is not a usable six-octet individual EUI-48 address", analysis.Incomplete, IssueInvalidMAC)
				return factKey{}, "", false
			}

			if _, ok := vrf.Interfaces[name]; !ok {
				addSkipped(name, "ip_neighbor", "interface carries no ip facet", analysis.Incomplete, IssueMissingIPFacet)
				return factKey{}, "", false
			}

			ip, okIP := parseIP(n.GetIp())
			if !okIP {
				addSkipped(name, "ip_neighbor", "neighbor address is not a valid IPv4 or IPv6 address", analysis.Incomplete, IssueInvalidIPAddress)
				return factKey{}, "", false
			}
			iface := vrf.Interfaces[name]
			matchingFamily := slices.ContainsFunc(iface.Prefixes, func(prefix netip.Prefix) bool {
				return prefix.Addr().Is4() == ip.Is4()
			})
			if !matchingFamily {
				addSkipped(name, "ip_neighbor", "neighbor address family has no interface prefix", analysis.Incomplete, IssueInvalidNeighborAddress)
				return factKey{}, "", false
			}
			key := name + "\x00" + ip.String()
			return factKey{id: key, display: name + "/" + ip.String(), scope: portScope(name)}, mac.String(), true
		})
		for _, conflict := range neighborConflicts {
			addConflict(conflict.key.scope, "ip_neighbor", conflict.key.display, conflict.detail(), IssueConflictNeighbor)
		}
		for _, key := range sortedKeys(neighborRows) {
			n := neighborRows[key]
			mac, _ := parseMAC(n.GetMac())
			ip, _ := parseIP(n.GetIp())
			vrf.Neighbors = append(vrf.Neighbors, routing.Neighbor{
				Interface: n.GetInterfaceName(),
				Addr:      ip,
				MAC:       mac,
			})
		}

		if len(vrf.Interfaces) > 0 {
			addDefault("", "vrf", routing.DefaultVRF)
			addDefault("", "mac", "assigned")
			cfg.Routing = &routing.Config{
				VRFs: map[string]routing.VRF{
					routing.DefaultVRF: vrf,
				},
			}
		} else {
			report.Capabilities = slices.DeleteFunc(report.Capabilities, func(l port.Layer) bool {
				return l == port.LayerRouting
			})
			delete(report.CapabilitySources, port.LayerRouting)
		}
	}

	var seeds []bridge.Seed
	fdbRows, fdbConflicts := resolveFacts(fdb, func(entry *switchingv1.FdbEntry) (factKey, string, bool) {
		if entry == nil {
			return factKey{}, "", false
		}
		switch {
		case !entry.HasStatus():
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "status is unreported", analysis.Incomplete, IssueInvalidFDBStatus)
			return factKey{}, "", false
		case entry.GetStatus() == switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_UNSPECIFIED:
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "status is UNSPECIFIED", analysis.Incomplete, IssueInvalidFDBStatus)
			return factKey{}, "", false
		case entry.GetStatus() == switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_INVALID:
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "status is INVALID", analysis.Unsupported, IssueInvalidFDBStatus)
			return factKey{}, "", false
		case entry.GetStatus() != switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE:
			addSkipped(entry.GetInterfaceName(), "fdb_entry", fmt.Sprintf("status has unrecognized value %d", entry.GetStatus()), analysis.Incomplete, IssueInvalidFDBStatus)
			return factKey{}, "", false
		}
		switch {
		case !entry.HasKind():
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "kind is unreported", analysis.Incomplete, IssueInvalidFDBKind)
			return factKey{}, "", false
		case entry.GetKind() == switchingv1.FdbEntryKind_FDB_ENTRY_KIND_UNSPECIFIED:
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "kind is UNSPECIFIED", analysis.Incomplete, IssueInvalidFDBKind)
			return factKey{}, "", false
		case entry.GetKind() != switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC &&
			entry.GetKind() != switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC:
			addSkipped(entry.GetInterfaceName(), "fdb_entry", fmt.Sprintf("kind %s cannot be represented as a forwarding seed", entry.GetKind()), analysis.Unsupported, IssueInvalidFDBKind)
			return factKey{}, "", false
		}
		vid := vlan.ID(entry.GetVlanId())
		if !vid.Valid() || uint64(entry.GetVlanId()) > uint64(vlan.MaxID) {
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "vlan_id outside 1 through 4094", analysis.Unsupported, IssueInvalidVlanID)
			return factKey{}, "", false
		}

		if _, ok := ports.Port(entry.GetInterfaceName()); !ok {
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "absent from port table", analysis.Incomplete, IssueUnknownPort)
			return factKey{}, "", false
		}

		mac, ok := parseEUI48(entry.GetMac())
		if !ok || mac.IsGroup() {
			addSkipped(entry.GetInterfaceName(), "fdb_entry", "mac is not a usable six-octet individual EUI-48 address", analysis.Incomplete, IssueInvalidMAC)
			return factKey{}, "", false
		}

		fdbKey := fmt.Sprintf("%d/%s", vid, mac.String())
		value := fmt.Sprintf("port=%q, kind=%s, status=ACTIVE", entry.GetInterfaceName(), entry.GetKind())
		return factKey{id: fdbKey, display: fdbKey, scope: rootScope}, value, true
	})
	for _, conflict := range fdbConflicts {
		addConflict(conflict.key.scope, "fdb_entry", conflict.key.display, conflict.detail(), IssueConflictFDB)
	}
	for _, fdbKey := range sortedKeys(fdbRows) {
		entry := fdbRows[fdbKey]
		vid := vlan.ID(entry.GetVlanId())
		mac, _ := parseEUI48(entry.GetMac())
		seeds = append(seeds, bridge.Seed{
			FID:       vid,
			MAC:       mac,
			Port:      entry.GetInterfaceName(),
			Static:    entry.GetKind() == switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC,
			LearnedAt: now,
		})
	}

	slices.Sort(report.Capabilities)
	slices.Sort(report.NoSwitchport)
	slices.SortFunc(report.Skipped, func(a, b Skipped) int {
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(a.What, b.What); r != 0 {
			return r
		}
		return cmp.Compare(a.Why, b.Why)
	})
	slices.SortFunc(report.Defaults, func(a, b Default) int {
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Field, b.Field); r != 0 {
			return r
		}
		return cmp.Compare(a.Value, b.Value)
	})
	slices.SortFunc(report.Conflicts, func(a, b Conflict) int {
		if r := cmp.Compare(a.What, b.What); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Key, b.Key); r != 0 {
			return r
		}
		return cmp.Compare(a.Detail, b.Detail)
	})

	slices.SortFunc(seeds, func(a, b bridge.Seed) int {
		if r := cmp.Compare(a.FID, b.FID); r != 0 {
			return r
		}
		if r := cmp.Compare(a.MAC.String(), b.MAC.String()); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Port, b.Port); r != 0 {
			return r
		}
		if a.Static != b.Static {
			if a.Static {
				return -1
			}
			return 1
		}
		return a.LearnedAt.Compare(b.LearnedAt)
	})

	normCfg := cfg.Normalize()
	if err := normCfg.Validate(); err != nil {
		return Result{}, errs.Wrap(err, "validate switch configuration")
	}

	metadata := analysis.NewMetadata(rootScope, issues, catalog, assumptions)

	return Result{
		Spec: vswitch.ConstructionSpec{
			Config:   normCfg,
			Seeds:    seeds,
			NodeID:   src.DeviceID,
			Metadata: metadata,
		},
		Report:   report.Clone(),
		Metadata: metadata,
	}, nil
}

func parseMAC(eui *addrv1.EuiAddress) (netaddr.MAC, bool) {
	if eui == nil || eui.GetEui48() == nil {
		return netaddr.MAC{}, false
	}
	return parseEUI48(eui.GetEui48())
}

func parseEUI48(eui *addrv1.Eui48Address) (netaddr.MAC, bool) {
	if eui == nil {
		return netaddr.MAC{}, false
	}
	octets := eui.GetOctets()
	if len(octets) != 6 {
		return netaddr.MAC{}, false
	}
	var mac netaddr.MAC
	copy(mac[:], octets)
	return mac, true
}

func parseIP(ipAddr *addrv1.IpAddress) (netip.Addr, bool) {
	if ipAddr == nil {
		return netip.Addr{}, false
	}
	if v4 := ipAddr.GetV4(); v4 != nil {
		octets := v4.GetOctets()
		if len(octets) != 4 {
			return netip.Addr{}, false
		}
		return netip.AddrFrom4([4]byte(octets)), true
	}
	if v6 := ipAddr.GetV6(); v6 != nil {
		octets := v6.GetOctets()
		if len(octets) != 16 {
			return netip.Addr{}, false
		}
		return netip.AddrFrom16([16]byte(octets)), true
	}
	return netip.Addr{}, false
}

func parsePrefix(p *addrv1.IpPrefix, address netip.Addr) (int, bool) {
	if p == nil || !address.IsValid() {
		return 0, false
	}
	if address.Is4() {
		v4 := p.GetV4()
		if v4 == nil || !v4.HasAddress() || !v4.HasLength() || len(v4.GetAddress().GetOctets()) != 4 || v4.GetLength() == 0 || v4.GetLength() > 32 {
			return 0, false
		}
		prefixAddress := netip.AddrFrom4([4]byte(v4.GetAddress().GetOctets()))
		prefix := netip.PrefixFrom(prefixAddress, int(v4.GetLength()))
		if prefix != prefix.Masked() || !prefix.Contains(address) {
			return 0, false
		}
		return int(v4.GetLength()), true
	}
	if address.Is6() {
		v6 := p.GetV6()
		if v6 == nil || !v6.HasAddress() || !v6.HasLength() || len(v6.GetAddress().GetOctets()) != 16 || v6.GetLength() == 0 || v6.GetLength() > 128 {
			return 0, false
		}
		prefixAddress := netip.AddrFrom16([16]byte(v6.GetAddress().GetOctets()))
		prefix := netip.PrefixFrom(prefixAddress, int(v6.GetLength()))
		if prefix != prefix.Masked() || !prefix.Contains(address) {
			return 0, false
		}
		return int(v6.GetLength()), true
	}
	return 0, false
}
