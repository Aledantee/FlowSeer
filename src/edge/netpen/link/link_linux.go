//go:build linux

package link

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/afpacket"
	"golang.org/x/net/bpf"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// pollTimeout is the per-poll wait. It bounds how long a blocked Receive
// stays unaware of ctx cancellation: the loop checks ctx.Done() after each
// poll returns, so cancellation lands within this window.
const pollTimeout = 100 * time.Millisecond

// linuxLeg wraps an afpacket.TPacket behind the [Leg] interface.
//
// Two locks keep a write from waiting on a poll: mu is the lifecycle lock
// under which closed flips and a poll registers itself in polls, and wmu
// orders every write and the final socket close. afpacket's Close writes
// the socket's fields with no lock of its own while WritePacketData reads
// them, so Close takes wmu before releasing the socket and only after the
// last registered poll has returned. done also wakes Receive when it is
// waiting for the consumer to read.
type linuxLeg struct {
	tp     packetSocket
	addr   Addr
	mu     sync.Mutex // guards closed transitions and polls registration
	wmu    sync.Mutex // orders Send, SetFilter, and the socket close
	polls  sync.WaitGroup
	closed atomic.Bool
	done   chan struct{}
}

type packetSocket interface {
	ReadPacketData() ([]byte, gopacket.CaptureInfo, error)
	WritePacketData([]byte) error
	SetBPF([]bpf.RawInstruction) error
	Close()
}

func open(iface string) (Leg, error) {
	addr, err := readAddr(iface)
	if err != nil {
		return nil, err
	}
	tp, err := afpacket.NewTPacket(
		afpacket.OptInterface(iface),
		afpacket.TPacketVersion3,
		afpacket.OptPollTimeout(pollTimeout),
	)
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeLegOpen).
			Attr("iface", iface).
			UserMsg("could not open the network interface for raw capture").
			Hint("check the interface name and that you are root").
			Msgf("afpacket open %q", iface)
	}

	if err := tp.SetPromiscuous(true); err != nil {
		tp.Close()
		return nil, errs.From(err).
			Code(ErrCodeLegOpen).
			Attr("iface", iface).
			Msgf("promiscuous mode on %q", iface)
	}

	return &linuxLeg{tp: tp, addr: addr, done: make(chan struct{})}, nil
}

func (l *linuxLeg) Addr() Addr { return l.addr }

// Send skips canceled writes and orders the write against SetFilter and
// Close, and it never waits on a poll.
func (l *linuxLeg) Send(ctx context.Context, pkt []byte) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if l.closed.Load() {
		return errs.New().
			Code(ErrCodeLegOpen).
			Msg("send on closed leg")
	}

	if err := l.tp.WritePacketData(pkt); err != nil {
		return errs.From(err).
			Code(ErrCodeLegOpen).
			Msg("write packet data")
	}

	return nil
}

// SetFilter replaces the socket filter, and an empty program removes it.
// Call SetFilter before Receive.
func (l *linuxLeg) SetFilter(raw []RawInstruction) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()

	if l.closed.Load() {
		return errs.New().
			Code(ErrCodeLegOpen).
			Msg("set filter on closed leg")
	}
	insts := make([]bpf.RawInstruction, len(raw))
	for i, r := range raw {
		insts[i] = bpf.RawInstruction{Op: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}

	return l.tp.SetBPF(insts)
}

// Receive closes its channel after cancellation or Close, even if the
// consumer stops reading. Terminal error delivery is best effort.
func (l *linuxLeg) Receive(ctx context.Context) <-chan Frame {
	frames := make(chan Frame, 1)

	// receive owns no terminal close of its own: every exit path below
	// already calls sendTerminal before returning, and spawn.Go's fn
	// closes frames once receive returns. The panic path closes through
	// the ReportTo sink instead, sending the recovered panic as the
	// terminal frame first, so a consumer draining frames until close
	// still learns why the receiver stopped.
	receive := func() {
		for {
			select {
			case <-ctx.Done():
				sendTerminal(frames, Frame{Err: ctx.Err()})
				return
			case <-l.done:
				return
			default:
			}

			data, open, err := l.readOnce()
			if !open {
				sendTerminal(frames, Frame{Err: errs.New().
					Code(ErrCodeLegOpen).
					Msg("receive on closed leg")})
				return
			}

			if err != nil {
				if isTimeout(err) {
					select {
					case <-ctx.Done():
						sendTerminal(frames, Frame{Err: ctx.Err()})
						return
					default:
						continue
					}
				}

				sendTerminal(frames, Frame{Err: err})
				return
			}

			select {
			case frames <- Frame{Data: data}:
			case <-ctx.Done():
				sendTerminal(frames, Frame{Err: ctx.Err()})
				return
			case <-l.done:
				return
			}
		}
	}

	spawn.Go(ctx, "netpen link receive", func() {
		receive()
		close(frames)
	}, spawn.ReportTo(func(err error) {
		sendTerminal(frames, Frame{Err: err})
		close(frames)
	}))

	return frames
}

// Close marks the leg closed, wakes any receiver waiting for its consumer,
// waits for an active poll, and then releases the socket under the write
// lock so no write is in flight when the socket's fields change. Repeated
// calls do nothing.
func (l *linuxLeg) Close() error {
	l.mu.Lock()
	if l.closed.Swap(true) {
		l.mu.Unlock()
		return nil
	}
	close(l.done)
	l.mu.Unlock()

	l.polls.Wait()

	l.wmu.Lock()
	l.tp.Close()
	l.wmu.Unlock()

	return nil
}

// readOnce performs one read registered against Close. open is false when
// the leg was closed before the read could register, so Close's wait covers
// every read that reaches the socket.
//
// The registration is released by a defer because this runs on a supervised
// goroutine: a panic in ReadPacketData is recovered, so a release written
// after the call would be skipped and Close would wait on the poll for the
// life of the process, a hang in place of the crash the recovery replaced.
func (l *linuxLeg) readOnce() (data []byte, open bool, err error) {
	l.mu.Lock()
	if l.closed.Load() {
		l.mu.Unlock()
		return nil, false, nil
	}
	l.polls.Add(1)
	l.mu.Unlock()
	defer l.polls.Done()

	data, _, err = l.tp.ReadPacketData()
	return data, true, err
}

// An abandoned consumer must not strand Receive on the final error frame.
func sendTerminal(frames chan<- Frame, f Frame) {
	select {
	case frames <- f:
	default:
	}
}

func isTimeout(err error) bool {
	return err == afpacket.ErrTimeout
}

var _ Leg = (*linuxLeg)(nil)
