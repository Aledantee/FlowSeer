package restconf

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// defaultTimeout bounds each request when the caller's context
// carries no earlier deadline.
const defaultTimeout = 30 * time.Second

// Options is the plain options struct for [Dial]: the basic
// auth credential surface, the TLS posture, and timeouts. Required
// parameters (the base URL) are positional on Dial. Configure Options
// before sharing it with concurrent Dial calls; an HTTPClient override
// and its transport must also support concurrent use.
type Options struct {
	// Username and Password enable HTTP basic auth, the scheme ICX
	// and IOS-XE RESTCONF use.
	Username string
	Password secret.Value

	// CACertPEM verifies the peer with a private CA bundle instead of
	// the system pool.
	CACertPEM []byte
	// ClientCertPEM and ClientKeyPEM enable mutual TLS.
	ClientCertPEM []byte
	ClientKeyPEM  secret.Value
	// InsecureSkipTLSVerify disables server-certificate verification.
	// An explicit lab-device opt-in, never a default.
	InsecureSkipTLSVerify bool

	// Timeout bounds each request when the caller's context has no
	// earlier deadline. Nonpositive values mean 30s.
	Timeout time.Duration

	// HTTPClient overrides the transport entirely (the test seam and
	// the proxy escape hatch). TLS options above are ignored when
	// set.
	HTTPClient *http.Client

	// TracerProvider supplies OTel tracing for request spans. Nil
	// means no-op. Only the OTel API is consumed, never the SDK.
	TracerProvider trace.TracerProvider
}

// withDefaults returns a copy with zero values defaulted.
func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	return o
}

// httpClient builds the HTTP client from the TLS options, or returns
// the override.
func (o Options) httpClient() (*http.Client, error) {
	if o.HTTPClient != nil {
		return o.HTTPClient, nil
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.InsecureSkipTLSVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // documented explicit lab opt-in, never a default
	}
	if len(o.CACertPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(o.CACertPEM) {
			return nil, errs.New().Code(ErrCodeTransport).Msg("options: CA bundle contains no usable certificates")
		}
		tlsCfg.RootCAs = pool
	}
	if len(o.ClientCertPEM) > 0 || !o.ClientKeyPEM.Empty() {
		cert, err := tls.X509KeyPair(o.ClientCertPEM, o.ClientKeyPEM.Reveal())
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeTransport).Msg("options: load client certificate")
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}, nil
}
