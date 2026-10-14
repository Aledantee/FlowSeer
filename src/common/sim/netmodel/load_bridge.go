package netmodel

import (
	"fmt"
	"slices"
	"strconv"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
)

// Issue codes used by netmodel.
const (
	IssueInvalidVLANID         analysis.IssueCode = "netmodel.vlan.invalid_id"
	IssueConflictVLAN          analysis.IssueCode = "netmodel.vlan.conflict"
	IssueInvalidFrameAdmission analysis.IssueCode = "netmodel.switchport.invalid_frame_admission"
	IssueInvalidSwitchportMode analysis.IssueCode = "netmodel.switchport.invalid_mode"
	IssueTunnelWithoutPvid     analysis.IssueCode = "netmodel.switchport.tunnel_without_pvid"
)

func (c *loadContext) reportUnwantedBridge(ifaces []*interfacev1.Interface, ownership loadOwnership) {
	if !c.isWanted(bridge.LayerNameVLAN) {
		for _, iface := range ifaces {
			hasSw := false
			if iface.GetPhysical() != nil && iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
				hasSw = true
			} else if iface.GetLag() != nil && iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
				hasSw = true
			}
			if hasSw {
				why := "layer not wanted"
				if c.isWanted(routing.LayerName) && ownership.ipPresent[iface.GetName()] {
					why = "interface is routed"
				}
				c.addSkipped(iface.GetName(), "switchport", why, analysis.Incomplete, IssueSkippedLayerNotWanted)
			}
		}
	}
}

func (c *loadContext) reportNoSwitchport(ifaces []*interfacev1.Interface) {
	for _, iface := range ifaces {
		hasSw := false
		if iface.GetPhysical() != nil && iface.GetPhysical().HasSwitchport() && iface.GetPhysical().GetSwitchport() != nil {
			hasSw = true
		} else if iface.GetLag() != nil && iface.GetLag().HasSwitchport() && iface.GetLag().GetSwitchport() != nil {
			hasSw = true
		}
		if !hasSw {
			c.report.NoSwitchport = append(c.report.NoSwitchport, iface.GetName())
		}
	}
}

func (c *loadContext) loadBridge(ifaces []*interfacev1.Interface, vlans []*switchingv1.Vlan, ownership loadOwnership) *bridge.Config {
	if !c.isWanted(bridge.LayerName) {
		return nil
	}
	bridgeCfg := &bridge.Config{
		AgingTime: bridge.DefaultAgingTime,
	}
	c.addDefault("", "aging_time", strconv.FormatFloat(bridge.DefaultAgingTime.Seconds(), 'f', -1, 64)+"s")

	if c.isWanted(bridge.LayerNameVLAN) {
		bridgeCfg.VLAN = c.loadVLANs(vlans)
		c.loadSwitchports(ifaces, ownership, bridgeCfg.VLAN)
	}
	return bridgeCfg
}

func (c *loadContext) loadVLANs(vlans []*switchingv1.Vlan) *bridge.VLAN {
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
			c.addSkipped("", "vlan", fmt.Sprintf("invalid vlan id %d", v.GetId()), analysis.Unsupported, IssueInvalidVLANID)
			return factKey{}, "", false
		}
		value := "name=unreported"
		if v.HasName() {
			value = fmt.Sprintf("name=%q", v.GetName())
		}
		key := strconv.FormatUint(uint64(vid), 10)
		return factKey{id: key, display: key, scope: c.rootScope}, value, true
	})
	for _, conflict := range vlanConflicts {
		c.addConflict(conflict.key.scope, "vlan", conflict.key.display, conflict.detail(), IssueConflictVLAN)
	}
	for _, key := range sortedKeys(vlanRows) {
		v := vlanRows[key]
		vid := vlan.ID(v.GetId())
		vlanCfg.Table[vid] = v.GetName()
	}

	return vlanCfg
}

