package historysink

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const scopeName = "go.aledante.io/FlowSeer/src/services/device/internal/historysink"

type sinkMetrics struct {
	inserted metric.Int64Counter
	refused  metric.Int64Counter
	retried  metric.Int64Counter
	batch    metric.Float64Histogram
	age      metric.Float64Histogram

	// discarded holds the last sampled gap between the stream's oldest record
	// and the consumer's acknowledged floor. The gauge callback reads it, so a
	// metric collection never calls the server.
	discarded atomic.Int64
}

func newMetrics(provider metric.MeterProvider) (*sinkMetrics, error) {
	meter := provider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL))
	m := &sinkMetrics{}
	var err error
	if m.inserted, err = meter.Int64Counter("flowseer.history.records.inserted",
		metric.WithUnit("{record}"),
		metric.WithDescription("Records acknowledged after the history store held them")); err != nil {
		return nil, err
	}
	if m.refused, err = meter.Int64Counter("flowseer.history.records.refused",
		metric.WithUnit("{record}"),
		metric.WithDescription("Records terminated because they cannot become a row or the store rejects them")); err != nil {
		return nil, err
	}
	if m.retried, err = meter.Int64Counter("flowseer.history.records.retried",
		metric.WithUnit("{record}"),
		metric.WithDescription("Records negatively acknowledged after a store or lookup failure")); err != nil {
		return nil, err
	}
	if m.batch, err = meter.Float64Histogram("flowseer.history.batch.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time to insert one batch, from the call to the store's answer")); err != nil {
		return nil, err
	}
	if m.age, err = meter.Float64Histogram("flowseer.history.record.age",
		metric.WithUnit("s"),
		metric.WithDescription("Age of a record in the ingest stream when the sink receives it")); err != nil {
		return nil, err
	}
	if _, err = meter.Int64ObservableGauge("flowseer.history.records.discarded",
		metric.WithUnit("{record}"),
		metric.WithDescription("Records the ingest stream removed before the sink acknowledged them: the stream's first sequence minus one, minus the consumer's acknowledged floor"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(m.discarded.Load())
			return nil
		})); err != nil {
		return nil, err
	}
	return m, nil
}
