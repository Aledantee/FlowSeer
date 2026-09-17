// Package dispatchapi is central's DispatchService: the relay that turns the
// journal's owed rows into the server stream an edge subscribes to, and the
// report handler that applies an edge's reports back to the journal.
//
// The relay owns no state of its own. Every message it sends is derived from
// the durable lane record by [journal.OwedRows], so a closed stream leaves
// what is owed in the record for the next open, and a re-send while a row
// stays owed is a re-derivation, not a queued copy. The same pass sweeps a
// read whose deadline has passed, closing it with a deadline error, so the
// obligation the journal's invariant calls "due for the sweep" is one this
// relay keeps whenever it runs.
package dispatchapi

import (
	"context"
	"log/slog"
	"time"

	connect "connectrpc.com/connect"
	"github.com/nats-io/nats.go/jetstream"

	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// Error codes the dispatch relay returns.
var (
	// ErrCodeEdge is a Subscribe whose caller could not be identified as an
	// edge. The assertion middleware should have rejected it first.
	ErrCodeEdge = errs.NewCode("dispatchapi/edge")
	// ErrCodeResolve is a failure to resolve the devices an edge hosts.
	ErrCodeResolve = errs.NewCode("dispatchapi/resolve")
	// ErrCodeForbidden is a report about a device the calling edge does not
	// host.
	ErrCodeForbidden = errs.NewCode("dispatchapi/forbidden")
)

// DeviceResolver names the devices an edge hosts and the per-device facts the
// relay needs that live in the registry rather than the lane record: the
// delayed-apply horizon a mutation's deadline is measured against, whether the
// registry still lists a device (which decides whether an unknown-device
// refusal is retryable or terminal), and whether an edge hosts a device (which
// binds a report or an audit delivery to the edge the assertion names). The
// registry service implements it; tests supply a fake.
//
// Lists returns an error rather than a bare bool so a transient registry
// failure is not read as "the device is gone", which would permanently reject
// an operator's in-flight mutation; on an error the refusal leaves the row
// owed instead.
type DeviceResolver interface {
	Devices(ctx context.Context, edgeID string) ([]string, error)
	Horizon(ctx context.Context, deviceID string) (time.Duration, error)
	Lists(ctx context.Context, deviceID string) (bool, error)
	Hosts(ctx context.Context, edgeID, deviceID string) (bool, error)
}

// CentralAudit records what central decides on its own about a mutation. The
// report handler is not where central's audit records are shaped — this is the
// seam to the component that shapes them, so a refusal the edge reports and a
// rejection central writes stay one decision with one record.
type CentralAudit interface {
	DispatchRejected(ctx context.Context, device *inventoryv1.DeviceGlobalRef, state *accessv1.MutationState, from accessv1.OperationPhase, refusalCode string) error
}

// Config wires the relay to the journal, the registry, and the lane bucket it
// watches for change signals.
type Config struct {
	// Journal is the lane store the owed rows derive from and reports write.
	Journal *journal.Journal
	// Resolver names an edge's devices and their horizons.
	Resolver DeviceResolver
	// Watch is the lane bucket; a change on any key wakes an open stream to
	// re-derive within one Resend step. Optional: without it the relay wakes
	// only on the Resend tick.
	Watch jetstream.KeyValue
	// EdgeID identifies the calling edge from the request context the
	// assertion middleware populated.
	EdgeID func(ctx context.Context) (string, error)
	// Resend is one backoff step: how often an open stream re-derives while a
	// row stays owed.
	Resend time.Duration
	// SweepInterval is how often the background sweeper lists the bucket and
	// closes expired reads. It is separate from Resend because a full key
	// replay every backoff step is far more work than enforcing read deadlines
	// needs; nil uses a slower default.
	SweepInterval time.Duration
	// SweepError is the error an expired read is closed with. Optional.
	SweepError func() *errsv1.ErrorPayload
	// Clock is the time source for a mutation's deadline; nil uses the wall
	// clock.
	Clock func() time.Time
	// Audit records central's own rejection of a dispatch. Optional, and its
	// absence is logged where it matters: without it a rejection still
	// happens and leaves no trace of why.
	Audit CentralAudit
	// Logger records a row that cannot be dispatched; nil discards.
	Logger *slog.Logger
}

const (
	defaultResend        = 2 * time.Second
	defaultSweepInterval = 30 * time.Second
)

// Service implements the DispatchService handler and runs the relay.
type Service struct {
	cfg           Config
	clock         func() time.Time
	resend        time.Duration
	sweepInterval time.Duration
	log           *slog.Logger
}

// New constructs the relay. Journal, Resolver, and EdgeID must be set.
func New(cfg Config) *Service {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	resend := cfg.Resend
	if resend <= 0 {
		resend = defaultResend
	}
	sweep := cfg.SweepInterval
	if sweep <= 0 {
		sweep = defaultSweepInterval
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{cfg: cfg, clock: clock, resend: resend, sweepInterval: sweep, log: log}
}

// Subscribe holds the stream open for one edge, deriving and sending every row
// its devices owe on open and re-deriving within one Resend step of a record
// change. A send failure ends the stream; what is owed stays in the record.
func (s *Service) Subscribe(ctx context.Context, _ *connect.Request[integrationv1.SubscribeRequest], stream *connect.ServerStream[integrationv1.SubscribeResponse]) error {
	edgeID, err := s.cfg.EdgeID(ctx)
	if err != nil {
		return connectErr(errs.From(err).Code(ErrCodeEdge).Msg("identify subscribing edge"))
	}

	var updates <-chan jetstream.KeyValueEntry
	if s.cfg.Watch != nil {
		watcher, err := s.cfg.Watch.WatchAll(ctx, jetstream.IgnoreDeletes())
		if err != nil {
			return connectErr(errs.From(err).Code(ErrCodeResolve).Attr("edge", edgeID).Msg("watch lane bucket"))
		}
		defer func() { _ = watcher.Stop() }()
		updates = watcher.Updates()
	}

	ticker := time.NewTicker(s.resend)
	defer ticker.Stop()

	for {
		if err := s.dispatchPass(ctx, edgeID, stream); err != nil {
			return connectErr(err)
		}
		select {
		case <-ctx.Done():
			return nil // the edge closed the stream; owed stays in the record
		case <-ticker.C:
		case _, ok := <-updates:
			if !ok {
				updates = nil // the watch ended; fall back to the ticker
				continue
			}
			drain(updates) // coalesce a burst of key changes into one pass
		}
	}
}

// drain empties the channel without blocking, so a burst of key changes costs
// one dispatch pass rather than one per change.
func drain(ch <-chan jetstream.KeyValueEntry) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		default:
			return
		}
	}
}
