package captureapi

import (
	"context"
	"log/slog"
	"sync"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.aledante.io/FlowSeer/src/common/errs"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

const (
	// failStreamTimeout bounds the record write that closes out a broken
	// upload stream, which runs on a context detached from the dead request.
	failStreamTimeout = 10 * time.Second

	// msgUnauthenticated is the whole of what a caller learns from a refused
	// assertion. Which verification check refused it is central's
	// business, and this route answers a caller that has not authenticated.
	msgUnauthenticated = "the call is not authorized as an enrolled edge"

	defaultAssertionWindow = 60 * time.Second
	defaultResendInterval  = 5 * time.Second
	defaultRetentionPeriod = 7 * 24 * time.Hour
)

// TailGapInfo counts what a lagging tail missed: the chunks and packets a
// subscriber was too far behind to receive, and the packet sequence range they
// spanned. The sequence bounds are meaningful only when DroppedPackets is
// positive; a dropped chunk that carried only stream markers bumps
// DroppedChunks and leaves them at zero.
type TailGapInfo struct {
	DroppedChunks        uint64
	DroppedPackets       uint64
	FirstDroppedSequence uint64
	LastDroppedSequence  uint64
}

// record folds one dropped chunk into the running gap. Chunks arrive in
// sequence order, so a later drop only extends the range's upper bound.
func (g *TailGapInfo) record(chunk *modelcapturev1.CapturePacketChunk) {
	g.DroppedChunks++
	n := uint64(len(chunk.GetPackets()))
	if n == 0 {
		return
	}
	first := chunk.GetFirstSequence()
	if g.DroppedPackets == 0 {
		g.FirstDroppedSequence = first
	}
	g.DroppedPackets += n
	g.LastDroppedSequence = first + n - 1
}

// TailItem is one delivery on a live tail: either a captured chunk, or a gap
// standing for chunks the subscriber was too far behind to receive. Exactly one
// of Chunk and Gap is set.
type TailItem struct {
	Chunk *modelcapturev1.CapturePacketChunk
	Gap   *TailGapInfo
}

// Subscription is one live tail's view of a capture session. Items delivers
// chunks and in-band gaps in order until the session ends, when it closes. A
// gap the channel had no room to carry — chunks dropped with nothing delivered
// after them — is not lost: after Items closes, read it from TerminalGap.
type Subscription struct {
	items chan TailItem
	// b and session let TerminalGap read the residual gap under the broadcaster
	// lock once the channel has closed.
	b       *Broadcaster
	session string
	// gap holds chunks dropped since the last delivered chunk, still waiting for
	// room on items. Guarded by b.mu.
	gap *TailGapInfo
}

// Items delivers tail chunks and in-band gaps in order. It is closed when the
// session ends.
func (s *Subscription) Items() <-chan TailItem {
	return s.items
}

// TerminalGap returns a gap for chunks dropped with nothing delivered after
// them, or nil when the tail lost nothing at the end. Call it only after Items
// has closed: until then a pending gap may still find room on the channel.
func (s *Subscription) TerminalGap() *TailGapInfo {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return s.gap
}

// deliver hands chunk to the subscriber, flushing any pending gap first so a
// gap always precedes the chunks that followed it. It reports whether chunk
// reached the channel; a chunk that did not is folded into the pending gap. The
// caller holds b.mu, and every send is non-blocking, so a slow reader never
// stalls a broadcast.
func (s *Subscription) deliver(chunk *modelcapturev1.CapturePacketChunk) bool {
	if s.gap != nil {
		select {
		case s.items <- TailItem{Gap: s.gap}:
			s.gap = nil
		default:
			// No room for the pending gap, so no room for a chunk behind it:
			// fold this chunk into the gap and keep the order.
			s.gap.record(chunk)
			return false
		}
	}
	select {
	case s.items <- TailItem{Chunk: chunk}:
		return true
	default:
		s.gap = &TailGapInfo{}
		s.gap.record(chunk)
		return false
	}
}

// Broadcaster manages live packet chunk subscriptions for active capture
// sessions. A Broadcaster is safe for concurrent use.
type Broadcaster struct {
	mu   sync.Mutex // guards subs and every subscription's gap
	subs map[string]map[*Subscription]struct{}
}

// NewBroadcaster returns an empty Broadcaster ready for concurrent use. The
// zero value is not usable.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: make(map[string]map[*Subscription]struct{}),
	}
}

// Subscribe registers a 128-item live tail for sessionID. The caller must call
// the returned function to unsubscribe. A slow reader may lose chunks but does
// not block the upload; the losses surface as gaps on the subscription.
func (b *Broadcaster) Subscribe(sessionID string) (*Subscription, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := &Subscription{
		items:   make(chan TailItem, 128),
		b:       b,
		session: sessionID,
	}
	set, ok := b.subs[sessionID]
	if !ok {
		set = make(map[*Subscription]struct{})
		b.subs[sessionID] = set
	}
	set[sub] = struct{}{}

	unsub := sync.OnceFunc(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.subs[sessionID]; ok {
			delete(s, sub)
			if len(s) == 0 {
				delete(b.subs, sessionID)
			}
		}
	})
	return sub, unsub
}

