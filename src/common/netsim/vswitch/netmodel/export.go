package netmodel

import (
	"slices"
	"strconv"

	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
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

// Stp exports the switch spanning tree layer's runtime and administrative state
// as typed [stpv1.BridgeState] and [stpv1.PortState] messages.
//
// Stp returns nil, nil if the switch has no spanning tree configuration or layer.
// Administrative settings (bridge priority, bridge MAC address, port priority,
// admin path cost, admin edge, and point-to-point mode) are sourced from the switch's
// active STP configuration. Times in force (max age, hello time, forward delay)
// reflect the bridge's own configured times because root-advertised timer values on
// non-root bridges are not exposed by the runtime layer's port info.
func Stp(sw *vswitch.Switch) (*stpv1.BridgeState, []*stpv1.PortState) {
	if sw == nil {
		return nil, nil
	}
	cfg := sw.Config()
	if cfg.STP == nil {
		return nil, nil
	}
	roles := sw.Roles()
	if roles == nil {
		return nil, nil
	}

	rootID, rootPathCost, rootPort := sw.Root()
	tcCount, _ := sw.TopologyChanges()

	bridgePrio := uint32(cfg.STP.Priority)
	bridgeAddr := addrv1.Eui48Address_builder{
		Octets: cfg.STP.Address[:],
	}.Build()
	bridgeID := stpv1.BridgeId_builder{
		Priority: &bridgePrio,
		Address:  bridgeAddr,
	}.Build()

	desigRootPrio := uint32(rootID.Priority)
	desigRootAddr := addrv1.Eui48Address_builder{
		Octets: rootID.Address[:],
	}.Build()
	desigRoot := stpv1.BridgeId_builder{
		Priority: &desigRootPrio,
		Address:  desigRootAddr,
	}.Build()

	protoVer := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP

	helloTime := cfg.STP.HelloTime
	if helloTime == 0 {
		helloTime = stp.DefaultHelloTime
	}
	maxAge := cfg.STP.MaxAge
	if maxAge == 0 {
		maxAge = stp.DefaultMaxAge
	}
	fwdDelay := cfg.STP.ForwardDelay
	if fwdDelay == 0 {
		fwdDelay = stp.DefaultForwardDelay
	}

	helloDur := durationpb.New(helloTime)
	maxAgeDur := durationpb.New(maxAge)
	fwdDelayDur := durationpb.New(fwdDelay)

	bb := stpv1.BridgeState_builder{
		ProtocolVersion:    &protoVer,
		BridgeId:           bridgeID,
		DesignatedRoot:     desigRoot,
		RootPathCost:       &rootPathCost,
		MaxAge:             maxAgeDur,
		HelloTime:          helloDur,
		ForwardDelay:       fwdDelayDur,
		BridgeMaxAge:       maxAgeDur,
		BridgeHelloTime:    helloDur,
		BridgeForwardDelay: fwdDelayDur,
		TopologyChanges:    &tcCount,
	}
	if rootPort != "" {
		bb.RootPortInterfaceName = &rootPort
	}
	bridgeState := bb.Build()

	portNames := sortedKeys(roles)
	portStates := make([]*stpv1.PortState, 0, len(portNames))
	for _, name := range portNames {
		info := roles[name]
		pCfg := cfg.STP.Ports[name]

		var role stpv1.PortRole
		switch info.Role {
		case stp.RoleRoot:
			role = stpv1.PortRole_PORT_ROLE_ROOT
		case stp.RoleDesignated:
			role = stpv1.PortRole_PORT_ROLE_DESIGNATED
		case stp.RoleAlternate:
			role = stpv1.PortRole_PORT_ROLE_ALTERNATE
		case stp.RoleBackup:
			role = stpv1.PortRole_PORT_ROLE_BACKUP
		case stp.RoleDisabled:
			role = stpv1.PortRole_PORT_ROLE_DISABLED
		default:
			role = stpv1.PortRole_PORT_ROLE_UNSPECIFIED
		}

		var fwdState stpv1.ForwardingState
		switch info.State {
		case stp.StateDiscarding:
			fwdState = stpv1.ForwardingState_FORWARDING_STATE_DISCARDING
		case stp.StateLearning:
			fwdState = stpv1.ForwardingState_FORWARDING_STATE_LEARNING
		case stp.StateForwarding:
			fwdState = stpv1.ForwardingState_FORWARDING_STATE_FORWARDING
		default:
			fwdState = stpv1.ForwardingState_FORWARDING_STATE_UNSPECIFIED
		}

		var p2pMode stpv1.PointToPointMode
		switch pCfg.PointToPoint {
		case stp.PointToPointForceTrue:
			p2pMode = stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_TRUE
		case stp.PointToPointForceFalse:
			p2pMode = stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_FALSE
		default:
			p2pMode = stpv1.PointToPointMode_POINT_TO_POINT_MODE_AUTO
		}

		prio := uint32(pCfg.Priority)
		adminPathCost := pCfg.PathCost
		pathCost := info.PathCost
		adminEdge := pCfg.AdminEdge
		operEdge := info.Edge
		operP2P := info.PointToPoint
		fwdTransitions := info.ForwardTransitions

		pb := stpv1.PortState_builder{
			InterfaceName:      &name,
			Priority:           &prio,
			AdminPathCost:      &adminPathCost,
			PathCost:           &pathCost,
			Role:               &role,
			State:              &fwdState,
			DesignatedRoot:     desigRoot,
			AdminEdge:          &adminEdge,
			OperEdge:           &operEdge,
			PointToPoint:       &p2pMode,
			OperPointToPoint:   &operP2P,
			ForwardTransitions: &fwdTransitions,
		}

		if info.Designated != (stp.BridgeID{}) {
			desigBridgePrio := uint32(info.Designated.Priority)
			desigBridgeAddr := addrv1.Eui48Address_builder{
				Octets: info.Designated.Address[:],
			}.Build()
			pb.DesignatedBridge = stpv1.BridgeId_builder{
				Priority: &desigBridgePrio,
				Address:  desigBridgeAddr,
			}.Build()
		}

		desigCost := info.DesignatedCost
		pb.DesignatedCost = &desigCost
		desigPort := uint32(info.DesignatedPort)
		pb.DesignatedPort = &desigPort

		portStates = append(portStates, pb.Build())
	}

	return bridgeState, portStates
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
