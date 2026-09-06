package edgebus

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeForwarder identifies a failure starting the forwarder.
var ErrCodeForwarder = errs.NewCode("edgebus/forwarder")

// scopeName is this module's instrumentation scope.
const scopeName = "go.aledante.io/FlowSeer/src/modules/edgebus"

// refusal reasons, the closed set the refused-records counter is labeled
// with.
const (
	reasonForeignSubject   = "foreign_subject"
	reasonNotOTel          = "not_otel"
	reasonCollectorReject  = "collector_rejected"
	reasonDeliveryExceeded = "max_deliver_exceeded"
)

// refusedLogInterval rate-limits the refusal warning per edge stream: a
// misbehaving agent could otherwise turn its own relabel attempts into a
// log flood, while the counter keeps the full count.
const refusedLogInterval = 10 * time.Second

// forwarderMaxDeliver bounds redelivery of a body the collector keeps
// rejecting, so one malformed payload cannot be retried for the life of the
// stream.
const forwarderMaxDeliver = 8

// ForwarderConfig declares where central ships the OpenTelemetry bodies its
// edges publish. Construct with keyed fields.
type ForwarderConfig struct {
	// Endpoint is the collector's base URL; the signal paths are appended.
	Endpoint string
	// Headers are sent on every request, an authorization header for
	// example.
	Headers map[string]string
	// Client sends the requests. Nil uses a client with a thirty-second
	// timeout.
	Client *http.Client
	// RetryDelay is how long a retryable failure waits before the stream
	// redelivers it. Zero means five seconds.
	RetryDelay time.Duration
	// DiscoveryInterval is how often the forwarder looks for edges attached
	// since it started. Zero means ten seconds.
	DiscoveryInterval time.Duration
	// Logger receives the refusal warnings. Nil discards them.
	Logger *slog.Logger
	// MeterProvider backs the refused-records counter. Nil records nothing.
	MeterProvider metric.MeterProvider
}

// Forwarder follows every edge's hub stream and posts each OTLP body on,
// unchanged, acknowledging only after the collector accepted it. The edge a
// record belongs to is the stream it sits in, never the subject it carries:
// a record whose subject lies outside that edge's subtree is dropped, so no
// permission change can let one edge speak as another.
type Forwarder struct {
	cfg     ForwarderConfig
	hub     *Hub
	logger  *slog.Logger
	refused metric.Int64Counter
	cancel  context.CancelFunc
	done    chan struct{}

	mu         sync.Mutex
	consumers  map[string]jetstream.ConsumeContext
	dropped    int
	lastLogged map[string]time.Time
}

// StartForwarder attaches to every edge stream present now and to each one
// attached later, and forwards until Close.
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
	if cfg.DiscoveryInterval == 0 {
		cfg.DiscoveryInterval = 10 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	provider := cfg.MeterProvider
	if provider == nil {
		provider = metricnoop.NewMeterProvider()
	}
	refused, err := provider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL)).Int64Counter(
		"flowseer.edgebus.records.refused",
		metric.WithUnit("{record}"),
		metric.WithDescription("Records the forwarder refused instead of shipping, by reason and signal"),
	)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeForwarder).Msg("build refused-records counter")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	f := &Forwarder{
		cfg: cfg, hub: hub, logger: logger, refused: refused,
		cancel: cancel, done: make(chan struct{}),
		consumers: map[string]jetstream.ConsumeContext{}, lastLogged: map[string]time.Time{},
	}
	if err := f.discover(ctx); err != nil {
		cancel()
		return nil, err
	}
	go f.follow(runCtx)
	return f, nil
}

// follow re-runs discovery on the configured interval so an edge attached
// after the forwarder started gets its consumer.
func (f *Forwarder) follow(ctx context.Context) {
	defer close(f.done)
	ticker := time.NewTicker(f.cfg.DiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = f.discover(ctx)
		}
	}
}

// discover attaches a durable consumer to every attached edge that has
// none. Each edge's source stream lives in that edge's own account, so the
// forwarder reads it through the hub's per-edge connection rather than one
// shared context.
func (f *Forwarder) discover(ctx context.Context) error {
	for _, edgeID := range f.hub.AttachedEdges() {
		f.mu.Lock()
		_, following := f.consumers[edgeID]
		f.mu.Unlock()
		if following {
			continue
		}
		if err := f.attach(ctx, edgeID); err != nil {
			return err
		}
	}
	return nil
}

func (f *Forwarder) attach(ctx context.Context, edgeID string) error {
	stream, err := f.hub.EdgeStream(ctx, edgeID)
	if err != nil {
		return errs.From(err).Code(ErrCodeForwarder).Attr("edge", edgeID).Msg("look up edge stream")
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "otel_forwarder",
		FilterSubject: "flowseer.*.edge.*.otel.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       f.cfg.Client.Timeout + 10*time.Second,
		MaxDeliver:    forwarderMaxDeliver,
	})
	if err != nil {
		return errs.From(err).Code(ErrCodeForwarder).Attr("edge", edgeID).Msg("create forwarder consumer")
	}
	consume, err := consumer.Consume(func(msg jetstream.Msg) { f.forward(edgeID, msg) })
	if err != nil {
		return errs.From(err).Code(ErrCodeForwarder).Attr("edge", edgeID).Msg("start forwarder consumer")
	}
	f.mu.Lock()
	f.consumers[edgeID] = consume
	f.mu.Unlock()
	return nil
}

