// Package auditapi is central's AuditService: the one writer of the device
// audit stream. An edge delivers a DeviceOperationEvent, central publishes it
// to JetStream and answers only once the stream holds it, so an edge may
// release the state a record describes only after the record is durable.
// Central reads nothing back from the stream; the record is for a later
// reader, and the fingerprint central needs comes from reports, not from here.
package auditapi

import (
	"context"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// ErrCodePublish is a failure to place a delivered event on the audit stream.
// The caller must not release the state the event describes.
var ErrCodePublish = errs.NewCode("auditapi/publish")

// Publisher places one event on the audit stream under a message id, and
// returns only once the stream has acknowledged it. The id carries the
// event's own id so the stream stores a resubmitted event once.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string) error
}

// Service implements the AuditService handler.
type Service struct {
	stream Publisher
	tenant string
}

// New constructs the audit handler over a stream publisher and the tenant
// whose audit subject the events are written to.
func New(stream Publisher, tenant string) *Service {
	return &Service{stream: stream, tenant: tenant}
}

// Deliver publishes one event and answers once the stream holds it. A publish
// failure is returned so the edge keeps the state the event describes; the
// event id is the stream's message id, so a duplicate is stored once.
func (s *Service) Deliver(ctx context.Context, req *connect.Request[eventv1.DeliverRequest]) (*connect.Response[eventv1.DeliverResponse], error) {
	event := req.Msg.GetEvent()
	deviceID := event.GetDevice().GetDevice().GetId()

	data, err := proto.Marshal(event)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodePublish).Attr("device", deviceID).Msg("marshal audit event")
	}
	subject := edgebus.AuditSubject(s.tenant, deviceID)
	if err := s.stream.Publish(ctx, subject, data, event.GetEventId()); err != nil {
		return nil, errs.From(err).Code(ErrCodePublish).Attr("device", deviceID).Attr("event", event.GetEventId()).Msg("publish audit event")
	}
	return connect.NewResponse(&eventv1.DeliverResponse{}), nil
}
