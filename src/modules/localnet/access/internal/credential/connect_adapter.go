package credential

import (
	"context"
	"sync"

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
// without the grant, and starts a background goroutine that keeps draining
// the stream and updating the returned handle's authority snapshot for as
// long as the stream stays open — independent of whether anything is
// calling Authority() at any given moment, so a pulse is reflected the
// instant this goroutine reads it off the wire, never only when a consumer
// happens to be receiving from a channel.
func (a *ConnectAdapter) Open(ctx context.Context, deviceID, bindingID string, sequence uint64) (SubmissionHandle, error) {
	req := &edgev1.OpenDeviceSubmissionRequest{}
	req.SetDeviceId(deviceID)
	req.SetBindingId(bindingID)
	req.SetSequence(sequence)

	stream, err := a.Client.OpenDeviceSubmission(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, errs.Wrap(err, "open device submission")
	}

	if !stream.Receive() {
		streamErr := stream.Err()
		if streamErr == nil {
			streamErr = errs.New().Code(ErrCodeStream).Msg("submission stream closed before delivering a grant")
		}

		return nil, errs.Wrap(streamErr, "open device submission")
	}

	grant := stream.Msg().GetGrant()
	if grant == nil {
		return nil, errs.New().Code(ErrCodeStream).Msg("submission stream's first message was not a grant")
	}

	h := &submissionHandle{
		grant:     grant,
		authority: edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED,
		stream:    stream,
	}
	go h.relay(ctx, stream)

	return h, nil
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

// submissionHandle implements [SubmissionHandle]. grant and stream are
// immutable after construction — stream is set in [ConnectAdapter.Open]
// itself, before the relay goroutine starts, so Close can never race the
// goroutine to see a nil stream and silently close nothing; authority and
// err are updated by relay under mu.
type submissionHandle struct {
	grant *edgev1.SubmissionGrant

	mu        sync.Mutex
	authority edgev1.SubmissionAuthority
	err       error
	stream    submissionStream
}

func (h *submissionHandle) Grant() *edgev1.SubmissionGrant { return h.grant }

func (h *submissionHandle) Authority() edgev1.SubmissionAuthority {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authority
}

func (h *submissionHandle) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *submissionHandle) Close() error {
	h.mu.Lock()
	stream := h.stream
	h.mu.Unlock()
	if stream == nil {
		return nil
	}
	return stream.Close()
}

// relay keeps draining stream for as long as it stays open, updating h's
// authority under h.mu the instant each pulse is read — not when some
// consumer next calls Authority() — and records why the stream ended
// (including a nil-but-not-EOF close, which the caller must not treat as
// "still authorized") before the handle reports it via Err().
func (h *submissionHandle) relay(ctx context.Context, stream submissionStream) {
	for stream.Receive() {
		pulse := stream.Msg().GetPulse()
		if pulse == nil {
			continue
		}

		h.mu.Lock()
		h.authority = pulse.GetAuthority()
		h.mu.Unlock()

		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.err = ctx.Err()
			h.mu.Unlock()
			return
		default:
		}
	}

	streamErr := stream.Err()
	if streamErr == nil {
		streamErr = errs.New().Code(ErrCodeStream).Msg("submission stream closed")
	}
	h.mu.Lock()
	h.err = streamErr
	h.mu.Unlock()
}
