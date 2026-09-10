//go:build linux

package rawsocket

import (
	"context"
	"encoding/hex"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/mirror"
)

// vxlanPayload is a VXLAN header (RFC 7348, I flag set, VNI 0x01ccdd)
// carrying a short inner frame, the same fixture shape
// src/modules/capture/mirror's own decode_test.go uses.
func vxlanPayload(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString("0800000001ccdd00001122334455aabbccddeeff08004954")
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return b
}

// udpLocalPort reads back the port the kernel assigned when openMirrorUDP
// was called with port 0.
func udpLocalPort(t *testing.T, fd int) int {
	t.Helper()
	sa, err := unix.Getsockname(fd)
	if err != nil {
		t.Fatalf("Getsockname: %v", err)
	}
	sa6, ok := sa.(*unix.SockaddrInet6)
	if !ok {
		t.Fatalf("Getsockname returned %T, want *unix.SockaddrInet6", sa)
	}
	return sa6.Port
}

// TestOpenMirrorUDP_RealPktinfo binds a real, unprivileged loopback UDP
// socket through openMirrorUDP, sends a VXLAN datagram to it over IPv6
// loopback with the standard library's own UDP client, and confirms
// IPV6_RECVPKTINFO lets pktinfoDestination recover ::1 as the destination —
// proving the real kernel ancillary-data path, not a hand-constructed one.
// It does not cover a v4-mapped sender arriving on this dual-stack socket,
// an interaction this environment cannot verify without a Linux host to run
// it on.
func TestOpenMirrorUDP_RealPktinfo(t *testing.T) {
	sock, err := openMirrorUDP(0, "") // port 0: kernel assigns an ephemeral port
	if err != nil {
		t.Fatalf("openMirrorUDP: %v", err)
	}
	defer func() { _ = sock.close() }()

	fd, ok := sock.(*fdMirrorSocket)
	if !ok {
		t.Fatalf("openMirrorUDP returned %T, want *fdMirrorSocket", sock)
	}
	port := udpLocalPort(t, fd.fd)

	conn, err := net.DialUDP("udp6", nil, &net.UDPAddr{IP: net.ParseIP("::1"), Port: port})
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write(vxlanPayload(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	candidates := []capturev1.MirrorEncapsulation{capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN}
	decodeUDP := func(payload []byte, src, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
		return mirror.DecodeUDP(payload, src, dst, candidates)
	}

	frames := make(chan Frame, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	src := &linuxMirrorSource{done: make(chan struct{})}
	go src.runMirrorLoop(ctx, sock, decodeUDP, frames)

	select {
	case f := <-frames:
		if f.Err != nil {
			t.Fatalf("received error: %v", f.Err)
		}
		if !f.Envelope.HasVxlan() {
			t.Fatalf("Envelope has no vxlan wrapper: %v", f.Envelope)
		}
		dst := f.Envelope.GetDestination().GetV6().GetOctets()
		want := net.ParseIP("::1").To16()
		if len(dst) != 16 || net.IP(dst).String() != want.String() {
			t.Errorf("destination = %v, want ::1", net.IP(dst))
		}
	case <-ctx.Done():
		t.Fatal("did not receive a frame within the timeout")
	}
}
