package netmodel

import (
	"slices"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
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
// Each exported facet indicates supported PSE role and delivery status.
// A port granted power carries [phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER], its
// power class, and its allocated milliwatts. A port denied power due to administrative
// disablement carries [phyv1.PoeStatus_POE_STATUS_DISABLED]. A port with no attached
// powered device, or denied power for budget, limit, or unsupported class, carries
// [phyv1.PoeStatus_POE_STATUS_SEARCHING] without a power class. An uncertain power
// allocation carries [phyv1.PoeStatus_POE_STATUS_UNSPECIFIED]. The message keys a PSE
// group by a positive integer, so a group key that is not one is an error.
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

		pa := alloc.Ports[portName]

		var status phyv1.PoeStatus
		switch {
		case pa.State == phy.PowerDelivered:
			status = phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER
		case pa.State == phy.PowerDenied && pa.Denial == phy.ReasonDisabled:
			status = phyv1.PoeStatus_POE_STATUS_DISABLED
		case pa.State == phy.PowerNoDevice || pa.State == phy.PowerDenied:
			status = phyv1.PoeStatus_POE_STATUS_SEARCHING
		default:
			// PowerUnknown, and a port Allocate did not report, carry the
			// explicit unknown status rather than a guess.
			status = phyv1.PoeStatus_POE_STATUS_UNSPECIFIED
		}

		fb := phyv1.PoeFacet_builder{
			Supported: &supported,
			Role:      &role,
			Status:    &status,
		}
		if status == phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER {
			if p.PDClass != nil {
				powerClass := uint32(*p.PDClass)
				fb.PowerClass = &powerClass
			}
			allocMW := pa.MaxMilliwatts
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
// The bridge identifier, port priorities, and the bridge's own times carry the
// values in effect, so a configuration that left them zero exports the
// defaults the layer runs with. The times in force are the root's as received
// on the root port, and time_since_topology_change is measured from now.
func Stp(now time.Time, sw *vswitch.Switch) (*stpv1.BridgeState, []*stpv1.PortState) {
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
	tcCount, lastTC := sw.TopologyChanges()
	maxAge, helloTime, fwdDelay := sw.Times()

	protoVer := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP

	bridgeHello := cfg.STP.HelloTime
	if bridgeHello == 0 {
		bridgeHello = stp.DefaultHelloTime
	}
	bridgeMaxAge := cfg.STP.MaxAge
	if bridgeMaxAge == 0 {
		bridgeMaxAge = stp.DefaultMaxAge
	}
	bridgeFwdDelay := cfg.STP.ForwardDelay
	if bridgeFwdDelay == 0 {
		bridgeFwdDelay = stp.DefaultForwardDelay
	}
	txHoldCount := uint32(cfg.STP.TxHoldCount)
	if txHoldCount == 0 {
		txHoldCount = uint32(stp.DefaultTxHoldCount)
	}

	bb := stpv1.BridgeState_builder{
		ProtocolVersion:    &protoVer,
		BridgeId:           bridgeIDMessage(sw.BridgeID()),
		DesignatedRoot:     bridgeIDMessage(rootID),
		RootPathCost:       &rootPathCost,
		MaxAge:             durationpb.New(maxAge),
		HelloTime:          durationpb.New(helloTime),
		ForwardDelay:       durationpb.New(fwdDelay),
		BridgeMaxAge:       durationpb.New(bridgeMaxAge),
		BridgeHelloTime:    durationpb.New(bridgeHello),
		BridgeForwardDelay: durationpb.New(bridgeFwdDelay),
		TopologyChanges:    &tcCount,
		TxHoldCount:        &txHoldCount,
	}
	if rootPort != "" {
		bb.RootPortInterfaceName = &rootPort
	}
	if !lastTC.IsZero() {
		bb.TimeSinceTopologyChange = durationpb.New(now.Sub(lastTC))
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

		prio := uint32(info.Priority)
		adminPathCost := pCfg.PathCost
		pathCost := info.PathCost
		adminEdge := pCfg.AdminEdge
		operEdge := info.Edge
		operP2P := info.PointToPoint
		fwdTransitions := info.ForwardTransitions
		autoEdge := pCfg.AutoEdge
		operProtoVer := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
		if !info.SendRSTP {
			operProtoVer = stpv1.ProtocolVersion_PROTOCOL_VERSION_STP
		}
		txBpdus := info.TxBPDUs
		rxBpdus := info.RxBPDUs
		badBpdus := info.BadBPDUs

		pb := stpv1.PortState_builder{
			InterfaceName:       &name,
			Priority:            &prio,
			AdminPathCost:       &adminPathCost,
			PathCost:            &pathCost,
			Role:                &role,
			State:               &fwdState,
			AdminEdge:           &adminEdge,
			OperEdge:            &operEdge,
			PointToPoint:        &p2pMode,
			OperPointToPoint:    &operP2P,
			ForwardTransitions:  &fwdTransitions,
			AutoEdge:            &autoEdge,
			OperProtocolVersion: &operProtoVer,
			TxBpdus:             &txBpdus,
			RxBpdus:             &rxBpdus,
			BadBpdus:            &badBpdus,
		}

		if info.DesignatedRoot != (stp.BridgeID{}) {
			pb.DesignatedRoot = bridgeIDMessage(info.DesignatedRoot)
		}
		if info.Designated != (stp.BridgeID{}) {
			pb.DesignatedBridge = bridgeIDMessage(info.Designated)
		}

		desigCost := info.DesignatedCost
		pb.DesignatedCost = &desigCost
		desigPort := uint32(info.DesignatedPort)
		pb.DesignatedPort = &desigPort

		portStates = append(portStates, pb.Build())
	}

	return bridgeState, portStates
}

// bridgeIDMessage builds a fresh message per call, so no two rows share one.
func bridgeIDMessage(id stp.BridgeID) *stpv1.BridgeId {
	prio := uint32(id.Priority)

	return stpv1.BridgeId_builder{
		Priority: &prio,
		Address:  addrv1.Eui48Address_builder{Octets: id.Address[:]}.Build(),
	}.Build()
}

// Lacp exports the switch link aggregation layer's runtime and administrative state
// as typed [lacpv1.AggregatorState] and [lacpv1.PortState] messages.
//
// Lacp returns nil, nil if the switch has no link aggregation group ports.
func Lacp(sw *vswitch.Switch) ([]*lacpv1.AggregatorState, []*lacpv1.PortState) {
	if sw == nil {
		return nil, nil
	}
	ports := sw.Ports()
	var lagPortNames []string
	for _, p := range ports.Ports() {
		if p.Kind == port.Lag {
			lagPortNames = append(lagPortNames, p.Name)
		}
	}
	if len(lagPortNames) == 0 {
		return nil, nil
	}
	slices.Sort(lagPortNames)

	cfg := sw.Config()
	lagCfg := lag.Config{}
	if cfg.LAG != nil {
		lagCfg = *cfg.LAG
	}
	effective := lagCfg.Defaults(ports, cfg.MAC)

	aggregators := make([]*lacpv1.AggregatorState, 0, len(lagPortNames))
	var portStates []*lacpv1.PortState

	for _, lagName := range lagPortNames {
		lCfg := effective.LAGs[lagName]
		info := sw.LagInfo(lagName)

		var mode lacpv1.LacpMode
		switch lCfg.LACP.Mode {
		case lag.Active:
			mode = lacpv1.LacpMode_LACP_MODE_ACTIVE
		case lag.Passive:
			mode = lacpv1.LacpMode_LACP_MODE_PASSIVE
		case lag.Off:
			mode = lacpv1.LacpMode_LACP_MODE_OFF
		default:
			mode = lacpv1.LacpMode_LACP_MODE_OFF
		}

		prio := uint32(lCfg.LACP.SystemPriority)
		key := uint32(lCfg.LACP.Key)
		fast := lCfg.LACP.Fast
		fallback := lCfg.LACP.Fallback

		aggName := lagName
		ab := lacpv1.AggregatorState_builder{
			InterfaceName:        &aggName,
			Mode:                 &mode,
			Fast:                 &fast,
			SystemPriority:       &prio,
			Key:                  &key,
			FallbackActiveBackup: &fallback,
		}
		if lCfg.LACP.SystemID != (netaddr.MAC{}) {
			ab.SystemId = addrv1.Eui48Address_builder{Octets: lCfg.LACP.SystemID[:]}.Build()
		}
		if info.PartnerSystemID != (netaddr.MAC{}) {
			ab.PartnerSystemId = addrv1.Eui48Address_builder{Octets: info.PartnerSystemID[:]}.Build()
		}
		if info.PartnerSystemPriority != 0 {
			partnerPrio := uint32(info.PartnerSystemPriority)
			ab.PartnerSystemPriority = &partnerPrio
		}
		if info.PartnerKey != 0 {
			partnerKey := uint32(info.PartnerKey)
			ab.PartnerKey = &partnerKey
		}
		if len(info.Enabled) > 0 {
			sel := slices.Clone(info.Enabled)
			slices.Sort(sel)
			ab.SelectedMembers = sel
		}

		aggregators = append(aggregators, ab.Build())

		members := ports.Members(lagName)
		var memberNames []string
		for _, m := range members {
			memberNames = append(memberNames, m.Name)
		}
		slices.Sort(memberNames)

		for _, memName := range memberNames {
			memInfo := sw.MemberInfo(memName)
			mCfg := lCfg.Members[memName]

			memPortPrio := uint32(mCfg.Priority)
			memKey := uint32(mCfg.Key)

			actorSysPrio := uint32(memInfo.Actor.SystemPriority)
			actorKey := uint32(memInfo.Actor.Key)
			actorPortPrio := uint32(memInfo.Actor.PortPriority)
			actorPortID := uint32(memInfo.Actor.PortID)
			actorStateBits := mapStateBits(memInfo.Actor.State)

			actorBuilder := lacpv1.LacpInfo_builder{
				SystemPriority: &actorSysPrio,
				Key:            &actorKey,
				PortPriority:   &actorPortPrio,
				PortId:         &actorPortID,
				State:          actorStateBits,
			}
			if memInfo.Actor.SystemID != (netaddr.MAC{}) {
				actorBuilder.SystemId = addrv1.Eui48Address_builder{Octets: memInfo.Actor.SystemID[:]}.Build()
			}

			var partner *lacpv1.LacpInfo
			if memInfo.Partner != (lacp.Info{}) {
				partnerSysPrio := uint32(memInfo.Partner.SystemPriority)
				partnerKey := uint32(memInfo.Partner.Key)
				partnerPortPrio := uint32(memInfo.Partner.PortPriority)
				partnerPortID := uint32(memInfo.Partner.PortID)
				partnerStateBits := mapStateBits(memInfo.Partner.State)

				pb := lacpv1.LacpInfo_builder{
					SystemPriority: &partnerSysPrio,
					Key:            &partnerKey,
					PortPriority:   &partnerPortPrio,
					PortId:         &partnerPortID,
					State:          partnerStateBits,
				}
				if memInfo.Partner.SystemID != (netaddr.MAC{}) {
					pb.SystemId = addrv1.Eui48Address_builder{Octets: memInfo.Partner.SystemID[:]}.Build()
				}
				partner = pb.Build()
			}

			var status lacpv1.LacpStatus
			switch memInfo.Status {
			case lag.Current:
				status = lacpv1.LacpStatus_LACP_STATUS_CURRENT
			case lag.Expired:
				status = lacpv1.LacpStatus_LACP_STATUS_EXPIRED
			case lag.Defaulted:
				status = lacpv1.LacpStatus_LACP_STATUS_DEFAULTED
			default:
				status = lacpv1.LacpStatus_LACP_STATUS_UNSPECIFIED
			}

			attached := memInfo.Attached
			enabled := memInfo.Enabled
			tx := memInfo.LACPDUsTx
			rx := memInfo.LACPDUsRx
			bad := memInfo.BadLACPDUs

			mName := memName
			pLagName := lagName
			psb := lacpv1.PortState_builder{
				InterfaceName:           &mName,
				AggregatorInterfaceName: &pLagName,
				PortPriority:            &memPortPrio,
				Key:                     &memKey,
				Actor:                   actorBuilder.Build(),
				Partner:                 partner,
				Status:                  &status,
				Attached:                &attached,
				Enabled:                 &enabled,
				LacpdusTx:               &tx,
				LacpdusRx:               &rx,
				BadLacpdus:              &bad,
			}
			portStates = append(portStates, psb.Build())
		}
	}

	return aggregators, portStates
}

func mapStateBits(st lacp.State) []lacpv1.LacpStateBit {
	var bits []lacpv1.LacpStateBit
	if st&lacp.StateActive != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_ACTIVITY)
	}
	if st&lacp.StateShortTimeout != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_TIMEOUT)
	}
	if st&lacp.StateAggregation != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_AGGREGATION)
	}
	if st&lacp.StateSynchronization != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_SYNCHRONIZATION)
	}
	if st&lacp.StateCollecting != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_COLLECTING)
	}
	if st&lacp.StateDistributing != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_DISTRIBUTING)
	}
	if st&lacp.StateDefaulted != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_DEFAULTED)
	}
	if st&lacp.StateExpired != 0 {
		bits = append(bits, lacpv1.LacpStateBit_LACP_STATE_BIT_EXPIRED)
	}

	return bits
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
