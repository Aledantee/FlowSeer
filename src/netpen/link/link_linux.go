// link_linux.go is the AF_PACKET leg implementation. It wraps afpacket.TPacket
// (v3 ring RX, promiscuous, WritePacketData TX) with a poll-loop cancellation
// contract: Receive polls with a short timeout and checks ctx.Done() each
// cycle, and Close from the signal path unblocks the poll (KTD4).
//
// AF_PACKET uses unix.Poll, not the Go netpoller, so there is no read deadline.
// The fd lifecycle is mutex-guarded against the polling goroutine: poll setup
// and fd close hold the same lock, so close-during-poll is an orderly EBADF
// exit rather than a use-after-close race. The contract is verified under
// `go test -race`.
//
//go:build linux

package link

import (
	"context"
	"sync"
	"time"

	"github.com/gopacket/gopacket/afpacket"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// pollTimeout is the per-poll wait. It bounds how long a blocked Receive
// stays unaware of ctx cancellation: the loop checks ctx.Done() after each
// poll returns, so cancellation lands within this window.
const pollTimeout = 100 * time.Millisecond

// linuxLeg wraps an afpacket.TPacket behind the [Leg] interface.
//
// The fd lifecycle is guarded by mu: poll setup (ReadPacketData) and fd close
// hold the same lock, so a Close from the signal path cannot race with an
// in-flight poll. Once closed is set, further operations return an error
// matching [ErrCodeLegOpen] rather than touching the freed socket.
type linuxLeg struct {
	tp     *afpacket.TPacket
	mu     sync.Mutex
	closed bool
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

	return &linuxLeg{tp: tp}, nil
}

// Send writes pkt to the wire. It takes mu so it cannot race with Close.
func (l *linuxLeg) Send(_ context.Context, pkt []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

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

// SetFilter installs a BPF program on the underlying socket. It must be
// called before Receive.
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

// Receive returns a channel that delivers captured frames until ctx is
// canceled or the leg is closed. It runs a single polling goroutine that
// calls ReadPacketData (which uses unix.Poll under the hood) and checks
// ctx.Done() after each poll returns. Close unblocks the poll by closing the
// fd under mu; the poll sees EBADF and the loop exits.
func (l *linuxLeg) Receive(ctx context.Context) <-chan Frame {
	frames := make(chan Frame)

	go func() {
		defer close(frames)

		for {
			select {
			case <-ctx.Done():
				frames <- Frame{Err: ctx.Err()}
				return
			default:
			}

			// Poll setup under the lock: if Close won the race, the
			// fd is already -1 and ReadPacketData returns an error,
			// which we treat as a terminal close.
			l.mu.Lock()
			if l.closed {
				l.mu.Unlock()
				frames <- Frame{Err: errs.New().
					Code(ErrCodeLegOpen).
					Msg("receive on closed leg")}
				return
			}
			data, _, err := l.tp.ReadPacketData()
			l.mu.Unlock()

			if err != nil {
				// A poll timeout is not terminal: the loop retries
				// after the ctx.Done() check. EBADF (from Close
				// winning the race) or any other error is terminal.
				if isTimeout(err) {
					select {
					case <-ctx.Done():
						frames <- Frame{Err: ctx.Err()}
						return
					default:
						continue
					}
				}

				frames <- Frame{Err: err}
				return
			}

			select {
			case frames <- Frame{Data: data}:
			case <-ctx.Done():
				frames <- Frame{Err: ctx.Err()}
				return
			}
		}
	}()

	return frames
}

// Close releases the socket and ring buffer. It is idempotent: the closed flag
// under mu makes a second call a no-op. It unblocks an active poll by closing
// the fd; the polling goroutine sees the error and exits.
func (l *linuxLeg) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	l.tp.Close()
	l.closed = true

	return nil
}

// isTimeout reports whether err is a poll timeout. afpacket returns
// ErrTimeout on a poll that returned no packets within the timeout.
func isTimeout(err error) bool {
	return err == afpacket.ErrTimeout
}

// Compile-time assertion that linuxLeg satisfies Leg.
var _ Leg = (*linuxLeg)(nil)

// unixETH_PAll silences the unused import warning on builds where unix is
// only referenced transitively through afpacket; it documents the default
// protocol.
const _ = unix.ETH_P_ALL
