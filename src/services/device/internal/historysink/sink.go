// Package historysink moves syslog records from the central ingest stream into
// the history store. It follows the stream with a durable consumer, maps each
// record to a row, inserts batches synchronously, and acknowledges a record
// only after the store holds it.
package historysink

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"google.golang.org/protobuf/proto"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// ErrCodeStart is a sink that cannot attach to the ingest stream.
var ErrCodeStart = errs.NewCode("historysink/start")

const (
	consumerName    = "history_sink"
	maxAckPending   = 10000
	ackWait         = 2 * time.Minute
	insertTimeout   = 30 * time.Second
	logInterval     = 10 * time.Second
	discardInterval = 10 * time.Second

	defaultMaxBatch      = 10000
	defaultFlushInterval = time.Second
	defaultRetryDelay    = 5 * time.Second

	reasonRecord   = "record"
	reasonRejected = "rejected"
	reasonStore    = "store"
	reasonConsumer = "consumer"
	reasonMirror   = "mirror"

	errorRejected = "rejected"
	errorStore    = "store"
)

// Config wires the sink to the central JetStream account and the store.
type Config struct {
	// JetStream is the central account that holds the ingest stream.
	JetStream jetstream.JetStream
	// Inserter writes batches to the history store.
	Inserter Inserter
	// Retention returns the retention class of a tenant. An error is
	// transient: the records of that tenant wait for a later delivery. A
	// tenant with no stored class takes [identityv1.RetentionClass_RETENTION_CLASS_STANDARD].
	Retention func(ctx context.Context, tenantID string) (identityv1.RetentionClass, error)
	// Location returns the UUID of the location the registry places a device
	// in, or empty when it is unplaced or unknown. Nil stamps no location.
	Location func(deviceID string) string
	// Mirror copies the registry's devices into the store. The sink calls it
	// until it succeeds, so a store that is down at start is mirrored once it
	// returns. Nil means no mirror.
	Mirror func(ctx context.Context) error
	// MaxBatch bounds the records of one insert. Zero means 10,000.
	MaxBatch int
	// FlushInterval is how long a batch waits to fill. Zero means one second.
	FlushInterval time.Duration
	// RetryDelay is the delay before a record the store did not take is
	// redelivered, and the pause before the next fetch. Zero means five
	// seconds.
	RetryDelay time.Duration
	// Logger receives retry and refusal warnings. Nil discards them.
	Logger *slog.Logger
	// MeterProvider backs the sink's instruments. Nil records nothing.
	MeterProvider metric.MeterProvider
}

// Sink is the consumer loop. It is not safe for concurrent use: one goroutine
// calls [Sink.Run].
type Sink struct {
	cfg      Config
	stream   jetstream.Stream
	consumer jetstream.Consumer
	logger   *slog.Logger
	metrics  *sinkMetrics

	classes  map[string]uint8
	mirrored bool

	logMu      sync.Mutex // guards lastLogged
	lastLogged map[string]time.Time
}

// Start attaches the durable consumer to the ingest stream. It does not touch
// the store, so a sink starts while ClickHouse is down.
func Start(ctx context.Context, cfg Config) (*Sink, error) {
	if cfg.JetStream == nil || cfg.Inserter == nil || cfg.Retention == nil {
		return nil, errs.New().Code(ErrCodeStart).Msg("the history sink needs a JetStream account, an inserter, and a retention lookup")
	}
	if cfg.MaxBatch <= 0 {
		cfg.MaxBatch = defaultMaxBatch
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultFlushInterval
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = defaultRetryDelay
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
		return nil, errs.From(err).Code(ErrCodeStart).Msg("build the history sink instruments")
	}

	stream, err := cfg.JetStream.Stream(ctx, edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Msg("open the syslog ingest stream")
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       ackWait,
		MaxDeliver:    -1,
		MaxAckPending: maxAckPending,
	})
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Msg("create the history sink consumer")
	}
	return &Sink{
		cfg: cfg, stream: stream, consumer: consumer, logger: logger, metrics: metrics,
		classes: map[string]uint8{}, lastLogged: map[string]time.Time{},
	}, nil
}

