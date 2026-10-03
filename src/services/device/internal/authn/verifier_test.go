package authn_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"google.golang.org/protobuf/proto"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn/authntest"
)

func signRSAToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	hJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hJSON) + "." + base64.RawURLEncoding.EncodeToString(cJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("rsa sign: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func signECDSAToken(t *testing.T, priv *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "ES256", "typ": "JWT", "kid": kid}
	hJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hJSON) + "." + base64.RawURLEncoding.EncodeToString(cJSON)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("ecdsa sign: %v", err)
	}
	sigBytes := make([]byte, 64)
	r.FillBytes(sigBytes[:32])
	s.FillBytes(sigBytes[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sigBytes)
}

func unsignedToken(t *testing.T, header, claims map[string]any) string {
	t.Helper()
	hJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(hJSON) + "." + base64.RawURLEncoding.EncodeToString(cJSON) + "."
}

// settableClock is a clock a test moves while the verifier's goroutines read
// it. The verifier reads the clock from its discovery goroutine and from
// go-oidc's key-fetch goroutine, so a plain variable would race.
type settableClock struct {
	nanos atomic.Int64
}

func newSettableClock(t time.Time) *settableClock {
	c := &settableClock{}
	c.nanos.Store(t.UnixNano())
	return c
}

func (c *settableClock) Now() time.Time {
	return time.Unix(0, c.nanos.Load()).UTC()
}

func (c *settableClock) Set(t time.Time) {
	c.nanos.Store(t.UnixNano())
}

func (c *settableClock) Advance(d time.Duration) {
	c.nanos.Add(int64(d))
}

func TestVerifierAcceptedTokens(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.Server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "org",
			},
		},
		Client: srv.Server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	acceptedClaims := map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}

	// RSA token
	rsaTok := signRSAToken(t, srv.RSAKey, srv.RSAKID, acceptedClaims)
	p, err := verifier.Verify(context.Background(), rsaTok)
	if err != nil {
		t.Fatalf("verify accepted RSA token: %v", err)
	}
	if p.Issuer != srv.Server.URL {
		t.Errorf("got Issuer %q, want %q", p.Issuer, srv.Server.URL)
	}
	if p.Subject != "u1" {
		t.Errorf("got Subject %q, want u1", p.Subject)
	}
	expectedID := authn.ComputePrincipalID(srv.Server.URL, "u1")
	if p.ID != expectedID || len(p.ID) != 64 {
		t.Errorf("got ID %q, want 64-char %q", p.ID, expectedID)
	}

	// ECDSA token
	ecTok := signECDSAToken(t, srv.ECKey, srv.ECKID, acceptedClaims)
	pEC, err := verifier.Verify(context.Background(), ecTok)
	if err != nil {
		t.Fatalf("verify accepted ECDSA token: %v", err)
	}
	if pEC.ID != expectedID || pEC.Subject != "u1" {
		t.Errorf("ECDSA principal mismatch: %+v", pEC)
	}
}

