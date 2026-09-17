package edgeapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/edge"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
)

const (
	testEdgeID   = "0192e6a0-0000-7000-8000-0000000000ed"
	testAudience = "flowseer-central"
	testPath     = "/flowseer.api.edge.v1.EdgeService/Heartbeat"
)

func keypair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return private, public
}

// signedHeader builds a valid assertion for testPath and body, signs it, and
// renders the Authorization header. issued_at is real-now so the clock window
// passes against the verifier's wall clock.
func signedHeader(t *testing.T, private ed25519.PrivateKey, body []byte) string {
	t.Helper()
	return signedHeaderAt(t, private, body, time.Now())
}

func signedHeaderAt(t *testing.T, private ed25519.PrivateKey, body []byte, now time.Time) string {
	t.Helper()
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i)
	}
	sum := sha256.Sum256(body)
	a := edgev1.EdgeAssertion_builder{
		Edge:       edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build()}.Build(),
		Audience:   proto.String(testAudience),
		IssuedAt:   timestamppb.New(now),
		ExpiresAt:  timestamppb.New(now.Add(30 * time.Second)),
		Nonce:      nonce,
		Procedure:  proto.String(testPath),
		BodySha256: sum[:],
	}.Build()
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(a)
	if err != nil {
		t.Fatalf("marshal assertion: %v", err)
	}
	signed := edgev1.SignedEdgeAssertion_builder{Payload: payload, Signature: ed25519.Sign(private, payload)}.Build()
	wire, err := proto.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal signed: %v", err)
	}
	return edge.HeaderScheme + " " + base64.RawStdEncoding.EncodeToString(wire)
}

// lookup answers as the edge store does for an enrolled edge, or fails the
// lookup when err is set.
func lookup(public ed25519.PublicKey, err error) edge.KeyLookup {
	return func(context.Context, string) (ed25519.PublicKey, edgev1.EdgeLifecycle, error) {
		return public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED, err
	}
}

// spyHandler records whether it ran and the edge id it saw.
type spyHandler struct {
	ran    bool
	edgeID string
	err    error
}

func (s *spyHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	s.ran = true
	s.edgeID, s.err = edgeapi.EdgeIDFromContext(r.Context())
}

func serve(t *testing.T, l edge.KeyLookup, req *http.Request) (*spyHandler, int) {
	t.Helper()
	spy, rec := serveResponse(t, l, req)
	return spy, rec.Code
}

func serveResponse(t *testing.T, l edge.KeyLookup, req *http.Request) (*spyHandler, *httptest.ResponseRecorder) {
	t.Helper()
	v := edge.NewVerifier(testAudience, time.Minute, l)
	spy := &spyHandler{}
	rec := httptest.NewRecorder()
	edgeapi.NewMiddleware(v, 0, nil).Wrap(spy).ServeHTTP(rec, req)
	return spy, rec
}

func TestMiddlewarePassesAValidAssertionAndCarriesTheEdgeID(t *testing.T) {
	private, public := keypair(t)
	body := []byte("request-body")
	req := httptest.NewRequest(http.MethodPost, testPath, strings.NewReader(string(body)))
	req.Header.Set("Authorization", signedHeader(t, private, body))

	spy, code := serve(t, lookup(public, nil), req)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !spy.ran {
		t.Fatal("inner handler did not run")
	}
	if spy.err != nil || spy.edgeID != testEdgeID {
		t.Fatalf("edge id = %q, err %v; want %q", spy.edgeID, spy.err, testEdgeID)
	}
}

func TestMiddlewareRefusesABadAssertionWithoutRunningTheHandler(t *testing.T) {
	private, public := keypair(t)
	req := httptest.NewRequest(http.MethodPost, testPath, strings.NewReader("body"))
	// A header signed over a different body than the request carries.
	req.Header.Set("Authorization", signedHeader(t, private, []byte("other-body")))

	spy, code := serve(t, lookup(public, nil), req)
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if spy.ran {
		t.Fatal("the handler ran on a rejected assertion")
	}
}

func TestMiddlewareNamesClockSkewInAResponseHeader(t *testing.T) {
	private, public := keypair(t)
	req := httptest.NewRequest(http.MethodPost, testPath, strings.NewReader("body"))
	req.Header.Set("Authorization", signedHeaderAt(t, private, []byte("body"), time.Now().Add(-2*time.Minute)))

	spy, response := serveResponse(t, lookup(public, nil), req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if got := response.Header().Get("FlowSeer-Refusal-Code"); got != "edge/clock-skew" {
		t.Errorf("FlowSeer-Refusal-Code = %q, want edge/clock-skew", got)
	}
	if spy.ran {
		t.Fatal("the handler ran on a clock-skewed assertion")
	}
}

func TestMiddlewareRefusesACompressedRequest(t *testing.T) {
	private, public := keypair(t)
	body := []byte("body")
	req := httptest.NewRequest(http.MethodPost, testPath, strings.NewReader(string(body)))
	req.Header.Set("Authorization", signedHeader(t, private, body))
	req.Header.Set("Content-Encoding", "gzip")

	spy, code := serve(t, lookup(public, nil), req)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if spy.ran {
		t.Fatal("the handler ran on a compressed request")
	}
}

func TestMiddlewareRefusesWhenTheLookupCannotAnswer(t *testing.T) {
	private, public := keypair(t)
	body := []byte("body")
	req := httptest.NewRequest(http.MethodPost, testPath, strings.NewReader(string(body)))
	req.Header.Set("Authorization", signedHeader(t, private, body))

	// A could-not-tell lookup failure must refuse the call, retryably, not
	// let it proceed.
	spy, code := serve(t, lookup(public, errors.New("bucket unreachable")), req)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if spy.ran {
		t.Fatal("the handler ran when the key lookup could not answer")
	}
}