// Broadcast distributes a packet chunk to all subscribers of sessionID and
// returns how many were too far behind to take it. A tail that cannot keep up
// loses chunks rather than stalling the upload it is watching, which makes the
// live stream a subsequence of what the edge sent; each subscriber records the
// loss as a gap it delivers in-band once its channel drains.
func (b *Broadcaster) Broadcast(sessionID string, chunk *modelcapturev1.CapturePacketChunk) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	set, ok := b.subs[sessionID]
	if !ok {
		return 0
	}
	dropped := 0
	for sub := range set {
		if !sub.deliver(chunk) {
			dropped++
		}
	}
	return dropped
}

// CloseSession closes every subscriber channel for sessionID and releases the
// subscriber set. A subscriber's residual gap stays on its Subscription for the
// reader to flush through TerminalGap after it drains the channel. Repeated
// calls are safe.
func (b *Broadcaster) CloseSession(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if set, ok := b.subs[sessionID]; ok {
		for sub := range set {
			close(sub.items)
		}
		delete(b.subs, sessionID)
	}
}

// SubscriberCount returns the number of active subscribers for sessionID.
func (b *Broadcaster) SubscriberCount(sessionID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[sessionID])
}

// AssertionVerifier checks SignedEdgeAssertion envelopes.
type AssertionVerifier interface {
	VerifySigned(ctx context.Context, signed *edgev1.SignedEdgeAssertion, procedure string, body []byte) (*edgev1.EdgeAssertion, error)
}

// EdgeServiceConfig configures an [EdgeService]. Non-positive durations use
// package defaults. Nil EdgeID reads the verified edge from the context, nil
// Clock uses the wall clock, and nil Logger discards records.
type EdgeServiceConfig struct {
	AssertionWindow time.Duration
	ResendInterval  time.Duration
	RetentionPeriod time.Duration
	EdgeID          func(context.Context) (string, error)
	EdgeTenant      func(edgeID string) string
	Clock           func() time.Time
	Logger          *slog.Logger
}

// EdgeService serves capture assignments and uploads from authenticated edges.
// An EdgeService is safe for concurrent use when its verifier and configured
// EdgeID, EdgeTenant, and Clock callbacks are safe for concurrent use.
type EdgeService struct {
	store           *Store
	verifier        AssertionVerifier
	broadcaster     *Broadcaster
	assertionWindow time.Duration
	resendInterval  time.Duration
	retentionPeriod time.Duration
	edgeID          func(context.Context) (string, error)
	edgeTenant      func(edgeID string) string
	clock           func() time.Time
	log             *slog.Logger

	mu       sync.Mutex // guards watchers and uploads
	watchers map[chan struct{}]struct{}
	uploads  map[string]struct{}
}

var _ capturev1connect.CaptureEdgeServiceHandler = (*EdgeService)(nil)

// NewEdgeService constructs an EdgeService. Store, verifier, and broadcaster
// must be non-nil; cfg uses the defaults documented by [EdgeServiceConfig].
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

	edgeTenant := cfg.EdgeTenant
	if edgeTenant == nil {
		edgeTenant = func(string) string { return edgebus.DefaultTenant }
	}

	return &EdgeService{
		store:           store,
		verifier:        verifier,
		broadcaster:     broadcaster,
		assertionWindow: assertionWindow,
		resendInterval:  resendInterval,
		retentionPeriod: retentionPeriod,
		edgeID:          edgeIDFunc,
		edgeTenant:      edgeTenant,
		clock:           clock,
		log:             logger,
		watchers:        make(map[chan struct{}]struct{}),
		uploads:         make(map[string]struct{}),
	}
}

func (s *EdgeService) resolveTenant(edgeID string) string {
	if s.edgeTenant != nil {
		if t := s.edgeTenant(edgeID); t != "" {
			return t
		}
	}
	return edgebus.DefaultTenant
}

