package captureapi

import (
	"context"
	"errors"
	"os"
	"time"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	operatorcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
)

const (
	defaultPageSize = 50
	maxPageSize     = 500
)

// OperatorServiceConfig carries configuration options for OperatorService.
type OperatorServiceConfig struct {
	NotifyChange func()
	Clock        func() time.Time
}

// OperatorService implements capturev1connect.CaptureServiceHandler.
type OperatorService struct {
	store        *Store
	broadcaster  *Broadcaster
	notifyChange func()
	clock        func() time.Time
}

// Ensure OperatorService satisfies CaptureServiceHandler.
var _ capturev1connect.CaptureServiceHandler = (*OperatorService)(nil)

// NewOperatorService constructs an OperatorService.
func NewOperatorService(store *Store, broadcaster *Broadcaster, cfg OperatorServiceConfig) *OperatorService {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	notify := cfg.NotifyChange
	if notify == nil {
		notify = func() {}
	}
	return &OperatorService{
		store:        store,
		broadcaster:  broadcaster,
		notifyChange: notify,
		clock:        clock,
	}
}

// CreateCaptureSession creates and persists a new capture session in PENDING state.
func (s *OperatorService) CreateCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.CreateCaptureSessionRequest],
) (*connect.Response[operatorcapturev1.CreateCaptureSessionResponse], error) {
	msg := req.Msg

	if msg.GetEdge() == nil || msg.GetEdge().GetEdge().GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("edge is required"))
	}
	if msg.GetSource() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("capture source is required"))
	}
	if msg.GetAuthorization() == nil || msg.GetAuthorization().GetOperator() == "" || msg.GetAuthorization().GetReason() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("authorization is required"))
	}

	budget := msg.GetBudget()
	if budget == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("budget is required"))
	}
	hasBound := (budget.HasMaxPackets() && budget.GetMaxPackets() > 0) ||
		(budget.HasMaxBytes() && budget.GetMaxBytes() > 0) ||
		(budget.HasMaxDuration() && budget.GetMaxDuration().AsDuration() > 0)
	if !hasBound {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("capture budget must bound packets, bytes, or duration"))
	}

	sessionID := uuid.NewString()
	globalRef := modelcapturev1.CaptureSessionGlobalRef_builder{
		Edge: msg.GetEdge(),
		CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
			Id: proto.String(sessionID),
		}.Build(),
	}.Build()

	configBuilder := modelcapturev1.CaptureSessionConfig_builder{
		Ref:           globalRef,
		Source:        msg.GetSource(),
		Budget:        msg.GetBudget(),
		Authorization: msg.GetAuthorization(),
	}
	if msg.HasName() {
		configBuilder.Name = proto.String(msg.GetName())
	}
	if msg.HasDescription() {
		configBuilder.Description = proto.String(msg.GetDescription())
	}
	if msg.GetFilter() != nil {
		configBuilder.Filter = msg.GetFilter()
	}

	rec, err := s.store.CreateSession(ctx, configBuilder.Build())
	if err != nil {
		return nil, connectErr(err)
	}

	s.notifyChange()

	resp := operatorcapturev1.CreateCaptureSessionResponse_builder{
		Session: rec,
	}.Build()
	return connect.NewResponse(resp), nil
}

// StopCaptureSession cancels an active or pending capture session.
func (s *OperatorService) StopCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.StopCaptureSessionRequest],
) (*connect.Response[operatorcapturev1.StopCaptureSessionResponse], error) {
	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session id is required"))
	}

	rec, err := s.store.MutateSession(ctx, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
		lifecycle := r.GetState().GetLifecycle()
		if lifecycle == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED ||
			lifecycle == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED ||
			lifecycle == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
			return nil
		}

		r.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED)
		r.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR)
		if lifecycle == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
			r.GetState().SetEndedAt(timestamppb.New(s.clock()))
		}
		return nil
	})
	if err != nil {
		return nil, connectErr(err)
	}

	s.notifyChange()

	resp := operatorcapturev1.StopCaptureSessionResponse_builder{
		Session: rec,
	}.Build()
	return connect.NewResponse(resp), nil
}

// GetCaptureSession retrieves a session record.
func (s *OperatorService) GetCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.GetCaptureSessionRequest],
) (*connect.Response[operatorcapturev1.GetCaptureSessionResponse], error) {
	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session id is required"))
	}

	rec, _, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, connectErr(err)
	}
	if rec == nil {
		return nil, errNoSuchSession()
	}

	resp := operatorcapturev1.GetCaptureSessionResponse_builder{
		Session: rec,
	}.Build()
	return connect.NewResponse(resp), nil
}

