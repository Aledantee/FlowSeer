package gnmi

import "go.aledante.io/FlowSeer/src/common/errs"

// Error codes are the gnmi package's wire contract: append-only,
// never renamed, never reused.
var (
	// ErrCodeTransport marks dial and channel failures.
	ErrCodeTransport = errs.NewCode("gnmi/transport")
	// ErrCodeRPC marks a device-rejected RPC; the gRPC status code
	// and, for Set, the failing path ride along as attributes.
	ErrCodeRPC = errs.NewCode("gnmi/rpc")
	// ErrCodeEncoding marks a peer that supports none of the
	// encodings this library speaks, or a payload that does not
	// decode under the negotiated encoding.
	ErrCodeEncoding = errs.NewCode("gnmi/encoding")
	// ErrCodeSessionClosed marks use of a closed session.
	ErrCodeSessionClosed = errs.NewCode("gnmi/session-closed")
)

// ErrSessionClosed is the sentinel every method returns after
// [Session.Close]; distinguish with errors.Is.
var ErrSessionClosed = errs.New().Code(ErrCodeSessionClosed).Msg("gnmi session is closed")
