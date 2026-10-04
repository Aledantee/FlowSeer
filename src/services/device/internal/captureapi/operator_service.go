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

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"

	operatorcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

const (
	defaultPageSize = 50
	maxPageSize     = 500

	msgOperatorUnauthenticated = "the call is not authenticated"
)

// ErrCodeFullPayloadExpired identifies a full-payload request without an active
// member grant, even when its authorization tuple has not yet been reconciled.
var ErrCodeFullPayloadExpired = errs.NewCode("captureapi/full-payload-expired")

func unauthenticatedOperator(err error) error {
	if err == nil {
		err = errs.Msg(msgOperatorUnauthenticated)
	}
	return connecterr.WrapRefused(msgOperatorUnauthenticated, err)
}

// OperatorServiceConfig configures an [OperatorService]. Nil NotifyChange and
// Project callbacks are no-ops, and nil Clock uses the wall clock. Nil
// FullPayload refuses full-payload creation with CodeUnavailable. FullPayload
// reads the authenticated operator's member grant. False means expired or absent,
// and an error refuses creation with CodeUnavailable.
type OperatorServiceConfig struct {
	EdgeTenant   func(ctx context.Context, edgeID string) (string, error)
	FullPayload  func(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (bool, error)
	NotifyChange func()
	Clock        func() time.Time
	Project      func(ctx context.Context, objectType, id string)
}

// OperatorService serves operator capture requests. An OperatorService is safe
// for concurrent use when all its configured callbacks are safe for concurrent use.
type OperatorService struct {
	store        *Store
	broadcaster  *Broadcaster
	edgeTenant   func(ctx context.Context, edgeID string) (string, error)
	fullPayload  func(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (bool, error)
	notifyChange func()
	clock        func() time.Time
	project      func(ctx context.Context, objectType, id string)
}

var _ capturev1connect.CaptureServiceHandler = (*OperatorService)(nil)

// NewOperatorService constructs an OperatorService. Store and broadcaster must
// be non-nil; cfg uses the defaults documented by [OperatorServiceConfig].
func NewOperatorService(store *Store, broadcaster *Broadcaster, cfg OperatorServiceConfig) *OperatorService {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	notify := cfg.NotifyChange
	if notify == nil {
		notify = func() {}
	}
	project := cfg.Project
	if project == nil {
		project = func(context.Context, string, string) {}
	}
	return &OperatorService{
		store:        store,
		broadcaster:  broadcaster,
		edgeTenant:   cfg.EdgeTenant,
		fullPayload:  cfg.FullPayload,
		notifyChange: notify,
		clock:        clock,
		project:      project,
	}
}

// CreateCaptureSession persists a bounded, authorized session in the pending
// state and notifies assignment subscribers. It returns CodeInvalidArgument
// when the request omits its edge, source, authorization, or positive budget.
func (s *OperatorService) CreateCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.CreateCaptureSessionRequest],
) (*connect.Response[operatorcapturev1.CreateCaptureSessionResponse], error) {
	principal, ok := authn.FromContext(ctx)
	if !ok {
		return nil, unauthenticatedOperator(nil)
	}
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, unauthenticatedOperator(err)
	}

	msg := req.Msg

	if msg.GetEdge() == nil || msg.GetEdge().GetEdge().GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("edge is required"))
	}
	edgeID := msg.GetEdge().GetEdge().GetId()
	if s.edgeTenant == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errs.Msg("edge tenant resolver not configured"))
	}
	owner, err := s.edgeTenant(ctx, edgeID)
	if err != nil {
		if code, ok := errs.CodeOf(err); ok && code == edgestore.ErrCodeUnknownEdge {
			return nil, connect.NewError(connect.CodeNotFound, errs.Msg("edge not found"))
		}
		return nil, connectErr(errs.From(err).Code(ErrCodeStore).Attr("edge", edgeID).Msg("resolve edge tenant"))
	}
	if owner != tenantID {
		return nil, connect.NewError(connect.CodeNotFound, errs.Msg("edge not found"))
	}
	if msg.GetSource() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("capture source is required"))
	}
	if msg.GetAuthorization() == nil || msg.GetAuthorization().GetReason() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("authorization is required"))
	}
	if msg.GetAuthorization().GetFullPayloadRequested() {
		if err := authz.Require(ctx, "full_payload", "tenant", tenantID); err != nil {
			return nil, err
		}
		if s.fullPayload == nil {
			return nil, connect.NewError(connect.CodeUnavailable, errs.Msg("full payload grant resolver not configured"))
		}
		active, err := s.fullPayload(ctx, tenantID, identityv1.OperatorRef_builder{
			Issuer: new(principal.Issuer), Subject: new(principal.Subject),
		}.Build())
		if err != nil {
			return nil, connecterr.WrapAs(connect.CodeUnavailable, "full payload grant cannot be read", errs.From(err).Code(ErrCodeStore).Msg("read full payload grant"))
		}
		if !active {
			return nil, connecterr.WrapAs(connect.CodePermissionDenied, "full payload grant expired", errs.New().Code(ErrCodeFullPayloadExpired).Msg("full payload grant expired"))
		}
	}

	budget := msg.GetBudget()
	if budget == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("budget is required"))
	}
	hasBound := (budget.HasMaxPackets() && budget.GetMaxPackets() > 0) ||
		(budget.HasMaxBytes() && budget.GetMaxBytes() > 0) ||
		(budget.HasMaxDuration() && budget.GetMaxDuration().AsDuration() > 0)
	if !hasBound {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("capture budget must bound packets, bytes, or duration"))
	}

	sessionID := uuid.NewString()
	globalRef := modelcapturev1.CaptureSessionGlobalRef_builder{
		Edge: msg.GetEdge(),
		CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
			Id: proto.String(sessionID),
		}.Build(),
	}.Build()

	authConfig := modelcapturev1.CaptureAuthorization_builder{
		Reason:               proto.String(msg.GetAuthorization().GetReason()),
		FullPayloadRequested: proto.Bool(msg.GetAuthorization().GetFullPayloadRequested()),
		RequestedBy: identityv1.OperatorRef_builder{
			Issuer:  proto.String(principal.Issuer),
			Subject: proto.String(principal.Subject),
		}.Build(),
	}.Build()

	configBuilder := modelcapturev1.CaptureSessionConfig_builder{
		Ref:           globalRef,
		Source:        msg.GetSource(),
		Budget:        msg.GetBudget(),
		Authorization: authConfig,
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

	rec, err := s.store.CreateSession(ctx, tenantID, configBuilder.Build())
	if err != nil {
		return nil, connectErr(err)
	}

	s.notifyChange()
	s.project(ctx, "capture_session", sessionID)

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
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, unauthenticatedOperator(err)
	}

	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("session id is required"))
	}

	rec, err := s.store.MutateSession(ctx, tenantID, sessionID, func(r *modelcapturev1.CaptureSessionRecord) error {
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

// GetCaptureSession returns the requested session. It returns CodeInvalidArgument
// for an empty session identifier and CodeNotFound when the store holds no such
// session.
func (s *OperatorService) GetCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.GetCaptureSessionRequest],
) (*connect.Response[operatorcapturev1.GetCaptureSessionResponse], error) {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, unauthenticatedOperator(err)
	}

	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("session id is required"))
	}

	rec, _, err := s.store.Session(ctx, tenantID, sessionID)
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
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, authz.Abandon(ctx, unauthenticatedOperator(err))
	}

	all, err := s.store.ListSessions(ctx, tenantID)
	if err != nil {
		return nil, authz.Abandon(ctx, connectErr(err))
	}

	pageSize := defaultPageSize
	if req.Msg.HasPageSize() && req.Msg.GetPageSize() > 0 {
		pageSize = int(req.Msg.GetPageSize())
		if pageSize > maxPageSize {
			pageSize = maxPageSize
		}
	}

	pageToken := req.Msg.GetPageToken()
	var candidates []*modelcapturev1.CaptureSessionRecord
	for _, rec := range all {
		if rec.GetConfig().GetRef().GetCaptureSession().GetId() > pageToken {
			candidates = append(candidates, rec)
		}
	}

	const maxCandidates = 500
	examined := 0
	var (
		page           []*modelcapturev1.CaptureSessionRecord
		lastExaminedID string
		lastReturnedID string
		pageFilled     bool
	)

	for len(page) < pageSize && examined < maxCandidates && len(candidates) > 0 {
		chunkSize := pageSize
		remExamined := maxCandidates - examined
		if remExamined < chunkSize {
			chunkSize = remExamined
		}
		if len(candidates) < chunkSize {
			chunkSize = len(candidates)
		}

		chunk := candidates[:chunkSize]
		candidates = candidates[chunkSize:]
		examined += len(chunk)
		lastExaminedID = chunk[len(chunk)-1].GetConfig().GetRef().GetCaptureSession().GetId()

		distinctEdgesMap := make(map[string]struct{})
		var distinctEdges []string
		for _, rec := range chunk {
			edgeID := rec.GetConfig().GetRef().GetEdge().GetEdge().GetId()
			if edgeID != "" {
				if _, exists := distinctEdgesMap[edgeID]; !exists {
					distinctEdgesMap[edgeID] = struct{}{}
					distinctEdges = append(distinctEdges, edgeID)
				}
			}
		}

		allowedEdges, err := authz.Filter(ctx, "capture", "edge", distinctEdges)
		if err != nil {
			return nil, err
		}
		allowedEdgeSet := make(map[string]bool, len(allowedEdges))
		for _, edgeID := range allowedEdges {
			allowedEdgeSet[edgeID] = true
		}

		for _, rec := range chunk {
			edgeID := rec.GetConfig().GetRef().GetEdge().GetEdge().GetId()
			if !allowedEdgeSet[edgeID] {
				continue
			}
			page = append(page, rec)
			lastReturnedID = rec.GetConfig().GetRef().GetCaptureSession().GetId()
			if len(page) == pageSize {
				pageFilled = true
				break
			}
		}
	}
	if examined == 0 {
		if _, err := authz.Filter(ctx, "capture", "edge", nil); err != nil {
			return nil, err
		}
	}

	var tokenID string
	if pageFilled {
		tokenID = lastReturnedID
	} else {
		tokenID = lastExaminedID
	}

	var hasRemaining bool
	if tokenID != "" {
		for _, rec := range all {
			if rec.GetConfig().GetRef().GetCaptureSession().GetId() > tokenID {
				hasRemaining = true
				break
			}
		}
	}

	respBuilder := operatorcapturev1.ListCaptureSessionsResponse_builder{
		Sessions: page,
	}
	if hasRemaining && tokenID != "" {
		respBuilder.NextPageToken = proto.String(tokenID)
	}

	return connect.NewResponse(respBuilder.Build()), nil
}

