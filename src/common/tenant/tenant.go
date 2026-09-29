// Package tenant provides context utilities for ambient tenancy.
package tenant

import (
	"context"

	"github.com/google/uuid"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// DefaultTenant is the default tenant identifier used in dev and test environments.
const DefaultTenant = "default"

// ErrCodeNoTenant is the error code returned when no tenant is found in context.
var ErrCodeNoTenant = errs.NewCode("tenant/no-tenant")

// ErrCodeInvalidTenant is the error code returned when a tenant identifier is not valid.
var ErrCodeInvalidTenant = errs.NewCode("tenant/invalid-tenant")

type contextKey struct{}

var tenantKey = contextKey{}

// WithTenant returns a new context carrying the given tenantID.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey, tenantID)
}

// FromContext extracts the tenantID from ctx. It returns an error with
// ErrCodeNoTenant if no tenant is set.
func FromContext(ctx context.Context) (string, error) {
	val, ok := ctx.Value(tenantKey).(string)
	if !ok || val == "" {
		return "", errs.New().Code(ErrCodeNoTenant).Msg("no tenant in context")
	}
	return val, nil
}

// Validate checks that tenantID is either a canonical lowercase UUID or DefaultTenant.
func Validate(tenantID string) error {
	if tenantID == DefaultTenant {
		return nil
	}
	u, err := uuid.Parse(tenantID)
	if err != nil || u.String() != tenantID {
		return errs.New().Code(ErrCodeInvalidTenant).Attr("tenant", tenantID).
			Msg("tenant identifier must be a UUID or the default tenant")
	}
	return nil
}
