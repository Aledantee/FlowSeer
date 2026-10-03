package syslogsource

import (
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
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

	recordBuilder := eventlogv1.SyslogRecord_builder{
		Device:     deviceEntry.Device,
		ReceivedAt: receivedAt,
	}

	if record.DeviceTime.Instant != nil {
		ts := timestamppb.New(*record.DeviceTime.Instant)
		if ts.CheckValid() == nil {
			recordBuilder.SentAt = ts
		} else {
			isParseFailure = true
		}
	}

	if record.Priority.Presence == syslog.Present {
		pri, err := strconv.Atoi(record.Priority.Value)
		if err == nil && pri >= 0 && pri <= 191 {
			recordBuilder.Severity = netlogv1.SyslogSeverity(pri % 8).Enum()
			recordBuilder.Facility = netlogv1.SyslogFacility(pri / 8).Enum()
		} else {
			isParseFailure = true
		}
	}

	if record.Hostname.Presence == syslog.Present {
		val := record.Hostname.Value
		if validText(val, maxHostnameLen) {
			recordBuilder.Hostname = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	if record.Application.Presence == syslog.Present {
		val := record.Application.Value
		if validText(val, maxAppNameLen) {
			recordBuilder.AppName = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	if record.ProcessID.Presence == syslog.Present {
		val := record.ProcessID.Value
		if validText(val, maxProcIDLen) {
			recordBuilder.ProcId = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	if record.MessageID.Presence == syslog.Present {
		val := record.MessageID.Value
		if validText(val, maxMsgIDLen) {
			recordBuilder.MsgId = proto.String(val)
		} else {
			isParseFailure = true
		}
	}

	if len(record.StructuredData) > 0 {
		seenIDs := make(map[string]bool, len(record.StructuredData))
		var sdElements []*eventlogv1.SyslogStructuredDataElement
		for _, el := range record.StructuredData {
			if !validText(el.ID, maxSDIDLen) || seenIDs[el.ID] {
				isParseFailure = true
				continue
			}
			seenIDs[el.ID] = true
			var params []*eventlogv1.SyslogStructuredDataParam
			paramsValid := true
			for _, p := range el.Parameters {
				if !validText(p.Name, maxParamName) || !utf8.Valid(p.Value) || utf8.RuneCount(p.Value) > maxParamValue {
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

	if len(record.Content) > maxMessageLen {
		recordBuilder.Message = record.Content[:maxMessageLen]
		recordBuilder.MessageTruncated = proto.Bool(true)
		isParseFailure = true
	} else if len(record.Content) > 0 {
		recordBuilder.Message = record.Content
		recordBuilder.MessageTruncated = proto.Bool(false)
	}

	peerAddr := record.Observation.Peer.Addr().Unmap()
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

	provBuilder := inventoryv1.Provenance_builder{
		Binding:    deviceEntry.Binding,
		Edge:       edgeRef,
		ObservedAt: receivedAt,
		Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
	}

	return syslogRecord, provBuilder.Build(), isParseFailure
}

// BuildEnvelope packages a syslog record, provenance, and optional raw evidence
// into an IngestRecord.
func BuildEnvelope(
	recordID string,
	prov *inventoryv1.Provenance,
	syslogRec *eventlogv1.SyslogRecord,
	rawData []byte,
	keepRaw bool,
	suppressed uint64,
) *ingestv1.IngestRecord {
	envBuilder := ingestv1.IngestRecord_builder{
		RecordId:   proto.String(recordID),
		Provenance: prov,
		Syslog:     syslogRec,
	}
	if keepRaw {
		envBuilder.Raw = ingestv1.RawEvidence_builder{
			Data:                rawData,
			Reason:              ingestv1.RawReason_RAW_REASON_PARSE_FAILURE.Enum(),
			SuppressedSinceLast: proto.Uint64(suppressed),
		}.Build()
	}
	return envBuilder.Build()
}

// validText reports whether s fits a string field bounded to 1..limit
// characters. The schema counts characters, not bytes, so a multi-byte value
// within the bound is kept. A string field holds UTF-8 only, and the parser
// keeps header and parameter bytes as the device sent them.
func validText(s string, limit int) bool {
	if !utf8.ValidString(s) {
		return false
	}
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= limit
}
