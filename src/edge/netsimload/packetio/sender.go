package packetio

import (
	"context"
	"errors"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeSenderOpen identifies an interface open or send failure.
var ErrCodeSenderOpen = errs.NewCode("netsimload/sender-open")

// ErrCodeUnsupportedPlatform identifies a platform without raw packet send.
var ErrCodeUnsupportedPlatform = errs.NewCode("netsimload/unsupported-platform")

// ErrUnsupportedPlatform is returned by OpenSender on non-Linux platforms.
var ErrUnsupportedPlatform = errs.New().
	Code(ErrCodeUnsupportedPlatform).
	UserMsg("raw packet sending is not supported on this platform").
	Hint("run on Linux, where netsimload uses AF_PACKET").
	Msg("packet sender is linux-only")

// ErrClosed is returned when a sender has already been closed.
var ErrClosed = errors.New("packet sender is closed")

// IsUnsupported reports whether err identifies the unsupported-platform case.
func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedPlatform)
}

// Sender writes complete Ethernet frames to one interface. A Sender is safe
// for one writer and concurrent Close calls. Cancellation is checked before
// each write; it does not interrupt a write already in progress.
type Sender interface {
	Send(ctx context.Context, frame []byte) error
	Close() error
}

// OpenSender opens a send-only raw packet socket bound to interfaceName. It
// does not enable promiscuous mode or create a receive ring.
func OpenSender(interfaceName string) (Sender, error) {
	return openSender(interfaceName)
}
