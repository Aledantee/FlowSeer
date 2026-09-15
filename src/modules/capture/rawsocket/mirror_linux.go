//go:build linux

package rawsocket

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/modules/capture/mirror"
)

// inet6PktinfoLen bounds the ancillary-data buffer: an in6_pktinfo (16-byte
// address + 4-byte ifindex) is larger than an in_pktinfo, so sizing for it
// fits either.
const inet6PktinfoLen = 20

// mirrorSocket is the syscall surface mirror_linux.go needs, seamed out so
// the receive-loop logic (shared by the raw GRE and UDP sockets) is tested
// without a real socket. The GRE-family path can only be exercised through
// this seam in default tests, since opening a raw socket needs CAP_NET_RAW;
// the UDP-family path is also tested against a real loopback socket, since
// VXLAN and TZSP's conventional ports need no elevated privilege.
type mirrorSocket interface {
	recvmsg(p, oob []byte) (n, oobn int, from unix.Sockaddr, err error)
	close() error
}

type fdMirrorSocket struct {
	fd int
}

func (s *fdMirrorSocket) recvmsg(p, oob []byte) (int, int, unix.Sockaddr, error) {
	n, oobn, _, from, err := unix.Recvmsg(s.fd, p, oob, unix.MSG_TRUNC)
	return n, oobn, from, err
}

func (s *fdMirrorSocket) close() error {
	return unix.Close(s.fd)
}

func setRecvTimeout(fd int) error {
	tv := unix.NsecToTimeval(pollTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return errs.From(err).
			Code(ErrCodeSourceOpen).
			Msg("set receive timeout")
	}
	return nil
}

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

// greFamily and udpFamily report which of MirrorReceiverSource's configured
// encapsulations need the raw GRE sockets versus the UDP socket.
func splitEncapsulations(encapsulations []capturev1.MirrorEncapsulation) (greFamily bool, udpFamily []capturev1.MirrorEncapsulation) {
	for _, e := range encapsulations {
		switch e {
		case capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_I,
			capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_II,
			capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_III,
			capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_GRE:
			greFamily = true
		case capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN,
			capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP:
			udpFamily = append(udpFamily, e)
		}
	}
	return greFamily, udpFamily
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
	src := &linuxMirrorSource{candidates: udpFamily, vm: vm, done: make(chan struct{})}

	if greFamily {
		v4, err := openRawGRE(unix.AF_INET, bindInterface)
		if err != nil {
			return nil, err
		}
		src.rawV4 = v4

		v6, err := openRawGRE(unix.AF_INET6, bindInterface)
		if err != nil {
			_ = v4.close()
			return nil, err
		}
		src.rawV6 = v6
	}

	if len(udpFamily) > 0 {
		udp, err := openMirrorUDP(udpPort, bindInterface)
		if err != nil {
			if src.rawV4 != nil {
				_ = src.rawV4.close()
			}
			if src.rawV6 != nil {
				_ = src.rawV6.close()
			}
			return nil, err
		}
		src.udp = udp
	}

	return src, nil
}

