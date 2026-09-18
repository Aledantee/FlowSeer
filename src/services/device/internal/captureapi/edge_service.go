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
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
)

const (
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

// Broadcast distributes a packet chunk to all subscribers of sessionID.
func (b *Broadcaster) Broadcast(sessionID string, chunk *modelcapturev1.CapturePacketChunk) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	set, ok := b.subs[sessionID]
	if !ok {
		return
	}
	for ch := range set {
		select {
		case ch <- chunk:
		default:
			// Subscriber buffer full; drop to prevent blocking the upload stream
		}
	}
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
	}
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
		return connect.NewError(connect.CodeUnauthenticated, err)
	}

	notifyCh, unwatch := s.registerWatcher()
	defer unwatch()

	ticker := time.NewTicker(s.resendInterval)
	defer ticker.Stop()

	sendOwed := func() error {
		sessions, err := s.store.ListSessions(ctx)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
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
			case lifecycle == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED && rec.GetState().GetArtifact() == nil:
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

	// The stream must open with a SignedEdgeAssertion.
	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("upload stream closed without opening assertion"))
	}

	firstMsg := stream.Msg()
	firstSigned := firstMsg.GetAssertion()
	if firstSigned == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("upload stream must open with SignedEdgeAssertion"))
	}

	firstAssertion, err := s.verifier.VerifySigned(ctx, firstSigned, procedure, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	callingEdgeID := firstAssertion.GetEdge().GetEdge().GetId()
	lastAssertionAt := s.clock()

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		sessionID      string
		sessionRef     *modelcapturev1.CaptureSessionGlobalRef
		sessionStarted bool
		linkType              = netcapturev1.LinkType_LINK_TYPE_ETHERNET
		snapLen        uint32 = 128
		timerMu        sync.Mutex
		lapsed         bool
	)

	timer := time.AfterFunc(s.assertionWindow, func() {
		timerMu.Lock()
		lapsed = true
		timerMu.Unlock()
		cancel()
	})
	defer timer.Stop()

	for stream.Receive() {
		now := s.clock()
		timerMu.Lock()
		timeLapsed := lapsed || now.Sub(lastAssertionAt) > s.assertionWindow
		timerMu.Unlock()

		if timeLapsed {
			if sessionID != "" {
				_ = s.failSession(sessionID)
			}
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("assertion window lapsed past deadline"))
		}

		msg := stream.Msg()

		if signed := msg.GetAssertion(); signed != nil {
			assertion, err := s.verifier.VerifySigned(streamCtx, signed, procedure, nil)
			if err != nil {
				if sessionID != "" {
					_ = s.failSession(sessionID)
				}
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}
			if assertion.GetEdge().GetEdge().GetId() != callingEdgeID {
				if sessionID != "" {
					_ = s.failSession(sessionID)
				}
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("assertion edge changed mid-stream"))
			}

			lastAssertionAt = s.clock()
			timer.Reset(s.assertionWindow)
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

		if !sessionStarted {
			sessionRef = chunk.GetSession()
			sessionID = sessionRef.GetCaptureSession().GetId()

			rec, _, err := s.store.GetSession(streamCtx, sessionID)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
			if rec == nil {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("capture session not found"))
			}

			if rec.GetConfig().GetRef().GetEdge().GetEdge().GetId() != callingEdgeID {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("session does not belong to calling edge"))
			}

			if rec.GetState().HasLinkType() {
				linkType = rec.GetState().GetLinkType()
			}
			if b := rec.GetConfig().GetBudget(); b != nil && b.GetSnapLength() > 0 {
				snapLen = b.GetSnapLength()
			}
			if rec.GetConfig().GetAuthorization().GetFullPayloadRequested() {
				snapLen = 65535
			}

			if rec.GetState().GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
				_, err = s.store.MutateSession(streamCtx, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
					if r.GetState().GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
						r.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING)
						r.GetState().SetStartedAt(timestamppb.New(s.clock()))
						r.GetState().SetLinkType(linkType)
					}
					return nil
				})
				if err != nil {
					return nil, connect.NewError(connect.CodeInternal, err)
				}
				s.NotifyStoreChange()
			}
			sessionStarted = true
		}

		if len(chunk.GetPackets()) > 0 {
			if err := s.store.AppendPackets(streamCtx, sessionID, linkType, snapLen, chunk.GetPackets()); err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
		}

		s.broadcaster.Broadcast(sessionID, chunk)

		if chunk.GetFinal() {
			expiresAt := s.clock().Add(s.retentionPeriod)
			artifact, err := s.store.FinalizeArtifact(streamCtx, sessionID, linkType, snapLen, chunk.GetCounters(), expiresAt)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}

			_, err = s.store.MutateSession(streamCtx, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
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
				return nil, connect.NewError(connect.CodeInternal, err)
			}

			s.broadcaster.CloseSession(sessionID)
			s.NotifyStoreChange()

			resp := captureedgev1.UploadCaptureResponse_builder{
				Session: sessionRef,
			}.Build()
			return connect.NewResponse(resp), nil
		}
	}

	timerMu.Lock()
	timeLapsed := lapsed || s.clock().Sub(lastAssertionAt) > s.assertionWindow
	timerMu.Unlock()

	if timeLapsed {
		if sessionID != "" {
			_ = s.failSession(sessionID)
		}
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("assertion window lapsed past deadline"))
	}

	if err := stream.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			timerMu.Lock()
			wasLapsed := lapsed
			timerMu.Unlock()
			if wasLapsed {
				if sessionID != "" {
					_ = s.failSession(sessionID)
				}
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("assertion window lapsed past deadline"))
			}
		}
		return nil, connect.NewError(connect.CodeUnknown, err)
	}

	return nil, connect.NewError(connect.CodeDataLoss, errors.New("upload stream terminated before final chunk"))
}

func (s *EdgeService) failSession(sessionID string) error {
	ctx := context.Background()
	_, err := s.store.MutateSession(ctx, sessionID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED)
		rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR)
		rec.GetState().SetEndedAt(timestamppb.New(s.clock()))
		return nil
	})
	s.NotifyStoreChange()
	return err
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
