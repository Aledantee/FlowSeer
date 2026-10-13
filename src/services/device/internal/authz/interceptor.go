package authz

import (
	"context"
	"slices"
	"strings"
	"sync"

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

const (
	tenantHeader = "X-FlowSeer-Tenant"
	// maxTenantSet is the most tenants one call may name. Each tenant costs two
	// checks, membership and the rule's relation, and [maxChecksPerBatch] is 50,
	// so 25 tenants are admitted by one BatchCheck.
	maxTenantSet = maxChecksPerBatch / 2
)

// singleTenantHeader returns the tenant a call names. A header sent on more
// than one line is refused, since the interceptor would otherwise admit the
// first line and ignore the others.
func singleTenantHeader(values []string) (string, error) {
	switch len(values) {
	case 0:
		return "", nil
	case 1:
		return values[0], nil
	}
	return "", invalidTenant(errs.New().Code(ErrCodeTenantList).Attr("lines", len(values)).
		Msg("tenant header sent on more than one line"))
}

// tenantList splits the header lines of a method that reads several tenants.
// It refuses an empty list, an empty item, a duplicate, an invalid tenant, and
// more than [maxTenantSet] tenants. Spaces around an item are ignored.
func tenantList(values []string) ([]string, error) {
	var tenants []string
	seen := map[string]bool{}
	for _, line := range values {
		for item := range strings.SplitSeq(line, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, invalidTenant(errs.New().Code(ErrCodeTenantList).Msg("tenant list holds an empty item"))
			}
			if err := tenant.Validate(item); err != nil {
				return nil, invalidTenant(errs.New().Code(ErrCodeNoTenant).Cause(err).Msg("no tenant named"))
			}
			if seen[item] {
				return nil, invalidTenant(errs.New().Code(ErrCodeTenantList).Attr("tenant", item).Msg("tenant list repeats a tenant"))
			}
			seen[item] = true
			tenants = append(tenants, item)
		}
	}
	if len(tenants) == 0 {
		return nil, invalidTenant(errs.New().Code(ErrCodeNoTenant).Msg("no tenant named"))
	}
	if len(tenants) > maxTenantSet {
		return nil, invalidTenant(errs.New().Code(ErrCodeTenantList).Attr("tenants", len(tenants)).
			Msg("tenant list is longer than one authorization batch"))
	}
	return tenants, nil
}

// admitSet is [Interceptor.Admit] for a method whose rule sets multi_tenant. It
// admits every tenant the header lists in one BatchCheck, membership and the
// rule's relation for each, and fails the call when any check is denied. The
// returned context carries the tenants as a set ([tenant.SetFrom]) and no single
// tenant.
func (i *Interceptor) admitSet(ctx context.Context, values []string, relation string) (context.Context, error) {
	principal, ok := authn.FromContext(ctx)
	if !ok || principal.ID == "" {
		return nil, connecterr.WrapAs(connect.CodeUnauthenticated, "authentication required", errs.New().Code(ErrCodeUnauthenticated).
			Msg("authentication required"))
	}
	tenants, err := tenantList(values)
	if err != nil {
		return nil, err
	}

	tuples := contextualTuples(principal)
	queries := make([]Query, 0, 2*len(tenants))
	for _, id := range tenants {
		queries = append(queries,
			Query{Object: "tenant:" + id, Relation: "member", User: "user:" + principal.ID, ContextualTuples: tuples},
			Query{Object: "tenant:" + id, Relation: relation, User: "user:" + principal.ID, ContextualTuples: tuples},
		)
	}
	answers, err := i.checker.BatchCheck(ctx, queries)
	if err != nil {
		return nil, checkerError(ctx, err, "authorization is unavailable")
	}
	if len(answers) != len(queries) {
		return nil, checkerError(ctx, nil, "checker returned unexpected answer count")
	}
	if slices.Contains(answers, false) {
		return nil, permissionDenied(errs.New().Code(ErrCodeDenied).Msg("permission denied"))
	}

	ctx = tenant.WithSet(ctx, tenants)
	return withTracker(ctx, newObligationTracker(i.checker, principal, "")), nil
}

