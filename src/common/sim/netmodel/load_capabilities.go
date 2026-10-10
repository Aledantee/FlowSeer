package netmodel

import (
	"fmt"
	"slices"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Issue codes used by netmodel.
const (
	IssueUnsupportedRequestedCapability analysis.IssueCode = "netmodel.capability.unsupported_request"
	IssueDuplicateRequestedCapability   analysis.IssueCode = "netmodel.capability.duplicate_request"
)

type facetSummary struct {
	hasSwitchportFacet bool
	hasEthernetFacet   bool
	hasPoeFacet        bool
	hasLag             bool
	hasIPFacet         bool
	hasRoutedIface     bool
	hasFilterFacet     bool
}

func summarizeFacets(ifaces []*interfacev1.Interface, ownership loadOwnership, want []trace.Layer, explicitRequest bool) facetSummary {
	summary := facetSummary{}
	for _, iface := range ifaces {
		if iface.GetFilter() != nil {
			summary.hasFilterFacet = true
		}
		if iface.GetIp() != nil {
			summary.hasIPFacet = true
			// A routed interface needs a relay that can leave it out, which
			// only a VLAN-aware relay has.
			if iface.GetVlan() != nil || iface.GetPhysical() != nil || iface.GetLag() != nil || iface.GetSub() != nil {
				summary.hasRoutedIface = true
			}
		}
		if iface.GetPhysical() != nil {
			if iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				summary.hasEthernetFacet = true
				if copper := iface.GetPhysical().GetEthernet().GetCopper(); copper != nil {
					if copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil {
						summary.hasPoeFacet = true
					}
				}
			}
			if iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
				routingWanted := !explicitRequest || slices.Contains(want, routing.LayerName)
				_, isRoutedSubParent := ownership.routedSubParents[iface.GetName()]
				if !routingWanted || (iface.GetIp() == nil && !isRoutedSubParent) {
					summary.hasSwitchportFacet = true
				}
			}
		} else if iface.GetLag() != nil {
			summary.hasLag = true
			if iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
				routingWanted := !explicitRequest || slices.Contains(want, routing.LayerName)
				_, isRoutedSubParent := ownership.routedSubParents[iface.GetName()]
				if !routingWanted || (iface.GetIp() == nil && !isRoutedSubParent) {
					summary.hasSwitchportFacet = true
				}
			}
		}
	}

	return summary
}

func (c *loadContext) requestedCapabilities(want []trace.Layer) ([]trace.Layer, bool) {
	explicitRequest := len(want) > 0
	if explicitRequest {
		requested := make([]trace.Layer, 0, len(want))
		seen := make(map[trace.Layer]struct{}, len(want))
		for _, layer := range want {
			switch layer {
			case bridge.LayerName, bridge.LayerNameVLAN, phy.LayerName, phy.LayerNamePoE,
				lag.LayerName, stp.LayerName, routing.LayerName, filter.LayerName:
			default:
				c.addSkipped(
					"",
					"requested_capability",
					fmt.Sprintf("layer %q is not supported by the network model loader", layer),
					analysis.Unsupported,
					IssueUnsupportedRequestedCapability,
				)
				continue
			}
			if _, duplicate := seen[layer]; duplicate {
				c.addSkipped(
					"",
					"requested_capability",
					fmt.Sprintf("layer %q is requested more than once", layer),
					analysis.Incomplete,
					IssueDuplicateRequestedCapability,
				)
				continue
			}
			seen[layer] = struct{}{}
			requested = append(requested, layer)
		}
		want = requested
	}

	return want, explicitRequest
}

