//go:build linux

package rawsocket

import (
	"net"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// inet6PktinfoLen bounds the ancillary-data buffer: an in6_pktinfo (16-byte
// address + 4-byte ifindex) is larger than an in_pktinfo, so sizing for it
// fits either.
const inet6PktinfoLen = 20

func enablePktinfo(fd, family int) error {
	if family == unix.AF_INET {
		if err := unix.SetsockoptInt(fd, unix.SOL_IP, unix.IP_PKTINFO, 1); err != nil {
			return errs.From(err).
				Code(ErrCodeSourceOpen).
				Msg("enable IP_PKTINFO")
		}
		return nil
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_IPV6, unix.IPV6_RECVPKTINFO, 1); err != nil {
		return errs.From(err).
			Code(ErrCodeSourceOpen).
			Msg("enable IPV6_RECVPKTINFO")
	}
	return nil
}

func openRawGRE(family int, bindInterface string) (mirrorSocket, error) {
	fd, err := unix.Socket(family, unix.SOCK_RAW, unix.IPPROTO_GRE)
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("family", family).
			UserMsg("could not open a raw socket for the mirror receiver").
			Hint("check that you have CAP_NET_RAW").
			Msg("raw GRE socket")
	}
	if err := enablePktinfo(fd, family); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if bindInterface != "" {
		if err := unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, bindInterface); err != nil {
			_ = unix.Close(fd)
			return nil, errs.From(err).
				Code(ErrCodeSourceOpen).
				Attr("bind_interface", bindInterface).
				Msgf("bind raw GRE socket to %q", bindInterface)
		}
	}
	if err := setRecvTimeout(fd); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &fdMirrorSocket{fd: fd}, nil
}

// openMirrorUDP opens one dual-stack UDP socket: AF_INET6 with
// IPV6_V6ONLY disabled, so a single socket and port serves both IPv4 and
// IPv6 senders (an IPv4 sender's address arrives v4-mapped).
func openMirrorUDP(port uint32, bindInterface string) (mirrorSocket, error) {
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			UserMsg("could not open the mirror receiver's UDP socket").
			Msg("udp socket")
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 0); err != nil {
		_ = unix.Close(fd)
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Msg("disable IPV6_V6ONLY for a dual-stack receiver")
	}
	if err := enablePktinfo(fd, unix.AF_INET6); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if bindInterface != "" {
		if err := unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, bindInterface); err != nil {
			_ = unix.Close(fd)
			return nil, errs.From(err).
				Code(ErrCodeSourceOpen).
				Attr("bind_interface", bindInterface).
				Msgf("bind UDP receiver to %q", bindInterface)
		}
	}
	sa := &unix.SockaddrInet6{Port: int(port)}
	if err := unix.Bind(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("udp_port", port).
			Msgf("bind udp port %d", port)
	}
	if err := setRecvTimeout(fd); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &fdMirrorSocket{fd: fd}, nil
}

func openMirrorReceiver(encapsulations []capturev1.MirrorEncapsulation, udpPort uint32, bindInterface string, prog []bpf.RawInstruction) (Source, error) {
	var vm *bpf.VM
	if len(prog) > 0 {
		// prog is the raw form filter.Assemble produces for SO_ATTACH_FILTER
		// (a []bpf.RawInstruction), not the high-level []bpf.Instruction
		// bpf.NewVM needs: RawInstruction satisfies the Instruction
		// interface syntactically but is never RetA or RetConstant, so
		// NewVM's own check that the program ends in one would reject every
		// program, and the VM's instruction dispatch has no RawInstruction
		// case at all. Disassemble back to the high-level form first.
		insts, allDecoded := bpf.Disassemble(prog)
		if !allDecoded {
			return nil, errs.New().
				Code(ErrCodeSourceOpen).
				Msg("disassemble the mirror receiver's filter program")
		}
		v, err := bpf.NewVM(insts)
		if err != nil {
			return nil, errs.From(err).
				Code(ErrCodeSourceOpen).
				Msg("build the mirror receiver's filter VM")
		}
		vm = v
	}

	greFamily, udpFamily := splitEncapsulations(encapsulations)
	if !greFamily && len(udpFamily) == 0 {
		return nil, errs.New().
			Code(ErrCodeSourceOpen).
			UserMsg("the capture session names no supported mirror encapsulation").
			Msg("mirror receiver: no GRE-family or UDP-family encapsulation configured")
	}
	src := &mirrorSource{candidates: udpFamily, vm: vm, done: make(chan struct{})}

	if greFamily {
		v4, err := openRawGRE(unix.AF_INET, bindInterface)
		if err != nil {
			return nil, err
		}
		src.rawV4 = linuxMirrorSocket(v4)

		v6, err := openRawGRE(unix.AF_INET6, bindInterface)
		if err != nil {
			_ = v4.close()
			return nil, err
		}
		src.rawV6 = linuxMirrorSocket(v6)
	}

	if len(udpFamily) > 0 {
		udp, err := openMirrorUDP(udpPort, bindInterface)
		if err != nil {
			if src.rawV4 != nil {
				_ = src.rawV4.socket.close()
			}
			if src.rawV6 != nil {
				_ = src.rawV6.socket.close()
			}
			return nil, err
		}
		src.udp = linuxMirrorSocket(udp)
	}

	return src, nil
}

// pktinfoDestination walks oob for an IP_PKTINFO or IPV6_PKTINFO ancillary
// message and returns the packet's destination address.
func pktinfoDestination(oob []byte) net.IP {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	for _, m := range msgs {
		switch {
		case m.Header.Level == unix.SOL_IP && m.Header.Type == unix.IP_PKTINFO:
			if dst, ok := parseInet4Pktinfo(m.Data); ok {
				return dst
			}
		case m.Header.Level == unix.SOL_IPV6 && m.Header.Type == unix.IPV6_PKTINFO:
			if dst, ok := parseInet6Pktinfo(m.Data); ok {
				return dst
			}
		}
	}
	return nil
}

// parseInet4Pktinfo reads a unix.Inet4Pktinfo (ifindex, spec_dst, addr;
// native byte order, as the kernel writes it) from raw ancillary data
// without an unsafe.Pointer cast, since a []byte from
// ParseSocketControlMessage carries no alignment guarantee beyond 1 byte.
func parseInet4Pktinfo(data []byte) (dst net.IP, ok bool) {
	if len(data) < 12 {
		return nil, false
	}
	ip := make(net.IP, 4)
	copy(ip, data[8:12]) // ipi_addr: the packet's destination address.
	return ip, true
}

// parseInet6Pktinfo reads a unix.Inet6Pktinfo (addr, ifindex; native byte
// order) the same way.
func parseInet6Pktinfo(data []byte) (dst net.IP, ok bool) {
	if len(data) < 16 {
		return nil, false
	}
	ip := make(net.IP, 16)
	copy(ip, data[0:16])
	return ip, true
}

func linuxMirrorSocket(sock mirrorSocket) *mirrorReceiverSocket {
	return &mirrorReceiverSocket{
		socket: sock,
		parseControl: func(oob []byte) (net.IP, uint32, bool) {
			return pktinfoDestination(oob), 0, false
		},
		oobSize: unix.CmsgSpace(inet6PktinfoLen),
	}
}
