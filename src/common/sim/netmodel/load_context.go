package netmodel

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Issue codes used by netmodel.
const (
	IssueUnknownPort                analysis.IssueCode = "netmodel.skipped.unknown_port"
	IssueSkippedInterfaceRouted     analysis.IssueCode = "netmodel.switchport.interface_routed"
	IssueSkippedLagMember           analysis.IssueCode = "netmodel.switchport.lag_member"
	IssueSkippedUnsupportedFacet    analysis.IssueCode = "netmodel.skipped.unsupported_facet"
	IssueSkippedMissingFacet        analysis.IssueCode = "netmodel.skipped.missing_facet"
	IssueSkippedLayerNotWanted      analysis.IssueCode = "netmodel.skipped.layer_not_wanted"
	IssueUnsupportedNetworkInstance analysis.IssueCode = "netmodel.instance.unsupported"
	IssueInvalidNetworkInstance     analysis.IssueCode = "netmodel.instance.invalid"
)

type loadContext struct {
	src          SourceContext
	rootScope    analysis.Scope
	stpScope     analysis.Scope
	routingScope analysis.Scope
	catalog      analysis.EvidenceCatalog
	issues       []analysis.Issue
	assumptions  []analysis.Assumption
	report       Report
	refusedIP    map[string]string
	refusedMST   map[uint32]bool
}

func newLoadContext(src SourceContext) *loadContext {
	c := &loadContext{
		src:          src,
		rootScope:    analysis.NodeScope(src.DeviceID),
		routingScope: routing.VRFScope(src.DeviceID, routing.DefaultVRF),
		catalog:      analysis.EvidenceCatalog{},
		refusedIP:    make(map[string]string),
		refusedMST:   make(map[uint32]bool),
		report:       Report{CapabilitySources: make(map[trace.Layer]string)},
	}
	c.stpScope = c.protocolScope(stp.LayerName, "0")
	return c
}

func (c *loadContext) portScope(portName string) analysis.Scope {
	return analysis.PortScope(c.src.DeviceID, portName)
}

func (c *loadContext) protocolScope(layer trace.Layer, instance string) analysis.Scope {
	return analysis.ProtocolScope(c.src.DeviceID, string(layer), instance)
}

func (c *loadContext) stpPortScope(portName string) analysis.Scope {
	return analysis.FieldScope(c.stpScope, "ports", portName)
}

func (c *loadContext) mstScope(path ...string) analysis.Scope {
	return analysis.FieldScope(c.stpScope, append([]string{"mst"}, path...)...)
}

func (c *loadContext) routingInterfaceLookupScope(iface *interfacev1.Interface) analysis.Scope {
	if iface.GetVlan() != nil {
		return routing.VLANLookupScope(c.src.DeviceID, routing.DefaultVRF, vlan.ID(iface.GetVlan().GetVlanId()))
	}
	if iface.GetPhysical() != nil || iface.GetLag() != nil {
		return routing.PortLookupScope(c.src.DeviceID, routing.DefaultVRF, iface.GetName())
	}
	if sub := iface.GetSub(); sub != nil {
		return routing.PortLookupScope(c.src.DeviceID, routing.DefaultVRF, sub.GetParentInterfaceName())
	}
	return routing.OwnershipScope(c.src.DeviceID, routing.DefaultVRF, iface.GetName())
}

func (c *loadContext) addEvidence(kind analysis.EvidenceKind, detail string) trace.EvidenceRef {
	origin := c.src.Origin
	if origin == "" {
		if c.src.DeviceID != "" {
			origin = c.src.DeviceID
		} else {
			origin = "netmodel"
		}
	}
	contextStr := c.src.Context
	if detail != "" {
		if contextStr != "" {
			contextStr = contextStr + "; " + detail
		} else {
			contextStr = detail
		}
	}
	var ref trace.EvidenceRef
	c.catalog, ref = c.catalog.Add(analysis.Evidence{
		Kind:    kind,
		Origin:  origin,
		Context: contextStr,
	})
	return ref
}

