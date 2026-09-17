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

	auditv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// Error codes the audit handler returns.
var (
	// ErrCodePublish is a failure to place a delivered event on the audit
	// stream. The caller must not release the state the event describes.
	ErrCodePublish = errs.NewCode("auditapi/publish")
	// ErrCodeEdge is a delivery whose caller could not be identified as an
	// edge.
	ErrCodeEdge = errs.NewCode("auditapi/edge")
	// ErrCodeResolve is a failure to resolve the edge-device binding.
	ErrCodeResolve = errs.NewCode("auditapi/resolve")
	// ErrCodeForbidden is a delivery about a device the calling edge does not
	// host.
	ErrCodeForbidden = errs.NewCode("auditapi/forbidden")
)

// Publisher places one event on the audit stream under a message id, and
// returns only once the stream has acknowledged it. The id carries the
// event's own id so the stream stores a resubmitted event once.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string) error
}

// EdgeBinding identifies the calling edge and says whether it hosts a device.
// It binds an audit delivery to the edge the assertion names, so an edge
// cannot write a record for another edge's device onto the stream central is
// meant to own exclusively. The registry service implements it.
type EdgeBinding interface {
	EdgeID(ctx context.Context) (string, error)
	Hosts(ctx context.Context, edgeID, deviceID string) (bool, error)
}

// Service implements the AuditService handler.
type Service struct {
	stream  Publisher
	binding EdgeBinding
	tenant  string
}

// New constructs the audit handler over a stream publisher, the edge binding
// that authorizes each delivery, and the tenant whose audit subject the events
// are written to.
func New(stream Publisher, binding EdgeBinding, tenant string) *Service {
	return &Service{stream: stream, binding: binding, tenant: tenant}
}

// Deliver publishes one event and answers once the stream holds it, after
// confirming the calling edge hosts the event's device. A publish failure is
// returned so the edge keeps the state the event describes; the event id is
// the stream's message id, so a duplicate is stored once.
func (s *Service) Deliver(ctx context.Context, req *connect.Request[auditv1.DeliverRequest]) (*connect.Response[auditv1.DeliverResponse], error) {
	event := req.Msg.GetEvent()
	deviceID := event.GetDevice().GetDevice().GetId()

	edgeID, err := s.binding.EdgeID(ctx)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeEdge).Msg("identify delivering edge"))
	}
	hosts, err := s.binding.Hosts(ctx, edgeID, deviceID)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeResolve).Attr("edge", edgeID).Attr("device", deviceID).
			Msg("resolve edge-device binding"))
	}
	if !hosts {
		return nil, connectErr(errs.New().Code(ErrCodeForbidden).Attr("edge", edgeID).Attr("device", deviceID).
			Msg("edge does not host this device"))
	}

	data, err := proto.Marshal(event)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodePublish).Attr("device", deviceID).Msg("marshal audit event"))
	}
	subject := edgebus.AuditSubject(s.tenant, deviceID)
	if err := s.stream.Publish(ctx, subject, data, event.GetEventId()); err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodePublish).Attr("device", deviceID).
			Attr("event", event.GetEventId()).Msg("publish audit event"))
	}
	return connect.NewResponse(&auditv1.DeliverResponse{}), nil
}
