package historysink

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const scopeName = "go.aledante.io/FlowSeer/src/services/device/internal/historysink"

type sinkMetrics struct {
	inserted    metric.Int64Counter
	refused     metric.Int64Counter
	retried     metric.Int64Counter
	redelivered metric.Int64Counter
	batch       metric.Float64Histogram
	cleanup     metric.Float64Histogram
	age         metric.Float64Histogram
}

// newMetrics registers the sink's instruments. The four stream gauges read
// gauges, which a cleanup pass fills, so a metric collection never calls the
// server.
func newMetrics(provider metric.MeterProvider, gauges *streamGauges) (*sinkMetrics, error) {
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
	if m.redelivered, err = meter.Int64Counter("flowseer.history.records.redelivered",
		metric.WithUnit("{record}"),
		metric.WithDescription("Deliveries of a record the stream had already delivered at least once")); err != nil {
		return nil, err
	}
	if m.cleanup, err = meter.Float64Histogram("flowseer.history.cleanup.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time one cleanup pass takes to read the stream and purge the resolved prefix, with the error type when it failed")); err != nil {
		return nil, err
	}
	err = errors.Join(
		observeInt(meter, "flowseer.history.records.pending", "{record}",
			"Records the stream holds that the member has not resolved, read at the last cleanup pass", &gauges.pending, gauges),
		observeInt(meter, "flowseer.history.stream.usage", "By",
			"Bytes the ingest stream stores, read at the last cleanup pass", &gauges.usage, gauges),
		observeInt(meter, "flowseer.history.stream.limit", "By",
			"Byte bound of the ingest stream, read at the last cleanup pass", &gauges.limit, gauges),
		observeOldestAge(meter, gauges),
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func observeInt(meter metric.Meter, name, unit, description string, value *atomic.Int64, gauges *streamGauges) error {
	_, err := meter.Int64ObservableGauge(name,
		metric.WithUnit(unit),
		metric.WithDescription(description),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			if gauges.reported.Load() {
				o.Observe(value.Load())
			}
			return nil
		}))
	return err
}

func observeOldestAge(meter metric.Meter, gauges *streamGauges) error {
	_, err := meter.Float64ObservableGauge("flowseer.history.stream.oldest.age",
		metric.WithUnit("s"),
		metric.WithDescription("Age of the oldest record the ingest stream stores, read at the last cleanup pass"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			if gauges.reported.Load() {
				o.Observe(time.Duration(gauges.oldest.Load()).Seconds())
			}
			return nil
		}))
	return err
}