func (c *loadContext) addDefaultAt(scope analysis.Scope, portName, field, value string) {
	detail := fmt.Sprintf("default %s=%s", field, value)
	if portName != "" {
		detail = fmt.Sprintf("port %s default %s=%s", portName, field, value)
	}
	ref := c.addEvidence(EvidenceKindDefault, detail)

	c.report.Defaults = append(c.report.Defaults, Default{
		Scope:    scope,
		Port:     portName,
		Field:    field,
		Value:    value,
		Evidence: []trace.EvidenceRef{ref},
	})

	c.assumptions = append(c.assumptions, analysis.Assumption{
		Scope:     scope,
		Statement: fmt.Sprintf("default value applied for %s: %s", field, value),
		Evidence:  []trace.EvidenceRef{ref},
	})
}

func (c *loadContext) addDefault(portName, field, value string) {
	scope := c.rootScope
	if portName != "" {
		scope = c.portScope(portName)
	}
	c.addDefaultAt(scope, portName, field, value)
}

func (c *loadContext) addAssumptionAt(scope analysis.Scope, portName, statement, detail string) trace.EvidenceRef {
	if detail != "" && portName != "" && !strings.HasPrefix(detail, "port ") {
		detail = fmt.Sprintf("port %s %s", portName, detail)
	}
	ref := c.addEvidence(EvidenceKindState, detail)
	c.assumptions = append(c.assumptions, analysis.Assumption{
		Scope:     scope,
		Statement: statement,
		Evidence:  []trace.EvidenceRef{ref},
	})
	return ref
}

func (c *loadContext) addAssumption(portName, statement, detail string) trace.EvidenceRef {
	scope := c.rootScope
	if portName != "" {
		scope = c.portScope(portName)
	}
	return c.addAssumptionAt(scope, portName, statement, detail)
}

