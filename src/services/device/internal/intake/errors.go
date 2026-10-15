package intake

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// Error codes the delivery handler returns.
var (
	// ErrCodeEdge is a delivery whose caller could not be identified as an
	// edge.
	ErrCodeEdge = errs.NewCode("intake/edge")
	// ErrCodeResolve is a failure to resolve the calling edge's tenant.
	ErrCodeResolve = errs.NewCode("intake/resolve")
)

// ClientErrors is what a delivering edge learns from each failure the delivery
// handler returns. A tenant lookup failure is Unavailable because the edge's
// answer to it is to keep its log and deliver again. It is exported so the
// cross-service consistency check can read it.
var ClientErrors = connecterr.Table{
	ErrCodeEdge:    {Code: connect.CodeUnauthenticated, UserMsg: "the call is not authenticated"},
	ErrCodeResolve: {Code: connect.CodeUnavailable, UserMsg: "the edge's tenant cannot be resolved right now"},
}

// connectErr gives a failure the Connect code the delivering edge branches on
// and the only text it may see. An unmapped code is Internal.
func connectErr(err error) error {
	return ClientErrors.Wrap(err)
}
