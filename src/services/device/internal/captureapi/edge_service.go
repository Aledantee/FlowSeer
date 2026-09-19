package captureapi

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

const (
	// failStreamTimeout bounds the record write that closes out a broken
	// upload stream, which runs on a context detached from the dead request.
	failStreamTimeout = 10 * time.Second

	// msgUnauthenticated is the whole of what a caller learns from a refused
	// assertion. Which of the verifier's eleven checks refused it is central's
	// business, and this route answers a caller that has not authenticated.
	msgUnauthenticated = "the call is not authorized as an enrolled edge"

	defaultAssertionWindow = 60 * time.Second
	defaultResendInterval  = 5 * time.Second
	defaultRetentionPeriod = 7 * 24 * time.Hour
)

// Broadcaster manages live packet chunk subscriptions for active capture sessions.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan *modelcapturev1.CapturePacketChunk]struct{}
}

// NewBroadcaster constructs an empty Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: make(map[string]map[chan *modelcapturev1.CapturePacketChunk]struct{}),
	}
}

// Subscribe returns a channel receiving chunks for sessionID, and an unsubscribe func.
func (b *Broadcaster) Subscribe(sessionID string) (<-chan *modelcapturev1.CapturePacketChunk, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan *modelcapturev1.CapturePacketChunk, 128)
	set, ok := b.subs[sessionID]
	if !ok {
		set = make(map[chan *modelcapturev1.CapturePacketChunk]struct{})
		b.subs[sessionID] = set
	}
	set[ch] = struct{}{}

	unsub := sync.OnceFunc(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.subs[sessionID]; ok {
			delete(s, ch)
			if len(s) == 0 {
				delete(b.subs, sessionID)
			}
		}
	})
	return ch, unsub
}

// Broadcast distributes a packet chunk to all subscribers of sessionID and
// returns how many subscribers were too far behind to take it. A tail that
// cannot keep up loses chunks rather than stalling the upload it is watching,
// which makes the live stream a subsequence of what the edge sent; the count
// is what lets the caller say so instead of leaving the gap invisible.
func (b *Broadcaster) Broadcast(sessionID string, chunk *modelcapturev1.CapturePacketChunk) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	set, ok := b.subs[sessionID]
	if !ok {
		return 0
	}
	dropped := 0
	for ch := range set {
		select {
		case ch <- chunk:
		default:
			dropped++
		}
	}
	return dropped
}

// CloseSession closes subscriber channels for sessionID and clears the subscriber set.
func (b *Broadcaster) CloseSession(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if set, ok := b.subs[sessionID]; ok {
		for ch := range set {
			close(ch)
		}
		delete(b.subs, sessionID)
	}
}

// SubscriberCount returns the number of active subscribers for sessionID.
func (b *Broadcaster) SubscriberCount(sessionID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[sessionID])
}

// AssertionVerifier checks SignedEdgeAssertion envelopes.
type AssertionVerifier interface {
	VerifySigned(ctx context.Context, signed *edgev1.SignedEdgeAssertion, procedure string, body []byte) (*edgev1.EdgeAssertion, error)
}

// EdgeServiceConfig carries options for EdgeService.
type EdgeServiceConfig struct {
	AssertionWindow time.Duration
	ResendInterval  time.Duration
	RetentionPeriod time.Duration
	EdgeID          func(context.Context) (string, error)
	Clock           func() time.Time
	Logger          *slog.Logger
}

// EdgeService implements capturev1connect.CaptureEdgeServiceHandler.
type EdgeService struct {
	store           *Store
	verifier        AssertionVerifier
	broadcaster     *Broadcaster
	assertionWindow time.Duration
	resendInterval  time.Duration
	retentionPeriod time.Duration
	edgeID          func(context.Context) (string, error)
	clock           func() time.Time
	log             *slog.Logger

	mu       sync.Mutex
	watchers map[chan struct{}]struct{}
	uploads  map[string]struct{}
}

// Ensure EdgeService satisfies CaptureEdgeServiceHandler.
var _ capturev1connect.CaptureEdgeServiceHandler = (*EdgeService)(nil)

