package netmodel

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Issue codes used by netmodel.
const (
	IssueConflictRoutedClaim      analysis.IssueCode = "netmodel.routing.claim_conflict"
	IssueConflictNeighbor         analysis.IssueCode = "netmodel.routing.conflict"
	IssueConflictAddress          analysis.IssueCode = "netmodel.routing.address_conflict"
	IssueInvalidPrefix            analysis.IssueCode = "netmodel.routing.invalid_prefix"
	IssueInvalidNeighborAddress   analysis.IssueCode = "netmodel.routing.invalid_neighbor_address"
	IssueInvalidIPAddress         analysis.IssueCode = "netmodel.routing.invalid_ip_address"
	IssueMissingNeighborMAC       analysis.IssueCode = "netmodel.routing.missing_neighbor_mac"
	IssueMissingIPFacet           analysis.IssueCode = "netmodel.routing.missing_ip_facet"
	IssueUnsupportedEncapsulation analysis.IssueCode = "netmodel.routing.unsupported_encapsulation"
	IssueUnsupportedInterfaceKind analysis.IssueCode = "netmodel.routing.unsupported_interface_kind"
)

func (c *loadContext) reportUnwantedRouting(ifaces []*interfacev1.Interface, addrs []*ipv1.InterfaceAddress, neighbors []*ipv1.NeighborEntry) {
	if !c.isWanted(routing.LayerName) {
		for _, iface := range ifaces {
			if _, refused := c.refusedIP[iface.GetName()]; iface.GetIp() != nil && !refused {
				c.addSkippedAt(c.routingInterfaceLookupScope(iface), iface.GetName(), "ip", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
			}
		}
		for _, addr := range addrs {
			if addr == nil {
				continue
			}
			c.addSkippedAt(c.routingScope, addr.GetInterfaceName(), "ip_address", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
		for _, n := range neighbors {
			if n == nil {
				continue
			}
			scope := routing.NeighborTableScope(c.src.DeviceID, routing.DefaultVRF, n.GetInterfaceName())
			if ip, err := parseIP(n.GetIp()); err == nil {
				scope = routing.NeighborLookupScope(c.src.DeviceID, routing.DefaultVRF, n.GetInterfaceName(), ip)
			}
			c.addSkippedAt(scope, n.GetInterfaceName(), "ip_neighbor", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
	}
}

type pendingRoutedInterface struct {
	iface    *interfacev1.Interface
	portName string
	vlanID   vlan.ID
}

func (c *loadContext) loadRouting(ifaces []*interfacev1.Interface, addrs []*ipv1.InterfaceAddress, neighbors []*ipv1.NeighborEntry, ifaceByName map[string]*interfacev1.Interface, ports port.Table) (*routing.Config, []vlan.ID) {
	if !c.isWanted(routing.LayerName) {
		return nil, nil
	}
	vrf := routing.VRF{
		Interfaces: make(map[string]routing.Interface),
	}

	pending, claimants, vlanClaimants, contributions := c.collectRoutedClaims(ifaces, ifaceByName, ports)
	conflictedClaimants := c.conflictingRoutedClaims(claimants, vlanClaimants)
	c.loadRoutedInterfaces(pending, conflictedClaimants, &vrf)
	c.loadAddresses(addrs, &vrf)
	c.loadNeighbors(neighbors, &vrf)
	if len(vrf.Interfaces) > 0 {
		c.addDefault("", "vrf", routing.DefaultVRF)
		c.addDefault("", "mac", "assigned")
		return &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: vrf,
			},
		}, contributions
	}
	c.report.Capabilities = slices.DeleteFunc(c.report.Capabilities, func(l trace.Layer) bool {
		return l == routing.LayerName
	})
	delete(c.report.CapabilitySources, routing.LayerName)

	return nil, contributions
}

func (c *loadContext) collectRoutedClaims(ifaces []*interfacev1.Interface, ifaceByName map[string]*interfacev1.Interface, ports port.Table) ([]pendingRoutedInterface, map[string][]string, map[vlan.ID][]string, []vlan.ID) {
	// A claim is the (port, VLAN) pair a routed interface resolves to,
	// or, for a VLAN-kind interface that resolves to no port, the VLAN
	// id on its own. Two sub-interfaces can report the same parent and
	// outer VID, and two VLAN interfaces can report the same VLAN id;
	// both are refused by routing validation as a duplicate claim, so
	// every claimant is collected before any of them reaches the VRF: a
	// key claimed more than once drops every claimant, the same way the
	// shared fact resolver drops a contested value instead of keeping
	// whichever input happened to arrive first. The two claim kinds get
	// separate namespaces, since a port-and-VLAN claim and a bare VLAN
	// claim never contend for the same slot.
	var pending []pendingRoutedInterface
	var contributions []vlan.ID
	claimants := make(map[string][]string)
	vlanClaimants := make(map[vlan.ID][]string)

	for _, iface := range ifaces {
		if _, refused := c.refusedIP[iface.GetName()]; iface.GetIp() == nil || refused {
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
			if !vlanID.Valid() {
				c.addSkippedAt(c.routingInterfaceLookupScope(iface), iface.GetName(), "ip", fmt.Sprintf("vlan id %d is not usable", iface.GetVlan().GetVlanId()), analysis.Unsupported, IssueInvalidVLANID)
				continue
			}
			contributions = append(contributions, vlanID)
			vlanClaimants[vlanID] = append(vlanClaimants[vlanID], iface.GetName())
		case iface.GetPhysical() != nil || iface.GetLag() != nil:
			if p, ok := ports.Port(iface.GetName()); ok && p.LagParent != "" {
				// A LAG member cannot be a routed port either directly or
				// through a sub-interface; routing validation refuses it,
				// so it is turned aside here with the other unsupported
				// parent kinds rather than reaching that refusal.
				isSupported = false
				break
			}
			portName = iface.GetName()
		case iface.GetSub() != nil:
			sub := iface.GetSub()
			resolvedPort, resolvedVLAN, accepted := acceptRoutedSubParent(ifaceByName, ports, sub)
			if !accepted {
				if _, parentOK := routedSubParentPort(ifaceByName, ports, sub.GetParentInterfaceName()); !parentOK {
					isSupported = false
					break
				}
				c.addSkippedAt(c.routingInterfaceLookupScope(iface), iface.GetName(), "ip", "unsupported encapsulation", analysis.Unsupported, IssueUnsupportedEncapsulation)
				continue
			}
			portName = resolvedPort
			vlanID = resolvedVLAN
		default:
			isSupported = false
		}

		if !isSupported {
			c.addSkippedAt(c.routingInterfaceLookupScope(iface), iface.GetName(), "ip", "unsupported interface kind", analysis.Unsupported, IssueUnsupportedInterfaceKind)
			continue
		}

		if portName != "" {
			claimKey := portName + "\x00" + strconv.Itoa(int(vlanID))
			claimants[claimKey] = append(claimants[claimKey], iface.GetName())
		}

		pending = append(pending, pendingRoutedInterface{iface: iface, portName: portName, vlanID: vlanID})
	}

	return pending, claimants, vlanClaimants, contributions
}

func (c *loadContext) conflictingRoutedClaims(claimants map[string][]string, vlanClaimants map[vlan.ID][]string) map[string]struct{} {
	conflictedClaimants := make(map[string]struct{})
	for _, claimKey := range sortedKeys(claimants) {
		names := claimants[claimKey]
		if len(names) < 2 {
			continue
		}
		slices.Sort(names)

		portName, vlanStr, _ := strings.Cut(claimKey, "\x00")
		quoted := make([]string, len(names))
		for i, name := range names {
			quoted[i] = strconv.Quote(name)
		}
		c.addConflict(
			routing.PortLookupScope(c.src.DeviceID, routing.DefaultVRF, portName),
			"routed_interface_claim",
			fmt.Sprintf("%s VLAN %s", portName, vlanStr),
			"claimed by "+strings.Join(quoted, ", "),
			IssueConflictRoutedClaim,
		)
		for _, name := range names {
			conflictedClaimants[name] = struct{}{}
		}
	}

	vlanIDs := make([]vlan.ID, 0, len(vlanClaimants))
	for vid := range vlanClaimants {
		vlanIDs = append(vlanIDs, vid)
	}
	slices.Sort(vlanIDs)
	for _, vid := range vlanIDs {
		names := vlanClaimants[vid]
		if len(names) < 2 {
			continue
		}
		slices.Sort(names)

		quoted := make([]string, len(names))
		for i, name := range names {
			quoted[i] = strconv.Quote(name)
		}
		c.addConflict(
			routing.VLANLookupScope(c.src.DeviceID, routing.DefaultVRF, vid),
			"routed_interface_claim",
			fmt.Sprintf("vlan %d", vid),
			"claimed by "+strings.Join(quoted, ", "),
			IssueConflictRoutedClaim,
		)
		for _, name := range names {
			conflictedClaimants[name] = struct{}{}
		}
	}

	return conflictedClaimants
}

func (c *loadContext) loadRoutedInterfaces(pending []pendingRoutedInterface, conflictedClaimants map[string]struct{}, vrf *routing.VRF) {
	for _, p := range pending {
		if _, conflicted := conflictedClaimants[p.iface.GetName()]; conflicted {
			continue
		}

		ownershipScope := routing.OwnershipScope(c.src.DeviceID, routing.DefaultVRF, p.iface.GetName())
		ifaceMAC, errMAC := parseMAC(p.iface.GetMac())
		switch {
		case p.iface.GetMac() == nil:
			ifaceMAC = netaddr.MAC{}
			c.addDefaultAt(ownershipScope, p.iface.GetName(), "mac", "device base address")
		case errMAC == nil && ifaceMAC != (netaddr.MAC{}) && !ifaceMAC.IsGroup():
		default:
			ifaceMAC = netaddr.MAC{}
			why := "not a usable six-octet individual EUI-48 address"
			if errMAC != nil {
				why = errMAC.Error()
			}
			c.addSkippedAt(ownershipScope, p.iface.GetName(), "interface_mac", why, analysis.Incomplete, IssueInvalidMAC)
			c.addDefaultAt(ownershipScope, p.iface.GetName(), "mac", "device base address")
		}

		vrf.Interfaces[p.iface.GetName()] = routing.Interface{
			VLAN: p.vlanID,
			Port: p.portName,
			MAC:  ifaceMAC,
		}
	}
}

func (c *loadContext) loadAddresses(addrs []*ipv1.InterfaceAddress, vrf *routing.VRF) {
	addressRows, addressConflicts := resolveFacts(addrs, func(addr *ipv1.InterfaceAddress) (factKey, string, bool) {
		if addr == nil {
			return factKey{}, "", false
		}
		name := addr.GetInterfaceName()
		if _, ok := vrf.Interfaces[name]; !ok {
			c.addSkippedAt(c.routingScope, name, "ip_address", "interface carries no ip facet", analysis.Incomplete, IssueMissingIPFacet)
			return factKey{}, "", false
		}

		ip, err := parseIP(addr.GetAddress())
		if err != nil {
			c.addSkippedAt(c.routingScope, name, "ip_address", err.Error(), analysis.Incomplete, IssueInvalidIPAddress)
			return factKey{}, "", false
		}
		length, err := parsePrefix(addr.GetPrefix(), ip)
		if err != nil {
			c.addSkippedAt(c.routingScope, name, "ip_address", err.Error(), analysis.Incomplete, IssueInvalidPrefix)
			return factKey{}, "", false
		}
		key := name + "\x00" + ip.String()
		prefix := netip.PrefixFrom(ip, length)
		return factKey{id: key, display: name + "/" + ip.String(), scope: c.routingScope}, prefix.String(), true
	})
	for _, conflict := range addressConflicts {
		c.addConflict(conflict.key.scope, "ip_address", conflict.key.display, conflict.detail(), IssueConflictAddress)
	}
	claimedSubnets := make(map[netip.Prefix]string)
	for _, key := range sortedKeys(addressRows) {
		addr := addressRows[key]
		name := addr.GetInterfaceName()
		iface := vrf.Interfaces[name]
		ip, _ := parseIP(addr.GetAddress())
		length, _ := parsePrefix(addr.GetPrefix(), ip)
		prefix := netip.PrefixFrom(ip, length)
		subnet := prefix.Masked()
		if !subnet.Addr().IsLinkLocalUnicast() {
			if prev, ok := claimedSubnets[subnet]; ok && prev != name {
				c.addConflict(c.routingScope, "ip_address", name+"/"+ip.String(),
					fmt.Sprintf("subnet %s already claimed by %q", subnet, prev), IssueConflictAddress)
				continue
			}
			claimedSubnets[subnet] = name
		}
		iface.Prefixes = append(iface.Prefixes, prefix)
		vrf.Interfaces[name] = iface
	}
}

func (c *loadContext) loadNeighbors(neighbors []*ipv1.NeighborEntry, vrf *routing.VRF) {
	neighborRows, neighborConflicts := resolveFacts(neighbors, func(n *ipv1.NeighborEntry) (factKey, string, bool) {
		if n == nil {
			return factKey{}, "", false
		}
		name := n.GetInterfaceName()
		ip, errIP := parseIP(n.GetIp())
		scope := routing.NeighborTableScope(c.src.DeviceID, routing.DefaultVRF, name)
		if errIP == nil {
			scope = routing.NeighborLookupScope(c.src.DeviceID, routing.DefaultVRF, name, ip)
		}
		mac, errMAC := parseMAC(n.GetMac())
		if n.GetMac() == nil {
			c.addSkippedAt(scope, name, "ip_neighbor", "neighbor has no mac", analysis.Incomplete, IssueMissingNeighborMAC)
			return factKey{}, "", false
		}
		if errMAC != nil || mac == (netaddr.MAC{}) || mac.IsGroup() {
			why := "mac is not a usable six-octet individual EUI-48 address"
			if errMAC != nil {
				why = errMAC.Error()
			}
			c.addSkippedAt(scope, name, "ip_neighbor", why, analysis.Incomplete, IssueInvalidMAC)
			return factKey{}, "", false
		}

		if _, ok := vrf.Interfaces[name]; !ok {
			c.addSkippedAt(scope, name, "ip_neighbor", "interface carries no ip facet", analysis.Incomplete, IssueMissingIPFacet)
			return factKey{}, "", false
		}

		if errIP != nil {
			c.addSkippedAt(scope, name, "ip_neighbor", errIP.Error(), analysis.Incomplete, IssueInvalidIPAddress)
			return factKey{}, "", false
		}
		iface := vrf.Interfaces[name]
		matchingFamily := slices.ContainsFunc(iface.Prefixes, func(prefix netip.Prefix) bool {
			return prefix.Addr().Is4() == ip.Is4()
		})
		if !matchingFamily {
			c.addSkippedAt(scope, name, "ip_neighbor", "neighbor address family has no interface prefix", analysis.Incomplete, IssueInvalidNeighborAddress)
			return factKey{}, "", false
		}
		key := name + "\x00" + ip.String()
		return factKey{id: key, display: name + "/" + ip.String(), scope: scope}, mac.String(), true
	})
	for _, conflict := range neighborConflicts {
		c.addConflict(conflict.key.scope, "ip_neighbor", conflict.key.display, conflict.detail(), IssueConflictNeighbor)
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
}
