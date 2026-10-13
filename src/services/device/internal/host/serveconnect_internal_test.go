package host

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TestAServeConnectPanicIsTheAttemptsError forces a real panic out of serve
// and checks that the Runner returns that panic as its error rather than
// hanging. The errCh send is serveConnect's only way to learn the server
// stopped, so a panic that skipped it would leave both select branches
// waiting on a channel nothing will ever write to.
func TestAServeConnectPanicIsTheAttemptsError(t *testing.T) {
	server := &http.Server{}
	t.Cleanup(func() { _ = server.Close() })

	done := make(chan error, 1)
	go func() {
		done <- serveConnect(context.Background(), "api", server, "127.0.0.1:0", func() error {
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
		done <- serveConnect(ctx, "api", server, "127.0.0.1:0", func() error {
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

// tlsServer is a server that can ServeTLS with the certificate httptest
// generates for its own TLS servers, which stands in for the service's.
func tlsServer(t *testing.T, handler http.Handler) *http.Server {
	t.Helper()
	donor := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := donor.TLS.Certificates[0]
	donor.Close()
	return &http.Server{
		Handler:   handler,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
	}
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

// TestAListenerThatFailsEndsTheOthersAndIsTheAttemptsError pins the two halves
// of serveListeners' contract on a failure: the failing listener's error is
// what the attempt returns, and the listener that was fine is shut down rather
// than left answering behind a half-working service.
func TestAListenerThatFailsEndsTheOthersAndIsTheAttemptsError(t *testing.T) {
	broken := listenLoopback(t)
	if err := broken.Close(); err != nil {
		t.Fatalf("close the listener before serving: %v", err)
	}
	healthy := listenLoopback(t)

	listeners := []connectListener{
		{name: "api", server: tlsServer(t, http.NotFoundHandler()), listener: healthy},
		{name: "edge", server: tlsServer(t, http.NotFoundHandler()), listener: broken},
	}

	done := make(chan error, 1)
	go func() { done <- serveListeners(context.Background(), listeners) }()

	select {
	case err := <-done:
		if code, ok := errs.CodeOf(err); !ok || code != ErrCodeStart {
			t.Fatalf("serveListeners = %v, want code %v", err, ErrCodeStart)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveListeners never returned after a listener failed")
	}

	conn, err := net.DialTimeout("tcp", healthy.Addr().String(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("the healthy listener still accepts after serveListeners returned")
	}
}

// TestListenersShutDownTogetherNotOneAfterAnother holds a connection that
// outlasts the grace on both listeners and asserts the grace is spent once.
// Served or shut down one after the other, the run would take two graces.
func TestListenersShutDownTogetherNotOneAfterAnother(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the shutdown grace")
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	entered := make(chan struct{}, 2)
	blocking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}
		<-release
	})

	var listeners []connectListener
	for _, name := range []string{"api", "edge"} {
		listeners = append(listeners, connectListener{
			name: name, server: tlsServer(t, blocking), listener: listenLoopback(t),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveListeners(ctx, listeners) }()

	for _, l := range listeners {
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // a loopback listener serving a throwaway certificate
			ForceAttemptHTTP2: false,
		}}
		t.Cleanup(client.CloseIdleConnections)
		url := "https://" + l.listener.Addr().String()
		go func() {
			resp, err := client.Get(url)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
	for range listeners {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("a handler never started, so a connection is not active on every listener")
		}
	}

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveListeners = %v, want a clean shutdown", err)
		}
	case <-time.After(3 * shutdownGrace):
		t.Fatal("serveListeners never returned")
	}
	elapsed := time.Since(start)
	if elapsed < shutdownGrace-time.Second {
		t.Fatalf("returned in %s, before the %s grace: a connection was not active", elapsed, shutdownGrace)
	}
	if elapsed > shutdownGrace+shutdownGrace/2 {
		t.Fatalf("returned in %s, want about one %s grace for both listeners", elapsed, shutdownGrace)
	}
}
