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

// LogAttrs returns the dropped-event attributes for one dispatch message:
// the device id, so a drop can be traced to the device whose message it was.
func LogAttrs(message *dispatchv1.SubscribeResponse) []slog.Attr {
	return []slog.Attr{slog.String("flowseer.device.id", message.GetDeviceId())}
}
