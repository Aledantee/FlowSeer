package drain

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/seglog"
)

// ErrCodeRun identifies a drain that could not start because its Config was
// incomplete.
var ErrCodeRun = errs.NewCode("agent/drain-run")

const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second

	// LogKey is the attribute naming the log an instrument or record is about.
	LogKey = "flowseer.edge.buffer.log"

	// nothingSettled is the error.type of a call that answered without error
	// and settled no record.
	nothingSettled = "nothing_settled"
)

// Log is the part of [seglog.Log] a drain uses. *seglog.Log satisfies it.
type Log interface {
	Next(ctx context.Context) (seglog.Segment, error)
	Ack(sequence uint64) error
	Stats() (seglog.Stats, error)
}

// Send delivers records in order and reports how many leading records central
// settled. A settled count below len(records) is not an error. The caller
// resends from the first unsettled record.
type Send func(ctx context.Context, records [][]byte) (settled int, err error)

// Config declares one drain. Construct with keyed fields.
type Config struct {
	// LogName is the value of the flowseer.edge.buffer.log attribute on the
	// drain's instruments and log records, such as "ingest".
	LogName string
	Log     Log
	Send    Send
	// Decodable reports whether a record can be unmarshaled into the item
	// the call carries. A record it refuses is counted and dropped, since
	// it can only come from a writer bug and would stall the log behind it.
	Decodable func(record []byte) bool
	// Wait blocks for d or until ctx ends, reporting whether it elapsed.
	// Nil means a timer. A test substitutes it.
	Wait   func(ctx context.Context, d time.Duration) bool
	Meter  metric.Meter
	Logger *slog.Logger
}

// Run drains cfg.Log until ctx ends. It returns nil when ctx ends or the log
// reports [seglog.ErrCodeClosed] from Next or Ack, and any other error from
// either. A failed call never returns: the drain waits, backing off from one
// second to thirty, and sends again. The wait resets once a call settles a
// record.
//
// Run registers the delivery instruments on cfg.Meter and logs a warning when
// the log's discard count rises, including while waiting and before returning.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Log == nil || cfg.Send == nil {
		return errs.New().Code(ErrCodeRun).Msg("the drain needs a log and a send")
	}
	meter := cfg.Meter
	if meter == nil {
		meter = noop.NewMeterProvider().Meter("go.aledante.io/FlowSeer/src/edge/agent/internal/drain")
	}
	d := &drain{
		log:       cfg.Log,
		send:      cfg.Send,
		decodable: cfg.Decodable,
		wait:      cfg.Wait,
		logger:    cfg.Logger,
		logAttr:   attribute.String(LogKey, cfg.LogName),
	}
	d.attrs = metric.WithAttributes(d.logAttr)
	if d.decodable == nil {
		d.decodable = func([]byte) bool { return true }
	}
	if d.wait == nil {
		d.wait = waitFor
	}
	if d.logger == nil {
		d.logger = slog.New(slog.DiscardHandler)
	}
	var err1, err2, err3 error
	d.settled, err1 = meter.Int64Counter("flowseer.edge.delivery.settled",
		metric.WithUnit("{record}"),
		metric.WithDescription("records central settled for the edge"),
	)
	d.undecodable, err2 = meter.Int64Counter("flowseer.edge.delivery.undecodable",
		metric.WithUnit("{record}"),
		metric.WithDescription("buffered records dropped because they could not be decoded"),
	)
	d.duration, err3 = meter.Float64Histogram("flowseer.edge.delivery.duration",
		metric.WithUnit("s"),
		metric.WithDescription("duration of each delivery call to central"),
	)
	if err := errors.Join(err1, err2, err3); err != nil {
		return err
	}
	err := d.run(ctx)
	d.warnOnDiscards(ctx)
	return err
}

