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
// failure other than an unsupported platform (nonexistent interface,
// permission, a receive that failed after the source opened) without
// leaking the raw syscall text.
var ErrCodeSourceOpen = errs.NewCode("capture/source-open")

// ErrCodeUnsupportedPlatform is ErrUnsupportedPlatform's own code, distinct
// from ErrCodeSourceOpen: errs.Error.Is matches on code alone, so sharing
// one code between "this platform cannot do raw capture at all" and every
// other open/receive failure would make IsUnsupported report true for a
// permission error or a bad interface name on Linux itself.
var ErrCodeUnsupportedPlatform = errs.NewCode("capture/unsupported-platform")

// ErrUnsupportedPlatform is returned by OpenLocalInterface and
// OpenMirrorReceiver on a platform with no AF_PACKET or raw-socket
// implementation.
var ErrUnsupportedPlatform = errs.New().
	Code(ErrCodeUnsupportedPlatform).
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

// Source is one capture source, opened by OpenLocalInterface or
// OpenMirrorReceiver. Every implementation this package returns satisfies
// it, so the capture engine's own Source interface (defined where it is
// consumed) is structurally compatible with any of them. A Source is safe
// for one Receive loop and one Close call; concurrent Receive calls are not
// supported.
type Source interface {
	// Receive returns a channel that delivers frames until ctx is canceled
	// or the source is closed, at which point the channel is closed. A
	// terminal error frame is best effort and may be omitted if the channel
	// is full.
	Receive(ctx context.Context) <-chan Frame
	// Stats reports the packet and interface-drop counts since the last
	// call to Stats (or since the source opened, on the first call): a
	// caller that wants a running total accumulates what it returns. A
	// mirror receiver has no interface-level drop counter to report and
	// always returns zero for it.
	Stats() (received, droppedByInterface uint64, err error)
	// Close releases the source. It is idempotent.
	Close() error
}

// OpenLocalInterface opens iface as a capture source: promiscuous when
// asked, with prog attached as a kernel packet filter. An empty prog accepts
// every packet, matching filter.Compile's own empty-filter contract. On
// Linux this is an AF_PACKET socket; elsewhere it returns
// ErrUnsupportedPlatform.
func OpenLocalInterface(iface string, promiscuous bool, prog []bpf.RawInstruction) (Source, error) {
	return openLocalInterface(iface, promiscuous, prog)
}

// OpenMirrorReceiver opens a mirror receiver for encapsulations: a raw
// IPPROTO_GRE socket (IPv4 and IPv6) for the GRE-family arms
// (erspan_type_i/ii/iii, gre), and a UDP socket bound to udpPort for the
// UDP-family arms (vxlan, tzsp), each bound to bindInterface when it is not
// empty. prog is run against each decapsulated inner frame through
// golang.org/x/net/bpf's own VM rather than attached to the kernel: the
// filter's field offsets assume the frame it reads starts after
// decapsulation, which is envelope-dependent and different for every mirror
// encapsulation, so there is no single kernel-attachable program that reads
// all of them the way OpenLocalInterface's does. On Linux this opens real
// sockets; elsewhere it returns ErrUnsupportedPlatform.
func OpenMirrorReceiver(encapsulations []capturev1.MirrorEncapsulation, udpPort uint32, bindInterface string, prog []bpf.RawInstruction) (Source, error) {
	return openMirrorReceiver(encapsulations, udpPort, bindInterface, prog)
}