// linuxMirrorSource fans multiple sockets — the two raw GRE-family sockets
// and the UDP socket — into one Frame channel. rawV4 and rawV6 are separate
// fields, not a slice, because they need different decode wrappers: an
// AF_INET SOCK_RAW socket includes the IPv4 header in what it delivers
// (raw(7)), an AF_INET6 one does not, and mirror.Decode's own contract
// never assumes an IP-header-prefixed buffer.
//
// mu guards closed and serializes it against an in-flight recvmsg on any of
// the three sockets, the same way linuxLocalSource does, so Close cannot
// release a file descriptor a receive goroutine is still blocked in.
// statsMu guards received/reportedReceived as a pair, since Stats' Load
// then Swap is not atomic across two calls. done wakes every loop goroutine
// on Close.
type linuxMirrorSource struct {
	rawV4      mirrorSocket
	rawV6      mirrorSocket
	udp        mirrorSocket
	candidates []capturev1.MirrorEncapsulation
	vm         *bpf.VM

	statsMu          sync.Mutex
	received         atomic.Uint64
	reportedReceived atomic.Uint64

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// decodeV4 strips the IPv4 header raw(7) says an AF_INET SOCK_RAW socket
// includes in every received datagram before delegating to mirror.Decode,
// whose own contract never assumes an IP-header-prefixed buffer. AF_INET6
// does not include one, so rawV6 uses mirror.Decode directly.
func decodeV4(payload []byte, src, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
	if len(payload) < 20 {
		return nil, nil, fmt.Errorf("IPv4 header truncated: %d bytes", len(payload))
	}
	ihl := int(payload[0]&0x0F) * 4
	if ihl < 20 || ihl > len(payload) {
		return nil, nil, fmt.Errorf("IPv4 header length %d invalid for a %d-byte packet", ihl, len(payload))
	}
	return mirror.Decode(payload[ihl:], src, dst)
}

func (s *linuxMirrorSource) Receive(ctx context.Context) <-chan Frame {
	frames := make(chan Frame, 1)
	var wg sync.WaitGroup

	// Each loop's own exits already call sendTerminal before returning; the
	// sink here covers only the case where the loop panics before reaching
	// one of them. It cannot race the close below: frames closes only after
	// every loop (this one included, via wg) has finished.
	// None of these loops reports through frames on the panic path, and they
	// must not: wg.Done is the goroutine's own defer, so it runs during the
	// panic unwind, before the helper's recover. The awaiting goroutine below
	// can therefore observe wg.Wait return and close frames before a sink
	// would run, and a send on a closed channel panics even from a select
	// with a default — inside the recover, where nothing catches it. A
	// recovered panic would become a process crash. The helper's log record
	// carries the diagnosis instead.
	if s.rawV4 != nil {
		wg.Add(1)
		spawn.Go(ctx, "rawsocket.linuxMirrorSource.runMirrorLoop.rawV4", func() {
			defer wg.Done()
			s.runMirrorLoop(ctx, s.rawV4, decodeV4, frames)
		})
	}
	if s.rawV6 != nil {
		wg.Add(1)
		spawn.Go(ctx, "rawsocket.linuxMirrorSource.runMirrorLoop.rawV6", func() {
			defer wg.Done()
			s.runMirrorLoop(ctx, s.rawV6, mirror.Decode, frames)
		})
	}
	if s.udp != nil {
		wg.Add(1)
		spawn.Go(ctx, "rawsocket.linuxMirrorSource.runMirrorLoop.udp", func() {
			defer wg.Done()
			decodeUDP := func(payload []byte, src, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
				return mirror.DecodeUDP(payload, src, dst, s.candidates)
			}
			s.runMirrorLoop(ctx, s.udp, decodeUDP, frames)
		})
	}

	// wg.Wait blocking forever needs every loop above to actually finish,
	// which they now do even on a panic (wg.Done is their first defer); the
	// only way this goroutine itself fails to close frames is a panic in
	// Wait or close, which the sink covers so frames is never left open
	// with nothing left running that could ever close it.
	spawn.Go(ctx, "rawsocket.linuxMirrorSource.awaitReceiveClose", func() {
		wg.Wait()
		close(frames)
	}, spawn.ReportTo(func(err error) {
		close(frames)
	}))

	return frames
}

// Stats reports the frames this receiver has counted (received, decoded to
// a marker or non-Ethernet payload, or filtered out — every packet that
// reached a socket read) since the last call, matching PACKET_STATISTICS'
// own read-resets-the-counter contract on the local-interface source, so a
// caller polls both the same way. No comparable per-socket kernel drop
// counter exists for a raw or UDP socket, so droppedByInterface is always
// zero: this receiver's dominant loss mode is delivery to whatever drains
// it, not the interface itself losing packets before a socket read ever
// happens.
func (s *linuxMirrorSource) Stats() (received, droppedByInterface uint64, err error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	total := s.received.Load()
	prev := s.reportedReceived.Swap(total)
	return total - prev, 0, nil
}

func (s *linuxMirrorSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	close(s.done)

	var firstErr error
	if s.rawV4 != nil {
		if err := s.rawV4.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.rawV6 != nil {
		if err := s.rawV6.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.udp != nil {
		if err := s.udp.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// runMirrorLoop polls sock, decodes each datagram with decode, runs vm
// (when non-nil) against the inner frame, and delivers what survives.
//
// s.mu is held around each recvmsg call, checking s.closed first, the same
// way linuxLocalSource guards its own single socket: Close cannot release a
// file descriptor this loop is still blocked reading from. All three of a
// receiver's sockets share s.mu, so their reads serialize against each
// other; a diagnostic capture's sockets are not a line-rate path, and
// correctness (never closing a fd out from under a blocked recvmsg, which
// risks another unrelated fd being assigned the same number) outweighs the
// small added latency.
func (s *linuxMirrorSource) runMirrorLoop(
	ctx context.Context,
	sock mirrorSocket,
	decode func(payload []byte, src, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error),
	frames chan<- Frame,
) {
	buf := make([]byte, maxFrameLen)
	oob := make([]byte, unix.CmsgSpace(inet6PktinfoLen))

	for {
		select {
		case <-ctx.Done():
			sendTerminal(frames, Frame{Err: ctx.Err()})
			return
		case <-s.done:
			return
		default:
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		n, oobn, from, err := sock.recvmsg(buf, oob)
		s.mu.Unlock()

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

		s.received.Add(1)

		captured := n
		if captured > len(buf) {
			// MSG_TRUNC: the wire datagram was longer than the buffer.
			captured = len(buf)
		}

		srcIP := sockaddrIP(from)
		dstIP := pktinfoDestination(oob[:oobn])
		if dstIP == nil {
			// Should not happen with IP_PKTINFO/IPV6_RECVPKTINFO enabled;
			// degrade to the source address rather than drop the packet's
			// own metadata over a missing ancillary message.
			dstIP = srcIP
		}

		env, inner, err := decode(buf[:captured], srcIP, dstIP)
		if err != nil || inner == nil {
			// A decode error (malformed or foreign packet) or a decoded
			// envelope with no inner frame (a marker, or a non-Ethernet
			// payload): counted above, no record.
			continue
		}
		if s.vm != nil {
			if accepted, err := s.vm.Run(inner); err != nil || accepted == 0 {
				continue
			}
		}

		// inner aliases buf, which the next iteration's recvmsg overwrites:
		// copy before handing it to the consumer.
		data := make([]byte, len(inner))
		copy(data, inner)

		f := Frame{Data: data, OriginalLength: uint32(len(inner)), Envelope: env, CapturedAt: time.Now()}
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

func sockaddrIP(sa unix.Sockaddr) net.IP {
	switch a := sa.(type) {
	case *unix.SockaddrInet4:
		ip := make(net.IP, 4)
		copy(ip, a.Addr[:])
		return ip
	case *unix.SockaddrInet6:
		ip := make(net.IP, 16)
		copy(ip, a.Addr[:])
		return ip
	default:
		return nil
	}
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
