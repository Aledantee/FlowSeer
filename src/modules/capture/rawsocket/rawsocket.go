package rawsocket

import (
	"context"
	"errors"
	"time"

	"golang.org/x/net/bpf"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeSourceOpen is the wire identity for a source open or receive
// failure. It names the failure at the boundary (nonexistent interface,
// permission, unsupported platform) without leaking the raw syscall text.
var ErrCodeSourceOpen = errs.NewCode("capture/source-open")

// ErrUnsupportedPlatform is returned by OpenLocalInterface and
// OpenMirrorReceiver on a platform with no AF_PACKET or raw-socket
// implementation. It carries ErrCodeSourceOpen so callers match it through
// errors.Is against any coded source-open failure.
var ErrUnsupportedPlatform = errs.New().
	Code(ErrCodeSourceOpen).
	UserMsg("raw packet capture is not supported on this platform").
	Hint("run on Linux, where the capture engine uses AF_PACKET and raw IP sockets").
	Msg("capture source is linux-only")

// IsUnsupported reports whether err is the unsupported-platform failure. It
// matches through the code, so a wrapped or wire-reconstructed variant still
// reports true.
func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedPlatform)
}

// Frame is one received frame or envelope, or a terminal receive error. The
// consumer may retain Data after the next receive; concurrent mutation of
// the same Frame or its Data requires synchronization.
type Frame struct {
	// Data is the captured bytes: the frame itself for a local-interface
	// source, or the decapsulated inner frame for a mirror receiver. Unset
	// on a terminal error, and also unset (nil, no error) when a mirror
	// receiver's decoded envelope carries no inner frame — a marker, or a
	// non-Ethernet mirrored payload.
	Data []byte
	// OriginalLength is the frame's length on the wire before any
	// snap-length truncation the caller applies.
	OriginalLength uint32
	// Envelope is the mirror wrapper metadata a mirror receiver decoded; nil
	// for a local-interface frame.
	Envelope *capturev1.MirrorEnvelope
	// CapturedAt is when this source received the frame.
	CapturedAt time.Time
	// Err is set on a terminal receive error; every other field is unset.
	Err error
}

// LocalSource is one local-interface capture source, opened by
// OpenLocalInterface. Both the Linux implementation and the non-Linux stub
// satisfy it, so the capture engine's own Source interface (defined where it
// is consumed) is structurally compatible with either. A LocalSource is safe
// for one Receive loop and one Close call; concurrent Receive calls are not
// supported.
type LocalSource interface {
	// Receive returns a channel that delivers frames until ctx is canceled
	// or the source is closed, at which point the channel is closed. A
	// terminal error frame is best effort and may be omitted if the channel
	// is full.
	Receive(ctx context.Context) <-chan Frame
	// Stats reports this source's cumulative packet and interface-drop
	// counts.
	Stats() (received, droppedByInterface uint64, err error)
	// Close releases the source. It is idempotent.
	Close() error
}

// OpenLocalInterface opens iface as a capture source: promiscuous when
// asked, with prog attached as a kernel packet filter. An empty prog accepts
// every packet, matching filter.Compile's own empty-filter contract. On
// Linux this is an AF_PACKET socket; elsewhere it returns
// ErrUnsupportedPlatform.
func OpenLocalInterface(iface string, promiscuous bool, prog []bpf.RawInstruction) (LocalSource, error) {
	return openLocalInterface(iface, promiscuous, prog)
}
