package captureapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// ClientErrors is what a caller learns from each failure these handlers
// return.
//
// Both routers this package serves need it, for opposite reasons. The operator
// surface carries no authorization check, so a caller that never enrolled can
// reach every handler here; the edge surface answers a party that is
// authenticated but not trusted with central's insides. Neither may be told
// that a path under the state directory could not be opened, or which
// JetStream operation refused.
//
// It is exported so the cross-service consistency check can read it: one code
// must not answer two different things depending which handler a caller
// reached.
var ClientErrors = connecterr.Table{
	ErrCodeBadSession: {Code: connect.CodeInvalidArgument, UserMsg: "the request does not name a capture session"},

	ErrCodeNotFound:         {Code: connect.CodeNotFound, UserMsg: "no such capture session"},
	ErrCodeArtifactNotFound: {Code: connect.CodeNotFound, UserMsg: "this session's capture is no longer stored"},

	ErrCodeConflict: {Code: connect.CodeAborted, UserMsg: "this capture session is being written concurrently; retry"},

	ErrCodeStore:  {Code: connect.CodeUnavailable, UserMsg: "the capture session's record cannot be reached right now"},
	ErrCodeDecode: {Code: connect.CodeInternal},
}

// connectErr gives a failure the Connect code a caller branches on and the
// only text they may see. An unmapped code is Internal.
func connectErr(err error) error {
	return ClientErrors.Wrap(err)
}
