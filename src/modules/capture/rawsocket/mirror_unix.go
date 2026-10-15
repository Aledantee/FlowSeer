//go:build linux || freebsd

package rawsocket

import (
	"context"
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

// mirrorSocket allows receive-loop tests without privileged raw sockets.
type mirrorSocket interface {
	recvmsg(p, oob []byte) (n, oobn, flags int, from unix.Sockaddr, err error)
	close() error
}

type fdMirrorSocket struct {
	fd int
}

func (s *fdMirrorSocket) recvmsg(p, oob []byte) (int, int, int, unix.Sockaddr, error) {
	return unix.Recvmsg(s.fd, p, oob, unix.MSG_TRUNC)
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

type controlParser func(oob []byte) (destination net.IP, index uint32, ok bool)

type mirrorReceiverSocket struct {
	socket       mirrorSocket
	parseControl controlParser
	oobSize      int
	wantedIndex  uint32
}

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

// mirrorSource combines the raw GRE and UDP sockets into one frame channel.
// rawV4 uses a decoder that strips the IPv4 header before mirror.Decode.
// mu serializes recvmsg and Close so a read cannot use a released descriptor.
// statsMu guards received and reportedReceived as a pair.
type mirrorSource struct {
	rawV4      *mirrorReceiverSocket
	rawV6      *mirrorReceiverSocket
	udp        *mirrorReceiverSocket
	candidates []capturev1.MirrorEncapsulation
	vm         *bpf.VM

	statsMu          sync.Mutex // guards received and reportedReceived as a pair
	received         atomic.Uint64
	reportedReceived atomic.Uint64

	mu     sync.Mutex // guards rawV4, rawV6, udp, and closed
	closed bool
	done   chan struct{}
}

// decodeV4 strips the IPv4 header delivered by raw IPv4 sockets.
func decodeV4(payload []byte, src, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
	if len(payload) < 20 {
		return nil, nil, errs.Msgf("IPv4 header truncated: %d bytes", len(payload))
	}
	ihl := int(payload[0]&0x0F) * 4
	if ihl < 20 || ihl > len(payload) {
		return nil, nil, errs.Msgf("IPv4 header length %d invalid for a %d-byte packet", ihl, len(payload))
	}
	return mirror.Decode(payload[ihl:], src, dst)
}

func (s *mirrorSource) Receive(ctx context.Context) <-chan Frame {
	frames := make(chan Frame, 1)
	var wg sync.WaitGroup

	// wg.Done runs during panic unwind, before spawn recovers. It can unblock
	// the waiter and close frames before a panic sink runs, so loop panic
	// sinks must not send to frames. spawn logs the panic instead.
	if s.rawV4 != nil {
		wg.Add(1)
		spawn.Go(ctx, "rawsocket.mirrorSource.runMirrorLoop.rawV4", func() {
			defer wg.Done()
			s.runMirrorLoop(ctx, s.rawV4, decodeV4, frames)
		})
	}
	if s.rawV6 != nil {
		wg.Add(1)
		spawn.Go(ctx, "rawsocket.mirrorSource.runMirrorLoop.rawV6", func() {
			defer wg.Done()
			s.runMirrorLoop(ctx, s.rawV6, mirror.Decode, frames)
		})
	}
	if s.udp != nil {
		wg.Add(1)
		spawn.Go(ctx, "rawsocket.mirrorSource.runMirrorLoop.udp", func() {
			defer wg.Done()
			decodeUDP := func(payload []byte, src, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
				return mirror.DecodeUDP(payload, src, dst, s.candidates)
			}
			s.runMirrorLoop(ctx, s.udp, decodeUDP, frames)
		})
	}

	// The sink closes frames if the waiter panics before reaching close.
	spawn.Go(ctx, "rawsocket.mirrorSource.awaitReceiveClose", func() {
		wg.Wait()
		close(frames)
	}, spawn.ReportTo(func(error) {
		close(frames)
	}))

	return frames
}

// Stats reports counts from the selected interface since the previous call,
// including packets rejected by decoding or filtering. Mirror sockets expose
// no interface drop counter.
func (s *mirrorSource) Stats() (received, droppedByInterface uint64, err error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	total := s.received.Load()
	prev := s.reportedReceived.Swap(total)
	return total - prev, 0, nil
}

func (s *mirrorSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	close(s.done)

	var firstErr error
	if s.rawV4 != nil {
		if err := s.rawV4.socket.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.rawV6 != nil {
		if err := s.rawV6.socket.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.udp != nil {
		if err := s.udp.socket.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// receiveOnce performs one guarded read on sock. open is false when the
// source has been closed under the lock, which is a clean shutdown.
//
// The lock is released by a defer because this runs on a supervised
// goroutine: a panic in recvmsg is recovered, so a release written after the
// call would be skipped and s.mu would stay held for the life of the process.
// Close also takes it and would block forever if the lock stayed held.
func (s *mirrorSource) receiveOnce(
	sock mirrorSocket,
	buf, oob []byte,
) (n, oobn, flags int, from unix.Sockaddr, open bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, 0, 0, nil, false, nil
	}
	n, oobn, flags, from, err = sock.recvmsg(buf, oob)
	return n, oobn, flags, from, true, err
}

func (s *mirrorSource) runMirrorLoop(
	ctx context.Context,
	sock *mirrorReceiverSocket,
	decode func(payload []byte, src, dst net.IP) (*capturev1.MirrorEnvelope, []byte, error),
	frames chan<- Frame,
) {
	buf := make([]byte, maxFrameLen)
	oob := make([]byte, sock.oobSize)

	for {
		select {
		case <-ctx.Done():
			sendTerminal(frames, Frame{Err: ctx.Err()})
			return
		case <-s.done:
			return
		default:
		}

		n, oobn, flags, from, open, err := s.receiveOnce(sock.socket, buf, oob)
		if !open {
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
				Msg("mirror control data truncated")})
			return
		}

		dstIP, index, indexOK := sock.parseControl(oob[:oobn])
		if sock.wantedIndex != 0 && (!indexOK || index != sock.wantedIndex) {
			continue
		}
		s.received.Add(1)

		captured := n
		if captured > len(buf) {
			// MSG_TRUNC: the wire datagram was longer than the buffer.
			captured = len(buf)
		}

		srcIP := sockaddrIP(from)
		if dstIP == nil {
			// Preserve envelope metadata when no destination was supplied.
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
