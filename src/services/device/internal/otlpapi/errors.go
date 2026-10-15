package otlpapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// Error codes the telemetry handler returns.
var (
	// ErrCodeEdge is a delivery whose caller could not be identified as an
	// edge.
	ErrCodeEdge = errs.NewCode("otlpapi/edge")
	// ErrCodeStart is a failure to build the handler.
	ErrCodeStart = errs.NewCode("otlpapi/start")
)

// ClientErrors is what a delivering edge learns from each failure the handler
// returns. A collector that cannot take a body is not a failure of the call:
// the call answers with the count settled and the edge resends the rest.
// ErrCodeStart is absent because a handler that failed to build serves no
// call. The table is exported so the cross-service consistency check can read
// it.
var ClientErrors = connecterr.Table{
	ErrCodeEdge: {Code: connect.CodeUnauthenticated, UserMsg: "the call is not authenticated"},
}

// connectErr gives a failure the Connect code the delivering edge branches on
// and the only text it may see. An unmapped code is Internal.
func connectErr(err error) error {
	return ClientErrors.Wrap(err)
}
