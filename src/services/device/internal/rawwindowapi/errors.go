package rawwindowapi

import (
	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// Error codes returned by [Store] and the two services.
var (
	// ErrCodeStore is a key-value read or write that failed.
	ErrCodeStore = errs.NewCode("rawwindowapi/store")
	// ErrCodeDecode is a stored record that does not decode.
	ErrCodeDecode = errs.NewCode("rawwindowapi/decode")
	// ErrCodeConflict is a compare-and-swap that did not settle within its
	// retry bound.
	ErrCodeConflict = errs.NewCode("rawwindowapi/conflict")
	// ErrCodeBadKey is a tenant or edge identifier that cannot name a record.
	ErrCodeBadKey = errs.NewCode("rawwindowapi/bad-key")
	// ErrCodeNotFound is a window the tenant does not have.
	ErrCodeNotFound = errs.NewCode("rawwindowapi/not-found")

	// ErrCodeNotSubscribed is an open for an edge that never subscribed.
	ErrCodeNotSubscribed = errs.NewCode("rawwindowapi/not-subscribed")
	// ErrCodeStaleFence is a subscribe or report from a process the stored
	// generation and epoch no longer accept.
	ErrCodeStaleFence = errs.NewCode("rawwindowapi/stale-fence")
	// ErrCodeSuperseded is an open stream whose process a newer subscription
	// replaced.
	ErrCodeSuperseded = errs.NewCode("rawwindowapi/superseded")

	// ErrCodeUnsupportedSource is a source selector naming a source the
	// subscribed process does not run.
	ErrCodeUnsupportedSource = errs.NewCode("rawwindowapi/unsupported-source")
	// ErrCodeUnknownSelector is a device or integration the edge does not have.
	ErrCodeUnknownSelector = errs.NewCode("rawwindowapi/unknown-selector")
	// ErrCodeBadExpiry is an expiry that is not in the future or lies more than
	// one hour ahead.
	ErrCodeBadExpiry = errs.NewCode("rawwindowapi/bad-expiry")
	// ErrCodeNoAllowance is an open on a deployment that configures no byte
	// allowance.
	ErrCodeNoAllowance = errs.NewCode("rawwindowapi/no-allowance")
	// ErrCodeWindowLimit is an open past the live window limit.
	ErrCodeWindowLimit = errs.NewCode("rawwindowapi/window-limit")
	// ErrCodeBudgetExceeded is an open whose budget does not fit the edge
	// allowance beside the reservations already held.
	ErrCodeBudgetExceeded = errs.NewCode("rawwindowapi/budget-exceeded")
	// ErrCodeRecordFull is an open whose record would exceed its serialized
	// bound.
	ErrCodeRecordFull = errs.NewCode("rawwindowapi/record-full")

	// ErrCodeForeignWindow is a report naming a window of another edge.
	ErrCodeForeignWindow = errs.NewCode("rawwindowapi/foreign-window")
	// ErrCodeBadReport is a report the window's state cannot have produced.
	ErrCodeBadReport = errs.NewCode("rawwindowapi/bad-report")

	// ErrCodeFullPayloadExpired is an open by an operator whose full-payload
	// grant is not active, even when its authorization tuple has not yet been
	// reconciled away.
	ErrCodeFullPayloadExpired = errs.NewCode("rawwindowapi/full-payload-expired")
)

// ClientErrors is what a caller learns from each failure these handlers
// return. It is exported so the cross-service consistency check can read it.
//
// A stale fence is FailedPrecondition: the same request cannot succeed until
// the edge starts a process with a greater boot generation. A superseded
// stream is Aborted, the code for a stream a newer one displaced, which tells a
// client to resubscribe rather than give up.
var ClientErrors = connecterr.Table{
	ErrCodeBadKey:   {Code: connect.CodeInvalidArgument, UserMsg: "the request does not name a raw window"},
	ErrCodeNotFound: {Code: connect.CodeNotFound, UserMsg: "no such raw window"},

	ErrCodeNotSubscribed: {Code: connect.CodeFailedPrecondition, UserMsg: "the edge has not subscribed to raw window controls"},
	ErrCodeStaleFence:    {Code: connect.CodeFailedPrecondition, UserMsg: "a newer edge process owns the raw window controls"},
	ErrCodeSuperseded:    {Code: connect.CodeAborted, UserMsg: "a newer subscription replaced this one"},

	ErrCodeUnsupportedSource: {Code: connect.CodeInvalidArgument, UserMsg: "the edge does not run that ingestion source"},
	ErrCodeUnknownSelector:   {Code: connect.CodeNotFound, UserMsg: "the edge has no such device or integration"},
	ErrCodeBadExpiry:         {Code: connect.CodeInvalidArgument, UserMsg: "the expiry must be in the future and at most one hour from now"},
	ErrCodeNoAllowance:       {Code: connect.CodeFailedPrecondition, UserMsg: "this deployment configures no raw window byte allowance"},
	ErrCodeWindowLimit:       {Code: connect.CodeResourceExhausted, UserMsg: "the edge already holds the most raw windows it may"},
	ErrCodeBudgetExceeded:    {Code: connect.CodeResourceExhausted, UserMsg: "the byte budget exceeds what the edge allowance has left"},
	ErrCodeRecordFull:        {Code: connect.CodeResourceExhausted, UserMsg: "the edge's raw window record is full"},

	ErrCodeForeignWindow: {Code: connect.CodePermissionDenied, UserMsg: "the report names a window of another edge"},
	ErrCodeBadReport:     {Code: connect.CodeInvalidArgument, UserMsg: "the report does not match the window's state"},

	ErrCodeConflict: {Code: connect.CodeAborted, UserMsg: "the edge's raw window record is being written concurrently; retry"},
	ErrCodeStore:    {Code: connect.CodeUnavailable, UserMsg: "the raw window record cannot be reached right now"},
	ErrCodeDecode:   {Code: connect.CodeInternal},
}

// connectErr gives a failure the Connect code a caller branches on and the
// only text they may see. An unmapped code is Internal.
func connectErr(err error) error {
	return ClientErrors.Wrap(err)
}
