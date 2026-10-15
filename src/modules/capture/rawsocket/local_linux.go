//go:build linux

package rawsocket

import (
	"encoding/binary"
	"net"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// packetSocket is the syscall surface local_linux.go needs, seamed out so
// the Receive-loop logic is tested without a real socket or CAP_NET_RAW.
type packetSocket interface {
	recvmsg(p, oob []byte) (n, oobn, flags int, err error)
	stats() (packets, drops uint64, err error)
	close() error
}

// ethPAllNetworkOrder is ETH_P_ALL in network byte order, as an AF_PACKET
// socket's socket(2) and bind(2) calls both require (see packet(7)).
func ethPAllNetworkOrder() uint16 {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(unix.ETH_P_ALL))
	return binary.NativeEndian.Uint16(b[:])
}

// fdSocket is packetSocket over a real AF_PACKET file descriptor.
type fdSocket struct {
	fd int
}

func (s *fdSocket) recvmsg(p, oob []byte) (int, int, int, error) {
	n, oobn, flags, _, err := unix.Recvmsg(s.fd, p, oob, unix.MSG_TRUNC)
	return n, oobn, flags, err
}

func (s *fdSocket) stats() (uint64, uint64, error) {
	st, err := unix.GetsockoptTpacketStats(s.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
	if err != nil {
		return 0, 0, err
	}
	// tp_packets includes tp_drops (net/packet/af_packet.c's
	// packet_getsockopt folds drops into the packet count before copying it
	// to userspace); subtract so received counts only what the engine
	// actually got, matching capture_counters.proto's "Packets the
	// interface delivered to the capture engine".
	return uint64(st.Packets) - uint64(st.Drops), uint64(st.Drops), nil
}

func (s *fdSocket) close() error {
	return unix.Close(s.fd)
}

func openLocalInterface(iface string, promiscuous bool, prog []bpf.RawInstruction) (Source, error) {
	var vm *bpf.VM
	if len(prog) > 0 {
		insts, allDecoded := bpf.Disassemble(prog)
		if !allDecoded {
			return nil, errs.New().
				Code(ErrCodeSourceOpen).
				Msg("disassemble the local interface's filter program")
		}
		v, err := bpf.NewVM(insts)
		if err != nil {
			return nil, errs.From(err).
				Code(ErrCodeSourceOpen).
				Msg("build the local interface's filter VM")
		}
		vm = v
	}

	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("iface", iface).
			UserMsg("the named interface does not exist").
			Msgf("resolve interface %q", iface)
	}

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(ethPAllNetworkOrder()))
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("iface", iface).
			UserMsg("could not open the network interface for raw capture").
			Hint("check the interface name and that you have CAP_NET_RAW").
			Msgf("af_packet socket %q", iface)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_PACKET, unix.PACKET_AUXDATA, 1); err != nil {
		_ = unix.Close(fd)
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Msgf("enable packet auxiliary data on %q", iface)
	}

	sa := &unix.SockaddrLinklayer{
		Protocol: ethPAllNetworkOrder(),
		Ifindex:  ifi.Index,
	}
	if err := unix.Bind(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("iface", iface).
			Msgf("bind af_packet socket to %q", iface)
	}

	if promiscuous {
		mreq := &unix.PacketMreq{Ifindex: int32(ifi.Index), Type: unix.PACKET_MR_PROMISC}
		if err := unix.SetsockoptPacketMreq(fd, unix.SOL_PACKET, unix.PACKET_ADD_MEMBERSHIP, mreq); err != nil {
			_ = unix.Close(fd)
			return nil, errs.From(err).
				Code(ErrCodeSourceOpen).
				Attr("iface", iface).
				Msgf("set promiscuous mode on %q", iface)
		}
	}

	tv := unix.NsecToTimeval(pollTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		_ = unix.Close(fd)
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("iface", iface).
			Msgf("set receive timeout on %q", iface)
	}

	src := newLinuxLocalSource(&fdSocket{fd: fd})
	src.vm = vm
	return src, nil
}

func newLinuxLocalSource(sock packetSocket) *localSource {
	return newLocalSource(&linuxFrameReader{sock: sock, oob: make([]byte, unix.CmsgSpace(20))})
}

type linuxFrameReader struct {
	sock packetSocket
	oob  []byte
}

func (r *linuxFrameReader) readFrame(buf []byte) ([]byte, int, time.Time, bool, error) {
	n, oobn, flags, err := r.sock.recvmsg(buf, r.oob)
	if err != nil {
		if isRetryable(err) {
			return nil, 0, time.Time{}, true, err
		}
		return nil, 0, time.Time{}, false, errs.From(err).
			Code(ErrCodeSourceOpen).
			Msg("recvmsg")
	}
	if flags&unix.MSG_CTRUNC != 0 {
		return nil, 0, time.Time{}, false, errs.New().
			Code(ErrCodeSourceOpen).
			Msg("packet auxiliary data truncated")
	}
	captured := n
	if captured > len(buf) {
		// MSG_TRUNC reports a wire length beyond the captured bytes.
		captured = len(buf)
	}
	data, wireLength, err := restoreVLAN(buf[:captured], n, r.oob[:oobn])
	if err != nil {
		return nil, 0, time.Time{}, false, errs.From(err).
			Code(ErrCodeSourceOpen).
			Msg("restore VLAN header")
	}
	return data, wireLength, time.Now(), false, nil
}

func (r *linuxFrameReader) stats() (uint64, uint64, error) {
	return r.sock.stats()
}

func (r *linuxFrameReader) close() error {
	return r.sock.close()
}

func restoreVLAN(frame []byte, originalLength int, oob []byte) ([]byte, int, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, 0, err
	}
	for _, message := range messages {
		if message.Header.Level != unix.SOL_PACKET || message.Header.Type != unix.PACKET_AUXDATA {
			continue
		}
		if len(message.Data) < 20 {
			return nil, 0, errs.Msg("short packet auxiliary data")
		}
		status := binary.NativeEndian.Uint32(message.Data[:4])
		if status&unix.TP_STATUS_VLAN_VALID == 0 {
			break
		}
		if len(frame) < 14 {
			return nil, 0, errs.Msg("VLAN metadata on short Ethernet frame")
		}

		tci := binary.NativeEndian.Uint16(message.Data[16:18])
		tpid := uint16(0x8100)
		if status&unix.TP_STATUS_VLAN_TPID_VALID != 0 {
			tpid = binary.NativeEndian.Uint16(message.Data[18:20])
		}
		data := make([]byte, len(frame)+4)
		copy(data, frame[:12])
		binary.BigEndian.PutUint16(data[12:14], tpid)
		binary.BigEndian.PutUint16(data[14:16], tci)
		copy(data[16:], frame[12:])
		return data, originalLength + 4, nil
	}

	data := make([]byte, len(frame))
	copy(data, frame)
	return data, originalLength, nil
}
