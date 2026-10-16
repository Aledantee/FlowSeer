package rawsocket

import (
	"encoding/binary"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// The file name ends in _freebsd_parse.go, not _freebsd.go, so Go applies no
// implicit GOOS constraint and the parsers build and test on every host.
// cmsg_layout_freebsd.go pins the offsets below to the kernel structs.

const (
	// inAddrLen is sizeof(struct in_addr), the IP_RECVDSTADDR payload.
	inAddrLen = 4
	// sdlIndexOff is the offset of sdl_index in struct sockaddr_dl, which
	// follows sdl_len and sdl_family; the IP_RECVIF payload is a sockaddr_dl.
	sdlIndexOff = 2
	// sdlIndexLen is the width of sdl_index, a u_short.
	sdlIndexLen = 2
	// pktinfoAddrOff and pktinfoIfindexOff locate ipi6_addr and ipi6_ifindex
	// in struct in6_pktinfo, the IPV6_PKTINFO payload.
	pktinfoAddrOff    = 0
	pktinfoIfindexOff = 16
	// pktinfoLen is sizeof(struct in6_pktinfo).
	pktinfoLen = 20
)

// The kernel writes ancillary data in host byte order, so these parsers read
// with binary.NativeEndian rather than a fixed order. That keeps the file
// correct on any host it is compiled for, while the layout check admits only
// the little-endian FreeBSD ports.

// parseRecvDstAddr decodes an IP_RECVDSTADDR payload, a 4-byte in_addr in
// network order, and refuses a shorter buffer.
func parseRecvDstAddr(data []byte) (netip.Addr, error) {
	if len(data) < inAddrLen {
		return netip.Addr{}, shortCmsg("IP_RECVDSTADDR", len(data), inAddrLen)
	}
	return netip.AddrFrom4([4]byte(data[:inAddrLen])), nil
}

// parseRecvIf decodes an IP_RECVIF payload, a sockaddr_dl, to its sdl_index.
// It reads only the index, so it accepts the truncated sockaddr_dl the kernel
// may supply and refuses a buffer that ends before the index.
func parseRecvIf(data []byte) (uint16, error) {
	if len(data) < sdlIndexOff+sdlIndexLen {
		return 0, shortCmsg("IP_RECVIF", len(data), sdlIndexOff+sdlIndexLen)
	}
	return binary.NativeEndian.Uint16(data[sdlIndexOff:]), nil
}

// parseIn6Pktinfo decodes an IPV6_PKTINFO payload to the destination address
// and the receiving interface index, and refuses a shorter buffer.
func parseIn6Pktinfo(data []byte) (netip.Addr, uint32, error) {
	if len(data) < pktinfoLen {
		return netip.Addr{}, 0, shortCmsg("IPV6_PKTINFO", len(data), pktinfoLen)
	}
	addr := netip.AddrFrom16([16]byte(data[pktinfoAddrOff : pktinfoAddrOff+16]))
	return addr, binary.NativeEndian.Uint32(data[pktinfoIfindexOff:]), nil
}

func shortCmsg(name string, got, want int) error {
	return errs.Msgf("%s control message is %d bytes, want at least %d", name, got, want)
}