func TestVerifierRefusalCases(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)

	otherRSAKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.Server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "org",
			},
		},
		Client: srv.Server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	baseClaims := func() map[string]any {
		return map[string]any{
			"iss": srv.Server.URL,
			"aud": "flowseer-device",
			"sub": "u1",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		}
	}

	cases := []struct {
		name     string
		makeTok  func() string
		wantCode errs.Code
	}{
		{
			name: "wrong audience aud: other",
			makeTok: func() string {
				c := baseClaims()
				c["aud"] = "other"
				return signRSAToken(t, srv.RSAKey, srv.RSAKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "unknown iss",
			makeTok: func() string {
				c := baseClaims()
				c["iss"] = "https://unknown.issuer.example.com"
				return signRSAToken(t, srv.RSAKey, srv.RSAKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "alg none",
			makeTok: func() string {
				c := baseClaims()
				return unsignedToken(t, map[string]any{"alg": "none", "typ": "JWT"}, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "another key signature",
			makeTok: func() string {
				c := baseClaims()
				// signed with a key not in JWKS
				return signRSAToken(t, otherRSAKey, "other-kid", c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "no sub claim",
			makeTok: func() string {
				c := baseClaims()
				delete(c, "sub")
				return signRSAToken(t, srv.RSAKey, srv.RSAKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "empty sub claim",
			makeTok: func() string {
				c := baseClaims()
				c["sub"] = ""
				return signRSAToken(t, srv.RSAKey, srv.RSAKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "sub with null byte",
			makeTok: func() string {
				c := baseClaims()
				c["sub"] = "u1\x00extra"
				return signRSAToken(t, srv.RSAKey, srv.RSAKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "past exp",
			makeTok: func() string {
				c := baseClaims()
				c["exp"] = now.Add(-10 * time.Minute).Unix()
				return signRSAToken(t, srv.RSAKey, srv.RSAKID, c)
			},
			wantCode: authn.ErrCodeTokenExpired,
		},
		{
			name: "without exp",
			makeTok: func() string {
				c := baseClaims()
				delete(c, "exp")
				return signRSAToken(t, srv.RSAKey, srv.RSAKID, c)
			},
			wantCode: authn.ErrCodeTokenExpired,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.makeTok()
			_, err := verifier.Verify(context.Background(), token)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			code, ok := errs.CodeOf(err)
			if !ok || code != tc.wantCode {
				t.Fatalf("got code %v, want %v (err: %v)", code, tc.wantCode, err)
			}
		})
	}
}

func TestVerifierOrganizationsAndPlatform(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)

	fakeStore := map[string]*identityv1.TenantRecord{
		"acme": identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-A")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.Server.URL),
				OrganizationClaimName:  proto.String("organization"),
				OrganizationClaimValue: proto.String("acme"),
			}.Build(),
			State: identityv1.TenantState_builder{
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
		}.Build(),
		"globex": identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-B")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.Server.URL),
				OrganizationClaimName:  proto.String("organization"),
				OrganizationClaimValue: proto.String("globex"),
			}.Build(),
			State: identityv1.TenantState_builder{
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
		}.Build(),
		"other-claim": identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-C")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.Server.URL),
				OrganizationClaimName:  proto.String("groups"),
				OrganizationClaimValue: proto.String("other-claim"),
			}.Build(),
			State: identityv1.TenantState_builder{
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
		}.Build(),
	}

	var failStore atomic.Bool
	resolver := func(_ context.Context, issuer, org string) (*identityv1.TenantRecord, error) {
		if failStore.Load() {
			return nil, errors.New("simulated store failure")
		}
		if issuer != srv.Server.URL {
			return nil, nil
		}
		return fakeStore[org], nil
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.Server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "organization",
			},
		},
		Platform: authn.PlatformConfig{
			Issuer:       srv.Server.URL,
			ClaimName:    "organization",
			Organization: "platform-ops",
		},
		Resolver: resolver,
		Client:   srv.Server.Client(),
		Clock:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	baseClaims := func(orgClaim any) map[string]any {
		m := map[string]any{
			"iss": srv.Server.URL,
			"aud": "flowseer-device",
			"sub": "u1",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		}
		if orgClaim != nil {
			m["organization"] = orgClaim
		}
		return m
	}

	// String claim: "acme" -> [tenant-A]
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims("acme"))
	p, err := verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify string org: %v", err)
	}
	if !slices.Equal(p.Tenants, []string{"tenant-A"}) {
		t.Fatalf("got tenants %v, want [tenant-A]", p.Tenants)
	}
	if p.Platform {
		t.Fatal("expected Platform false")
	}

	// Array claim: ["acme", "globex"] -> [tenant-A, tenant-B]
	tok = signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims([]string{"acme", "globex"}))
	p, err = verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify array org: %v", err)
	}
	if !slices.Equal(p.Tenants, []string{"tenant-A", "tenant-B"}) {
		t.Fatalf("got tenants %v, want [tenant-A, tenant-B]", p.Tenants)
	}

	// Object claim: {"acme": {}, "globex": {"roles": []}} -> [tenant-A, tenant-B]
	tok = signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims(map[string]any{"acme": true, "globex": 123}))
	p, err = verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify object org: %v", err)
	}
	if !slices.Equal(p.Tenants, []string{"tenant-A", "tenant-B"}) {
		t.Fatalf("got tenants %v, want [tenant-A, tenant-B]", p.Tenants)
	}

	// Binding under different claim name ("groups") yields none
	tok = signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims("other-claim"))
	p, err = verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify mismatched claim name: %v", err)
	}
	if len(p.Tenants) != 0 {
		t.Fatalf("got tenants %v, want empty", p.Tenants)
	}

	// Store error yields Unavailable with authn/unavailable
	failStore.Store(true)
	tok = signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims("acme"))
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error on store failure, got nil")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable", code)
	}
	failStore.Store(false)

	// Number as claim yields authn/token-invalid
	tok = signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims(42))
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error for numeric org claim")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid", code)
	}

	// Null organization claim yields authn/token-invalid
	tokNull := signRSAToken(t, srv.RSAKey, srv.RSAKID, map[string]any{
		"iss":          srv.Server.URL,
		"aud":          "flowseer-device",
		"sub":          "u1",
		"exp":          now.Add(time.Hour).Unix(),
		"iat":          now.Unix(),
		"organization": nil,
	})
	_, err = verifier.Verify(context.Background(), tokNull)
	if err == nil {
		t.Fatal("expected error for null org claim")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid for null claim", code)
	}

	// Array claim holding non-string item yields authn/token-invalid
	tokNonStr := signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims([]any{"acme", 123}))
	_, err = verifier.Verify(context.Background(), tokNonStr)
	if err == nil {
		t.Fatal("expected error for non-string item in org array")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid for non-string array item", code)
	}

	// 100 values in organization claim yields authn/token-invalid
	var hundredOrgs []string
	for i := 0; i < 100; i++ {
		hundredOrgs = append(hundredOrgs, fmt.Sprintf("org-%d", i))
	}
	tok = signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims(hundredOrgs))
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error for 100 org claim values")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid", code)
	}

	// Platform flag set and unset by one field
	tokPlatform := signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims("platform-ops"))
	pPlat, err := verifier.Verify(context.Background(), tokPlatform)
	if err != nil {
		t.Fatalf("verify platform token: %v", err)
	}
	if !pPlat.Platform {
		t.Fatal("expected Platform true for platform-ops claim")
	}

	tokNonPlatform := signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims("acme"))
	pNonPlat, err := verifier.Verify(context.Background(), tokNonPlatform)
	if err != nil {
		t.Fatalf("verify non-platform token: %v", err)
	}
	if pNonPlat.Platform {
		t.Fatal("expected Platform false for acme claim")
	}

	// Platform claim name differing from issuer organization claim name
	platDiffVerifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.Server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "org",
			},
		},
		Platform: authn.PlatformConfig{
			Issuer:       srv.Server.URL,
			ClaimName:    "groups",
			Organization: "platform-ops",
		},
		Resolver: resolver,
		Client:   srv.Server.Client(),
		Clock:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier platform differing: %v", err)
	}
	diffClaims := map[string]any{
		"iss":    srv.Server.URL,
		"aud":    "flowseer-device",
		"sub":    "u1",
		"exp":    now.Add(time.Hour).Unix(),
		"iat":    now.Unix(),
		"org":    "acme",
		"groups": []string{"platform-ops"},
	}
	tokDiff := signRSAToken(t, srv.RSAKey, srv.RSAKID, diffClaims)
	pDiff, err := platDiffVerifier.Verify(context.Background(), tokDiff)
	if err != nil {
		t.Fatalf("verify platform claim differing: %v", err)
	}
	if !pDiff.Platform {
		t.Fatal("expected Platform true when platform claim name differs and value matches")
	}

	// A platform claim under its own name that is neither a string, an array
	// of strings, nor an object refuses the token, and the error names it.
	platformClaimRefusals := []struct {
		name  string
		claim any
	}{
		{name: "null", claim: nil},
		{name: "number", claim: 42},
		{name: "boolean", claim: true},
		{name: "array with a non-string item", claim: []any{"platform-ops", 7}},
	}
	for _, tc := range platformClaimRefusals {
		claims := cloneMap(diffClaims)
		claims["groups"] = tc.claim
		_, err := platDiffVerifier.Verify(context.Background(), signRSAToken(t, srv.RSAKey, srv.RSAKID, claims))
		if err == nil {
			t.Fatalf("platform claim %s: expected error, got nil", tc.name)
		}
		if code, ok := errs.CodeOf(err); !ok || code != authn.ErrCodeTokenInvalid {
			t.Fatalf("platform claim %s: got code %v, want authn/token-invalid (err: %v)", tc.name, code, err)
		}
		if !strings.Contains(err.Error(), "platform claim") || strings.Contains(err.Error(), "organization claim") {
			t.Fatalf("platform claim %s: error names the wrong claim: %v", tc.name, err)
		}
	}

	// Platform issuer differs from token's issuer yields Platform false
	otherIssuerVerifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.Server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "organization",
			},
		},
		Platform: authn.PlatformConfig{
			Issuer:       "https://other-platform.example.com",
			ClaimName:    "organization",
			Organization: "platform-ops",
		},
		Client: srv.Server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier other platform issuer: %v", err)
	}
	tokOtherIssuer := signRSAToken(t, srv.RSAKey, srv.RSAKID, baseClaims("platform-ops"))
	pOtherIssuer, err := otherIssuerVerifier.Verify(context.Background(), tokOtherIssuer)
	if err != nil {
		t.Fatalf("verify other platform issuer: %v", err)
	}
	if pOtherIssuer.Platform {
		t.Fatal("expected Platform false when platform issuer differs from token issuer")
	}
}

