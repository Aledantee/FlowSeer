// Package authz implements relationship-based authorization enforcement for
// operator Connect RPCs.
package authz

import (
	"context"

	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// Errs codes under authz/ identifying specific authorization outcomes.
var (
	ErrCodeUnauthenticated     = errs.NewCode("authz/unauthenticated")
	ErrCodeNoTenant            = errs.NewCode("authz/no-tenant")
	ErrCodeDenied              = errs.NewCode("authz/denied")
	ErrCodeStreaming           = errs.NewCode("authz/streaming-unsupported")
	ErrCodeUnsupportedRule     = errs.NewCode("authz/unsupported-rule")
	ErrCodeNoObjectID          = errs.NewCode("authz/no-object-id")
	ErrCodeUnavailable         = errs.NewCode("authz/unavailable")
	ErrCodeObligationViolation = errs.NewCode("authz/obligation-violation")
)

// Tuple represents a single relationship between an object and a user.
// A Tuple must not be modified once handed to a Checker.
type Tuple struct {
	Object   string
	Relation string
	User     string
}

// Query specifies an authorization check over an object and relation for a user,
// carrying contextual tuples that hold for the duration of the check.
// A Query must not be modified once handed to a Checker.
type Query struct {
	Object           string
	Relation         string
	User             string
	ContextualTuples []Tuple
}

// Checker evaluates authorization queries against a relationship graph.
// Implementations must be safe for concurrent use and must not modify a
// query or its tuples. Implementations should honor context cancellation.
// Callers inspect ctx.Err() on failure.
type Checker interface {
	Check(ctx context.Context, q Query) (bool, error)
	// BatchCheck evaluates queries in batch, returning one boolean answer per
	// query in identical query order.
	BatchCheck(ctx context.Context, queries []Query) ([]bool, error)
}

func invalidTenant(err error) error {
	return connecterr.WrapAs(connect.CodeInvalidArgument, "no tenant named", err)
}

func permissionDenied(err error) error {
	return connecterr.WrapAs(connect.CodePermissionDenied, "permission denied", err)
}

func checkerError(ctx context.Context, err error, msg string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	b := errs.New().Code(ErrCodeUnavailable).Retryable()
	if err != nil {
		b = b.Cause(err)
	}
	return connecterr.WrapAs(connect.CodeUnavailable, "authorization is unavailable", b.Msg(msg))
}

func internalError(err error) error {
	return connecterr.WrapAs(connect.CodeInternal, "", err)
}
