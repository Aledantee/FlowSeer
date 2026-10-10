package netmodel

import (
	"bytes"
	"fmt"
	stdmaps "maps"
	"slices"
	"strconv"
	"strings"

	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// Issue codes used by MST translation.
const (
	IssueIncompleteMSTRegion   analysis.IssueCode = "netmodel.mst.incomplete_region"
	IssueIncompleteMSTInstance analysis.IssueCode = "netmodel.mst.incomplete_instance"
	IssueIncompleteMSTMap      analysis.IssueCode = "netmodel.mst.incomplete_map"
	IssueConflictMSTInstance   analysis.IssueCode = "netmodel.mst.instance_conflict"
	IssueConflictMSTMap        analysis.IssueCode = "netmodel.mst.map_conflict"
)

func (c *loadContext) loadMST(bridge *stpv1.BridgeState, instances []*stpv1.MstInstance, mstPorts []*stpv1.MstPort, maps []*stpv1.MstVlanMap, common map[string]stp.Port, ports port.Table) (*stp.MST, []analysis.Issue) {
	start := len(c.issues)
	m := c.loadMSTRegion(bridge)
	if m != nil {
		conflicted := c.loadMSTInstances(bridge, instances, m)
		c.loadMSTMaps(maps, m, conflicted)
		for _, row := range mstPorts {
			if row != nil {
				if _, ok := m.Instances[bpdu.MSTID(row.GetMstId())]; row.GetMstId() > 4094 || !ok {
					status := analysis.Incomplete
					if conflicted[fmt.Sprint(row.GetMstId())] {
						status = analysis.Unstable
					}
					c.addSkippedAt(c.mstScope("instances", fmt.Sprint(row.GetMstId()), "ports", row.GetInterfaceName()), row.GetInterfaceName(), "mst_port", "MST instance is unavailable", status, IssueIncompleteMSTInstance)
				}
			}
		}
		if len(m.Instances) > bpdu.MaxMSTIRecords {
			c.addSkippedAt(c.mstScope("instances"), "", "mst_region", "more than 64 instances cannot execute", analysis.Incomplete, IssueIncompleteMSTRegion)
			m = nil
		} else if digest := m.ConfigID().Digest; !bytes.Equal(digest[:], bridge.GetMstConfigId().GetDigest()) {
			c.addSkippedAt(c.mstScope("mst_config_id", "digest"), "", "mst_region", "reconstructed VLAN allocation differs from reported digest", analysis.Incomplete, IssueIncompleteMSTRegion)
			m = nil
		} else if err := m.Validate(ports, common); err != nil {
			c.addSkippedAt(c.mstScope(), "", "mst_region", err.Error(), analysis.Incomplete, IssueIncompleteMSTRegion)
			m = nil
		}
	}
	if m == nil {
		c.refuseMSTDependents(instances, maps, mstPorts)
	}
	return m, slices.Clone(c.issues[start:])
}

func (c *loadContext) loadMSTRegion(bridge *stpv1.BridgeState) *stp.MST {
	if bridge == nil {
		c.addSkippedAt(c.mstScope("mst_config_id"), "", "mst_region", "bridge state is missing", analysis.Incomplete, IssueIncompleteMSTRegion)
		return nil
	}
	if !c.acceptInstance(bridge, "mst_region", "0", "") {
		return nil
	}
	identity := bridge.GetMstConfigId()
	field, why := "", ""
	switch {
	case identity == nil:
		field, why = "mst_config_id", "MST configuration identity is missing"
	case !identity.HasName():
		field, why = "name", "region name is unreported"
	case len(identity.GetName()) > 32 || strings.ContainsRune(identity.GetName(), 0):
		field, why = "name", "region name exceeds 32 octets or contains NUL"
	case !identity.HasRevisionLevel():
		field, why = "revision_level", "region revision is unreported"
	case identity.GetRevisionLevel() > 65535:
		field, why = "revision_level", "region revision exceeds 65535"
	case !identity.HasDigest():
		field, why = "digest", "region digest is unreported"
	case len(identity.GetDigest()) != 16:
		field, why = "digest", "region digest must contain 16 octets"
	case bridge.HasMaxHops() && (bridge.GetMaxHops() < 6 || bridge.GetMaxHops() > 40):
		field, why = "max_hops", "maximum hops must be 6 through 40"
	}
	if why != "" {
		scope := c.mstScope("mst_config_id", field)
		if field == "mst_config_id" || field == "max_hops" {
			scope = c.mstScope(field)
		}
		c.addSkippedAt(scope, "", "mst_region", why, analysis.Incomplete, IssueIncompleteMSTRegion)
		return nil
	}
	maxHops := uint8(bridge.GetMaxHops())
	if !bridge.HasMaxHops() {
		// stp.MST.Normalize uses 20 (src/common/sim/layer/stp/config.go).
		maxHops = 20
		c.addDefaultAt(c.mstScope("max_hops"), "", "max_hops", "20")
	}
	return &stp.MST{Name: identity.GetName(), Revision: uint16(identity.GetRevisionLevel()), MaxHops: maxHops, Instances: make(map[bpdu.MSTID]stp.Instance)}
}

func (c *loadContext) loadMSTInstances(bridge *stpv1.BridgeState, instances []*stpv1.MstInstance, m *stp.MST) map[string]bool {
	rows, conflicts := resolveFacts(instances, func(row *stpv1.MstInstance) (factKey, string, bool) {
		if row == nil {
			return factKey{}, "", false
		}
		id := strconv.FormatUint(uint64(row.GetMstId()), 10)
		if !c.acceptInstance(row, "mst_instance", id, "") {
			return factKey{}, "", false
		}
		field, why := validateMSTInstance(bridge, row)
		if why != "" {
			c.addSkippedAt(c.mstScope("instances", id, field), "", "mst_instance", why, analysis.Incomplete, IssueIncompleteMSTInstance)
			return factKey{}, "", false
		}
		priority := "unreported"
		if row.GetBridgeId().HasPriority() {
			priority = fmt.Sprint(row.GetBridgeId().GetPriority())
		}
		value := fmt.Sprintf("address=%x,priority=%s", row.GetBridgeId().GetAddress().GetOctets(), priority)
		return factKey{id: id, display: id, scope: c.mstScope("instances", id)}, value, true
	})
	conflicted := make(map[string]bool, len(conflicts))
	for _, conflict := range conflicts {
		conflicted[conflict.key.id] = true
		c.addConflict(conflict.key.scope, "mst_instance", conflict.key.display, conflict.detail(), IssueConflictMSTInstance)
	}
	for _, key := range sortedKeys(rows) {
		row := rows[key]
		id := row.GetBridgeId()
		m.Instances[bpdu.MSTID(row.GetMstId())] = stp.Instance{Priority: uint16(id.GetPriority()), PriorityPresent: id.HasPriority()}
		if !id.HasPriority() {
			c.addDefaultAt(c.mstScope("instances", key, "priority"), "", "priority", fmt.Sprint(stp.DefaultBridgePriority))
		}
	}
	return conflicted
}

func validateMSTInstance(bridge *stpv1.BridgeState, row *stpv1.MstInstance) (string, string) {
	if !row.HasMstId() || row.GetMstId() < 1 || row.GetMstId() > 4094 {
		return "mst_id", "instance id must be present and 1 through 4094"
	}
	id := row.GetBridgeId()
	if id == nil {
		return "bridge_id", "instance bridge id is missing"
	}
	mac, errAddress := parseEUI48(id.GetAddress())
	bridgeMAC, errBridgeAddress := parseEUI48(bridge.GetBridgeId().GetAddress())
	if errAddress != nil || errBridgeAddress != nil || mac != bridgeMAC {
		return "address", "instance address must identify this bridge with six octets"
	}
	if id.HasPriority() && (id.GetPriority() > 61440 || id.GetPriority()%4096 != 0) {
		return "priority", "instance priority must be 0 through 61440 in steps of 4096"
	}
	return "", ""
}

func (c *loadContext) loadMSTMaps(maps []*stpv1.MstVlanMap, m *stp.MST, conflicted map[string]bool) {
	rows, conflicts := resolveFacts(maps, func(row *stpv1.MstVlanMap) (factKey, string, bool) {
		if row == nil {
			return factKey{}, "", false
		}
		id := fmt.Sprint(row.GetMstId())
		if !c.acceptInstance(row, "mst_vlan_map", id, "") {
			return factKey{}, "", false
		}
		scope := c.mstScope("vlan_maps", id)
		if !row.HasMstId() || row.GetMstId() > 4094 {
			c.addSkippedAt(c.mstScope("vlan_maps", id, "mst_id"), "", "mst_vlan_map", "map target must be present and 0 through 4094", analysis.Incomplete, IssueIncompleteMSTMap)
			return factKey{}, "", false
		}
		if _, ok := m.Instances[bpdu.MSTID(row.GetMstId())]; row.GetMstId() != 0 && !ok {
			status := analysis.Incomplete
			if conflicted[id] {
				status = analysis.Unstable
			}
			c.addSkippedAt(scope, "", "mst_vlan_map", "target instance is unavailable", status, IssueIncompleteMSTMap)
			return factKey{}, "", false
		}
		vids := slices.Clone(row.GetVlanIds())
		slices.Sort(vids)
		for i, vid := range vids {
			if vid < 1 || vid > 4094 {
				c.addSkippedAt(c.mstScope("vlan_maps", id, "vlan_ids"), "", "mst_vlan_map", "VLAN id must be 1 through 4094", analysis.Incomplete, IssueIncompleteMSTMap)
				return factKey{}, "", false
			}
			if i > 0 && vid == vids[i-1] {
				c.addConflict(c.mstScope("vlan_maps", id, "vlan_ids"), "mst_vlan_map", id, fmt.Sprintf("VLAN %d is repeated within allocation", vid), IssueConflictMSTMap)
				return factKey{}, "", false
			}
		}
		return factKey{id: id, display: id, scope: scope}, fmt.Sprint(vids), true
	})
	for _, conflict := range conflicts {
		c.addConflict(conflict.key.scope, "mst_vlan_map", conflict.key.display, conflict.detail(), IssueConflictMSTMap)
	}
	claims := make(map[uint32][]string)
	for _, key := range sortedKeys(rows) {
		for _, vid := range rows[key].GetVlanIds() {
			claims[vid] = append(claims[vid], key)
		}
	}
	for _, key := range sortedKeys(rows) {
		row := rows[key]
		inst := m.Instances[bpdu.MSTID(row.GetMstId())]
		vids := slices.Clone(row.GetVlanIds())
		slices.Sort(vids)
		for _, vid := range vids {
			if len(claims[vid]) > 1 {
				c.addConflict(c.mstScope("vlan_maps", key, "vlan_ids"), "mst_vlan_map", key, fmt.Sprintf("VLAN %d is claimed by trees %v", vid, claims[vid]), IssueConflictMSTMap)
				continue
			}
			if row.GetMstId() != 0 {
				inst.VLANs = append(inst.VLANs, vlan.ID(vid))
			}
		}
		if row.GetMstId() != 0 {
			m.Instances[bpdu.MSTID(row.GetMstId())] = inst
		}
	}
	for _, id := range slices.Sorted(stdmaps.Keys(m.Instances)) {
		if _, ok := rows[fmt.Sprint(id)]; !ok {
			c.addSkippedAt(c.mstScope("instances", fmt.Sprint(id), "vlan_maps"), "", "mst_instance", "instance allocation is unreported or refused", analysis.Incomplete, IssueIncompleteMSTMap)
			delete(m.Instances, id)
		}
	}
}

func (c *loadContext) refuseMSTDependents(instances []*stpv1.MstInstance, maps []*stpv1.MstVlanMap, ports []*stpv1.MstPort) {
	for _, row := range instances {
		if row != nil {
			c.addSkippedAt(c.mstScope("instances", fmt.Sprint(row.GetMstId())), "", "mst_instance", "MST region is unavailable", analysis.Incomplete, IssueIncompleteMSTRegion)
		}
	}
	for _, row := range maps {
		if row != nil {
			c.addSkippedAt(c.mstScope("vlan_maps", fmt.Sprint(row.GetMstId())), "", "mst_vlan_map", "MST region is unavailable", analysis.Incomplete, IssueIncompleteMSTRegion)
		}
	}
	for _, row := range ports {
		if row != nil {
			c.addSkippedAt(c.mstScope("instances", fmt.Sprint(row.GetMstId()), "ports", row.GetInterfaceName()), row.GetInterfaceName(), "mst_port", "MST region is unavailable", analysis.Incomplete, IssueIncompleteMSTRegion)
		}
	}
}
