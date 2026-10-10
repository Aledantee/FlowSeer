package netmodel

import (
	"fmt"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// Issue codes used by netmodel.
const (
	IssueConflictLACP    analysis.IssueCode = "netmodel.lacp.conflict"
	IssueInvalidLACPMode analysis.IssueCode = "netmodel.lacp.invalid_mode"
	IssueInvalidBondMode analysis.IssueCode = "netmodel.lag.invalid_bond_mode"
)

func (c *loadContext) loadLAG(ifaces []*interfacev1.Interface, lacpAggregators []*lacpv1.AggregatorState, lacpPorts []*lacpv1.PortState, ports port.Table, hasLag bool) *lag.Config {
	aggByPort := c.loadLACPAggregators(lacpAggregators, ports)
	portStateByMember := c.loadLACPMembers(lacpPorts, ports)
	if !hasLag {
		return nil
	}
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

		if facet != nil && facet.HasBondMode() {
			switch facet.GetBondMode() {
			case switchingv1.BondMode_BOND_MODE_UNSPECIFIED:
				lagItem.Mode = lag.ActiveBackup
				c.addDefault(lagName, "bond_mode", "active-backup")
			case switchingv1.BondMode_BOND_MODE_ACTIVE_BACKUP:
				lagItem.Mode = lag.ActiveBackup
			case switchingv1.BondMode_BOND_MODE_BALANCE_SLB:
				lagItem.Mode = lag.BalanceSLB
			case switchingv1.BondMode_BOND_MODE_BALANCE_TCP:
				lagItem.Mode = lag.BalanceTCP
			default:
				lagItem.Mode = lag.ActiveBackup
				c.addSkipped(lagName, "bond_mode", fmt.Sprintf("has unrecognized value %d", facet.GetBondMode()), analysis.Unsupported, IssueInvalidBondMode)
			}
		} else {
			lagItem.Mode = lag.ActiveBackup
			c.addDefault(lagName, "bond_mode", "active-backup")
		}

		if facet != nil && facet.HasUpDelay() {
			lagItem.UpDelay = facet.GetUpDelay().AsDuration()
		} else {
			lagItem.UpDelay = 0
			c.addDefault(lagName, "up_delay", "0")
		}

		if facet != nil && facet.HasDownDelay() {
			lagItem.DownDelay = facet.GetDownDelay().AsDuration()
		} else {
			lagItem.DownDelay = 0
			c.addDefault(lagName, "down_delay", "0")
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
			case lacpv1.LacpMode_LACP_MODE_UNSPECIFIED:
				lacpCfg.Mode = lag.Off
				c.addDefault(lagName, "lacp_mode", "off")
			case lacpv1.LacpMode_LACP_MODE_ACTIVE:
				lacpCfg.Mode = lag.Active
			case lacpv1.LacpMode_LACP_MODE_PASSIVE:
				lacpCfg.Mode = lag.Passive
			case lacpv1.LacpMode_LACP_MODE_OFF:
				lacpCfg.Mode = lag.Off
			}
			if agg.GetSystemId() != nil {
				lacpCfg.SystemID, _ = parseEUI48(agg.GetSystemId())
			}
			lagItem.LACP = lacpCfg
		} else {
			lagItem.LACP.Mode = lag.Off
			c.addDefault(lagName, "lacp", "off")
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

	return lagCfg
}

func (c *loadContext) loadLACPAggregators(lacpAggregators []*lacpv1.AggregatorState, ports port.Table) map[string]*lacpv1.AggregatorState {
	aggByPort, aggConflicts := resolveFacts(lacpAggregators, func(agg *lacpv1.AggregatorState) (factKey, string, bool) {
		if agg == nil {
			return factKey{}, "", false
		}
		name := agg.GetInterfaceName()
		p, ok := ports.Port(name)
		if !ok {
			c.addSkipped(name, "lacp_port", "absent from port table", analysis.Incomplete, IssueUnknownPort)
			return factKey{}, "", false
		}
		if p.Kind != port.LAG {
			c.addSkipped(name, "lacp_port", "port is not a LAG", analysis.Incomplete, IssueSkippedUnsupportedFacet)
			return factKey{}, "", false
		}
		switch agg.GetMode() {
		case lacpv1.LacpMode_LACP_MODE_UNSPECIFIED,
			lacpv1.LacpMode_LACP_MODE_OFF,
			lacpv1.LacpMode_LACP_MODE_ACTIVE,
			lacpv1.LacpMode_LACP_MODE_PASSIVE:
		default:
			c.addSkipped(name, "lacp_aggregator", fmt.Sprintf("mode has unrecognized value %d", agg.GetMode()), analysis.Unsupported, IssueInvalidLACPMode)
			return factKey{}, "", false
		}
		systemID := "unreported"
		if agg.GetSystemId() != nil {
			mac, ok := parseEUI48(agg.GetSystemId())
			if !ok || mac.IsGroup() {
				c.addSkipped(name, "lacp_aggregator", "system_id is not a usable six-octet individual EUI-48 address", analysis.Incomplete, IssueInvalidMAC)
				return factKey{}, "", false
			}
			systemID = mac.String()
		}
		value := fmt.Sprintf(
			"mode=%d, fast=%t, system_priority=%d, system_id=%s, key=%d, fallback=%t",
			agg.GetMode(), agg.GetFast(), agg.GetSystemPriority(), systemID, agg.GetKey(), agg.GetFallbackActiveBackup(),
		)
		return factKey{id: name, display: name, scope: c.portScope(name)}, value, true
	})
	for _, conflict := range aggConflicts {
		c.addConflict(conflict.key.scope, "lacp_aggregator", conflict.key.display, conflict.detail(), IssueConflictLACP)
	}

	return aggByPort
}

func (c *loadContext) loadLACPMembers(lacpPorts []*lacpv1.PortState, ports port.Table) map[string]*lacpv1.PortState {
	portStateByMember, lacpPortConflicts := resolveFacts(lacpPorts, func(ps *lacpv1.PortState) (factKey, string, bool) {
		if ps == nil {
			return factKey{}, "", false
		}
		name := ps.GetInterfaceName()
		p, ok := ports.Port(name)
		if !ok {
			c.addSkipped(name, "lacp_port", "absent from port table", analysis.Incomplete, IssueUnknownPort)
			return factKey{}, "", false
		}
		if p.LagParent == "" {
			c.addSkipped(name, "lacp_port", "port is not a LAG member", analysis.Incomplete, IssueSkippedLagMember)
			return factKey{}, "", false
		}
		value := fmt.Sprintf("port_priority=%d, key=%d", ps.GetPortPriority(), ps.GetKey())
		return factKey{id: name, display: name, scope: c.portScope(name)}, value, true
	})
	for _, conflict := range lacpPortConflicts {
		c.addConflict(conflict.key.scope, "lacp_port_state", conflict.key.display, conflict.detail(), IssueConflictLACP)
	}

	return portStateByMember
}