func (c *loadContext) selectCapabilities(summary facetSummary, want []trace.Layer, explicitRequest, hasBudgets, hasBridgeState, hasFilterSets bool) {
	if !explicitRequest {
		c.report.Capabilities = append(c.report.Capabilities, bridge.LayerName)
		c.report.CapabilitySources[bridge.LayerName] = "always"

		if summary.hasSwitchportFacet {
			c.report.Capabilities = append(c.report.Capabilities, bridge.LayerNameVLAN)
			c.report.CapabilitySources[bridge.LayerNameVLAN] = "inferred:switchport"
		}
		if summary.hasEthernetFacet {
			c.report.Capabilities = append(c.report.Capabilities, phy.LayerName)
			c.report.CapabilitySources[phy.LayerName] = "inferred:ethernet"
		}
		if summary.hasPoeFacet {
			c.report.Capabilities = append(c.report.Capabilities, phy.LayerNamePoE)
			c.report.CapabilitySources[phy.LayerNamePoE] = "inferred:poe"
		} else if hasBudgets {
			c.report.Capabilities = append(c.report.Capabilities, phy.LayerNamePoE)
			c.report.CapabilitySources[phy.LayerNamePoE] = "inferred:pse_budget"
		}
		if summary.hasLag {
			c.report.Capabilities = append(c.report.Capabilities, lag.LayerName)
			c.report.CapabilitySources[lag.LayerName] = "inferred:lag"
		}
		if hasBridgeState {
			c.report.Capabilities = append(c.report.Capabilities, stp.LayerName)
			c.report.CapabilitySources[stp.LayerName] = "inferred:stp"
		}
		if summary.hasIPFacet {
			c.report.Capabilities = append(c.report.Capabilities, routing.LayerName)
			c.report.CapabilitySources[routing.LayerName] = "inferred:ip"
		}
		if summary.hasRoutedIface && !slices.Contains(c.report.Capabilities, bridge.LayerNameVLAN) {
			c.report.Capabilities = append(c.report.Capabilities, bridge.LayerNameVLAN)
			c.report.CapabilitySources[bridge.LayerNameVLAN] = "implied:routing"
		}
		if summary.hasFilterFacet {
			c.report.Capabilities = append(c.report.Capabilities, filter.LayerName)
			c.report.CapabilitySources[filter.LayerName] = "inferred:filter"
		} else if hasFilterSets {
			c.report.Capabilities = append(c.report.Capabilities, filter.LayerName)
			c.report.CapabilitySources[filter.LayerName] = "inferred:filter_rule_set"
		}
	} else {
		c.report.Capabilities = make([]trace.Layer, len(want))
		copy(c.report.Capabilities, want)
		for _, l := range want {
			c.report.CapabilitySources[l] = "wanted"
		}
		if slices.Contains(want, bridge.LayerNameVLAN) && !slices.Contains(want, bridge.LayerName) {
			c.report.Capabilities = append(c.report.Capabilities, bridge.LayerName)
			c.report.CapabilitySources[bridge.LayerName] = "implied:vlan"
		}
		if slices.Contains(want, stp.LayerName) && !slices.Contains(want, bridge.LayerName) && !slices.Contains(c.report.Capabilities, bridge.LayerName) {
			c.report.Capabilities = append(c.report.Capabilities, bridge.LayerName)
			c.report.CapabilitySources[bridge.LayerName] = "implied:stp"
		}
		if summary.hasLag && !slices.Contains(want, lag.LayerName) {
			c.report.Capabilities = append(c.report.Capabilities, lag.LayerName)
			c.report.CapabilitySources[lag.LayerName] = "present:lag"
		}
		if slices.Contains(want, filter.LayerName) && summary.hasIPFacet {
			if !slices.Contains(c.report.Capabilities, routing.LayerName) {
				c.report.Capabilities = append(c.report.Capabilities, routing.LayerName)
				c.report.CapabilitySources[routing.LayerName] = "implied:filter"
			}
		}
		if (slices.Contains(want, routing.LayerName) || slices.Contains(c.report.Capabilities, routing.LayerName)) && summary.hasRoutedIface {
			if !slices.Contains(c.report.Capabilities, bridge.LayerNameVLAN) {
				c.report.Capabilities = append(c.report.Capabilities, bridge.LayerNameVLAN)
				c.report.CapabilitySources[bridge.LayerNameVLAN] = "implied:routing"
			}
			if !slices.Contains(c.report.Capabilities, bridge.LayerName) {
				c.report.Capabilities = append(c.report.Capabilities, bridge.LayerName)
				c.report.CapabilitySources[bridge.LayerName] = "implied:routing"
			}
		}
	}
}
