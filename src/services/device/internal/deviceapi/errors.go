package deviceapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
)

// ClientErrors is what an operator learns from each failure these handlers
// return, including the journal and registry codes they pass through.
//
// An operator is a caller who can act on the answer, so these messages say
// what to do differently — which is not a license to describe central's
// insides. A journal conflict names contention and asks for a retry; it does
// not name the bucket.
//
// It is exported so the cross-service consistency check can read it: one code
// must not answer two different things depending which handler a caller
// reached.
var ClientErrors = connecterr.Table{
	ErrCodeRequest:       {Code: connect.CodeInvalidArgument, UserMsg: "the request is not one this service can act on"},
	ErrCodeUnknownDevice: {Code: connect.CodeNotFound, UserMsg: "no such device"},

	ErrCodePolicy:                      {Code: connect.CodeFailedPrecondition, UserMsg: "the intent names an access policy version the device does not pin"},
	ErrCodeFirmwareEpoch:               {Code: connect.CodeFailedPrecondition, UserMsg: "the device reports a firmware epoch other than the one the intent expects"},
	ErrCodeLaneHeld:                    {Code: connect.CodeFailedPrecondition, UserMsg: "a mutation still holds this device's lane"},
	ErrCodeNoExpectation:               {Code: connect.CodeFailedPrecondition, UserMsg: "there is nothing recorded for this interface to resolve against"},
	registry.ErrCodeHorizonUnset:       {Code: connect.CodeFailedPrecondition, UserMsg: "the device has no measured delayed-apply horizon, so no change can be bounded"},
	journal.ErrCodeIdempotencyMismatch: {Code: connect.CodeAlreadyExists, UserMsg: "this idempotency key was already used for a different request"},
	journal.ErrCodeState:               {Code: connect.CodeFailedPrecondition, UserMsg: "the device's record does not allow this operation"},

	journal.ErrCodeHoldsFull: {Code: connect.CodeResourceExhausted, UserMsg: "the device holds as many unresolved mutations as it can; some must be acknowledged before more are recorded"},

	ErrCodeReadFailed:  {Code: connect.CodeUnavailable, UserMsg: "the interface could not be read"},
	ErrCodeReadTimeout: {Code: connect.CodeDeadlineExceeded, UserMsg: "the read did not answer in time; it is still running and its result will appear in the device's status"},

	journal.ErrCodeConflict: {Code: connect.CodeUnavailable, UserMsg: "the device's record is being written concurrently; retry"},
	journal.ErrCodeStore:    {Code: connect.CodeUnavailable, UserMsg: "the device's record cannot be reached right now"},

	journal.ErrCodeDecode:   {Code: connect.CodeInternal},
	registry.ErrCodeLoad:    {Code: connect.CodeInternal},
	registry.ErrCodeInvalid: {Code: connect.CodeInternal},
}

// connectErr gives a failure the Connect code an operator branches on and the
// only text they may see. An unmapped code is Internal.
func connectErr(err error) error {
	return ClientErrors.Wrap(err)
}
