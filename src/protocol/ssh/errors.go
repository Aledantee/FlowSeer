package ssh

import "go.aledante.io/FlowSeer/src/common/errs"

// Error codes are the ssh package's wire contract: append-only, never
// renamed, never reused.
var (
	// ErrCodeTransport marks a transport-level failure: dial, SSH
	// handshake, or host-key verification.
	ErrCodeTransport = errs.NewCode("ssh/transport")
	// ErrCodeShell marks a failure opening or starting the one
	// interactive shell channel.
	ErrCodeShell = errs.NewCode("ssh/shell")
	// ErrCodeSessionClosed marks use of a closed session.
	ErrCodeSessionClosed = errs.NewCode("ssh/session-closed")
	// ErrCodeConnectionLost marks the peer closing the connection
	// while a command was in flight.
	ErrCodeConnectionLost = errs.NewCode("ssh/connection-lost")
)

// ErrSessionClosed marks a Run attempted after [Session.Close], once
// local validation passes; distinguish with errors.Is.
var ErrSessionClosed = errs.New().Code(ErrCodeSessionClosed).Msg("ssh session is closed")