// Run follows the stream until ctx ends and returns nil. A store that is down
// or refusing never ends it: records the store did not take stay in the stream
// and are tried again after the retry delay.
func (s *Sink) Run(ctx context.Context) error {
	var lastDiscardPoll time.Time
	for ctx.Err() == nil {
		if time.Since(lastDiscardPoll) >= discardInterval {
			s.pollDiscarded(ctx)
			lastDiscardPoll = time.Now()
		}
		s.mirror(ctx)

		msgs, err := s.fetch()
		if err != nil {
			if ctx.Err() == nil {
				s.warn("consumer error", reasonConsumer)
				sleep(ctx, s.cfg.RetryDelay)
			}
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		if s.process(ctx, msgs) {
			sleep(ctx, s.cfg.RetryDelay)
		}
	}
	return nil
}

func (s *Sink) fetch() ([]jetstream.Msg, error) {
	batch, err := s.consumer.Fetch(s.cfg.MaxBatch, jetstream.FetchMaxWait(s.cfg.FlushInterval))
	if err != nil {
		return nil, err
	}
	var msgs []jetstream.Msg
	for msg := range batch.Messages() {
		msgs = append(msgs, msg)
	}
	return msgs, batch.Error()
}

func (s *Sink) mirror(ctx context.Context) {
	if s.mirrored || s.cfg.Mirror == nil {
		return
	}
	if err := s.cfg.Mirror(ctx); err != nil {
		if ctx.Err() == nil {
			s.warn("device mirror failed", reasonMirror)
		}
		return
	}
	s.mirrored = true
}

// pending is one delivered record that is still unacknowledged.
type pending struct {
	msg      jetstream.Msg
	row      Row
	sequence uint64
}

// process maps and inserts one fetched batch. It reports whether the store or
// a lookup failed, so the caller pauses before the next fetch.
func (s *Sink) process(ctx context.Context, msgs []jetstream.Msg) (failed bool) {
	now := time.Now()
	batch := make([]pending, 0, len(msgs))
	for _, msg := range msgs {
		meta, err := msg.Metadata()
		if err != nil {
			s.refuse(ctx, msg, reasonRecord)
			continue
		}
		age := max(now.Sub(meta.Timestamp), 0)
		s.metrics.age.Record(ctx, age.Seconds())

		row, err := s.row(ctx, msg)
		if err != nil {
			if errs.Retryable(err) {
				s.nak(ctx, msg)
				failed = true
				continue
			}
			s.refuse(ctx, msg, reasonRecord)
			continue
		}
		batch = append(batch, pending{msg: msg, row: row, sequence: meta.Sequence.Stream})
	}
	if len(batch) > 0 && s.insert(ctx, batch) {
		failed = true
	}
	return failed
}

func (s *Sink) row(ctx context.Context, msg jetstream.Msg) (Row, error) {
	tenantID, ok := subjectTenant(msg.Subject())
	if !ok {
		return Row{}, errs.New().Code(ErrCodeRecord).Msg("subject names no tenant")
	}
	record := &ingestv1.IngestRecord{}
	if err := proto.Unmarshal(msg.Data(), record); err != nil {
		return Row{}, errs.From(err).Code(ErrCodeRecord).Msg("decode ingest record")
	}
	class, err := s.class(ctx, tenantID)
	if err != nil {
		return Row{}, errs.From(err).Retryable().Msg("look up the retention class")
	}
	location := ""
	if s.cfg.Location != nil {
		location = s.cfg.Location(record.GetSyslog().GetDevice().GetDevice().GetId())
	}
	return mapRecord(tenantID, record, location, class)
}

// subjectTenant returns token 1 of flowseer.<tenant>.ingest.syslog.<device>.
func subjectTenant(subject string) (string, bool) {
	tokens := strings.SplitN(subject, ".", 3)
	if len(tokens) < 3 || tokens[0] != "flowseer" || tokens[1] == "" {
		return "", false
	}
	return tokens[1], true
}

// class returns the tenant's retention class number, cached once known.
func (s *Sink) class(ctx context.Context, tenantID string) (uint8, error) {
	if class, ok := s.classes[tenantID]; ok {
		return class, nil
	}
	found, err := s.cfg.Retention(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if found == identityv1.RetentionClass_RETENTION_CLASS_UNSPECIFIED {
		found = identityv1.RetentionClass_RETENTION_CLASS_STANDARD
	}
	s.classes[tenantID] = uint8(found)
	return uint8(found), nil
}

// insert writes the batch and acknowledges what the store took. A rejection
// splits the batch to isolate the record that causes it. It reports whether a
// transient failure left records unacknowledged.
func (s *Sink) insert(ctx context.Context, batch []pending) (failed bool) {
	rows := make([]Row, len(batch))
	sequences := make([]uint64, len(batch))
	for i, p := range batch {
		rows[i] = p.row
		sequences[i] = p.sequence
	}

	started := time.Now()
	insertCtx, cancel := context.WithTimeout(ctx, insertTimeout)
	err := s.cfg.Inserter.Insert(insertCtx, rows, batchToken(sequences))
	cancel()
	s.recordBatch(ctx, started, err)

	switch {
	case err == nil:
		for _, p := range batch {
			_ = p.msg.Ack()
		}
		s.metrics.inserted.Add(ctx, int64(len(batch)))
		return false
	case IsRejected(err) && len(batch) == 1:
		s.refuse(ctx, batch[0].msg, reasonRejected)
		return false
	case IsRejected(err):
		half := len(batch) / 2
		if s.insert(ctx, batch[:half]) {
			s.nakAll(ctx, batch[half:])
			return true
		}
		return s.insert(ctx, batch[half:])
	default:
		s.nakAll(ctx, batch)
		if s.allow(reasonStore) {
			s.logger.Warn("insert retry scheduled",
				slog.String(string(semconv.ErrorTypeKey), errorStore),
				slog.String("flowseer.history.reason", reasonStore))
		}
		return true
	}
}

func (s *Sink) recordBatch(ctx context.Context, started time.Time, err error) {
	var attrs []attribute.KeyValue
	if err != nil {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errorTypeOf(err)))
	}
	s.metrics.batch.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attrs...))
}

