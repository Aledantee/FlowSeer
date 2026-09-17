package dispatchapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	dispatchv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
)

// TestSubscribeStreamsOnOpenAndWakesOnChange runs the relay behind the real
// Connect handler and a client stream: the mutation owed at open arrives over
// the wire, and a read opened afterwards arrives within a backoff step because
// the record change wakes the loop. This exercises the running relay, not the
// pass in isolation.
func TestSubscribeStreamsOnOpenAndWakesOnChange(t *testing.T) {
	j, kv := newJournalKV(t)
	// Resend far longer than the test's patience, so the read that arrives
	// after the record change can only have come from the watch waking the
	// loop, not from the fallback ticker. With Watch nil this test would hang.
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
		Resend:   10 * time.Minute,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	path, handler := dispatchv1connect.NewDispatchServiceHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := dispatchv1connect.NewDispatchServiceClient(srv.Client(), srv.URL)

	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000c01"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}

	stream, err := client.Subscribe(ctx, connect.NewRequest(&dispatchv1.SubscribeRequest{}))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Receive() {
		t.Fatalf("stream closed before the first dispatch: %v", stream.Err())
	}
	if stream.Msg().GetExecute().GetMutation() == nil {
		t.Fatalf("first dispatch is not the owed mutation: %+v", stream.Msg())
	}

	// A read opened now is a record change; the watch must wake the loop so its
	// dispatch arrives without waiting for anything external.
	if _, err := j.OpenRead(ctx, deviceID, deviceRef(deviceID), "ethernet 1/1/1", typedRead(), "0192e6a0-0000-7000-8000-000000000f05", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("open read: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the read dispatch never arrived after the record change")
		}
		if !stream.Receive() {
			t.Fatalf("stream closed while waiting for the read: %v", stream.Err())
		}
		if stream.Msg().GetExecute().GetRead() != nil {
			return // the change woke the loop and the read reached the wire
		}
	}
}