func TestVerifierReplayTransportAndKeyOutage(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)
	clock := newSettableClock(now)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.Server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.Server.Client(),
		Clock:  clock.Now,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}

	// Repeated unknown kid tokens within 10 s replay window trigger one key fetch.
	startFetchCount := srv.KeyFetchCount.Load()
	for i := 0; i < 20; i++ {
		tokUnknown := signRSAToken(t, otherKey, fmt.Sprintf("unknown-kid-%d", i), claims)
		_, _ = verifier.Verify(context.Background(), tokUnknown)
	}
	fetchesAfter20 := srv.KeyFetchCount.Load() - startFetchCount
	if fetchesAfter20 != 1 {
		t.Fatalf("expected 1 key fetch for 20 unknown kid tokens within 10s, got %d", fetchesAfter20)
	}

	// Key endpoint answering 500 yields Unavailable for unknown kid.
	clock.Advance(15 * time.Second)
	srv.KeysErr.Store(true)
	tokUnknown500 := signRSAToken(t, otherKey, "unknown-kid-fail", claims)
	_, err = verifier.Verify(context.Background(), tokUnknown500)
	if err == nil {
		t.Fatal("expected error with failing keys endpoint")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable when keys answer 500", code)
	}

	// Known-kid token with wrong audience yields authn/token-invalid despite key outage.
	wrongAudClaims := map[string]any{
		"iss": srv.Server.URL,
		"aud": "wrong-audience",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tokWrongAud := signRSAToken(t, srv.RSAKey, srv.RSAKID, wrongAudClaims)
	_, err = verifier.Verify(context.Background(), tokWrongAud)
	if err == nil {
		t.Fatal("expected error for wrong audience token")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid for wrong audience during key outage", code)
	}

	// Healthy keys endpoint yields Unauthenticated with authn/token-invalid.
	srv.KeysErr.Store(false)
	clock.Advance(15 * time.Second)
	tokUnknownHealthy := signRSAToken(t, otherKey, "unknown-kid-healthy", claims)
	_, err = verifier.Verify(context.Background(), tokUnknownHealthy)
	if err == nil {
		t.Fatal("expected error with unknown kid on healthy keys")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid with unknown kid on healthy keys", code)
	}
}

func TestVerifierKeyOutageOnSeparateHost(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	keysMux := http.NewServeMux()
	keysMux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "jwks outage", http.StatusInternalServerError)
	})
	keysServer := httptest.NewServer(keysMux)
	defer keysServer.Close()

	var discServer *httptest.Server
	discServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                discServer.URL,
			"jwks_uri":                              keysServer.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	}))
	defer discServer.Close()

	now := time.Now().Truncate(time.Second)
	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   discServer.URL,
				Audience: "flowseer-device",
			},
		},
		Client: discServer.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": discServer.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	}
	tok := signRSAToken(t, otherKey, "unknown-kid", claims)
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error on separate host key outage")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable when separate host jwks answers 500", code)
	}
}

func TestVerifierDiscoveryFailureRecoveryAndTimeout(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)
	clock := newSettableClock(now)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.Server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.Server.Client(),
		Clock:  clock.Now,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, claims)

	// Discovery that fails returns Unavailable.
	srv.DiscoveryErr.Store(true)
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected discovery error")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable on discovery failure", code)
	}

	// Server recovers, but within replay window cached error remains.
	srv.DiscoveryErr.Store(false)
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected still cached discovery error within replay window")
	}

	// Advance past replay window: discovers and verifies successfully.
	clock.Advance(15 * time.Second)
	p, err := verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify after recovery and replay window: %v", err)
	}
	if p.Subject != "u1" {
		t.Fatalf("got subject %q, want u1", p.Subject)
	}

	// Hanging server yields Unavailable once client timeout passes.
	// The handler answers nothing until the client gives up or the test ends.
	releaseHanging := make(chan struct{})
	hangingServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-releaseHanging:
		}
	}))
	defer hangingServer.Close()
	defer close(releaseHanging)

	slowClient := &http.Client{Timeout: 20 * time.Millisecond}
	hangingVerifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{Issuer: hangingServer.URL, Audience: "aud"},
		},
		Client: slowClient,
	})
	if err != nil {
		t.Fatalf("NewVerifier hanging: %v", err)
	}

	hangingTok := signRSAToken(t, srv.RSAKey, srv.RSAKID, map[string]any{
		"iss": hangingServer.URL,
		"aud": "aud",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	})
	_, err = hangingVerifier.Verify(context.Background(), hangingTok)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable on timeout", code)
	}
	if !errs.Retryable(err) {
		t.Fatalf("discovery timeout must be retryable: %v", err)
	}

	// The caller's context is live, so the client's timeout stays in the text
	// and out of the chain.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("live caller's discovery timeout carries a context error in its chain: %v", err)
	}
	if !strings.Contains(err.Error(), "Client.Timeout") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("discovery timeout lost its text: %v", err)
	}

	// The replay cache hands the same failure to the next caller in the window.
	_, err = hangingVerifier.Verify(context.Background(), hangingTok)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("replayed discovery timeout carries a context error in its chain: %v", err)
	}
	if code, ok := errs.CodeOf(err); !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("replayed discovery timeout got code %v, want authn/unavailable (err: %v)", code, err)
	}
}

// panicOnceTransport panics on its first round trip and delegates afterward.
type panicOnceTransport struct {
	base     http.RoundTripper
	panicked atomic.Bool
}

func (p *panicOnceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.panicked.CompareAndSwap(false, true) {
		panic("transport panicked on the discovery request")
	}
	return p.base.RoundTrip(req)
}

func TestVerifierDiscoveryPanicReleasesWaiters(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	})

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{{Issuer: srv.Server.URL, Audience: "flowseer-device"}},
		Client:  &http.Client{Transport: &panicOnceTransport{base: srv.Server.Client().Transport}},
		Clock:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// The deadline only bounds a failing run: a waiter that is never released
	// returns the context's error instead of a coded outage.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = verifier.Verify(ctx, tok)
	if code, ok := errs.CodeOf(err); !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable after a panic during discovery (err: %v)", code, err)
	}
	if !errs.Retryable(err) {
		t.Fatalf("panic during discovery must be retryable: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("waiter was held until its deadline: %v", ctx.Err())
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("live caller's result carries a context error in its chain: %v", err)
	}

	// The panic left no inflight discovery behind, so the next caller discovers.
	p, err := verifier.Verify(ctx, tok)
	if err != nil {
		t.Fatalf("verify after the panicked discovery: %v", err)
	}
	if p.Subject != "u1" {
		t.Fatalf("got subject %q, want u1", p.Subject)
	}
}

// TestVerifierDiscoveryClockPanicLeavesNoLockHeld panics the caller-supplied
// clock at the replay transport's cache check, where the transport must not
// hold its lock while it runs caller code.
func TestVerifierDiscoveryClockPanicLeavesNoLockHeld(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)
	clock := newSettableClock(now)
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	})

	var panicNext atomic.Bool
	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{{Issuer: srv.Server.URL, Audience: "flowseer-device"}},
		Client:  srv.Server.Client(),
		Clock: func() time.Time {
			if panicNext.CompareAndSwap(true, false) {
				panic("clock panicked during the replay transport's cache check")
			}
			return clock.Now()
		},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The failed discovery lands in the replay cache, so the next attempt's
	// cache check finds an entry and reads the clock to age it.
	srv.DiscoveryErr.Store(true)
	if _, err := verifier.Verify(ctx, tok); err == nil {
		t.Fatal("expected the failed discovery to be refused")
	}

	panicNext.Store(true)
	_, err = verifier.Verify(ctx, tok)
	if code, ok := errs.CodeOf(err); !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable after the clock panicked (err: %v)", code, err)
	}
	if panicNext.Load() {
		t.Fatal("the clock was never read, so the panic did not fire")
	}

	// The endpoint recovers and the failure leaves the window. A transport that
	// kept its lock held would block this discovery until the deadline.
	srv.DiscoveryErr.Store(false)
	clock.Advance(15 * time.Second)
	if _, err := verifier.Verify(ctx, tok); err != nil {
		t.Fatalf("verify after the clock panic: %v", err)
	}
}

