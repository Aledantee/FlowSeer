package otlpreceiver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

type panicOnAcceptListener struct {
	net.Listener
	err error
}

func (l panicOnAcceptListener) Accept() (net.Conn, error) {
	panic(l.err)
}

func TestReceiverServePanicRecordsErrAndClosesDone(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	acceptErr := errors.New("accept panic")
	r := &Receiver{
		server: &http.Server{Handler: http.NewServeMux()},
		done:   make(chan struct{}),
	}
	r.startServing(t.Context(), panicOnAcceptListener{Listener: listener, err: acceptErr})

	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("receiver did not close done after the Accept panic")
	}
	if !errors.Is(r.err, acceptErr) {
		t.Fatalf("receiver error = %v, want %v", r.err, acceptErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := r.Close(ctx); !errors.Is(err, acceptErr) {
		t.Fatalf("Close() = %v, want %v", err, acceptErr)
	}
}
