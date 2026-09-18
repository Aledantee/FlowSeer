package capture

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/modules/capture"
)

// Default intervals for capture session streaming.
const (
	defaultReassertInterval  = 30 * time.Second
	defaultInactivityTimeout = 60 * time.Second
)

// HandlerConfig configures [Handler].
type HandlerConfig struct {
	Client            capturev1connect.CaptureEdgeServiceClient
	SignAssertion     func(ctx context.Context) (*edgev1.SignedEdgeAssertion, error)
	OpenCaptureSource func(ctx context.Context, cfg capture.Config) (capture.Source, bool, error)
	InactivityTimeout time.Duration
	ReassertInterval  time.Duration
	Logger            *slog.Logger
}

type activeSession struct {
	cancelEngine context.CancelFunc
	abortUpload  context.CancelFunc
	stopping     atomic.Bool
	done         chan struct{}
}

// Handler manages active capture sessions assigned by central. It implements
// [subscribeloop.Handler] to demultiplex incoming assignments into dedicated
// session runner goroutines. Safe for concurrent use.
type Handler struct {
	client            capturev1connect.CaptureEdgeServiceClient
	signAssertion     func(ctx context.Context) (*edgev1.SignedEdgeAssertion, error)
	openSource        func(ctx context.Context, cfg capture.Config) (capture.Source, bool, error)
	inactivityTimeout time.Duration
	reassertInterval  time.Duration
	logger            *slog.Logger

	mu       sync.Mutex
	sessions map[string]*activeSession
	closed   bool
}

// NewHandler constructs a Handler from cfg.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("capture: Client is required")
	}
	if cfg.SignAssertion == nil {
		return nil, fmt.Errorf("capture: SignAssertion is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	reassertInterval := cfg.ReassertInterval
	if reassertInterval <= 0 {
		reassertInterval = defaultReassertInterval
	}
	inactivityTimeout := cfg.InactivityTimeout
	if inactivityTimeout <= 0 {
		inactivityTimeout = defaultInactivityTimeout
	}

	return &Handler{
		client:            cfg.Client,
		signAssertion:     cfg.SignAssertion,
		openSource:        cfg.OpenCaptureSource,
		inactivityTimeout: inactivityTimeout,
		reassertInterval:  reassertInterval,
		logger:            logger,
		sessions:          make(map[string]*activeSession),
	}, nil
}

// Handle processes one assignment from SubscribeCaptureAssignments. It returns
// promptly so the subscribe loop is not blocked, launching active sessions in
// their own supervised goroutines.
func (h *Handler) Handle(ctx context.Context, msg *captureedgev1.SubscribeCaptureAssignmentsResponse) error {
	if msg == nil {
		return nil
	}

	switch {
	case msg.GetStart() != nil:
		return h.handleStart(ctx, msg.GetStart())
	case msg.GetStop() != nil:
		return h.handleStop(ctx, msg.GetStop())
	default:
		return nil
	}
}

func (h *Handler) handleStart(_ context.Context, cfg *modelcapturev1.CaptureSessionConfig) error {
	ref := cfg.GetRef()
	if ref == nil || ref.GetCaptureSession() == nil {
		return fmt.Errorf("start assignment missing session ref")
	}
	sessionID := ref.GetCaptureSession().GetId()
	if sessionID == "" {
		return fmt.Errorf("start assignment missing session ID")
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return context.Canceled
	}
	if _, running := h.sessions[sessionID]; running {
		h.mu.Unlock()
		return nil
	}

	sessionCtx, abortUpload := context.WithCancel(context.Background())
	engineCtx, cancelEngine := context.WithCancel(sessionCtx)
	sess := &activeSession{
		cancelEngine: cancelEngine,
		abortUpload:  abortUpload,
		done:         make(chan struct{}),
	}
	h.sessions[sessionID] = sess
	h.mu.Unlock()

	spawn.Go(sessionCtx, "agent.capture.runner", func() {
		h.runSession(sessionCtx, engineCtx, cfg, sess)
	})
	return nil
}

func (h *Handler) handleStop(_ context.Context, ref *modelcapturev1.CaptureSessionGlobalRef) error {
	if ref == nil || ref.GetCaptureSession() == nil {
		return nil
	}
	sessionID := ref.GetCaptureSession().GetId()
	if sessionID == "" {
		return nil
	}

	h.mu.Lock()
	sess, ok := h.sessions[sessionID]
	h.mu.Unlock()
	if !ok {
		return nil
	}

	sess.stopping.Store(true)
	sess.cancelEngine()
	return nil
}

func (h *Handler) removeSession(sessionID string) {
	h.mu.Lock()
	sess, ok := h.sessions[sessionID]
	if ok {
		delete(h.sessions, sessionID)
		close(sess.done)
	}
	h.mu.Unlock()
}

// ActiveSessions returns the count of currently running capture sessions.
func (h *Handler) ActiveSessions() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

// Close stops all in-flight capture sessions and prevents new sessions from
// starting.
func (h *Handler) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	var toWait []chan struct{}
	for _, sess := range h.sessions {
		sess.abortUpload()
		sess.cancelEngine()
		toWait = append(toWait, sess.done)
	}
	h.mu.Unlock()

	for _, done := range toWait {
		<-done
	}
	return nil
}