type requestScopedKey struct{}

// contextValueTransport records whether a request's context carries the value
// a caller attached to its own context.
type contextValueTransport struct {
	base   http.RoundTripper
	leaked atomic.Bool
}

func (c *contextValueTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Context().Value(requestScopedKey{}) != nil {
		c.leaked.Store(true)
	}
	return c.base.RoundTrip(req)
}

func TestVerifierDiscoveryDoesNotInheritRequestScope(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	})

	transport := &contextValueTransport{base: srv.Server.Client().Transport}
	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{{Issuer: srv.Server.URL, Audience: "flowseer-device"}},
		Client:  &http.Client{Transport: transport},
		Clock:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// The discovery every waiter shares outlives the caller that started it, so
	// the caller's span and values must not ride along.
	ctx := context.WithValue(context.Background(), requestScopedKey{}, "the leader's span")
	if _, err := verifier.Verify(ctx, tok); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if transport.leaked.Load() {
		t.Fatal("a request-scoped value reached the shared discovery request")
	}
}

func TestVerifierEndedContextReturnsBare(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.Server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.Server.Client(),
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	}
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, claims)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = verifier.Verify(ctx, tok)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled bare, got: %v", err)
	}
}

func TestVerifierCallerCancellationNotCached(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	rsaKID := "test-rsa-key-1"

	var cancelFirstRequest atomic.Pointer[context.CancelFunc]
	var discCalls atomic.Int64
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			n := discCalls.Add(1)
			if n == 1 {
				if cancel := cancelFirstRequest.Load(); cancel != nil {
					(*cancel)()
					time.Sleep(50 * time.Millisecond)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                srv.URL,
				"jwks_uri":                              srv.URL + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
				"response_types_supported":              []string{"id_token"},
				"subject_types_supported":               []string{"public"},
			})
		case "/keys":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"kty": "RSA",
						"kid": rsaKID,
						"use": "sig",
						"alg": "RS256",
						"n":   base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
						"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes()),
					},
				},
			})
		}
	}))
	defer srv.Close()

	now := time.Now().Truncate(time.Second)
	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	}
	tok := signRSAToken(t, rsaKey, rsaKID, claims)

	// Caller drops connection during discovery.
	callerCtx, cancel := context.WithCancel(context.Background())
	cancelFirstRequest.Store(&cancel)
	_, err = verifier.Verify(callerCtx, tok)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled bare, got %v", err)
	}

	// Subsequent caller with live context must not receive a cached canceled error.
	p, err := verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("expected successful verification after prior canceled caller, got %v", err)
	}
	if p.Subject != "u1" {
		t.Fatalf("got subject %q, want u1", p.Subject)
	}
}

func TestVerifierConcurrentDiscoverySingleFlight(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.Server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.Server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	}
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, claims)

	const concurrency = 10
	errCh := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			_, err := verifier.Verify(context.Background(), tok)
			errCh <- err
		}()
	}

	for i := 0; i < concurrency; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent verify failed: %v", err)
		}
	}

	if discCount := srv.DiscoveryFetchCount.Load(); discCount != 1 {
		t.Fatalf("expected exactly 1 discovery request, got %d", discCount)
	}
}

func TestVerifierInjectedClockDecidesExpiry(t *testing.T) {
	srv := authntest.New(t)
	realNow := time.Now().Truncate(time.Second)

	tokenExp := realNow.Add(30 * time.Minute)
	claims := map[string]any{
		"iss": srv.Server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": tokenExp.Unix(),
	}
	tok := signRSAToken(t, srv.RSAKey, srv.RSAKID, claims)

	injectedClock := func() time.Time {
		return realNow.Add(time.Hour)
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.Server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.Server.Client(),
		Clock:  injectedClock,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected expired token error with injected clock")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenExpired {
		t.Fatalf("got code %v, want authn/token-expired (err: %v)", code, err)
	}
}

// endpointBehavior is what one of the issuer's endpoints does with a request.
type endpointBehavior string

const (
	behaviorAnswersCorrectly endpointBehavior = "answers-correctly"
	behaviorAnswers500       endpointBehavior = "answers-500"
	behaviorClosesConnection endpointBehavior = "closes-connection-without-response"
	behaviorNeverAnswers     endpointBehavior = "never-answers"
)

// propertyTokenKind names a token for what it is. Whether the verifier holds
// the key a token needs is a property of the prior state, never of the token.
type propertyTokenKind string

const (
	tokenSignedByServedKey   propertyTokenKind = "signed-by-served-key"
	tokenSignedByUnservedKey propertyTokenKind = "signed-by-unserved-key"
	tokenServedKIDWrongSig   propertyTokenKind = "served-key-id-wrong-signature"
	tokenAudOther            propertyTokenKind = "served-key-aud-other"
	tokenAudFetchingKeys     propertyTokenKind = "served-key-aud-fetching-keys"
	tokenExpired             propertyTokenKind = "served-key-expired"
	tokenMalformed           propertyTokenKind = "malformed"
)

// propertyPriorState is the history a verifier has before the caller under
// test calls it. The states that name a cached key really cache it: they verify
// a token signed by the served key against healthy endpoints first.
type propertyPriorState string

const (
	priorFresh                    propertyPriorState = "fresh-verifier"
	priorKeyCached                propertyPriorState = "key-cached"
	priorKeyFetchFailedInWindow   propertyPriorState = "key-cached-then-key-fetch-failed-in-window"
	priorKeyFetchFailedPastWindow propertyPriorState = "key-cached-then-key-fetch-failed-past-window"
	priorCanceledMidDiscovery     propertyPriorState = "other-caller-canceled-mid-discovery"
	priorCanceledMidKeyFetch      propertyPriorState = "key-cached-then-other-caller-canceled-mid-key-fetch"
)

// cachesKey reports whether the prior state leaves the served key in the
// verifier's key set.
func (p propertyPriorState) cachesKey() bool {
	switch p {
	case priorKeyCached, priorKeyFetchFailedInWindow, priorKeyFetchFailedPastWindow, priorCanceledMidKeyFetch:
		return true
	default:
		return false
	}
}

type propertyCallerContext string

const (
	ctxLive           propertyCallerContext = "live"
	ctxCanceledBefore propertyCallerContext = "canceled-before-call"
	ctxCanceledMidReq propertyCallerContext = "canceled-mid-request"
)

// propertyRow is one combination of the matrix.
type propertyRow struct {
	disc  endpointBehavior
	keys  endpointBehavior
	tok   propertyTokenKind
	prior propertyPriorState
	ctx   propertyCallerContext
}

func (r propertyRow) name() string {
	return fmt.Sprintf("disc=%s,keys=%s,tok=%s,prior=%s,ctx=%s", r.disc, r.keys, r.tok, r.prior, r.ctx)
}

