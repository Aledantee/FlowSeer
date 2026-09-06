package edgebus

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeForwarder identifies a failure starting the forwarder.
var ErrCodeForwarder = errs.NewCode("edgebus/forwarder")

// ForwarderConfig declares where central ships the OpenTelemetry bodies
// its edges publish. Construct with keyed fields.
type ForwarderConfig struct {
	// Endpoint is the collector's base URL; the signal paths are appended.
	Endpoint string
	// Headers are sent on every request, an authorization header for
	// example.
	Headers map[string]string
	// Client sends the requests. Nil uses a client with a thirty-second
	// timeout.
	Client *http.Client
	// RetryDelay is how long a failed body waits before the stream
	// redelivers it. Zero means five seconds.
	RetryDelay time.Duration
}

// Forwarder reads every OTLP body from the hub's buffer and posts it on,
// unchanged, acknowledging only after the collector accepted it.
type Forwarder struct {
	cfg      ForwarderConfig
	consumer jetstream.ConsumeContext
	done     chan struct{}
}

// StartForwarder attaches the durable consumer and forwards until Close.
func StartForwarder(ctx context.Context, hub *Hub, cfg ForwarderConfig) (*Forwarder, error) {
	if cfg.Endpoint == "" {
		return nil, errs.New().Code(ErrCodeConfig).Msg("forwarder needs the collector endpoint")
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 5 * time.Second
	}
	stream, err := hub.JetStream().Stream(ctx, HubBufferStream)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeForwarder).Msg("look up hub buffer stream")
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "otel-forwarder",
		FilterSubject: "flowseer.*.edge.*.otel.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.Client.Timeout + 10*time.Second,
	})
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeForwarder).Msg("create forwarder consumer")
	}
	f := &Forwarder{cfg: cfg, done: make(chan struct{})}
	f.consumer, err = consumer.Consume(f.forward)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeForwarder).Msg("start forwarder consumer")
	}
	return f, nil
}

func (f *Forwarder) forward(msg jetstream.Msg) {
	signal, ok := signalOf(msg.Subject())
	if !ok {
		// Not an OTLP body; the filter should not deliver it, and there is
		// nothing to retry.
		_ = msg.Term()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), f.cfg.Client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(f.cfg.Endpoint, "/")+"/v1/"+string(signal), bytes.NewReader(msg.Data()))
	if err != nil {
		_ = msg.Term()
		return
	}
	req.Header.Set("Content-Type", otlpContentType)
	for k, v := range f.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := f.cfg.Client.Do(req)
	if err != nil {
		_ = msg.NakWithDelay(f.cfg.RetryDelay)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_ = msg.Ack()
		return
	}
	_ = msg.NakWithDelay(f.cfg.RetryDelay)
}

// Close stops consuming; a body in flight finishes.
func (f *Forwarder) Close() {
	if f.consumer != nil {
		f.consumer.Drain()
		f.consumer = nil
	}
}
