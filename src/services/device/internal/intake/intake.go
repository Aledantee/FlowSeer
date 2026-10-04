// Package intake moves validated records from edge buffers into central
// ingestion streams.
package intake

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"google.golang.org/protobuf/proto"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// ErrCodeStart identifies a failure to start intake.
var ErrCodeStart = errs.NewCode("intake/start")

const (
	scopeName = "go.aledante.io/FlowSeer/src/services/device/internal/intake"

	defaultRetryDelay        = 5 * time.Second
	defaultDiscoveryInterval = 10 * time.Second
	intakeAckWait            = 30 * time.Second

	consumerName   = "ingest_intake"
	consumerFilter = "flowseer.*.edge.*.ingest.>"

	reasonForeignSubject    = "foreign_subject"
	reasonMalformed         = "malformed"
	reasonInvalid           = "invalid"
	reasonForeignProvenance = "foreign_provenance"

	refusalLogInterval = 10 * time.Second
)

// Publisher is the central JetStream publishing operation intake uses.
type Publisher interface {
	Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// Config wires intake to the hub and the central JetStream account.
type Config struct {
	// Hub identifies attached edge streams and resolves their tenants.
	Hub *edgebus.Hub
	// Central publishes validated records into the central streams.
	Central Publisher
	// RetryDelay is the delay before a publish failure is redelivered. Zero
	// means five seconds.
	RetryDelay time.Duration
	// DiscoveryInterval is how often newly attached edges are discovered. Zero
	// means ten seconds.
	DiscoveryInterval time.Duration
	// Logger receives rate-limited refusal warnings. Nil discards them.
	Logger *slog.Logger
	// MeterProvider backs intake's counters and histograms. Nil records
	// nothing.
	MeterProvider metric.MeterProvider
}

// Intake follows every attached edge stream and republishes valid records to
// central. A failed central publish leaves the source message pending for
// redelivery. Intake is safe for concurrent use.
type Intake struct {
	hub        *edgebus.Hub
	central    Publisher
	retryDelay time.Duration
	logger     *slog.Logger
	metrics    intakeMetrics
	follower   *edgebus.EdgeFollower
	handlerCtx context.Context
	cancel     context.CancelFunc

	mu         sync.Mutex
	lastLogged map[string]time.Time
}

// Start attaches intake to every current edge and discovers edges attached
// later until ctx ends or [Intake.Close] is called. The returned Intake must
// be closed.
func Start(ctx context.Context, cfg Config) (*Intake, error) {
	if cfg.Hub == nil {
		return nil, errs.New().Code(ErrCodeStart).Msg("intake needs a hub")
	}
	if cfg.Central == nil {
		return nil, errs.New().Code(ErrCodeStart).Msg("intake needs a central publisher")
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = defaultRetryDelay
	}
	if cfg.DiscoveryInterval == 0 {
		cfg.DiscoveryInterval = defaultDiscoveryInterval
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
		return nil, errs.From(err).Code(ErrCodeStart).Msg("build intake instruments")
	}
	handlerCtx, cancel := context.WithCancel(ctx)
	i := &Intake{
		hub: cfg.Hub, central: cfg.Central, retryDelay: cfg.RetryDelay,
		logger: logger, metrics: metrics, lastLogged: map[string]time.Time{},
		handlerCtx: handlerCtx, cancel: cancel,
	}
	follower, err := edgebus.FollowEdges(ctx, cfg.Hub, cfg.DiscoveryInterval, func(attachCtx context.Context, edgeID string) (jetstream.ConsumeContext, error) {
		return i.attach(attachCtx, edgeID)
	})
	if err != nil {
		cancel()
		return nil, errs.From(err).Code(ErrCodeStart).Msg("follow edge streams")
	}
	i.follower = follower
	return i, nil
}

// Close stops discovery and drains every intake consumer.
func (i *Intake) Close() {
	if i == nil || i.follower == nil {
		return
	}
	i.cancel()
	i.follower.Close()
}

func (i *Intake) attach(ctx context.Context, edgeID string) (jetstream.ConsumeContext, error) {
	stream, err := i.hub.EdgeStream(ctx, edgeID)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Attr("edge", edgeID).Msg("look up edge stream")
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: consumerFilter,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       intakeAckWait,
		MaxDeliver:    -1,
	})
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Attr("edge", edgeID).Msg("create intake consumer")
	}
	consume, err := consumer.Consume(func(msg jetstream.Msg) { i.handle(edgeID, msg) }, jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
		i.logConsumeError(edgeID, err)
	}))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Attr("edge", edgeID).Msg("start intake consumer")
	}
	return consume, nil
}