// Admit verifies caller authentication, validates the tenant identifier, confirms
// that the principal is an active member of the tenant, and returns a child context
// carrying the admitted tenant and an obligation tracker.
//
// It returns Unauthenticated when caller identity is missing, InvalidArgument when
// the tenant identifier is missing or malformed, PermissionDenied when membership
// is denied, and Unavailable when the checker fails.
func (i *Interceptor) Admit(ctx context.Context, tenantID string) (context.Context, error) {
	principal, ok := authn.FromContext(ctx)
	if !ok || principal.ID == "" {
		return nil, connecterr.WrapAs(connect.CodeUnauthenticated, "authentication required", errs.New().Code(ErrCodeUnauthenticated).
			Msg("authentication required"))
	}

	if tenantID == "" {
		return nil, invalidTenant(errs.New().Code(ErrCodeNoTenant).Msg("no tenant named"))
	}
	if err := tenant.Validate(tenantID); err != nil {
		return nil, invalidTenant(errs.New().Code(ErrCodeNoTenant).Cause(err).Msg("no tenant named"))
	}

	tuples := contextualTuples(principal)
	membershipQuery := Query{
		Object:           "tenant:" + tenantID,
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

	ctx = tenant.WithTenant(ctx, tenantID)
	tracker := newObligationTracker(i.checker, principal, tenantID)
	ctx = withTracker(ctx, tracker)
	return ctx, nil
}

// WrapStreamingClient passes streaming client calls through unmodified.
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler authorizes server-streaming RPCs under a request rule,
// checking caller membership at admission and evaluating object permissions on
// the first received request message.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		md, ok := conn.Spec().Schema.(protoreflect.MethodDescriptor)
		if !ok || md == nil {
			return permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
				Msg("request schema is not a method descriptor"))
		}

		opts, ok := md.Options().(*descriptorpb.MethodOptions)
		if !ok || opts == nil || !proto.HasExtension(opts, authzv1.E_Rule) {
			return permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
				Msg("method carries no authorization rule"))
		}

		rule, ok := proto.GetExtension(opts, authzv1.E_Rule).(*authzv1.Rule)
		if !ok || rule == nil {
			return permissionDenied(errs.New().Code(ErrCodeUnsupportedRule).
				Msg("method carries no authorization rule"))
		}

		if conn.Spec().StreamType != connect.StreamTypeServer || rule.GetMode() != authzv1.RuleMode_RULE_MODE_REQUEST {
			return permissionDenied(errs.New().Code(ErrCodeStreaming).
				Msg("streaming calls are supported only under a request rule on a server stream"))
		}

		tenantID, err := singleTenantHeader(conn.RequestHeader().Values(tenantHeader))
		if err != nil {
			return err
		}
		admittedCtx, err := i.Admit(ctx, tenantID)
		if err != nil {
			return err
		}

		principal, _ := authn.FromContext(admittedCtx)
		tracker := trackerFromContext(admittedCtx)

		wrapped := &authzStreamingConn{
			StreamingHandlerConn: conn,
			ctx:                  admittedCtx,
			interceptor:          i,
			rule:                 rule,
			principal:            principal,
			admittedTenant:       tenantID,
		}

		err = next(admittedCtx, wrapped)
		if tracker != nil {
			_, checkFailed, inFlight := tracker.flags()
			if err == nil && inFlight > 0 {
				return internalError(errs.New().Code(ErrCodeObligationViolation).
					Msg("handler returned while an authorization check was in flight"))
			}
			if err == nil && checkFailed {
				return internalError(errs.New().Code(ErrCodeObligationViolation).
					Msg("handler returned response after authorization check failed"))
			}
		}
		return err
	}
}

type authzStreamingConn struct {
	connect.StreamingHandlerConn
	ctx            context.Context
	interceptor    *Interceptor
	rule           *authzv1.Rule
	principal      authn.Principal
	admittedTenant string

	mu       sync.Mutex
	checked  bool
	checkErr error
}

func (c *authzStreamingConn) Receive(msg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checked {
		err := c.checkErr
		if err != nil {
			return err
		}
		return c.StreamingHandlerConn.Receive(msg)
	}
	c.checked = true

	if err := c.StreamingHandlerConn.Receive(msg); err != nil {
		c.checkErr = err
		return err
	}

	protoMsg, ok := msg.(proto.Message)
	if !ok || protoMsg == nil {
		c.checkErr = permissionDenied(errs.New().Code(ErrCodeNoObjectID).
			Msg("request is not a proto message"))
		return c.checkErr
	}

	objectID, err := extractObjectID(protoMsg, c.rule.GetObjectIdPath())
	if err != nil || objectID == "" {
		c.checkErr = permissionDenied(errs.New().Code(ErrCodeNoObjectID).Cause(err).
			Msg("request rule yielded no id"))
		return c.checkErr
	}

	allowed, err := checkObjects(c.ctx, c.interceptor.checker, c.principal, c.admittedTenant, c.rule.GetObjectType(), c.rule.GetRelation(), []string{objectID})
	if err != nil {
		c.checkErr = err
		return err
	}
	if len(allowed) == 0 {
		c.checkErr = permissionDenied(errs.New().Code(ErrCodeDenied).
			Msg("permission denied"))
		return c.checkErr
	}
	return nil
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
			tracker = newObligationTracker(i.checker, principal, "")
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
			tenantValues := req.Header().Values(tenantHeader)
			multiTenant := mode == authzv1.RuleMode_RULE_MODE_TENANT && rule.GetMultiTenant()
			var tenantID string
			var err error
			if multiTenant {
				ctx, err = i.admitSet(ctx, tenantValues, rule.GetRelation())
			} else {
				if tenantID, err = singleTenantHeader(tenantValues); err != nil {
					return nil, err
				}
				ctx, err = i.Admit(ctx, tenantID)
			}
			if err != nil {
				return nil, err
			}
			tracker = trackerFromContext(ctx)

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

				allowed, err := checkObjects(ctx, i.checker, principal, tenantID, rule.GetObjectType(), rule.GetRelation(), []string{objectID})
				if err != nil {
					return nil, err
				}
				if len(allowed) == 0 {
					return nil, permissionDenied(errs.New().Code(ErrCodeDenied).
						Msg("permission denied"))
				}

			case authzv1.RuleMode_RULE_MODE_TENANT:
				if multiTenant {
					break
				}
				allowed, err := checkObjects(ctx, i.checker, principal, tenantID, "tenant", rule.GetRelation(), []string{tenantID})
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
		discharged, checkFailed, inFlight := tracker.flags()
		if (mode == authzv1.RuleMode_RULE_MODE_LOADED || mode == authzv1.RuleMode_RULE_MODE_FILTERED) && !discharged {
			if err != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, internalError(errs.New().Code(ErrCodeObligationViolation).
				Msg("handler returned without fulfilling authorization obligation"))
		}
		if err == nil && inFlight > 0 {
			return nil, internalError(errs.New().Code(ErrCodeObligationViolation).
				Msg("handler returned while an authorization check was in flight"))
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
