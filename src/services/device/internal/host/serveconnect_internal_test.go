package host

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TestAServeConnectPanicIsTheAttemptsError is evidence for the converted
// goroutine in serveConnect: it forces a real panic out of serve and checks
// that the Runner returns that panic as its error rather than hanging or
// letting the process die. Before the sink was wired, a panic in this
// goroutine would have taken the whole process down with it — the errCh
// send never happened, so neither select branch in serveConnect could ever
// have returned.
func TestAServeConnectPanicIsTheAttemptsError(t *testing.T) {
	server := &http.Server{}
	t.Cleanup(func() { _ = server.Close() })

	done := make(chan error, 1)
	go func() {
		done <- serveConnect(context.Background(), server, "127.0.0.1:0", func() error {
			panic("serve tls fell over")
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("serveConnect returned nil after serve panicked, want the panic reported as an error")
		}
		if errs.Attributes(err)["panic"] == nil {
			t.Errorf("serveConnect's error carries no recovered panic value: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveConnect never returned after serve panicked")
	}
}

// TestAServeConnectShutdownStillJoinsAfterAPanic covers the other branch:
// ctx ending concurrently with a panicking serve. serveConnect's shutdown
// path still receives from errCh before returning, which only terminates if
// the panic path completed that channel's one send — the same ordering rule
// the first test checks from the other branch.
func TestAServeConnectShutdownStillJoinsAfterAPanic(t *testing.T) {
	server := &http.Server{}
	t.Cleanup(func() { _ = server.Close() })

	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- serveConnect(ctx, server, "127.0.0.1:0", func() error {
			<-release
			panic("serve tls fell over after ctx ended")
		})
	}()

	cancel()
	// Give serveConnect's select time to take the ctx.Done() branch and
	// reach its blocking <-errCh before serve panics.
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serveConnect returned %v on the shutdown branch, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveConnect's shutdown branch never returned: the panic's errCh send did not arrive")
	}
}
