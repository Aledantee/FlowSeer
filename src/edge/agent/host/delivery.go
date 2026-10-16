package host

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/drain"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/seglog"
)

func openDeliveryLogs(cfg *Config) (*seglog.Log, *seglog.Log, error) {
	ingestBytes, ingestAge := cfg.IngestBuffer()
	ingest, err := seglog.Open(seglog.Config{
		Dir:            filepath.Join(cfg.StateDir(), "buffer", "ingest"),
		MaxBytes:       ingestBytes,
		MaxAge:         ingestAge,
		SegmentBytes:   defaultSegmentBytes,
		MaxRecordBytes: defaultRecordBytes,
	})
	if err != nil {
		return nil, nil, err
	}
	telemetryBytes, telemetryAge := cfg.TelemetryBuffer()
	telemetry, err := seglog.Open(seglog.Config{
		Dir:            filepath.Join(cfg.StateDir(), "buffer", "telemetry"),
		MaxBytes:       telemetryBytes,
		MaxAge:         telemetryAge,
		SegmentBytes:   defaultSegmentBytes,
		MaxRecordBytes: defaultRecordBytes,
	})
	if err != nil {
		_ = ingest.Close()
		return nil, nil, err
	}
	return ingest, telemetry, nil
}

// Each drain is supervised separately so a storage fault in one log leaves the
// other draining. The logs live across attempts and close after the runtime.
type deliveryAssembly struct {
	name      string
	log       drain.Log
	send      drain.Send
	decodable func([]byte) bool
}

func (a deliveryAssembly) module() service.Module {
	// The runtime requires finite positive budgets. A machine-sized count per
	// minute leaves backoff as the practical restart limit for faults and panics.
	// The branch keeps those restarts out of the root's intensity budget.
	budget := service.RestartBudget{Max: math.MaxInt, Window: time.Minute}
	return service.Module{
		Name: "delivery_" + a.name,
		Branch: &service.Branch{
			Intensity: budget,
			Children: []service.Module{{
				Name: "drain",
				Policy: service.Policy{
					Error: service.OutcomePolicy{Budget: budget},
					Panic: service.OutcomePolicy{Budget: budget},
				},
				Leaf: &service.Leaf{Setup: a.setup},
			}},
		},
	}
}

func (a deliveryAssembly) setup(ctx context.Context) (service.Attempt, error) {
	meter := service.Meter(ctx)
	return service.Attempt{Runner: func(ctx context.Context) error {
		registration, err := registerBufferInstruments(meter, a.name, a.log.Stats)
		if err != nil {
			return err
		}
		defer func() { _ = registration.Unregister() }()
		return drain.Run(ctx, drain.Config{
			LogName:   a.name,
			Log:       a.log,
			Send:      a.send,
			Decodable: a.decodable,
			Meter:     meter,
			Logger:    service.Logger(ctx),
		})
	}}, nil
}

// One snapshot per collection keeps the log's record and byte totals coherent.
// Registration and removal bound the callback's lifetime to a running attempt.
func registerBufferInstruments(meter metric.Meter, name string, read func() (seglog.Stats, error)) (metric.Registration, error) {
	records, e1 := meter.Int64ObservableUpDownCounter("flowseer.edge.buffer.records", metric.WithUnit("{record}"), metric.WithDescription("records held in the edge log"))
	size, e2 := meter.Int64ObservableUpDownCounter("flowseer.edge.buffer.size", metric.WithUnit("By"), metric.WithDescription("disk bytes held in the edge log"))
	age, e3 := meter.Float64ObservableGauge("flowseer.edge.buffer.oldest_age", metric.WithUnit("s"), metric.WithDescription("age of the oldest segment in the edge log"))
	discarded, e4 := meter.Int64ObservableCounter("flowseer.edge.buffer.discarded", metric.WithUnit("{record}"), metric.WithDescription("records discarded by the edge log bounds"))
	corrupt, e5 := meter.Int64ObservableCounter("flowseer.edge.buffer.corrupt", metric.WithUnit("{segment}"), metric.WithDescription("corrupt segments recovered from the edge log"))
	if err := errors.Join(e1, e2, e3, e4, e5); err != nil {
		return nil, err
	}
	attrs := metric.WithAttributes(attribute.String(drain.LogKey, name))
	return meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		stats, err := read()
		if err != nil {
			return err
		}
		o.ObserveInt64(records, stats.Records, attrs)
		o.ObserveInt64(size, stats.Bytes, attrs)
		o.ObserveFloat64(age, stats.OldestAge.Seconds(), attrs)
		o.ObserveInt64(discarded, stats.DiscardedRecords, attrs)
		o.ObserveInt64(corrupt, stats.CorruptSegments, attrs)
		return nil
	}, records, size, age, discarded, corrupt)
}
