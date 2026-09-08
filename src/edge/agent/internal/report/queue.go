// Package report carries what the edge owes central: the re-send queue for
// dispatch reports, and the blocking deliverer for audit records.
package report

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	connect "connectrpc.com/connect"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeQueue identifies a re-send queue that could not be started.
var ErrCodeQueue = errs.NewCode("agent/report-queue")

// defaultCeiling bounds the queue against a bug producing unbounded distinct
// keys, not against an absent central — a central that cannot be reached also
// cannot dispatch, so the set of outstanding operations stops growing while it
// is away.
const (
	defaultCeiling = 4096
	defaultResend  = 15 * time.Second
)

// Reporter is the Report call this queue drains through.
type Reporter interface {
	Report(context.Context, *connect.Request[integrationv1.ReportRequest]) (*connect.Response[integrationv1.ReportResponse], error)
}

// Confirmer is told when central has a report, so whatever else is holding
// the operation open can let it go. The dispatch demultiplexer's registry is
// the one that matters: it and this queue share a drain condition, so they
// cannot disagree about whether an operation is outstanding.
type Confirmer interface {
	Confirmed(device string, sequence uint64)
}

// key identifies one outstanding report. Kind is part of it because an
// operation can owe more than one thing at once — a result and a refusal are
// different answers and neither supersedes the other.
type key struct {
	device   string
	sequence uint64
	kind     string
}

type entry struct {
	report   *integrationv1.ReportRequest
	admitted uint64
}

// Queue holds what central has not confirmed, and re-sends it until it does.
//
// It supersedes rather than accumulates. Requirement 9 asks for the *last*
// report to be re-sent, so a mutation that reports admission, verification and
// release occupies one entry rather than three, and the queue's size tracks
// outstanding operations rather than reports ever made.
type Queue struct {
	client  Reporter
	confirm Confirmer
	ceiling int
	log     *slog.Logger

	mu       sync.Mutex
	pending  map[key]entry
	sequence uint64
	woken    chan struct{}

	dropped atomic.Int64
	sent    atomic.Int64
}

// Config declares the queue. Construct with keyed fields.
type Config struct {
	Client  Reporter
	Confirm Confirmer
	// Ceiling bounds outstanding entries. Zero means the default.
	Ceiling int
	Logger  *slog.Logger
}

// New builds the queue. Nothing is sent until Run drives it.
func New(cfg Config) (*Queue, error) {
	if cfg.Client == nil {
		return nil, errs.New().Code(ErrCodeQueue).Msg("the report queue needs a DispatchService client")
	}
	ceiling := cfg.Ceiling
	if ceiling <= 0 {
		ceiling = defaultCeiling
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Queue{
		client:  cfg.Client,
		confirm: cfg.Confirm,
		ceiling: ceiling,
		log:     log,
		pending: make(map[key]entry),
		woken:   make(chan struct{}, 1),
	}, nil
}

// Dropped is how many reports the ceiling discarded. Non-zero means a bug,
// not a slow central: the ceiling is not reached by an edge waiting for
// contact, because a central that cannot be reached dispatches nothing new.
func (q *Queue) Dropped() int64 { return q.dropped.Load() }

// Sent is how many reports central has confirmed.
func (q *Queue) Sent() int64 { return q.sent.Load() }

// Pending is how many reports are outstanding.
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Report takes a report and returns at once. It never blocks and never fails:
// the lane calls this while holding a device's drain lock, and a report that
// could fail would make every operation's outcome depend on the network it is
// being reported over.
func (q *Queue) Report(_ context.Context, report *integrationv1.ReportRequest) {
	if report == nil {
		return
	}
	k := keyOf(report)

	q.mu.Lock()
	if _, superseding := q.pending[k]; !superseding && len(q.pending) >= q.ceiling {
		q.evictOldestLocked()
	}
	q.sequence++
	q.pending[k] = entry{report: report, admitted: q.sequence}
	q.mu.Unlock()

	q.wake()
}

// evictOldestLocked drops the oldest entry that may be dropped. The caller
// must hold mu.
//
// Oldest rather than newest, because almost every report is one central will
// ask for again: its outbox re-derives whatever it holds no report for, the
// edge answers that re-dispatch from its registry, and the report comes back
// here. Dropping the newest would discard the terminal report and keep the
// progress ones, which is the opposite of useful.
//
// Onboarded is never dropped. It is the one report central does not know to
// ask for — the edge sends it at start, unprompted — and losing it leaves
// central's record believing this edge still holds state it lost when it
// restarted.
func (q *Queue) evictOldestLocked() {
	var oldest key
	var oldestAt uint64
	found := false
	for k, e := range q.pending {
		if k.kind == kindOnboarded {
			continue
		}
		if !found || e.admitted < oldestAt {
			oldest, oldestAt, found = k, e.admitted, true
		}
	}
	if !found {
		// Everything outstanding is an Onboarded. The ceiling is a defense
		// against a bug and this is what that bug looks like; the new report
		// is admitted over the ceiling rather than dropping one of these.
		q.log.Error("report queue is full of onboarding reports",
			slog.Int("flowseer.edge.reports.pending", len(q.pending)))
		return
	}
	delete(q.pending, oldest)
	q.dropped.Add(1)
	q.log.Warn("report dropped at the queue ceiling",
		slog.String("flowseer.device.id", oldest.device),
		slog.Uint64("flowseer.device.sequence", oldest.sequence),
		slog.String("flowseer.edge.report.kind", oldest.kind),
		slog.Int64("flowseer.edge.reports.dropped", q.dropped.Load()))
}

func (q *Queue) wake() {
	select {
	case q.woken <- struct{}{}:
	default:
	}
}
