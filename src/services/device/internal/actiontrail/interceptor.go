package actiontrail

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	capturev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	edgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	identityapiv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	identityapiv1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	operatorv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/operator/v1"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	modeledgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

// edgeAdminProcedureActions maps EdgeAdminService procedures to their recorded operator actions.
var edgeAdminProcedureActions = map[string]operatorv1.OperatorAction{
	edgev1connect.EdgeAdminServiceCreateEdgeProcedure:     operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_CREATE,
	edgev1connect.EdgeAdminServiceIssueSetupKeyProcedure:  operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_ISSUE,
	edgev1connect.EdgeAdminServiceRevokeSetupKeyProcedure: operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_REVOKE,
	edgev1connect.EdgeAdminServiceRetireEdgeProcedure:     operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_RETIRE,
	edgev1connect.EdgeAdminServiceGetEdgeProcedure:        operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_GET,
	edgev1connect.EdgeAdminServiceListEdgesProcedure:      operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_LIST,
}

// platformToken stands in for the tenant in the subject of a record whose call
// was admitted to no tenant. tenant.Validate refuses it as a tenant id.
const platformToken = "platform"

// readActions are the recorded views. Their records go to the read stream so
// that a flood of views cannot evict a record of a change.
var readActions = map[operatorv1.OperatorAction]bool{
	operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_GET:  true,
	operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_LIST: true,
}

// objectSetter names the object of an event.
type objectSetter func(*operatorv1.OperatorActionEvent)

// identityTrail is the trail entry of one identity procedure. A nil attempt
// names no object before the handler runs. A completion repeats the attempt's
// object unless complete names one from the response.
type identityTrail struct {
	action   operatorv1.OperatorAction
	attempt  func(req any) objectSetter
	complete func(req, resp any) objectSetter
}

// identityProcedures maps the recorded procedures of TenantService and
// TenantAdminService to their trail entries. The two services' reads are not
// recorded: a list of members discloses nothing a setup key or a payload does.
var identityProcedures = map[string]identityTrail{
	identityapiv1connect.TenantServiceCreateTenantProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_TENANT_CREATE,
		complete: func(_, resp any) objectSetter {
			r, _ := resp.(*identityapiv1.CreateTenantResponse)
			return tenantObject(r.GetTenant().GetConfig().GetRef())
		},
	},
	identityapiv1connect.TenantAdminServiceEnrollMemberProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_MEMBER_ENROLL,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.EnrollMemberRequest)
			return memberObject(r.GetMember())
		},
	},
	identityapiv1connect.TenantAdminServiceRemoveMemberProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_MEMBER_REMOVE,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.RemoveMemberRequest)
			return memberObject(r.GetMember())
		},
	},
	identityapiv1connect.TenantAdminServiceCreateRoleProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE,
		complete: func(_, resp any) objectSetter {
			r, _ := resp.(*identityapiv1.CreateRoleResponse)
			return roleObject(r.GetRole().GetRef(), r.GetRole().GetRelations())
		},
	},
	identityapiv1connect.TenantAdminServiceDeleteRoleProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_DELETE,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.DeleteRoleRequest)
			return roleObject(r.GetRole(), nil)
		},
	},
	identityapiv1connect.TenantAdminServiceAssignRoleProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_ASSIGN,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.AssignRoleRequest)
			return roleAssignmentObject(r.GetRole(), r.GetMember())
		},
	},
	identityapiv1connect.TenantAdminServiceUnassignRoleProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_UNASSIGN,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.UnassignRoleRequest)
			return roleAssignmentObject(r.GetRole(), r.GetMember())
		},
	},
	identityapiv1connect.TenantAdminServiceConnectPartnerProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_PARTNER_CONNECT,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.ConnectPartnerRequest)
			return partnerObject(r.GetPartner(), r.GetRelations())
		},
	},
	identityapiv1connect.TenantAdminServiceDisconnectPartnerProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_PARTNER_DISCONNECT,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.DisconnectPartnerRequest)
			return tenantObject(r.GetPartner())
		},
	},
	identityapiv1connect.TenantAdminServiceGrantFullPayloadProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_FULL_PAYLOAD_GRANT,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.GrantFullPayloadRequest)
			return memberObject(r.GetMember())
		},
		// The expiry is central's clock, known only once the handler stored it.
		complete: func(req, resp any) objectSetter {
			r, _ := req.(*identityapiv1.GrantFullPayloadRequest)
			g, _ := resp.(*identityapiv1.GrantFullPayloadResponse)
			return fullPayloadGrantObject(r.GetMember(), g.GetMember().GetFullPayload().GetExpiresAt())
		},
	},
	identityapiv1connect.TenantAdminServiceRevokeFullPayloadProcedure: {
		action: operatorv1.OperatorAction_OPERATOR_ACTION_FULL_PAYLOAD_REVOKE,
		attempt: func(req any) objectSetter {
			r, _ := req.(*identityapiv1.RevokeFullPayloadRequest)
			return memberObject(r.GetMember())
		},
	},
}

