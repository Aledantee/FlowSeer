//go:build freebsd && (amd64 || arm64)

package rawsocket

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// A constant converted to uint fails to compile when it is negative, so each
// pair below is an equality check in both directions between the offsets
// cmsg_freebsd_parse.go reads at and the structs x/sys declares. The
// constraint is the one src/common/net/bpfdev/layout_freebsd.go uses: amd64
// and arm64 are the little-endian FreeBSD ports.

var (
	sdlProbe     unix.RawSockaddrDatalink
	pktinfoProbe unix.Inet6Pktinfo
)

const (
	_ = uint(sdlIndexOff - unsafe.Offsetof(sdlProbe.Index))
	_ = uint(unsafe.Offsetof(sdlProbe.Index) - sdlIndexOff)
	_ = uint(sdlIndexLen - unsafe.Sizeof(sdlProbe.Index))
	_ = uint(unsafe.Sizeof(sdlProbe.Index) - sdlIndexLen)
	_ = uint(pktinfoAddrOff - unsafe.Offsetof(pktinfoProbe.Addr))
	_ = uint(unsafe.Offsetof(pktinfoProbe.Addr) - pktinfoAddrOff)
	_ = uint(16 - unsafe.Sizeof(pktinfoProbe.Addr))
	_ = uint(unsafe.Sizeof(pktinfoProbe.Addr) - 16)
	_ = uint(pktinfoIfindexOff - unsafe.Offsetof(pktinfoProbe.Ifindex))
	_ = uint(unsafe.Offsetof(pktinfoProbe.Ifindex) - pktinfoIfindexOff)
	_ = uint(pktinfoLen - unsafe.Sizeof(pktinfoProbe))
	_ = uint(unsafe.Sizeof(pktinfoProbe) - pktinfoLen)
)
