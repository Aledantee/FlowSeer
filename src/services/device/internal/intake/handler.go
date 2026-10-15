package intake

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"google.golang.org/protobuf/proto"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

const (
	reasonValidation = "validation"
	reasonMarshal    = "marshal"
)

// HandlerConfig wires the delivery handler to central.
type HandlerConfig struct {
	// Central publishes validated records into the central streams.
	Central Publisher
	// EdgeID names the calling edge from the call's verified assertion.
	EdgeID func(context.Context) (string, error)
	// EdgeTenant resolves an edge to its tenant.
	EdgeTenant func(ctx context.Context, edgeID string) (string, error)
	// Logger receives refusal events and retry warnings. Nil discards them.
	Logger *slog.Logger
	// MeterProvider backs the handler's counters and histogram. Nil records
	// nothing.
	MeterProvider metric.MeterProvider
}

// Handler is central's IngestService. It prepares and publishes each delivered
// record as the stream consumer does for a record arriving on the edge's own
// stream. It is safe for concurrent use when its Publisher is.
type Handler struct {
	central    Publisher
	edgeID     func(context.Context) (string, error)
	edgeTenant func(ctx context.Context, edgeID string) (string, error)
	logger     *slog.Logger
	metrics    intakeMetrics
	validator  func(proto.Message) error

	mu         sync.Mutex // guards lastLogged
	lastLogged map[string]time.Time
}

var _ ingestv1connect.IngestServiceHandler = (*Handler)(nil)

// NewHandler builds the delivery handler.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Central == nil {
		return nil, errs.New().Code(ErrCodeStart).Msg("the delivery handler needs a central publisher")
	}
	if cfg.EdgeID == nil || cfg.EdgeTenant == nil {
		return nil, errs.New().Code(ErrCodeStart).Msg("the delivery handler needs edge and tenant resolvers")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	provider := cfg.MeterProvider
	if provider == nil {
		provider = metricnoop.NewMeterProvider()
	}
	metrics, err := newMetrics(provider)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Msg("build delivery instruments")
	}
	return &Handler{
		central: cfg.Central, edgeID: cfg.EdgeID, edgeTenant: cfg.EdgeTenant,
		logger: logger, metrics: metrics,
		validator:  func(msg proto.Message) error { return protovalidate.Validate(msg) },
		lastLogged: map[string]time.Time{},
	}, nil
}

// Deliver publishes the batch's records in order and answers with the count of
// leading records settled. A record is settled when central stores it, already
// holds it, or refuses it for a reason about the record. A failure that is not
// about a record ends the count there and the call still answers, so the edge
// resends from the first unsettled record.
func (h *Handler) Deliver(ctx context.Context, req *connect.Request[edgeingestv1.DeliverRequest]) (*connect.Response[edgeingestv1.DeliverResponse], error) {
	edgeID, err := h.edgeID(ctx)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeEdge).Msg("identify delivering edge"))
	}
	tenantID, err := h.edgeTenant(ctx, edgeID)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeResolve).Attr("edge", edgeID).Msg("resolve edge tenant"))
	}
	if err := tenant.Validate(tenantID); err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeResolve).Attr("edge", edgeID).Attr("tenant", tenantID).Msg("validate edge tenant"))
	}

	var settled uint32
	for _, record := range req.Msg.GetRecords() {
		if !h.deliverOne(ctx, tenantID, edgeID, record) {
			break
		}
		settled++
	}
	return connect.NewResponse(edgeingestv1.DeliverResponse_builder{Settled: &settled}.Build()), nil
}

// deliverOne settles one record and reports whether it did. A false answer is
// a retry the metrics already count.
func (h *Handler) deliverOne(ctx context.Context, tenantID, edgeID string, record *ingestv1.IngestRecord) bool {
	started := time.Now()
	defer func() { h.metrics.duration.Record(ctx, time.Since(started).Seconds()) }()

	data, err := proto.Marshal(record)
	if err != nil {
		h.retry(ctx, edgeID, reasonMarshal)
		return false
	}
	publications, reason, retryType := prepareRecord(tenantID, edgeID, record, data, h.validator)
	if retryType != "" {
		h.retry(ctx, edgeID, retryType)
		return false
	}
	if reason != "" {
		h.metrics.refused.Add(ctx, 1, metric.WithAttributes(attribute.String("flowseer.intake.reason", reason)))
		h.logRefusal(ctx, edgeID, reason)
		return true
	}

	recordType := metric.WithAttributes(attribute.String("flowseer.intake.record_type", publications[0].recordType))
	duplicate := false
	for _, item := range publications {
		ack, err := h.central.Publish(ctx, item.subject, item.data, jetstream.WithMsgID(item.messageID))
		if err != nil {
			reason := reasonPublishFailed
			if !item.evidence && edgebus.IsStreamFull(err) {
				reason = reasonStreamFull
			}
			h.retry(ctx, edgeID, reason)
			return false
		}
		if ack != nil && ack.Duplicate {
			duplicate = true
		}
		if item.evidence {
			h.metrics.evidenceStored.Add(ctx, 1, recordType)
		}
	}
	if duplicate {
		h.metrics.duplicate.Add(ctx, 1, recordType)
	}
	h.metrics.republished.Add(ctx, 1, recordType)
	return true
}

func (h *Handler) retry(ctx context.Context, edgeID, reason string) {
	h.metrics.retried.Add(ctx, 1, metric.WithAttributes(attribute.String("flowseer.intake.reason", reason)))
	if ctx.Err() != nil || !allowEvery(&h.mu, h.lastLogged, edgeID+":"+retryLogKey) {
		return
	}
	h.logger.WarnContext(ctx, "record left for redelivery",
		fsconv.EdgeID(edgeID),
		slog.String(string(semconv.ErrorTypeKey), reason),
	)
}

func (h *Handler) logRefusal(ctx context.Context, edgeID, reason string) {
	if !allowEvery(&h.mu, h.lastLogged, edgeID+":"+refusalLogKey) {
		return
	}
	h.logger.WarnContext(ctx, "record refused",
		fsconv.EventName("flowseer.intake.record.refused"),
		fsconv.EdgeID(edgeID),
		slog.String("flowseer.intake.reason", reason),
	)
}
