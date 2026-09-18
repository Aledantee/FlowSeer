package identity

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

const (
	refusalCodeHeader = "FlowSeer-Refusal-Code"
	clockSkewCode     = "edge/clock-skew"
)

// PinnedTransport dials central over TLS pinned to anchors, the SPKI digests
// this edge was provisioned with or last told to trust.
//
// It is the same pin the leaf node dials the bus with, built from the same
// anchors by the same verifier, so the two cannot drift into trusting
// different certificates. Pinning is what makes central's clock and its
// enrollment answer safe to believe: a boxed edge with a dead battery takes
// its assertion timestamps from central, and corporate TLS interception on a
// customer network would otherwise read every assertion this edge sends.
func PinnedTransport(anchors [][]byte) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = edgebus.PinnedTLSConfig(anchors)
	return transport
}

// PinnedClient is the HTTP client for Enroll: pinned, and carrying no
// assertion. Enroll is the one call an edge makes before it has an identity
// to sign with, and it is served in front of central's assertion middleware.
func PinnedClient(anchors [][]byte) *http.Client {
	return &http.Client{Transport: PinnedTransport(anchors)}
}

// SigningClient is the HTTP client for every other call: pinned, and putting
// a fresh assertion on each request.
func SigningClient(anchors [][]byte, signer *Signer) *http.Client {
	return &http.Client{Transport: SigningTransport(PinnedTransport(anchors), signer)}
}

// SigningTransport puts an assertion on every request that goes through base.
//
// It is a RoundTripper rather than a Connect interceptor because the
// assertion covers the request body bytes as they go on the wire, and an
// interceptor sees decoded messages: a client's encoding need not match a
// re-marshal of what it sent, and central hashes what it received. This is
// the layer where those bytes exist.
//
// It reads each request body whole, which is right for the calls it carries —
// EdgeService is unary except for OpenDeviceSubmission, whose request is one
// enveloped message — and wrong for a client-streaming call, which has no
// last byte to sign. There is no such call on this service.
//
// A body already compressed is refused rather than sent. Central refuses a
// compressed request before verifying, because the bytes it hashes have to be
// the bytes on the wire, and a client built with compression on would
// otherwise fail every call with an answer that says nothing about why.
//
// # Retries send the same assertion, and that is safe here
//
// This signs once per RoundTrip, so a retry inside net/http re-sends the
// assertion it already signed — and central refuses a nonce it has seen for
// this edge inside the assertion's window. The two only agree because of what
// net/http will retry: for a POST with no Idempotency-Key, `shouldRetryRequest`
// retries on `nothingWrittenError` and on having no usable HTTP/2 connection,
// and takes `isReplayable` (false for these requests) for everything else. In
// both retried cases nothing reached central, so the nonce was never seen and
// the retry verifies as the first attempt it is.
//
// If a request were ever retried after its bytes were written, central would
// refuse it as a replay. That is the correct direction — a nonce it has seen
// is one it must not accept again — and the loops above this one make the call
// again with a fresh assertion.
//
// GetBody is set on the signed request for the same reason: it is what lets
// net/http rewind and make that safe retry rather than failing the call.
//
// A clock-skew refusal is the other safe retry. Central rejects it before the
// RPC handler runs and returns both its structured refusal code and standard
// HTTP Date. The transport adopts that trusted time, signs a new assertion,
// and retries once.
func SigningTransport(base http.RoundTripper, signer *Signer) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &signingTransport{base: base, signer: signer}
}

type signingTransport struct {
	base   http.RoundTripper
	signer *Signer
}

func (t *signingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// UploadCapture carries in-stream assertions rather than an Authorization
	// header. A client-streaming call has no last byte to sign in advance, so
	// draining its body would deadlock until the upload ends.
	if req.URL.Path == capturev1connect.CaptureEdgeServiceUploadCaptureProcedure {
		return t.base.RoundTrip(req)
	}

	if header, encoding := compressedBy(req); header != "" {
		// Closed first: a RoundTripper owns the body it is handed and must
		// close it on every return, error paths included.
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, errs.New().Code(ErrCodeState).Attr("header", header).Attr("content_encoding", encoding).
			Msg("this client must send uncompressed requests; central hashes the bytes it receives")
	}

	body, err := drainBody(req)
	if err != nil {
		return nil, err
	}
	response, err := t.send(req, body)
	if err != nil || response.StatusCode != http.StatusUnauthorized || response.Header.Get(refusalCodeHeader) != clockSkewCode {
		return response, err
	}

	serverTime, err := http.ParseTime(response.Header.Get("Date"))
	if err != nil {
		return response, nil
	}
	if response.Body != nil {
		if err := response.Body.Close(); err != nil {
			return nil, errs.From(err).Code(ErrCodeState).Msg("close the clock-skew refusal before retrying")
		}
	}
	t.signer.AdoptServerTime(req.Context(), serverTime)
	return t.send(req, body)
}

func (t *signingTransport) send(req *http.Request, body []byte) (*http.Response, error) {
	// The procedure is the URL path, which is how Connect names a method and
	// what central checks the assertion's own procedure against.
	header, err := t.signer.Header(req.URL.Path, body)
	if err != nil {
		return nil, err
	}

	// Cloned: a RoundTripper may consume the body it is given and must not
	// otherwise modify the request, since the caller may still hold it — and
	// a retry needs a body to send again.
	signed := req.Clone(req.Context())
	signed.Header.Set("Authorization", header)
	signed.Body = io.NopCloser(bytes.NewReader(body))
	signed.ContentLength = int64(len(body))
	signed.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return t.base.RoundTrip(signed)
}

// drainBody reads a request's body whole and closes it.
func drainBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	closeErr := req.Body.Close()
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeState).Msg("read the request body to sign it")
	}
	if closeErr != nil {
		return nil, errs.From(closeErr).Code(ErrCodeState).Msg("close the request body after signing it")
	}
	return body, nil
}

// compressedBy names the header carrying a non-identity encoding, and the
// encoding, or two empty strings when the request is uncompressed.
//
// All three headers, because Connect and gRPC do not agree on one: connect-go
// puts a unary request's encoding on Content-Encoding and a streaming
// request's on Connect-Content-Encoding, and a gRPC client uses Grpc-Encoding.
// Central refuses on any of the three, so a guard reading only the first lets
// a compressed stream through to be refused there instead — with a bare
// status, which is the answer this exists to prevent. Case-insensitive for
// the same reason: central compares that way, and a guard stricter than the
// server refuses calls the server would have taken.
func compressedBy(req *http.Request) (string, string) {
	for _, header := range []string{"Content-Encoding", "Connect-Content-Encoding", "Grpc-Encoding"} {
		if v := req.Header.Get(header); v != "" && !strings.EqualFold(v, "identity") {
			return header, v
		}
	}
	return "", ""
}