func (f *Forwarder) forward(edgeID string, msg jetstream.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), f.cfg.Client.Timeout)
	defer cancel()
	signal, ok := signalOf(msg.Subject())
	switch {
	case !ok:
		f.refuse(ctx, edgeID, msg, reasonNotOTel, "")
		return
	case !belongsToEdge(f.hub.Tenant(), edgeID, msg.Subject()):
		f.refuse(ctx, edgeID, msg, reasonForeignSubject, string(signal))
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(f.cfg.Endpoint, "/")+"/v1/"+string(signal), bytes.NewReader(msg.Data()))
	if err != nil {
		f.refuse(ctx, edgeID, msg, reasonCollectorReject, string(signal))
		return
	}
	req.Header.Set("Content-Type", otlpContentType)
	for k, v := range f.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := f.cfg.Client.Do(req)
	if err != nil {
		f.retry(msg, edgeID)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		_ = msg.Ack()
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests:
		f.retry(msg, edgeID)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// A permanent rejection: a malformed or oversized payload, including
		// one an injection produced. Dropping it, not retrying it forever.
		f.refuse(ctx, edgeID, msg, reasonCollectorReject, string(signal))
	default:
		f.retry(msg, edgeID)
	}
}

// retry redelivers a body after the configured delay, or drops it once the
// delivery count crosses the backstop so a persistently failing collector
// cannot wedge the stream.
func (f *Forwarder) retry(msg jetstream.Msg, edgeID string) {
	if meta, err := msg.Metadata(); err == nil && meta.NumDelivered >= forwarderMaxDeliver {
		f.record(edgeID, msg.Subject(), reasonDeliveryExceeded)
		_ = msg.Term()
		return
	}
	_ = msg.NakWithDelay(f.cfg.RetryDelay)
}

// refuse terminates a record the forwarder will not ship and makes the
// refusal visible: a WARN record (rate-limited per edge stream) naming the
// stream's edge and the edge the subject claimed, and a counter labeled by
// the closed reason and signal sets only, since an edge id is an identity
// the cardinality rule keeps off metric attributes. A record refused for a
// foreign subject is the one sign that an agent is trying to relabel
// another edge's data, so it must reach an operator, not only a counter.
func (f *Forwarder) refuse(ctx context.Context, edgeID string, msg jetstream.Msg, reason, signal string) {
	_ = msg.Term()
	f.refused.Add(ctx, 1, metric.WithAttributes(
		attribute.String("flowseer.edgebus.reason", reason),
		attribute.String("flowseer.device.signal", signal),
	))
	f.logRefusal(edgeID, msg.Subject(), reason)
}

// record counts a refusal without a live context, for the redelivery
// backstop.
func (f *Forwarder) record(edgeID, subject, reason string) {
	f.refused.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("flowseer.edgebus.reason", reason),
		attribute.String("flowseer.device.signal", ""),
	))
	f.logRefusal(edgeID, subject, reason)
}

func (f *Forwarder) logRefusal(edgeID, subject, reason string) {
	f.mu.Lock()
	f.dropped++
	now := time.Now()
	logIt := now.Sub(f.lastLogged[edgeID]) >= refusedLogInterval
	if logIt {
		f.lastLogged[edgeID] = now
	}
	f.mu.Unlock()
	if !logIt {
		return
	}
	f.logger.Warn("record refused",
		slog.String("otel.event.name", "flowseer.edgebus.record.refused"),
		slog.String("flowseer.edgebus.reason", reason),
		slog.String("flowseer.edge.id", edgeID),
		slog.String("flowseer.edgebus.claimed_edge_id", claimedEdge(subject)),
		slog.String("flowseer.edgebus.subject", subject),
	)
}

// claimedEdge reads the edge id a subject under the edge subtree layout
// names, or "" when the subject has another shape.
func claimedEdge(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) < 4 || parts[0] != "flowseer" || parts[2] != "edge" {
		return ""
	}
	return parts[3]
}

// Dropped counts records refused: a foreign subject, no OTLP signal, a
// collector rejection, or a body past the delivery backstop.
func (f *Forwarder) Dropped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dropped
}

// Close stops discovery and drains every consumer, waiting for each to
// finish, so no consume goroutine outlives the call.
func (f *Forwarder) Close() {
	f.cancel()
	<-f.done
	f.mu.Lock()
	consumers := f.consumers
	f.consumers = map[string]jetstream.ConsumeContext{}
	f.mu.Unlock()
	for _, consume := range consumers {
		consume.Drain()
		<-consume.Closed()
	}
}
