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
	operatorv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/operator/v1"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	modeledgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

// EdgeAdminProcedureActions maps EdgeAdminService procedures to their recorded operator actions.
var EdgeAdminProcedureActions = map[string]operatorv1.OperatorAction{
	edgev1connect.EdgeAdminServiceCreateEdgeProcedure:     operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_CREATE,
	edgev1connect.EdgeAdminServiceIssueSetupKeyProcedure:  operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_ISSUE,
	edgev1connect.EdgeAdminServiceRevokeSetupKeyProcedure: operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_REVOKE,
	edgev1connect.EdgeAdminServiceRetireEdgeProcedure:     operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_RETIRE,
	edgev1connect.EdgeAdminServiceGetEdgeProcedure:        operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_GET,
	edgev1connect.EdgeAdminServiceListEdgesProcedure:      operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_LIST,
}

var streamingCaptureProcedureActions = map[string]operatorv1.OperatorAction{
	capturev1connect.CaptureServiceTailCaptureSessionProcedure:     operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_TAIL,
	capturev1connect.CaptureServiceDownloadCaptureSessionProcedure: operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_DOWNLOAD,
}

// Interceptor records operator action events for admitted Connect RPC procedures.
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
	if len(s) > 128 {
		return s[:128]
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

// WrapUnary records operator actions for configured unary procedures.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req == nil {
			return next(ctx, req)
		}

		proc := req.Spec().Procedure
		var (
			action      operatorv1.OperatorAction
			attemptEdge *modeledgev1.EdgeGlobalRef
			record      bool
		)

		if proc == capturev1connect.CaptureServiceCreateCaptureSessionProcedure {
			createReq, ok := req.Any().(*capturev1.CreateCaptureSessionRequest)
			if ok && createReq != nil && createReq.GetAuthorization().GetFullPayloadRequested() {
				record = true
				action = operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_FULL_PAYLOAD_CREATE
				attemptEdge = createReq.GetEdge()
			}
		} else if act, ok := EdgeAdminProcedureActions[proc]; ok {
			record = true
			action = act
			switch proc {
			case edgev1connect.EdgeAdminServiceCreateEdgeProcedure, edgev1connect.EdgeAdminServiceListEdgesProcedure:
				// no attempt object
			case edgev1connect.EdgeAdminServiceIssueSetupKeyProcedure:
				if r, ok := req.Any().(*edgev1.IssueSetupKeyRequest); ok && r != nil {
					attemptEdge = r.GetEdge()
				}
			case edgev1connect.EdgeAdminServiceRevokeSetupKeyProcedure:
				if r, ok := req.Any().(*edgev1.RevokeSetupKeyRequest); ok && r != nil {
					attemptEdge = r.GetEdge()
				}
			case edgev1connect.EdgeAdminServiceRetireEdgeProcedure:
				if r, ok := req.Any().(*edgev1.RetireEdgeRequest); ok && r != nil {
					attemptEdge = r.GetEdge()
				}
			case edgev1connect.EdgeAdminServiceGetEdgeProcedure:
				if r, ok := req.Any().(*edgev1.GetEdgeRequest); ok && r != nil {
					attemptEdge = r.GetEdge()
				}
			}
		}

		if !record {
			return next(ctx, req)
		}

		principal, ok := authn.FromContext(ctx)
		tenantID, err := tenant.FromContext(ctx)
		if !ok || principal.ID == "" || err != nil || tenantID == "" {
			return nil, connecterr.WrapAs(
				connect.CodeInternal,
				"action trail unprepared",
				errs.New().Code(ErrCodeUnprepared).Msg("principal or tenant missing from context"),
			)
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
		if attemptEdge != nil {
			attemptEvent.SetEdge(attemptEdge)
		}
		attemptEvent.SetAttempted(&operatorv1.OperatorActionAttempted{})

		actToken := actionToken(action)
		subject := edgebus.OperatorActionSubject(tenantID, actToken)

		attemptData, err := proto.Marshal(attemptEvent)
		if err != nil {
			return nil, connecterr.WrapAs(
				connect.CodeInternal,
				"action trail attempt marshal",
				errs.From(err).Code(ErrCodeUnavailable).Msg("marshal attempt event"),
			)
		}

		if err := i.pub.Publish(ctx, subject, attemptData, attemptEventID); err != nil {
			return nil, connecterr.WrapAs(
				connect.CodeUnavailable,
				"action trail unavailable",
				errs.New().Code(ErrCodeUnavailable).Cause(err).Msg("failed to publish action attempt"),
			)
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

		switch proc {
		case edgev1connect.EdgeAdminServiceCreateEdgeProcedure:
			if resp != nil {
				if r, ok := resp.Any().(*edgev1.CreateEdgeResponse); ok && r != nil {
					if edgeRef := r.GetEdge().GetConfig().GetRef(); edgeRef != nil {
						completionEvent.SetEdge(edgeRef)
					}
				}
			}
		case capturev1connect.CaptureServiceCreateCaptureSessionProcedure:
			var sessionRef *modelcapturev1.CaptureSessionGlobalRef
			if resp != nil {
				if r, ok := resp.Any().(*capturev1.CreateCaptureSessionResponse); ok && r != nil {
					sessionRef = r.GetSession().GetConfig().GetRef()
				}
			}
			if sessionRef != nil {
				completionEvent.SetCaptureSession(sessionRef)
			} else if attemptEdge != nil {
				completionEvent.SetEdge(attemptEdge)
			}
		default:
			if attemptEdge != nil {
				completionEvent.SetEdge(attemptEdge)
			}
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
		if !authOk || principal.ID == "" || tenantErr != nil || tenantID == "" {
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