// NewEdgeService constructs an EdgeService.
func NewEdgeService(store *Store, verifier AssertionVerifier, broadcaster *Broadcaster, cfg EdgeServiceConfig) *EdgeService {
	assertionWindow := cfg.AssertionWindow
	if assertionWindow <= 0 {
		assertionWindow = defaultAssertionWindow
	}
	resendInterval := cfg.ResendInterval
	if resendInterval <= 0 {
		resendInterval = defaultResendInterval
	}
	retentionPeriod := cfg.RetentionPeriod
	if retentionPeriod <= 0 {
		retentionPeriod = defaultRetentionPeriod
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	edgeIDFunc := cfg.EdgeID
	if edgeIDFunc == nil {
		edgeIDFunc = edgeapi.EdgeIDFromContext
	}

	return &EdgeService{
		store:           store,
		verifier:        verifier,
		broadcaster:     broadcaster,
		assertionWindow: assertionWindow,
		resendInterval:  resendInterval,
		retentionPeriod: retentionPeriod,
		edgeID:          edgeIDFunc,
		clock:           clock,
		log:             logger,
		watchers:        make(map[chan struct{}]struct{}),
		uploads:         make(map[string]struct{}),
	}
}

// claimUpload reserves a session for one upload stream, reporting whether this
// stream got it. One pcapng file is written per session, by one writer, so two
// streams uploading the same session would interleave their packets into it
// and each would discard the other's partial file on its way out. Central
// authenticates the edge but does not trust it to open a session once.
func (s *EdgeService) claimUpload(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, held := s.uploads[sessionID]; held {
		return false
	}
	s.uploads[sessionID] = struct{}{}
	return true
}

func (s *EdgeService) releaseUpload(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.uploads, sessionID)
}

// NotifyStoreChange alerts all active assignment streams of a session state transition.
func (s *EdgeService) NotifyStoreChange() {
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

	unsub := sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.watchers, ch)
	})
	return ch, unsub
}

// SubscribeCaptureAssignments streams owed capture assignments to the calling edge.
func (s *EdgeService) SubscribeCaptureAssignments(
	ctx context.Context,
	_ *connect.Request[captureedgev1.SubscribeCaptureAssignmentsRequest],
	stream *connect.ServerStream[captureedgev1.SubscribeCaptureAssignmentsResponse],
) error {
	edgeID, err := s.edgeID(ctx)
	if err != nil {
		return connecterr.WrapAs(connect.CodeUnauthenticated, msgUnauthenticated, err)
	}

	notifyCh, unwatch := s.registerWatcher()
	defer unwatch()

	ticker := time.NewTicker(s.resendInterval)
	defer ticker.Stop()

	sendOwed := func() error {
		sessions, err := s.store.ListSessions(ctx)
		if err != nil {
			return connectErr(err)
		}
		for _, rec := range sessions {
			cfg := rec.GetConfig()
			if cfg.GetRef().GetEdge().GetEdge().GetId() != edgeID {
				continue
			}

			lifecycle := rec.GetState().GetLifecycle()
			switch {
			case lifecycle == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING:
				resp := captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
					Start: cfg,
				}.Build()
				if err := stream.Send(resp); err != nil {
					return err
				}
			// A stop is owed only for a session an edge actually started.
			// started_at is set at the PENDING to RUNNING transition, so a
			// session canceled while it was still pending owes nothing —
			// without that arm it would owe a stop on every resend tick for
			// the life of the record, for a capture no edge ever ran.
			case lifecycle == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED &&
				rec.GetState().HasStartedAt() && rec.GetState().GetArtifact() == nil:
				resp := captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
					Stop: cfg.GetRef(),
				}.Build()
				if err := stream.Send(resp); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := sendOwed(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notifyCh:
			if err := sendOwed(); err != nil {
				return err
			}
		case <-ticker.C:
			if err := sendOwed(); err != nil {
				return err
			}
		}
	}
}

