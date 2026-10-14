//go:build freebsd && (amd64 || arm64)

package bpfdev

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// The constraint above is what makes buffer.go's little-endian reads true:
// amd64 and arm64 are the little-endian 64-bit FreeBSD ports, and powerpc64
// is big-endian.

var bpfHdrProbe unix.BpfHdr

// A constant converted to uint fails to compile when it is negative, so each
// pair below is an equality check in both directions between the constants
// hdrLayout64 is built from and the kernel's bpf_hdr as x/sys declares it.
const (
	_ = uint(hdr64SecOff - (unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Sec)))
	_ = uint((unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Sec)) - hdr64SecOff)
	_ = uint(hdr64UsecOff - (unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Usec)))
	_ = uint((unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Usec)) - hdr64UsecOff)
	_ = uint(hdr64CaplenOff - unsafe.Offsetof(bpfHdrProbe.Caplen))
	_ = uint(unsafe.Offsetof(bpfHdrProbe.Caplen) - hdr64CaplenOff)
	_ = uint(hdr64DatalenOff - unsafe.Offsetof(bpfHdrProbe.Datalen))
	_ = uint(unsafe.Offsetof(bpfHdrProbe.Datalen) - hdr64DatalenOff)
	_ = uint(hdr64HdrlenOff - unsafe.Offsetof(bpfHdrProbe.Hdrlen))
	_ = uint(unsafe.Offsetof(bpfHdrProbe.Hdrlen) - hdr64HdrlenOff)
	_ = uint(hdr64WordSize - unsafe.Sizeof(uintptr(0)))
	_ = uint(unsafe.Sizeof(uintptr(0)) - hdr64WordSize)
)