// ListCaptureSessions lists session records with pagination.
func (s *OperatorService) ListCaptureSessions(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.ListCaptureSessionsRequest],
) (*connect.Response[operatorcapturev1.ListCaptureSessionsResponse], error) {
	all, err := s.store.ListSessions(ctx)
	if err != nil {
		return nil, connectErr(err)
	}

	pageSize := defaultPageSize
	if req.Msg.HasPageSize() && req.Msg.GetPageSize() > 0 {
		pageSize = int(req.Msg.GetPageSize())
		if pageSize > maxPageSize {
			pageSize = maxPageSize
		}
	}

	pageToken := req.Msg.GetPageToken()
	startIndex := 0
	if pageToken != "" {
		for i, rec := range all {
			if rec.GetConfig().GetRef().GetCaptureSession().GetId() > pageToken {
				startIndex = i
				break
			}
			startIndex = len(all)
		}
	}

	endIndex := startIndex + pageSize
	if endIndex > len(all) {
		endIndex = len(all)
	}

	var page []*modelcapturev1.CaptureSessionRecord
	if startIndex < len(all) {
		page = all[startIndex:endIndex]
	}

	respBuilder := operatorcapturev1.ListCaptureSessionsResponse_builder{
		Sessions: page,
	}
	if endIndex < len(all) && len(page) > 0 {
		nextID := page[len(page)-1].GetConfig().GetRef().GetCaptureSession().GetId()
		respBuilder.NextPageToken = proto.String(nextID)
	}

	return connect.NewResponse(respBuilder.Build()), nil
}

// DeleteCaptureSession purges a session record and unlinks its artifact file.
func (s *OperatorService) DeleteCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.DeleteCaptureSessionRequest],
) (*connect.Response[operatorcapturev1.DeleteCaptureSessionResponse], error) {
	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session id is required"))
	}

	if err := s.store.DeleteSession(ctx, sessionID); err != nil {
		return nil, connectErr(err)
	}

	s.broadcaster.CloseSession(sessionID)
	s.notifyChange()

	return connect.NewResponse(operatorcapturev1.DeleteCaptureSessionResponse_builder{}.Build()), nil
}

// TailCaptureSession streams live packet chunks from active uploads.
func (s *OperatorService) TailCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.TailCaptureSessionRequest],
	stream *connect.ServerStream[operatorcapturev1.TailCaptureSessionResponse],
) error {
	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("session id is required"))
	}

	rec, _, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return connectErr(err)
	}
	if rec == nil {
		return errNoSuchSession()
	}

	ch, unsub := s.broadcaster.Subscribe(sessionID)
	defer unsub()

	// Subscribe first, then read the session again. Only the upload relay
	// closes a tail's channel, and it does so once, when the capture ends.
	// A tail that opened after that moment would wait on a channel nobody
	// will ever send to or close; re-reading under the subscription is what
	// catches the session that finished in between.
	rec, _, err = s.store.GetSession(ctx, sessionID)
	if err != nil {
		return connectErr(err)
	}
	if rec == nil || lifecycleIsTerminal(rec.GetState().GetLifecycle()) {
		return nil
	}

	// Only now can the caller know that no chunk the session uploads will be
	// missed, and only this side knows when that became true.
	if err := stream.Send(operatorcapturev1.TailCaptureSessionResponse_builder{
		Attached: proto.Bool(true),
	}.Build()); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				return nil
			}
			resp := operatorcapturev1.TailCaptureSessionResponse_builder{
				Chunk: chunk,
			}.Build()
			if err := stream.Send(resp); err != nil {
				return err
			}
			if chunk.GetFinal() {
				return nil
			}
		}
	}
}

// DownloadCaptureSession streams pcapng artifact chunks <=1MB from disk.
func (s *OperatorService) DownloadCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.DownloadCaptureSessionRequest],
	stream *connect.ServerStream[operatorcapturev1.DownloadCaptureSessionResponse],
) error {
	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("session id is required"))
	}

	rec, _, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return connectErr(err)
	}
	if rec == nil {
		return errNoSuchSession()
	}

	// The record decides what may be served, not the file. A capture still
	// running has a pcapng on disk that is missing its closing block and
	// whose bytes will not match the digest the session is about to record,
	// and an expired one may still be on disk for as long as a sweep tick:
	// asking the filesystem alone would serve both as a finished artifact.
	artifact := rec.GetState().GetArtifact()
	switch {
	case artifact == nil:
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("this capture has not finished; there is nothing to download yet"))
	case artifact.HasPurgedAt() || !s.clock().Before(artifact.GetExpiresAt().AsTime()):
		return connect.NewError(connect.CodeNotFound, errors.New("this session's capture is no longer retained"))
	}

	err = s.store.ReadArtifact(ctx, sessionID, func(chunk *modelcapturev1.CaptureArtifactChunk) error {
		resp := operatorcapturev1.DownloadCaptureSessionResponse_builder{
			Chunk: chunk,
		}.Build()
		return stream.Send(resp)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return connect.NewError(connect.CodeNotFound, errors.New("this session's capture is no longer stored"))
		}
		return connectErr(err)
	}
	return nil
}

func errNoSuchSession() error {
	return connect.NewError(connect.CodeNotFound, errors.New("no such capture session"))
}
