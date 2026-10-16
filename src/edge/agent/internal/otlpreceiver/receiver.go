// Package otlpreceiver accepts local telemetry exports into the agent log.
package otlpreceiver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// ErrCodeReceiver identifies a failure starting the loopback receiver.
var ErrCodeReceiver = errs.NewCode("otlpreceiver/receiver")

const (
	otlpContentType = "application/x-protobuf"
	// maxOTLPBody bounds one export request at 255 KiB. The edge's log holds
	// a body as one record of at most 256 KiB, and the delivery call's
	// TelemetryExport.body holds at most 255 KiB, so a body central cannot
	// carry is refused here with a 413 instead of stalling the log.
	maxOTLPBody = 255 << 10
)

// Appender stores one marshaled TelemetryExport per record. Append must be
// safe for concurrent calls from the receiver's HTTP handlers.
type Appender interface {
	Append(record []byte) error
}

// Receiver accepts uncompressed protobuf bodies on a loopback HTTP endpoint.
// Each accepted body is stored with its signal in the appender. It is safe
// for concurrent use and must be closed after the telemetry runtime stops.
type Receiver struct {
	appender Appender
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	err      error
}

// Start binds the receiver and serves until Close. The returned
// Receiver must be closed.
func Start(appender Appender) (*Receiver, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeReceiver).Msg("bind loopback receiver")
	}
	r := &Receiver{appender: appender, listener: listener, done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/logs", r.handle(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS))
	mux.HandleFunc("POST /v1/metrics", r.handle(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS))
	mux.HandleFunc("POST /v1/traces", r.handle(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_TRACES))
	r.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	// There is no request context during the accept loop, so a serve panic
	// is reported without request correlation.
	r.startServing(context.Background(), listener)
	return r, nil
}

// startServing writes r.err before closing r.done on both completion paths.
// Close joins through r.done before reading r.err, including after a panic.
func (r *Receiver) startServing(ctx context.Context, listener net.Listener) {
	spawn.Go(ctx, "otlpreceiver.Receiver.serve", func() {
		if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.err = err
		}
		close(r.done)
	}, spawn.ReportTo(func(err error) {
		r.err = err
		close(r.done)
	}))
}

// Endpoint is the bound base HTTP URL. The telemetry runtime appends the
// signal path.
func (r *Receiver) Endpoint() string {
	return "http://" + r.listener.Addr().String()
}

func (r *Receiver) handle(signal telemetryv1.TelemetrySignal) http.HandlerFunc {
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
		record, err := proto.Marshal(telemetryv1.TelemetryExport_builder{
			Signal: signal.Enum(),
			Body:   body,
		}.Build())
		if err != nil {
			http.Error(w, "encode export", http.StatusInternalServerError)
			return
		}
		if err := r.appender.Append(record); err != nil {
			// An unavailable log must leave the export eligible for retry.
			http.Error(w, "buffer unavailable", http.StatusServiceUnavailable)
			return
		}
		// The exporter expects an empty protobuf response on success.
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
		return errs.Wrap(r.err, "loopback receiver")
	}
	return nil
}
