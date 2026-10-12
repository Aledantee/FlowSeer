package rawwindowapi

import (
	"context"

	connect "connectrpc.com/connect"

	apiingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/ingest/v1/ingestv1connect"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

const msgOperatorUnauthenticated = "the call is not authenticated"

// OperatorServiceConfig configures an [OperatorService]. EdgeTenant, FullPayload,
// Registry, and Allowance are required. A nil NotifyChange is a no-op.
//
// FullPayload reads the authenticated operator's member grant. False means
// expired or absent and refuses the open with CodePermissionDenied, and an
// error refuses it with CodeUnavailable. Allowance reports the deployment's
// per-edge byte allowance, and false when none is configured, which refuses
// every open.
type OperatorServiceConfig struct {
	EdgeTenant   func(ctx context.Context, edgeID string) (string, error)
	FullPayload  func(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (bool, error)
	Registry     Registry
	Allowance    func() (uint64, bool)
	NotifyChange func()
}

// OperatorService serves the operator's raw window requests. It is safe for
// concurrent use when every configured callback is.
type OperatorService struct {
	store        *Store
	edgeTenant   func(ctx context.Context, edgeID string) (string, error)
	fullPayload  func(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (bool, error)
	registry     Registry
	allowance    func() (uint64, bool)
	notifyChange func()
}

var _ ingestv1connect.RawWindowServiceHandler = (*OperatorService)(nil)

// NewOperatorService constructs an OperatorService over store. The store must
// be non-nil, and cfg's required fields must be set.
func NewOperatorService(store *Store, cfg OperatorServiceConfig) *OperatorService {
	notify := cfg.NotifyChange
	if notify == nil {
		notify = func() {}
	}
	return &OperatorService{
		store:        store,
		edgeTenant:   cfg.EdgeTenant,
		fullPayload:  cfg.FullPayload,
		registry:     cfg.Registry,
		allowance:    cfg.Allowance,
		notifyChange: notify,
	}
}

// OpenRawWindow admits a pending window on one of the caller's tenant's edges.
// The operator needs capture access on the edge, an active full-payload grant
// on the tenant that the service reads fresh from the member record, and an
// edge that has subscribed. The service stamps requested_by from the
// authenticated principal over whatever the request carried, and notifies the
// relay once the window is stored.
func (s *OperatorService) OpenRawWindow(
	ctx context.Context,
	req *connect.Request[apiingestv1.OpenRawWindowRequest],
) (*connect.Response[apiingestv1.OpenRawWindowResponse], error) {
	principal, ok := authn.FromContext(ctx)
	if !ok {
		return nil, unauthenticatedOperator(nil)
	}
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, unauthenticatedOperator(err)
	}

	msg := req.Msg
	edgeID := msg.GetEdge().GetEdge().GetId()
	if err := s.requireEdgeOf(ctx, tenantID, edgeID); err != nil {
		return nil, err
	}

	if err := authz.Require(ctx, "full_payload", "tenant", tenantID); err != nil {
		return nil, err
	}
	operator := identityv1.OperatorRef_builder{Issuer: new(principal.Issuer), Subject: new(principal.Subject)}.Build()
	if s.fullPayload == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errs.Msg("full payload grant resolver not configured"))
	}
	active, err := s.fullPayload(ctx, tenantID, operator)
	if err != nil {
		return nil, connecterr.WrapAs(connect.CodeUnavailable, "full payload grant cannot be read",
			errs.From(err).Code(ErrCodeStore).Msg("read full payload grant"))
	}
	if !active {
		return nil, connecterr.WrapAs(connect.CodePermissionDenied, "full payload grant expired",
			errs.New().Code(ErrCodeFullPayloadExpired).Msg("full payload grant expired"))
	}

	allowance, _ := s.allowance()
	window, err := s.store.Open(ctx, tenantID, OpenRequest{
		EdgeID:    edgeID,
		Selector:  msg.GetSelector(),
		ExpiresAt: msg.GetExpiresAt().AsTime(),
		MaxBytes:  msg.GetMaxBytes(),
		Authorization: rawwindowv1.RawWindowAuthorization_builder{
			RequestedBy: operator,
			Reason:      new(msg.GetAuthorization().GetReason()),
		}.Build(),
		AllowanceBytes:  allowance,
		ResolveSelector: s.selectorResolver(edgeID, msg.GetSelector()),
	})
	if err != nil {
		return nil, connectErr(err)
	}

	s.notifyChange()
	return connect.NewResponse(apiingestv1.OpenRawWindowResponse_builder{Window: window}.Build()), nil
}

