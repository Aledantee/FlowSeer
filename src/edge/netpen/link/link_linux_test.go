//go:build linux

package link

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/afpacket"
	"golang.org/x/net/bpf"

	"go.aledante.io/FlowSeer/src/common/errs"
)

type fakePacketSocket struct {
	writes int
	closes int
}

func (*fakePacketSocket) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	return []byte{1}, gopacket.CaptureInfo{}, nil
}

func (s *fakePacketSocket) WritePacketData([]byte) error {
	s.writes++
	return nil
}

func (*fakePacketSocket) SetBPF([]bpf.RawInstruction) error { return nil }

func (s *fakePacketSocket) Close() { s.closes++ }

// panicPacketSocket panics on every read, standing in for a driver bug in
// the receive loop [linuxLeg.Receive] spawns.
type panicPacketSocket struct{}

func (panicPacketSocket) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	panic("boom")
}

func (panicPacketSocket) WritePacketData([]byte) error { return nil }

func (panicPacketSocket) SetBPF([]bpf.RawInstruction) error { return nil }

func (panicPacketSocket) Close() {}

// TestLinuxReceivePanicReportsTerminalFrameBeforeClose proves the goroutine
// spawn.Go starts for Receive reports a panic as a terminal Frame before
// frames closes, not merely eventually: a consumer draining frames must see
// the error, not a channel that closed with no explanation.
func TestLinuxReceivePanicReportsTerminalFrameBeforeClose(t *testing.T) {
	leg := &linuxLeg{tp: panicPacketSocket{}, done: make(chan struct{})}
	frames := leg.Receive(context.Background())

	frame, ok := <-frames
	if !ok {
		t.Fatal("frames closed with no terminal frame; the panic must be reported before close")
	}
	if frame.Err == nil {
		t.Fatal("terminal frame carries no error; the recovered panic was not attached")
	}

	if _, stillOpen := <-frames; stillOpen {
		t.Fatal("frames stayed open after the terminal frame")
	}
}

func TestLinuxReceiveShutdownWithFullChannel(t *testing.T) {
	for _, shutdown := range []string{"close", "cancel"} {
		t.Run(shutdown, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				socket := &fakePacketSocket{}
				leg := &linuxLeg{tp: socket, done: make(chan struct{})}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				frames := leg.Receive(ctx)
				synctest.Wait()

				if shutdown == "cancel" {
					cancel()
				} else if err := leg.Close(); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()

				packets := 0
				for frame := range frames {
					if frame.Err == nil {
						packets++
					}
				}
				if packets != 1 {
					t.Errorf("buffered packets after shutdown = %d, want 1", packets)
				}
				for range 2 {
					if err := leg.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if socket.closes != 1 {
					t.Errorf("socket close calls = %d, want 1", socket.closes)
				}
			})
		})
	}
}

// blockingPacketSocket parks every read until release is closed and records
// the order of the read's return and the socket close, standing in for a
// poll that waits its full timeout with nothing on the wire.
type blockingPacketSocket struct {
	readStarted chan struct{}
	release     chan struct{}
	mu          sync.Mutex
	order       []string
}

func newBlockingPacketSocket() *blockingPacketSocket {
	return &blockingPacketSocket{readStarted: make(chan struct{}, 1), release: make(chan struct{})}
}

func (s *blockingPacketSocket) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	select {
	case s.readStarted <- struct{}{}:
	default:
	}
	<-s.release
	s.mu.Lock()
	s.order = append(s.order, "read-returned")
	s.mu.Unlock()
	return nil, gopacket.CaptureInfo{}, afpacket.ErrTimeout
}

func (*blockingPacketSocket) WritePacketData([]byte) error { return nil }

func (*blockingPacketSocket) SetBPF([]bpf.RawInstruction) error { return nil }

func (s *blockingPacketSocket) Close() {
	s.mu.Lock()
	s.order = append(s.order, "socket-closed")
	s.mu.Unlock()
}

// TestLinuxSendDoesNotWaitOnPoll pins that a write completes while a poll
// is parked, since a behavior that paces its sends would otherwise lose up
// to the poll timeout on every frame. The 20 ms bound is the timing under
// test: it is well under the 100 ms poll the write must not wait on.
func TestLinuxSendDoesNotWaitOnPoll(t *testing.T) {
	socket := newBlockingPacketSocket()
	leg := &linuxLeg{tp: socket, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	frames := leg.Receive(ctx)
	<-socket.readStarted

	start := time.Now()
	err := leg.Send(ctx, []byte{1})
	elapsed := time.Since(start)

	close(socket.release)
	cancel()
	for range frames {
	}
	if err != nil {
		t.Fatalf("Send during a poll: %v", err)
	}
	if elapsed >= 20*time.Millisecond {
		t.Errorf("Send took %v while a poll was parked, want under 20ms", elapsed)
	}
}

// TestLinuxCloseWaitsForPollThenRefusesSend pins the close order: Close
// returns only after the parked poll has returned, the socket closes after
// that poll, and a later Send fails with the leg-open code.
func TestLinuxCloseWaitsForPollThenRefusesSend(t *testing.T) {
	socket := newBlockingPacketSocket()
	leg := &linuxLeg{tp: socket, done: make(chan struct{})}
	frames := leg.Receive(context.Background())
	<-socket.readStarted

	closed := make(chan error, 1)
	go func() { closed <- leg.Close() }()
	close(socket.release)
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}
	for range frames {
	}

	socket.mu.Lock()
	order := append([]string(nil), socket.order...)
	socket.mu.Unlock()
	if len(order) != 2 || order[0] != "read-returned" || order[1] != "socket-closed" {
		t.Errorf("order = %v, want [read-returned socket-closed]", order)
	}

	err := leg.Send(context.Background(), []byte{1})
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeLegOpen {
		t.Errorf("Send after Close = %v (code %q ok %v), want %q", err, code, ok, ErrCodeLegOpen)
	}
}

func TestLinuxSendCanceledContext(t *testing.T) {
	socket := &fakePacketSocket{}
	leg := &linuxLeg{tp: socket}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := leg.Send(ctx, []byte{1}); !errors.Is(err, context.Canceled) {
		t.Errorf("Send error = %v, want context.Canceled", err)
	}
	if socket.writes != 0 {
		t.Errorf("socket writes = %d, want 0", socket.writes)
	}
}