func (c *loadContext) addSkippedAt(scope analysis.Scope, portName, what, why string, status analysis.Status, code analysis.IssueCode) {
	detail := fmt.Sprintf("skipped %s: %s", what, why)
	if portName != "" {
		detail = fmt.Sprintf("port %s skipped %s: %s", portName, what, why)
	}
	ref := c.addEvidence(EvidenceKindSkipped, detail)

	c.report.Skipped = append(c.report.Skipped, Skipped{
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
		c.issues = append(c.issues, analysis.Issue{
			Code:     code,
			Status:   status,
			Scope:    scope,
			Message:  msg,
			Evidence: []trace.EvidenceRef{ref},
		})
	}
}

func (c *loadContext) addSkipped(portName, what, why string, status analysis.Status, code analysis.IssueCode) {
	scope := c.rootScope
	if portName != "" {
		scope = c.portScope(portName)
	}
	c.addSkippedAt(scope, portName, what, why, status, code)
}

func (c *loadContext) addConflict(scope analysis.Scope, what, key, detail string, code analysis.IssueCode) {
	ref := c.addEvidence(EvidenceKindConflict, fmt.Sprintf("conflict in %s for %s: %s", what, key, detail))

	c.report.Conflicts = append(c.report.Conflicts, Conflict{
		Scope:    scope,
		What:     what,
		Key:      key,
		Detail:   detail,
		Evidence: []trace.EvidenceRef{ref},
	})

	c.issues = append(c.issues, analysis.Issue{
		Code:     code,
		Status:   analysis.Unstable,
		Scope:    scope,
		Message:  fmt.Sprintf("conflicting %s for %s: %s", what, key, detail),
		Evidence: []trace.EvidenceRef{ref},
	})
}

func (c *loadContext) isWanted(l trace.Layer) bool {
	return slices.Contains(c.report.Capabilities, l)
}

type loadOwnership struct {
	ipPresent        map[string]bool
	ipAccepted       map[string]bool
	routedSubParents map[string]struct{}
}

func prepareOwnership(ifaces []*interfacev1.Interface, ports port.Table) (loadOwnership, map[string]*interfacev1.Interface) {
	ownership := loadOwnership{ipPresent: make(map[string]bool, len(ifaces)), ipAccepted: make(map[string]bool, len(ifaces))}
	for _, iface := range ifaces {
		ownership.ipPresent[iface.GetName()] = iface.GetIp() != nil
		ip := iface.GetIp()
		ownership.ipAccepted[iface.GetName()] = ip != nil && defaultInstance(ip)
	}
	ifaceByName := make(map[string]*interfacev1.Interface, len(ifaces))
	for _, iface := range ifaces {
		ifaceByName[iface.GetName()] = iface
	}

	// A parent whose sub-interface carries an IP facet is routed even though
	// its own IP facet is absent. Only a sub-interface the routing walk would
	// itself accept counts, so a parent keeps its switchport and
	// spanning-tree membership when the sub-interface routing rejects is the
	// only reason it looked routed.
	//
	// Two sub-interfaces can claim the same parent and outer VID; the
	// routing walk drops both claimants for that conflict, and this pre-pass
	// must agree before it decides routedSubParents, or the switchport and
	// spanning-tree walks strip a parent's membership for a routed status
	// the routing walk then also refuses, leaving the port in none of the
	// three tables. So the conflict is decided here first: claims are
	// grouped by parent and VID, and a parent is routed only through a group
	// with exactly one claimant.
	routedSubClaimants := make(map[string][]string)
	for _, iface := range ifaces {
		sub := iface.GetSub()
		if sub == nil || !ownership.ipAccepted[iface.GetName()] {
			continue
		}
		if parentPort, vlanID, accepted := acceptRoutedSubParent(ifaceByName, ports, sub); accepted {
			key := parentPort + "\x00" + strconv.Itoa(int(vlanID))
			routedSubClaimants[key] = append(routedSubClaimants[key], iface.GetName())
		}
	}
	ownership.routedSubParents = make(map[string]struct{})
	for key, claimants := range routedSubClaimants {
		if len(claimants) != 1 {
			continue
		}
		parentPort, _, _ := strings.Cut(key, "\x00")
		ownership.routedSubParents[parentPort] = struct{}{}
	}

	return ownership, ifaceByName
}

type instanceRow interface {
	HasNetworkInstance() bool
	GetNetworkInstance() string
}

func defaultInstance(row instanceRow) bool {
	return !row.HasNetworkInstance() || row.GetNetworkInstance() == DefaultNetworkInstance
}

func (c *loadContext) instanceScope(domain, what, key string) analysis.Scope {
	return analysis.FieldScope(c.rootScope, "network_instance", domain, what, key)
}

func (c *loadContext) acceptInstance(row instanceRow, what, key, portName string) bool {
	domain := row.GetNetworkInstance()
	if !row.HasNetworkInstance() {
		c.addDefaultAt(c.instanceScope(DefaultNetworkInstance, what, key), portName, "network_instance", DefaultNetworkInstance)
		return true
	}
	if defaultInstance(row) {
		return true
	}
	c.refuseInstance(domain, what, key, portName)
	return false
}

func (c *loadContext) refuseInstance(domain, what, key, portName string) {
	why := fmt.Sprintf("network_instance %q is not supported", domain)
	status, code := analysis.Unsupported, IssueUnsupportedNetworkInstance
	if domain == "" {
		why = "network_instance is present but empty"
		status, code = analysis.Incomplete, IssueInvalidNetworkInstance
	}
	c.addSkippedAt(c.instanceScope(domain, what, key), portName, what, why, status, code)
}

func (c *loadContext) gateInstances(in *Input) {
	for _, iface := range in.Interfaces {
		ip := iface.GetIp()
		if ip == nil || c.acceptInstance(ip, "ip", iface.GetName(), iface.GetName()) {
			continue
		}
		c.refusedIP[iface.GetName()] = ip.GetNetworkInstance()
		if facet := iface.GetFilter(); facet != nil {
			if facet.GetInSet() != "" {
				c.refuseInstance(ip.GetNetworkInstance(), "filter_in", iface.GetName()+"/"+facet.GetInSet(), iface.GetName())
			}
			if facet.GetOutSet() != "" {
				c.refuseInstance(ip.GetNetworkInstance(), "filter_out", iface.GetName()+"/"+facet.GetOutSet(), iface.GetName())
			}
		}
	}
	in.Addresses = slices.DeleteFunc(slices.Clone(in.Addresses), func(row *ipv1.InterfaceAddress) bool {
		if row == nil {
			return false
		}
		domain, refused := c.refusedIP[row.GetInterfaceName()]
		if refused {
			c.refuseInstance(domain, "ip_address", row.GetInterfaceName()+"/"+row.GetAddress().String(), row.GetInterfaceName())
		}
		return refused
	})
	in.Neighbors = slices.DeleteFunc(slices.Clone(in.Neighbors), func(row *ipv1.NeighborEntry) bool {
		if row == nil {
			return false
		}
		domain, refused := c.refusedIP[row.GetInterfaceName()]
		if refused {
			c.refuseInstance(domain, "ip_neighbor", row.GetInterfaceName()+"/"+row.GetIp().String(), row.GetInterfaceName())
		}
		return refused
	})
	in.VLANs = slices.DeleteFunc(slices.Clone(in.VLANs), func(row *switchingv1.Vlan) bool {
		return row != nil && !c.acceptInstance(row, "vlan", strconv.FormatUint(uint64(row.GetId()), 10), "")
	})
	in.FDB = slices.DeleteFunc(slices.Clone(in.FDB), func(row *switchingv1.FdbEntry) bool {
		if row == nil {
			return false
		}
		key := fmt.Sprintf("%d/%s/%s", row.GetVlanId(), row.GetMac().String(), row.GetInterfaceName())
		return !c.acceptInstance(row, "fdb_entry", key, row.GetInterfaceName())
	})
	if row := in.BridgeState; row != nil && !c.acceptInstance(row, "stp_bridge", "0", "") {
		for _, ps := range in.STPPorts {
			if ps != nil {
				c.refuseInstance(row.GetNetworkInstance(), "stp_port", ps.GetInterfaceName(), ps.GetInterfaceName())
			}
		}
		in.BridgeState, in.STPPorts = nil, nil
	}
	in.MSTInstances = slices.DeleteFunc(slices.Clone(in.MSTInstances), func(row *stpv1.MstInstance) bool {
		if row != nil && !c.acceptInstance(row, "mst_instance", strconv.FormatUint(uint64(row.GetMstId()), 10), "") {
			c.refusedMST[row.GetMstId()] = true
			return true
		}
		return false
	})
	in.MSTVLANMaps = slices.DeleteFunc(slices.Clone(in.MSTVLANMaps), func(row *stpv1.MstVlanMap) bool {
		if row != nil && !c.acceptInstance(row, "mst_vlan_map", strconv.FormatUint(uint64(row.GetMstId()), 10), "") {
			c.refusedMST[row.GetMstId()] = true
			return true
		}
		return false
	})
}

// routedSubParentPort resolves parentName to the interface the routing walk
// requires it to be: a physical or link-aggregation interface that is not
// itself a LAG member. It reports the parent's interface and whether it
// qualifies.
func routedSubParentPort(ifaceByName map[string]*interfacev1.Interface, ports port.Table, parentName string) (*interfacev1.Interface, bool) {
	parent := ifaceByName[parentName]
	if parent == nil || (parent.GetPhysical() == nil && parent.GetLag() == nil) {
		return nil, false
	}
	if p, ok := ports.Port(parent.GetName()); ok && p.LagParent != "" {
		return nil, false
	}
	return parent, true
}

// acceptRoutedSubParent applies the routing walk's full acceptance test for a
// routed sub-interface: routedSubParentPort on its parent, plus an
// encapsulation of exactly one customer 802.1Q tag at a usable VLAN id. It
// returns the parent's port name and that VLAN id when accepted.
func acceptRoutedSubParent(ifaceByName map[string]*interfacev1.Interface, ports port.Table, sub *interfacev1.Subinterface) (string, vlan.ID, bool) {
	parent, ok := routedSubParentPort(ifaceByName, ports, sub.GetParentInterfaceName())
	if !ok {
		return "", 0, false
	}
	tags := sub.GetEncapsulation().GetTags()
	if len(tags) != 1 || tags[0].GetTpid() != packetv1.EtherType_ETHER_TYPE_DOT1Q || !vlan.ID(tags[0].GetVlanId()).Valid() {
		return "", 0, false
	}
	return parent.GetName(), vlan.ID(tags[0].GetVlanId()), true
}
