//go:build linux || freebsd

package rawsocket

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

type controlMirrorSocket struct {
	reads      int
	flags      int
	bufferSize int
}

var errMirrorQueueDrained = errors.New("mirror queue drained")

func (s *controlMirrorSocket) recvmsg(p, oob []byte) (int, int, int, unix.Sockaddr, error) {
	s.reads++
	if s.reads > 1 {
		return 0, 0, 0, nil, errMirrorQueueDrained
	}
	s.bufferSize = len(oob)
	n := copy(p, []byte("inner frame"))
	oobn := copy(oob, []byte{42})
	return n, oobn, s.flags, &unix.SockaddrInet4{Addr: [4]byte{192, 0, 2, 1}}, nil
}

func (s *controlMirrorSocket) close() error { return nil }

func checkMirrorInterface(t *testing.T, index uint32, indexOK bool, wantReceived uint64) {
	t.Helper()
	sock := &controlMirrorSocket{}
	parserCalls := 0
	src := &mirrorSource{done: make(chan struct{})}
	paired := &mirrorReceiverSocket{
		socket:      sock,
		oobSize:     73,
		wantedIndex: 3,
		parseControl: func(oob []byte) (net.IP, uint32, bool) {
			parserCalls++
			if !bytes.Equal(oob, []byte{42}) {
				t.Errorf("control bytes = %v, want [42]", oob)
			}
			return net.IP{192, 0, 2, 2}, index, indexOK
		},
	}
	frames := make(chan Frame, 2)
	decodeCalls := 0
	decode := func(payload []byte, _, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
		decodeCalls++
		if !dst.Equal(net.IP{192, 0, 2, 2}) {
			t.Errorf("destination = %v, want 192.0.2.2", dst)
		}
		return nil, payload, nil
	}
	src.runMirrorLoop(context.Background(), paired, decode, frames)
	if sock.reads != 2 || parserCalls != 1 || sock.bufferSize != 73 {
		t.Fatalf("reads, parser calls, buffer size = %d, %d, %d, want 2, 1, 73", sock.reads, parserCalls, sock.bufferSize)
	}
	if got, drops, err := src.Stats(); got != wantReceived || drops != 0 || err != nil {
		t.Errorf("Stats = (%d, %d, %v), want (%d, 0, nil)", got, drops, err, wantReceived)
	}
	if decodeCalls != int(wantReceived) {
		t.Errorf("decode calls = %d, want %d", decodeCalls, wantReceived)
	}
	if wantReceived == 1 {
		f := <-frames
		if f.Err != nil || !bytes.Equal(f.Data, []byte("inner frame")) {
			t.Fatalf("frame = %+v, want inner frame without error", f)
		}
	}
	f := <-frames
	if !errors.Is(f.Err, errMirrorQueueDrained) {
		t.Fatalf("terminal frame error = %v, want %v", f.Err, errMirrorQueueDrained)
	}
	if len(frames) != 0 {
		t.Errorf("remaining frames = %d, want 0", len(frames))
	}
}

func TestMirrorSource_DropsOtherInterfaceBeforeCounting(t *testing.T) {
	checkMirrorInterface(t, 7, true, 0)
}

func TestMirrorSource_DeliversBoundInterface(t *testing.T) {
	checkMirrorInterface(t, 3, true, 1)
}

func TestMirrorSource_MissingIndexIsDroppedWhenBound(t *testing.T) {
	checkMirrorInterface(t, 3, false, 0)
}

func TestMirrorSource_ControlTruncationIsTerminal(t *testing.T) {
	sock := &controlMirrorSocket{flags: unix.MSG_CTRUNC}
	src := &mirrorSource{done: make(chan struct{})}
	paired := &mirrorReceiverSocket{
		socket:  sock,
		oobSize: 73,
		parseControl: func([]byte) (net.IP, uint32, bool) {
			t.Error("parser called for truncated control data")
			return nil, 0, false
		},
	}
	frames := make(chan Frame, 2)
	decode := func(payload []byte, _, _ net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
		t.Error("decoder called for truncated control data")
		return nil, payload, nil
	}
	src.runMirrorLoop(context.Background(), paired, decode, frames)
	if sock.reads != 1 {
		t.Errorf("reads = %d, want 1", sock.reads)
	}
	if got, drops, err := src.Stats(); got != 0 || drops != 0 || err != nil {
		t.Errorf("Stats = (%d, %d, %v), want (0, 0, nil)", got, drops, err)
	}
	f := <-frames
	code, _ := errs.CodeOf(f.Err)
	if f.Err == nil || !strings.Contains(f.Err.Error(), "mirror control data truncated") || code != ErrCodeSourceOpen {
		t.Errorf("terminal error = %v, want capture/source-open for truncated control data", f.Err)
	}
	if len(frames) != 0 {
		t.Errorf("remaining frames = %d, want 0", len(frames))
	}
}

func TestSplitEncapsulations(t *testing.T) {
	gre, udp := splitEncapsulations([]capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_II,
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN,
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP,
	})
	if !gre {
		t.Error("greFamily = false, want true")
	}
	if len(udp) != 2 {
		t.Errorf("udpFamily = %v, want 2 entries", udp)
	}
}