func edgeObject(ref *modeledgev1.EdgeGlobalRef) objectSetter {
	if ref == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) { e.SetEdge(ref) }
}

func captureSessionObject(ref *modelcapturev1.CaptureSessionGlobalRef) objectSetter {
	if ref == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) { e.SetCaptureSession(ref) }
}

func tenantObject(ref *identityv1.TenantGlobalRef) objectSetter {
	if ref == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) { e.SetTenant(ref) }
}

func memberObject(member *identityv1.OperatorRef) objectSetter {
	if member == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) { e.SetMember(member) }
}

func roleObject(ref *identityv1.RoleGlobalRef, relations []identityv1.TenantRelation) objectSetter {
	if ref == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) {
		role := &operatorv1.OperatorActionRole{}
		role.SetRole(ref)
		role.SetRelations(relations)
		e.SetRole(role)
	}
}

func roleAssignmentObject(role *identityv1.RoleGlobalRef, member *identityv1.OperatorRef) objectSetter {
	if role == nil || member == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) {
		assignment := &operatorv1.OperatorActionRoleAssignment{}
		assignment.SetRole(role)
		assignment.SetMember(member)
		e.SetRoleAssignment(assignment)
	}
}

func partnerObject(provider *identityv1.TenantGlobalRef, relations []identityv1.TenantRelation) objectSetter {
	if provider == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) {
		partner := &operatorv1.OperatorActionPartner{}
		partner.SetTenant(provider)
		partner.SetRelations(relations)
		e.SetPartner(partner)
	}
}

func fullPayloadGrantObject(member *identityv1.OperatorRef, expiresAt *timestamppb.Timestamp) objectSetter {
	if member == nil || expiresAt == nil {
		return nil
	}
	return func(e *operatorv1.OperatorActionEvent) {
		grant := &operatorv1.OperatorActionFullPayloadGrant{}
		grant.SetMember(member)
		grant.SetExpiresAt(expiresAt)
		e.SetFullPayloadGrant(grant)
	}
}

// subjectOf is where the record of an action by one tenant's operator goes.
func subjectOf(action operatorv1.OperatorAction, tenantID string) string {
	if readActions[action] {
		return edgebus.OperatorReadSubject(tenantID, actionToken(action))
	}
	return edgebus.OperatorActionSubject(tenantID, actionToken(action))
}

var streamingCaptureProcedureActions = map[string]operatorv1.OperatorAction{
	capturev1connect.CaptureServiceTailCaptureSessionProcedure:     operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_TAIL,
	capturev1connect.CaptureServiceDownloadCaptureSessionProcedure: operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_DOWNLOAD,
}

// Interceptor records operator action events for admitted Connect RPC procedures.
// An Interceptor is safe for concurrent use.
type Interceptor struct {
	pub   Publisher
	clock func() time.Time
	log   *slog.Logger
}

// NewInterceptor constructs an action trail interceptor.
func NewInterceptor(pub Publisher, clock func() time.Time, log *slog.Logger) *Interceptor {
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Interceptor{
		pub:   pub,
		clock: clock,
		log:   log,
	}
}

func actionToken(action operatorv1.OperatorAction) string {
	name := action.String()
	name = strings.TrimPrefix(name, "OPERATOR_ACTION_")
	return strings.ToLower(name)
}

func truncateErrorType(s string) string {
	n := 0
	for i := range s {
		if n == 128 {
			return s[:i]
		}
		n++
	}
	return s
}

