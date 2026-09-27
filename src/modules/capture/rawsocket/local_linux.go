//go:build linux

package rawsocket

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// pollTimeout is the per-poll wait. It bounds how long a blocked Receive
// stays unaware of ctx cancellation: the loop checks ctx.Done() after each
// poll returns, so cancellation lands within this window. It also matches
// SO_RCVTIMEO, so a blocking Recvmsg returns EAGAIN on its own rather than
// blocking indefinitely.
const pollTimeout = 100 * time.Millisecond

// maxFrameLen bounds the receive buffer. It exceeds any Ethernet frame,
// jumbo frames included; MSG_TRUNC still reports a longer wire length if one
// somehow arrives, so truncation is never silent.
const maxFrameLen = 65536

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

// linuxLocalSource wraps a packetSocket behind the LocalSource contract.
//
// mu guards sock access and the closed flag; a poll and a Close cannot race
// each other into a use-after-close. done wakes a Receive blocked trying to
// send to an abandoned consumer.
type linuxLocalSource struct {
	sock packetSocket
	vm   *bpf.VM

	mu     sync.Mutex // guards sock and closed
	closed bool
	done   chan struct{}
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

func newLinuxLocalSource(sock packetSocket) *linuxLocalSource {
	return &linuxLocalSource{sock: sock, done: make(chan struct{})}
}

// Receive closes its channel after cancellation or Close, even if the
// consumer stops reading. Terminal error delivery is best effort.
func (s *linuxLocalSource) Receive(ctx context.Context) <-chan Frame {
	frames := make(chan Frame, 1)

	// frames is closed exactly once, from whichever of these two paths
	// reaches it first: recvLoop's own return (fn's literal statement,
	// after recvLoop is done sending), or the panic sink. Neither is a
	// deferred close inside recvLoop itself — a deferred close there would
	// already have run, during the panic's own unwind, by the time the sink
	// fires, and closing frames twice panics.
	spawn.Go(ctx, "rawsocket.linuxLocalSource.Receive", func() {
		s.recvLoop(ctx, frames)
		close(frames)
	}, spawn.ReportTo(func(err error) {
		// A terminal Frame here is best effort, same as every other exit
		// from recvLoop; the caller does not depend on it, since an
		// unattributed close it did not itself request already reads as an
		// error (Engine.run: "source closed its frame channel
		// unexpectedly").
		sendTerminal(frames, Frame{Err: err})
		close(frames)
	}))

	return frames
}

// recvLoop is Receive's goroutine body, factored out so Receive can close
// frames itself after recvLoop returns rather than deferring the close
// inside it. See the ordering comment at the call site.
func (s *linuxLocalSource) recvLoop(ctx context.Context, frames chan<- Frame) {
	buf := make([]byte, maxFrameLen)
	oob := make([]byte, unix.CmsgSpace(20))
	for {
		select {
		case <-ctx.Done():
			sendTerminal(frames, Frame{Err: ctx.Err()})
			return
		case <-s.done:
			return
		default:
		}

		n, oobn, flags, open, err := s.receiveOnce(buf, oob)
		if !open {
			// Close always closes done before releasing the lock, so
			// reaching here means the <-s.done case above lost this
			// iteration's select race, not that anything failed: a
			// clean shutdown, no terminal error frame.
			return
		}

		if err != nil {
			if isRetryable(err) {
				select {
				case <-ctx.Done():
					sendTerminal(frames, Frame{Err: ctx.Err()})
					return
				default:
					continue
				}
			}
			sendTerminal(frames, Frame{Err: errs.From(err).
				Code(ErrCodeSourceOpen).
				Msg("recvmsg")})
			return
		}
		if flags&unix.MSG_CTRUNC != 0 {
			sendTerminal(frames, Frame{Err: errs.New().
				Code(ErrCodeSourceOpen).
				Msg("packet auxiliary data truncated")})
			return
		}

		captured := n
		if captured > len(buf) {
			// MSG_TRUNC: the wire frame was longer than the buffer.
			captured = len(buf)
		}
		data, originalLength, err := restoreVLAN(buf[:captured], n, oob[:oobn])
		if err != nil {
			sendTerminal(frames, Frame{Err: errs.From(err).
				Code(ErrCodeSourceOpen).
				Msg("restore VLAN header")})
			return
		}
		if s.vm != nil {
			accepted, err := s.vm.Run(data)
			if err != nil {
				sendTerminal(frames, Frame{Err: errs.From(err).
					Code(ErrCodeSourceOpen).
					Msg("run local interface filter")})
				return
			}
			if accepted == 0 {
				continue
			}
		}

		f := Frame{Data: data, OriginalLength: uint32(originalLength), CapturedAt: time.Now()}
		select {
		case frames <- f:
		case <-ctx.Done():
			sendTerminal(frames, Frame{Err: ctx.Err()})
			return
		case <-s.done:
			return
		}
	}
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

// Stats reports counts since the last call. PACKET_STATISTICS resets the
// kernel's own counters to zero on each read, so this method's return
// values are a delta, not a running total; a caller that wants a running
// total accumulates the deltas itself. Stats shares a lock with an
// in-flight Receive poll, so a call can block for up to one poll interval
// (100ms).
func (s *linuxLocalSource) Stats() (received, droppedByInterface uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, 0, errs.New().
			Code(ErrCodeSourceOpen).
			Msg("stats on closed source")
	}
	packets, drops, err := s.sock.stats()
	if err != nil {
		return 0, 0, errs.From(err).
			Code(ErrCodeSourceOpen).
			Msg("packet statistics")
	}
	return packets, drops, nil
}

// Close waits for an active poll before releasing the socket (up to one
// poll interval, 100ms), then wakes any receiver waiting for its consumer.
// Repeated calls do nothing.
func (s *linuxLocalSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	err := s.sock.close()
	s.closed = true
	close(s.done)
	return err
}

// An abandoned consumer must not strand Receive on the final error frame.
// receiveOnce performs one guarded read. open is false when the source has
// been closed under the lock, which is a clean shutdown rather than an error.
//
// The lock is released by a defer because this runs on a supervised
// goroutine: a panic in recvmsg is recovered, so a release written after the
// call would be skipped and s.mu would stay held for the life of the process.
// Stats and Close both take it, so the capture engine would block there
// forever — a hang in place of the crash the recovery replaced.
func (s *linuxLocalSource) receiveOnce(buf, oob []byte) (n, oobn, flags int, open bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, 0, 0, false, nil
	}
	n, oobn, flags, err = s.sock.recvmsg(buf, oob)
	return n, oobn, flags, true, err
}

func sendTerminal(frames chan<- Frame, f Frame) {
	select {
	case frames <- f:
	default:
	}
}

// isRetryable reports whether err means "no data this poll, try again"
// rather than a real failure: SO_RCVTIMEO's own timeout (EAGAIN/EWOULDBLOCK)
// or a signal interrupting the blocked receive (EINTR). signal(7) states
// that a socket with SO_RCVTIMEO set always returns EINTR on a signal,
// never restarting the call regardless of SA_RESTART, and the Go runtime's
// own asynchronous goroutine preemption (SIGURG) and profiling signals make
// that a routine event during a long poll, not an edge case.
func isRetryable(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR)
}

var _ Source = (*linuxLocalSource)(nil)
