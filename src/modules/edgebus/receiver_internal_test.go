package edgebus

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// panicOnAcceptListener panics from its first Accept call, simulating a
// failure inside http.Server.Serve's own accept loop — the frame the
// converted goroutine actually runs on, and one Serve itself does not
// recover from (a handler panic is recovered per-connection by net/http on
// a goroutine of its own; an Accept panic is not). It wraps a real listener
// rather than a nil one, so Serve's own deferred Close on the way out of
// the panic's unwind has something real to close instead of nil-dereferencing
// and masking the panic this test means to cause.
type panicOnAcceptListener struct {
	net.Listener
}

func (panicOnAcceptListener) Accept() (net.Conn, error) {
	panic("simulated accept panic")
}

// TestReceiverServePanicRecordsErrAndClosesDone forces a real panic on the
// goroutine startServing starts (the listener's Accept, not a manufactured
// error), then reads r.err only after <-r.done — the ordering the production
// code's own comment says is what makes the unsynchronized field safe. Close
// must report the panic rather than leave the caller believing the receiver
// stopped cleanly.
func TestReceiverServePanicRecordsErrAndClosesDone(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	r := &Receiver{
		server: &http.Server{Handler: http.NewServeMux()},
		done:   make(chan struct{}),
	}
	r.startServing(context.Background(), panicOnAcceptListener{Listener: listener})

	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for r.done to close after the Accept panic")
	}

	if r.err == nil {
		t.Fatal("r.err is nil after the accept-loop goroutine panicked; the panic was recorded as a clean stop")
	}

	closeErr := r.Close(context.Background())
	if closeErr == nil {
		t.Fatal("Close() returned nil after the accept-loop goroutine panicked")
	}
	if errors.Is(closeErr, http.ErrServerClosed) {
		t.Fatalf("Close() reported the ordinary shutdown error, not the recovered panic: %v", closeErr)
	}
}
