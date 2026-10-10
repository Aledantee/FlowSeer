package netmodel

import (
	"fmt"
	"strconv"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
)

// Issue codes used by netmodel.
const (
	IssueConflictBudget            analysis.IssueCode = "netmodel.poe.conflict"
	IssueInvalidPoEPriority        analysis.IssueCode = "netmodel.poe.invalid_priority"
	IssueInvalidEthernetDuplex     analysis.IssueCode = "netmodel.ethernet.invalid_duplex"
	IssuePowerClassWithoutDelivery analysis.IssueCode = "netmodel.poe.power_class_without_delivery"
	IssueUnsupportedPowerClass     analysis.IssueCode = "netmodel.poe.unsupported_power_class"
)

func (c *loadContext) reportUnwantedPHY(ifaces []*interfacev1.Interface, budgets []*phyv1.PseBudget) {
	if !c.isWanted(phy.LayerName) {
		for _, iface := range ifaces {
			if iface.GetPhysical() != nil && iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				c.addSkipped(iface.GetName(), "ethernet", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
			}
		}
	}

	if !c.isWanted(phy.LayerNamePoE) {
		for _, iface := range ifaces {
			if iface.GetPhysical() != nil && iface.GetPhysical().HasEthernet() && iface.GetPhysical().GetEthernet() != nil {
				if copper := iface.GetPhysical().GetEthernet().GetCopper(); copper != nil {
					if copper.GetPoe() != nil || copper.GetPoeSettings() != nil || copper.GetPoeDetail() != nil {
						c.addSkipped(iface.GetName(), "poe", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
					}
				}
			}
		}
		for _, b := range budgets {
			groupStr := strconv.FormatUint(uint64(b.GetPseGroup()), 10)
			c.addSkipped(groupStr, "pse_budget", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
	}
}

func (c *loadContext) loadPHY(ifaces []*interfacev1.Interface, budgets []*phyv1.PseBudget) *phy.Config {
	if !c.isWanted(phy.LayerName) && !c.isWanted(phy.LayerNamePoE) {
		return nil
	}
	phyCfg := &phy.Config{}
	if c.isWanted(phy.LayerName) {
		phyCfg.Ethernet = c.loadEthernet(ifaces)
	}
	if c.isWanted(phy.LayerNamePoE) {
		phyCfg.PoE = c.loadPoE(ifaces, budgets)
	}
	if phyCfg.Ethernet != nil || phyCfg.PoE != nil {
		return phyCfg
	}
	return nil
}

func (c *loadContext) loadEthernet(ifaces []*interfacev1.Interface) map[string]phy.Ethernet {
	ethernet := make(map[string]phy.Ethernet)
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
				if caps.GetAutoNegotiationSupported() {
					eth.AutoNegotiationSupported = phy.CapabilitySupported
				} else {
					eth.AutoNegotiationSupported = phy.CapabilityUnsupported
				}
			} else {
				eth.AutoNegotiationSupported = phy.CapabilityUnknown
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
			case phyv1.EthernetDuplex_ETHERNET_DUPLEX_UNSPECIFIED:
				c.addSkipped(iface.GetName(), "ethernet_duplex", "active_duplex is UNSPECIFIED", analysis.Incomplete, IssueInvalidEthernetDuplex)
			case phyv1.EthernetDuplex_ETHERNET_DUPLEX_FULL:
				duplex = phy.Full
			case phyv1.EthernetDuplex_ETHERNET_DUPLEX_HALF:
				duplex = phy.Half
			default:
				c.addSkipped(iface.GetName(), "ethernet_duplex", fmt.Sprintf("active_duplex has unrecognized value %d", ef.GetActiveDuplex()), analysis.Unsupported, IssueInvalidEthernetDuplex)
			}
		}
		if speed > 0 || duplex != phy.Unknown {
			obs = &phy.Observed{
				SpeedBPS: speed,
				Duplex:   duplex,
			}
		}
		eth.Observed = obs
		ethernet[iface.GetName()] = eth
	}

	return ethernet
}

