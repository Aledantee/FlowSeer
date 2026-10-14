package bpfdev

import (
	"time"

	"golang.org/x/net/bpf"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeOpen is the wire identity for a device that cannot be opened or
// configured.
var ErrCodeOpen = errs.NewCode("bpfdev/open")

// ErrCodeIO is the wire identity for a failed operation on an open device.
var ErrCodeIO = errs.NewCode("bpfdev/io")

const (
	// DefaultBufferLen is the BufferLen used when the field is zero: the
	// net.bpf.maxbufsize default per bpf(4).
	DefaultBufferLen = 524288
	// DefaultReadTimeout is the ReadTimeout used when the field is zero.
	DefaultReadTimeout = 100 * time.Millisecond
)

// Direction selects which packets the device sees, as bpf(4) BIOCSDIRECTION
// does. The values follow enum bpf_direction in sys/net/bpf.h.
type Direction uint32

const (
	// DirectionIn sees only packets received on the interface. It is the zero
	// value, so a zero Config applies it.
	DirectionIn Direction = 0
	// DirectionInOut sees received and sent packets.
	DirectionInOut Direction = 1
	// DirectionOut sees only packets sent on the interface.
	DirectionOut Direction = 2
)

// Config holds the options Open applies. Every field is optional.
type Config struct {
	// BufferLen is the requested read buffer size in bytes. Zero means
	// [DefaultBufferLen]. The kernel clamps the request and the device reads
	// with the size the kernel reports back.
	BufferLen int
	// ReadTimeout bounds how long ReadBatch blocks, and so how long Close
	// waits for an in-flight read. Zero means [DefaultReadTimeout], never
	// "no timeout". Negative is refused.
	ReadTimeout time.Duration
	// Immediate makes a read return as soon as a packet arrives instead of
	// when the buffer fills or the timeout expires.
	Immediate bool
	// Promiscuous puts the interface in promiscuous mode.
	Promiscuous bool
	// HeaderComplete makes Write send the link-layer header as given instead
	// of filling in the source address.
	HeaderComplete bool
	// Direction selects which packets the device sees.
	Direction Direction
	// ReadFilter is the BPF program applied to incoming packets. Nil accepts
	// all.
	ReadFilter []bpf.RawInstruction
}