func outcomeOf(handlerErr error) (operatorv1.OperatorActionOutcome, string) {
	switch {
	case handlerErr == nil:
		return operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED, ""
	case connect.CodeOf(handlerErr) == connect.CodePermissionDenied:
		return operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_DENIED, truncateErrorType(telemetry.ErrorType(handlerErr))
	default:
		return operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED, truncateErrorType(telemetry.ErrorType(handlerErr))
	}
}

func abandon(ctx context.Context, err error) error {
	aErr := authz.Abandon(ctx, err)
	if code, ok := errs.CodeOf(aErr); ok && code == authz.ErrCodeObligationViolation {
		return err
	}
	return aErr
}

// WrapUnary records operator actions for configured unary procedures.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req == nil {
			return next(ctx, req)
		}

		proc := req.Spec().Procedure
		var (
			action        operatorv1.OperatorAction
			attemptObject objectSetter
			record        bool
		)

		if proc == capturev1connect.CaptureServiceCreateCaptureSessionProcedure {
			createReq, ok := req.Any().(*capturev1.CreateCaptureSessionRequest)
			if ok && createReq != nil && createReq.GetAuthorization().GetFullPayloadRequested() {
				record = true
				action = operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_FULL_PAYLOAD_CREATE
				attemptObject = edgeObject(createReq.GetEdge())
			}
		} else if act, ok := edgeAdminProcedureActions[proc]; ok {
			record = true
			action = act
			switch proc {
			case edgev1connect.EdgeAdminServiceCreateEdgeProcedure, edgev1connect.EdgeAdminServiceListEdgesProcedure:
				// no attempt object
			case edgev1connect.EdgeAdminServiceIssueSetupKeyProcedure:
				if r, ok := req.Any().(*edgev1.IssueSetupKeyRequest); ok && r != nil {
					attemptObject = edgeObject(r.GetEdge())
				}
			case edgev1connect.EdgeAdminServiceRevokeSetupKeyProcedure:
				if r, ok := req.Any().(*edgev1.RevokeSetupKeyRequest); ok && r != nil {
					attemptObject = edgeObject(r.GetEdge())
				}
			case edgev1connect.EdgeAdminServiceRetireEdgeProcedure:
				if r, ok := req.Any().(*edgev1.RetireEdgeRequest); ok && r != nil {
					attemptObject = edgeObject(r.GetEdge())
				}
			case edgev1connect.EdgeAdminServiceGetEdgeProcedure:
				if r, ok := req.Any().(*edgev1.GetEdgeRequest); ok && r != nil {
					attemptObject = edgeObject(r.GetEdge())
				}
			}
		} else if trail, ok := identityProcedures[proc]; ok {
			record = true
			action = trail.action
			if trail.attempt != nil {
				attemptObject = trail.attempt(req.Any())
			}
		}

		if !record {
			return next(ctx, req)
		}

		principal, ok := authn.FromContext(ctx)
		tenantID, tenantErr := tenant.FromContext(ctx)
		if tenantErr != nil && proc == identityapiv1connect.TenantServiceCreateTenantProcedure {
			tenantID, tenantErr = platformToken, nil
		} else if tenantErr == nil {
			tenantErr = tenant.Validate(tenantID)
		}
		if !ok || principal.ID == "" || tenantErr != nil {
			return nil, abandon(ctx, connecterr.WrapAs(
				connect.CodeInternal,
				"action trail unprepared",
				errs.New().Code(ErrCodeUnprepared).Msg("principal or tenant missing from context"),
			))
		}

		opRef := &identityv1.OperatorRef{}
		opRef.SetIssuer(principal.Issuer)
		opRef.SetSubject(principal.Subject)

		callID := uuid.NewString()
		attemptEventID := uuid.NewString()

		attemptEvent := &operatorv1.OperatorActionEvent{}
		attemptEvent.SetEventId(attemptEventID)
		attemptEvent.SetCallId(callID)
		attemptEvent.SetOccurredAt(timestamppb.New(i.clock()))
		attemptEvent.SetOperator(opRef)
		attemptEvent.SetAction(action)
		if attemptObject != nil {
			attemptObject(attemptEvent)
		}
		attemptEvent.SetAttempted(&operatorv1.OperatorActionAttempted{})

		actToken := actionToken(action)
		subject := subjectOf(action, tenantID)

		attemptData, err := proto.Marshal(attemptEvent)
		if err != nil {
			return nil, abandon(ctx, connecterr.WrapAs(
				connect.CodeUnavailable,
				"action trail unavailable",
				errs.From(err).Code(ErrCodeUnavailable).Msg("marshal attempt event"),
			))
		}

		if err := i.pub.Publish(ctx, subject, attemptData, attemptEventID); err != nil {
			return nil, abandon(ctx, connecterr.WrapAs(
				connect.CodeUnavailable,
				"action trail unavailable",
				errs.New().Code(ErrCodeUnavailable).Cause(err).Msg("failed to publish action attempt"),
			))
		}

		resp, handlerErr := next(ctx, req)

		detachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		completedDetail := &operatorv1.OperatorActionCompleted{}
		outcome, errType := outcomeOf(handlerErr)
		completedDetail.SetOutcome(outcome)
		if errType != "" {
			completedDetail.SetErrorType(errType)
		}

		completionEventID := uuid.NewString()
		completionEvent := &operatorv1.OperatorActionEvent{}
		completionEvent.SetEventId(completionEventID)
		completionEvent.SetCallId(callID)
		completionEvent.SetOccurredAt(timestamppb.New(i.clock()))
		completionEvent.SetOperator(opRef)
		completionEvent.SetAction(action)

		var completionObject objectSetter
		switch proc {
		case edgev1connect.EdgeAdminServiceCreateEdgeProcedure:
			if resp != nil {
				if r, ok := resp.Any().(*edgev1.CreateEdgeResponse); ok && r != nil {
					completionObject = edgeObject(r.GetEdge().GetConfig().GetRef())
				}
			}
		case capturev1connect.CaptureServiceCreateCaptureSessionProcedure:
			if resp != nil {
				if r, ok := resp.Any().(*capturev1.CreateCaptureSessionResponse); ok && r != nil {
					completionObject = captureSessionObject(r.GetSession().GetConfig().GetRef())
				}
			}
			if completionObject == nil {
				completionObject = attemptObject
			}
		default:
			if trail, ok := identityProcedures[proc]; ok && trail.complete != nil && resp != nil {
				completionObject = trail.complete(req.Any(), resp.Any())
			}
			if completionObject == nil {
				completionObject = attemptObject
			}
		}
		if completionObject != nil {
			completionObject(completionEvent)
		}
		completionEvent.SetCompleted(completedDetail)

		if compData, compErr := proto.Marshal(completionEvent); compErr == nil {
			if pubErr := i.pub.Publish(detachCtx, subject, compData, completionEventID); pubErr != nil {
				i.log.ErrorContext(detachCtx, "failed to publish operator action completion",
					slog.String("error.type", telemetry.ErrorType(pubErr)),
					slog.String("flowseer.tenant.id", tenantID),
					slog.String("flowseer.operator.action", actToken),
					slog.String("flowseer.call.id", callID),
				)
			}
		} else {
			i.log.ErrorContext(detachCtx, "failed to marshal operator action completion",
				slog.String("error.type", telemetry.ErrorType(compErr)),
				slog.String("flowseer.tenant.id", tenantID),
				slog.String("flowseer.operator.action", actToken),
				slog.String("flowseer.call.id", callID),
			)
		}

		return resp, handlerErr
	}
}

