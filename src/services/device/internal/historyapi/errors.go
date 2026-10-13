package historyapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/historyquery"
)

// ErrCodeBadRequest is a request the handler cannot turn into a query although
// it passed its schema rules.
var ErrCodeBadRequest = errs.NewCode("historyapi/bad-request")

// ClientErrors is what a caller learns from each failure the handler returns.
// It is exported so the cross-service consistency check can read it.
//
// A reader limit is ResourceExhausted, not Unavailable: the same read fails the
// same way until the window or the scope shrinks, and a retry would only repeat
// the work. An unreachable store is Unavailable, which a client may retry.
var ClientErrors = connecterr.Table{
	ErrCodeBadRequest:            {Code: connect.CodeInvalidArgument, UserMsg: "the request does not name a device or location"},
	historyquery.ErrCodeBadToken: {Code: connect.CodeInvalidArgument, UserMsg: "the page token is not valid for this request"},
	historyquery.ErrCodeLimit:    {Code: connect.CodeResourceExhausted, UserMsg: "the read matches more data than one call may read"},
	historyquery.ErrCodeStore:    {Code: connect.CodeUnavailable, UserMsg: "the history store cannot be reached right now"},
}

// connectErr gives a failure the Connect code a caller branches on and the only
// text they may see. An unmapped code, including a read that reached the handler
// with no admitted tenant, is Internal.
func connectErr(err error) error {
	return ClientErrors.Wrap(err)
}
