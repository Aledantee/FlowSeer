//go:build linux

package packetio

import (
	"context"
	"io"
	"net"
	"sync"

	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// writeSocket is the small syscall seam used by the sender and its tests.
type writeSocket interface {
	write([]byte) (int, error)
	close() error
}

// fdSocket is a bound AF_PACKET descriptor.
type fdSocket struct {
	fd int
}

func (s *fdSocket) write(p []byte) (int, error) {
	return unix.Write(s.fd, p)
}

func (s *fdSocket) close() error {
	return unix.Close(s.fd)
}

type linuxSender struct {
	sock writeSocket

	mu     sync.Mutex
	closed bool
}

const sendOnlyProtocol = 0

func openSender(interfaceName string) (Sender, error) {
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeSenderOpen).
			Attr("iface", interfaceName).
			UserMsg("the named transmit interface does not exist").
			Msgf("resolve transmit interface %q", interfaceName)
	}

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, sendOnlyProtocol)
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeSenderOpen).
			Attr("iface", interfaceName).
			UserMsg("could not open the transmit interface for raw packet sending").
			Hint("check the interface name and that you have CAP_NET_RAW").
			Msgf("af_packet socket %q", interfaceName)
	}

	address := &unix.SockaddrLinklayer{
		Protocol: sendOnlyProtocol,
		Ifindex:  iface.Index,
	}
	if err := unix.Bind(fd, address); err != nil {
		_ = unix.Close(fd)
		return nil, errs.From(err).
			Code(ErrCodeSenderOpen).
			Attr("iface", interfaceName).
			Msgf("bind af_packet sender to %q", interfaceName)
	}

	return newLinuxSender(&fdSocket{fd: fd}), nil
}

func newLinuxSender(sock writeSocket) *linuxSender {
	return &linuxSender{sock: sock}
}

// Send checks ctx and writes one complete frame. A short kernel write is
// reported as io.ErrShortWrite because sending a prefix would corrupt the
// frame-level observation contract.
func (s *linuxSender) Send(ctx context.Context, frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return ErrClosed
	}

	n, err := s.sock.write(frame)
	if err != nil {
		return err
	}
	if n != len(frame) {
		return io.ErrShortWrite
	}

	return nil
}

// Close closes the descriptor once. Later calls return nil even if the first
// close returned an operating-system error.
func (s *linuxSender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	return s.sock.close()
}
