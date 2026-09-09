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
// A confirmed report that ends its operation also releases the dispatch
// registry's memory of it, through Confirmer. Only the reports that end one:
// the queue tracks what central has not taken, the registry tracks what is
// still running, and the two are not the same condition. Confirming the
// second on the first would forget an operation mid-flight.
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
//
// A device whose report is refused is left alone for the rest of the pass.
// Within a pass the order is the sort; across passes it is only kept by not
// sending a device's later report while an earlier one is still owed. The
// case that needs it is Onboarded, which is not phase-ordered and clears
// central's confirmations for the device when it lands: delivered after the
// reports it precedes, it re-opens operations those reports had settled.
// Another device's report still goes out, which is the point of not stopping
// the pass outright.
func (q *Queue) drain(ctx context.Context) {
	stalled := make(map[string]bool)
	for _, k := range q.outstanding() {
		if ctx.Err() != nil {
			return
		}
		if stalled[k.device] {
			continue
		}
		q.mu.Lock()
		e, still := q.pending[k]
		q.mu.Unlock()
		if !still {
			continue
		}

		if _, err := q.client.Report(ctx, connect.NewRequest(e.report)); err != nil {
			// Kept, and this device is done for the pass. Central has not
			// confirmed, and this loop will try again. One failure does not
			// stop the pass for other devices: their reports may reach a
			// central that is refusing this one, and a queue that gave up on
			// the first error would hold everything hostage to the oldest
			// problem.
			stalled[k.device] = true
			q.log.DebugContext(ctx, "report not accepted; keeping it",
				slog.String("flowseer.device.id", k.device),
				slog.Uint64("flowseer.device.sequence", k.sequence),
				slog.String("flowseer.edge.report.kind", k.kind),
				slog.Any("error", err))
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
		// Only for a report that ends the operation. Confirming a progress
		// report or an acknowledgement would release the memory of an
		// operation still running, and a re-dispatch of its sequence would
		// then be admitted and run on the device a second time.
		if q.confirm != nil && closesOperation(k, e.report) {
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
