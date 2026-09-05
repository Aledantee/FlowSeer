package credential

import (
	"context"

	connect "connectrpc.com/connect"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeStream identifies a malformed or prematurely closed submission
// stream: one that ends before delivering a grant, or whose first message
// is not a grant.
var ErrCodeStream = errs.NewCode("credential/stream")

// ConnectAdapter satisfies [ReadCredentialSource] and
// [SubmissionCredentialSource] against a real
// edgev1connect.EdgeServiceClient. Production wiring constructs one;
// tests construct a fake of the two interfaces directly instead.
type ConnectAdapter struct {
	Client edgev1connect.EdgeServiceClient
}

// AcquireReadCredential implements [ReadCredentialSource].
func (a *ConnectAdapter) AcquireReadCredential(ctx context.Context, deviceID, bindingID string, accessPolicy *policyv1.AccessPolicyHandle) (*edgev1.AcquireReadCredentialResponse, error) {
	req := &edgev1.AcquireReadCredentialRequest{}
	req.SetDeviceId(deviceID)
	req.SetBindingId(bindingID)
	req.SetAccessPolicy(accessPolicy)

	resp, err := a.Client.AcquireReadCredential(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, errs.Wrap(err, "acquire read credential")
	}

	return resp.Msg, nil
}

// Open implements [SubmissionCredentialSource]. It reads the stream's first
// message synchronously, since the mutation state machine cannot proceed
// without the grant, and relays every later message to updates from a
// background goroutine that exits when the stream ends or ctx is done.
func (a *ConnectAdapter) Open(ctx context.Context, deviceID, bindingID string, sequence uint64) (*edgev1.SubmissionGrant, <-chan SubmissionUpdate, error) {
	req := &edgev1.OpenDeviceSubmissionRequest{}
	req.SetDeviceId(deviceID)
	req.SetBindingId(bindingID)
	req.SetSequence(sequence)

	stream, err := a.Client.OpenDeviceSubmission(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, nil, errs.Wrap(err, "open device submission")
	}

	if !stream.Receive() {
		streamErr := stream.Err()
		if streamErr == nil {
			streamErr = errs.New().Code(ErrCodeStream).Msg("submission stream closed before delivering a grant")
		}

		return nil, nil, errs.Wrap(streamErr, "open device submission")
	}

	grant := stream.Msg().GetGrant()
	if grant == nil {
		return nil, nil, errs.New().Code(ErrCodeStream).Msg("submission stream's first message was not a grant")
	}

	updates := make(chan SubmissionUpdate)

	go relaySubmissionUpdates(ctx, stream, updates)

	return grant, updates, nil
}

// submissionStream is the subset of *connect.ServerStreamForClient
// [Open] relays; naming it lets connect_adapter_test.go exercise the relay
// goroutine against a fake stream.
type submissionStream interface {
	Receive() bool
	Msg() *edgev1.OpenDeviceSubmissionResponse
	Err() error
	Close() error
}

func relaySubmissionUpdates(ctx context.Context, stream submissionStream, updates chan<- SubmissionUpdate) {
	defer close(updates)
	defer stream.Close()

	for stream.Receive() {
		msg := stream.Msg()

		var u SubmissionUpdate

		switch {
		case msg.GetPulse() != nil:
			u.Pulse = msg.GetPulse()
		case msg.GetGrant() != nil:
			u.Grant = msg.GetGrant()
		default:
			continue
		}

		select {
		case updates <- u:
		case <-ctx.Done():
			return
		}
	}
}
