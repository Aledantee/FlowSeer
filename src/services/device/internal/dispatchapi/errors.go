package dispatchapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// ClientErrors is what a reporting or subscribing edge learns from each
// failure this service returns, including the journal codes it passes through.
// A journal conflict is the edge's to retry; a store failure is not, but the
// edge retries anyway because the report is what it owes central, so both are
// Unavailable.
// It is exported so the cross-service consistency check can read it: one
// code must not answer two different things depending which handler a caller
// reached.
var ClientErrors = connecterr.Table{
	ErrCodeEdge:      {Code: connect.CodeUnauthenticated, UserMsg: "the call is not authenticated"},
	ErrCodeForbidden: {Code: connect.CodePermissionDenied, UserMsg: "the request was refused"},
	ErrCodeReport:    {Code: connect.CodeInvalidArgument, UserMsg: "the report carries no arm central can apply"},

	ErrCodeResolve:          {Code: connect.CodeUnavailable, UserMsg: "the edge's devices cannot be resolved right now"},
	journal.ErrCodeConflict: {Code: connect.CodeUnavailable, UserMsg: "the device's record is being written concurrently; retry"},
	journal.ErrCodeStore:    {Code: connect.CodeUnavailable, UserMsg: "the device's record cannot be reached right now"},

	journal.ErrCodeState:     {Code: connect.CodeFailedPrecondition, UserMsg: "the device's record does not admit this report"},
	journal.ErrCodeHoldsFull: {Code: connect.CodeResourceExhausted, UserMsg: "the device holds as many unresolved mutations as it can"},

	ErrCodeReadDeadline:   {Code: connect.CodeInternal},
	journal.ErrCodeDecode: {Code: connect.CodeInternal},
}

// connectErr gives a failure the Connect code the edge branches on and the
// only text it may see: this stream and this handler answer a caller whose
// assertion the middleware verified, not one central trusts with its own
// transports and paths. An unmapped code is Internal.
func connectErr(err error) error {
	return ClientErrors.Wrap(err)
}