// noRequestRules lists the token and prior-state combinations whose
// verification sends no request to either endpoint, so a caller canceled
// "mid-request" has no request to be canceled in. The live-context rows assert
// the claim by counting the requests the endpoints receive.
var noRequestRules = []struct {
	priors []propertyPriorState
	tokens []propertyTokenKind
	reason string
}{
	{
		priors: []propertyPriorState{
			priorFresh, priorKeyCached, priorKeyFetchFailedInWindow,
			priorKeyFetchFailedPastWindow, priorCanceledMidDiscovery, priorCanceledMidKeyFetch,
		},
		tokens: []propertyTokenKind{tokenMalformed},
		reason: "the token is refused before any request is sent",
	},
	{
		priors: []propertyPriorState{
			priorKeyCached, priorKeyFetchFailedInWindow, priorKeyFetchFailedPastWindow, priorCanceledMidKeyFetch,
		},
		tokens: []propertyTokenKind{tokenSignedByServedKey, tokenAudOther, tokenAudFetchingKeys, tokenExpired},
		reason: "the cached key verifies the signature, so no request is sent",
	},
	{
		priors: []propertyPriorState{priorKeyFetchFailedInWindow},
		tokens: []propertyTokenKind{tokenSignedByUnservedKey, tokenServedKIDWrongSig},
		reason: "the key fetch is replayed from the failure the replay window holds, so no request reaches the endpoint",
	},
}

// noRequestReason returns why a verification sends no request, or "" when it
// sends at least one.
func noRequestReason(prior propertyPriorState, tok propertyTokenKind) string {
	for _, rule := range noRequestRules {
		if slices.Contains(rule.priors, prior) && slices.Contains(rule.tokens, tok) {
			return rule.reason
		}
	}
	return ""
}

// propertySigning holds the keys every row shares: one the issuer serves and
// one it does not.
type propertySigning struct {
	served    *rsa.PrivateKey
	unserved  *rsa.PrivateKey
	servedKID string
	otherKID  string
}

func newPropertySigning(t *testing.T) *propertySigning {
	t.Helper()

	served, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate served rsa key: %v", err)
	}
	unserved, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate unserved rsa key: %v", err)
	}

	return &propertySigning{
		served:    served,
		unserved:  unserved,
		servedKID: "test-rsa-key-1",
		otherKID:  "test-unserved-kid-1",
	}
}

func (s *propertySigning) token(t *testing.T, kind propertyTokenKind, issuerURL string, now time.Time) string {
	t.Helper()

	baseClaims := map[string]any{
		"iss": issuerURL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}

	switch kind {
	case tokenSignedByServedKey:
		return signRSAToken(t, s.served, s.servedKID, baseClaims)
	case tokenSignedByUnservedKey:
		return signRSAToken(t, s.unserved, s.otherKID, baseClaims)
	case tokenServedKIDWrongSig:
		return signRSAToken(t, s.unserved, s.servedKID, baseClaims)
	case tokenAudOther:
		c := cloneMap(baseClaims)
		c["aud"] = "other"
		return signRSAToken(t, s.served, s.servedKID, c)
	case tokenAudFetchingKeys:
		c := cloneMap(baseClaims)
		c["aud"] = "aud with fetching keys text"
		return signRSAToken(t, s.served, s.servedKID, c)
	case tokenExpired:
		c := cloneMap(baseClaims)
		c["exp"] = now.Add(-time.Hour).Unix()
		return signRSAToken(t, s.served, s.servedKID, c)
	case tokenMalformed:
		return "header.malformed.payload"
	default:
		t.Fatalf("unhandled token kind: %v", kind)
		return ""
	}
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// propertyEndpoint is one endpoint of a row's issuer.
type propertyEndpoint struct {
	env      *propertyEnv
	behavior atomic.Pointer[endpointBehavior]
	hook     atomic.Pointer[func()]
	answer   func(w http.ResponseWriter)
}

func (e *propertyEndpoint) set(b endpointBehavior) {
	e.behavior.Store(&b)
}

func (e *propertyEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.env.requests.Add(1)

	if hook := e.hook.Load(); hook != nil {
		(*hook)()
	}
	if gate := e.env.gate.Load(); gate != nil {
		gate.onRequest(r.Context(), e.env.release)
	}

	switch *e.behavior.Load() {
	case behaviorAnswersCorrectly:
		e.answer(w)
	case behaviorAnswers500:
		http.Error(w, "endpoint answers 500", http.StatusInternalServerError)
	case behaviorClosesConnection:
		hj, ok := w.(http.Hijacker)
		if !ok {
			panic("response writer is not an http.Hijacker")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			panic(err)
		}
		_ = conn.Close()
	case behaviorNeverAnswers:
		select {
		case <-r.Context().Done():
		case <-e.env.release:
		}
	}
}

// keyRefreshFrame is the frame of go-oidc's key-set refresh goroutine.
const keyRefreshFrame = "oidc.(*RemoteKeySet).keysFromRemote.func1"

// keyRefreshInFlight reports whether any goroutine is inside go-oidc's key-set
// refresh.
func keyRefreshInFlight() bool {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return bytes.Contains(buf[:n], []byte(keyRefreshFrame))
		}
		buf = make([]byte, 2*len(buf))
	}
}

// waitForKeyRefreshToFinish blocks until no key-set refresh goroutine is left.
// That goroutine wakes its waiters before it clears its own in-flight record
// (oidc@v3.21.0/oidc/jwks.go:209-226), so a fetch that starts in the gap is
// answered with the finished result and sends no request. A harness that needs
// its next fetch to reach the endpoint waits here after the previous one.
func waitForKeyRefreshToFinish(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for keyRefreshInFlight() {
		if time.Now().After(deadline) {
			t.Fatal("go-oidc key-set refresh goroutine did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestKeyRefreshWitnessSeesTheRefreshGoroutine(t *testing.T) {
	signing := newPropertySigning(t)
	env := newPropertyEnv(t, signing)
	env.keys.set(behaviorNeverAnswers)

	keySet := oidc.NewRemoteKeySet(context.Background(), env.keysURL)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once sync.Once
	hook := func() { once.Do(func() { close(started) }) }
	env.keys.hook.Store(&hook)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = keySet.VerifySignature(ctx, signing.token(t, tokenSignedByServedKey, env.discURL, time.Now()))
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("key request never reached the endpoint")
	}
	if !keyRefreshInFlight() {
		t.Fatalf("no goroutine in frame %q while a key fetch is in flight: go-oidc renamed it, so the settle wait no longer waits", keyRefreshFrame)
	}

	cancel()
	<-done
	env.releaseHandlers()
	waitForKeyRefreshToFinish(t)
}

// propertyEnv is the issuer, clock, and transport of one row. Each row owns its
// own, so a handler left running by one row cannot reach the next.
type propertyEnv struct {
	disc      *propertyEndpoint
	keys      *propertyEndpoint
	discURL   string
	keysURL   string
	clock     *settableClock
	transport *http.Transport
	requests  atomic.Int64
	gate      atomic.Pointer[cancelGate]

	// release is closed when the row ends and frees every handler that waits.
	release     chan struct{}
	releaseOnce sync.Once
}

func (env *propertyEnv) releaseHandlers() {
	env.releaseOnce.Do(func() { close(env.release) })
}

func newPropertyEnv(t *testing.T, signing *propertySigning) *propertyEnv {
	t.Helper()

	env := &propertyEnv{
		clock:     newSettableClock(time.Time{}),
		transport: &http.Transport{},
		release:   make(chan struct{}),
	}
	t.Cleanup(env.transport.CloseIdleConnections)

	env.disc = &propertyEndpoint{env: env, answer: func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                env.discURL,
			"jwks_uri":                              env.keysURL,
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	}}
	env.keys = &propertyEndpoint{env: env, answer: func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": signing.servedKID,
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(signing.served.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signing.served.E)).Bytes()),
				},
			},
		})
	}}
	env.disc.set(behaviorAnswersCorrectly)
	env.keys.set(behaviorAnswersCorrectly)

	discServer := httptest.NewServer(env.disc)
	t.Cleanup(discServer.Close)
	keysServer := httptest.NewServer(env.keys)
	t.Cleanup(keysServer.Close)
	env.discURL = discServer.URL
	env.keysURL = keysServer.URL + "/keys"

	// Cleanups run last in, first out: the release must come before the
	// servers close, because Close waits for every handler to return.
	t.Cleanup(env.releaseHandlers)

	return env
}

