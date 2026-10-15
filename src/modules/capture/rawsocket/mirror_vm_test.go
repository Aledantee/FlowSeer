//go:build freebsd && rawsocket_vm

package rawsocket

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
)

const envMirror = "FLOWSEER_RAWSOCKET_MIRROR"

func openMirrorVM(t *testing.T, enc capturev1.MirrorEncapsulation, iface string) (Source, <-chan Frame) {
	t.Helper()
	ifaceFromEnv(t, envMirror)
	// FreeBSD sys/net/if_vxlan.h defines VXLAN_PORT as 4789.
	src, err := OpenMirrorReceiver([]capturev1.MirrorEncapsulation{enc}, 4789, iface, nil)
	if err != nil {
		t.Fatalf("OpenMirrorReceiver: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return src, src.Receive(ctx)
}

func checkHostVXLAN(t *testing.T, f Frame) {
	t.Helper()
	if !f.Envelope.HasVxlan() || f.Envelope.GetVxlan().GetVni() != 42 {
		t.Fatalf("Envelope = %v, want VXLAN VNI 42", f.Envelope)
	}
	checkMirrorDestination(t, f)
	// Ethernet type ARP, hardware Ethernet, protocol IPv4, request opcode.
	if len(f.Data) < 42 || !bytes.Equal(f.Data[12:22], []byte{8, 6, 0, 1, 8, 0, 6, 4, 0, 1}) {
		t.Fatalf("inner frame = % x, want Ethernet ARP request", f.Data)
	}
	if !bytes.Equal(f.Data[38:42], []byte{10, 99, 42, 2}) {
		t.Errorf("ARP target = %v, want 10.99.42.2", net.IP(f.Data[38:42]))
	}
}

func checkMirrorDestination(t *testing.T, f Frame) {
	t.Helper()
	got := f.Envelope.GetDestination().GetV4().GetOctets()
	if !bytes.Equal(got, []byte{10, 99, 0, 2}) {
		t.Errorf("destination = %v, want 10.99.0.2", net.IP(got))
	}
}

func TestMirrorReceiverDecodesHostVXLAN(t *testing.T) {
	_, frames := openMirrorVM(t, capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN, "")
	checkHostVXLAN(t, nextFrame(t, frames))
}

func waitMirrorCount(t *testing.T, src Source, frames <-chan Frame) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	var total uint64
	for {
		select {
		case f := <-frames:
			t.Fatalf("GRE IPv4 payload delivered a frame: %+v", f)
		case <-ticker.C:
			received, _, err := src.Stats()
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			total += received
		case <-deadline.C:
			if total == 0 {
				t.Fatal("no host GRE datagram counted within 3s")
			}
			return
		}
	}
}

func TestMirrorReceiverCountsHostGRE(t *testing.T) {
	src, frames := openMirrorVM(t, capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_GRE, "")
	waitMirrorCount(t, src, frames)
}

func TestMirrorReceiverDecodesTEBGRE(t *testing.T) {
	ifaceFromEnv(t, envMirror)
	src, err := OpenMirrorReceiver([]capturev1.MirrorEncapsulation{capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_GRE}, 0, "", nil)
	if err != nil {
		t.Fatalf("OpenMirrorReceiver: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	s := src.(*mirrorSource)
	indices := make(chan uint32, 16)
	s.rawV4.parseControl = func(oob []byte) (net.IP, uint32, bool) {
		dst, index, ok := freeBSDControlV4(oob)
		if dst.Equal(net.IPv4(10, 99, 0, 2)) {
			select {
			case indices <- index:
			default:
			}
		}
		return dst, index, ok
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := src.Receive(ctx)
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_GRE)
	if err != nil {
		t.Fatalf("sender socket: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	if err := unix.Bind(fd, &unix.SockaddrInet4{Addr: [4]byte{10, 99, 0, 3}}); err != nil {
		t.Fatalf("bind sender: %v", err)
	}
	inner := testFrame(nil)
	payload := append([]byte{0, 0, 0x65, 0x58}, inner...)
	if err := unix.Sendto(fd, payload, 0, &unix.SockaddrInet4{Addr: [4]byte{10, 99, 0, 2}}); err != nil {
		t.Fatalf("send TEB GRE: %v", err)
	}
	f := nextFrame(t, frames)
	if !f.Envelope.HasGre() || !bytes.Equal(f.Data, inner) {
		t.Fatalf("frame = %+v, want GRE with inner frame % x", f, inner)
	}
	checkMirrorDestination(t, f)
	// lo0's receive index is unverified on 14.3. Read and report it without
	// requiring the packet to arrive on epair0b.
	select {
	case index := <-indices:
		t.Logf("raw IPv4 receive interface index = %d", index)
	default:
		t.Error("no control message destination 10.99.0.2 observed")
	}
}

func TestMirrorReceiverBindInterfaceDropsOtherInterface(t *testing.T) {
	// Establish that the host VXLAN fixture is producing decodable traffic.
	witness, witnessFrames := openMirrorVM(t, capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN, "")
	checkHostVXLAN(t, nextFrame(t, witnessFrames))
	if err := witness.Close(); err != nil {
		t.Fatalf("close witness: %v", err)
	}
	src, frames := openMirrorVM(t, capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN, ifaceFromEnv(t, envLocalB))
	// Observe four host ping intervals to detect wrongly admitted traffic.
	select {
	case f := <-frames:
		t.Fatalf("other-interface traffic delivered a frame: %+v", f)
	case <-time.After(2 * time.Second):
	}
	received, _, err := src.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if received != 0 {
		t.Errorf("received = %d, want 0", received)
	}
}

func TestMirrorReceiverBindInterfaceKeepsBoundInterface(t *testing.T) {
	src, frames := openMirrorVM(t, capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_GRE, ifaceFromEnv(t, envMirror))
	waitMirrorCount(t, src, frames)
}
