// local_linktest_test.go is the opt-in real-interface round-trip test. It
// opens an actual AF_PACKET socket and confirms a frame sent on the named
// interface comes back. It needs CAP_NET_RAW, so it is behind the
// capturetest build tag and the linux constraint; the default race run
// skips it.
//
//go:build linux && capturetest

package rawsocket

import (
	"bytes"
	"context"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRealInterfaceRoundTrip opens the interface FLOWSEER_CAPTURE_IFACE
// names (typically a veth pair end), sends a raw frame on it with a second
// AF_PACKET socket, and expects OpenLocalInterface's Receive to deliver it.
// The test does not create the interface itself; the operator or CI does.
func TestRealInterfaceRoundTrip(t *testing.T) {
	iface := os.Getenv("FLOWSEER_CAPTURE_IFACE")
	if iface == "" {
		t.Skip("FLOWSEER_CAPTURE_IFACE not set; skipping real-interface round trip")
	}

	src, err := OpenLocalInterface(iface, true, nil)
	if err != nil {
		t.Fatalf("OpenLocalInterface(%q): %v", iface, err)
	}
	defer func() { _ = src.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frames := src.Receive(ctx)

	if err := sendRawFrame(iface, bytes.Repeat([]byte{0x02}, 64)); err != nil {
		t.Fatalf("send raw frame on %q: %v", iface, err)
	}

	for {
		select {
		case f := <-frames:
			if f.Err != nil {
				t.Fatalf("received error: %v", f.Err)
			}
			if bytes.Contains(f.Data, bytes.Repeat([]byte{0x02}, 64)) {
				return
			}
			// Some other frame arrived first (the interface is not
			// otherwise quiet); keep waiting for ours.
		case <-ctx.Done():
			t.Fatal("did not receive the sent frame within 5s")
		}
	}
}

func sendRawFrame(iface string, pkt []byte) error {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(ethPAllNetworkOrder()))
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()

	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return err
	}
	sa := &unix.SockaddrLinklayer{Protocol: ethPAllNetworkOrder(), Ifindex: ifi.Index}
	return unix.Sendto(fd, pkt, 0, sa)
}
