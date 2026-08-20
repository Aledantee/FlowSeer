package netconf

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/crypto/ssh"
	nclib "nemith.io/netconf"
	ncssh "nemith.io/netconf/transport/ssh"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// Transport is the RPC transport seam (KTD1): the envelope layer the
// session drives. The production implementation wraps
// nemith.io/netconf; tests script a fake; a house transport can
// replace the dependency without touching callers.
type Transport interface {
	// Exec sends one RPC operation and decodes the reply body into
	// reply (which may be nil for ok-only operations). A device
	// <rpc-error> surfaces as an error.
	Exec(ctx context.Context, op, reply any) error
	// Capabilities returns the server's advertised capability URIs.
	Capabilities() []string
	// Close tears the transport down.
	Close(ctx context.Context) error
}

// Session is a NETCONF session over a [Transport]. Construct via
// [Dial] or [NewSession]; see the package documentation for the
// lifecycle contract. Safe for concurrent use.
type Session struct {
	t      Transport
	caps   capabilities
	opts   Options
	tracer trace.Tracer

	mu     sync.Mutex
	err    error // first latched terminal error
	closed bool

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// Dial establishes a NETCONF-over-SSH session to addr (host:port).
// Username plus at least one credential are required; host-key
// verification must be configured explicitly (pin or insecure
// opt-in).
func Dial(ctx context.Context, addr string, opts Options) (*Session, error) {
	opts = opts.withDefaults()
	cfg, err := sshConfig(opts)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, opts.DialTimeout)
	defer cancel()
	tr, err := ncssh.Dial(dialCtx, "tcp", addr, cfg)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeTransport).Msgf("dial %s", addr)
	}
	inner, err := nclib.NewSession(tr)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeTransport).Msgf("establish NETCONF session with %s", addr)
	}
	return NewSession(&nemithTransport{s: inner}, opts), nil
}

// sshConfig builds the SSH client configuration from the options.
func sshConfig(opts Options) (*ssh.ClientConfig, error) {
	if opts.Username == "" {
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: username is required")
	}
	var auth []ssh.AuthMethod
	if len(opts.PrivateKeyPEM) > 0 {
		signer, err := ssh.ParsePrivateKey(opts.PrivateKeyPEM)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeTransport).Msg("options: parse private key")
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if opts.Password != "" {
		auth = append(auth, ssh.Password(opts.Password))
	}
	if len(auth) == 0 {
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: a password or private key is required")
	}

	var hostKey ssh.HostKeyCallback
	switch {
	case opts.InsecureIgnoreHostKey && opts.HostKeySHA256 != "":
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: host-key pin and insecure opt-in are mutually exclusive")
	case opts.InsecureIgnoreHostKey:
		hostKey = ssh.InsecureIgnoreHostKey() //nolint:gosec // documented explicit lab opt-in (KTD8)
	case opts.HostKeySHA256 != "":
		want := opts.HostKeySHA256
		if !hasSHA256Prefix(want) {
			want = "SHA256:" + want
		}
		hostKey = pinnedHostKey(want)
	default:
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: set HostKeySHA256 or the explicit InsecureIgnoreHostKey opt-in")
	}

	return &ssh.ClientConfig{
		User:            opts.Username,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         opts.DialTimeout,
	}, nil
}

// pinnedHostKey verifies the peer key against a SHA256:base64
// fingerprint pin. The observed key is never interpolated: a
// mismatch names only the host.
func pinnedHostKey(pin string) ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		if got := ssh.FingerprintSHA256(key); got != pin {
			return errs.New().
				Code(ErrCodeTransport).
				Msgf("host key of %s does not match the pinned fingerprint", hostname)
		}
		return nil
	}
}

// hasSHA256Prefix reports whether s already carries the fingerprint
// scheme prefix.
func hasSHA256Prefix(s string) bool {
	return len(s) > 7 && s[:7] == "SHA256:"
}

// nemithTransport adapts nemith.io/netconf's session to [Transport].
type nemithTransport struct {
	s *nclib.Session
}

// Exec sends one RPC through the wrapped session.
func (t *nemithTransport) Exec(ctx context.Context, op, reply any) error {
	if reply == nil {
		reply = &struct{}{}
	}
	return t.s.Exec(ctx, op, reply)
}

// Capabilities returns the server's advertised capabilities.
func (t *nemithTransport) Capabilities() []string {
	var out []string
	for c := range t.s.ServerCaps().All() {
		out = append(out, c)
	}
	return out
}

// Close closes the wrapped session.
func (t *nemithTransport) Close(ctx context.Context) error {
	return t.s.Close(ctx)
}

// NewSession wraps an established transport: it parses the
// capability set and starts the keepalive guard when configured.
func NewSession(t Transport, opts Options) *Session {
	opts = opts.withDefaults()
	tp := opts.TracerProvider
	if tp == nil {
		tp = tracenoop.NewTracerProvider()
	}
	s := &Session{
		t:      t,
		caps:   parseCapabilities(t.Capabilities()),
		opts:   opts,
		tracer: tp.Tracer("go.aledante.io/FlowSeer/src/common/netconf"),
		stop:   make(chan struct{}),
	}
	if opts.KeepaliveInterval > 0 {
		s.wg.Add(1)
		go s.keepalive()
	}
	return s
}