// unauthenticated returns a Connect error with [connect.CodeUnauthenticated]
// and [msgUnauthenticated]. Wire output carries only the public status, and
// err remains in the cause chain for server-side logging.
func unauthenticated(err error) error {
	if err == nil {
		err = errs.Msg(msgUnauthenticated)
	}
	return connecterr.WrapRefused(msgUnauthenticated, err)
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
		return unauthenticated(err)
	}
	tenantID := s.resolveTenant(edgeID)

	notifyCh, unwatch := s.registerWatcher()
	defer unwatch()

	ticker := time.NewTicker(s.resendInterval)
	defer ticker.Stop()

	sendOwed := func() error {
		sessions, err := s.store.ListSessions(ctx, tenantID)
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

// UploadCapture accepts one session's packet chunks and fresh assertions from
// an authenticated edge. A refused assertion reveals only the public
// unauthenticated message; which verification check refused it stays internal.
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

	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return nil, unauthenticated(err)
		}
		return nil, unauthenticated(errs.Msg("stream closed before opening assertion"))
	}

	firstMsg := stream.Msg()
	firstSigned := firstMsg.GetAssertion()
	if firstSigned == nil {
		return nil, unauthenticated(errs.Msg("opening message carries no assertion"))
	}

	firstAssertion, err := s.verifier.VerifySigned(ctx, firstSigned, procedure, nil)
	if err != nil {
		return nil, unauthenticated(err)
	}

	callingEdgeID := firstAssertion.GetEdge().GetEdge().GetId()
	tenantID := s.resolveTenant(callingEdgeID)
	lastAssertionAt := s.clock()

	var (
		sessionID      string
		sessionRef     *modelcapturev1.CaptureSessionGlobalRef
		sessionStarted bool
		linkType              = netcapturev1.LinkType_LINK_TYPE_ETHERNET
		snapLen        uint32 = 128
	)

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
			s.store.AbandonWriter(tenantID, sessionID)
		}
		s.releaseUpload(sessionID)
	}()

	for stream.Receive() {
		now := s.clock()
		if now.Sub(lastAssertionAt) > s.assertionWindow {
			s.failStream(ctx, tenantID, sessionID, "assertion window lapsed")
			return nil, unauthenticated(errs.Msg("assertion window lapsed past deadline"))
		}

		msg := stream.Msg()

		if signed := msg.GetAssertion(); signed != nil {
			assertion, err := s.verifier.VerifySigned(ctx, signed, procedure, nil)
			if err != nil {
				s.failStream(ctx, tenantID, sessionID, "mid-stream assertion did not verify")
				return nil, unauthenticated(err)
			}
			if assertion.GetEdge().GetEdge().GetId() != callingEdgeID {
				s.failStream(ctx, tenantID, sessionID, "assertion edge changed mid-stream")
				return nil, connect.NewError(connect.CodePermissionDenied, errs.Msg("assertion edge changed mid-stream"))
			}

			lastAssertionAt = s.clock()
			extendDeadline()
			continue
		}

		chunk := msg.GetChunk()
		if chunk == nil {
			continue
		}

		chunkEdgeID := chunk.GetSession().GetEdge().GetEdge().GetId()
		if chunkEdgeID != callingEdgeID {
			return nil, connect.NewError(connect.CodePermissionDenied, errs.Msg("chunk session edge does not match authenticated edge"))
		}

		if sessionStarted && chunk.GetSession().GetCaptureSession().GetId() != sessionID {
			// One stream carries one session: its packets go into one pcapng,
			// and only the first chunk's ref was resolved against a record.
			return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("chunk names a different capture session than the stream opened with"))
		}

		if !sessionStarted {
			sessionRef = chunk.GetSession()
			sessionID = sessionRef.GetCaptureSession().GetId()

			rec, _, err := s.store.Session(ctx, tenantID, sessionID)
			if err != nil {
				return nil, connectErr(err)
			}
			if rec == nil {
				return nil, errNoSuchSession()
			}

			if rec.GetConfig().GetRef().GetEdge().GetEdge().GetId() != callingEdgeID {
				return nil, connect.NewError(connect.CodePermissionDenied, errs.Msg("session does not belong to calling edge"))
			}

			// A session that has already produced its artifact is an audit
			// record. Central authenticates the edge but does not trust its
			// view of the session, so a second stream naming a finished
			// session is refused here rather than allowed to rewrite the
			// stored capture and the digest that describes it. A canceled
			// session stays open until its artifact exists: flushing the
			// final chunk is how the edge answers a stop.
			if captureIsOver(rec.GetState()) {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errs.Msg("capture session has already stopped"))
			}

			if !s.claimUpload(sessionID) {
				return nil, connect.NewError(connect.CodeAlreadyExists, errs.Msg("another stream is already uploading this capture session"))
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
				_, err = s.store.MutateSession(ctx, tenantID, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
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
			if err := s.store.AppendPackets(ctx, tenantID, sessionID, linkType, snapLen, chunk.GetPackets()); err != nil {
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
			artifact, err := s.store.FinalizeArtifact(ctx, tenantID, sessionID, linkType, snapLen, chunk.GetCounters(), expiresAt)
			if err != nil {
				return nil, connectErr(err)
			}

			_, err = s.store.MutateSession(ctx, tenantID, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
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
				s.store.DiscardArtifact(tenantID, sessionID)
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
		s.failStream(ctx, tenantID, sessionID, "assertion window lapsed")
		return nil, unauthenticated(errs.Msg("assertion window lapsed past deadline"))
	}

	if err := stream.Err(); err != nil {
		s.failStream(ctx, tenantID, sessionID, "upload stream failed")
		return nil, connecterr.WrapAs(connect.CodeUnknown, "the upload stream did not complete", err)
	}

	s.failStream(ctx, tenantID, sessionID, "upload stream ended before its final chunk")
	return nil, connect.NewError(connect.CodeDataLoss, errs.Msg("upload stream terminated before final chunk"))
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
func (s *EdgeService) failStream(ctx context.Context, tenantID, sessionID, reason string) {
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
	if _, err := s.store.MutateSession(ctx, tenantID, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
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
	if budget.HasMaxPackets() && counters != nil && counters.GetAcceptedPackets() >= budget.GetMaxPackets() {
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
