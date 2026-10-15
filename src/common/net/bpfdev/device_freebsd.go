//go:build freebsd && (amd64 || arm64)

package bpfdev

import (
	"errors"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	devicePath = "/dev/bpf"
	// ifNameLen is IFNAMSIZ, the size of the name field in struct ifreq.
	ifNameLen = 16
)

// ifreq is the 32-byte argument BIOCSETIF takes (the size field of
// 0x8020426c). Only the name is set.
type ifreq struct {
	name [ifNameLen]byte
	_    [16]byte
}

// Device is an open BPF device attached to one interface.
//
// Every operation holds mu for reading and Close holds it for writing, so
// Close waits for an in-flight read for at most one ReadTimeout and no
// operation uses a closed descriptor. A Write does not wait behind a read.
type Device struct {
	mu     sync.RWMutex // guards fd and closed
	fd     int
	closed bool
	// readMu serialises readers, which share buf.
	readMu sync.Mutex
	// buf is the read buffer, sized to the length the kernel reported.
	buf []byte
}

// Open opens /dev/bpf, attaches it to iface, and applies cfg. The kernel
// calls run in the order bpf(4) needs: buffer length before attaching, link
// type and promiscuous mode after.
func Open(iface string, cfg Config) (*Device, error) {
	bufLen, timeout, err := cfg.resolve()
	if err != nil {
		return nil, openError(iface, err).Msg("invalid config")
	}
	if iface == "" || len(iface) >= ifNameLen {
		return nil, errs.New().Code(ErrCodeOpen).Attr("iface", iface).Msg("interface name is empty or longer than IFNAMSIZ-1")
	}

	fd, err := unix.Open(devicePath, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, openError(iface, err).Msg("open " + devicePath)
	}
	d, err := configure(fd, iface, cfg, bufLen, timeout)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return d, nil
}

func configure(fd int, iface string, cfg Config, bufLen int, timeout time.Duration) (*Device, error) {
	// BIOCSBLEN is _IOWR: the kernel clamps the request and writes the size
	// back, and a read of any other size fails with EINVAL (bpf(4)).
	kernelLen := uint32(bufLen)
	if err := ioctlPtr(fd, unix.BIOCSBLEN, unsafe.Pointer(&kernelLen)); err != nil {
		return nil, openError(iface, err).Msg("BIOCSBLEN")
	}

	var req ifreq
	copy(req.name[:], iface)
	if err := ioctlPtr(fd, unix.BIOCSETIF, unsafe.Pointer(&req)); err != nil {
		return nil, openError(iface, err).UserMsg("the named interface cannot be attached to a BPF device").Msg("BIOCSETIF")
	}

	if _, err := unix.IoctlGetInt(fd, unix.BIOCGDLT); err != nil {
		return nil, openError(iface, err).Msg("BIOCGDLT")
	}

	if err := unix.IoctlSetPointerInt(fd, unix.BIOCIMMEDIATE, boolInt(cfg.Immediate)); err != nil {
		return nil, openError(iface, err).Msg("BIOCIMMEDIATE")
	}

	// BIOCSRTIMEOUT stores tvtohz(tv) - 1 as bd_rtout (sys/net/bpf.c), and
	// tvtohz truncates to whole ticks plus one (sys/kern/kern_clock.c), so a
	// timeout under one tick stores zero, which bpfread hands to msleep as no
	// timeout and Close would then wait forever on the read. Raise it to one
	// tick.
	hz, err := unix.SysctlUint32("kern.hz")
	if err != nil {
		return nil, openError(iface, err).Msg("sysctl kern.hz")
	}
	if hz == 0 {
		return nil, errs.New().Code(ErrCodeOpen).Attr("iface", iface).Msg("kern.hz is zero")
	}
	timeout = max(timeout, minReadTimeout(hz))
	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	if err := ioctlPtr(fd, unix.BIOCSRTIMEOUT, unsafe.Pointer(&tv)); err != nil {
		return nil, openError(iface, err).Msg("BIOCSRTIMEOUT")
	}

	if err := unix.IoctlSetPointerInt(fd, unix.BIOCSHDRCMPLT, boolInt(cfg.HeaderComplete)); err != nil {
		return nil, openError(iface, err).Msg("BIOCSHDRCMPLT")
	}

	if err := unix.IoctlSetPointerInt(fd, unix.BIOCSDIRECTION, int(cfg.Direction)); err != nil {
		return nil, openError(iface, err).Msg("BIOCSDIRECTION")
	}

	if cfg.Promiscuous {
		// BIOCPROMISC is _IO: it takes no argument.
		if err := ioctlPtr(fd, unix.BIOCPROMISC, nil); err != nil {
			return nil, openError(iface, err).Msg("BIOCPROMISC")
		}
	}

	if len(cfg.ReadFilter) > 0 {
		if err := setFilter(fd, cfg.ReadFilter); err != nil {
			return nil, openError(iface, err).Msg("BIOCSETF")
		}
	}

	return &Device{fd: fd, buf: make([]byte, kernelLen)}, nil
}

