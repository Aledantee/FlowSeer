package historysink

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// streamGauges holds the last successful cleanup report for the observable
// gauges, which the metric reader calls from its own goroutine. Nothing is
// observed before the first report, so a sink that never cleaned does not
// export a limit of zero.
type streamGauges struct {
	reported atomic.Bool
	pending  atomic.Int64
	usage    atomic.Int64
	limit    atomic.Int64
	// oldest is the age of the first stored record in nanoseconds when the
	// report was read.
	oldest atomic.Int64
}

func (g *streamGauges) store(report edgebus.CleanReport, now time.Time) {
	var oldest time.Duration
	if report.FirstSequence <= report.LastSequence && !report.FirstTime.IsZero() {
		oldest = max(now.Sub(report.FirstTime), 0)
	}
	g.pending.Store(clampInt64(report.Pending))
	g.usage.Store(clampInt64(report.Bytes))
	g.limit.Store(max(report.MaxBytes, 0))
	g.oldest.Store(int64(oldest))
	g.reported.Store(true)
}

func clampInt64(v uint64) int64 { return int64(min(v, math.MaxInt64)) }

// clean runs one cleanup pass and publishes its report to the gauges. The
// report describes the stream as the pass found it, before the purge. A failed
// pass keeps the previous report and logs at the limited rate.
func (s *Sink) clean(ctx context.Context) {
	started := time.Now()
	report, err := edgebus.CleanIngest(ctx, s.cfg.JetStream, edgebus.IngestRecordTypeSyslog)
	if ctx.Err() != nil {
		return
	}
	var attrs []metric.RecordOption
	if err != nil {
		attrs = append(attrs, metric.WithAttributes(semconv.ErrorTypeKey.String(errorCleanup)))
	}
	s.metrics.cleanup.Record(ctx, time.Since(started).Seconds(), attrs...)
	if err != nil {
		s.warn("history cleanup failed", errorCleanup)
		return
	}
	s.gauges.store(report, time.Now())
}
