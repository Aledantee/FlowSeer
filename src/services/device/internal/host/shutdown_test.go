package host_test

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// TestShutdownCutsAConnectionThatOutlastsTheGrace is finding 2's test.
//
// http.Server.Shutdown never cuts an active connection. It closes the
// listeners, closes idle connections, and returns its context's error once
// the grace passes — leaving in-flight handlers running. Discarding that
// error returned from Run with those goroutines still alive: an edge's
// dispatch stream handler looping on its resend ticker against a bus its
// own module had already closed. In production the process exits and hides
// it; in the in-process host the end-to-end test needs, it outlives the
// test that created it.
//
// The connection here is genuinely active for the whole grace rather than
// idle, which is the distinction the fix turns on: a request whose declared
// body never arrives leaves the server blocked reading it. An idle
// connection would be closed by Shutdown alone and would prove nothing.
//
// The observable is the connection ending — a read returning — after Run
// has returned. Asserting that Run returns proves nothing: it returns
// either way, which is the defect.
func TestShutdownCutsAConnectionThatOutlastsTheGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a whole service and waits out the shutdown grace")
	}
	base, stop, waitStopped := runningServiceWithControl(t)
	address := strings.TrimPrefix(base, "https://")

	// Wait for the API to be serving before holding a connection on it.
	waitForAPI(t, base)

	// http/1.1 explicitly: the server offers h2 over TLS, and a raw
	// HTTP/1.1 request written onto an h2 connection is discarded rather
	// than served, which leaves nothing active to hold open.
	conn, err := tls.Dial("tcp", address, &tls.Config{ //nolint:gosec // a loopback service that generated its own certificate this second
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	if err != nil {
		t.Fatalf("dial the api: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// A body that is declared and never finishes arriving. The handler
	// blocks reading it, so this connection is active — not idle — for as
	// long as the test leaves it that way.
	request := "POST /flowseer.api.device.v1.DeviceService/GetDeviceAccessStatus HTTP/1.1\r\n" +
		"Host: " + address + "\r\n" +
		"Content-Type: application/proto\r\n" +
		"Content-Length: 1048576\r\n" +
		"\r\n" +
		"\x00\x00\x00"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("write the request head: %v", err)
	}
	// Give the server time to read the head and enter the handler, which
	// then blocks on the body that never finishes. Without this the
	// connection is still idle when the shutdown starts and the grace
	// assertion below catches it.
	time.Sleep(500 * time.Millisecond)

	stoppedAt := time.Now()
	stop()
	if err := waitStopped(); err != nil {
		t.Fatalf("the service stopped with %v, want a clean shutdown", err)
	}

	// The grace must actually have been spent: if Run came back before it,
	// this connection was not active and the rest of the test proves
	// nothing about cutting one. That was true of the first version of
	// this test, which wrote HTTP/1.1 onto an h2 connection and passed
	// with the fix removed.
	if elapsed := time.Since(stoppedAt); elapsed < 4*time.Second {
		t.Fatalf("the service stopped in %s, before the %s grace: the connection was not active", elapsed, shutdownGraceForTest)
	}

	// Run has returned. The connection it was serving must be gone with it.
	// Generous, because the point is "eventually, without the process
	// exiting", not a latency bound: without the fix this read blocks until
	// the deadline below and the connection is still live behind it.
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("the connection is still serving after Run returned")
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("the connection outlived Run: Shutdown left an active handler running and nothing closed it")
	}
	if !errors.Is(err, io.EOF) && !isConnectionEnded(err) {
		t.Fatalf("read after shutdown returned %v, want the connection closed", err)
	}
}

func isConnectionEnded(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "reset by peer") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "use of closed") ||
		strings.Contains(text, "connection reset")
}

// waitForAPI blocks until the service's listener accepts a TLS connection.
func waitForAPI(t *testing.T, base string) {
	t.Helper()
	address := strings.TrimPrefix(base, "https://")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := tls.Dial("tcp", address, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // see above
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the api at %s never came up", base)
}

// shutdownGraceForTest mirrors the host's own grace. It is duplicated
// rather than exported: the test asserts the grace was spent, and a
// constant it could read from the code under test would move with it.
const shutdownGraceForTest = 5 * time.Second
