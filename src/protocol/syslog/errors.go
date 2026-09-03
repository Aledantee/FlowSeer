package syslog

import "go.aledante.io/FlowSeer/src/common/errs"

var (
	// ErrLimit identifies a configured resource limit, not a malformed payload.
	ErrLimit = errs.Msg("syslog resource limit")
	// ErrBusy means another operation owns the parser, receiver consumer, or sender.
	ErrBusy = errs.Msg("syslog operation already active")
	// ErrClosed means the receiver or sender has been closed.
	ErrClosed = errs.Msg("syslog closed")
	// ErrFraming means a stream no longer has trustworthy message boundaries.
	ErrFraming = errs.Msg("invalid syslog framing")
	// ErrUnrepresentable means encoding would lose unapproved data or create invalid output.
	ErrUnrepresentable = errs.Msg("syslog record cannot be represented")
)
