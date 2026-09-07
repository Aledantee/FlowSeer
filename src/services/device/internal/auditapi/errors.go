package auditapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// clientErrors is what a delivering edge learns from each failure this handler
// returns. A publish failure is Unavailable because the edge's answer to it is
// to hold the record and deliver again, which is what the stream's contract
// requires of it.
var clientErrors = connecterr.Table{
	ErrCodeEdge:      {Code: connect.CodeUnauthenticated, UserMsg: "the call is not authenticated"},
	ErrCodeForbidden: {Code: connect.CodePermissionDenied, UserMsg: "the request was refused"},
	ErrCodeResolve:   {Code: connect.CodeUnavailable, UserMsg: "the edge-device binding cannot be resolved right now"},
	ErrCodePublish:   {Code: connect.CodeUnavailable, UserMsg: "the audit record was not stored; deliver it again"},
}

// connectErr gives a failure the Connect code the delivering edge branches on
// and the only text it may see. An unmapped code is Internal.
func connectErr(err error) error {
	return clientErrors.Wrap(err)
}