func errorTypeOf(err error) string {
	if IsRejected(err) {
		return errorRejected
	}
	return errorStore
}

func (s *Sink) nak(ctx context.Context, msg jetstream.Msg) {
	_ = msg.NakWithDelay(s.cfg.RetryDelay)
	s.metrics.retried.Add(ctx, 1)
}

func (s *Sink) nakAll(ctx context.Context, batch []pending) {
	for _, p := range batch {
		s.nak(ctx, p.msg)
	}
}

func (s *Sink) refuse(ctx context.Context, msg jetstream.Msg, reason string) {
	_ = msg.Term()
	s.metrics.refused.Add(ctx, 1, metric.WithAttributes(attribute.String("flowseer.history.reason", reason)))
	s.warn("record refused", reason)
}

// pollDiscarded samples how many records the stream removed before the
// consumer acknowledged them.
func (s *Sink) pollDiscarded(ctx context.Context) {
	streamInfo, err := s.stream.Info(ctx)
	if err != nil {
		return
	}
	consumerInfo, err := s.consumer.Info(ctx)
	if err != nil {
		return
	}
	var gap uint64
	if first := streamInfo.State.FirstSeq; first > 0 && first-1 > consumerInfo.AckFloor.Stream {
		gap = first - 1 - consumerInfo.AckFloor.Stream
	}
	s.metrics.discarded.Store(int64(min(gap, math.MaxInt64)))
}

// allow rate-limits a warning: it reports whether key last logged at least
// one interval ago, so a store outage does not write a line per batch.
func (s *Sink) allow(key string) bool {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if time.Since(s.lastLogged[key]) < logInterval {
		return false
	}
	s.lastLogged[key] = time.Now()
	return true
}

func (s *Sink) warn(message, reason string) {
	if s.allow(reason) {
		s.logger.Warn(message, slog.String("flowseer.history.reason", reason))
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
