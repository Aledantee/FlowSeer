package syslog

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Delivery describes local write evidence, never a receiver acknowledgment.
type Delivery string

const (
	// NotSent means no payload bytes were written by this operation.
	NotSent Delivery = "not_sent"
	// Written means all bytes were accepted by the local socket implementation.
	Written Delivery = "written"
	// UnknownDelivery means a failed write may have delivered part of the message.
	UnknownDelivery Delivery = "unknown"
)

// SendReport preserves the encoding projection and local delivery evidence.
type SendReport struct {
	Encoding     EncodeReport
	Delivery     Delivery
	BytesWritten int
}

// SenderOptions selects framing, verified TLS, and finite operation limits.
// Timeout defaults to five seconds. TLS callbacks must honor their context.
type SenderOptions struct {
	Framing   Framing
	TLSConfig *tls.Config
	Timeout   time.Duration
	Limits    Limits
}

// Sender owns one destination connection with no queue or automatic replay.
// One Send may be active; concurrent calls return ErrBusy without copying input.
type Sender struct {
	address    string
	transport  Transport
	options    SenderOptions
	busy       atomic.Bool
	mu         sync.Mutex
	closed     bool
	raw, conn  net.Conn
	cancel     context.CancelFunc
	activeDone chan struct{}
}

// NewSender validates and snapshots configuration without opening a socket.
// Address is host:port. An omitted TLS ServerName is derived from its host.
func NewSender(address string, transport Transport, options SenderOptions) (*Sender, error) {
	if transport != UDP && transport != TCP && transport != TLS {
		return nil, errs.Msg("invalid syslog send transport")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errs.Wrap(err, "syslog destination")
	}
	l, err := options.Limits.normalized()
	if err != nil {
		return nil, err
	}
	options.Limits = l
	options.Framing = framingDefault(options.Framing, transport)
	if !validFraming(options.Framing) {
		return nil, ErrFraming
	}
	if options.Timeout < 0 {
		return nil, errs.Msg("negative syslog send timeout")
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if transport == TLS {
		options.TLSConfig, err = tlsConfiguration(options.TLSConfig, false)
		if err != nil {
			return nil, err
		}
		if options.TLSConfig.ServerName == "" {
			options.TLSConfig.ServerName = host
		}
	}
	return &Sender{address: address, transport: transport, options: options}, nil
}

// Send preflights encoding and framing before dialing. It respects the earlier
// of caller cancellation and Timeout. Failed connections are closed; only a new
// explicit Send can reconnect. The caller keeps record/options stable until return.
func (s *Sender) Send(ctx context.Context, record Record, options EncodeOptions) (SendReport, error) {
	report := SendReport{Delivery: NotSent}
	if !s.busy.CompareAndSwap(false, true) {
		return report, ErrBusy
	}
	defer s.busy.Store(false)
	operation, cancel := context.WithTimeout(ctx, s.options.Timeout)
	defer cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return report, ErrClosed
	}
	done := make(chan struct{})
	s.activeDone = done
	s.cancel = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.cancel = nil; s.activeDone = nil; close(done); s.mu.Unlock() }()
	if options.Limits != (Limits{}) {
		return report, errs.Msg("set sender limits on SenderOptions")
	}
	options.Limits = s.options.Limits
	payload, encoding, err := Encode(record, options)
	report.Encoding = encoding
	if err != nil {
		return report, err
	}
	if s.transport != UDP {
		payload, err = frameOutput(payload, s.options.Framing)
		if err != nil {
			return report, err
		}
	}
	if err := operation.Err(); err != nil {
		return report, err
	}
	conn, raw, err := s.connection(operation)
	if err != nil {
		return report, err
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(operation, func() { _ = raw.Close(); close(interrupted) })
	defer func() {
		if !stop() {
			<-interrupted
			s.discard()
		}
	}()
	deadline, _ := operation.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		s.discard()
		return report, err
	}
	n, err := writeAll(conn, payload, s.transport == UDP)
	report.BytesWritten = n
	if err != nil {
		s.discard()
		if n > 0 {
			report.Delivery = UnknownDelivery
		}
		if operation.Err() != nil {
			err = operation.Err()
		}
		return report, errs.Wrap(err, "send syslog")
	}
	report.Delivery = Written
	return report, nil
}

func (s *Sender) connection(ctx context.Context) (net.Conn, net.Conn, error) {
	s.mu.Lock()
	conn, raw := s.conn, s.raw
	s.mu.Unlock()
	if conn != nil {
		return conn, raw, nil
	}
	network := "tcp"
	if s.transport == UDP {
		network = "udp"
	}
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, network, s.address)
	if err != nil {
		return nil, nil, errs.Wrap(err, "dial syslog")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = raw.Close()
		return nil, nil, ErrClosed
	}
	s.raw = raw
	s.mu.Unlock()
	conn = raw
	if s.transport == TLS {
		secure := tls.Client(raw, s.options.TLSConfig)
		deadline, _ := ctx.Deadline()
		if err := raw.SetDeadline(deadline); err != nil {
			s.discard()
			return nil, nil, err
		}
		if err := secure.HandshakeContext(ctx); err != nil {
			s.discard()
			return nil, nil, errs.Wrap(err, "syslog TLS handshake")
		}
		conn = secure
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = raw.Close()
		return nil, nil, ErrClosed
	}
	s.conn = conn
	return conn, raw, nil
}

func (s *Sender) discard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.raw != nil {
		_ = s.raw.Close()
	}
	s.raw = nil
	s.conn = nil
}

// Close interrupts dialing, handshakes, or writes and waits for the active Send.
// It is safe to call repeatedly or concurrently. Caller callbacks must cancel.
func (s *Sender) Close() error {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.raw != nil {
		_ = s.raw.Close()
	}
	s.conn = nil
	s.raw = nil
	done := s.activeDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func writeAll(w io.Writer, payload []byte, datagram bool) (int, error) {
	total := 0
	for total < len(payload) {
		n, err := w.Write(payload[total:])
		if n < 0 || n > len(payload)-total {
			return total, io.ErrShortWrite
		}
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
		if datagram && total != len(payload) {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}
