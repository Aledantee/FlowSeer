//go:build linux

package rawsocket

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/filter"
)

// queuedFrame is one recvmsg result a fakeSocket hands out in order. data
// is what the fake copies into the caller's buffer; originalLen is what it
// reports as n, which can exceed len(data) to simulate MSG_TRUNC exactly as
// the kernel would: it fills the caller's buffer up to its own capacity and
// reports the true, longer wire length.
type queuedFrame struct {
	data        []byte
	oob         []byte
	originalLen int
}

// fakeSocket is packetSocket without a real file descriptor, so
// linuxLocalSource's Receive-loop logic (cancellation, error delivery,
// Close idempotency) is tested without CAP_NET_RAW.
type fakeSocket struct {
	mu       sync.Mutex
	queue    []queuedFrame
	afterErr error // returned once the queue is drained; nil means EAGAIN forever
	eintr    int   // number of leading recvmsg calls that return EINTR before anything else

	statsPackets, statsDrops uint64
	statsErr                 error

	closed   bool
	closeErr error
}

func (f *fakeSocket) recvmsg(p, oob []byte) (int, int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.eintr > 0 {
		f.eintr--
		return 0, 0, 0, unix.EINTR
	}
	if len(f.queue) == 0 {
		if f.afterErr != nil {
			return 0, 0, 0, f.afterErr
		}
		return 0, 0, 0, unix.EAGAIN
	}
	qf := f.queue[0]
	f.queue = f.queue[1:]
	copy(p, qf.data)
	copy(oob, qf.oob)
	return qf.originalLen, len(qf.oob), 0, nil
}

func packetAuxdata(status uint32, tci, tpid uint16) []byte {
	oob := make([]byte, unix.CmsgSpace(20))
	header := (*unix.Cmsghdr)(unsafe.Pointer(&oob[0]))
	header.SetLen(unix.CmsgLen(20))
	header.Level = unix.SOL_PACKET
	header.Type = unix.PACKET_AUXDATA
	data := oob[unix.CmsgLen(0):]
	binary.NativeEndian.PutUint32(data[:4], status)
	binary.NativeEndian.PutUint16(data[16:18], tci)
	binary.NativeEndian.PutUint16(data[18:20], tpid)
	return oob
}

func TestLinuxLocalSource_RestoresOffloadedVLAN(t *testing.T) {
	untagged := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x08, 0x00, 0x45}
	sock := &fakeSocket{queue: []queuedFrame{{
		data:        untagged,
		oob:         packetAuxdata(unix.TP_STATUS_VLAN_VALID, 1000, 0),
		originalLen: len(untagged),
	}}}
	s := newLinuxLocalSource(sock)
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case frame := <-s.Receive(ctx):
		want := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x81, 0x00, 0x03, 0xe8, 0x08, 0x00, 0x45}
		if string(frame.Data) != string(want) {
			t.Errorf("Data = %x, want %x", frame.Data, want)
		}
		if frame.OriginalLength != uint32(len(want)) {
			t.Errorf("OriginalLength = %d, want %d", frame.OriginalLength, len(want))
		}
	case <-ctx.Done():
		t.Fatal("did not receive a frame within the timeout")
	}
}

func TestRestoreVLAN_PreservesPriorityTagAndTPID(t *testing.T) {
	frame := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x08, 0x00}
	oob := packetAuxdata(unix.TP_STATUS_VLAN_VALID|unix.TP_STATUS_VLAN_TPID_VALID, 0xa000, 0x88a8)
	got, originalLength, err := restoreVLAN(frame, len(frame), oob)
	if err != nil {
		t.Fatalf("restoreVLAN: %v", err)
	}
	want := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x88, 0xa8, 0xa0, 0x00, 0x08, 0x00}
	if string(got) != string(want) {
		t.Errorf("Data = %x, want %x", got, want)
	}
	if originalLength != len(want) {
		t.Errorf("OriginalLength = %d, want %d", originalLength, len(want))
	}
}

func TestLinuxLocalSource_FiltersRestoredVLAN(t *testing.T) {
	match := &capturev1.VlanMatch{}
	match.SetVlanId(1000)
	clause := &capturev1.CaptureFilterClause{}
	clause.SetVlan(match)
	request := &capturev1.CaptureFilter{}
	request.SetAnyOf([]*capturev1.CaptureFilterClause{clause})
	insts, err := filter.Compile(request)
	if err != nil {
		t.Fatalf("compile filter: %v", err)
	}
	vm, err := bpf.NewVM(insts)
	if err != nil {
		t.Fatalf("build filter VM: %v", err)
	}

	untagged := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x08, 0x00}
	sock := &fakeSocket{queue: []queuedFrame{
		{data: untagged, originalLen: len(untagged)},
		{data: untagged, oob: packetAuxdata(unix.TP_STATUS_VLAN_VALID, 1000, 0), originalLen: len(untagged)},
	}}
	s := newLinuxLocalSource(sock)
	s.vm = vm
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case frame := <-s.Receive(ctx):
		want := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x81, 0x00, 0x03, 0xe8, 0x08, 0x00}
		if string(frame.Data) != string(want) {
			t.Errorf("Data = %x, want %x", frame.Data, want)
		}
	case <-ctx.Done():
		t.Fatal("did not receive a VLAN 1000 frame within the timeout")
	}
}

func (f *fakeSocket) stats() (uint64, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statsPackets, f.statsDrops, f.statsErr
}

func (f *fakeSocket) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}

