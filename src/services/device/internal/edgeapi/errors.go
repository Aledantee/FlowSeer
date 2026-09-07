package edgeapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

// msgUnauthenticated is what every call that reached a handler without a
// verified edge answers. The middleware has already refused the ways an
// assertion can fail; a handler reaching this has no assertion at all, and the
// caller learns only that.
const msgUnauthenticated = "the call is not authenticated"

// clientErrors is what an edge or an operator learns from each failure these
// services return, including the store codes they pass through unchanged.
//
// A refused setup key and a device the calling edge does not host are both
// PermissionDenied and carry the same sentence, because the caller must not
// learn from the answer which of the several ways it failed. Everything the
// caller can do nothing about leaves the message empty, which reports the
// generic string rather than naming a bucket, a transport, or a file.
var clientErrors = connecterr.Table{
	ErrCodeRequest:   {Code: connect.CodeInvalidArgument, UserMsg: "the request is not well-formed"},
	ErrCodePageToken: {Code: connect.CodeInvalidArgument, UserMsg: "the page token did not come from this service"},
	ErrCodeKeyProof:  {Code: connect.CodeInvalidArgument, UserMsg: "the key proof does not verify for this call"},

	ErrCodeNotFound: {Code: connect.CodeNotFound, UserMsg: "no such edge"},

	ErrCodeLifecycle:          {Code: connect.CodeFailedPrecondition, UserMsg: "the edge's lifecycle does not allow this call"},
	ErrCodeSetupKey:           {Code: connect.CodeFailedPrecondition, UserMsg: "the edge has no such outstanding setup key"},
	ErrCodePolicy:             {Code: connect.CodeFailedPrecondition, UserMsg: "the device's access policy does not resolve for this call"},
	ErrCodeNotCheckpointed:    {Code: connect.CodeFailedPrecondition, UserMsg: "the device holds no checkpointed mutation at that sequence"},
	ErrCodeAuthorityWithdrawn: {Code: connect.CodeFailedPrecondition, UserMsg: "submission authority for this mutation has been withdrawn"},
	ErrCodeGrantExpired:       {Code: connect.CodeFailedPrecondition, UserMsg: "the mutation's horizon has passed"},

	ErrCodeSetupKeyRefused: {Code: connect.CodePermissionDenied, UserMsg: "the request was refused"},
	ErrCodeForbidden:       {Code: connect.CodePermissionDenied, UserMsg: "the request was refused"},

	ErrCodeBus:              {Code: connect.CodeUnavailable, UserMsg: "the edge's bus identity cannot be issued right now"},
	ErrCodeAuthorityUnknown: {Code: connect.CodeUnavailable, UserMsg: "submission authority cannot be determined right now"},
	edgestore.ErrCodeStore:  {Code: connect.CodeUnavailable, UserMsg: "the edge store cannot be reached right now"},
	edgestore.ErrCodeConflict: {
		Code:    connect.CodeUnavailable,
		UserMsg: "the edge record is being written concurrently; retry",
	},

	ErrCodeNoEdge:           {Code: connect.CodeUnauthenticated, UserMsg: msgUnauthenticated},
	ErrCodeConfig:           {Code: connect.CodeInternal},
	ErrCodeRandom:           {Code: connect.CodeInternal},
	ErrCodeCredential:       {Code: connect.CodeInternal},
	edgestore.ErrCodeDecode: {Code: connect.CodeInternal},
	edgestore.ErrCodeState:  {Code: connect.CodeInternal},
}

// connectErr gives a failure the Connect code its caller branches on and the
// only text that caller may see. An unmapped code is Internal, so a new
// failure is never quietly mistaken for a request the caller can fix.
func connectErr(err error) error {
	return clientErrors.Wrap(err)
}

// unauthenticated answers a handler reached without a verified edge.
func unauthenticated(err error) error {
	return connecterr.WrapAs(connect.CodeUnauthenticated, msgUnauthenticated, err)
}
