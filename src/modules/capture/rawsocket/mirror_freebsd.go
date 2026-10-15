//go:build freebsd && (amd64 || arm64)

package rawsocket

import (
	"net"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

func openMirrorReceiver(encapsulations []capturev1.MirrorEncapsulation, udpPort uint32, bindInterface string, prog []bpf.RawInstruction) (Source, error) {
	var wantedIndex uint32
	if bindInterface != "" {
		iface, err := net.InterfaceByName(bindInterface)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeSourceOpen).
				Attr("bind_interface", bindInterface).Msg("resolve mirror receiver interface")
		}
		wantedIndex = uint32(iface.Index)
	}
	var vm *bpf.VM
	if len(prog) > 0 {
		insts, allDecoded := bpf.Disassemble(prog)
		if !allDecoded {
			return nil, errs.New().Code(ErrCodeSourceOpen).
				Msg("disassemble the mirror receiver's filter program")
		}
		var err error
		vm, err = bpf.NewVM(insts)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeSourceOpen).
				Msg("build the mirror receiver's filter VM")
		}
	}
	greFamily, udpFamily := splitEncapsulations(encapsulations)
	if !greFamily && len(udpFamily) == 0 {
		return nil, errs.New().Code(ErrCodeSourceOpen).
			UserMsg("the capture session names no supported mirror encapsulation").
			Msg("mirror receiver: no GRE-family or UDP-family encapsulation configured")
	}
	src := &mirrorSource{candidates: udpFamily, vm: vm, done: make(chan struct{})}
	if greFamily {
		v4, err := openFreeBSDMirrorSocket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_GRE, 0, wantedIndex)
		if err != nil {
			return nil, err
		}
		src.rawV4 = v4
		v6, err := openFreeBSDMirrorSocket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_GRE, 0, wantedIndex)
		if err != nil {
			_ = src.Close()
			return nil, err
		}
		src.rawV6 = v6
	}
	if len(udpFamily) > 0 {
		udp, err := openFreeBSDMirrorSocket(unix.AF_INET6, unix.SOCK_DGRAM, unix.IPPROTO_UDP, udpPort, wantedIndex)
		if err != nil {
			_ = src.Close()
			return nil, err
		}
		src.udp = udp
	}
	return src, nil
}

func openFreeBSDMirrorSocket(family, kind, protocol int, port, wantedIndex uint32) (*mirrorReceiverSocket, error) {
	fd, err := unix.Socket(family, kind, protocol)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeSourceOpen).
			Attr("family", family).Attr("protocol", protocol).Msg("open mirror receiver socket")
	}
	sock := &mirrorReceiverSocket{socket: &fdMirrorSocket{fd: fd}, wantedIndex: wantedIndex}
	ready := false
	defer func() {
		if !ready {
			_ = unix.Close(fd)
		}
	}()
	if family == unix.AF_INET {
		for _, option := range []int{unix.IP_RECVDSTADDR, unix.IP_RECVIF} {
			if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, option, 1); err != nil {
				return nil, errs.From(err).Code(ErrCodeSourceOpen).
					Attr("option", option).Msg("enable IPv4 mirror control message")
			}
		}
		sock.parseControl = freeBSDControlV4
		sock.oobSize = unix.CmsgSpace(4) + unix.CmsgSpace(unix.SizeofSockaddrDatalink)
	} else {
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, 1); err != nil {
			return nil, errs.From(err).Code(ErrCodeSourceOpen).Msg("enable IPV6_RECVPKTINFO")
		}
		sock.parseControl = freeBSDControlV6
		sock.oobSize = unix.CmsgSpace(unix.SizeofInet6Pktinfo)
	}
	if kind == unix.SOCK_DGRAM {
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 0); err != nil {
			return nil, errs.From(err).Code(ErrCodeSourceOpen).Msg("disable IPV6_V6ONLY for a dual-stack receiver")
		}
		if err := unix.Bind(fd, &unix.SockaddrInet6{Port: int(port)}); err != nil {
			return nil, errs.From(err).Code(ErrCodeSourceOpen).
				Attr("udp_port", port).Msg("bind mirror UDP socket")
		}
	}
	if err := setRecvTimeout(fd); err != nil {
		return nil, err
	}
	ready = true
	return sock, nil
}

func freeBSDControlV4(oob []byte) (destination net.IP, index uint32, ok bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, 0, false
	}
	for _, msg := range msgs {
		if msg.Header.Level != unix.IPPROTO_IP {
			continue
		}
		switch msg.Header.Type {
		case unix.IP_RECVDSTADDR:
			if addr, err := parseRecvDstAddr(msg.Data); err == nil {
				destination = net.IP(addr.AsSlice())
			}
		case unix.IP_RECVIF:
			if idx, err := parseRecvIf(msg.Data); err == nil {
				index, ok = uint32(idx), true
			}
		}
	}
	return destination, index, ok
}

func freeBSDControlV6(oob []byte) (net.IP, uint32, bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, 0, false
	}
	for _, msg := range msgs {
		if msg.Header.Level == unix.IPPROTO_IPV6 && msg.Header.Type == unix.IPV6_PKTINFO {
			if addr, index, err := parseIn6Pktinfo(msg.Data); err == nil {
				return net.IP(addr.AsSlice()), index, true
			}
		}
	}
	return nil, 0, false
}