func TestLinuxLocalSource_DeliversFrame(t *testing.T) {
	sock := &fakeSocket{queue: []queuedFrame{{data: []byte{1, 2, 3, 4}, originalLen: 4}}}
	s := newLinuxLocalSource(sock)
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := s.Receive(ctx)
	select {
	case f := <-frames:
		if f.Err != nil {
			t.Fatalf("received error: %v", f.Err)
		}
		if string(f.Data) != "\x01\x02\x03\x04" {
			t.Errorf("Data = %x, want 01020304", f.Data)
		}
		if f.OriginalLength != 4 {
			t.Errorf("OriginalLength = %d, want 4", f.OriginalLength)
		}
	case <-ctx.Done():
		t.Fatal("did not receive a frame within the timeout")
	}
}

// TestLinuxLocalSource_EINTRIsRetried proves a signal-interrupted recvmsg
// (EINTR) is retried within the same poll cycle rather than reported as a
// terminal error: SO_RCVTIMEO sockets never auto-restart on a signal, and
// Go's own runtime routinely delivers one to a goroutine blocked this long.
func TestLinuxLocalSource_EINTRIsRetried(t *testing.T) {
	sock := &fakeSocket{
		eintr: 3,
		queue: []queuedFrame{{data: []byte{9, 9}, originalLen: 2}},
	}
	s := newLinuxLocalSource(sock)
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := s.Receive(ctx)
	select {
	case f := <-frames:
		if f.Err != nil {
			t.Fatalf("received error after EINTR: %v", f.Err)
		}
		if string(f.Data) != "\x09\x09" {
			t.Errorf("Data = %x, want 0909", f.Data)
		}
	case <-ctx.Done():
		t.Fatal("did not receive a frame within the timeout")
	}
}

func TestLinuxLocalSource_Truncation(t *testing.T) {
	full := make([]byte, maxFrameLen)
	for i := range full {
		full[i] = byte(i)
	}
	// originalLen exceeds maxFrameLen: the wire frame was longer than the
	// receive buffer, exactly as MSG_TRUNC reports it.
	sock := &fakeSocket{queue: []queuedFrame{{data: full, originalLen: maxFrameLen + 1000}}}
	s := newLinuxLocalSource(sock)
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := s.Receive(ctx)
	select {
	case f := <-frames:
		if f.Err != nil {
			t.Fatalf("received error: %v", f.Err)
		}
		if len(f.Data) != maxFrameLen {
			t.Errorf("len(Data) = %d, want %d (clamped to the buffer)", len(f.Data), maxFrameLen)
		}
		if f.OriginalLength != maxFrameLen+1000 {
			t.Errorf("OriginalLength = %d, want %d (the true wire length)", f.OriginalLength, maxFrameLen+1000)
		}
	case <-ctx.Done():
		t.Fatal("did not receive a frame within the timeout")
	}
}

func TestLinuxLocalSource_CancellationUnblocksWithinOnePoll(t *testing.T) {
	sock := &fakeSocket{} // always EAGAIN: no data ever arrives
	s := newLinuxLocalSource(sock)
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	frames := s.Receive(ctx)

	time.AfterFunc(10*time.Millisecond, cancel)

	select {
	case f, ok := <-frames:
		// The channel is buffered (capacity 1) and nothing else is
		// competing for it here, so the terminal send cannot lose its
		// race: requiring ok catches the test vacuously passing if it ever
		// did.
		if !ok {
			t.Fatal("channel closed with no terminal frame")
		}
		if !errors.Is(f.Err, context.Canceled) {
			t.Errorf("terminal frame error = %v, want context.Canceled", f.Err)
		}
	case <-time.After(pollTimeout + 2*time.Second):
		t.Fatal("Receive did not unblock within one poll of cancellation")
	}
}

func TestLinuxLocalSource_ReadErrorIsTerminal(t *testing.T) {
	wantErr := errors.New("device gone")
	sock := &fakeSocket{afterErr: wantErr}
	s := newLinuxLocalSource(sock)
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := s.Receive(ctx)
	select {
	case f := <-frames:
		if f.Err == nil {
			t.Fatal("terminal frame carries no error")
		}
		if !errors.Is(f.Err, wantErr) {
			t.Errorf("terminal frame error = %v, want it to wrap %v", f.Err, wantErr)
		}
	case <-ctx.Done():
		t.Fatal("did not receive the terminal error frame within the timeout")
	}

	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("channel delivered a frame after the terminal error")
		}
	case <-ctx.Done():
		t.Fatal("channel did not close after the terminal error")
	}
}

func TestLinuxLocalSource_CloseIsIdempotent(t *testing.T) {
	sock := &fakeSocket{}
	s := newLinuxLocalSource(sock)

	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !sock.closed {
		t.Error("underlying socket was never closed")
	}
}

func TestLinuxLocalSource_Stats(t *testing.T) {
	sock := &fakeSocket{statsPackets: 100, statsDrops: 3}
	s := newLinuxLocalSource(sock)
	defer func() { _ = s.Close() }()

	received, dropped, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if received != 100 || dropped != 3 {
		t.Errorf("Stats = (%d, %d), want (100, 3)", received, dropped)
	}
}

func TestLinuxLocalSource_StatsAfterClose(t *testing.T) {
	sock := &fakeSocket{}
	s := newLinuxLocalSource(sock)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, err := s.Stats(); err == nil {
		t.Fatal("Stats after Close: want an error, got nil")
	}
}
