package rawwindowapi

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
)

const (
	// msgUnauthenticated is the whole of what a caller learns from a refused
	// assertion.
	msgUnauthenticated = "the call is not authorized as an enrolled edge"

	defaultResendInterval = 5 * time.Second
)

// EdgeServiceConfig configures an [EdgeService]. A non-positive ResendInterval
// is five seconds. A nil EdgeID reads the verified edge from the context, a nil
// EdgeTenant refuses tenant resolution with [ErrCodeStore], a nil Logger
// discards records, and a nil EdgeEnrollment treats every subscription as one
// enrollment.
//
// EdgeEnrollment names the edge's current enrollment, which must change when
// the edge enrolls again and must not change on a rekey.
type EdgeServiceConfig struct {
	ResendInterval time.Duration
	EdgeID         func(context.Context) (string, error)
	EdgeTenant     func(ctx context.Context, edgeID string) (string, error)
	EdgeEnrollment func(ctx context.Context, tenantID, edgeID string) (string, error)
	Logger         *slog.Logger
}

// EdgeService serves raw window controls to authenticated edges and applies
// their reports. It is safe for concurrent use when its EdgeID and EdgeTenant
// callbacks are.
type EdgeService struct {
	store          *Store
	resendInterval time.Duration
	edgeID         func(context.Context) (string, error)
	edgeTenant     func(ctx context.Context, edgeID string) (string, error)
	edgeEnrollment func(ctx context.Context, tenantID, edgeID string) (string, error)
	log            *slog.Logger

	mu       sync.Mutex // guards watchers
	watchers map[chan struct{}]struct{}
}

var _ ingestv1connect.RawWindowEdgeServiceHandler = (*EdgeService)(nil)

// NewEdgeService constructs an EdgeService over store, which must be non-nil.
// cfg uses the defaults documented by [EdgeServiceConfig].
func NewEdgeService(store *Store, cfg EdgeServiceConfig) *EdgeService {
	resend := cfg.ResendInterval
	if resend <= 0 {
		resend = defaultResendInterval
	}
	edgeID := cfg.EdgeID
	if edgeID == nil {
		edgeID = edgeapi.EdgeIDFromContext
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &EdgeService{
		store:          store,
		resendInterval: resend,
		edgeID:         edgeID,
		edgeTenant:     cfg.EdgeTenant,
		edgeEnrollment: cfg.EdgeEnrollment,
		log:            logger,
		watchers:       make(map[chan struct{}]struct{}),
	}
}

// NotifyChange wakes every open subscription so it sends what is owed without
// waiting for its resend tick.
func (s *EdgeService) NotifyChange() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *EdgeService) registerWatcher() (chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan struct{}, 1)
	s.watchers[ch] = struct{}{}
	return ch, sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.watchers, ch)
	})
}

