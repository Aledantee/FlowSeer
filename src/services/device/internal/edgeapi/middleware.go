// Package edgeapi serves the edge-facing services behind an assertion-
// verifying HTTP middleware and implements their handlers.
//
// The middleware runs before Connect decodes the request: it hashes the raw
// HTTP body a client sent, so body_sha256 binds to the bytes on the wire, and
// it refuses a compressed request because the edge sends none and the hash is
// over the uncompressed bytes. It refuses the call on any verification error —
// a bad signature, an unknown edge, or a lookup that could not answer — so a
// could-not-tell never lets a later handler proceed with the edge-to-device
// binding dissolved. On success it puts the verified edge id in the request
// context, where the report and audit handlers read it to bind the call to the
// device it names.
package edgeapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/edge"
)

// ErrCodeNoEdge is returned by EdgeIDFromContext when the context carries no
// verified edge — a handler reached without the middleware, which is a wiring
// bug, not an authentication failure.
var ErrCodeNoEdge = errs.NewCode("edgeapi/no-edge")

// defaultMaxBody bounds the request body the middleware buffers to hash. Every
// edge request is small: an assertion header, and at most one enveloped
// submission-open message.
const defaultMaxBody = 1 << 20

type edgeIDKey struct{}

// EdgeIDFromContext returns the verified edge id the middleware placed in ctx.
// The dispatch relay's EdgeID hook and the audit handler's binding read the
// calling edge through it.
func EdgeIDFromContext(ctx context.Context) (string, error) {
	id, ok := ctx.Value(edgeIDKey{}).(string)
	if !ok || id == "" {
		return "", errs.New().Code(ErrCodeNoEdge).Msg("request context carries no verified edge")
	}
	return id, nil
}

// Middleware verifies the assertion on every wrapped call.
type Middleware struct {
	verifier *edge.Verifier
	maxBody  int64
	log      *slog.Logger
}

// NewMiddleware wraps a verifier. maxBody defaults when non-positive.
func NewMiddleware(verifier *edge.Verifier, maxBody int64, log *slog.Logger) *Middleware {
	if maxBody <= 0 {
		maxBody = defaultMaxBody
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Middleware{verifier: verifier, maxBody: maxBody, log: log}
}

// Wrap returns next behind the assertion check.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if compressed(r) {
			m.refuse(w, http.StatusBadRequest, "compressed request refused: the edge sends uncompressed requests")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, m.maxBody+1))
		if err != nil {
			m.refuse(w, http.StatusBadRequest, "read request body")
			return
		}
		if int64(len(body)) > m.maxBody {
			m.refuse(w, http.StatusRequestEntityTooLarge, "request body exceeds the edge limit")
			return
		}

		header := r.Header.Get("Authorization")
		assertion, err := m.verifier.Verify(r.Context(), header, r.URL.Path, body)
		if err != nil {
			// Refuse on any verification error, could-not-tell included, so a
			// lookup that could not answer blocks the call rather than letting
			// it proceed unauthenticated.
			m.log.InfoContext(r.Context(), "edge assertion rejected", "procedure", r.URL.Path, "error", err)
			status := http.StatusUnauthorized
			if errs.Retryable(err) {
				status = http.StatusServiceUnavailable
			}
			code, _ := errs.CodeOf(err)
			m.refuse(w, status, code.String())
			return
		}

		// Restore the body for the inner handler, which reads it fresh, and
		// hand the verified edge id down for the binding checks.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		ctx := context.WithValue(r.Context(), edgeIDKey{}, assertion.GetEdge().GetEdge().GetId())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) refuse(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
}

// compressed reports whether the request carries a non-identity content
// encoding on any of the headers Connect and gRPC use, which the middleware
// refuses so the bytes it hashes are the bytes the client meant to send.
func compressed(r *http.Request) bool {
	for _, h := range []string{"Content-Encoding", "Connect-Content-Encoding", "Grpc-Encoding"} {
		if v := r.Header.Get(h); v != "" && !strings.EqualFold(v, "identity") {
			return true
		}
	}
	return false
}
