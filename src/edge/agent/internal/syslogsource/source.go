// Package syslogsource receives syslog from the devices the agent hosts, maps
// each message into an ingest record, and publishes it to the edge buffer.
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
//
// Policy defaults to 20 failures per device per minute, sampling 1 in 100 thereafter.
// Meter defaults to an OpenTelemetry no-op meter.
// InitialBackoff defaults to 25ms.
// MaxBackoff defaults to 1s.
type Config struct {
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
// Run is called once: a second concurrent Run gets syslog.ErrBusy from the
// receiver and its return closes the receiver under the first. Close and
// Receiver may be called from other goroutines.
type Source struct {
	receiver       *syslog.Receiver
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

// ReceiverOptions returns the parse options and limits the source's receiver
// runs with. MapRecord's contract holds for every payload they accept.
func ReceiverOptions() syslog.ReceiverOptions {
	return syslog.ReceiverOptions{
		Parse:  syslog.ParseOptions{CaptureRaw: true},
		Limits: syslog.Limits{MaxPayload: 65535},
	}
}

// Listen binds the configured listeners and returns a ready Source.
func Listen(ctx context.Context, cfg Config) (*Source, error) {
	if cfg.EdgeRef.GetEdge().GetId() == "" {
		return nil, errs.Msg("syslog source requires an edge reference with an id")
	}
	if cfg.Index == nil {
		return nil, errs.Msg("syslog source requires a device index")
	}
	if cfg.Publisher == nil {
		return nil, errs.Msg("syslog source requires a publisher")
	}
	if len(cfg.Listeners) == 0 {
		return nil, errs.Msg("syslog source requires at least one listener")
	}

	receiver, err := syslog.Listen(ctx, cfg.Listeners, ReceiverOptions())
	if err != nil {
		return nil, err
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
		_ = receiver.Close()
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

// Run loops on Receiver.Next until ctx is canceled or the receiver closes, and
// closes the receiver on return. It returns nil when ctx is canceled or the
// receiver is closed. It returns the receiver's terminal listener error when a
// listener fails, and a non-nil error if record ID generation, record
// validation, or marshaling fails.
func (s *Source) Run(ctx context.Context) error {
	defer func() { _ = s.receiver.Close() }()

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
		devEntry, res := s.index.Lookup(peerAddr.String())
		switch res {
		case lanehost.LookupFound:
		case lanehost.LookupAmbiguous:
			s.dropped.Add(ctx, 1, metric.WithAttributes(
				attribute.String("flowseer.edge.syslog.reason", "ambiguous_source"),
			))
			continue
		default:
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

		var (
			keepRaw    bool
			suppressed uint64
			rawData    []byte
		)
		if isParseFailure {
			var keep bool
			keep, suppressed = s.policy.Evaluate(devEntry.DeviceID)
			if keep {
				s.rawKept.Add(ctx, 1)
				keepRaw = true
				if rec.Raw != nil {
					rawData = *rec.Raw
				}
			} else {
				s.rawSuppressed.Add(ctx, 1)
			}
		}

		envelope := BuildEnvelope(recordID, prov, syslogRec, rawData, keepRaw, suppressed)
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
				return nil
			}
			s.retries.Add(ctx, 1)

			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			backoff = min(backoff*2, s.maxBackoff)
		}
	}
}
