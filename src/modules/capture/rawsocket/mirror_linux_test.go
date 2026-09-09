//go:build linux

package rawsocket

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/filter"
)

// queuedDatagram is one recvmsg result a fakeMirrorSocket hands out in
// order. from is the sender's address; oob is the raw ancillary-data bytes
// (empty to exercise the no-PKTINFO degrade path, since hand-constructing a
// well-formed IP_PKTINFO/IPV6_PKTINFO ancillary message risks getting this
// package's own platform-specific Cmsghdr layout wrong without a way to
// execute the result — see TestOpenMirrorUDP_RealPktinfo for the real,
// kernel-constructed case instead).
type queuedDatagram struct {
	payload []byte
	from    unix.Sockaddr
}

type fakeMirrorSocket struct {
	queue    []queuedDatagram
	afterErr error // returned once the queue is drained; nil means EAGAIN forever
	closed   bool
}

func (f *fakeMirrorSocket) recvmsg(p, _ []byte) (int, int, unix.Sockaddr, error) {
	if len(f.queue) == 0 {
		if f.afterErr != nil {
			return 0, 0, nil, f.afterErr
		}
		return 0, 0, nil, unix.EAGAIN
	}
	qd := f.queue[0]
	f.queue = f.queue[1:]
	n := copy(p, qd.payload)
	return n, 0, qd.from, nil
}

func (f *fakeMirrorSocket) close() error {
	f.closed = true
	return nil
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("mustHex(%q): %v", s, err)
	}
	return b
}

// erspanTypeIPayload is a GRE/ERSPAN Type I packet (protocol 0x88BE, no
// sequence number) carrying a short inner frame, the same shape
// src/modules/capture/mirror's own decode_test.go fixtures use.
func erspanTypeIPayload(t *testing.T) []byte {
	t.Helper()
	return mustHex(t, "000088be001122334455aabbccddeeff08004954")
}

func TestLinuxMirrorSource_DeliversDecodedFrame(t *testing.T) {
	sock := &fakeMirrorSocket{queue: []queuedDatagram{
		{payload: erspanTypeIPayload(t), from: &unix.SockaddrInet4{Addr: [4]byte{192, 0, 2, 1}}},
	}}
	src := &linuxMirrorSource{rawV6: sock, done: make(chan struct{})}
	defer func() { _ = src.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := src.Receive(ctx)
	select {
	case f := <-frames:
		if f.Err != nil {
			t.Fatalf("received error: %v", f.Err)
		}
		if f.Envelope == nil || !f.Envelope.HasErspanTypeI() {
			t.Fatalf("Envelope = %v, want an erspan_type_i wrapper", f.Envelope)
		}
		if len(f.Data) == 0 {
			t.Errorf("Data is empty, want the inner frame")
		}
		// No ancillary data was queued, so the destination degrades to the
		// source address rather than dropping the packet's metadata.
		if f.Envelope.GetDestination().GetV4() == nil {
			t.Errorf("Destination has no IPv4 address after the no-PKTINFO degrade")
		}
	case <-ctx.Done():
		t.Fatal("did not receive a frame within the timeout")
	}

	if r, _, _ := src.Stats(); r == 0 {
		t.Errorf("Stats received = 0, want at least 1")
	}
}

func TestLinuxMirrorSource_FilterRejects(t *testing.T) {
	sock := &fakeMirrorSocket{queue: []queuedDatagram{
		{payload: erspanTypeIPayload(t), from: &unix.SockaddrInet4{Addr: [4]byte{192, 0, 2, 1}}},
	}}
	// A filter compiled to reject every packet.
	vm, err := bpf.NewVM([]bpf.Instruction{bpf.RetConstant{Val: 0}})
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	src := &linuxMirrorSource{rawV6: sock, vm: vm, done: make(chan struct{})}
	defer func() { _ = src.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	frames := src.Receive(ctx)
	select {
	case f, ok := <-frames:
		if ok && f.Err == nil {
			t.Fatalf("received a frame the filter should have rejected: %+v", f)
		}
		// A closed channel or a context-cancellation terminal frame both
		// mean no accepted frame arrived, which is correct here.
	case <-ctx.Done():
		// No frame arrived before the deadline: also correct.
	}

	if r, _, _ := src.Stats(); r == 0 {
		t.Errorf("Stats received = 0, want at least 1 (counted even though filtered out)")
	}
}

func TestLinuxMirrorSource_ReadErrorIsTerminal(t *testing.T) {
	wantErr := errors.New("device gone")
	sock := &fakeMirrorSocket{afterErr: wantErr}
	src := &linuxMirrorSource{rawV6: sock, done: make(chan struct{})}
	defer func() { _ = src.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := src.Receive(ctx)
	select {
	case f := <-frames:
		if f.Err == nil || !errors.Is(f.Err, wantErr) {
			t.Errorf("terminal frame error = %v, want it to wrap %v", f.Err, wantErr)
		}
	case <-ctx.Done():
		t.Fatal("did not receive the terminal error frame within the timeout")
	}
}

func TestLinuxMirrorSource_CloseIsIdempotent(t *testing.T) {
	sock := &fakeMirrorSocket{}
	src := &linuxMirrorSource{rawV6: sock, done: make(chan struct{})}

	if err := src.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !sock.closed {
		t.Error("underlying socket was never closed")
	}
}

// TestOpenMirrorReceiver_BuildsVMFromRealFilterProgram proves
// openMirrorReceiver can actually build its filter VM from the raw form
// filter.Assemble produces (a []bpf.RawInstruction) rather than the
// []bpf.Instruction bpf.NewVM needs directly: a RawInstruction satisfies the
// Instruction interface syntactically, so this mismatch compiles, but
// bpf.NewVM's own check that a program ends in RetA or RetConstant rejects
// every such program, and its dispatch has no RawInstruction case at all.
// encapsulations is deliberately empty so the call reaches the "no
// supported mirror encapsulation" validation right after VM construction
// and returns before ever opening a privileged socket, keeping this a
// no-root unit test.
func TestOpenMirrorReceiver_BuildsVMFromRealFilterProgram(t *testing.T) {
	insts, err := filter.Compile(nil)
	if err != nil {
		t.Fatalf("filter.Compile: %v", err)
	}
	prog, err := filter.Assemble(insts)
	if err != nil {
		t.Fatalf("filter.Assemble: %v", err)
	}

	_, err = openMirrorReceiver(nil, 0, "", prog)
	if err == nil {
		t.Fatal("openMirrorReceiver: want an error for no configured encapsulation, got nil")
	}
	if !strings.Contains(err.Error(), "no GRE-family or UDP-family encapsulation configured") {
		t.Fatalf("openMirrorReceiver failed before reaching the encapsulation check (likely the filter VM build itself): %v", err)
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