func (i *Intake) handle(edgeID string, msg jetstream.Msg) {
	started := time.Now()
	ctx := i.handlerCtx
	i.recordAge(ctx, msg, started)

	tenantID, known := i.hub.EdgeTenant(edgeID)
	if !known {
		// The hub has no tenant for the edge, which says nothing about the
		// record, so it stays in the edge stream for a later delivery.
		i.retry(ctx, msg, started)
		return
	}
	publications, reason, err := prepare(tenantID, edgeID, msg.Subject(), msg.Data())
	if err != nil {
		i.retry(ctx, msg, started)
		return
	}
	if reason != "" {
		_ = msg.Term()
		i.metrics.refused.Add(ctx, 1, metric.WithAttributes(attribute.String("flowseer.intake.reason", reason)))
		i.logRefusal(ctx, edgeID, msg.Subject(), reason)
		i.metrics.duration.Record(ctx, time.Since(started).Seconds())
		return
	}

	recordType := metric.WithAttributes(attribute.String("flowseer.intake.record_type", publications[0].recordType))
	duplicate := false
	for _, item := range publications {
		ack, err := i.central.Publish(ctx, item.subject, item.data, jetstream.WithMsgID(item.messageID))
		if err != nil {
			i.retry(ctx, msg, started)
			return
		}
		if ack != nil && ack.Duplicate {
			duplicate = true
		}
		if item.evidence {
			i.metrics.evidenceStored.Add(ctx, 1, recordType)
		}
	}
	// One count per delivery, though a record with evidence makes two
	// publishes that can each report a duplicate.
	if duplicate {
		i.metrics.duplicate.Add(ctx, 1, recordType)
	}
	_ = msg.Ack()
	i.metrics.republished.Add(ctx, 1, recordType)
	i.metrics.duration.Record(ctx, time.Since(started).Seconds())
}

func (i *Intake) retry(ctx context.Context, msg jetstream.Msg, started time.Time) {
	_ = msg.NakWithDelay(i.retryDelay)
	i.metrics.retried.Add(ctx, 1)
	i.metrics.duration.Record(ctx, time.Since(started).Seconds())
}

func (i *Intake) recordAge(ctx context.Context, msg jetstream.Msg, deliveredAt time.Time) {
	metadata, err := msg.Metadata()
	if err != nil || metadata == nil {
		return
	}
	age := deliveredAt.Sub(metadata.Timestamp)
	if age < 0 {
		age = 0
	}
	i.metrics.age.Record(ctx, age.Seconds())
}

func (i *Intake) logRefusal(ctx context.Context, edgeID, subject, reason string) {
	i.mu.Lock()
	now := time.Now()
	previous := i.lastLogged[edgeID]
	if now.Sub(previous) < refusalLogInterval {
		i.mu.Unlock()
		return
	}
	i.lastLogged[edgeID] = now
	i.mu.Unlock()
	i.logger.WarnContext(ctx, "record refused",
		slog.String("otel.event.name", "flowseer.intake.record.refused"),
		slog.String("flowseer.edge.id", edgeID),
		slog.String("flowseer.intake.reason", reason),
		slog.String("flowseer.intake.subject", subject),
	)
}

func (i *Intake) logConsumeError(edgeID string, err error) {
	errorType := "consume_error"
	switch {
	case errors.Is(err, jetstream.ErrConsumerDeleted):
		errorType = "consumer_deleted"
	case errors.Is(err, jetstream.ErrBadRequest):
		errorType = "bad_request"
	}
	i.logger.WarnContext(i.handlerCtx, "intake consumer stopped",
		slog.String("flowseer.edge.id", edgeID),
		slog.String(string(semconv.ErrorTypeKey), errorType),
	)
}

type publication struct {
	subject    string
	data       []byte
	messageID  string
	recordType string
	evidence   bool
}

// prepare checks and converts one edge delivery without depending on
// JetStream. It returns the evidence publication first when raw bytes are
// attached, so a redelivery after the typed publication fails is duplicate
// safe in both central streams.
func prepare(tenantID, edgeID, subject string, data []byte) ([]publication, string, error) {
	return prepareWithValidator(tenantID, edgeID, subject, data, func(msg proto.Message) error {
		return protovalidate.Validate(msg)
	})
}

