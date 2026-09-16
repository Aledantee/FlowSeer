//go:build linux

package link

import (
	"context"
	"sync"
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
// mu guards the socket and closed flag. Close waits for the current poll;
// done also wakes Receive when it is waiting for the consumer to read.
type linuxLeg struct {
	tp     packetSocket
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

type packetSocket interface {
	ReadPacketData() ([]byte, gopacket.CaptureInfo, error)
	WritePacketData([]byte) error
	SetBPF([]bpf.RawInstruction) error
	Close()
}

func open(iface string) (Leg, error) {
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

	return &linuxLeg{tp: tp, done: make(chan struct{})}, nil
}

// Send skips canceled writes and serializes socket access with Receive and Close.
func (l *linuxLeg) Send(ctx context.Context, pkt []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if l.closed {
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

// SetFilter replaces the socket filter; an empty program removes it.
// Call SetFilter before Receive.
func (l *linuxLeg) SetFilter(raw []RawInstruction) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
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

// Close waits for an active poll before releasing the socket, then wakes any
// receiver waiting for its consumer. Repeated calls do nothing.
func (l *linuxLeg) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	l.tp.Close()
	l.closed = true
	close(l.done)

	return nil
}

// readOnce performs one guarded read. open is false when the leg has been
// closed under the lock.
//
// The lock is released by a defer because this runs on a supervised
// goroutine: a panic in ReadPacketData is recovered, so a release written
// after the call would be skipped and l.mu would stay held for the life of
// the process, leaving Close blocked on it forever — a hang in place of the
// crash the recovery replaced.
func (l *linuxLeg) readOnce() (data []byte, open bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil, false, nil
	}
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
