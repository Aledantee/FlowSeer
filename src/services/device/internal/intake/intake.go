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
	"go.aledante.io/FlowSeer/src/common/fsconv"
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

	reasonStreamFull    = "stream_full"
	reasonPublishFailed = "publish_failed"
	reasonUnknownEdge   = "unknown_edge"

	refusalLogInterval = 10 * time.Second
	refusalLogKey      = "refusal"
	retryLogKey        = "retry"
	consumeLogKey      = "consume_error"
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
	// Logger receives refusal events, retry warnings, and consumer error
	// messages. Nil discards them.
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
	validator  func(proto.Message) error

	mu         sync.Mutex // guards lastLogged
	lastLogged map[string]time.Time

	gateMu       sync.Mutex // guards retryAfter and retryCounted
	retryAfter   map[string]time.Time
	retryCounted map[string]map[string]bool
}

// Start starts intake for every attached edge stream and republishes valid
// records to central. Discovery ends when ctx ends or [Intake.Close] is called.
// Delivery uses a lifetime that ends when ctx ends, [Intake.Close] is called,
// or the first discovery pass fails, before the drain. A delivery between ctx
// ending and [Intake.Close] is retried. An interval attach failure does not end
// the lifetime. The returned Intake must be closed.
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
	i := &Intake{
		hub: cfg.Hub, central: cfg.Central, retryDelay: cfg.RetryDelay,
		logger: logger, metrics: metrics,
		validator:    func(msg proto.Message) error { return protovalidate.Validate(msg) },
		lastLogged:   map[string]time.Time{},
		retryAfter:   map[string]time.Time{},
		retryCounted: map[string]map[string]bool{},
	}
	follower, err := edgebus.FollowEdges(ctx, cfg.Hub, cfg.DiscoveryInterval, func(attachCtx, lifetime context.Context, edgeID string) (jetstream.ConsumeContext, error) {
		return i.attach(attachCtx, lifetime, edgeID)
	})
	if err != nil {
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
	i.follower.Close()
}

func (i *Intake) attach(ctx, lifetime context.Context, edgeID string) (jetstream.ConsumeContext, error) {
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
	consume, err := consumer.Consume(func(msg jetstream.Msg) { i.handle(lifetime, edgeID, msg) }, jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
		i.logConsumeError(lifetime, edgeID, err)
	}))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Attr("edge", edgeID).Msg("start intake consumer")
	}
	return consume, nil
}

