package netmodel

import (
	"slices"
	"strconv"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
)

// FdbEntries converts active bridge forwarding database entries into typed network model
// [switchingv1.FdbEntry] messages.
//
// Each entry derives its VLAN identifier from FID, leaving it unset when FID is zero on a
// bridge without VLAN awareness. Dynamic entries map to [switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC]
// and static entries map to [switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC]. Usability status is
// always set to [switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE].
func FdbEntries(entries []bridge.Entry) []*switchingv1.FdbEntry {
	if len(entries) == 0 {
		return nil
	}
	res := make([]*switchingv1.FdbEntry, 0, len(entries))
	for _, e := range entries {
		eui48 := addrv1.Eui48Address_builder{
			Octets: e.MAC[:],
		}.Build()

		kind := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC
		if e.Static {
			kind = switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
		}
		status := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE

		b := switchingv1.FdbEntry_builder{
			Mac:    eui48,
			Kind:   &kind,
			Status: &status,
		}
		if e.Port != "" {
			p := e.Port
			b.InterfaceName = &p
		}
		if e.FID != 0 {
			vid := uint32(e.FID)
			b.VlanId = &vid
		}
		res = append(res, b.Build())
	}

	return res
}

// Poe converts physical PoE configurations and allocation results into typed network model
// [phyv1.PseBudget] messages and per-port [phyv1.PoeFacet] messages.
//
// Each exported facet indicates supported PSE role and attached powered-device class.
// Successfully allocated ports carry [phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER] and their
// allocated power in milliwatts. Denied ports carry [phyv1.PoeStatus_POE_STATUS_SEARCHING] with
// their allocated power field left unset.
func Poe(cfg phy.Config, alloc phy.Allocation) ([]*phyv1.PseBudget, map[string]*phyv1.PoeFacet) {
	if cfg.PoE == nil {
		return nil, nil
	}

	groupKeys := sortedKeys(cfg.PoE.Groups)
	budgets := make([]*phyv1.PseBudget, 0, len(groupKeys))
	for _, groupKey := range groupKeys {
		g := cfg.PoE.Groups[groupKey]
		groupNum, _ := strconv.ParseUint(groupKey, 10, 32)
		pseGroup := uint32(groupNum)
		b := phyv1.PseBudget_builder{
			PseGroup: &pseGroup,
		}
		if g.PowerMilliwatts > 0 {
			pmw := g.PowerMilliwatts
			b.PowerMilliwatts = &pmw
		}
		if allocGroup, ok := alloc.Groups[groupKey]; ok {
			consumption := allocGroup.AllocatedMilliwatts
			b.ConsumptionMilliwatts = &consumption
			operStatus := phyv1.PseOperStatus_PSE_OPER_STATUS_ON
			if allocGroup.AllocatedMilliwatts == 0 {
				operStatus = phyv1.PseOperStatus_PSE_OPER_STATUS_OFF
			}
			b.OperStatus = &operStatus
		}
		budgets = append(budgets, b.Build())
	}

	facets := make(map[string]*phyv1.PoeFacet, len(cfg.PoE.Ports))
	for _, portName := range sortedKeys(cfg.PoE.Ports) {
		p := cfg.PoE.Ports[portName]
		supported := true
		role := phyv1.PoeRole_POE_ROLE_PSE
		powerClass := uint32(p.PDClass)

		pa, ok := alloc.Ports[portName]
		isAllocated := ok && pa.Denial == "" && pa.Milliwatts > 0

		var status phyv1.PoeStatus
		if isAllocated {
			status = phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER
		} else {
			status = phyv1.PoeStatus_POE_STATUS_SEARCHING
		}

		fb := phyv1.PoeFacet_builder{
			Supported:  &supported,
			Role:       &role,
			PowerClass: &powerClass,
			Status:     &status,
		}
		if isAllocated {
			allocMW := pa.Milliwatts
			fb.AllocatedPowerMilliwatts = &allocMW
		}
		facets[portName] = fb.Build()
	}

	return budgets, facets
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
