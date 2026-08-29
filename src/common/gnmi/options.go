package gnmi

import (
	"crypto/tls"
	"crypto/x509"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Default timeouts, applied by [Options.withDefaults].
const (
	defaultDialTimeout = 30 * time.Second
	defaultRPCTimeout  = 60 * time.Second
)

// Options is the plain options struct for [Dial]: credential
// metadata, the TLS posture, and timeouts. Required parameters (the
// target address) are positional on Dial. Exactly one TLS posture
// must be chosen — there is no permissive default.
type Options struct {
	// Username and Password travel as gNMI metadata on every RPC,
	// the convention AOS-CX and IOS-XE gNMI use.
	Username string
	Password string

	// CACertPEM verifies the peer with a private CA bundle (server
	// TLS).
	CACertPEM []byte
	// ClientCertPEM and ClientKeyPEM enable mutual TLS; combine with
	// CACertPEM for a private CA.
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	// InsecureSkipTLSVerify runs TLS but skips server verification.
	// An explicit lab-device opt-in.
	InsecureSkipTLSVerify bool
	// Plaintext disables TLS entirely (test servers, side-band lab
	// ports). An explicit opt-in.
	Plaintext bool

	// DialTimeout bounds channel establishment plus the Capabilities
	// exchange. Zero means 30s.
	DialTimeout time.Duration
	// RPCTimeout bounds unary RPCs when the caller's context has no
	// earlier deadline. Zero means 60s. Subscribe streams are bounded
	// only by their context.
	RPCTimeout time.Duration

	// TracerProvider supplies OTel tracing for RPC spans. Nil means
	// no-op. Only the OTel API is consumed, never the SDK.
	TracerProvider trace.TracerProvider
}

// withDefaults returns a copy with zero timeouts defaulted.
func (o Options) withDefaults() Options {
	if o.DialTimeout <= 0 {
		o.DialTimeout = defaultDialTimeout
	}
	if o.RPCTimeout <= 0 {
		o.RPCTimeout = defaultRPCTimeout
	}
	return o
}

// transportCredentials derives the gRPC credentials from the TLS
// posture.
func (o Options) transportCredentials() (credentials.TransportCredentials, error) {
	explicitTLS := len(o.CACertPEM) > 0 || len(o.ClientCertPEM) > 0 || o.InsecureSkipTLSVerify
	switch {
	case o.Plaintext && explicitTLS:
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: plaintext and TLS settings are mutually exclusive")
	case o.Plaintext:
		return insecure.NewCredentials(), nil
	case !explicitTLS:
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: choose a TLS posture — CA bundle, client certificate, InsecureSkipTLSVerify, or the explicit Plaintext opt-in")
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.InsecureSkipTLSVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec // documented explicit lab opt-in, never a default
	}
	if len(o.CACertPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(o.CACertPEM) {
			return nil, errs.New().Code(ErrCodeTransport).Msg("options: CA bundle contains no usable certificates")
		}
		cfg.RootCAs = pool
	}
	if len(o.ClientCertPEM) > 0 || len(o.ClientKeyPEM) > 0 {
		cert, err := tls.X509KeyPair(o.ClientCertPEM, o.ClientKeyPEM)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeTransport).Msg("options: load client certificate")
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return credentials.NewTLS(cfg), nil
}
