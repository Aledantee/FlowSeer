// Package authn carries caller identity across request contexts.
package authn

import (
	"context"
)

// Principal represents the authenticated caller identity.
// A Principal is safe for concurrent use across goroutines.
type Principal struct {
	ID       string
	Tenants  []string
	Platform bool
}

type principalKey struct{}

// NewContext returns a new context carrying the given principal.
func NewContext(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext extracts the Principal from ctx, returning false if no principal is present.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
