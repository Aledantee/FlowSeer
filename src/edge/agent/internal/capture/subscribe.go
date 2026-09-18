// Package capture provides the edge agent's capture assignment handler and
// upload streaming client.
package capture

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/subscribeloop"
)

// Open adapts client into the subscribeloop.Opener contract, opening
// central's capture assignment stream with an empty request. Central writes
// any owed start or stop assignments for this edge once the stream is up.
func Open(client capturev1connect.CaptureEdgeServiceClient) subscribeloop.Opener[captureedgev1.SubscribeCaptureAssignmentsResponse] {
	return func(ctx context.Context) (subscribeloop.Stream[captureedgev1.SubscribeCaptureAssignmentsResponse], error) {
		return client.SubscribeCaptureAssignments(ctx, connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{}))
	}
}

// Events names what the capture assignment loop's telemetry is known by.
// The stream events sit under flowseer.edge.capture. The two count keys are
// the attribute names the loop logs its connection and message counts under,
// matching the counter metrics host registers.
var Events = subscribeloop.Events{
	Connected:          "flowseer.edge.capture.connected",
	Disconnected:       "flowseer.edge.capture.disconnected",
	Dropped:            "flowseer.edge.capture.dropped",
	ConnectionCountKey: "flowseer.edge.capture.connections",
	MessageCountKey:    "flowseer.edge.capture.messages",
}

// LogAttrs returns the dropped-event attributes for one capture assignment
// message: the capture session ID, so an unhandled assignment is attributed
// to the session it named. No packet payload or interface information is
// included.
func LogAttrs(message *captureedgev1.SubscribeCaptureAssignmentsResponse) []slog.Attr {
	if message == nil {
		return nil
	}
	var sessionID string
	switch {
	case message.GetStart() != nil:
		sessionID = message.GetStart().GetRef().GetCaptureSession().GetId()
	case message.GetStop() != nil:
		sessionID = message.GetStop().GetCaptureSession().GetId()
	}
	if sessionID != "" {
		return []slog.Attr{slog.String("flowseer.capture.session.id", sessionID)}
	}
	return nil
}
