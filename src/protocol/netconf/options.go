package netconf

import (
	"time"

	"go.opentelemetry.io/otel/trace"
)

// Default timeouts, applied by [Options.withDefaults].
const (
	defaultDialTimeout = 30 * time.Second
	defaultRPCTimeout  = 60 * time.Second
)

// Options configures SSH authentication and session timeouts.
// [Dial] requires credentials and explicit host-key verification;
// [NewSession] uses only the timeout, keepalive, and tracing fields.
// Options may be read concurrently but must not be changed while
// being passed to either constructor.
type Options struct {
	// Username authenticates the SSH transport. Required by Dial.
	Username string
	// Password enables SSH password authentication when non-empty.
	Password string
	// PrivateKeyPEM enables SSH public-key authentication when
	// non-empty. Both may be set; the transport offers both.
	PrivateKeyPEM []byte

	// HostKeySHA256 pins the peer's host key as the base64 SHA-256
	// fingerprint (the ssh-keygen -lf form). Exactly one of
	// HostKeySHA256 or InsecureIgnoreHostKey must be set: host-key
	// verification has no implicit default.
	HostKeySHA256 string
	// InsecureIgnoreHostKey disables host-key verification. An
	// explicit lab-device opt-in, never a default.
	InsecureIgnoreHostKey bool

	// DialTimeout bounds transport establishment plus hello exchange.
	// Non-positive values mean 30s.
	DialTimeout time.Duration
	// RPCTimeout bounds each RPC when the caller's context carries no
	// earlier deadline. Non-positive values mean 60s.
	RPCTimeout time.Duration
	// KeepaliveInterval enables the background liveness probe: every
	// interval the session issues a minimal RPC and latches a
	// transport error on failure. Non-positive values disable the guard.
	KeepaliveInterval time.Duration

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