// cancelGate fires the caller's cancel from inside an endpoint handler, so the
// cancel lands while the request the caller waits on is in flight. The handler
// that claims the gate holds its response until the caller's Verify has
// returned, so the response cannot race the cancel.
type cancelGate struct {
	cancel context.CancelFunc

	// armed is closed by the test just before the caller calls Verify.
	armed chan struct{}
	// waiting is closed when the verifier first asks the caller's context for
	// its Done channel, which it does only to wait on a request.
	waiting chan struct{}
	// returned is closed by the test once the caller's Verify has returned.
	returned chan struct{}

	claimed atomic.Bool
	fired   atomic.Bool
}

func newCancelGate(cancel context.CancelFunc) *cancelGate {
	return &cancelGate{
		cancel:   cancel,
		armed:    make(chan struct{}),
		waiting:  make(chan struct{}),
		returned: make(chan struct{}),
	}
}

func (g *cancelGate) onRequest(reqCtx context.Context, release <-chan struct{}) {
	if !g.claimed.CompareAndSwap(false, true) {
		return
	}

	for _, stage := range []<-chan struct{}{g.armed, g.waiting} {
		select {
		case <-stage:
		case <-reqCtx.Done():
			return
		case <-release:
			return
		}
	}

	// The flag goes up before the cancel: the caller returns as soon as it
	// sees the cancel and reads the flag.
	g.fired.Store(true)
	g.cancel()

	select {
	case <-g.returned:
	case <-reqCtx.Done():
	case <-release:
	}
}

// watchedContext tells the gate when the verifier starts waiting on it.
type watchedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *watchedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type propertyExpectedResult struct {
	errCode    errs.Code
	retryable  bool
	contextErr error
	success    bool
}

func propertyExpectedOutcome(row propertyRow) propertyExpectedResult {
	unavailable := propertyExpectedResult{errCode: authn.ErrCodeUnavailable, retryable: true}

	if row.ctx == ctxCanceledBefore || row.ctx == ctxCanceledMidReq {
		return propertyExpectedResult{contextErr: context.Canceled}
	}

	if row.tok == tokenMalformed {
		return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
	}

	if !row.prior.cachesKey() {
		if row.disc != behaviorAnswersCorrectly || row.keys != behaviorAnswersCorrectly {
			return unavailable
		}
	}

	switch row.tok {
	case tokenSignedByServedKey:
		return propertyExpectedResult{success: true}
	case tokenAudOther, tokenAudFetchingKeys:
		return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
	case tokenExpired:
		return propertyExpectedResult{errCode: authn.ErrCodeTokenExpired}
	}

	// A token the cached key set cannot verify needs a key fetch.
	if !row.prior.cachesKey() {
		return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
	}
	if row.prior == priorKeyFetchFailedInWindow || row.keys != behaviorAnswersCorrectly {
		return unavailable
	}

	return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
}

func checkCaseResult(t *testing.T, expected propertyExpectedResult, p authn.Principal, err error) {
	t.Helper()

	if expected.contextErr != nil {
		if !errors.Is(err, expected.contextErr) {
			t.Fatalf("want context error %v bare, got %v", expected.contextErr, err)
		}
		if err != context.Canceled && err != context.DeadlineExceeded {
			t.Fatalf("want bare context error, got %T: %v", err, err)
		}
		return
	}

	if expected.success {
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if p.Subject != "u1" {
			t.Fatalf("got subject %q, want u1", p.Subject)
		}
		return
	}

	if err == nil {
		t.Fatalf("expected error %v, got success (principal: %+v)", expected.errCode, p)
	}

	code, ok := errs.CodeOf(err)
	if !ok || code != expected.errCode {
		t.Fatalf("got code %v, want %v (err: %v)", code, expected.errCode, err)
	}

	if expected.retryable && !errs.Retryable(err) {
		t.Fatalf("expected retryable error, got non-retryable: %v", err)
	}
	if !expected.retryable && errs.Retryable(err) {
		t.Fatalf("expected non-retryable error, got retryable: %v", err)
	}

	// The caller's own context is live, so neither a cancellation nor a
	// deadline may appear in the chain. An endpoint's timeout is a fact about
	// the endpoint, and the replay cache hands it to every caller in the window.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live caller's result carries a context error in its chain: %v", err)
	}
}

// startCanceledLeader starts a verification whose context ends when its request
// reaches ep, and returns a channel closed once that verification has returned.
// The request stays in flight for the caller under test to join.
func (env *propertyEnv) startCanceledLeader(t *testing.T, v *authn.Verifier, token string, ep *propertyEndpoint, gate *cancelGate) <-chan struct{} {
	t.Helper()

	started := make(chan struct{})
	done := make(chan struct{})
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	t.Cleanup(cancelLeader)

	var once sync.Once
	hook := func() {
		once.Do(func() {
			cancelLeader()
			close(started)
		})
	}
	ep.hook.Store(&hook)
	if gate != nil {
		env.gate.Store(gate)
	}

	go func() {
		defer close(done)
		_, _ = v.Verify(leaderCtx, token)
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("leader's request never reached the endpoint")
	}
	ep.hook.Store(nil)

	return done
}