// Capabilities returns the server's advertised capability URIs.
func (s *Session) Capabilities() []string { return s.caps.all }

// EditTarget returns the datastore this peer's edits address
// (capability-driven per R1) and whether the peer is editable at all.
func (s *Session) EditTarget() (Datastore, bool) { return s.caps.editTarget() }

// Err returns the session's first latched terminal error (dead
// transport, keepalive trip), or nil.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// latch records the first terminal error.
func (s *Session) latch(err error) {
	s.mu.Lock()
	if s.err == nil && err != nil {
		s.err = err
	}
	s.mu.Unlock()
}

// Close tears the session down. Idempotent; safe concurrently with
// in-flight RPCs, which fail with their transport's close error.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.stopOnce.Do(func() { close(s.stop) })
	err := s.t.Close(ctx)
	s.wg.Wait()
	if err != nil {
		return errs.From(err).Code(ErrCodeTransport).Msg("close transport")
	}
	return nil
}

// exec runs one RPC with timeout defaulting, tracing, error mapping,
// and transport-error latching.
func (s *Session) exec(ctx context.Context, name string, op, reply any) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.opts.RPCTimeout)
		defer cancel()
	}

	ctx, span := s.tracer.Start(ctx, "netconf."+name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("netconf_operation", name)))
	defer span.End()

	err := s.t.Exec(ctx, op, reply)
	if err == nil {
		return nil
	}
	span.SetStatus(codes.Error, err.Error())
	mapped := s.mapError(name, err)
	if mappedCode, ok := errs.CodeOf(mapped); ok && mappedCode == ErrCodeTransport {
		s.latch(mapped)
	}
	return mapped
}

// mapError translates transport-layer errors into the package's
// error contract.
func (s *Session) mapError(name string, err error) error {
	// Context cancellation stays an unwrapped ctx error end to end.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var rpcErrs nclib.RPCErrors
	var rpcErr nclib.RPCError
	switch {
	case errors.As(err, &rpcErrs) && len(rpcErrs) > 0:
		return deviceError(name, rpcErrs[0], err)
	case errors.As(err, &rpcErr):
		return deviceError(name, rpcErr, err)
	}
	return errs.From(err).Code(ErrCodeTransport).Msgf("%s transport failed", name)
}

// deviceError shapes one <rpc-error> into the errs contract,
// preserving the device's fields as attributes.
func deviceError(name string, e nclib.RPCError, cause error) error {
	b := errs.From(cause).
		Attr("error_tag", string(e.Tag)).
		Attr("severity", string(e.Severity)).
		Attr("error_path", e.Path)
	if e.Tag == nclib.ErrLockDenied {
		return b.Code(ErrCodeLockDenied).Retryable().Msgf("%s: datastore lock held by another session", name)
	}
	return b.Code(ErrCodeRPC).Msgf("%s rejected by device: %s", name, deviceMessage(e))
}

// deviceMessage picks the most useful human text from an rpc-error.
func deviceMessage(e nclib.RPCError) string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Tag)
}

// keepalive probes the peer between RPCs so a dead transport latches
// promptly instead of blocking the next caller.
func (s *Session) keepalive() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.opts.KeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.opts.KeepaliveInterval)
		// A subtree filter selecting a nonexistent element keeps the
		// probe reply near-empty on any conformant peer.
		err := s.exec(ctx, "keepalive", &getConfigOp{
			Source: dsElem(Running),
			Filter: newSubtreeFilter([]byte("<flowseer-keepalive-probe/>")),
		}, &dataReply{})
		cancel()
		if err != nil && !errors.Is(err, ErrSessionClosed) {
			s.latch(errs.From(err).Code(ErrCodeTransport).Msg("keepalive probe failed"))
			return
		}
	}
}

// Get reads operational state plus config (RFC 6241 <get>) under an
// optional subtree filter; the zero Path selects everything. The
// returned bytes are the <data> payload for the generated codecs.
func (s *Session) Get(ctx context.Context, filter yang.Path) ([]byte, error) {
	inner, err := renderFilter(filter)
	if err != nil {
		return nil, err
	}
	var reply dataReply
	if err := s.exec(ctx, "get", &getOp{Filter: newSubtreeFilter(inner)}, &reply); err != nil {
		return nil, err
	}
	return reply.Data.Inner, nil
}

// GetConfig reads one datastore's configuration under an optional
// subtree filter.
func (s *Session) GetConfig(ctx context.Context, ds Datastore, filter yang.Path) ([]byte, error) {
	inner, err := renderFilter(filter)
	if err != nil {
		return nil, err
	}
	var reply dataReply
	op := &getConfigOp{Source: dsElem(ds), Filter: newSubtreeFilter(inner)}
	if err := s.exec(ctx, "get-config", op, &reply); err != nil {
		return nil, err
	}
	return reply.Data.Inner, nil
}

// renderFilter renders a path as subtree-filter XML; the zero path
// means no filter.
func renderFilter(p yang.Path) ([]byte, error) {
	if len(p.Segments) == 0 {
		return nil, nil
	}
	return p.SubtreeFilterXML()
}
