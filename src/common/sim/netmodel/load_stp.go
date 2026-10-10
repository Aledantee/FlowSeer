package netmodel

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Issue codes used by netmodel.
const (
	IssueConflictSTPPort               analysis.IssueCode = "netmodel.stp.conflict"
	IssueUnsupportedSTPProtocolVersion analysis.IssueCode = "netmodel.stp.unsupported_protocol_version"
	IssueMissingSTPProtocolVersion     analysis.IssueCode = "netmodel.stp.missing_protocol_version"
	IssueMissingSTPBridgeState         analysis.IssueCode = "netmodel.stp.missing_bridge_state"
	IssueInvalidPointToPointMode       analysis.IssueCode = "netmodel.stp.invalid_point_to_point_mode"
	IssueInvalidAdminPathCost          analysis.IssueCode = "netmodel.stp.invalid_admin_path_cost"
	IssueInvalidPortPriority           analysis.IssueCode = "netmodel.stp.invalid_port_priority"
	IssueInvalidTxHoldCount            analysis.IssueCode = "netmodel.stp.invalid_tx_hold_count"
	IssueMissingBridgeAddress          analysis.IssueCode = "netmodel.stp.missing_bridge_address"
	IssueInvalidSTPBridgeTimer         analysis.IssueCode = "netmodel.stp.invalid_bridge_timer"
	IssueInvalidBridgePriority         analysis.IssueCode = "netmodel.stp.invalid_bridge_priority"
)

func (c *loadContext) reportMissingSTP(bridgeState *stpv1.BridgeState, stpPorts []*stpv1.PortState) {
	stpRequestedWithoutBridge := c.isWanted(stp.LayerName) && bridgeState == nil
	if bridgeState == nil && (stpRequestedWithoutBridge || len(stpPorts) > 0) {
		if stpRequestedWithoutBridge {
			c.addSkippedAt(c.stpScope, "", "stp_bridge", "requested STP layer has no bridge state", analysis.Incomplete, IssueMissingSTPBridgeState)
		}
		for _, ps := range stpPorts {
			if ps == nil {
				continue
			}
			c.addSkippedAt(c.stpPortScope(ps.GetInterfaceName()), ps.GetInterfaceName(), "stp_port", "bridge state is missing", analysis.Incomplete, IssueMissingSTPBridgeState)
		}
		c.report.Capabilities = slices.DeleteFunc(c.report.Capabilities, func(layer trace.Layer) bool {
			return layer == stp.LayerName
		})
		delete(c.report.CapabilitySources, stp.LayerName)
	}
}

