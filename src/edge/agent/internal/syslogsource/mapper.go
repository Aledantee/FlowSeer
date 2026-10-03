package syslogsource

import (
	"strconv"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	netlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

const (
	maxHostnameLen = 255
	maxAppNameLen  = 48
	maxProcIDLen   = 128
	maxMsgIDLen    = 32
	maxMessageLen  = 65527
	maxSDIDLen     = 32
	maxParamName   = 32
	maxParamValue  = 1024
)

// MapRecord translates a parsed syslog record and its resolved device entry
// into a SyslogRecord and Provenance, determining whether a parse failure occurred.
func MapRecord(
	record syslog.Record,
	deviceEntry lanehost.DeviceEntry,
	edgeRef *edgev1.EdgeGlobalRef,
) (*eventlogv1.SyslogRecord, *inventoryv1.Provenance, bool) {
	isParseFailure := record.Status != syslog.Complete

	receivedAt := timestamppb.New(record.Observation.ReceivedAt)

	// Build SyslogRecord builder
	recordBuilder := eventlogv1.SyslogRecord_builder{
		Device:     deviceEntry.Device,
		ReceivedAt: receivedAt,
	}

	if record.DeviceTime.Instant != nil {
		recordBuilder.SentAt = timestamppb.New(*record.DeviceTime.Instant)
	}

	// Priority -> Severity and Facility
	if record.Priority.Presence == syslog.Present {
		pri, err := strconv.Atoi(record.Priority.Value)
		if err == nil && pri >= 0 && pri <= 191 {
			recordBuilder.Severity = netlogv1.SyslogSeverity(pri % 8).Enum()
			recordBuilder.Facility = netlogv1.SyslogFacility(pri / 8).Enum()
		} else {
			isParseFailure = true
		}
	}

	// Hostname
	if record.Hostname.Presence == syslog.Present {
		val := record.Hostname.Value
		if len(val) >= 1 && len(val) <= maxHostnameLen {
			recordBuilder.Hostname = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	// Application (app_name)
	if record.Application.Presence == syslog.Present {
		val := record.Application.Value
		if len(val) >= 1 && len(val) <= maxAppNameLen {
			recordBuilder.AppName = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	// ProcessID (proc_id)
	if record.ProcessID.Presence == syslog.Present {
		val := record.ProcessID.Value
		if len(val) >= 1 && len(val) <= maxProcIDLen {
			recordBuilder.ProcId = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	// MessageID (msg_id)
	if record.MessageID.Presence == syslog.Present {
		val := record.MessageID.Value
		if len(val) >= 1 && len(val) <= maxMsgIDLen {
			recordBuilder.MsgId = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	// Structured data
	if len(record.StructuredData) > 0 {
		seenIDs := make(map[string]bool, len(record.StructuredData))
		var sdElements []*eventlogv1.SyslogStructuredDataElement
		for _, el := range record.StructuredData {
			if len(el.ID) < 1 || len(el.ID) > maxSDIDLen || seenIDs[el.ID] {
				isParseFailure = true
				continue
			}
			seenIDs[el.ID] = true
			var params []*eventlogv1.SyslogStructuredDataParam
			paramsValid := true
			for _, p := range el.Parameters {
				if len(p.Name) < 1 || len(p.Name) > maxParamName || len(p.Value) > maxParamValue {
					isParseFailure = true
					paramsValid = false
					break
				}
				params = append(params, eventlogv1.SyslogStructuredDataParam_builder{
					Name:  proto.String(p.Name),
					Value: proto.String(string(p.Value)),
				}.Build())
			}
			if paramsValid {
				sdElements = append(sdElements, eventlogv1.SyslogStructuredDataElement_builder{
					Id:     proto.String(el.ID),
					Params: params,
				}.Build())
			}
		}
		if len(sdElements) > 0 {
			recordBuilder.StructuredData = sdElements
		}
	}

	// Message content and truncation
	if len(record.Content) > maxMessageLen {
		recordBuilder.Message = record.Content[:maxMessageLen]
		recordBuilder.MessageTruncated = proto.Bool(true)
		isParseFailure = true
	} else if len(record.Content) > 0 {
		recordBuilder.Message = record.Content
		recordBuilder.MessageTruncated = proto.Bool(false)
	}

	// Source address from peer
	peerAddr := record.Observation.Peer.Addr()
	if peerAddr.IsValid() {
		if peerAddr.Is4() {
			recordBuilder.SourceAddress = addrv1.IpAddress_builder{
				V4: addrv1.Ipv4Address_builder{Octets: peerAddr.AsSlice()}.Build(),
			}.Build()
		} else if peerAddr.Is6() {
			recordBuilder.SourceAddress = addrv1.IpAddress_builder{
				V6: addrv1.Ipv6Address_builder{
					Octets: peerAddr.AsSlice(),
				}.Build(),
			}.Build()
		}
	}

	syslogRecord := recordBuilder.Build()

	// Provenance
	provBuilder := inventoryv1.Provenance_builder{
		Binding:    deviceEntry.Binding,
		ObservedAt: receivedAt,
		Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
	}
	if edgeRef != nil {
		provBuilder.Edge = edgeRef
	}

	return syslogRecord, provBuilder.Build(), isParseFailure
}