// GetRawWindow returns one window with its pending, active, or terminal state.
// The edge comes from the stored window, never from the request, and the caller
// needs capture access on it.
func (s *OperatorService) GetRawWindow(
	ctx context.Context,
	req *connect.Request[apiingestv1.GetRawWindowRequest],
) (*connect.Response[apiingestv1.GetRawWindowResponse], error) {
	window, err := s.load(ctx, req.Msg.GetRawWindow().GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(apiingestv1.GetRawWindowResponse_builder{Window: window}.Build()), nil
}

// CloseRawWindow records the operator's wish to stop a window and notifies the
// relay. It returns the window as it stands: pending or active until the edge
// reports the end. Closing a window that has ended succeeds and changes
// nothing. The edge comes from the stored window, and the caller needs capture
// access on it.
func (s *OperatorService) CloseRawWindow(
	ctx context.Context,
	req *connect.Request[apiingestv1.CloseRawWindowRequest],
) (*connect.Response[apiingestv1.CloseRawWindowResponse], error) {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, authz.Abandon(ctx, unauthenticatedOperator(err))
	}
	loaded, err := s.load(ctx, req.Msg.GetRawWindow().GetId())
	if err != nil {
		return nil, err
	}

	window, err := s.store.Close(ctx, tenantID, loaded.GetConfig().GetRef().GetEdge().GetEdge().GetId(), req.Msg.GetRawWindow().GetId())
	if err != nil {
		return nil, connectErr(err)
	}

	s.notifyChange()
	return connect.NewResponse(apiingestv1.CloseRawWindowResponse_builder{Window: window}.Build()), nil
}

// load reads a window of the admitted tenant and requires capture access on
// its stored edge. Every refusal before that check abandons the authorization
// obligation.
func (s *OperatorService) load(ctx context.Context, windowID string) (*rawwindowv1.RawWindowRecord, error) {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, authz.Abandon(ctx, unauthenticatedOperator(err))
	}

	window, err := s.store.Window(ctx, tenantID, windowID)
	if err != nil {
		return nil, authz.Abandon(ctx, connectErr(err))
	}
	if err := authz.Require(ctx, "capture", "edge", window.GetConfig().GetRef().GetEdge().GetEdge().GetId()); err != nil {
		return nil, err
	}
	return window, nil
}

// requireEdgeOf refuses an edge the tenant does not own with CodeNotFound, the
// same answer an unknown edge gets.
func (s *OperatorService) requireEdgeOf(ctx context.Context, tenantID, edgeID string) error {
	if s.edgeTenant == nil {
		return connect.NewError(connect.CodeUnavailable, errs.Msg("edge tenant resolver not configured"))
	}
	owner, err := s.edgeTenant(ctx, edgeID)
	if err != nil {
		if code, ok := errs.CodeOf(err); ok && code == edgestore.ErrCodeUnknownEdge {
			return connect.NewError(connect.CodeNotFound, errs.Msg("edge not found"))
		}
		return connectErr(errs.From(err).Code(ErrCodeStore).Attr("edge", edgeID).Msg("resolve edge tenant"))
	}
	if owner != tenantID {
		return connect.NewError(connect.CodeNotFound, errs.Msg("edge not found"))
	}
	return nil
}

// selectorResolver places a device or integration selector on the edge through
// the registry. A source selector needs no registry, because the store checks
// it against what the subscribed process advertised.
func (s *OperatorService) selectorResolver(edgeID string, selector *rawwindowv1.RawWindowSelector) func(context.Context) error {
	return func(ctx context.Context) error {
		switch selector.WhichScope() {
		case rawwindowv1.RawWindowSelector_Device_case:
			deviceID := selector.GetDevice().GetDevice().GetId()
			hosted, err := s.registry.Hosts(ctx, edgeID, deviceID)
			if err != nil {
				return errs.From(err).Code(ErrCodeStore).Attr("edge", edgeID).Msg("resolve device")
			}
			if !hosted {
				return errs.New().Code(ErrCodeUnknownSelector).Attr("edge", edgeID).Attr("device", deviceID).
					Msg("edge does not host the device")
			}
		case rawwindowv1.RawWindowSelector_Integration_case:
			integrationID := selector.GetIntegration().GetIntegration().GetId()
			if s.registry.EdgeID() != edgeID || s.registry.IntegrationID() != integrationID {
				return errs.New().Code(ErrCodeUnknownSelector).Attr("edge", edgeID).Attr("integration", integrationID).
					Msg("integration is not the edge's")
			}
		case rawwindowv1.RawWindowSelector_Source_case, rawwindowv1.RawWindowSelector_Scope_not_set_case:
		}
		return nil
	}
}

func unauthenticatedOperator(err error) error {
	if err == nil {
		err = errs.Msg(msgOperatorUnauthenticated)
	}
	return connecterr.WrapRefused(msgOperatorUnauthenticated, err)
}
