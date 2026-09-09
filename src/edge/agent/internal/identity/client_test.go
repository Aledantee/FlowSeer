package identity_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
)

// capturingCentral records the bytes and headers of each request as they
// arrive, which is what central's own middleware verifies over: it reads the
// raw body and hashes it, so anything the edge's transport does to the
// request after signing shows up here as a mismatch.
type capturingCentral struct {
	edgev1connect.UnimplementedEdgeServiceHandler

	mu       sync.Mutex
	requests []capturedRequest
}

type capturedRequest struct {
	path     string
	body     []byte
	header   string
	encoding string
}

// wrap sits in front of the Connect handler in the same place central's
// assertion middleware does, and like it reads the body whole and puts it
// back for the handler behind it — which is a handler that reads its own
// request and answers nothing without one.
func (c *capturingCentral) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		c.mu.Lock()
		c.requests = append(c.requests, capturedRequest{
			path:     r.URL.Path,
			body:     body,
			header:   r.Header.Get("Authorization"),
			encoding: r.Header.Get("Content-Encoding"),
		})
		c.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (c *capturingCentral) captured() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

func (c *capturingCentral) Heartbeat(
	context.Context, *connect.Request[edgev1.HeartbeatRequest],
) (*connect.Response[edgev1.HeartbeatResponse], error) {
	return connect.NewResponse(&edgev1.HeartbeatResponse{}), nil
}

func (c *capturingCentral) ListDevices(
	context.Context, *connect.Request[edgev1.ListDevicesRequest],
) (*connect.Response[edgev1.ListDevicesResponse], error) {
	return connect.NewResponse(&edgev1.ListDevicesResponse{}), nil
}

func (c *capturingCentral) OpenDeviceSubmission(
	_ context.Context, _ *connect.Request[edgev1.OpenDeviceSubmissionRequest],
	stream *connect.ServerStream[edgev1.OpenDeviceSubmissionResponse],
) error {
	return stream.Send(&edgev1.OpenDeviceSubmissionResponse{})
}