// DeleteCaptureSession purges a session record and unlinks its artifact file.
func (s *OperatorService) DeleteCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.DeleteCaptureSessionRequest],
) (*connect.Response[operatorcapturev1.DeleteCaptureSessionResponse], error) {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, unauthenticatedOperator(err)
	}

	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.Msg("session id is required"))
	}

	rec, _, err := s.store.Session(ctx, tenantID, sessionID)
	if err != nil {
		return nil, connectErr(err)
	}
	if rec == nil {
		return nil, errNoSuchSession()
	}

	if err := s.store.DeleteSession(ctx, tenantID, sessionID); err != nil {
		return nil, connectErr(err)
	}

	s.broadcaster.CloseSession(tenantID, sessionID)
	s.notifyChange()
	s.project(ctx, "capture_session", sessionID)

	return connect.NewResponse(operatorcapturev1.DeleteCaptureSessionResponse_builder{}.Build()), nil
}

// TailCaptureSession streams live packet chunks from active uploads.
func (s *OperatorService) TailCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.TailCaptureSessionRequest],
	stream *connect.ServerStream[operatorcapturev1.TailCaptureSessionResponse],
) error {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return unauthenticatedOperator(err)
	}

	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errs.Msg("session id is required"))
	}

	rec, _, err := s.store.Session(ctx, tenantID, sessionID)
	if err != nil {
		return connectErr(err)
	}
	if rec == nil {
		return errNoSuchSession()
	}

	sub, unsub := s.broadcaster.Subscribe(tenantID, sessionID)
	defer unsub()

	// Subscribe first, then read the session again. Only the upload relay
	// closes a tail's channel, and it does so once, when the capture ends.
	// A tail that opened after that moment would wait on a channel nobody
	// will ever send to or close; re-reading under the subscription is what
	// catches the session that finished in between.
	rec, _, err = s.store.Session(ctx, tenantID, sessionID)
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
		case item, ok := <-sub.Items():
			if !ok {
				// The session ended. A gap the channel had no room to carry —
				// a dropped final chunk among them — waits on the subscription
				// so a slow consumer reads it here instead of a clean EOF over
				// lost packets.
				return sendTailFrame(stream, tailGapFrame(sub.TerminalGap()))
			}
			if err := sendTailFrame(stream, tailItemFrame(item)); err != nil {
				return err
			}
			if item.Chunk.GetFinal() {
				return nil
			}
		}
	}
}

