package report

import (
	"context"
	"log/slog"
	"time"

	connect "connectrpc.com/connect"
)

// Run drains the queue until ctx ends, re-sending what central has not
// confirmed every resend interval.
//
// A report is removed only when central answers. Report returns immediately
// and cannot fail, so this loop is the only thing that knows whether central
// has it — and an entry removed on a send that failed would be a report the
// edge believes it made and central never received, which is precisely the
// state the re-send exists to prevent.
//
// Confirmation is also what releases the dispatch registry's memory of the
// operation, through Confirmer. The two share one drain condition rather than
// each deciding for itself, so they cannot disagree about whether an operation
// is still outstanding.
func (q *Queue) Run(ctx context.Context, resend time.Duration) error {
	if resend <= 0 {
		resend = defaultResend
	}
	timer := time.NewTimer(resend)
	defer timer.Stop()

	for {
		q.drain(ctx)

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(resend)

		select {
		case <-ctx.Done():
			return nil
		case <-q.woken:
			// A new report arrived; send it now rather than at the next
			// interval. The interval is the re-send cadence for what central
			// has not confirmed, not the latency floor for a fresh report.
		case <-timer.C:
		}
	}
}

// drain sends everything outstanding once, oldest first.
//
// Oldest first because central applies reports in phase order, and a record
// that receives a release before the admission it follows discards it as
// stale. The queue supersedes per operation, so this is at most one report per
// outstanding operation, in the order the edge made them.
func (q *Queue) drain(ctx context.Context) {
	for _, k := range q.outstanding() {
		if ctx.Err() != nil {
			return
		}
		q.mu.Lock()
		e, still := q.pending[k]
		q.mu.Unlock()
		if !still {
			continue
		}

		if _, err := q.client.Report(ctx, connect.NewRequest(e.report)); err != nil {
			// Kept. Central has not confirmed, and this loop will try again.
			// One failure does not stop the pass: another device's report may
			// reach a central that is refusing this one, and a queue that
			// gave up on the first error would hold everything hostage to the
			// oldest problem.
			attrs := []slog.Attr{
				slog.String("flowseer.device.id", k.device),
				slog.Uint64("flowseer.device.sequence", k.sequence),
				slog.String("flowseer.edge.report.kind", k.kind),
				slog.Any("error", err),
			}
			code := connect.CodeOf(err)
			if code == connect.CodePermissionDenied || code == connect.CodeInvalidArgument {
				// Do not drop or confirm a refusal: central still lacks the report,
				// and releasing the registry could let a mutation run twice.
				attrs = append(attrs, slog.String("error.type", code.String()))
				q.log.LogAttrs(ctx, slog.LevelError, "report permanently refused; keeping it", attrs...)
			} else {
				q.log.LogAttrs(ctx, slog.LevelDebug, "report not accepted; keeping it", attrs...)
			}
			continue
		}

		q.mu.Lock()
		// Only if it is still the report that was sent. A newer one arriving
		// mid-flight has to be delivered too: confirming the key would drop a
		// report central never saw.
		if current, ok := q.pending[k]; ok && current.admitted == e.admitted {
			delete(q.pending, k)
		}
		q.mu.Unlock()

		q.sent.Add(1)
		// Only for a report that names a real operation. An unknown arm
		// carries this queue's own counter in the sequence slot so it cannot
		// supersede anything, and passing that to the registry would confirm
		// an operation nobody ran.
		if q.confirm != nil && k.kind != kindUnknown && sequenceOf(k) != 0 {
			q.confirm.Confirmed(k.device, k.sequence)
		}
	}
}

// outstanding lists the pending keys in admission order.
func (q *Queue) outstanding() []key {
	q.mu.Lock()
	defer q.mu.Unlock()

	keys := make([]key, 0, len(q.pending))
	for k := range q.pending {
		keys = append(keys, k)
	}
	sortByAdmission(keys, q.pending)
	return keys
}