// signedClient stands a fake central up and returns a client whose requests
// go through the signing transport, plus the signer a test re-signs with.
func signedClient(t *testing.T, central *capturingCentral) (edgev1connect.EdgeServiceClient, *identity.Signer) {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := edgev1connect.NewEdgeServiceHandler(central)
	mux.Handle(path, central.wrap(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	issued := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	signer := identity.NewSigner(key, vectorEnrollment(), func() time.Time { return issued })
	// The published vector's own nonce and clock, so a header this transport
	// produced can be compared against the vector as well as against a
	// re-signing. Production draws from crypto/rand, and central refuses a
	// repeat.
	identity.SetNonceForTest(signer, vectorNonce())

	httpClient := &http.Client{Transport: identity.SigningTransport(server.Client().Transport, signer)}
	return edgev1connect.NewEdgeServiceClient(httpClient, server.URL), signer
}

// wantSignedAs asserts that the request central received carries the header
// this edge's own signer produces over the bytes central received, on the
// procedure central saw. That is the whole of what the verifier checks about
// the transport, checked against the bytes on the server's side of the wire
// rather than against what the client believed it sent.
func wantSignedAs(t *testing.T, signer *identity.Signer, got capturedRequest) {
	t.Helper()
	if got.encoding != "" && got.encoding != "identity" {
		t.Fatalf("request arrived with Content-Encoding %q; central refuses a compressed request before verifying", got.encoding)
	}
	want, err := signer.Header(got.path, got.body)
	if err != nil {
		t.Fatalf("Header() error: %v", err)
	}
	if got.header != want {
		assertion := identity.DecodeForTest(t, got.header)
		digest := sha256.Sum256(got.body)
		t.Errorf("the assertion does not match the request central received:\nprocedure signed %q, invoked %q\nbody digest signed %x, received %x",
			assertion.GetProcedure(), got.path, assertion.GetBodySha256(), digest[:])
	}
}

// TestEveryCallCarriesAnAssertionOverTheBytesCentralReceives is the transport's
// reason for existing. Central verifies over the raw HTTP body, so a signature
// taken anywhere but the wire — over a re-marshal of the message, say — is a
// valid signature over bytes nobody sent.
//
// The two calls differ in body: one carries fields, the other is an empty
// message, and an empty body is the case a transport that skipped signing
// when there was nothing to read would still pass with.
func TestEveryCallCarriesAnAssertionOverTheBytesCentralReceives(t *testing.T) {
	central := &capturingCentral{}
	client, signer := signedClient(t, central)

	if _, err := client.Heartbeat(context.Background(), connect.NewRequest(edgev1.HeartbeatRequest_builder{
		AgentVersion: proto.String("v0.1.0-test"),
	}.Build())); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if _, err := client.ListDevices(context.Background(), connect.NewRequest(&edgev1.ListDevicesRequest{})); err != nil {
		t.Fatalf("ListDevices: %v", err)
	}

	captured := central.captured()
	if len(captured) != 2 {
		t.Fatalf("central received %d requests, want 2", len(captured))
	}
	if len(captured[0].body) == 0 {
		t.Fatal("the heartbeat arrived with an empty body; this test cannot tell a signed body from an unsigned one")
	}
	for _, got := range captured {
		wantSignedAs(t, signer, got)
	}
}

// The api/edge README's second worked header, for a server-stream open with
// a non-empty body, and the request it was computed over: the same key,
// edge, audience, window and nonce as the first vector, procedure
// OpenDeviceSubmission, and body_sha256 over the Connect-enveloped request
// for this device, binding and sequence. A conformance test keeps the header
// in that file.
const (
	vectorStreamDevice  = "0192e6a0-0000-7000-8000-0000000000d1"
	vectorStreamBinding = "0192e6a0-0000-7000-8000-0000000000b1"
	vectorStreamSeq     = 42
	vectorStreamHeader  = "FlowSeer-Edge CrgBCigKJgokMDE5MmU2YTAtMDAwMC03MDAwLTgwMDAtMDAwMDAwMDAwMGVkEhBmbG93c2Vlci1jZW50cmFsGgYIwIjw1AYiBgjeiPDUBioQAAECAwQFBgcICQoLDA0ODzI2L2Zsb3dzZWVyLmFwaS5lZGdlLnYxLkVkZ2VTZXJ2aWNlL09wZW5EZXZpY2VTdWJtaXNzaW9uOiBeiE+EkKO3xyVNss7plVtArK4AMWo9MlZn0egw06XT8BJA7CbFLzvZmR/jh2rq6sc+fB89EXI+K99j9O69oKmbsQc2AwMZpsAPlXngwrtopUzc5itWFa+FDYXe2DB+ZQOZBg"
)

func vectorNonce() []byte {
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i)
	}
	return nonce
}

