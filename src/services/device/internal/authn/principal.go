// Package authn carries caller identity across request contexts.
package authn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Principal represents the authenticated caller identity.
// A Principal must not be modified once placed in a context.
type Principal struct {
	ID       string
	Issuer   string
	Subject  string
	Tenants  []string
	Platform bool
}

// ComputePrincipalID returns the lowercase hex-encoded SHA-256 digest
// of issuer, a zero byte, and subject.
func ComputePrincipalID(issuer, subject string) string {
	h := sha256.New()
	h.Write([]byte(issuer))
	h.Write([]byte{0})
	h.Write([]byte(subject))
	return hex.EncodeToString(h.Sum(nil))
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
