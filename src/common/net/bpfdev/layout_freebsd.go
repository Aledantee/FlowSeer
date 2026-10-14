//go:build freebsd && (amd64 || arm64)

package bpfdev

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// The constraint above is what makes buffer.go's little-endian reads true:
// amd64 and arm64 are the little-endian 64-bit FreeBSD ports, and powerpc64
// is big-endian.

// The offsets hdrLayout64 uses, held here as constants so the compiler can
// check them against unix.BpfHdr. A mismatch is a build error.
const (
	wantSecOff     = 0
	wantUsecOff    = 8
	wantCaplenOff  = 16
	wantDatalenOff = 20
	wantHdrlenOff  = 24
	wantWordSize   = 8
)

var bpfHdrProbe unix.BpfHdr

// A constant converted to uint fails to compile when it is negative, so each
// pair below is an equality check in both directions.
const (
	_ = uint(wantSecOff - (unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Sec)))
	_ = uint((unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Sec)) - wantSecOff)
	_ = uint(wantUsecOff - (unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Usec)))
	_ = uint((unsafe.Offsetof(bpfHdrProbe.Tstamp) + unsafe.Offsetof(bpfHdrProbe.Tstamp.Usec)) - wantUsecOff)
	_ = uint(wantCaplenOff - unsafe.Offsetof(bpfHdrProbe.Caplen))
	_ = uint(unsafe.Offsetof(bpfHdrProbe.Caplen) - wantCaplenOff)
	_ = uint(wantDatalenOff - unsafe.Offsetof(bpfHdrProbe.Datalen))
	_ = uint(unsafe.Offsetof(bpfHdrProbe.Datalen) - wantDatalenOff)
	_ = uint(wantHdrlenOff - unsafe.Offsetof(bpfHdrProbe.Hdrlen))
	_ = uint(unsafe.Offsetof(bpfHdrProbe.Hdrlen) - wantHdrlenOff)
	_ = uint(wantWordSize - unsafe.Sizeof(uintptr(0)))
	_ = uint(unsafe.Sizeof(uintptr(0)) - wantWordSize)
)

// mustMatchLayout panics at start-up when hdrLayout64 differs from the
// constants checked above, which closes the gap between the compile-time
// check and the value the parser uses.
func mustMatchLayout(got hdrLayout) struct{} {
	want := hdrLayout{
		secOff:     wantSecOff,
		usecOff:    wantUsecOff,
		caplenOff:  wantCaplenOff,
		datalenOff: wantDatalenOff,
		hdrlenOff:  wantHdrlenOff,
		wordSize:   wantWordSize,
	}
	if got != want {
		panic("bpfdev: hdrLayout64 does not match the kernel bpf_hdr offsets")
	}
	return struct{}{}
}

var _ = mustMatchLayout(hdrLayout64)