func prepareWithValidator(tenantID, edgeID, subject string, data []byte, validate func(proto.Message) error) ([]publication, string, error) {
	if !strings.HasPrefix(subject, edgebus.EdgeSubtree(tenantID, edgeID)+".ingest.") {
		return nil, reasonForeignSubject, nil
	}
	record := &ingestv1.IngestRecord{}
	if err := proto.Unmarshal(data, record); err != nil {
		return nil, reasonMalformed, nil
	}
	if err := validate(record); err != nil {
		var validationErr *protovalidate.ValidationError
		if !errors.As(err, &validationErr) {
			return nil, "", err
		}
		return nil, reasonInvalid, nil
	}
	if record.GetProvenance().GetEdge().GetEdge().GetId() != edgeID {
		return nil, reasonForeignProvenance, nil
	}

	recordType, deviceID := recordTypeAndDevice(record)
	if recordType == "" || deviceID == "" {
		return nil, reasonInvalid, nil
	}
	messageID := tenantID + "." + record.GetRecordId()
	typed := data
	publications := make([]publication, 0, 2)
	if record.GetRaw() != nil {
		publications = append(publications, publication{
			subject: edgebus.EvidenceSubject(tenantID, recordType, deviceID), data: data,
			messageID: messageID, recordType: recordType, evidence: true,
		})
		withoutRaw := proto.Clone(record).(*ingestv1.IngestRecord)
		withoutRaw.ClearRaw()
		encoded, err := proto.Marshal(withoutRaw)
		if err != nil {
			return nil, "", err
		}
		typed = encoded
	}
	publications = append(publications, publication{
		subject: edgebus.CentralIngestSubject(tenantID, recordType, deviceID), data: typed,
		messageID: messageID, recordType: recordType,
	})
	return publications, "", nil
}

func recordTypeAndDevice(record *ingestv1.IngestRecord) (string, string) {
	if syslog := record.GetSyslog(); syslog != nil {
		return edgebus.IngestRecordTypeSyslog, syslog.GetDevice().GetDevice().GetId()
	}
	return "", ""
}

type intakeMetrics struct {
	republished    metric.Int64Counter
	duplicate      metric.Int64Counter
	evidenceStored metric.Int64Counter
	refused        metric.Int64Counter
	retried        metric.Int64Counter
	duration       metric.Float64Histogram
	age            metric.Float64Histogram
}

func newMetrics(provider metric.MeterProvider) (intakeMetrics, error) {
	meter := provider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL))
	republished, err := meter.Int64Counter("flowseer.intake.records.republished", metric.WithUnit("{record}"), metric.WithDescription("Records acknowledged after central republish"))
	if err != nil {
		return intakeMetrics{}, err
	}
	duplicate, err := meter.Int64Counter("flowseer.intake.records.duplicate", metric.WithUnit("{record}"), metric.WithDescription("Central publish acknowledgements marked duplicate"))
	if err != nil {
		return intakeMetrics{}, err
	}
	evidenceStored, err := meter.Int64Counter("flowseer.intake.evidence.stored", metric.WithUnit("{record}"), metric.WithDescription("Raw evidence publish acknowledgements"))
	if err != nil {
		return intakeMetrics{}, err
	}
	refused, err := meter.Int64Counter("flowseer.intake.records.refused", metric.WithUnit("{record}"), metric.WithDescription("Records terminated after an intake refusal"))
	if err != nil {
		return intakeMetrics{}, err
	}
	retried, err := meter.Int64Counter("flowseer.intake.records.retried", metric.WithUnit("{record}"), metric.WithDescription("Records negatively acknowledged for retry"))
	if err != nil {
		return intakeMetrics{}, err
	}
	duration, err := meter.Float64Histogram("flowseer.intake.record.duration", metric.WithUnit("s"), metric.WithDescription("Time from delivery to the final intake acknowledgement"))
	if err != nil {
		return intakeMetrics{}, err
	}
	age, err := meter.Float64Histogram("flowseer.intake.record.age", metric.WithUnit("s"), metric.WithDescription("Age of a record when intake receives it"))
	if err != nil {
		return intakeMetrics{}, err
	}
	return intakeMetrics{
		republished: republished, duplicate: duplicate, evidenceStored: evidenceStored,
		refused: refused, retried: retried, duration: duration, age: age,
	}, nil
}
