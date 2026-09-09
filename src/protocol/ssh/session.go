package ssh

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Session is one interactive SSH shell. Construct via [Dial]. Safe
// for concurrent use, except that [Session.Run] calls must not
// overlap: the shell has one input stream, so the caller serializes
// its own commands.
type Session struct {
	client *ssh.Client
	sh     *ssh.Session
	stdin  io.WriteCloser
	stdout *ring
	stderr *ring
	opts   Options

	mu     sync.Mutex // guards closed
	closed bool

	wg sync.WaitGroup
}

// Dial opens a TCP connection to addr, completes the SSH handshake,
// opens the one shell channel with a PTY, and starts it. Host-key
// verification must be configured explicitly via opts (pin or
// insecure opt-in); see [Options]. Dial respects ctx and
// opts.DialTimeout across both the TCP connect and the SSH handshake,
// since [golang.org/x/crypto/ssh.NewClientConn] takes no context.
func Dial(ctx context.Context, addr string, opts Options) (*Session, error) {
	opts = opts.withDefaults()
	cfg, err := sshConfig(opts)
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, opts.DialTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		if dialCtx.Err() != nil {
			return nil, dialCtx.Err()
		}
		return nil, errs.From(err).Code(ErrCodeTransport).Msgf("dial %s", addr)
	}

	// NewClientConn has no context parameter; watch dialCtx and close
	// the raw connection to unblock the handshake if it fires first.
	done := make(chan struct{})
	go func() {
		select {
		case <-dialCtx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	close(done)
	if err != nil {
		// NewClientConn does not close conn on failure, and the watcher
		// above is no longer eligible once done is closed. Left open, an
		// auth failure or a host-key mismatch leaks the socket until a
		// finalizer runs — and a device that caps concurrent sessions then
		// refuses the next login, which reads as a wrong password rather
		// than as exhaustion.
		_ = conn.Close()
		if dialCtx.Err() != nil {
			return nil, dialCtx.Err()
		}
		return nil, errs.From(err).Code(ErrCodeTransport).Msgf("ssh handshake with %s", addr)
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	sess, err := newSession(client, opts)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return sess, nil
}

// newSession opens the one shell channel on an established client and
// starts draining it.
func newSession(client *ssh.Client, opts Options) (*Session, error) {
	sh, err := client.NewSession()
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeShell).Msg("open shell channel")
	}

	stdin, err := sh.StdinPipe()
	if err != nil {
		_ = sh.Close()
		return nil, errs.From(err).Code(ErrCodeShell).Msg("open stdin pipe")
	}
	stdout, err := sh.StdoutPipe()
	if err != nil {
		_ = sh.Close()
		return nil, errs.From(err).Code(ErrCodeShell).Msg("open stdout pipe")
	}
	stderr, err := sh.StderrPipe()
	if err != nil {
		_ = sh.Close()
		return nil, errs.From(err).Code(ErrCodeShell).Msg("open stderr pipe")
	}
	if err := sh.RequestPty(opts.PTYTerm, opts.PTYHeight, opts.PTYWidth, ssh.TerminalModes{}); err != nil {
		_ = sh.Close()
		return nil, errs.From(err).Code(ErrCodeShell).Msg("request pty")
	}
	if err := sh.Shell(); err != nil {
		_ = sh.Close()
		return nil, errs.From(err).Code(ErrCodeShell).Msg("start shell")
	}

	s := &Session{
		client: client,
		sh:     sh,
		stdin:  stdin,
		stdout: newRing(opts.StdoutBufferBytes),
		stderr: newRing(opts.StderrBufferBytes),
		opts:   opts,
	}
	s.wg.Add(2)
	go s.drain(stdout, s.stdout)
	go s.drain(stderr, s.stderr)
	return s, nil
}

// drain copies r into ring until r returns an error (including a
// clean EOF), then records that error on ring and returns. One drain
// goroutine per stream, owned by the Session that started it and
// stopped by [Session.Close].
func (s *Session) drain(r io.Reader, ring *ring) {
	defer s.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			ring.write(buf[:n])
		}
		if err != nil {
			ring.closeWithErr(err)
			return
		}
	}
}

// isClosed reports whether Close has run.
func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close closes the shell and the underlying SSH connection and waits
// for both drain goroutines to stop. Idempotent and safe to call
// concurrently with [Session.Run]; a Run in flight observes the
// closure through its ring wait and returns an error.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.stdout.closeWithErr(ErrSessionClosed)
	s.stderr.closeWithErr(ErrSessionClosed)

	shErr := s.sh.Close()
	clientErr := s.client.Close()
	s.wg.Wait()

	if shErr != nil && !errors.Is(shErr, io.EOF) {
		return errs.From(shErr).Code(ErrCodeShell).Msg("close shell channel")
	}
	if clientErr != nil && !errors.Is(clientErr, io.EOF) {
		return errs.From(clientErr).Code(ErrCodeTransport).Msg("close ssh connection")
	}
	return nil
}