// TestAStreamOpenIsSignedOverItsEnvelopedRequest covers the one shape that is
// not a plain unary body: a server-stream's request is a single
// Connect-enveloped message, and central hashes those bytes like any other.
// A transport that signed only what it could read without blocking would
// pass every unary test and fail here.
//
// The request is the one the README's second worked vector was computed
// over, so the header this transport produced is compared against a string
// nothing in this repository's edge side generated. A transport checked only
// against its own signer proves the two halves of one implementation agree;
// this is the same evidence the signer's own vector test rests on, carried
// one layer out to the bytes Connect actually puts on the wire.
func TestAStreamOpenIsSignedOverItsEnvelopedRequest(t *testing.T) {
	central := &capturingCentral{}
	client, signer := signedClient(t, central)

	stream, err := client.OpenDeviceSubmission(context.Background(), connect.NewRequest(edgev1.OpenDeviceSubmissionRequest_builder{
		DeviceId:  proto.String(vectorStreamDevice),
		BindingId: proto.String(vectorStreamBinding),
		Sequence:  proto.Uint64(vectorStreamSeq),
	}.Build()))
	if err != nil {
		t.Fatalf("OpenDeviceSubmission: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	if !stream.Receive() {
		t.Fatalf("the stream delivered nothing: %v", stream.Err())
	}

	captured := central.captured()
	if len(captured) != 1 {
		t.Fatalf("central received %d requests, want 1", len(captured))
	}
	// The envelope is a flags byte and a length prefix in front of the
	// message, so a body no longer than the message would mean the bytes
	// signed are not the bytes sent.
	if len(captured[0].body) <= proto.Size(edgev1.OpenDeviceSubmissionRequest_builder{
		DeviceId:  proto.String(vectorStreamDevice),
		BindingId: proto.String(vectorStreamBinding),
		Sequence:  proto.Uint64(vectorStreamSeq),
	}.Build()) {
		t.Fatalf("the stream open arrived with %d bytes, want the enveloped message", len(captured[0].body))
	}
	wantSignedAs(t, signer, captured[0])

	if captured[0].header != vectorStreamHeader {
		// Two causes, and they are worth telling apart. Either the transport
		// signed something other than the enveloped bytes central received —
		// which wantSignedAs above would already have caught — or Connect no
		// longer encodes this message the way the vector's deterministic
		// marshal does, which is allowed and would make this comparison the
		// wrong instrument rather than the code wrong.
		t.Errorf("the stream open's header does not match the published vector:\ngot  %s\nwant %s", captured[0].header, vectorStreamHeader)
	}
}

// TestEnrollCarriesNoAssertion. Enroll is the call an edge makes before it
// has a key central knows, and it is served in front of the assertion
// middleware; the pinned client is what makes it, and it signs nothing.
func TestEnrollCarriesNoAssertion(t *testing.T) {
	central := &capturingCentral{}
	mux := http.NewServeMux()
	path, handler := edgev1connect.NewEdgeServiceHandler(central)
	mux.Handle(path, central.wrap(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := edgev1connect.NewEdgeServiceClient(server.Client(), server.URL)
	// The handler is unimplemented, so the call fails; what this test is
	// about is the request that reached central, not the answer.
	_, _ = client.Enroll(context.Background(), connect.NewRequest(&edgev1.EnrollRequest{}))

	captured := central.captured()
	if len(captured) != 1 {
		t.Fatalf("central received %d requests, want 1", len(captured))
	}
	if captured[0].header != "" {
		t.Errorf("Enroll carried an Authorization header %q, want none", captured[0].header)
	}
}

// TestACompressedRequestIsRefusedHereRatherThanByCentral. Central refuses a
// compressed request before it verifies anything, because the bytes it hashes
// have to be the bytes on the wire — so an edge built with compression on
// fails every call with an answer that says nothing about why. This says why.
func TestACompressedRequestIsRefusedHereRatherThanByCentral(t *testing.T) {
	signer := identity.NewSigner(
		ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)), vectorEnrollment(), time.Now)
	sent := false
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		sent = true
		return nil, nil
	})

	request, err := http.NewRequest(http.MethodPost, "https://central.example.test/flowseer.api.edge.v1.EdgeService/Heartbeat", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Content-Encoding", "gzip")

	if _, err := identity.SigningTransport(base, signer).RoundTrip(request); err == nil {
		t.Error("RoundTrip() error = nil, want a compressed request refused")
	}
	if sent {
		t.Error("the compressed request was sent anyway")
	}
}

func TestClockSkewResponseRetriesWithServerTime(t *testing.T) {
	localNow := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	serverNow := time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC)
	signer := identity.NewSigner(
		ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)),
		vectorEnrollment(),
		func() time.Time { return localNow },
	)
	identity.SetNonceForTest(signer, make([]byte, 16))

	var headers []string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		headers = append(headers, req.Header.Get("Authorization"))
		response := &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}
		if len(headers) == 1 {
			response.StatusCode = http.StatusUnauthorized
			response.Status = "401 Unauthorized"
			response.Header.Set("FlowSeer-Refusal-Code", "edge/clock-skew")
			response.Header.Set("Date", serverNow.Format(http.TimeFormat))
		}
		return response, nil
	})
	request, err := http.NewRequest(http.MethodPost, "https://central.example.test/flowseer.api.edge.v1.EdgeService/Heartbeat", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	response, err := identity.SigningTransport(base, signer).RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response: %v", err)
		}
	}()
	if len(headers) != 2 {
		t.Fatalf("central saw %d requests, want the clock-skew refusal retried once", len(headers))
	}
	if headers[0] == headers[1] {
		t.Error("the retry reused the refused assertion")
	}
	if got := identity.DecodeForTest(t, headers[1]).GetIssuedAt().AsTime(); !got.Equal(serverNow) {
		t.Errorf("retried issued_at = %v, want response Date %v", got, serverNow)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