// tailItemFrame renders a broadcast item as the wire frame for it: a chunk, or
// a gap standing for the chunks a slow consumer missed before it.
func tailItemFrame(item TailItem) *operatorcapturev1.TailCaptureSessionResponse {
	if item.Chunk != nil {
		return operatorcapturev1.TailCaptureSessionResponse_builder{
			Chunk: item.Chunk,
		}.Build()
	}
	return tailGapFrame(item.Gap)
}

// tailGapFrame renders a gap as a response frame, or nil when there is no gap,
// which callers treat as nothing to send.
func tailGapFrame(gap *TailGapInfo) *operatorcapturev1.TailCaptureSessionResponse {
	if gap == nil {
		return nil
	}
	msg := operatorcapturev1.TailGap_builder{
		DroppedChunks:  proto.Uint64(gap.DroppedChunks),
		DroppedPackets: proto.Uint64(gap.DroppedPackets),
	}
	// The sequence bounds mean nothing when no packets were lost, only stream
	// markers, so they are left unset to say so.
	if gap.DroppedPackets > 0 {
		msg.FirstDroppedSequence = proto.Uint64(gap.FirstDroppedSequence)
		msg.LastDroppedSequence = proto.Uint64(gap.LastDroppedSequence)
	}
	return operatorcapturev1.TailCaptureSessionResponse_builder{
		Gap: msg.Build(),
	}.Build()
}

