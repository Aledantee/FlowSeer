package credential

import (
	"context"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
)

// ReadCredentialSource wraps EdgeService.AcquireReadCredential: a fresh
// read credential for one device under one access-policy version. There is
// no standing lease, so a caller acquires one for each read rather than
// caching it beyond that read.
type ReadCredentialSource interface {
	AcquireReadCredential(ctx context.Context, deviceID, bindingID string, accessPolicy *policyv1.AccessPolicyHandle) (*edgev1.AcquireReadCredentialResponse, error)
}

// SubmissionHandle is what one open submission stream hands its caller: a
// synchronous snapshot of the one-use grant and the current submission
// authority, rather than a channel a caller must race to drain. Requirement
// 8 asks for a positive AUTHORIZED check immediately before every command;
// a channel-based design that treats "no pulse queued right now" as
// authorization can miss a pulse that already arrived over the wire but has
// not yet reached a consumer that happened not to be listening at that
// instant (an unbuffered channel with a non-blocking reader drops exactly
// that message). A synchronous snapshot updated by the transport as soon as
// it reads a pulse off the wire — not gated on a consumer being ready to
// receive it — closes that gap.
type SubmissionHandle interface {
	// Grant returns the one-use submission credential delivered when the
	// stream opened.
	Grant() *edgev1.SubmissionGrant
	// Authority returns the most recently observed authority. Before any
	// pulse arrives it is SUBMISSION_AUTHORITY_AUTHORIZED — the grant's own
	// issuance implies authorization until told otherwise. A caller must
	// check this immediately before submitting each command and proceed
	// only when it is exactly SUBMISSION_AUTHORITY_AUTHORIZED.
	Authority() edgev1.SubmissionAuthority
	// Err returns why the underlying stream ended, once it has (nil until
	// then, and nil for a stream that is still open). A caller must not
	// treat a stream that ended for any reason other than an explicit
	// revocation as still authorized — check Err() alongside Authority()
	// rather than inferring a clean close from an unchanged Authority().
	Err() error
	// Close releases the underlying stream. Safe to call more than once.
	Close() error
}

// SubmissionCredentialSource wraps EdgeService.OpenDeviceSubmission.
type SubmissionCredentialSource interface {
	Open(ctx context.Context, deviceID, bindingID string, sequence uint64) (SubmissionHandle, error)
}