// UploadCapture accepts the stream of packet chunks and mid-stream re-assertions from the edge.
func (s *EdgeService) UploadCapture(
	ctx context.Context,
	stream *connect.ClientStream[captureedgev1.UploadCaptureRequest],
) (*connect.Response[captureedgev1.UploadCaptureResponse], error) {
	procedure := capturev1connect.CaptureEdgeServiceUploadCaptureProcedure

	// The window is enforced twice because neither check alone covers both
	// ways an edge can exceed it. The transport deadline closes the read for
	// an edge that goes silent, which the loop below could never observe; the
	// arithmetic on lastAssertionAt covers an edge that keeps sending chunks
	// without ever re-asserting, and is what remains where no transport
	// supports a deadline. The deadline is armed here, before the opening
	// read, so a caller that opens the stream and says nothing cannot hold it
	// without ever being authenticated.
	extendDeadline := s.deadlineExtender(ctx)
	extendDeadline()

	// The stream must open with a SignedEdgeAssertion.
	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return nil, connecterr.WrapAs(connect.CodeUnauthenticated, msgUnauthenticated, err)
		}
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New(msgUnauthenticated))
	}

	firstMsg := stream.Msg()
	firstSigned := firstMsg.GetAssertion()
	if firstSigned == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("upload stream must open with SignedEdgeAssertion"))
	}

	firstAssertion, err := s.verifier.VerifySigned(ctx, firstSigned, procedure, nil)
	if err != nil {
		return nil, connecterr.WrapAs(connect.CodeUnauthenticated, msgUnauthenticated, err)
	}

	callingEdgeID := firstAssertion.GetEdge().GetEdge().GetId()
	lastAssertionAt := s.clock()

	var (
		sessionID      string
		sessionRef     *modelcapturev1.CaptureSessionGlobalRef
		sessionStarted bool
		linkType              = netcapturev1.LinkType_LINK_TYPE_ETHERNET
		snapLen        uint32 = 128
	)

	// The window is enforced twice because neither check alone covers both
	// ways an edge can exceed it. The transport deadline closes the read for
	// an edge that goes silent, which the loop below could never observe;
	// the arithmetic on lastAssertionAt covers an edge that keeps sending
	// chunks without ever re-asserting, and is the only check where no
	// transport supports a deadline.
	// An upload stream that ends without a final chunk leaves a partial
	// pcapng, an open descriptor and a claimed session behind; all three are
	// this handler's to release, because nothing else knows the stream is over.
	finalized := false
	claimed := false
	droppedReported := false
	defer func() {
		if !claimed {
			return
		}
		// Drop the writer before releasing the claim, never after. The claim
		// is what keeps a second stream out; releasing first opens a window
		// where the retry is admitted, finds this stream's writer still in
		// the map, and appends into a file this one is about to unlink.
		if !finalized {
			s.store.AbandonWriter(sessionID)
		}
		s.releaseUpload(sessionID)
	}()

	for stream.Receive() {
		now := s.clock()
		if now.Sub(lastAssertionAt) > s.assertionWindow {
			s.failStream(ctx, sessionID, "assertion window lapsed")
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("assertion window lapsed past deadline"))
		}

		msg := stream.Msg()

		if signed := msg.GetAssertion(); signed != nil {
			assertion, err := s.verifier.VerifySigned(ctx, signed, procedure, nil)
			if err != nil {
				s.failStream(ctx, sessionID, "mid-stream assertion did not verify")
				return nil, connecterr.WrapAs(connect.CodeUnauthenticated, msgUnauthenticated, err)
			}
			if assertion.GetEdge().GetEdge().GetId() != callingEdgeID {
				s.failStream(ctx, sessionID, "assertion edge changed mid-stream")
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("assertion edge changed mid-stream"))
			}

			lastAssertionAt = s.clock()
			extendDeadline()
			continue
		}

		chunk := msg.GetChunk()
		if chunk == nil {
			continue
		}

		// Enforce edge boundary: chunk session must match calling edge.
		chunkEdgeID := chunk.GetSession().GetEdge().GetEdge().GetId()
		if chunkEdgeID != callingEdgeID {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("chunk session edge does not match authenticated edge"))
		}

		if sessionStarted && chunk.GetSession().GetCaptureSession().GetId() != sessionID {
			// One stream carries one session: its packets go into one pcapng,
			// and only the first chunk's ref was resolved against a record.
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("chunk names a different capture session than the stream opened with"))
		}

		if !sessionStarted {
			sessionRef = chunk.GetSession()
			sessionID = sessionRef.GetCaptureSession().GetId()

			rec, _, err := s.store.GetSession(ctx, sessionID)
			if err != nil {
				return nil, connectErr(err)
			}
			if rec == nil {
				return nil, errNoSuchSession()
			}

			if rec.GetConfig().GetRef().GetEdge().GetEdge().GetId() != callingEdgeID {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("session does not belong to calling edge"))
			}

			// A session that has already produced its artifact is an audit
			// record. Central authenticates the edge but does not trust its
			// view of the session, so a second stream naming a finished
			// session is refused here rather than allowed to rewrite the
			// stored capture and the digest that describes it. A canceled
			// session stays open until its artifact exists: flushing the
			// final chunk is how the edge answers a stop.
			if captureIsOver(rec.GetState()) {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("capture session has already stopped"))
			}

			if !s.claimUpload(sessionID) {
				return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("another stream is already uploading this capture session"))
			}
			claimed = true

			if rec.GetState().HasLinkType() {
				linkType = rec.GetState().GetLinkType()
			}
			if b := rec.GetConfig().GetBudget(); b != nil && b.GetSnapLength() > 0 {
				snapLen = b.GetSnapLength()
			}
			if rec.GetConfig().GetAuthorization().GetFullPayloadRequested() {
				snapLen = 65535
			}

			// started_at records that an edge has begun capturing, which is
			// what the owed stop turns on. It has to be written on the first
			// chunk whatever the lifecycle says, not only on the transition
			// out of PENDING: an operator who cancels between the assignment
			// and the first chunk would otherwise leave central unable to
			// tell an edge that never started from one that had not reported
			// yet, and the cancellation would never be delivered.
			if !rec.GetState().HasStartedAt() {
				_, err = s.store.MutateSession(ctx, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
					if !r.GetState().HasStartedAt() {
						r.GetState().SetStartedAt(timestamppb.New(s.clock()))
						r.GetState().SetLinkType(linkType)
					}
					if r.GetState().GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
						r.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING)
					}
					return nil
				})
				if err != nil {
					return nil, connectErr(err)
				}
				s.NotifyStoreChange()
			}
			sessionStarted = true
		}

		if len(chunk.GetPackets()) > 0 {
			if err := s.store.AppendPackets(ctx, sessionID, linkType, snapLen, chunk.GetPackets()); err != nil {
				return nil, connectErr(err)
			}
		}

		if dropped := s.broadcaster.Broadcast(sessionID, chunk); dropped > 0 && !droppedReported {
			// Once per stream: a tail that stalls for a whole capture drops
			// on every chunk, and one line per chunk would bury the rest.
			droppedReported = true
			s.log.WarnContext(ctx, "live capture tail fell behind and lost a chunk",
				slog.String("flowseer.capture.session.id", sessionID),
				slog.Uint64("flowseer.capture.chunk.first_sequence", chunk.GetFirstSequence()),
				slog.Int("flowseer.capture.tail.dropped_subscribers", dropped))
		}

		if chunk.GetFinal() {
			expiresAt := s.clock().Add(s.retentionPeriod)
			artifact, err := s.store.FinalizeArtifact(ctx, sessionID, linkType, snapLen, chunk.GetCounters(), expiresAt)
			if err != nil {
				return nil, connectErr(err)
			}

			_, err = s.store.MutateSession(ctx, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
				r.GetState().SetCounters(chunk.GetCounters())
				r.GetState().SetEndedAt(timestamppb.New(s.clock()))
				r.GetState().SetArtifact(artifact)
				if r.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
					r.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
					if r.GetState().GetStopReason() == modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_UNSPECIFIED {
						r.GetState().SetStopReason(deriveStopReason(r.GetConfig().GetBudget(), chunk.GetCounters()))
					}
				}
				return nil
			})
			if err != nil {
				// The artifact exists and its record does not name it, so
				// nothing will ever reach those bytes again.
				s.store.DiscardArtifact(sessionID)
				return nil, connectErr(err)
			}

			finalized = true
			s.broadcaster.CloseSession(sessionID)
			s.NotifyStoreChange()

			resp := captureedgev1.UploadCaptureResponse_builder{
				Session: sessionRef,
			}.Build()
			return connect.NewResponse(resp), nil
		}
	}

	// The read ended. Whether that was the transport deadline this handler
	// set, a reset, or an orderly half-close, the stream carried no final
	// chunk, so the session stops here either way: a session left RUNNING
	// with no stream behind it is one nothing ever retries or reports.
	if s.clock().Sub(lastAssertionAt) > s.assertionWindow {
		s.failStream(ctx, sessionID, "assertion window lapsed")
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("assertion window lapsed past deadline"))
	}

	if err := stream.Err(); err != nil {
		s.failStream(ctx, sessionID, "upload stream failed")
		return nil, connecterr.WrapAs(connect.CodeUnknown, "the upload stream did not complete", err)
	}

	s.failStream(ctx, sessionID, "upload stream ended before its final chunk")
	return nil, connect.NewError(connect.CodeDataLoss, errors.New("upload stream terminated before final chunk"))
}

