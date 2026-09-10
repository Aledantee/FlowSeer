package report

import (
	"context"

	connect "connectrpc.com/connect"

	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeDeliver identifies an audit record central did not take.
var ErrCodeDeliver = errs.NewCode("agent/audit-deliver")

// AuditClient is the Deliver call. Satisfied by the generated client.
type AuditClient interface {
	Deliver(context.Context, *connect.Request[eventv1.DeliverRequest]) (*connect.Response[eventv1.DeliverResponse], error)
}

// Deliverer hands the lane's audit records to central, and blocks.
//
// It is the opposite of the report queue on purpose, and the two must not be
// confused. A report tells central what happened and is retried until it
// lands; an audit record is the durable account of a device changing, and the
// lane holds its own state transition until this returns. Central answers
// only once the audit stream has acknowledged the record, so a nil error here
// means the record is held — which is what makes it safe for the lane to
// release the state the record describes.
//
// Queueing these would break that. A lane that released on an enqueue would
// be recording "this happened" against a record that might never be written,
// and the phase transition it guards would have no account of it anywhere.
// The cost is that a device operation waits on central, which is the trade
// this makes deliberately: correctness of the account over latency of the
// operation.
type Deliverer struct {
	client AuditClient
	edge   string
}

// NewDeliverer builds the deliverer for one edge.
func NewDeliverer(client AuditClient, edgeID string) *Deliverer {
	return &Deliverer{client: client, edge: edgeID}
}

// Emit delivers one record and returns once central holds it.
//
// The error is returned unchanged in meaning: the lane treats a failure as
// "the record is not held", and every caller of this in the lane keeps its
// state where it was rather than moving on. A deliverer that swallowed the
// error would be telling the lane a record was written when it was not.
func (d *Deliverer) Emit(ctx context.Context, event *eventv1.DeviceOperationEvent) error {
	if event == nil {
		// Refused rather than passed over. A nil error here is the lane's
		// signal that the record is durable, and returning one for a record
		// that does not exist would release the phase transition it was
		// meant to account for with nothing written anywhere.
		return errs.New().Code(ErrCodeDeliver).Attr("edge", d.edge).
			Msg("there is no audit record to deliver")
	}
	request := &eventv1.DeliverRequest{}
	request.SetEvent(event)

	if _, err := d.client.Deliver(ctx, connect.NewRequest(request)); err != nil {
		return errs.From(err).Code(ErrCodeDeliver).
			Attr("edge", d.edge).
			Attr("event", event.GetEventId()).
			Msg("central did not take the audit record")
	}
	return nil
}