// WrapStreamingHandler records operator actions for streaming procedures.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		proc := conn.Spec().Procedure
		action, ok := streamingCaptureProcedureActions[proc]
		if !ok {
			return next(ctx, conn)
		}

		principal, authOk := authn.FromContext(ctx)
		tenantID, tenantErr := tenant.FromContext(ctx)
		if !authOk || principal.ID == "" || tenantErr != nil || tenantID == "" || tenant.Validate(tenantID) != nil {
			return connecterr.WrapAs(
				connect.CodeInternal,
				"action trail unprepared",
				errs.New().Code(ErrCodeUnprepared).Msg("principal or tenant missing from context"),
			)
		}

		opRef := &identityv1.OperatorRef{}
		opRef.SetIssuer(principal.Issuer)
		opRef.SetSubject(principal.Subject)

		callID := uuid.NewString()
		actToken := actionToken(action)
		subject := edgebus.OperatorActionSubject(tenantID, actToken)

		wrapped := &actionTrailStreamingConn{
			StreamingHandlerConn: conn,
			interceptor:          i,
			ctx:                  ctx,
			tenantID:             tenantID,
			action:               action,
			actToken:             actToken,
			subject:              subject,
			opRef:                opRef,
			callID:               callID,
		}

		handlerErr := next(ctx, wrapped)

		wrapped.mu.Lock()
		attemptPublished := wrapped.attemptPublished
		sessionRef := wrapped.sessionRef
		wrapped.mu.Unlock()

		if attemptPublished {
			detachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()

			completedDetail := &operatorv1.OperatorActionCompleted{}
			outcome, errType := outcomeOf(handlerErr)
			completedDetail.SetOutcome(outcome)
			if errType != "" {
				completedDetail.SetErrorType(errType)
			}

			completionEventID := uuid.NewString()
			completionEvent := &operatorv1.OperatorActionEvent{}
			completionEvent.SetEventId(completionEventID)
			completionEvent.SetCallId(callID)
			completionEvent.SetOccurredAt(timestamppb.New(i.clock()))
			completionEvent.SetOperator(opRef)
			completionEvent.SetAction(action)
			if sessionRef != nil {
				completionEvent.SetCaptureSession(sessionRef)
			}
			completionEvent.SetCompleted(completedDetail)

			if compData, compErr := proto.Marshal(completionEvent); compErr == nil {
				if pubErr := i.pub.Publish(detachCtx, subject, compData, completionEventID); pubErr != nil {
					i.log.ErrorContext(detachCtx, "failed to publish operator action completion",
						slog.String("error.type", telemetry.ErrorType(pubErr)),
						slog.String("flowseer.tenant.id", tenantID),
						slog.String("flowseer.operator.action", actToken),
						slog.String("flowseer.call.id", callID),
					)
				}
			} else {
				i.log.ErrorContext(detachCtx, "failed to marshal operator action completion",
					slog.String("error.type", telemetry.ErrorType(compErr)),
					slog.String("flowseer.tenant.id", tenantID),
					slog.String("flowseer.operator.action", actToken),
					slog.String("flowseer.call.id", callID),
				)
			}
		}

		return handlerErr
	}
}