// deadlineExtender returns a func that pushes the transport read deadline out
// by one assertion window. Where the transport carries no deadline it logs
// once and returns a no-op, because the caller's other check still holds for
// an edge that keeps sending.
func (s *EdgeService) deadlineExtender(ctx context.Context) func() {
	set := readDeadlineFrom(ctx)
	if set == nil {
		s.log.WarnContext(ctx, "capture upload stream carries no read deadline; a silent edge holds it open until the client disconnects")
		return func() {}
	}
	unsupported := false
	return func() {
		if unsupported {
			return
		}
		if err := set(time.Now().Add(s.assertionWindow)); err != nil {
			unsupported = true
			s.log.WarnContext(ctx, "capture upload stream could not take a read deadline",
				slog.String("error.type", telemetry.ErrorType(err)))
		}
	}
}

// failStream records that an upload ended without completing its capture. It
// is a no-op before the first chunk named a session, and it never overwrites a
// session that already reached a terminal state: an operator's cancellation
// and its recorded reason outlive the stream that was serving it.
func (s *EdgeService) failStream(ctx context.Context, sessionID, reason string) {
	if sessionID == "" {
		return
	}

	// The reasons this is reached are mostly reasons ctx is already dead: the
	// read deadline fired, or the peer went away. Recording why the capture
	// stopped is the last thing this handler owes, and it cannot be done on a
	// canceled context, so cancellation is dropped and a bound put back.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failStreamTimeout)
	defer cancel()

	changed := false
	if _, err := s.store.MutateSession(ctx, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		if lifecycleIsTerminal(rec.GetState().GetLifecycle()) {
			changed = false
			return nil
		}
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED)
		rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR)
		rec.GetState().SetEndedAt(timestamppb.New(s.clock()))
		changed = true
		return nil
	}); err != nil {
		s.log.ErrorContext(ctx, "capture session could not be marked failed",
			slog.String("flowseer.capture.session.id", sessionID),
			slog.String("flowseer.capture.stop.reason", reason),
			slog.String("error.type", telemetry.ErrorType(err)))
		return
	}

	if !changed {
		return
	}
	s.log.WarnContext(ctx, "capture session failed",
		slog.String("flowseer.capture.session.id", sessionID),
		slog.String("flowseer.capture.stop.reason", reason))
	// The tails watching this session end with it; only the final-chunk path
	// closes them otherwise, and this stream will not reach it.
	s.broadcaster.CloseSession(sessionID)
	s.NotifyStoreChange()
}

