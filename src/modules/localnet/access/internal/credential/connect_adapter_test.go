package credential_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/credential"
)

// stubEdgeService serves OpenDeviceSubmission and AcquireReadCredential
// with fixture responses and returns Unimplemented for every other RPC, so
// the adapter test exercises a real Connect stream rather than a fake of
// the adapter's own interfaces.
type stubEdgeService struct {
	edgev1connect.UnimplementedEdgeServiceHandler

	submissionUpdates []func() (*edgev1.OpenDeviceSubmissionResponse, error)
	acquireResponse   *edgev1.AcquireReadCredentialResponse
	acquireErr        error
}

func (s *stubEdgeService) AcquireReadCredential(_ context.Context, _ *connect.Request[edgev1.AcquireReadCredentialRequest]) (*connect.Response[edgev1.AcquireReadCredentialResponse], error) {
	if s.acquireErr != nil {
		return nil, s.acquireErr
	}

	return connect.NewResponse(s.acquireResponse), nil
}

func (s *stubEdgeService) OpenDeviceSubmission(_ context.Context, _ *connect.Request[edgev1.OpenDeviceSubmissionRequest], stream *connect.ServerStream[edgev1.OpenDeviceSubmissionResponse]) error {
	for _, next := range s.submissionUpdates {
		msg, err := next()
		if err != nil {
			return err
		}
		if err := stream.Send(msg); err != nil {
			return err
		}
	}

	return nil
}

func grantMessage(grant *edgev1.SubmissionGrant) *edgev1.OpenDeviceSubmissionResponse {
	msg := &edgev1.OpenDeviceSubmissionResponse{}
	msg.SetGrant(grant)

	return msg
}

func pulseMessage(pulse *edgev1.AuthorityPulse) *edgev1.OpenDeviceSubmissionResponse {
	msg := &edgev1.OpenDeviceSubmissionResponse{}
	msg.SetPulse(pulse)

	return msg
}

func newTestGrant() *edgev1.SubmissionGrant {
	cred := &edgev1.DeviceCredential{}
	cred.SetTypedMaterial(shellMaterial("material"))

	grant := &edgev1.SubmissionGrant{}
	grant.SetCredential(cred)

	return grant
}

func newTestServer(t *testing.T, svc *stubEdgeService) *httptest.Server {
	t.Helper()
	path, handler := edgev1connect.NewEdgeServiceHandler(svc)
	mux := httptest.NewServer(handler)
	_ = path
	t.Cleanup(mux.Close)

	return mux
}

func TestConnectAdapterOpenTranslatesFirstMessageToGrantAndRestToPulses(t *testing.T) {
	grant := newTestGrant()
	// AUTHORIZED is the handle's own starting value (Open sets it before
	// the relay goroutine reads anything), so a REVOKED pulse is the only
	// one that proves the relay goroutine actually wrote what it read: the
	// test would pass unchanged against a relay that never touched
	// h.authority at all if this pulse stayed AUTHORIZED.
	pulse := &edgev1.AuthorityPulse{}
	pulse.SetAuthority(edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED)

	svc := &stubEdgeService{
		submissionUpdates: []func() (*edgev1.OpenDeviceSubmissionResponse, error){
			func() (*edgev1.OpenDeviceSubmissionResponse, error) { return grantMessage(grant), nil },
			func() (*edgev1.OpenDeviceSubmissionResponse, error) { return pulseMessage(pulse), nil },
		},
	}
	server := newTestServer(t, svc)

	adapter := &credential.ConnectAdapter{Client: edgev1connect.NewEdgeServiceClient(server.Client(), server.URL)}

	handle, err := adapter.Open(context.Background(), "device-1", "binding-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if got := handle.Grant().GetCredential().GetTypedMaterial().GetShell().GetUsername(); got != "material" {
		t.Fatalf("expected the grant's credential material to round-trip, got %q", got)
	}

	// Authority() is a synchronous snapshot the relay goroutine updates as
	// soon as it reads a pulse off the wire, independent of whether
	// anything is polling at that instant — poll with a deadline rather
	// than blocking on a channel receive.
	deadline := time.Now().Add(5 * time.Second)
	for handle.Authority() != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Authority() to reflect the REVOKED pulse, got %v", handle.Authority())
		}
		time.Sleep(time.Millisecond)
	}

	deadline = time.Now().Add(5 * time.Second)
	for handle.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Err() to report the stream ending")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConnectAdapterOpenErrorsWhenStreamClosesBeforeGrant(t *testing.T) {
	svc := &stubEdgeService{submissionUpdates: nil}
	server := newTestServer(t, svc)

	adapter := &credential.ConnectAdapter{Client: edgev1connect.NewEdgeServiceClient(server.Client(), server.URL)}

	_, err := adapter.Open(context.Background(), "device-1", "binding-1", 1)
	if err == nil {
		t.Fatal("expected an error when the stream closes before a grant")
	}
}