func (i *Intake) handle(ctx context.Context, edgeID string, msg jetstream.Msg) {
	started := time.Now()
	i.recordAge(ctx, msg, started)

	tenantID, known := i.hub.EdgeTenant(edgeID)
	if !known {
		// The hub has no tenant for the edge, which says nothing about the
		// record, so it stays in the edge stream for a later delivery.
		i.retry(ctx, msg, started, reasonUnknownEdge)
		return
	}
	publications, reason, retryType := prepare(tenantID, edgeID, msg.Subject(), msg.Data(), i.validator)
	if retryType != "" {
		i.retry(ctx, msg, started, retryType)
		i.logRetry(ctx, edgeID, msg.Subject(), retryType)
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
	if i.gated(ctx, msg, started, publications[0]) {
		return
	}
	duplicate := false
	for _, item := range publications {
		ack, err := i.central.Publish(ctx, item.subject, item.data, jetstream.WithMsgID(item.messageID))
		if err != nil {
			reason := reasonPublishFailed
			if !item.evidence && edgebus.IsStreamFull(err) {
				reason = reasonStreamFull
				i.closeGate(item)
			}
			i.retry(ctx, msg, started, reason)
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

// gated negatively acknowledges a record whose type is inside the retry delay
// that a full-stream refusal opened, and reports whether it did. The first
// delivery of each message id in the delay counts one retry and later ones none.
func (i *Intake) gated(ctx context.Context, msg jetstream.Msg, started time.Time, typed publication) bool {
	i.gateMu.Lock()
	if !time.Now().Before(i.retryAfter[typed.recordType]) {
		delete(i.retryCounted, typed.recordType)
		i.gateMu.Unlock()
		return false
	}
	counted := i.retryCounted[typed.recordType][typed.messageID]
	i.retryCounted[typed.recordType][typed.messageID] = true
	i.gateMu.Unlock()
	if counted {
		_ = msg.NakWithDelay(i.retryDelay)
		i.metrics.duration.Record(ctx, time.Since(started).Seconds())
		return true
	}
	i.retry(ctx, msg, started, reasonStreamFull)
	return true
}

// closeGate stops publishing the refused record's type for one retry delay.
// The refusing delivery is counted by its own retry. A refusal inside a closed
// gate, from a handler that passed gated before the gate closed, extends the
// gate and keeps the ids already counted.
func (i *Intake) closeGate(refused publication) {
	i.gateMu.Lock()
	defer i.gateMu.Unlock()
	now := time.Now()
	if !now.Before(i.retryAfter[refused.recordType]) || i.retryCounted[refused.recordType] == nil {
		i.retryCounted[refused.recordType] = map[string]bool{}
	}
	i.retryCounted[refused.recordType][refused.messageID] = true
	i.retryAfter[refused.recordType] = now.Add(i.retryDelay)
}

func (i *Intake) retry(ctx context.Context, msg jetstream.Msg, started time.Time, reason string) {
	_ = msg.NakWithDelay(i.retryDelay)
	i.metrics.retried.Add(ctx, 1, metric.WithAttributes(attribute.String("flowseer.intake.reason", reason)))
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
	if !i.allowLog(edgeID + ":" + refusalLogKey) {
		return
	}
	i.logger.WarnContext(ctx, "record refused",
		fsconv.EventName("flowseer.intake.record.refused"),
		fsconv.EdgeID(edgeID),
		slog.String("flowseer.intake.reason", reason),
		slog.String("flowseer.intake.subject", subject),
	)
}

func (i *Intake) logRetry(ctx context.Context, edgeID, subject, errorType string) {
	if ctx.Err() != nil || !i.allowLog(edgeID+":"+retryLogKey) {
		return
	}
	i.logger.WarnContext(ctx, "record retry scheduled",
		fsconv.EdgeID(edgeID),
		slog.String("flowseer.intake.subject", subject),
		slog.String(string(semconv.ErrorTypeKey), errorType),
	)
}

func (i *Intake) allowLog(key string) bool {
	return allowEvery(&i.mu, i.lastLogged, key)
}

// allowEvery reports whether key last logged at least refusalLogInterval ago,
// and records now as its last time when it did. last is guarded by mu.
func allowEvery(mu *sync.Mutex, last map[string]time.Time, key string) bool {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	if now.Sub(last[key]) < refusalLogInterval {
		return false
	}
	last[key] = now
	return true
}

func (i *Intake) logConsumeError(ctx context.Context, edgeID string, err error) {
	errorType, terminal := consumeErrorType(err)
	if ctx.Err() != nil || (!terminal && !i.allowLog(edgeID+":"+consumeLogKey)) {
		return
	}
	message := "intake consumer error"
	if terminal {
		message = "intake consumer stopped"
	}
	i.logger.WarnContext(ctx, message,
		fsconv.EdgeID(edgeID),
		slog.String(string(semconv.ErrorTypeKey), errorType),
	)
}

func consumeErrorType(err error) (string, bool) {
	switch {
	case errors.Is(err, jetstream.ErrConsumerDeleted):
		return "consumer_deleted", true
	case errors.Is(err, jetstream.ErrBadRequest):
		return "bad_request", true
	case errors.Is(err, jetstream.ErrConnectionClosed):
		return "connection_closed", true
	case errors.Is(err, jetstream.ErrNoHeartbeat):
		return "no_heartbeat", false
	}
	return "consume_error", false
}

type publication struct {
	subject    string
	data       []byte
	messageID  string
	recordType string
	evidence   bool
}

// prepare checks and converts one edge stream delivery without depending on JetStream.
// It returns the typed publication first, so a full typed stream refuses a
// record before its evidence is stored and a retry never stores evidence again.
func prepare(tenantID, edgeID, subject string, data []byte, validate func(proto.Message) error) ([]publication, string, string) {
	if !strings.HasPrefix(subject, edgebus.EdgeSubtree(tenantID, edgeID)+".ingest.") {
		return nil, reasonForeignSubject, ""
	}
	record := &ingestv1.IngestRecord{}
	if err := proto.Unmarshal(data, record); err != nil {
		return nil, reasonMalformed, ""
	}
	return prepareRecord(tenantID, edgeID, record, data, validate)
}

// prepareRecord checks and converts one decoded record for the edge and tenant
// it arrived from, whether it came off the edge's stream or through the
// delivery call. data is the record's encoding, which is the evidence
// publication's payload. It returns what [prepare] returns, so both paths
// refuse and publish alike.
func prepareRecord(tenantID, edgeID string, record *ingestv1.IngestRecord, data []byte, validate func(proto.Message) error) ([]publication, string, string) {
	if err := validate(record); err != nil {
		var validationErr *protovalidate.ValidationError
		if !errors.As(err, &validationErr) {
			return nil, "", "validation"
		}
		return nil, reasonInvalid, ""
	}
	if record.GetProvenance().GetEdge().GetEdge().GetId() != edgeID {
		return nil, reasonForeignProvenance, ""
	}

	recordType, deviceID := recordTypeAndDevice(record)
	if recordType == "" || deviceID == "" {
		return nil, reasonInvalid, ""
	}
	messageID := tenantID + "." + record.GetRecordId()
	typed := data
	var evidence *publication
	if record.GetRaw() != nil {
		evidence = &publication{
			subject: edgebus.EvidenceSubject(tenantID, recordType, deviceID), data: data,
			messageID: messageID, recordType: recordType, evidence: true,
		}
		withoutRaw := proto.Clone(record).(*ingestv1.IngestRecord)
		withoutRaw.ClearRaw()
		encoded, err := proto.Marshal(withoutRaw)
		if err != nil {
			return nil, "", "marshal"
		}
		typed = encoded
	}
	publications := make([]publication, 0, 2)
	publications = append(publications, publication{
		subject: edgebus.CentralIngestSubject(tenantID, recordType, deviceID), data: typed,
		messageID: messageID, recordType: recordType,
	})
	if evidence != nil {
		publications = append(publications, *evidence)
	}
	return publications, "", ""
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
	retried, err := meter.Int64Counter("flowseer.intake.records.retried", metric.WithUnit("{record}"), metric.WithDescription("Records left for redelivery, by a stream negative acknowledgement or an unsettled delivery call"))
	if err != nil {
		return intakeMetrics{}, err
	}
	duration, err := meter.Float64Histogram("flowseer.intake.record.duration", metric.WithUnit("s"), metric.WithDescription("Time from delivery to the final outcome of a record, on the stream or the delivery call"))
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