// lifecycleIsTerminal reports whether a session's lifecycle has settled. A
// cancellation is settled the moment an operator records it; what the edge
// still owes is the artifact, not a lifecycle change.
func lifecycleIsTerminal(lifecycle modelcapturev1.CaptureLifecycle) bool {
	switch lifecycle {
	case modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED,
		modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED,
		modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED:
		return true
	default:
		return false
	}
}

// captureIsOver reports whether a session will accept no further packets: it
// stopped for good, or it was canceled and the edge already flushed the final
// chunk that produced its artifact.
func captureIsOver(state *modelcapturev1.CaptureSessionState) bool {
	if state.GetArtifact() != nil {
		return true
	}
	return state.GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED &&
		lifecycleIsTerminal(state.GetLifecycle())
}

func deriveStopReason(budget *modelcapturev1.CaptureBudget, counters *netcapturev1.CaptureCounters) modelcapturev1.CaptureStopReason {
	if budget == nil {
		return modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT
	}
	if budget.HasMaxPackets() && counters != nil && counters.GetAccepted() >= budget.GetMaxPackets() {
		return modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT
	}
	if budget.HasMaxDuration() {
		return modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_DURATION
	}
	if budget.HasMaxBytes() {
		return modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_BYTE_COUNT
	}
	return modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT
}
