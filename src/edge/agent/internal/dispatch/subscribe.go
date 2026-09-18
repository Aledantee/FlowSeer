package dispatch

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"

	dispatchv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/subscribeloop"
)

// ErrCodeSubscribe identifies a dispatch message that names no arm this
// agent understands.
var ErrCodeSubscribe = errs.NewCode("agent/subscribe")

// ErrCodeUncodedRefusal is what a dispatch is refused with when the lane
// declined it through an error carrying no code of its own. It crosses the
// wire, so central classifies on it: deliberately not one of the codes
// central names, so the refusal falls to central's default and stays
// retryable rather than disposing a mutation on a cause nobody diagnosed.
var ErrCodeUncodedRefusal = errs.NewCode("agent/uncoded-refusal")

// Open adapts client into the subscribeloop.Opener contract, opening
// central's dispatch stream with an empty SubscribeRequest. Central writes
// everything this edge is owed once the stream is up, so the request itself
// carries nothing.
func Open(client dispatchv1connect.DispatchServiceClient) subscribeloop.Opener[dispatchv1.SubscribeResponse] {
	return func(ctx context.Context) (subscribeloop.Stream[dispatchv1.SubscribeResponse], error) {
		return client.Subscribe(ctx, connect.NewRequest(&dispatchv1.SubscribeRequest{}))
	}
}

// Events names what the dispatch loop's telemetry is known by. The three
// stream events sit under flowseer.edge.dispatch; the resync failure does not,
// because what the resync re-lists is the edge's devices and an operator
// hunting a listing that stopped answering looks where the devices are. The
// two count keys are the attribute names the loop logs its own connection and
// message counts under, and they are also the names host registers the
// matching counters under, so a log line and a metric point about the same
// number are searchable as one thing.
var Events = subscribeloop.Events{
	Connected:          "flowseer.edge.dispatch.connected",
	Disconnected:       "flowseer.edge.dispatch.disconnected",
	Dropped:            "flowseer.edge.dispatch.dropped",
	ResyncFailed:       "flowseer.edge.devices.listing_failed",
	ConnectionCountKey: "flowseer.edge.dispatch.connections",
	MessageCountKey:    "flowseer.edge.dispatch.messages",
}

// LogAttrs returns the dropped-event attributes for one dispatch message:
// the device id, so a drop can be traced to the device whose message it was.
func LogAttrs(message *dispatchv1.SubscribeResponse) []slog.Attr {
	return []slog.Attr{slog.String("flowseer.device.id", message.GetDeviceId())}
}
