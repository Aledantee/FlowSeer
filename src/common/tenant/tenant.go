// Package tenant provides context utilities for ambient tenancy.
package tenant

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeNoTenant is the error code returned when no tenant is found in context.
var ErrCodeNoTenant = errs.NewCode("tenant/no-tenant")

type contextKey struct{}

var tenantKey = contextKey{}

// WithTenant returns a new context carrying the given tenantID.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey, tenantID)
}

// FromContext extracts the tenantID from ctx. It returns an error with
// ErrCodeNoTenant if no tenant is set.
func FromContext(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", errs.New().Code(ErrCodeNoTenant).Msg("no tenant in context")
	}
	val, ok := ctx.Value(tenantKey).(string)
	if !ok || val == "" {
		return "", errs.New().Code(ErrCodeNoTenant).Msg("no tenant in context")
	}
	return val, nil
}