// run executes the row and reports whether the caller's cancel was fired from
// an endpoint handler.
func (r propertyRow) run(t *testing.T, signing *propertySigning) (cancelFired bool) {
	t.Helper()

	env := newPropertyEnv(t, signing)
	t0 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	env.clock.Set(t0)

	// A row that waits on an endpoint that never answers waits for the client
	// timeout, so it gets a short one. A canceled-mid-request row never waits
	// for it: the gate holds the request open until the caller is gone. Every
	// other row gets a long timeout, so a stalled test process cannot turn an
	// answer into a timeout.
	timeout := 2 * time.Second
	if r.ctx != ctxCanceledMidReq && (r.disc == behaviorNeverAnswers || r.keys == behaviorNeverAnswers) {
		timeout = 200 * time.Millisecond
	}

	v, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{{Issuer: env.discURL, Audience: "flowseer-device"}},
		Client:  &http.Client{Transport: env.transport, Timeout: timeout},
		Clock:   env.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	servedTok := signing.token(t, tokenSignedByServedKey, env.discURL, t0)
	unservedTok := signing.token(t, tokenSignedByUnservedKey, env.discURL, t0)
	testToken := signing.token(t, r.tok, env.discURL, t0)

	var callCtx context.Context
	var gate *cancelGate
	switch r.ctx {
	case ctxLive:
		callCtx = context.Background()
	case ctxCanceledBefore:
		c, cancel := context.WithCancel(context.Background())
		cancel()
		callCtx = c
	case ctxCanceledMidReq:
		c, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		gate = newCancelGate(cancel)
		callCtx = &watchedContext{Context: c, waiting: gate.waiting}
	}

	cacheKey := func() {
		if _, err := v.Verify(context.Background(), servedTok); err != nil {
			t.Fatalf("setup: verify token signed by the served key: %v", err)
		}
		waitForKeyRefreshToFinish(t)
		env.clock.Advance(15 * time.Second)
	}
	failKeyFetch := func() {
		env.keys.set(behaviorAnswers500)
		_, err := v.Verify(context.Background(), unservedTok)
		if code, _ := errs.CodeOf(err); code != authn.ErrCodeUnavailable {
			t.Fatalf("setup: key fetch against a 500 endpoint gave %v, want authn/unavailable", err)
		}
		waitForKeyRefreshToFinish(t)
	}
	useRowBehaviors := func() {
		env.disc.set(r.disc)
		env.keys.set(r.keys)
	}

	var leaderDone <-chan struct{}
	switch r.prior {
	case priorFresh:
	case priorKeyCached:
		cacheKey()
	case priorKeyFetchFailedInWindow:
		cacheKey()
		failKeyFetch()
	case priorKeyFetchFailedPastWindow:
		cacheKey()
		failKeyFetch()
		env.clock.Advance(15 * time.Second)
	case priorCanceledMidDiscovery:
		useRowBehaviors()
		leaderDone = env.startCanceledLeader(t, v, servedTok, env.disc, gate)
	case priorCanceledMidKeyFetch:
		cacheKey()
		useRowBehaviors()
		leaderDone = env.startCanceledLeader(t, v, unservedTok, env.keys, gate)
	}
	useRowBehaviors()

	if gate != nil {
		env.gate.Store(gate)
		close(gate.armed)
	}

	requestsBefore := env.requests.Load()
	p, callErr := v.Verify(callCtx, testToken)
	if gate != nil {
		close(gate.returned)
	}
	if leaderDone != nil {
		<-leaderDone
	}

	// A canceled leader's own request can still be retried by the transport
	// after the call returns, when the endpoint closes its connection, so the
	// count is asserted only where no other request is in flight.
	if reason := noRequestReason(r.prior, r.tok); reason != "" && r.ctx == ctxLive && leaderDone == nil {
		if sent := env.requests.Load() - requestsBefore; sent != 0 {
			t.Errorf("verification sent %d request(s), want none: %s", sent, reason)
		}
	}

	checkCaseResult(t, propertyExpectedOutcome(r), p, callErr)

	cancelFired = gate != nil && gate.fired.Load()
	if r.ctx == ctxCanceledMidReq && !cancelFired {
		t.Errorf("no endpoint handler fired the caller's cancel, so the row did not cancel mid-request")
	}

	return cancelFired
}

func TestVerifierOutageClassificationProperty(t *testing.T) {
	signing := newPropertySigning(t)

	endpointBehaviors := []endpointBehavior{
		behaviorAnswersCorrectly,
		behaviorAnswers500,
		behaviorClosesConnection,
		behaviorNeverAnswers,
	}

	tokens := []propertyTokenKind{
		tokenSignedByServedKey,
		tokenSignedByUnservedKey,
		tokenServedKIDWrongSig,
		tokenAudOther,
		tokenAudFetchingKeys,
		tokenExpired,
		tokenMalformed,
	}

	priorStates := []propertyPriorState{
		priorFresh,
		priorKeyCached,
		priorKeyFetchFailedInWindow,
		priorKeyFetchFailedPastWindow,
		priorCanceledMidDiscovery,
		priorCanceledMidKeyFetch,
	}

	callerContexts := []propertyCallerContext{
		ctxLive,
		ctxCanceledBefore,
		ctxCanceledMidReq,
	}

	// The expected counts are written out. Each is the product of the axes
	// above, so an axis value deleted or added changes the rows that run and
	// fails here until the number is updated on purpose:
	// 4 disc * 4 keys * 7 tokens * 6 prior states * 3 contexts = 2016 rows,
	// of which 24 token/prior combinations * 16 endpoint pairs = 384 are
	// canceled-mid-request rows with no request to cancel in, leaving 1632.
	// The other 18 * 16 = 288 canceled-mid-request rows each fire the cancel
	// from an endpoint handler.
	const (
		wantRowsRun      = 1632
		wantImpossible   = 384
		wantCancelsFired = 288
	)

	impossibleCases := map[string]string{}
	for _, rule := range noRequestRules {
		for _, prior := range rule.priors {
			for _, tok := range rule.tokens {
				for _, disc := range endpointBehaviors {
					for _, keys := range endpointBehaviors {
						row := propertyRow{disc: disc, keys: keys, tok: tok, prior: prior, ctx: ctxCanceledMidReq}
						impossibleCases[row.name()] = rule.reason
					}
				}
			}
		}
	}
	if len(impossibleCases) != wantImpossible {
		t.Fatalf("listed %d impossible combinations, want %d", len(impossibleCases), wantImpossible)
	}

	runCount := 0
	skippedCount := 0
	cancelsFired := 0

	for _, disc := range endpointBehaviors {
		for _, keys := range endpointBehaviors {
			for _, tok := range tokens {
				for _, prior := range priorStates {
					for _, ctxKind := range callerContexts {
						row := propertyRow{disc: disc, keys: keys, tok: tok, prior: prior, ctx: ctxKind}
						if _, impossible := impossibleCases[row.name()]; impossible {
							skippedCount++
							continue
						}
						runCount++

						t.Run(row.name(), func(t *testing.T) {
							if row.run(t, signing) {
								cancelsFired++
							}
						})
					}
				}
			}
		}
	}

	if skippedCount != len(impossibleCases) {
		t.Errorf("matrix holds %d of the %d listed impossible combinations", skippedCount, len(impossibleCases))
	}
	if runCount != wantRowsRun {
		t.Errorf("ran %d rows, want %d", runCount, wantRowsRun)
	}
	if cancelsFired != wantCancelsFired {
		t.Errorf("fired the caller's cancel from an endpoint handler in %d rows, want %d", cancelsFired, wantCancelsFired)
	}
}