// WrapStreamingClient passes through streaming client calls.
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

type actionTrailStreamingConn struct {
	connect.StreamingHandlerConn
	interceptor *Interceptor
	ctx         context.Context
	tenantID    string
	action      operatorv1.OperatorAction
	actToken    string
	subject     string
	opRef       *identityv1.OperatorRef
	callID      string

	mu               sync.Mutex
	attemptPublished bool
	sessionRef       *modelcapturev1.CaptureSessionGlobalRef
}

func (c *actionTrailStreamingConn) Receive(msg any) error {
	if err := c.StreamingHandlerConn.Receive(msg); err != nil {
		return err
	}

	c.mu.Lock()
	if c.attemptPublished {
		c.mu.Unlock()
		return nil
	}

	switch req := msg.(type) {
	case *capturev1.TailCaptureSessionRequest:
		c.sessionRef = req.GetSession()
	case *capturev1.DownloadCaptureSessionRequest:
		c.sessionRef = req.GetSession()
	}
	sessionRef := c.sessionRef
	c.mu.Unlock()

	attemptEventID := uuid.NewString()
	attemptEvent := &operatorv1.OperatorActionEvent{}
	attemptEvent.SetEventId(attemptEventID)
	attemptEvent.SetCallId(c.callID)
	attemptEvent.SetOccurredAt(timestamppb.New(c.interceptor.clock()))
	attemptEvent.SetOperator(c.opRef)
	attemptEvent.SetAction(c.action)
	if sessionRef != nil {
		attemptEvent.SetCaptureSession(sessionRef)
	}
	attemptEvent.SetAttempted(&operatorv1.OperatorActionAttempted{})

	data, err := proto.Marshal(attemptEvent)
	if err != nil {
		return connecterr.WrapAs(
			connect.CodeUnavailable,
			"action trail unavailable",
			errs.From(err).Code(ErrCodeUnavailable).Msg("marshal attempt event"),
		)
	}

	if err := c.interceptor.pub.Publish(c.ctx, c.subject, data, attemptEventID); err != nil {
		return connecterr.WrapAs(
			connect.CodeUnavailable,
			"action trail unavailable",
			errs.New().Code(ErrCodeUnavailable).Cause(err).Msg("failed to publish action attempt"),
		)
	}

	c.mu.Lock()
	c.attemptPublished = true
	c.mu.Unlock()

	return nil
}
