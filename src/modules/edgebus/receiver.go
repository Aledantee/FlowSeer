package edgebus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeReceiver identifies a failure starting the loopback receiver.
var ErrCodeReceiver = errs.NewCode("edgebus/receiver")

const (
	otlpContentType = "application/x-protobuf"
	// maxOTLPBody bounds one export request, matching the largest batch the
	// runtime's exporter sends.
	maxOTLPBody = 16 << 20
)

// Receiver is the loopback OTLP/HTTP endpoint the edge's own telemetry
// runtime exports into. It binds 127.0.0.1 on a kernel-chosen port, so a
// collision cannot stop the agent, and publishes every export body as
// received into the leaf's buffer; the runtime keeps its batching and
// retry, and central's forwarder posts the same bytes on.
type Receiver struct {
	leaf     *Leaf
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	err      error
}

// StartReceiver binds the receiver and serves until Close. The returned
// Receiver must be closed.
func StartReceiver(leaf *Leaf) (*Receiver, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeReceiver).Msg("bind loopback receiver")
	}
	r := &Receiver{leaf: leaf, listener: listener, done: make(chan struct{})}
	mux := http.NewServeMux()
	for _, signal := range []OTelSignal{SignalLogs, SignalMetrics, SignalTraces} {
		mux.HandleFunc("POST /v1/"+string(signal), r.handle(signal))
	}
	r.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		defer close(r.done)
		if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.err = err
		}
	}()
	return r, nil
}

// Endpoint is the URL the edge hands its runtime as the OTLP endpoint; the
// runtime appends the signal paths itself.
func (r *Receiver) Endpoint() string {
	return "http://" + r.listener.Addr().String()
}

func (r *Receiver) handle(signal OTelSignal) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if ct := req.Header.Get("Content-Type"); !strings.HasPrefix(ct, otlpContentType) {
			http.Error(w, "expected "+otlpContentType, http.StatusUnsupportedMediaType)
			return
		}
		if enc := req.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
			http.Error(w, "compressed export bodies are not accepted", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, maxOTLPBody+1))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if len(body) > maxOTLPBody {
			http.Error(w, "export body too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := r.leaf.Publish(req.Context(), r.leaf.OTelSubject(signal), body, ""); err != nil {
			// The buffer refused the record; the runtime's exporter retries
			// on a 503 with its own backoff.
			http.Error(w, "buffer unavailable", http.StatusServiceUnavailable)
			return
		}
		// An empty Export*ServiceResponse is a valid protobuf message with no
		// partial-success field set.
		w.Header().Set("Content-Type", otlpContentType)
		w.WriteHeader(http.StatusOK)
	}
}

// Close stops serving and waits for the server to stop.
func (r *Receiver) Close(ctx context.Context) error {
	err := r.server.Shutdown(ctx)
	<-r.done
	if err != nil {
		return errs.From(err).Code(ErrCodeReceiver).Msg("stop loopback receiver")
	}
	if r.err != nil {
		return fmt.Errorf("loopback receiver: %w", r.err)
	}
	return nil
}