func TestConnectAdapterOpenStopsRelayOnContextCancellation(t *testing.T) {
	grant := newTestGrant()
	pulse := &edgev1.AuthorityPulse{}
	pulse.SetAuthority(edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED)

	// Enough pulses that the relay goroutine is still sending when the
	// context is canceled, proving the goroutine exits instead of leaking.
	updates := make([]func() (*edgev1.OpenDeviceSubmissionResponse, error), 0, 101)
	updates = append(updates, func() (*edgev1.OpenDeviceSubmissionResponse, error) { return grantMessage(grant), nil })
	for range 100 {
		updates = append(updates, func() (*edgev1.OpenDeviceSubmissionResponse, error) { return pulseMessage(pulse), nil })
	}

	svc := &stubEdgeService{submissionUpdates: updates}
	server := newTestServer(t, svc)

	adapter := &credential.ConnectAdapter{Client: edgev1connect.NewEdgeServiceClient(server.Client(), server.URL)}

	ctx, cancel := context.WithCancel(context.Background())
	handle, err := adapter.Open(ctx, "device-1", "binding-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	cancel()

	// The relay goroutine must observe the cancellation and stop rather
	// than leak; Err() reports ctx.Err() once it does.
	deadline := time.Now().Add(5 * time.Second)
	for handle.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the relay to stop after cancellation")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestConnectAdapterCloseTornsDownTheStreamImmediatelyAfterOpen proves
// Close actually tears down the stream rather than the relay merely
// noticing the stream end on its own. The stub paces its remaining
// messages 10ms apart, so completing all of them naturally takes about
// 2 seconds; Close is called right after the first pulse arrives, and
// handle.Err() must become non-nil well before that natural completion —
// with a Close that closes nothing, the relay would instead keep
// receiving paced pulses for the full ~2 seconds.
func TestConnectAdapterCloseTornsDownTheStreamImmediatelyAfterOpen(t *testing.T) {
	grant := newTestGrant()
	pulse := &edgev1.AuthorityPulse{}
	pulse.SetAuthority(edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED)

	const pacedPulses = 200 // 10ms apart: ~2s to complete naturally
	updates := make([]func() (*edgev1.OpenDeviceSubmissionResponse, error), 0, pacedPulses+1)
	updates = append(updates, func() (*edgev1.OpenDeviceSubmissionResponse, error) { return grantMessage(grant), nil })
	for range pacedPulses {
		updates = append(updates, func() (*edgev1.OpenDeviceSubmissionResponse, error) {
			time.Sleep(10 * time.Millisecond)
			return pulseMessage(pulse), nil
		})
	}

	svc := &stubEdgeService{submissionUpdates: updates}
	server := newTestServer(t, svc)

	adapter := &credential.ConnectAdapter{Client: edgev1connect.NewEdgeServiceClient(server.Client(), server.URL)}

	handle, err := adapter.Open(context.Background(), "device-1", "binding-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for handle.Authority() != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the relay to read the first pulse")
		}
		time.Sleep(time.Millisecond)
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Well under the ~2s the remaining paced pulses would need to
	// complete naturally.
	deadline = time.Now().Add(time.Second)
	for handle.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the relay to stop after Close; Close closed nothing and the stream is still running its natural, paced course")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConnectAdapterAcquireReadCredentialTranslatesResponse(t *testing.T) {
	cred := &edgev1.DeviceCredential{}
	cred.SetTypedMaterial(shellMaterial("read-material"))
	resp := &edgev1.AcquireReadCredentialResponse{}
	resp.SetCredential(cred)

	svc := &stubEdgeService{acquireResponse: resp}
	server := newTestServer(t, svc)

	adapter := &credential.ConnectAdapter{Client: edgev1connect.NewEdgeServiceClient(server.Client(), server.URL)}

	policy := &policyv1.AccessPolicyHandle{}
	policy.SetKey("icx7150-lab")
	policy.SetVersion(1)

	got, err := adapter.AcquireReadCredential(context.Background(), "device-1", "binding-1", policy)
	if err != nil {
		t.Fatalf("AcquireReadCredential: %v", err)
	}
	if username := got.GetCredential().GetTypedMaterial().GetShell().GetUsername(); username != "read-material" {
		t.Fatalf("expected the credential material to round-trip, got %q", username)
	}
}

// shellMaterial builds a shell credential whose username is the marker a
// test checks for after the round trip.
func shellMaterial(username string) *credentialv1.CredentialMaterial {
	shell := &credentialv1.ShellCredential{}
	shell.SetUsername(username)
	shell.SetPassword("secret")
	material := &credentialv1.CredentialMaterial{}
	material.SetShell(shell)
	return material
}

func TestConnectAdapterAcquireReadCredentialWrapsError(t *testing.T) {
	svc := &stubEdgeService{acquireErr: connect.NewError(connect.CodeUnavailable, errors.New("down"))}
	server := newTestServer(t, svc)

	adapter := &credential.ConnectAdapter{Client: edgev1connect.NewEdgeServiceClient(server.Client(), server.URL)}

	policy := &policyv1.AccessPolicyHandle{}
	policy.SetKey("icx7150-lab")
	policy.SetVersion(1)

	if _, err := adapter.AcquireReadCredential(context.Background(), "device-1", "binding-1", policy); err == nil {
		t.Fatal("expected an error")
	}
}
