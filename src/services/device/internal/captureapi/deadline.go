package captureapi

import (
	"context"
	"net/http"
	"time"
)

type readDeadlineKey struct{}

// setReadDeadline moves the deadline on the request body a handler is reading
// from. It returns http.ErrNotSupported where the transport cannot carry one.
type setReadDeadline func(time.Time) error

// WithUploadReadDeadline hands the handler a way to bound how long it will
// wait for the next message on a client-streaming call.
//
// UploadCapture must close a stream whose assertion window has lapsed, and the
// edge that stops sending is exactly the case a handler cannot see: connect's
// ClientStream.Receive blocks on the request body and takes no context, so
// canceling anything derived from the request context leaves it blocked. The
// deadline is the one lever that reaches the read, and only the layer holding
// the ResponseWriter can pull it.
func WithUploadReadDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		ctx := context.WithValue(r.Context(), readDeadlineKey{}, setReadDeadline(controller.SetReadDeadline))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// readDeadlineFrom returns the deadline setter the transport installed, or nil
// where nothing did.
func readDeadlineFrom(ctx context.Context) setReadDeadline {
	set, _ := ctx.Value(readDeadlineKey{}).(setReadDeadline)
	return set
}
