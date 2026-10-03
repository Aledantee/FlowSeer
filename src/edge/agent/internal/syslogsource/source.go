package syslogsource

import (
	"context"
	"errors"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/protobuf/proto"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

// Publisher writes records to an edge bus leaf. Satisfied by *edgebus.Leaf.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string) error
	Subject(name string) string
}

// Config configures a syslog Source.
type Config struct {
	Receiver       *syslog.Receiver
	Listeners      []syslog.ListenConfig
	Index          *lanehost.DeviceIndex
	Publisher      Publisher
	EdgeRef        *edgev1.EdgeGlobalRef
	Policy         *RawPolicy
	Meter          metric.Meter
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// Source receives syslog frames from one or more listeners, maps them to IngestRecords,
// applies the raw suppression policy, and publishes them to the edge buffer.
type Source struct {
	receiver       *syslog.Receiver
	ownsReceiver   bool
	index          *lanehost.DeviceIndex
	publisher      Publisher
	edgeRef        *edgev1.EdgeGlobalRef
	policy         *RawPolicy
	initialBackoff time.Duration
	maxBackoff     time.Duration

	published     metric.Int64Counter
	dropped       metric.Int64Counter
	rawKept       metric.Int64Counter
	rawSuppressed metric.Int64Counter
	retries       metric.Int64Counter
}

// Listen binds the configured listeners and returns a ready Source.
func Listen(ctx context.Context, cfg Config) (*Source, error) {
	if cfg.Index == nil {
		return nil, errs.Msg("syslog source requires a device index")
	}
	if cfg.Publisher == nil {
		return nil, errs.Msg("syslog source requires a publisher")
	}

	receiver := cfg.Receiver
	ownsReceiver := false
	if receiver == nil {
		if len(cfg.Listeners) == 0 {
			return nil, errs.Msg("syslog source requires at least one listener")
		}
		var err error
		receiver, err = syslog.Listen(ctx, cfg.Listeners, syslog.ReceiverOptions{
			Parse: syslog.ParseOptions{
				CaptureRaw: true,
			},
			Limits: syslog.Limits{
				MaxPayload: 65535,
			},
		})
		if err != nil {
			return nil, err
		}
		ownsReceiver = true
	}

	meter := cfg.Meter
	if meter == nil {
		meter = noop.NewMeterProvider().Meter("go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource")
	}

	published, err1 := meter.Int64Counter("flowseer.edge.syslog.published",
		metric.WithUnit("{record}"),
		metric.WithDescription("syslog records published to the edge buffer"),
	)
	dropped, err2 := meter.Int64Counter("flowseer.edge.syslog.dropped",
		metric.WithUnit("{record}"),
		metric.WithDescription("syslog records dropped before publish"),
	)
	rawKept, err3 := meter.Int64Counter("flowseer.edge.syslog.raw.kept",
		metric.WithUnit("{record}"),
		metric.WithDescription("raw syslog payloads kept and attached to ingest records"),
	)
	rawSuppressed, err4 := meter.Int64Counter("flowseer.edge.syslog.raw.suppressed",
		metric.WithUnit("{record}"),
		metric.WithDescription("raw syslog payloads suppressed by the raw policy"),
	)
	retries, err5 := meter.Int64Counter("flowseer.edge.syslog.publish.retries",
		metric.WithUnit("{attempt}"),
		metric.WithDescription("syslog publish attempts retried after buffer refusal"),
	)
	if err := errors.Join(err1, err2, err3, err4, err5); err != nil {
		if ownsReceiver {
			_ = receiver.Close()
		}
		return nil, err
	}

	policy := cfg.Policy
	if policy == nil {
		policy = NewRawPolicy(20, 100, time.Now)
	}

	initialBackoff := cfg.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = 25 * time.Millisecond
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 1 * time.Second
	}

	return &Source{
		receiver:       receiver,
		ownsReceiver:   ownsReceiver,
		index:          cfg.Index,
		publisher:      cfg.Publisher,
		edgeRef:        cfg.EdgeRef,
		policy:         policy,
		initialBackoff: initialBackoff,
		maxBackoff:     maxBackoff,
		published:      published,
		dropped:        dropped,
		rawKept:        rawKept,
		rawSuppressed:  rawSuppressed,
		retries:        retries,
	}, nil
}

// Receiver returns the underlying syslog.Receiver.
func (s *Source) Receiver() *syslog.Receiver {
	return s.receiver
}

// Close stops the receiver.
func (s *Source) Close() error {
	return s.receiver.Close()
}

// Run loops on Receiver.Next until ctx is canceled or the receiver closes.
func (s *Source) Run(ctx context.Context) error {
	if s.ownsReceiver {
		defer func() { _ = s.receiver.Close() }()
	}

	for {
		rec, err := s.receiver.Next(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, syslog.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return err
		}

		peerAddr := rec.Observation.Peer.Addr()
		if !peerAddr.IsValid() {
			s.dropped.Add(ctx, 1, metric.WithAttributes(
				attribute.String("flowseer.edge.syslog.reason", "unknown_source"),
			))
			continue
		}
		devEntry, ok := s.index.Lookup(lanehost.Key(peerAddr))
		if !ok {
			s.dropped.Add(ctx, 1, metric.WithAttributes(
				attribute.String("flowseer.edge.syslog.reason", "unknown_source"),
			))
			continue
		}

		syslogRec, prov, isParseFailure := MapRecord(rec, devEntry, s.edgeRef)

		id, err := uuid.NewV7()
		if err != nil {
			return errs.From(err).Msg("generate ingest record id")
		}
		recordID := id.String()

		envBuilder := ingestv1.IngestRecord_builder{
			RecordId:   proto.String(recordID),
			Provenance: prov,
			Syslog:     syslogRec,
		}

		if isParseFailure {
			keep, suppressed := s.policy.Evaluate(devEntry.DeviceID)
			if keep {
				s.rawKept.Add(ctx, 1)
				var rawData []byte
				if rec.Raw != nil {
					rawData = *rec.Raw
				}
				envBuilder.Raw = ingestv1.RawEvidence_builder{
					Data:                rawData,
					Reason:              ingestv1.RawReason_RAW_REASON_PARSE_FAILURE.Enum(),
					SuppressedSinceLast: proto.Uint64(uint64(suppressed)),
				}.Build()
			} else {
				s.rawSuppressed.Add(ctx, 1)
			}
		}

		envelope := envBuilder.Build()
		if err := protovalidate.Validate(envelope); err != nil {
			return errs.From(err).Msg("validate ingest record")
		}

		data, err := proto.Marshal(envelope)
		if err != nil {
			return errs.From(err).Msg("marshal ingest record")
		}

		subject := s.publisher.Subject("ingest.syslog")
		backoff := s.initialBackoff
		for {
			err := s.publisher.Publish(ctx, subject, data, recordID)
			if err == nil {
				s.published.Add(ctx, 1)
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.retries.Add(ctx, 1)

			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			backoff = min(backoff*2, s.maxBackoff)
		}
	}
}