// Subscribe fences the edge's process, sends ready, and then sends every owed
// control on each resend tick and each [EdgeService.NotifyChange]. It reads the
// record before every send and ends the stream with CodeAborted once a newer
// subscription replaced this one, so an old stream closes even while the newer
// one is open. A subscription that does not outrank the stored process fails
// with CodeFailedPrecondition.
//
// The base interceptor passes edge-facing streams through, so the handler
// validates its own request.
func (s *EdgeService) Subscribe(
	ctx context.Context,
	req *connect.Request[edgeingestv1.SubscribeRequest],
	stream *connect.ServerStream[edgeingestv1.SubscribeResponse],
) error {
	edgeID, tenantID, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if err := protovalidate.Validate(req.Msg); err != nil {
		return connecterr.WrapAs(connect.CodeInvalidArgument, "the subscription does not satisfy its schema rules",
			errs.From(err).Msg("raw window subscription fails its schema rules"))
	}

	generation, epoch := req.Msg.GetBootGeneration(), req.Msg.GetProcessEpoch()
	var enrollment string
	if s.edgeEnrollment != nil {
		if enrollment, err = s.edgeEnrollment(ctx, tenantID, edgeID); err != nil {
			return connectErr(errs.From(err).Code(ErrCodeStore).Attr("edge", edgeID).Msg("resolve edge enrollment"))
		}
	}
	ended, err := s.store.Subscribe(ctx, tenantID, edgeID, enrollment, generation, epoch, req.Msg.GetEnabledSources())
	if err != nil {
		return connectErr(err)
	}
	if ended > 0 {
		s.log.InfoContext(ctx, "raw windows ended by edge restart",
			fsconv.EventName("flowseer.raw_window.restart.ended"),
			fsconv.TenantID(tenantID),
			fsconv.EdgeID(edgeID),
			slog.Int("flowseer.raw_window.ended", ended))
	}
	s.log.DebugContext(ctx, "raw window subscription established",
		fsconv.TenantID(tenantID),
		fsconv.EdgeID(edgeID),
		slog.Uint64("flowseer.raw_window.boot_generation", generation))

	// Wake the streams of the process this one replaced, so they close now and
	// not on their next tick. This stream registers after, so it is not woken
	// into a send it is about to do anyway.
	s.NotifyChange()
	notifyCh, unwatch := s.registerWatcher()
	defer unwatch()

	ticker := time.NewTicker(s.resendInterval)
	defer ticker.Stop()

	ready := edgeingestv1.SubscribeResponse_builder{Ready: edgeingestv1.RawWindowReady_builder{}.Build()}.Build()
	if err := stream.Send(ready); err != nil {
		return err
	}

	for {
		if err := s.sendOwed(ctx, stream, tenantID, edgeID, generation, epoch); err != nil {
			code, coded := errs.CodeOf(err)
			if !coded {
				return err
			}
			if code == ErrCodeSuperseded {
				s.log.InfoContext(ctx, "raw window subscription superseded",
					fsconv.EventName("flowseer.raw_window.subscription.superseded"),
					fsconv.TenantID(tenantID),
					fsconv.EdgeID(edgeID),
					slog.Uint64("flowseer.raw_window.boot_generation", generation))
			}
			return connectErr(err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notifyCh:
		case <-ticker.C:
		}
	}
}

// sendOwed sends each owed control once, re-reading the record before every
// send. The re-read is what notices a supersession between two sends, and what
// drops a control a later revision replaced.
func (s *EdgeService) sendOwed(ctx context.Context, stream *connect.ServerStream[edgeingestv1.SubscribeResponse], tenantID, edgeID string, generation uint64, epoch string) error {
	sent := map[string]uint64{}
	for {
		owed, err := s.store.Owed(ctx, tenantID, edgeID, generation, epoch)
		if err != nil {
			return err
		}

		sentOne := false
		for _, control := range owed {
			window := control.GetRef().GetRawWindow().GetId()
			if revision, done := sent[window]; done && revision >= control.GetRevision() {
				continue
			}
			if err := stream.Send(edgeingestv1.SubscribeResponse_builder{Control: control}.Build()); err != nil {
				return err
			}
			sent[window] = control.GetRevision()
			sentOne = true
			break
		}
		if !sentOne {
			return nil
		}
	}
}

// Report applies the edge process's cumulative window state. It fails with
// CodeFailedPrecondition for a report from a process that is not the
// subscribed one, CodePermissionDenied for a window of another edge, and
// CodeInvalidArgument for a report the window's state cannot have produced.
func (s *EdgeService) Report(
	ctx context.Context,
	req *connect.Request[edgeingestv1.ReportRequest],
) (*connect.Response[edgeingestv1.ReportResponse], error) {
	edgeID, tenantID, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.store.Report(ctx, tenantID, edgeID, req.Msg.GetBootGeneration(), req.Msg.GetProcessEpoch(), req.Msg.GetReports()); err != nil {
		s.log.DebugContext(ctx, "raw window report refused",
			fsconv.TenantID(tenantID),
			fsconv.EdgeID(edgeID),
			fsconv.ErrorType(err))
		return nil, connectErr(err)
	}
	return connect.NewResponse(edgeingestv1.ReportResponse_builder{}.Build()), nil
}

// caller resolves the verified edge and its tenant.
func (s *EdgeService) caller(ctx context.Context) (edgeID, tenantID string, err error) {
	edgeID, err = s.edgeID(ctx)
	if err != nil {
		return "", "", connecterr.WrapRefused(msgUnauthenticated, err)
	}
	if s.edgeTenant == nil {
		return "", "", connectErr(errs.New().Code(ErrCodeStore).Attr("edge", edgeID).Msg("no edge tenant resolver configured"))
	}
	tenantID, err = s.edgeTenant(ctx, edgeID)
	if err != nil {
		return "", "", connectErr(errs.From(err).Code(ErrCodeStore).Attr("edge", edgeID).Msg("resolve edge tenant"))
	}
	if err := tenant.Validate(tenantID); err != nil {
		return "", "", connectErr(errs.From(err).Code(ErrCodeBadKey).Attr("edge", edgeID).Msg("validate edge tenant"))
	}
	return edgeID, tenantID, nil
}