type drain struct {
	log       Log
	send      Send
	decodable func([]byte) bool
	wait      func(context.Context, time.Duration) bool
	logger    *slog.Logger
	attrs     metric.MeasurementOption

	settled     metric.Int64Counter
	undecodable metric.Int64Counter
	duration    metric.Float64Histogram
	logAttr     attribute.KeyValue

	// sequence and offset are the resume point: the first unsettled record of
	// the segment with that sequence. have is false until a segment arrives.
	sequence uint64
	have     bool
	offset   int

	discardedRecords int64
	discardedBytes   int64
}

func (d *drain) run(ctx context.Context) error {
	backoff := minBackoff
	for {
		nextCtx, cancel := context.WithTimeout(ctx, maxBackoff)
		seg, err := d.log.Next(nextCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil || isClosed(err) {
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				d.warnOnDiscards(ctx)
				continue
			}
			return err
		}
		d.warnOnDiscards(ctx)
		if !d.have || seg.Sequence != d.sequence {
			d.sequence, d.have, d.offset = seg.Sequence, true, 0
		}

		var pending []int
		for i := d.offset; i < len(seg.Records); i++ {
			if d.decodable(seg.Records[i]) {
				pending = append(pending, i)
			}
		}
		settled := 0
		if len(pending) > 0 {
			batch := make([][]byte, len(pending))
			for i, at := range pending {
				batch[i] = seg.Records[at]
			}
			n, err := d.call(ctx, batch)
			if err != nil || n == 0 {
				if !d.wait(ctx, backoff) {
					return nil
				}
				backoff = min(backoff*2, maxBackoff)
				continue
			}
			settled = n
			backoff = minBackoff
		}

		// Records before the first unsettled one are settled or undecodable.
		next := len(seg.Records)
		if settled < len(pending) {
			next = pending[settled]
		}
		d.undecodable.Add(ctx, int64(next-d.offset-settled), d.attrs)
		d.offset = next
		if d.offset == len(seg.Records) {
			if err := d.log.Ack(seg.Sequence); err != nil {
				if ctx.Err() != nil || isClosed(err) {
					return nil
				}
				return err
			}
		}
	}
}

// call sends one batch and records its duration. It returns the settled count
// clamped to the batch, since a count above the number sent can only mean all
// of them.
func (d *drain) call(ctx context.Context, batch [][]byte) (int, error) {
	start := time.Now()
	settled, err := d.send(ctx, batch)
	attrs := []attribute.KeyValue{d.logAttr}
	switch {
	case err != nil:
		attrs = append(attrs, attribute.String(fsconv.ErrorTypeKey, fsconv.ErrorTypeOf(err)))
	case settled <= 0:
		attrs = append(attrs, attribute.String(fsconv.ErrorTypeKey, nothingSettled))
	}
	d.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
	if err != nil {
		return 0, err
	}
	settled = max(0, min(settled, len(batch)))
	d.settled.Add(ctx, int64(settled), d.attrs)
	return settled, nil
}

// warnOnDiscards logs the records the log's bounds discarded since the last
// check. It is the drop diagnostic that does not travel through the
// telemetry log it may be reporting on.
func (d *drain) warnOnDiscards(ctx context.Context) {
	stats, err := d.log.Stats()
	if err != nil || stats.DiscardedRecords <= d.discardedRecords {
		return
	}
	d.logger.WarnContext(ctx, "buffer discarded records",
		fsconv.EventName("flowseer.edge.buffer.discarded"),
		slog.String(LogKey, d.logAttr.Value.AsString()),
		slog.Int64("flowseer.edge.buffer.discarded_records", stats.DiscardedRecords-d.discardedRecords),
		slog.Int64("flowseer.edge.buffer.discarded_bytes", stats.DiscardedBytes-d.discardedBytes))
	d.discardedRecords, d.discardedBytes = stats.DiscardedRecords, stats.DiscardedBytes
}

func isClosed(err error) bool {
	code, ok := errs.CodeOf(err)
	return ok && code == seglog.ErrCodeClosed
}

func waitFor(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
