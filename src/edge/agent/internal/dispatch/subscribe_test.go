package dispatch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	connect "connectrpc.com/connect"

	dispatchv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/dispatch"
)

// centralStream serves Subscribe: it sends what a test queued, then ends the
// stream, and counts how many times it was opened.
type centralStream struct {
	dispatchv1connect.UnimplementedDispatchServiceHandler

	mu       sync.Mutex
	opens    int
	messages []*dispatchv1.SubscribeResponse
}

func (c *centralStream) Subscribe(
	_ context.Context, _ *connect.Request[dispatchv1.SubscribeRequest],
	stream *connect.ServerStream[dispatchv1.SubscribeResponse],
) error {
	c.mu.Lock()
	c.opens++
	batch := c.messages
	c.mu.Unlock()

	for _, message := range batch {
		if err := stream.Send(message); err != nil {
			return err
		}
	}
	return nil
}

func (c *centralStream) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens
}

func servedClient(t *testing.T, handler *centralStream) dispatchv1connect.DispatchServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(dispatchv1connect.NewDispatchServiceHandler(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return dispatchv1connect.NewDispatchServiceClient(server.Client(), server.URL)
}

func dispatchTo(device string) *dispatchv1.SubscribeResponse {
	message := &dispatchv1.SubscribeResponse{}
	message.SetDeviceId(device)
	message.SetCheckpoint(&dispatchv1.CheckpointRequest{})
	return message
}

// TestOpenUsesAnEmptySubscribeRequest: the adapter opens central's stream
// with nothing but an empty request, and hands back something the loop can
// read from — proven by reading a real message off it.
func TestOpenUsesAnEmptySubscribeRequest(t *testing.T) {
	central := &centralStream{messages: []*dispatchv1.SubscribeResponse{dispatchTo("dev-1")}}
	client := servedClient(t, central)

	stream, err := dispatch.Open(client)(context.Background())
	if err != nil {
		t.Fatalf("Open(client)(ctx) = %v", err)
	}
	defer func() { _ = stream.Close() }()

	if got := central.openCount(); got != 1 {
		t.Errorf("openCount() = %d, want 1", got)
	}
	if !stream.Receive() {
		t.Fatalf("Receive() = false, want a message; Err() = %v", stream.Err())
	}
	if got := stream.Msg().GetDeviceId(); got != "dev-1" {
		t.Errorf("Msg().GetDeviceId() = %q, want %q", got, "dev-1")
	}
}

// TestLogAttrsCarriesTheDeviceIDOntoTheDroppedEvent: the dropped event's one
// caller-supplied attribute is the device id, so a drop can be traced back
// to the device whose message it was.
func TestLogAttrsCarriesTheDeviceIDOntoTheDroppedEvent(t *testing.T) {
	attrs := dispatch.LogAttrs(dispatchTo("dev-1"))
	if len(attrs) != 1 {
		t.Fatalf("LogAttrs returned %d attrs, want 1: %v", len(attrs), attrs)
	}
	if got, want := attrs[0].Key, "flowseer.device.id"; got != want {
		t.Errorf("attrs[0].Key = %q, want %q", got, want)
	}
	if got, want := attrs[0].Value.String(), "dev-1"; got != want {
		t.Errorf("attrs[0].Value = %q, want %q", got, want)
	}
}
