package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
)

// panickingDispatch stands in for the dispatch service on the one call shape
// the recovery has to cover: the long-lived server stream an edge holds open.
type panickingDispatch struct{}

func (panickingDispatch) Subscribe(
	context.Context, *connect.Request[dispatchv1.SubscribeRequest], *connect.ServerStream[dispatchv1.SubscribeResponse],
) error {
	panic("a relay pass went wrong")
}

func (panickingDispatch) Report(
	context.Context, *connect.Request[dispatchv1.ReportRequest],
) (*connect.Response[dispatchv1.ReportResponse], error) {
	return connect.NewResponse(&dispatchv1.ReportResponse{}), nil
}

// TestAPanicOnTheDispatchStreamAnswersRatherThanResetting covers the option
// the dispatch handler is mounted with. A panic on the stream an edge holds
// open costs the most of any: without recovery the transport resets, the
// telemetry interceptor records no duration, and the relay's per-device
// state is abandoned mid-pass.
func TestAPanicOnTheDispatchStreamAnswersRatherThanResetting(t *testing.T) {
	path, handler := dispatchv1connect.NewDispatchServiceHandler(panickingDispatch{}, panicRecovery())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := dispatchv1connect.NewDispatchServiceClient(server.Client(), server.URL)
	stream, err := client.Subscribe(context.Background(), connect.NewRequest(&dispatchv1.SubscribeRequest{}))
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	if stream.Receive() {
		t.Fatal("the stream delivered a message from a handler that panicked")
	}
	err = stream.Err()
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("code = %v (%v), want internal rather than a transport reset", got, err)
	}
	message := err.Error()
	if !strings.Contains(message, "handler panicked") {
		t.Errorf("error = %q, want the handler-panicked sentence", message)
	}
	if strings.Contains(message, "a relay pass went wrong") {
		t.Errorf("error = %q, want the panic value kept off the wire", message)
	}
}