func TestRequirement11(t *testing.T) {
	srv := authntest.New(t)
	now := time.Now().Truncate(time.Second)

	t.Run("subject length 256 accepted and 257 refused", func(t *testing.T) {
		verifier, err := authn.NewVerifier(authn.Options{
			Issuers: []authn.IssuerConfig{
				{
					Issuer:   srv.URL(),
					Audience: "flowseer-device",
				},
			},
			Client: srv.Client(),
			Clock:  func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}

		sub256 := strings.Repeat("a", 256)
		tok256 := srv.Sign(map[string]any{
			"iss": srv.URL(),
			"aud": "flowseer-device",
			"sub": sub256,
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		})
		p, err := verifier.Verify(context.Background(), tok256)
		if err != nil {
			t.Fatalf("verify 256-char subject token: %v", err)
		}
		if p.Subject != sub256 {
			t.Fatalf("got subject len %d, want 256", len(p.Subject))
		}

		sub257 := strings.Repeat("a", 257)
		tok257 := srv.Sign(map[string]any{
			"iss": srv.URL(),
			"aud": "flowseer-device",
			"sub": sub257,
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		})
		_, err = verifier.Verify(context.Background(), tok257)
		if err == nil {
			t.Fatal("expected error for 257-char subject, got nil")
		}
		code, ok := errs.CodeOf(err)
		if !ok || code != authn.ErrCodeTokenInvalid {
			t.Fatalf("got code %v, want %v", code, authn.ErrCodeTokenInvalid)
		}
	})

	t.Run("suspended tenant yields no tenant beside active", func(t *testing.T) {
		activeRec := identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-active")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.URL()),
				OrganizationClaimName:  proto.String("org"),
				OrganizationClaimValue: proto.String("active-org"),
			}.Build(),
			State: identityv1.TenantState_builder{
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
		}.Build()

		suspendedRec := identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-suspended")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.URL()),
				OrganizationClaimName:  proto.String("org"),
				OrganizationClaimValue: proto.String("suspended-org"),
			}.Build(),
			State: identityv1.TenantState_builder{
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_SUSPENDED.Enum(),
			}.Build(),
		}.Build()

		resolver := func(_ context.Context, _, org string) (*identityv1.TenantRecord, error) {
			switch org {
			case "active-org":
				return activeRec, nil
			case "suspended-org":
				return suspendedRec, nil
			default:
				return nil, nil
			}
		}

		verifier, err := authn.NewVerifier(authn.Options{
			Issuers: []authn.IssuerConfig{
				{
					Issuer:                srv.URL(),
					Audience:              "flowseer-device",
					OrganizationClaimName: "org",
				},
			},
			Resolver: resolver,
			Client:   srv.Client(),
			Clock:    func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}

		tokActive := srv.Sign(map[string]any{
			"iss": srv.URL(),
			"aud": "flowseer-device",
			"sub": "u1",
			"org": "active-org",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		})
		pActive, err := verifier.Verify(context.Background(), tokActive)
		if err != nil {
			t.Fatalf("verify active tenant token: %v", err)
		}
		if len(pActive.Tenants) != 1 || pActive.Tenants[0] != "tenant-active" {
			t.Fatalf("got tenants %v, want ['tenant-active']", pActive.Tenants)
		}

		tokSuspended := srv.Sign(map[string]any{
			"iss": srv.URL(),
			"aud": "flowseer-device",
			"sub": "u1",
			"org": "suspended-org",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		})
		pSuspended, err := verifier.Verify(context.Background(), tokSuspended)
		if err != nil {
			t.Fatalf("verify suspended tenant token: %v", err)
		}
		if len(pSuspended.Tenants) != 0 {
			t.Fatalf("got tenants %v, want empty for suspended tenant", pSuspended.Tenants)
		}
	})

	t.Run("key endpoint answering 200 not json yields unavailable beside empty key set token invalid", func(t *testing.T) {
		// Key endpoint returns 200 and "not json" -> authn/unavailable
		keyIssNotJSON := authntest.New(t)
		tokNotJSON := keyIssNotJSON.Sign(map[string]any{
			"iss": keyIssNotJSON.URL(),
			"aud": "flowseer-device",
			"sub": "u1",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		})

		vNotJSON, err := authn.NewVerifier(authn.Options{
			Issuers: []authn.IssuerConfig{
				{
					Issuer:   keyIssNotJSON.URL(),
					Audience: "flowseer-device",
				},
			},
			Client: keyIssNotJSON.Client(),
			Clock:  func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}

		keyIssNotJSON.SetKeyResponse(http.StatusOK, "text/plain", []byte("not json"))
		_, err = vNotJSON.Verify(context.Background(), tokNotJSON)
		if err == nil {
			t.Fatal("expected error for not json key endpoint, got nil")
		}
		code, ok := errs.CodeOf(err)
		if !ok || code != authn.ErrCodeUnavailable {
			t.Fatalf("got code %v, want %v (err: %v)", code, authn.ErrCodeUnavailable, err)
		}

		// Key endpoint returns 200 with empty key set -> authn/token-invalid
		keyIssEmpty := authntest.New(t)
		tokEmpty := keyIssEmpty.Sign(map[string]any{
			"iss": keyIssEmpty.URL(),
			"aud": "flowseer-device",
			"sub": "u1",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		})

		vEmpty, err := authn.NewVerifier(authn.Options{
			Issuers: []authn.IssuerConfig{
				{
					Issuer:   keyIssEmpty.URL(),
					Audience: "flowseer-device",
				},
			},
			Client: keyIssEmpty.Client(),
			Clock:  func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}

		keyIssEmpty.SetKeyResponse(http.StatusOK, "application/json", []byte(`{"keys":[]}`))
		_, err = vEmpty.Verify(context.Background(), tokEmpty)
		if err == nil {
			t.Fatal("expected error for empty key set, got nil")
		}
		code, ok = errs.CodeOf(err)
		if !ok || code != authn.ErrCodeTokenInvalid {
			t.Fatalf("got code %v, want %v (err: %v)", code, authn.ErrCodeTokenInvalid, err)
		}
	})

	t.Run("platform claim name empty beside set", func(t *testing.T) {
		tok := srv.Sign(map[string]any{
			"iss":   srv.URL(),
			"aud":   "flowseer-device",
			"sub":   "u1",
			"roles": "platform-ops",
			"exp":   now.Add(time.Hour).Unix(),
			"iat":   now.Unix(),
		})

		// ClaimName set: Platform is true
		vSet, err := authn.NewVerifier(authn.Options{
			Issuers: []authn.IssuerConfig{
				{
					Issuer:   srv.URL(),
					Audience: "flowseer-device",
				},
			},
			Platform: authn.PlatformConfig{
				Issuer:       srv.URL(),
				ClaimName:    "roles",
				Organization: "platform-ops",
			},
			Client: srv.Client(),
			Clock:  func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		pSet, err := vSet.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("Verify with claim name set: %v", err)
		}
		if !pSet.Platform {
			t.Fatal("expected Platform true when ClaimName is set to 'roles'")
		}

		// ClaimName empty: Platform is false even though Organization matches
		vEmpty, err := authn.NewVerifier(authn.Options{
			Issuers: []authn.IssuerConfig{
				{
					Issuer:   srv.URL(),
					Audience: "flowseer-device",
				},
			},
			Platform: authn.PlatformConfig{
				Issuer:       srv.URL(),
				ClaimName:    "",
				Organization: "platform-ops",
			},
			Client: srv.Client(),
			Clock:  func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		pEmpty, err := vEmpty.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("Verify with claim name empty: %v", err)
		}
		if pEmpty.Platform {
			t.Fatal("expected Platform false when ClaimName is empty")
		}
	})

	t.Run("BadSigner fails with authn/token-invalid", func(t *testing.T) {
		verifier, err := authn.NewVerifier(authn.Options{
			Issuers: []authn.IssuerConfig{
				{
					Issuer:   srv.URL(),
					Audience: "flowseer-device",
				},
			},
			Client: srv.Client(),
			Clock:  func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		badTok := srv.SignBad(map[string]any{
			"iss": srv.URL(),
			"aud": "flowseer-device",
			"sub": "u1",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		})
		_, err = verifier.Verify(context.Background(), badTok)
		if err == nil {
			t.Fatal("expected error with BadSigner, got nil")
		}
		code, ok := errs.CodeOf(err)
		if !ok || code != authn.ErrCodeTokenInvalid {
			t.Fatalf("got code %v, want %v", code, authn.ErrCodeTokenInvalid)
		}
	})
}
