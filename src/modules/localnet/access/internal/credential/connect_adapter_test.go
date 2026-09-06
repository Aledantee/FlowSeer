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
	cred.SetMaterial([]byte("material"))

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
	pulse := &edgev1.AuthorityPulse{}
	pulse.SetAuthority(edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED)

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
	if string(handle.Grant().GetCredential().GetMaterial()) != "material" {
		t.Fatalf("expected the grant's credential material to round-trip, got %q", handle.Grant().GetCredential().GetMaterial())
	}

	// Authority() is a synchronous snapshot the relay goroutine updates as
	// soon as it reads a pulse off the wire, independent of whether
	// anything is polling at that instant — poll with a deadline rather
	// than blocking on a channel receive.
	deadline := time.Now().Add(5 * time.Second)
	for handle.Authority() != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Authority() to reflect the AUTHORIZED pulse, got %v", handle.Authority())
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

func TestConnectAdapterAcquireReadCredentialTranslatesResponse(t *testing.T) {
	cred := &edgev1.DeviceCredential{}
	cred.SetMaterial([]byte("read-material"))
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
	if string(got.GetCredential().GetMaterial()) != "read-material" {
		t.Fatalf("expected the credential material to round-trip, got %q", got.GetCredential().GetMaterial())
	}
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
