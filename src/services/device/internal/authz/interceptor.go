package authz

import (
	"context"
	"strings"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// Interceptor enforces operator RPC authorization rules before delegating to
// service handlers.
// An Interceptor is safe for concurrent use.
type Interceptor struct {
	checker Checker
}

// NewInterceptor constructs an authorization interceptor backed by checker.
func NewInterceptor(checker Checker) *Interceptor {
	return &Interceptor{checker: checker}
}

// WrapStreamingClient passes streaming client calls through unmodified.
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler refuses every streaming handler call with PermissionDenied.
func (i *Interceptor) WrapStreamingHandler(_ connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(_ context.Context, _ connect.StreamingHandlerConn) error {
		return permissionDenied(errs.New().Code(ErrCodeStreaming).
			Msg("streaming calls are refused until streaming rules are defined"))
	}
}

// WrapUnary enforces authorization rules, caller identity, and relationship
// permissions around a unary handler.
//
// Before invoking the handler, WrapUnary returns Unauthenticated when caller
// identity is missing, InvalidArgument when the tenant header is missing or
// invalid, and PermissionDenied when rules are unsupported, object identifiers
// cannot be extracted, or checks are denied.
//
// A checker error yields ctx.Err() when the context has ended, and Unavailable
// otherwise.
//
// WrapUnary returns Internal with no response when a loaded or filtered handler
// discharged no check, or when the handler returned a response after Require or
// Filter returned an error. A loaded or filtered handler that discharged no check
// and returned an error while ctx.Err() is non-nil yields ctx.Err().
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		md, ok := req.Spec().Schema.(protoreflect.MethodDescriptor)
		if !ok || md == nil {
			return nil, permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
				Msg("request schema is not a method descriptor"))
		}

		opts, ok := md.Options().(*descriptorpb.MethodOptions)
		if !ok || opts == nil || !proto.HasExtension(opts, authzv1.E_Rule) {
			return nil, permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
				Msg("method carries no authorization rule"))
		}

		rule, ok := proto.GetExtension(opts, authzv1.E_Rule).(*authzv1.Rule)
		if !ok || rule == nil {
			return nil, permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
				Msg("method carries no authorization rule"))
		}

		mode := rule.GetMode()
		switch mode {
		case authzv1.RuleMode_RULE_MODE_REQUEST,
			authzv1.RuleMode_RULE_MODE_TENANT,
			authzv1.RuleMode_RULE_MODE_LOADED,
			authzv1.RuleMode_RULE_MODE_FILTERED,
			authzv1.RuleMode_RULE_MODE_PLATFORM:
		default:
			return nil, permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
				Attr("mode", mode.String()).Msg("unsupported or unspecified authorization rule mode"))
		}

		principal, ok := authn.FromContext(ctx)
		if !ok || principal.ID == "" {
			return nil, connecterr.WrapAs(connect.CodeUnauthenticated, "authentication required", errs.New().Code(ErrCodeUnauthenticated).
				Msg("authentication required"))
		}

		tuples := contextualTuples(principal)

		var tracker *obligationTracker
		if mode == authzv1.RuleMode_RULE_MODE_PLATFORM {
			tracker = &obligationTracker{
				checker:   i.checker,
				principal: principal,
			}
			ctx = withTracker(ctx, tracker)

			q := Query{
				Object:           "platform:flowseer",
				Relation:         rule.GetRelation(),
				User:             "user:" + principal.ID,
				ContextualTuples: tuples,
			}
			allowed, err := i.checker.Check(ctx, q)
			if err != nil {
				return nil, checkerError(ctx, err, "authorization is unavailable")
			}
			if !allowed {
				return nil, permissionDenied(errs.New().Code(ErrCodeDenied).
					Msg("permission denied"))
			}
		} else {
			tenantHeader := req.Header().Get("X-FlowSeer-Tenant")
			if tenantHeader == "" {
				return nil, invalidTenant(errs.New().Code(ErrCodeNoTenant).Msg("no tenant named"))
			}
			if err := tenant.Validate(tenantHeader); err != nil {
				return nil, invalidTenant(errs.New().Code(ErrCodeNoTenant).Cause(err).Msg("no tenant named"))
			}

			membershipQuery := Query{
				Object:           "tenant:" + tenantHeader,
				Relation:         "member",
				User:             "user:" + principal.ID,
				ContextualTuples: tuples,
			}
			member, err := i.checker.Check(ctx, membershipQuery)
			if err != nil {
				return nil, checkerError(ctx, err, "authorization is unavailable")
			}
			if !member {
				return nil, permissionDenied(errs.New().Code(ErrCodeDenied).Msg("permission denied"))
			}

			ctx = tenant.WithTenant(ctx, tenantHeader)

			tracker = &obligationTracker{
				checker:        i.checker,
				principal:      principal,
				admittedTenant: tenantHeader,
			}
			ctx = withTracker(ctx, tracker)

			switch mode {
			case authzv1.RuleMode_RULE_MODE_REQUEST:
				msg, ok := req.Any().(proto.Message)
				if !ok || msg == nil {
					return nil, permissionDenied(errs.New().Code(ErrCodeNoObjectID).
						Msg("request is not a proto message"))
				}
				objectID, err := extractObjectID(msg, rule.GetObjectIdPath())
				if err != nil || objectID == "" {
					return nil, permissionDenied(errs.New().Code(ErrCodeNoObjectID).Cause(err).
						Msg("request rule yielded no id"))
				}

				allowed, err := checkObjects(ctx, i.checker, principal, tenantHeader, rule.GetObjectType(), rule.GetRelation(), []string{objectID})
				if err != nil {
					return nil, err
				}
				if len(allowed) == 0 {
					return nil, permissionDenied(errs.New().Code(ErrCodeDenied).
						Msg("permission denied"))
				}

			case authzv1.RuleMode_RULE_MODE_TENANT:
				allowed, err := checkObjects(ctx, i.checker, principal, tenantHeader, "tenant", rule.GetRelation(), []string{tenantHeader})
				if err != nil {
					return nil, err
				}
				if len(allowed) == 0 {
					return nil, permissionDenied(errs.New().Code(ErrCodeDenied).
						Msg("permission denied"))
				}

			case authzv1.RuleMode_RULE_MODE_LOADED, authzv1.RuleMode_RULE_MODE_FILTERED:
				// Handlers discharge obligations during execution.
			}
		}

		resp, err := next(ctx, req)
		discharged, checkFailed := tracker.flags()
		if (mode == authzv1.RuleMode_RULE_MODE_LOADED || mode == authzv1.RuleMode_RULE_MODE_FILTERED) && !discharged {
			if err != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, internalError(errs.New().Code(ErrCodeObligationViolation).
				Msg("handler returned without fulfilling authorization obligation"))
		}
		if err == nil && checkFailed {
			return nil, internalError(errs.New().Code(ErrCodeObligationViolation).
				Msg("handler returned response after authorization check failed"))
		}
		return resp, err
	}
}

func extractObjectID(msg proto.Message, path string) (string, error) {
	if msg == nil || path == "" {
		return "", errs.New().Code(ErrCodeNoObjectID).Msg("missing message or path")
	}
	parts := strings.Split(path, ".")
	refl := msg.ProtoReflect()
	for i, part := range parts {
		fd := refl.Descriptor().Fields().ByName(protoreflect.Name(part))
		if fd == nil || !refl.Has(fd) {
			return "", errs.New().Code(ErrCodeNoObjectID).
				Attr("field", part).Msg("field not set on message")
		}
		if i < len(parts)-1 {
			if fd.IsList() || fd.IsMap() || fd.Kind() != protoreflect.MessageKind {
				return "", errs.New().Code(ErrCodeNoObjectID).
					Attr("field", part).Msg("intermediate field is not a message")
			}
			refl = refl.Get(fd).Message()
		} else {
			if fd.IsList() || fd.IsMap() || fd.Kind() != protoreflect.StringKind {
				return "", errs.New().Code(ErrCodeNoObjectID).
					Attr("field", part).Msg("leaf field is not a string")
			}
			return refl.Get(fd).String(), nil
		}
	}
	return "", errs.New().Code(ErrCodeNoObjectID).Msg("path not resolved")
}
