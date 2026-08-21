package netconf

import "go.aledante.io/FlowSeer/src/common/errs"

// Error codes are the netconf package's wire contract: append-only,
// never renamed, never reused.
var (
	// ErrCodeRPC marks a device <rpc-error>. The error carries the
	// device's error-tag, severity, path, and message as attributes.
	ErrCodeRPC = errs.NewCode("netconf/rpc")
	// ErrCodeLockDenied marks a datastore lock held by another
	// session. Retryable: the holder usually releases it.
	ErrCodeLockDenied = errs.NewCode("netconf/lock-denied")
	// ErrCodeUnsupported marks an operation the peer's capability set
	// cannot serve (no writable datastore, no candidate, no
	// validate).
	ErrCodeUnsupported = errs.NewCode("netconf/unsupported")
	// ErrCodeSessionClosed marks use of a closed session.
	ErrCodeSessionClosed = errs.NewCode("netconf/session-closed")
	// ErrCodeTransport marks a transport-level failure (dial, dead
	// peer, keepalive trip).
	ErrCodeTransport = errs.NewCode("netconf/transport")
)

// ErrSessionClosed is the sentinel every method returns after
// [Session.Close]; distinguish with errors.Is.
var ErrSessionClosed = errs.New().Code(ErrCodeSessionClosed).Msg("netconf session is closed")