// sendTailFrame sends frame unless it is nil, which stands for nothing to send.
func sendTailFrame(stream *connect.ServerStream[operatorcapturev1.TailCaptureSessionResponse], frame *operatorcapturev1.TailCaptureSessionResponse) error {
	if frame == nil {
		return nil
	}
	return stream.Send(frame)
}

// DownloadCaptureSession streams pcapng artifact chunks <=1MB from disk.
func (s *OperatorService) DownloadCaptureSession(
	ctx context.Context,
	req *connect.Request[operatorcapturev1.DownloadCaptureSessionRequest],
	stream *connect.ServerStream[operatorcapturev1.DownloadCaptureSessionResponse],
) error {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return unauthenticatedOperator(err)
	}

	sessionID := req.Msg.GetSession().GetCaptureSession().GetId()
	if sessionID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errs.Msg("session id is required"))
	}

	rec, _, err := s.store.Session(ctx, tenantID, sessionID)
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
		return connect.NewError(connect.CodeFailedPrecondition, errs.Msg("this capture has not finished; there is nothing to download yet"))
	case artifact.HasPurgedAt() || !s.clock().Before(artifact.GetExpiresAt().AsTime()):
		return connect.NewError(connect.CodeNotFound, errs.Msg("this session's capture is no longer retained"))
	}

	err = s.store.ReadArtifact(ctx, tenantID, sessionID, func(chunk *modelcapturev1.CaptureArtifactChunk) error {
		resp := operatorcapturev1.DownloadCaptureSessionResponse_builder{
			Chunk: chunk,
		}.Build()
		return stream.Send(resp)
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return connect.NewError(connect.CodeNotFound, errs.Msg("this session's capture is no longer stored"))
		}
		return connectErr(err)
	}
	return nil
}

func errNoSuchSession() error {
	return connect.NewError(connect.CodeNotFound, errs.Msg("no such capture session"))
}
