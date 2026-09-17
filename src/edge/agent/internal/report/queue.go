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

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
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
	Report(context.Context, *connect.Request[dispatchv1.ReportRequest]) (*connect.Response[dispatchv1.ReportResponse], error)
}

// Confirmer is told when central has taken a report that ends its operation,
// so whatever else is holding that operation open can let it go. The dispatch
// demultiplexer's registry is the one that matters.
//
// Only the reports that end an operation, which is narrower than the reports
// this queue drains: a progress report and the two acknowledgements are taken
// by central while the operation is still running, and an implementation that
// confirmed on those would release the registry mid-flight. closesOperation
// draws the line.
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
	report   *dispatchv1.ReportRequest
	admitted uint64
}

// Queue holds what central has not confirmed, and re-sends it until it does.
//
// It supersedes rather than accumulates. What central needs is the *last*
// report of an operation, not every report of it, so a mutation that reports
// admission, verification and
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
	// overCeiling records that the ceiling was reached with nothing droppable
	// behind it, so the condition is logged on the way in rather than once per
	// admission for as long as it lasts. Guarded by mu.
	overCeiling bool

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

// Dropped is how many reports the ceiling discarded.
//
// An edge merely waiting for contact does not reach the ceiling: a central it
// cannot reach dispatches nothing new, so nothing arrives to queue. Non-zero
// means either a bug or the one asymmetric case — a central that serves the
// dispatch stream while refusing Report. There the drift poll keeps opening
// read sequences and each is a new entry, so the queue grows with time and
// managed-interface count without anything here being wrong.
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
func (q *Queue) Report(ctx context.Context, report *dispatchv1.ReportRequest) {
	if report == nil {
		return
	}
	k := keyOf(report)

	q.mu.Lock()
	q.sequence++
	if k.kind == kindUnknown {
		// Each unknown report gets its own key, so one can never replace
		// another. Superseding is a judgment — that a later report answers
		// the same question as an earlier one — and this is the branch that
		// has already admitted it cannot make it: it does not know what
		// these messages are. Keying them together would silently lose one
		// per device in a build that adds two arms at once, and the loss
		// would look like central never asking.
		k.sequence = q.sequence
	}
	if _, superseding := q.pending[k]; !superseding && len(q.pending) >= q.ceiling {
		q.evictOldestLocked(ctx)
	}
	q.pending[k] = entry{report: report, admitted: q.sequence}
	q.mu.Unlock()

	q.wake()
}

// evictOldestLocked drops the oldest entry that may be dropped. The caller
// must hold mu.
//
// Oldest by last write, not by first admission: an entry's admitted value is
// refreshed when a newer report supersedes it, so a device reporting steadily
// keeps its place while a device that reported once and went quiet loses
// its. That is the intended trade — the actively reporting device is the one
// whose report central is most likely still waiting on — and it is worth
// naming because the natural reading of "oldest" is the other one.
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
func (q *Queue) evictOldestLocked(ctx context.Context) {
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
		// Everything outstanding is an Onboarded, so there is nothing this
		// may drop and the new report is admitted over the ceiling. The
		// condition persists once reached — an edge with more onboarding
		// reports than the ceiling does not lose them by carrying on — so it
		// is recorded on the way in and not again, rather than once per
		// report for as long as it lasts.
		if !q.overCeiling {
			q.overCeiling = true
			q.log.WarnContext(ctx, "report queue holds nothing but onboarding reports; admitting over the ceiling",
				slog.Int("flowseer.edge.reports.pending", len(q.pending)))
		}
		return
	}
	q.overCeiling = false
	delete(q.pending, oldest)
	q.dropped.Add(1)
	q.log.WarnContext(ctx, "report dropped at the queue ceiling",
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
