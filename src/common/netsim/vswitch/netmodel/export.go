package netmodel

import (
	"slices"
	"strconv"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
)

// FdbEntries converts active bridge forwarding database entries into typed network model
// [switchingv1.FdbEntry] messages.
//
// The message requires a VLAN id, so the entries of a bridge without VLAN awareness,
// which live under filtering database 0, have no row shape and make FdbEntries return
// an error rather than a message the schema refuses. Dynamic entries map to
// [switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC] and static entries to
// [switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC]; the status is always
// [switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE].
func FdbEntries(entries []bridge.Entry) ([]*switchingv1.FdbEntry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	res := make([]*switchingv1.FdbEntry, 0, len(entries))
	for _, e := range entries {
		if e.FID == 0 {
			return nil, errs.New().
				Attr("mac", e.MAC.String()).
				Attr("port", e.Port).
				Msg("an entry of a bridge without VLAN awareness has no FdbEntry shape")
		}
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
		vid := uint32(e.FID)
		b.VlanId = &vid
		res = append(res, b.Build())
	}

	return res, nil
}

// Poe converts physical PoE configurations and allocation results into typed network model
// [phyv1.PseBudget] messages and per-port [phyv1.PoeFacet] messages.
//
// Each exported facet indicates supported PSE role and attached powered-device class.
// A port granted power carries [phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER] and its
// allocated milliwatts; a port with no attached device, or one denied for budget or
// limit, is [phyv1.PoeStatus_POE_STATUS_SEARCHING], since the PSE keeps probing and
// would power it when capacity returns; a port whose delivery is disabled is
// [phyv1.PoeStatus_POE_STATUS_DISABLED]; a device of a class the port cannot source is
// [phyv1.PoeStatus_POE_STATUS_FAULT]. The message keys a PSE group by a positive
// integer, so a group key that is not one is an error.
func Poe(cfg phy.Config, alloc phy.Allocation) ([]*phyv1.PseBudget, map[string]*phyv1.PoeFacet, error) {
	if cfg.PoE == nil {
		return nil, nil, nil
	}

	groupKeys := sortedKeys(cfg.PoE.Groups)
	budgets := make([]*phyv1.PseBudget, 0, len(groupKeys))
	for _, groupKey := range groupKeys {
		g := cfg.PoE.Groups[groupKey]
		groupNum, err := strconv.ParseUint(groupKey, 10, 32)
		if err != nil || groupNum == 0 {
			return nil, nil, errs.New().
				Attr("group", groupKey).
				Msg("a PSE group key must be a positive integer to export as pse_group")
		}
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

		pa, ok := alloc.Ports[portName]
		isAllocated := ok && pa.Denial == "" && pa.Milliwatts > 0

		var status phyv1.PoeStatus
		switch {
		case isAllocated:
			status = phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER
		case pa.Denial == phy.ReasonDisabled:
			status = phyv1.PoeStatus_POE_STATUS_DISABLED
		case pa.Denial == phy.ReasonClassUnsupported:
			status = phyv1.PoeStatus_POE_STATUS_FAULT
		default:
			status = phyv1.PoeStatus_POE_STATUS_SEARCHING
		}

		fb := phyv1.PoeFacet_builder{
			Supported: &supported,
			Role:      &role,
			Status:    &status,
		}
		if p.PDClass != nil {
			powerClass := uint32(*p.PDClass)
			fb.PowerClass = &powerClass
		}
		if isAllocated {
			allocMW := pa.Milliwatts
			fb.AllocatedPowerMilliwatts = &allocMW
		}
		facets[portName] = fb.Build()
	}

	return budgets, facets, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