func (c *loadContext) loadPoE(ifaces []*interfacev1.Interface, budgets []*phyv1.PseBudget) *phy.PoE {
	poe := &phy.PoE{
		Groups: make(map[string]phy.Group, len(budgets)),
		Ports:  make(map[string]phy.PsePort),
	}
	budgetRows, budgetConflicts := resolveFacts(budgets, func(b *phyv1.PseBudget) (factKey, string, bool) {
		if b == nil {
			return factKey{}, "", false
		}
		groupStr := strconv.FormatUint(uint64(b.GetPseGroup()), 10)
		value := "power_nanowatts=unreported"
		if b.HasPowerNanowatts() {
			value = "power_nanowatts=" + strconv.FormatUint(b.GetPowerNanowatts(), 10)
		}
		return factKey{id: groupStr, display: groupStr, scope: c.rootScope}, value, true
	})
	for _, conflict := range budgetConflicts {
		c.addConflict(conflict.key.scope, "pse_budget", conflict.key.display, conflict.detail(), IssueConflictBudget)
	}
	for _, groupStr := range sortedKeys(budgetRows) {
		b := budgetRows[groupStr]
		var power uint64
		if b.HasPowerNanowatts() {
			power = b.GetPowerNanowatts()
		} else {
			c.addDefault(groupStr, "power_nanowatts", "0")
		}
		poe.Groups[groupStr] = phy.Group{
			PowerNanowatts: power,
		}
	}

	c.loadPoEPorts(ifaces, poe)
	return poe
}

func (c *loadContext) loadPoEPorts(ifaces []*interfacev1.Interface, poe *phy.PoE) {
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
			c.addSkipped(iface.GetName(), "poe", "missing poe_detail", analysis.Incomplete, IssueSkippedMissingFacet)
			continue
		}
		groupStr := strconv.FormatUint(uint64(detail.GetPseGroup()), 10)
		if _, exists := poe.Groups[groupStr]; !exists {
			c.addSkipped(iface.GetName(), "poe", "pse group budget is missing or conflicting", analysis.Incomplete, IssueSkippedMissingFacet)
			continue
		}
		c.addDefault(iface.GetName(), "max_class", "8")
		psePort := phy.PsePort{
			Group:    groupStr,
			MaxClass: 8,
			Enabled:  true,
		}
		validPriority := true
		if ps := copper.GetPoeSettings(); ps != nil {
			if ps.HasEnabled() {
				psePort.Enabled = ps.GetEnabled()
			}
			if ps.HasPowerLimitNanowatts() {
				lim := ps.GetPowerLimitNanowatts()
				psePort.Limit = &lim
			}
			if ps.HasPriority() {
				switch ps.GetPriority() {
				case phyv1.PoePriority_POE_PRIORITY_UNSPECIFIED:
					c.addDefault(iface.GetName(), "priority", "")
				case phyv1.PoePriority_POE_PRIORITY_CRITICAL:
					psePort.Priority = phy.PriorityCritical
				case phyv1.PoePriority_POE_PRIORITY_HIGH:
					psePort.Priority = phy.PriorityHigh
				case phyv1.PoePriority_POE_PRIORITY_LOW:
					psePort.Priority = phy.PriorityLow
				default:
					c.addSkipped(iface.GetName(), "poe_priority", fmt.Sprintf("priority has unrecognized value %d", ps.GetPriority()), analysis.Unsupported, IssueInvalidPoEPriority)
					validPriority = false
				}
			} else {
				c.addDefault(iface.GetName(), "priority", "")
			}
		} else {
			c.addDefault(iface.GetName(), "priority", "")
		}
		if !validPriority {
			continue
		}
		if poeFacet := copper.GetPoe(); poeFacet != nil {
			isDelivering := false
			if poeFacet.HasStatus() {
				switch poeFacet.GetStatus() {
				case phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER:
					psePort.PD = phy.PDAttached
					isDelivering = true
				case phyv1.PoeStatus_POE_STATUS_SEARCHING:
					psePort.PD = phy.PDAbsent
					c.addAssumption(iface.GetName(), "absence is inferred from the searching status", fmt.Sprintf("port %s poe status searching", iface.GetName()))
				case phyv1.PoeStatus_POE_STATUS_DISABLED,
					phyv1.PoeStatus_POE_STATUS_TEST,
					phyv1.PoeStatus_POE_STATUS_FAULT,
					phyv1.PoeStatus_POE_STATUS_OTHER_FAULT,
					phyv1.PoeStatus_POE_STATUS_UNSPECIFIED:
					psePort.PD = phy.PDUnknown
				default:
					psePort.PD = phy.PDUnknown
				}
			} else {
				psePort.PD = phy.PDUnknown
			}

			if poeFacet.HasPowerClass() {
				if !isDelivering {
					c.addSkipped(iface.GetName(), "power_class", "class valid only while delivering power", analysis.Incomplete, IssuePowerClassWithoutDelivery)
				} else if class := poeFacet.GetPowerClass(); class > 8 {
					c.addSkipped(iface.GetName(), "power_class", "class above 8", analysis.Unsupported, IssueUnsupportedPowerClass)
				} else {
					psePort.PDClass = phy.Class(uint8(class))
				}
			}
		}
		poe.Ports[iface.GetName()] = psePort
	}
}
