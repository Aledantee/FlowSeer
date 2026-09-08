package dispatch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1/devicev1connect"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/dispatch"
)

// centralStream serves Subscribe: it sends what a test queued, then ends the
// stream or fails, and counts how many times it was opened.
type centralStream struct {
	devicev1connect.UnimplementedDispatchServiceHandler

	mu       sync.Mutex
	opens    int
	messages [][]*integrationv1.SubscribeResponse
	failOpen bool
}

func (c *centralStream) Subscribe(
	_ context.Context, _ *connect.Request[integrationv1.SubscribeRequest],
	stream *connect.ServerStream[integrationv1.SubscribeResponse],
) error {
	c.mu.Lock()
	c.opens++
	open := c.opens
	failOpen := c.failOpen
	var batch []*integrationv1.SubscribeResponse
	if open-1 < len(c.messages) {
		batch = c.messages[open-1]
	}
	c.mu.Unlock()

	if failOpen {
		return connect.NewError(connect.CodeUnavailable, errors.New("central is down"))
	}
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

type handlerFake struct {
	mu       sync.Mutex
	seen     []string
	err      error
	released chan struct{}
}

func (h *handlerFake) Handle(_ context.Context, message *integrationv1.SubscribeResponse) error {
	h.mu.Lock()
	h.seen = append(h.seen, message.GetDeviceId())
	count := len(h.seen)
	h.mu.Unlock()
	if h.released != nil && count == 1 {
		close(h.released)
	}
	return h.err
}

func (h *handlerFake) devices() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

func servedClient(t *testing.T, handler *centralStream) devicev1connect.DispatchServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(devicev1connect.NewDispatchServiceHandler(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return devicev1connect.NewDispatchServiceClient(server.Client(), server.URL)
}

func dispatchTo(device string) *integrationv1.SubscribeResponse {
	message := &integrationv1.SubscribeResponse{}
	message.SetDeviceId(device)
	message.SetCheckpoint(&integrationv1.CheckpointRequest{})
	return message
}

// runFor drives the loop until it has made n attempts, then stops it.
func runFor(t *testing.T, cfg dispatch.Config, contact *dispatch.Contact, attempts int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var waited atomic.Int64
	cfg.Wait = func(ctx context.Context, _ time.Duration) bool {
		if waited.Add(1) >= int64(attempts) {
			return false
		}
		return ctx.Err() == nil
	}
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(ctx, cfg, contact) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the dispatch loop never stopped")
	}
}

// TestAClientThatNeverConnectsIsVisibleAsANumber is the failure a
// reconnecting loop hides. A client dead since its first attempt looks
// exactly like one with nothing to do: no messages, no errors reaching
// anyone, a quiet fleet. Connections is what tells them apart, and asserting
// it is zero here is the same assertion that is non-zero in every other test
// in this file.
func TestAClientThatNeverConnectsIsVisibleAsANumber(t *testing.T) {
	central := &centralStream{failOpen: true}
	contact := &dispatch.Contact{}
	handler := &handlerFake{}

	runFor(t, dispatch.Config{
		Client: servedClient(t, central), Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 3)

	if got := contact.Connections(); got != 0 {
		t.Errorf("Connections() = %d, want 0: no stream was ever served", got)
	}
	if got := contact.Failures(); got == 0 {
		t.Error("Failures() = 0: a loop that never connected must not look idle")
	}
}

// TestTheStreamIsReopenedAfterItEnds covers the ordinary case: central closes
// the stream and the edge comes back. Counting opens on the server proves the
// reconnect happened rather than that the loop merely survived.
func TestTheStreamIsReopenedAfterItEnds(t *testing.T) {
	central := &centralStream{messages: [][]*integrationv1.SubscribeResponse{
		{dispatchTo("dev-1")},
		{dispatchTo("dev-2")},
	}}
	contact := &dispatch.Contact{}
	handler := &handlerFake{}

	runFor(t, dispatch.Config{
		Client: servedClient(t, central), Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 2)

	if central.openCount() < 2 {
		t.Errorf("the stream was opened %d times, want at least 2", central.openCount())
	}
	if got := contact.Connections(); got < 2 {
		t.Errorf("Connections() = %d, want at least 2", got)
	}
	if devices := handler.devices(); len(devices) < 2 || devices[0] != "dev-1" || devices[1] != "dev-2" {
		t.Errorf("handled %v, want dev-1 then dev-2 across the reconnect", devices)
	}
}

// TestAHandlerErrorDoesNotDropTheStream: one device's message failing must
// not cost every other device on the same stream. Central re-sends what it is
// owed, and the handler has already answered a message it cannot apply with a
// refusal.
func TestAHandlerErrorDoesNotDropTheStream(t *testing.T) {
	central := &centralStream{messages: [][]*integrationv1.SubscribeResponse{
		{dispatchTo("dev-1"), dispatchTo("dev-2"), dispatchTo("dev-3")},
	}}
	contact := &dispatch.Contact{}
	handler := &handlerFake{err: errors.New("cannot apply")}

	runFor(t, dispatch.Config{
		Client: servedClient(t, central), Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 1)

	if got := len(handler.devices()); got != 3 {
		t.Errorf("handled %d messages, want all 3: one failure must not end the stream", got)
	}
}

// TestACentralThatServesAndClosesLooksHealthyExceptForMessages is the case
// Contact's doc had no story for. A central accepting every stream and
// closing it immediately leaves Connections climbing and Failures at zero,
// which reads as a working edge — and the backoff is meanwhile doubling to
// its ceiling, because it resets on a delivered message and none arrive.
//
// The test exists to keep the doc honest rather than to change behaviour:
// Messages is the number that separates this from a healthy loop, and it must
// stay at zero here or the doc's advice is wrong.
func TestACentralThatServesAndClosesLooksHealthyExceptForMessages(t *testing.T) {
	central := &centralStream{}
	contact := &dispatch.Contact{}
	handler := &handlerFake{}

	runFor(t, dispatch.Config{
		Client: servedClient(t, central), Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 3)

	if got := contact.Connections(); got < 3 {
		t.Errorf("Connections() = %d, want at least 3: central served every stream", got)
	}
	if got := contact.Failures(); got != 0 {
		t.Errorf("Failures() = %d, want 0: nothing failed", got)
	}
	if got := contact.Messages(); got != 0 {
		t.Errorf("Messages() = %d, want 0: this is the number that shows nothing is arriving", got)
	}
}