func (c *loadContext) reportUnwantedSTP(bridgeState *stpv1.BridgeState, stpPorts []*stpv1.PortState) {
	if !c.isWanted(stp.LayerName) && bridgeState != nil {
		c.addSkippedAt(c.stpScope, "", "stp_bridge", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		for _, ps := range stpPorts {
			if ps == nil {
				continue
			}
			c.addSkippedAt(c.stpPortScope(ps.GetInterfaceName()), ps.GetInterfaceName(), "stp_port", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
	}
}

func (c *loadContext) loadSTP(bridgeState *stpv1.BridgeState, stpPorts []*stpv1.PortState, ports port.Table, ownership loadOwnership) *stp.Config {
	var loaded *stp.Config
	if c.isWanted(stp.LayerName) && bridgeState != nil {
		loaded = c.loadSTPBridge(bridgeState, stpPorts, ports, ownership)
	}
	if c.isWanted(stp.LayerName) && loaded == nil {
		c.report.Capabilities = slices.DeleteFunc(c.report.Capabilities, func(layer trace.Layer) bool {
			return layer == stp.LayerName
		})
		delete(c.report.CapabilitySources, stp.LayerName)
	}

	return loaded
}

func (c *loadContext) loadSTPBridge(bridgeState *stpv1.BridgeState, stpPorts []*stpv1.PortState, ports port.Table, ownership loadOwnership) *stp.Config {
	bridgeAddress := bridgeState.GetBridgeId().GetAddress()
	mac, errAddress := parseEUI48(bridgeAddress)
	prio := bridgeState.GetBridgeId().GetPriority()
	priorityPresent := bridgeState.GetBridgeId() != nil && bridgeState.GetBridgeId().HasPriority()

	timerWhy := validateSTPTimers(bridgeState)
	var (
		bridgeWhy    string
		bridgeCode   analysis.IssueCode
		bridgeStatus analysis.Status
	)
	switch {
	case !bridgeState.HasProtocolVersion() || bridgeState.GetProtocolVersion() == stpv1.ProtocolVersion_PROTOCOL_VERSION_UNSPECIFIED:
		bridgeWhy = "protocol version is unreported or UNSPECIFIED"
		bridgeCode = IssueMissingSTPProtocolVersion
		bridgeStatus = analysis.Incomplete
	case bridgeState.GetProtocolVersion() != stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP:
		bridgeWhy = fmt.Sprintf("protocol version %d is not supported by the RSTP layer", bridgeState.GetProtocolVersion())
		bridgeCode = IssueUnsupportedSTPProtocolVersion
		bridgeStatus = analysis.Unsupported
	case bridgeAddress == nil:
		bridgeWhy = "bridge id has no address"
		bridgeCode = IssueMissingBridgeAddress
		bridgeStatus = analysis.Unsupported
	case errAddress != nil || mac.IsGroup():
		bridgeWhy = "bridge id address is not a usable six-octet individual EUI-48 address"
		if errAddress != nil {
			bridgeWhy = errAddress.Error()
		}
		bridgeCode = IssueInvalidMAC
		bridgeStatus = analysis.Incomplete
	case mac == (netaddr.MAC{}):
		bridgeWhy = "bridge id has no address"
		bridgeCode = IssueMissingBridgeAddress
		bridgeStatus = analysis.Unsupported
	case priorityPresent && (prio >= 65536 || prio%4096 != 0):
		bridgeWhy = "bridge priority is not a multiple of 4096 below 65536"
		bridgeCode = IssueInvalidBridgePriority
		bridgeStatus = analysis.Unsupported
	case timerWhy != "":
		bridgeWhy = timerWhy
		bridgeCode = IssueInvalidSTPBridgeTimer
		bridgeStatus = analysis.Unsupported
	}
	if bridgeWhy != "" {
		c.addSkippedAt(c.stpScope, "", "stp_bridge", bridgeWhy, bridgeStatus, bridgeCode)
		for _, ps := range stpPorts {
			if ps == nil {
				continue
			}
			c.addSkippedAt(
				c.stpPortScope(ps.GetInterfaceName()),
				ps.GetInterfaceName(),
				"stp_port",
				"spanning tree bridge configuration is unavailable",
				analysis.Incomplete,
				IssueMissingSTPBridgeState,
			)
		}
		return nil
	}
	stpCfg := c.loadSTPSettings(bridgeState, mac, prio, priorityPresent)
	c.loadSTPPorts(stpPorts, ports, ownership, &stpCfg)
	return &stpCfg
}

func validateSTPTimers(bridgeState *stpv1.BridgeState) string {
	var timerWhy string
	for _, timer := range []struct {
		field     string
		value     *durationpb.Duration
		low, high time.Duration
	}{
		{"bridge_hello_time", bridgeState.GetBridgeHelloTime(), time.Second, 10 * time.Second},
		{"bridge_max_age", bridgeState.GetBridgeMaxAge(), 6 * time.Second, 40 * time.Second},
		{"bridge_forward_delay", bridgeState.GetBridgeForwardDelay(), 4 * time.Second, 30 * time.Second},
	} {
		if timer.value == nil {
			continue
		}
		if err := timer.value.CheckValid(); err != nil {
			timerWhy = fmt.Sprintf("%s is not a valid protobuf duration: %v", timer.field, err)
			break
		}
		duration := timer.value.AsDuration()
		if duration < timer.low || duration > timer.high {
			timerWhy = fmt.Sprintf("%s %s is outside %s through %s", timer.field, duration, timer.low, timer.high)
			break
		}
	}
	if timerWhy == "" {
		timers := stp.Config{}
		if bridgeState.GetBridgeHelloTime() != nil {
			timers.HelloTime = bridgeState.GetBridgeHelloTime().AsDuration()
		}
		if bridgeState.GetBridgeMaxAge() != nil {
			timers.MaxAge = bridgeState.GetBridgeMaxAge().AsDuration()
		}
		if bridgeState.GetBridgeForwardDelay() != nil {
			timers.ForwardDelay = bridgeState.GetBridgeForwardDelay().AsDuration()
		}
		if err := timers.ValidateTimers(); err != nil {
			timerWhy = err.Error()
		}
	}

	return timerWhy
}

func (c *loadContext) loadSTPSettings(bridgeState *stpv1.BridgeState, mac netaddr.MAC, prio uint32, priorityPresent bool) stp.Config {
	stpCfg := stp.Config{
		Priority:        uint16(prio),
		PriorityPresent: priorityPresent,
		Address:         mac,
		Ports:           make(map[string]stp.Port),
	}
	if !priorityPresent {
		c.addDefaultAt(c.stpScope, "", "bridge_priority", strconv.FormatUint(uint64(stp.DefaultBridgePriority), 10))
	}

	if bridgeState.GetBridgeHelloTime() != nil {
		stpCfg.HelloTime = bridgeState.GetBridgeHelloTime().AsDuration()
	} else {
		c.addDefaultAt(c.stpScope, "", "bridge_hello_time", stp.DefaultHelloTime.String())
	}
	if bridgeState.GetBridgeMaxAge() != nil {
		stpCfg.MaxAge = bridgeState.GetBridgeMaxAge().AsDuration()
	} else {
		c.addDefaultAt(c.stpScope, "", "bridge_max_age", stp.DefaultMaxAge.String())
	}
	if bridgeState.GetBridgeForwardDelay() != nil {
		stpCfg.ForwardDelay = bridgeState.GetBridgeForwardDelay().AsDuration()
	} else {
		c.addDefaultAt(c.stpScope, "", "bridge_forward_delay", stp.DefaultForwardDelay.String())
	}
	if bridgeState.HasTxHoldCount() {
		if v := bridgeState.GetTxHoldCount(); v < 1 || v > 10 {
			c.addSkippedAt(c.stpScope, "", "stp_tx_hold_count", "outside 1 through 10", analysis.Unsupported, IssueInvalidTxHoldCount)
			c.addDefaultAt(c.stpScope, "", "tx_hold_count", strconv.FormatUint(uint64(stp.DefaultTxHoldCount), 10))
		} else {
			stpCfg.TxHoldCount = uint8(v)
		}
	} else {
		c.addDefaultAt(c.stpScope, "", "tx_hold_count", "6")
	}

	return stpCfg
}

func (c *loadContext) loadSTPPorts(stpPorts []*stpv1.PortState, ports port.Table, ownership loadOwnership, stpCfg *stp.Config) {
	stpPortRows, stpPortConflicts := resolveFacts(stpPorts, func(ps *stpv1.PortState) (factKey, string, bool) {
		if ps == nil {
			return factKey{}, "", false
		}
		portName := ps.GetInterfaceName()
		p, ok := ports.Port(portName)
		if !ok {
			c.addSkippedAt(c.stpPortScope(portName), portName, "stp_port", "absent from port table", analysis.Incomplete, IssueUnknownPort)
			return factKey{}, "", false
		}
		if p.LagParent != "" {
			c.addSkippedAt(c.stpPortScope(portName), portName, "stp_port", "port is a LAG member", analysis.Incomplete, IssueSkippedLagMember)
			return factKey{}, "", false
		}
		if c.isWanted(routing.LayerName) {
			_, isRoutedSubParent := ownership.routedSubParents[portName]
			if ownership.ipPresent[portName] || isRoutedSubParent {
				c.addSkippedAt(c.stpPortScope(portName), portName, "stp_port", "interface is routed", analysis.Incomplete, IssueSkippedInterfaceRouted)
				return factKey{}, "", false
			}
		}
		if ps.GetPriority() > 255 {
			c.addSkippedAt(c.stpPortScope(portName), portName, "stp_port", "priority above 255", analysis.Unsupported, IssueInvalidPortPriority)
			return factKey{}, "", false
		}
		if ps.HasAdminPathCost() && ps.GetAdminPathCost() > stp.MaxPathCost {
			c.addSkippedAt(
				c.stpPortScope(portName),
				portName,
				"stp_port",
				fmt.Sprintf("admin_path_cost exceeds maximum %d", stp.MaxPathCost),
				analysis.Unsupported,
				IssueInvalidAdminPathCost,
			)
			return factKey{}, "", false
		}
		switch ps.GetPointToPoint() {
		case stpv1.PointToPointMode_POINT_TO_POINT_MODE_UNSPECIFIED,
			stpv1.PointToPointMode_POINT_TO_POINT_MODE_AUTO,
			stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_TRUE,
			stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_FALSE:
		default:
			c.addSkippedAt(c.stpPortScope(portName), portName, "stp_port", fmt.Sprintf("point_to_point has unrecognized value %d", ps.GetPointToPoint()), analysis.Unsupported, IssueInvalidPointToPointMode)
			return factKey{}, "", false
		}
		adminPathCost := "unreported"
		if ps.HasAdminPathCost() {
			adminPathCost = strconv.FormatUint(uint64(ps.GetAdminPathCost()), 10)
		}
		priority := "unreported"
		if ps.HasPriority() {
			priority = strconv.FormatUint(uint64(ps.GetPriority()), 10)
		}
		value := fmt.Sprintf(
			"priority=%s, admin_path_cost=%s, admin_edge=%t, auto_edge=%t, point_to_point=%d",
			priority, adminPathCost, ps.GetAdminEdge(), ps.GetAutoEdge(), ps.GetPointToPoint(),
		)
		return factKey{id: portName, display: portName, scope: c.stpPortScope(portName)}, value, true
	})
	for _, conflict := range stpPortConflicts {
		c.addConflict(conflict.key.scope, "stp_port", conflict.key.display, conflict.detail(), IssueConflictSTPPort)
	}
	for _, portName := range sortedKeys(stpPortRows) {
		ps := stpPortRows[portName]
		priorityPresent := ps.HasPriority()
		if !priorityPresent {
			c.addDefaultAt(c.stpPortScope(portName), portName, "port_priority", strconv.FormatUint(uint64(stp.DefaultPortPriority), 10))
		}

		var adminPathCost uint32
		if ps.HasAdminPathCost() {
			adminPathCost = ps.GetAdminPathCost()
		} else {
			c.addDefaultAt(c.stpPortScope(portName), portName, "admin_path_cost", "0")
		}

		var p2p stp.PointToPointMode
		switch ps.GetPointToPoint() {
		case stpv1.PointToPointMode_POINT_TO_POINT_MODE_UNSPECIFIED:
			p2p = stp.PointToPointAuto
			c.addDefaultAt(c.stpPortScope(portName), portName, "point_to_point", "AUTO")
		case stpv1.PointToPointMode_POINT_TO_POINT_MODE_AUTO:
			p2p = stp.PointToPointAuto
		case stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_TRUE:
			p2p = stp.PointToPointForceTrue
		case stpv1.PointToPointMode_POINT_TO_POINT_MODE_FORCE_FALSE:
			p2p = stp.PointToPointForceFalse
		}

		stpCfg.Ports[portName] = stp.Port{
			Priority:        uint8(ps.GetPriority()),
			PriorityPresent: priorityPresent,
			PathCost:        adminPathCost,
			AdminEdge:       ps.GetAdminEdge(),
			AutoEdge:        ps.GetAutoEdge(),
			PointToPoint:    p2p,
		}
	}
}
