package syslog

import (
	"context"
	"testing"
	"time"
)

// TestReceiverShutdownClosesDoneAfterAPanic proves the receiver shutdown
// goroutine's close(r.done) still runs when the goroutine panics partway
// through cleanup. [Receiver.Close] blocks on r.done, so before the
// shutdown goroutine's completion was moved into a defer, a panic there
// left Close blocked forever with no signal beyond the logged panic — a
// hang in place of the crash spawn.Go replaces.
func TestReceiverShutdownClosesDoneAfterAPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := Listen(ctx, []ListenConfig{{Transport: UDP, Address: "127.0.0.1:0"}}, ReceiverOptions{})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	// admission.stop and admission.release both dereference a.mu; nilling
	// the field forces the shutdown goroutine's own r.stop(nil) call to
	// panic as soon as ctx cancels, before it reaches r.done's cleanup.
	r.admission = nil
	cancel()

	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("r.done never closed after a panic in the receiver shutdown goroutine")
	}

	// Close no longer has anything left to panic on (stop already ran,
	// guarded by sync.Once) and returns now that r.done is closed.
	closeDone := make(chan error, 1)
	go func() { closeDone <- r.Close() }()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after r.done closed")
	}
}
