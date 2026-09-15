//go:build linux

package link

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/gopacket/gopacket"
	"golang.org/x/net/bpf"
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
