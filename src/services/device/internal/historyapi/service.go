// Package historyapi serves the operator's history reads: it turns a
// ListSyslogRecords request into a [historyquery] read for the tenants the
// authorization interceptor admitted, and the stored rows back into records.
package historyapi

import (
	"context"
	"net/netip"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	historyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/history/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/history/v1/historyv1connect"
	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	logv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/historyquery"
)

// Reader is the read the service runs. [historyquery.Reader] is the
// implementation.
type Reader interface {
	ListSyslog(ctx context.Context, q historyquery.Query) (historyquery.Result, error)
}

// Service serves [historyv1connect.HistoryServiceHandler]. It is safe for
// concurrent use when its Reader is.
type Service struct {
	reader Reader
}

var _ historyv1connect.HistoryServiceHandler = (*Service)(nil)

// New returns a Service over reader.
func New(reader Reader) *Service { return &Service{reader: reader} }

// ListSyslogRecords returns one page of the scope's syslog records for the
// tenants the interceptor admitted for this call. The handler takes no tenant
// from the request.
func (s *Service) ListSyslogRecords(
	ctx context.Context,
	req *connect.Request[historyv1.ListSyslogRecordsRequest],
) (*connect.Response[historyv1.ListSyslogRecordsResponse], error) {
	query, err := queryOf(req.Msg)
	if err != nil {
		return nil, connectErr(err)
	}
	result, err := s.reader.ListSyslog(ctx, query)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, connectErr(err)
	}

	entries := make([]*historyv1.SyslogHistoryEntry, 0, len(result.Entries))
	for _, e := range result.Entries {
		entries = append(entries, entryOf(e))
	}
	response := historyv1.ListSyslogRecordsResponse_builder{Entries: entries}.Build()
	if result.NextToken != "" {
		response.SetNextPageToken(result.NextToken)
	}
	return connect.NewResponse(response), nil
}

func queryOf(req *historyv1.ListSyslogRecordsRequest) (historyquery.Query, error) {
	q := historyquery.Query{
		From:      req.GetFrom().AsTime(),
		To:        req.GetTo().AsTime(),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	}
	switch req.WhichScope() {
	case historyv1.ListSyslogRecordsRequest_Device_case:
		id, err := uuid.Parse(req.GetDevice().GetDevice().GetId())
		if err != nil {
			return q, errs.From(err).Code(ErrCodeBadRequest).Msg("device id is not a UUID")
		}
		q.Scope = historyquery.DeviceScope(id)
	case historyv1.ListSyslogRecordsRequest_Location_case:
		id, err := uuid.Parse(req.GetLocation().GetLocation().GetId())
		if err != nil {
			return q, errs.From(err).Code(ErrCodeBadRequest).Msg("location id is not a UUID")
		}
		q.Scope = historyquery.LocationScope(id)
	default:
		return q, errs.New().Code(ErrCodeBadRequest).Msg("request names no scope")
	}
	if req.HasMinSeverity() {
		q.MinSeverity = new(uint8(req.GetMinSeverity()))
	}
	return q, nil
}

// entryOf rebuilds the record the sink stored. A header field the store holds
// as an empty string was absent, a bit of present says whether the message
// carried a priority, a sent time, or a source address, and the structured data
// elements are regrouped from their parallel arrays.
func entryOf(e historyquery.Entry) *historyv1.SyslogHistoryEntry {
	record := eventlogv1.SyslogRecord_builder{
		Device: inventoryv1.DeviceGlobalRef_builder{
			Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(e.DeviceID.String())}.Build(),
		}.Build(),
		ReceivedAt:     timestamppb.New(e.ReceivedAt),
		Message:        []byte(e.Message),
		StructuredData: structuredData(e),
	}
	if e.Present&historyquery.PresentSeverity != 0 {
		record.Severity = logv1.SyslogSeverity(e.Severity).Enum()
	}
	if e.Present&historyquery.PresentFacility != 0 {
		record.Facility = logv1.SyslogFacility(e.Facility).Enum()
	}
	if e.Present&historyquery.PresentSentAt != 0 {
		record.SentAt = timestamppb.New(e.SentAt)
	}
	if e.Present&historyquery.PresentSourceIP != 0 {
		record.SourceAddress = addressOf(e.SourceIP)
	}
	record.Hostname = optional(e.Hostname)
	record.AppName = optional(e.AppName)
	record.ProcId = optional(e.ProcID)
	record.MsgId = optional(e.MsgID)
	if e.MessageTruncated {
		record.MessageTruncated = proto.Bool(true)
	}

	return historyv1.SyslogHistoryEntry_builder{
		RecordId: proto.String(e.RecordID.String()),
		Tenant:   identityv1.TenantLocalRef_builder{Id: proto.String(e.TenantID)}.Build(),
		Record:   record.Build(),
	}.Build()
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func structuredData(e historyquery.Entry) []*eventlogv1.SyslogStructuredDataElement {
	var elements []*eventlogv1.SyslogStructuredDataElement
	for _, id := range e.SDIDs {
		var params []*eventlogv1.SyslogStructuredDataParam
		for i, owner := range e.SDParamSDID {
			if owner == id {
				params = append(params, eventlogv1.SyslogStructuredDataParam_builder{
					Name:  proto.String(e.SDParamName[i]),
					Value: proto.String(e.SDParamValue[i]),
				}.Build())
			}
		}
		elements = append(elements, eventlogv1.SyslogStructuredDataElement_builder{Id: proto.String(id), Params: params}.Build())
	}
	return elements
}

// addressOf is the inverse of the sink's mapping: an IPv4-mapped IPv6 address is
// an IPv4 address.
func addressOf(octets [16]byte) *addrv1.IpAddress {
	addr := netip.AddrFrom16(octets)
	if addr.Is4In6() {
		v4 := addr.Unmap().As4()
		return addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: v4[:]}.Build()}.Build()
	}
	return addrv1.IpAddress_builder{V6: addrv1.Ipv6Address_builder{Octets: octets[:]}.Build()}.Build()
}
