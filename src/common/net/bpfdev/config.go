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
	// maxReadTimeout is the largest ReadTimeout Open accepts. Close waits up
	// to one ReadTimeout, and a value near math.MaxInt64 overflows the
	// rounding in unix.NsecToTimeval into a timeval the kernel refuses.
	maxReadTimeout = time.Hour
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
	// "no timeout". A value under one kernel clock tick is raised to one
	// tick, because the kernel rounds down and treats zero as no timeout.
	// Negative and over one hour are refused.
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

// resolve applies the defaults and refuses out-of-range values.
func (c Config) resolve() (bufLen int, timeout time.Duration, err error) {
	if c.BufferLen < 0 || c.BufferLen > int(^uint32(0)) {
		return 0, 0, errs.New().Code(ErrCodeOpen).Attr("buffer_len", c.BufferLen).Msg("buffer length out of range")
	}
	if c.ReadTimeout < 0 {
		return 0, 0, errs.New().Code(ErrCodeOpen).Attr("read_timeout", c.ReadTimeout.String()).Msg("read timeout is negative")
	}
	if c.ReadTimeout > maxReadTimeout {
		return 0, 0, errs.New().Code(ErrCodeOpen).Attr("read_timeout", c.ReadTimeout.String()).Msg("read timeout is over one hour")
	}
	if c.Direction > DirectionOut {
		return 0, 0, errs.New().Code(ErrCodeOpen).Attr("direction", uint32(c.Direction)).Msg("unknown direction")
	}
	bufLen, timeout = c.BufferLen, c.ReadTimeout
	if bufLen == 0 {
		bufLen = DefaultBufferLen
	}
	if timeout == 0 {
		timeout = DefaultReadTimeout
	}
	return bufLen, timeout, nil
}

// minReadTimeout returns the shortest ReadTimeout the kernel turns into a
// nonzero bd_rtout at the given kern.hz: one tick, rounded up to the
// nanosecond. BIOCSRTIMEOUT stores tvtohz(tv) - 1 (sys/net/bpf.c) and tvtohz
// is the truncated tick count plus one (sys/kern/kern_clock.c), so the stored
// value is zero, which means no timeout, unless usec*hz >= 1000000. Rounding
// down misses that for an hz that does not divide 1e6.
func minReadTimeout(hz uint32) time.Duration {
	return (time.Second + time.Duration(hz) - 1) / time.Duration(hz)
}