// ReadBatch reads once and returns the records in the buffer. The records
// alias the device's buffer until the next call. A read that times out
// returns no records and no error. It blocks for at most the ReadTimeout.
func (d *Device) ReadBatch() ([]Record, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, closedError("read")
	}

	// The read length is the whole buffer: bpf(4) fails any other size with
	// EINVAL.
	n, err := unix.Read(d.fd, d.buf)
	if err != nil {
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			return nil, nil
		}
		return nil, errs.From(err).Code(ErrCodeIO).Msg("read")
	}
	return parseBuffer(d.buf[:n], hdrLayout64)
}

// Write sends frame with one write(2). With HeaderComplete it goes out as
// given, otherwise the kernel fills in the source address.
func (d *Device) Write(frame []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return closedError("write")
	}

	n, err := unix.Write(d.fd, frame)
	if err != nil {
		return errs.From(err).Code(ErrCodeIO).Attr("length", len(frame)).Msg("write")
	}
	if n != len(frame) {
		return errs.New().Code(ErrCodeIO).Attr("length", len(frame)).Attr("written", n).Msg("short write")
	}
	return nil
}

// Stats returns the kernel's counters: packets received and packets dropped
// for lack of buffer space.
func (d *Device) Stats() (unix.BpfStat, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var st unix.BpfStat
	if d.closed {
		return st, closedError("stats")
	}
	if err := ioctlPtr(d.fd, unix.BIOCGSTATS, unsafe.Pointer(&st)); err != nil {
		return unix.BpfStat{}, errs.From(err).Code(ErrCodeIO).Msg("BIOCGSTATS")
	}
	return st, nil
}

// SetReadFilter replaces the read filter. A nil filter accepts all. bpf(4)
// says BIOCSETF flushes the buffers and zeroes the counters.
func (d *Device) SetReadFilter(filter []bpf.RawInstruction) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return closedError("set read filter")
	}
	if err := setFilter(d.fd, filter); err != nil {
		return errs.From(err).Code(ErrCodeIO).Msg("BIOCSETF")
	}
	return nil
}

// LinkType returns the data link type, such as unix.DLT_EN10MB.
func (d *Device) LinkType() (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return 0, closedError("link type")
	}
	dlt, err := unix.IoctlGetInt(d.fd, unix.BIOCGDLT)
	if err != nil {
		return 0, errs.From(err).Code(ErrCodeIO).Msg("BIOCGDLT")
	}
	return dlt, nil
}

// Close waits for an in-flight read, up to one ReadTimeout, and releases the
// descriptor. Closing twice returns nil.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if err := unix.Close(d.fd); err != nil {
		return errs.From(err).Code(ErrCodeIO).Msg("close")
	}
	return nil
}

// setFilter attaches prog, or removes the filter when prog is empty.
func setFilter(fd int, prog []bpf.RawInstruction) error {
	insns := make([]unix.BpfInsn, len(prog))
	for i, in := range prog {
		insns[i] = unix.BpfInsn{Code: in.Op, Jt: in.Jt, Jf: in.Jf, K: in.K}
	}
	p := unix.BpfProgram{Len: uint32(len(insns))}
	if len(insns) > 0 {
		p.Insns = &insns[0]
	}
	return ioctlPtr(fd, unix.BIOCSETF, unsafe.Pointer(&p))
}

// ioctlPtr is ioctl(2) with a pointer argument. x/sys exports typed setters
// only for integers, and its own pointer helper is unexported.
func ioctlPtr(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func openError(iface string, cause error) errs.Builder {
	return errs.From(cause).Code(ErrCodeOpen).Attr("iface", iface)
}

func closedError(op string) error {
	return errs.New().Code(ErrCodeIO).Msg(op + " on closed device")
}
