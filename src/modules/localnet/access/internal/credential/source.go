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

// SubmissionUpdate is one message from an open submission stream: either
// the one-use grant (always first) or a later authority pulse. Exactly one
// field is set, mirroring OpenDeviceSubmissionResponse's oneof.
type SubmissionUpdate struct {
	Grant *edgev1.SubmissionGrant
	Pulse *edgev1.AuthorityPulse
}

// SubmissionCredentialSource wraps EdgeService.OpenDeviceSubmission. Open
// returns the stream's first message eagerly (production always sends the
// grant first) and a channel of every message after it, including the
// grant read to build the first value — a caller that wants every message
// uniformly can also read updates from the start; both are provided
// because the mutation state machine needs the grant to make progress but
// the recovery package only cares about later pulses. The channel closes
// when the stream ends; err reports why if it ended abnormally, checked
// after the channel closes.
type SubmissionCredentialSource interface {
	Open(ctx context.Context, deviceID, bindingID string, sequence uint64) (grant *edgev1.SubmissionGrant, updates <-chan SubmissionUpdate, err error)
}
