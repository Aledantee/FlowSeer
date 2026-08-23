// linktest_test.go is the opt-in real-link round-trip test. It opens an actual
// AF_PACKET socket, sends one crafted frame, and receives it back. It needs
// CAP_NET_RAW (and CAP_NET_ADMIN to create the veth pair), so it is behind the
// `linktest` build tag and the linux constraint; the default race run skips
// it.
//
//go:build linux && linktest

package link

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// TestRealLinkRoundTrip sends a frame on a real interface and expects to
// receive it back. It is skipped unless the linktest tag is set and the
// NETPEN_LINK_IFACE environment variable names an interface (typically a veth
// pair end). The test does not create the veth pair itself; the operator or CI
// sets it up.
func TestRealLinkRoundTrip(t *testing.T) {
	iface := getenv("NETPEN_LINK_IFACE")
	if iface == "" {
		t.Skip("NETPEN_LINK_IFACE not set; skipping real-link round trip")
	}

	leg, err := Open(iface)
	if err != nil {
		t.Fatalf("Open %q: %v", iface, err)
	}
	defer func() { _ = leg.Close() }()

	pkt := bytes.Repeat([]byte{0x02}, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := leg.Receive(ctx)

	if err := leg.Send(ctx, pkt); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case f := <-frames:
		if f.Err != nil {
			t.Fatalf("received error: %v", f.Err)
		}
		if !bytes.Equal(f.Data, pkt) {
			t.Errorf("received %d bytes, want %d", len(f.Data), len(pkt))
		}
	case <-ctx.Done():
		t.Fatal("did not receive frame within 5s")
	}
}