func (c *loadContext) loadSwitchports(ifaces []*interfacev1.Interface, ownership loadOwnership, vlanCfg *bridge.VLAN) {
	for _, iface := range ifaces {
		var (
			swFacet  *switchingv1.SwitchportFacet
			isMember bool
		)
		if iface.GetPhysical() != nil {
			if iface.GetPhysical().HasLagParentInterfaceName() {
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
			c.addSkipped(iface.GetName(), "switchport", "lag member", analysis.Incomplete, IssueSkippedLagMember)
			continue
		}
		_, isRoutedSubParent := ownership.routedSubParents[iface.GetName()]
		if c.isWanted(routing.LayerName) && (ownership.ipPresent[iface.GetName()] || isRoutedSubParent) {
			c.addSkipped(iface.GetName(), "switchport", "interface is routed", analysis.Incomplete, IssueSkippedInterfaceRouted)
			continue
		}
		if swFacet.HasMode() {
			switch swFacet.GetMode() {
			case switchingv1.SwitchportMode_SWITCHPORT_MODE_UNSPECIFIED,
				switchingv1.SwitchportMode_SWITCHPORT_MODE_OTHER,
				switchingv1.SwitchportMode_SWITCHPORT_MODE_ACCESS,
				switchingv1.SwitchportMode_SWITCHPORT_MODE_TRUNK,
				switchingv1.SwitchportMode_SWITCHPORT_MODE_HYBRID,
				switchingv1.SwitchportMode_SWITCHPORT_MODE_DOT1Q_TUNNEL:
			default:
				c.addSkipped(iface.GetName(), "switchport", fmt.Sprintf("mode has unrecognized value %d", swFacet.GetMode()), analysis.Unsupported, IssueInvalidSwitchportMode)
				continue
			}
		}
		if swFacet.HasFrameAdmission() {
			switch swFacet.GetFrameAdmission() {
			case switchingv1.FrameAdmission_FRAME_ADMISSION_UNSPECIFIED,
				switchingv1.FrameAdmission_FRAME_ADMISSION_ALL,
				switchingv1.FrameAdmission_FRAME_ADMISSION_TAGGED_ONLY,
				switchingv1.FrameAdmission_FRAME_ADMISSION_UNTAGGED_AND_PRIORITY_TAGGED_ONLY:
			default:
				c.addSkipped(iface.GetName(), "switchport", fmt.Sprintf("frame_admission has unrecognized value %d", swFacet.GetFrameAdmission()), analysis.Unsupported, IssueInvalidFrameAdmission)
				continue
			}
		}
		if swFacet.GetMode() == switchingv1.SwitchportMode_SWITCHPORT_MODE_DOT1Q_TUNNEL {
			c.loadTunnelSwitchport(iface.GetName(), swFacet, vlanCfg)
			continue
		}
		c.loadOrdinarySwitchport(iface.GetName(), swFacet, vlanCfg)
	}
}

func (c *loadContext) loadTunnelSwitchport(name string, swFacet *switchingv1.SwitchportFacet, vlanCfg *bridge.VLAN) {
	if !swFacet.HasPvid() {
		c.addSkipped(name, "switchport", "tunnel without pvid", analysis.Unsupported, IssueTunnelWithoutPvid)
		return
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
		c.addDefault(name, "ingress_filtering", "false")
	}

	c.addDefault(name, "qinq_ethtype", "0x88A8")

	if len(swFacet.GetUntaggedVlanIds()) > 0 {
		c.addSkipped(name, "untagged_vlan_ids", "tunnel port", analysis.Incomplete, IssueSkippedLayerNotWanted)
	}

	vlanCfg.Switchports[name] = sw

	if _, exists := vlanCfg.Table[pvid]; !exists {
		vlanCfg.Table[pvid] = ""
	}
}

func (c *loadContext) loadOrdinarySwitchport(name string, swFacet *switchingv1.SwitchportFacet, vlanCfg *bridge.VLAN) {
	sw := bridge.Switchport{}
	if swFacet.HasPvid() {
		vid := vlan.ID(swFacet.GetPvid())
		sw.PVID = &vid
	} else if len(swFacet.GetUntaggedVlanIds()) == 1 {
		vid := vlan.ID(swFacet.GetUntaggedVlanIds()[0])
		sw.PVID = &vid
		c.addDefault(name, "pvid", strconv.FormatUint(uint64(vid), 10))
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
		c.addDefault(name, "ingress_filtering", "false")
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
		c.addDefault(name, "frame_admission", "ALL")
	}

	vlanCfg.Switchports[name] = sw

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
