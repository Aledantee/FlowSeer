// Package link wraps the raw AF_PACKET socket behind a leg abstraction with
// an explicit cancellation contract, so the protocol layer and attacks never
// touch a file descriptor directly.
//
// Both legs — the attack leg (-i) and the optional watch leg (-w) — are the
// same shape: promiscuous AF_PACKET v3 ring RX with WritePacketData TX and
// programmatic BPF filters. The [Leg] interface decouples the runtime from
// that detail so the non-Linux build compiles and tests drive a mock.
package link

import (
	"context"
	"errors"
	"io"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeLegOpen is the wire identity for a leg open failure. It names the
// failure at the boundary (nonexistent interface, permission, unsupported
// platform) without leaking the raw syscall text.
var ErrCodeLegOpen = errs.NewCode("netpen/leg-open")

// ErrUnsupportedPlatform is returned by [Open] on a platform with no AF_PACKET
// implementation. It carries [ErrCodeLegOpen] so callers match it through
// errors.Is against any coded leg-open failure.
var ErrUnsupportedPlatform = errs.New().
	Code(ErrCodeLegOpen).
	UserMsg("raw packet capture is not supported on this platform").
	Hint("run on Linux, where netpen uses AF_PACKET").
	Msg("afpacket leg is linux-only")

// Frame is one captured packet: the bytes the kernel delivered and any error
// the poll returned. A terminal error (context cancellation, socket close)
// terminates the RX loop.
type Frame struct {
	Data []byte
	Err  error
}

// Leg is one network interface opened for raw send and receive. Both the
// attack and watch legs implement it; the concrete linux type wraps
// afpacket.TPacket and the non-linux stub returns ErrUnsupportedPlatform.
//
// A Leg is safe for concurrent use by one writer and one reader: the RX loop
// runs in a single goroutine and Close is called from the signal path. The fd
// lifecycle is mutex-guarded inside the implementation so close-during-poll is
// an orderly EBADF exit, not a use-after-close race.
type Leg interface {
	// Send writes pkt to the wire. It returns an error matching
	// [ErrCodeLegOpen] when the leg is closed or the write fails.
	Send(ctx context.Context, pkt []byte) error

	// SetFilter installs a BPF program built from insts. An empty program
	// removes any installed filter. It must be called before Receive.
	SetFilter(insts []RawInstruction) error

	// Receive returns a channel that delivers captured frames until the
	// context is canceled or the leg is closed, at which point the channel
	// is closed. The implementation polls with a short timeout and checks
	// ctx.Done() each cycle so cancellation unblocks within one poll.
	Receive(ctx context.Context) <-chan Frame

	// Close releases the underlying socket and ring buffer. It is
	// idempotent: calling Close more than once is a no-op (Collection
	// Primitives lifecycle). It unblocks an active Receive poll.
	Close() error
}

// RawInstruction is one BPF instruction, the raw form afpacket.SetBPF accepts.
// It is the [golang.org/x/net/bpf.RawInstruction] shape re-exported so the
// platform-neutral file has no linux-only import; the bpf builder produces
// these from [Instruction] values.
type RawInstruction struct {
	Op uint16
	Jt uint8
	Jf uint8
	K  uint32
}

// Open opens iface as a promiscuous raw leg. On Linux it creates an
// afpacket.TPacket v3 ring; elsewhere it returns ErrUnsupportedPlatform. A
// nonexistent or unprivileged interface surfaces as an error carrying
// [ErrCodeLegOpen], never a panic.
func Open(iface string) (Leg, error) {
	return open(iface)
}

// IsUnsupported reports whether err is the unsupported-platform failure. It
// matches through the code, so a wrapped or wire-reconstructed variant still
// reports true.
func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedPlatform)
}

// AsIO is a convenience for tests: it asserts leg implements io.Closer and
// returns it as such. It exists only to keep the interface import honest.
func AsIO(leg Leg) io.Closer { //nolint:unused // kept for the io.Closer contract proof
	return leg
}
