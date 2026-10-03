package authn_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
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
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

type testOidcServer struct {
	server              *httptest.Server
	rsaKey              *rsa.PrivateKey
	ecKey               *ecdsa.PrivateKey
	rsaKID              string
	ecKID               string
	keyFetchCount       atomic.Int64
	discoveryFetchCount atomic.Int64
	discoveryErr        atomic.Bool
	keysErr             atomic.Bool
}

func newTestOidcServer(t *testing.T) *testOidcServer {
	t.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}

	ts := &testOidcServer{
		rsaKey: rsaKey,
		ecKey:  ecKey,
		rsaKID: "test-rsa-key-1",
		ecKID:  "test-ec-key-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		ts.discoveryFetchCount.Add(1)
		if ts.discoveryErr.Load() {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                ts.server.URL,
			"jwks_uri":                              ts.server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256", "ES256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	})

	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		ts.keyFetchCount.Add(1)
		if ts.keysErr.Load() {
			http.Error(w, "jwks error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": ts.rsaKID,
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes()),
				},
				{
					"kty": "EC",
					"crv": "P-256",
					"kid": ts.ecKID,
					"use": "sig",
					"alg": "ES256",
					"x":   base64.RawURLEncoding.EncodeToString(ecKey.X.Bytes()),
					"y":   base64.RawURLEncoding.EncodeToString(ecKey.Y.Bytes()),
				},
			},
		})
	})

	ts.server = httptest.NewServer(mux)
	t.Cleanup(ts.server.Close)
	return ts
}

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

func TestVerifierAcceptedTokens(t *testing.T) {
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "org",
			},
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	acceptedClaims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}

	// RSA token
	rsaTok := signRSAToken(t, srv.rsaKey, srv.rsaKID, acceptedClaims)
	p, err := verifier.Verify(context.Background(), rsaTok)
	if err != nil {
		t.Fatalf("verify accepted RSA token: %v", err)
	}
	if p.Issuer != srv.server.URL {
		t.Errorf("got Issuer %q, want %q", p.Issuer, srv.server.URL)
	}
	if p.Subject != "u1" {
		t.Errorf("got Subject %q, want u1", p.Subject)
	}
	expectedID := authn.ComputePrincipalID(srv.server.URL, "u1")
	if p.ID != expectedID || len(p.ID) != 64 {
		t.Errorf("got ID %q, want 64-char %q", p.ID, expectedID)
	}

	// ECDSA token
	ecTok := signECDSAToken(t, srv.ecKey, srv.ecKID, acceptedClaims)
	pEC, err := verifier.Verify(context.Background(), ecTok)
	if err != nil {
		t.Fatalf("verify accepted ECDSA token: %v", err)
	}
	if pEC.ID != expectedID || pEC.Subject != "u1" {
		t.Errorf("ECDSA principal mismatch: %+v", pEC)
	}
}

func TestVerifierRefusalCases(t *testing.T) {
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)

	otherRSAKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "org",
			},
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	baseClaims := func() map[string]any {
		return map[string]any{
			"iss": srv.server.URL,
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
				return signRSAToken(t, srv.rsaKey, srv.rsaKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "unknown iss",
			makeTok: func() string {
				c := baseClaims()
				c["iss"] = "https://unknown.issuer.example.com"
				return signRSAToken(t, srv.rsaKey, srv.rsaKID, c)
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
				return signRSAToken(t, srv.rsaKey, srv.rsaKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "empty sub claim",
			makeTok: func() string {
				c := baseClaims()
				c["sub"] = ""
				return signRSAToken(t, srv.rsaKey, srv.rsaKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "sub with null byte",
			makeTok: func() string {
				c := baseClaims()
				c["sub"] = "u1\x00extra"
				return signRSAToken(t, srv.rsaKey, srv.rsaKID, c)
			},
			wantCode: authn.ErrCodeTokenInvalid,
		},
		{
			name: "past exp",
			makeTok: func() string {
				c := baseClaims()
				c["exp"] = now.Add(-10 * time.Minute).Unix()
				return signRSAToken(t, srv.rsaKey, srv.rsaKID, c)
			},
			wantCode: authn.ErrCodeTokenExpired,
		},
		{
			name: "without exp",
			makeTok: func() string {
				c := baseClaims()
				delete(c, "exp")
				return signRSAToken(t, srv.rsaKey, srv.rsaKID, c)
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
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)

	fakeStore := map[string]*identityv1.TenantRecord{
		"acme": identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-A")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.server.URL),
				OrganizationClaimName:  proto.String("organization"),
				OrganizationClaimValue: proto.String("acme"),
			}.Build(),
		}.Build(),
		"globex": identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-B")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.server.URL),
				OrganizationClaimName:  proto.String("organization"),
				OrganizationClaimValue: proto.String("globex"),
			}.Build(),
		}.Build(),
		"other-claim": identityv1.TenantRecord_builder{
			Config: identityv1.TenantConfig_builder{
				Ref: identityv1.TenantGlobalRef_builder{
					Tenant: identityv1.TenantLocalRef_builder{Id: proto.String("tenant-C")}.Build(),
				}.Build(),
				Issuer:                 proto.String(srv.server.URL),
				OrganizationClaimName:  proto.String("groups"),
				OrganizationClaimValue: proto.String("other-claim"),
			}.Build(),
		}.Build(),
	}

	var failStore atomic.Bool
	resolver := func(_ context.Context, issuer, org string) (*identityv1.TenantRecord, error) {
		if failStore.Load() {
			return nil, errors.New("simulated store failure")
		}
		if issuer != srv.server.URL {
			return nil, nil
		}
		return fakeStore[org], nil
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "organization",
			},
		},
		Platform: authn.PlatformConfig{
			Issuer:       srv.server.URL,
			Organization: "platform-ops",
		},
		Resolver: resolver,
		Client:   srv.server.Client(),
		Clock:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	baseClaims := func(orgClaim any) map[string]any {
		m := map[string]any{
			"iss": srv.server.URL,
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
	tok := signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims("acme"))
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
	tok = signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims([]string{"acme", "globex"}))
	p, err = verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify array org: %v", err)
	}
	if !slices.Equal(p.Tenants, []string{"tenant-A", "tenant-B"}) {
		t.Fatalf("got tenants %v, want [tenant-A, tenant-B]", p.Tenants)
	}

	// Object claim: {"acme": {}, "globex": {"roles": []}} -> [tenant-A, tenant-B]
	tok = signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims(map[string]any{"acme": true, "globex": 123}))
	p, err = verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify object org: %v", err)
	}
	if !slices.Equal(p.Tenants, []string{"tenant-A", "tenant-B"}) {
		t.Fatalf("got tenants %v, want [tenant-A, tenant-B]", p.Tenants)
	}

	// Binding under different claim name ("groups") yields none
	tok = signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims("other-claim"))
	p, err = verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify mismatched claim name: %v", err)
	}
	if len(p.Tenants) != 0 {
		t.Fatalf("got tenants %v, want empty", p.Tenants)
	}

	// Store error yields Unavailable with authn/unavailable
	failStore.Store(true)
	tok = signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims("acme"))
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
	tok = signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims(42))
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error for numeric org claim")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid", code)
	}

	// Null organization claim yields authn/token-invalid
	tokNull := signRSAToken(t, srv.rsaKey, srv.rsaKID, map[string]any{
		"iss":          srv.server.URL,
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
	tokNonStr := signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims([]any{"acme", 123}))
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
	tok = signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims(hundredOrgs))
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error for 100 org claim values")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid", code)
	}

	// Platform flag set and unset by one field
	tokPlatform := signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims("platform-ops"))
	pPlat, err := verifier.Verify(context.Background(), tokPlatform)
	if err != nil {
		t.Fatalf("verify platform token: %v", err)
	}
	if !pPlat.Platform {
		t.Fatal("expected Platform true for platform-ops claim")
	}

	tokNonPlatform := signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims("acme"))
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
				Issuer:                srv.server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "org",
			},
		},
		Platform: authn.PlatformConfig{
			Issuer:       srv.server.URL,
			ClaimName:    "groups",
			Organization: "platform-ops",
		},
		Resolver: resolver,
		Client:   srv.server.Client(),
		Clock:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier platform differing: %v", err)
	}
	diffClaims := map[string]any{
		"iss":    srv.server.URL,
		"aud":    "flowseer-device",
		"sub":    "u1",
		"exp":    now.Add(time.Hour).Unix(),
		"iat":    now.Unix(),
		"org":    "acme",
		"groups": []string{"platform-ops"},
	}
	tokDiff := signRSAToken(t, srv.rsaKey, srv.rsaKID, diffClaims)
	pDiff, err := platDiffVerifier.Verify(context.Background(), tokDiff)
	if err != nil {
		t.Fatalf("verify platform claim differing: %v", err)
	}
	if !pDiff.Platform {
		t.Fatal("expected Platform true when platform claim name differs and value matches")
	}

	// Platform issuer differs from token's issuer yields Platform false
	otherIssuerVerifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:                srv.server.URL,
				Audience:              "flowseer-device",
				OrganizationClaimName: "organization",
			},
		},
		Platform: authn.PlatformConfig{
			Issuer:       "https://other-platform.example.com",
			ClaimName:    "organization",
			Organization: "platform-ops",
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier other platform issuer: %v", err)
	}
	tokOtherIssuer := signRSAToken(t, srv.rsaKey, srv.rsaKID, baseClaims("platform-ops"))
	pOtherIssuer, err := otherIssuerVerifier.Verify(context.Background(), tokOtherIssuer)
	if err != nil {
		t.Fatalf("verify other platform issuer: %v", err)
	}
	if pOtherIssuer.Platform {
		t.Fatal("expected Platform false when platform issuer differs from token issuer")
	}
}

func TestVerifierReplayTransportAndKeyOutage(t *testing.T) {
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)
	curTime := now

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return curTime },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}

	// Repeated unknown kid tokens within 10 s replay window trigger one key fetch.
	startFetchCount := srv.keyFetchCount.Load()
	for i := 0; i < 20; i++ {
		tokUnknown := signRSAToken(t, otherKey, fmt.Sprintf("unknown-kid-%d", i), claims)
		_, _ = verifier.Verify(context.Background(), tokUnknown)
	}
	fetchesAfter20 := srv.keyFetchCount.Load() - startFetchCount
	if fetchesAfter20 != 1 {
		t.Fatalf("expected 1 key fetch for 20 unknown kid tokens within 10s, got %d", fetchesAfter20)
	}

	// Key endpoint answering 500 yields Unavailable for unknown kid.
	curTime = curTime.Add(15 * time.Second)
	srv.keysErr.Store(true)
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
		"iss": srv.server.URL,
		"aud": "wrong-audience",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tokWrongAud := signRSAToken(t, srv.rsaKey, srv.rsaKID, wrongAudClaims)
	_, err = verifier.Verify(context.Background(), tokWrongAud)
	if err == nil {
		t.Fatal("expected error for wrong audience token")
	}
	code, ok = errs.CodeOf(err)
	if !ok || code != authn.ErrCodeTokenInvalid {
		t.Fatalf("got code %v, want authn/token-invalid for wrong audience during key outage", code)
	}

	// Healthy keys endpoint yields Unauthenticated with authn/token-invalid.
	srv.keysErr.Store(false)
	curTime = curTime.Add(15 * time.Second)
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
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)
	curTime := now

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return curTime },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tok := signRSAToken(t, srv.rsaKey, srv.rsaKID, claims)

	// Discovery that fails returns Unavailable.
	srv.discoveryErr.Store(true)
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected discovery error")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authn.ErrCodeUnavailable {
		t.Fatalf("got code %v, want authn/unavailable on discovery failure", code)
	}

	// Server recovers, but within replay window cached error remains.
	srv.discoveryErr.Store(false)
	_, err = verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected still cached discovery error within replay window")
	}

	// Advance past replay window: discovers and verifies successfully.
	curTime = curTime.Add(15 * time.Second)
	p, err := verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify after recovery and replay window: %v", err)
	}
	if p.Subject != "u1" {
		t.Fatalf("got subject %q, want u1", p.Subject)
	}

	// Hanging server yields Unavailable once client timeout passes.
	hangingServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer hangingServer.Close()

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

	hangingTok := signRSAToken(t, srv.rsaKey, srv.rsaKID, map[string]any{
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
}

func TestVerifierEndedContextReturnsBare(t *testing.T) {
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	}
	tok := signRSAToken(t, srv.rsaKey, srv.rsaKID, claims)

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

	var cancelFirstRequest context.CancelFunc
	var discCalls atomic.Int64
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			n := discCalls.Add(1)
			if n == 1 && cancelFirstRequest != nil {
				cancelFirstRequest()
				time.Sleep(50 * time.Millisecond)
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
	cancelFirstRequest = cancel
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
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
	}
	tok := signRSAToken(t, srv.rsaKey, srv.rsaKID, claims)

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

	if discCount := srv.discoveryFetchCount.Load(); discCount != 1 {
		t.Fatalf("expected exactly 1 discovery request, got %d", discCount)
	}
}

func TestVerifierInjectedClockDecidesExpiry(t *testing.T) {
	srv := newTestOidcServer(t)
	realNow := time.Now().Truncate(time.Second)

	tokenExp := realNow.Add(30 * time.Minute)
	claims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": tokenExp.Unix(),
	}
	tok := signRSAToken(t, srv.rsaKey, srv.rsaKID, claims)

	injectedClock := func() time.Time {
		return realNow.Add(time.Hour)
	}

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.server.Client(),
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

type endpointBehavior string

const (
	behaviorAnswersCorrectly endpointBehavior = "answers-correctly"
	behaviorAnswers500       endpointBehavior = "answers-500"
	behaviorRefusesConn      endpointBehavior = "refuses-connection"
	behaviorNeverAnswers     endpointBehavior = "never-answers-until-timeout"
)

type propertyTokenKind string

const (
	tokenValidCachedKID   propertyTokenKind = "valid-with-cached-key-id"
	tokenValidUnknownKID  propertyTokenKind = "valid-with-unknown-key-id"
	tokenKnownKIDWrongSig propertyTokenKind = "known-key-id-wrong-signature"
	tokenAudOther         propertyTokenKind = "correct-signature-aud-other"
	tokenAudFetchingKeys  propertyTokenKind = "correct-signature-aud-fetching-keys"
	tokenExpired          propertyTokenKind = "expired"
	tokenMalformed        propertyTokenKind = "malformed"
)

type propertyPriorState string

const (
	priorFresh                    propertyPriorState = "fresh-verifier"
	priorKeyFetchFailedInWindow   propertyPriorState = "key-fetch-failed-in-window"
	priorKeyFetchFailedPastWindow propertyPriorState = "key-fetch-failed-past-window"
	priorCanceledMidDiscovery     propertyPriorState = "caller-canceled-mid-discovery"
	priorCanceledMidKeyFetch      propertyPriorState = "caller-canceled-mid-key-fetch"
)

type propertyCallerContext string

const (
	ctxLive           propertyCallerContext = "live"
	ctxCanceledBefore propertyCallerContext = "canceled-before-call"
	ctxCanceledMidReq propertyCallerContext = "canceled-mid-request"
)

type propertyHarness struct {
	rsaKey      *rsa.PrivateKey
	otherRSAKey *rsa.PrivateKey
	rsaKID      string
	otherKID    string

	discServer *httptest.Server
	keysServer *httptest.Server

	currentDiscBehavior atomic.Pointer[endpointBehavior]
	currentKeysBehavior atomic.Pointer[endpointBehavior]

	cancelMidRequest atomic.Pointer[context.CancelFunc]

	discHook atomic.Pointer[func()]
	keysHook atomic.Pointer[func()]

	frozenNanos atomic.Int64
}

func (h *propertyHarness) now() time.Time {
	return time.Unix(0, h.frozenNanos.Load()).UTC()
}

func (h *propertyHarness) setNow(t time.Time) {
	h.frozenNanos.Store(t.UnixNano())
}

func newPropertyHarness(t *testing.T) *propertyHarness {
	t.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	otherRSAKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other rsa key: %v", err)
	}

	h := &propertyHarness{
		rsaKey:      rsaKey,
		otherRSAKey: otherRSAKey,
		rsaKID:      "test-rsa-key-1",
		otherKID:    "test-unknown-kid-1",
	}

	behOK := behaviorAnswersCorrectly
	h.currentDiscBehavior.Store(&behOK)
	h.currentKeysBehavior.Store(&behOK)

	discMux := http.NewServeMux()
	discMux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if fn := h.cancelMidRequest.Swap(nil); fn != nil {
			(*fn)()
		}
		if hook := h.discHook.Load(); hook != nil && *hook != nil {
			(*hook)()
		}
		if fn := h.cancelMidRequest.Swap(nil); fn != nil {
			(*fn)()
		}

		beh := *h.currentDiscBehavior.Load()
		switch beh {
		case behaviorAnswersCorrectly:
			jwksURI := h.keysServer.URL + "/keys"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                h.discServer.URL,
				"jwks_uri":                              jwksURI,
				"id_token_signing_alg_values_supported": []string{"RS256"},
				"response_types_supported":              []string{"id_token"},
				"subject_types_supported":               []string{"public"},
			})
		case behaviorAnswers500:
			http.Error(w, "discovery 500 error", http.StatusInternalServerError)
		case behaviorNeverAnswers:
			time.Sleep(250 * time.Millisecond)
		case behaviorRefusesConn:
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}
	})
	h.discServer = httptest.NewServer(discMux)
	t.Cleanup(h.discServer.Close)

	keysMux := http.NewServeMux()
	keysMux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		if fn := h.cancelMidRequest.Swap(nil); fn != nil {
			(*fn)()
		}
		if hook := h.keysHook.Load(); hook != nil && *hook != nil {
			(*hook)()
		}
		if fn := h.cancelMidRequest.Swap(nil); fn != nil {
			(*fn)()
		}

		beh := *h.currentKeysBehavior.Load()
		switch beh {
		case behaviorAnswersCorrectly:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"kty": "RSA",
						"kid": h.rsaKID,
						"use": "sig",
						"alg": "RS256",
						"n":   base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
						"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes()),
					},
				},
			})
		case behaviorAnswers500:
			http.Error(w, "keys 500 error", http.StatusInternalServerError)
		case behaviorNeverAnswers:
			time.Sleep(250 * time.Millisecond)
		case behaviorRefusesConn:
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}
	})
	h.keysServer = httptest.NewServer(keysMux)
	t.Cleanup(h.keysServer.Close)

	return h
}

func (h *propertyHarness) makeToken(t *testing.T, kind propertyTokenKind, issuerURL string) string {
	t.Helper()
	now := h.now()
	baseClaims := map[string]any{
		"iss": issuerURL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}

	switch kind {
	case tokenValidCachedKID:
		return signRSAToken(t, h.rsaKey, h.rsaKID, baseClaims)
	case tokenValidUnknownKID:
		return signRSAToken(t, h.otherRSAKey, h.otherKID, baseClaims)
	case tokenKnownKIDWrongSig:
		return signRSAToken(t, h.otherRSAKey, h.rsaKID, baseClaims)
	case tokenAudOther:
		c := cloneMap(baseClaims)
		c["aud"] = "other"
		return signRSAToken(t, h.rsaKey, h.rsaKID, c)
	case tokenAudFetchingKeys:
		c := cloneMap(baseClaims)
		c["aud"] = "aud with fetching keys text"
		return signRSAToken(t, h.rsaKey, h.rsaKID, c)
	case tokenExpired:
		c := cloneMap(baseClaims)
		c["exp"] = now.Add(-time.Hour).Unix()
		return signRSAToken(t, h.rsaKey, h.rsaKID, c)
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

type propertyExpectedResult struct {
	errCode    errs.Code
	retryable  bool
	contextErr error
	success    bool
}

func propertyExpectedOutcome(
	disc endpointBehavior,
	keys endpointBehavior,
	tok propertyTokenKind,
	prior propertyPriorState,
	callerCtx propertyCallerContext,
) propertyExpectedResult {
	if callerCtx == ctxCanceledBefore {
		return propertyExpectedResult{contextErr: context.Canceled}
	}

	if tok == tokenMalformed {
		return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
	}

	discoveryCached := (prior == priorKeyFetchFailedInWindow ||
		prior == priorKeyFetchFailedPastWindow ||
		prior == priorCanceledMidKeyFetch)
	validKeyCached := discoveryCached

	if validKeyCached {
		switch tok {
		case tokenValidCachedKID:
			return propertyExpectedResult{success: true}
		case tokenAudOther:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
		case tokenAudFetchingKeys:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
		case tokenExpired:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenExpired}
		}
	}

	if prior == priorKeyFetchFailedInWindow && (tok == tokenValidUnknownKID || tok == tokenKnownKIDWrongSig) {
		return propertyExpectedResult{errCode: authn.ErrCodeUnavailable, retryable: true}
	}

	if callerCtx == ctxCanceledMidReq {
		return propertyExpectedResult{contextErr: context.Canceled}
	}

	if !discoveryCached {
		if disc != behaviorAnswersCorrectly {
			return propertyExpectedResult{errCode: authn.ErrCodeUnavailable, retryable: true}
		}
		if keys != behaviorAnswersCorrectly {
			return propertyExpectedResult{errCode: authn.ErrCodeUnavailable, retryable: true}
		}
		switch tok {
		case tokenValidCachedKID:
			return propertyExpectedResult{success: true}
		case tokenValidUnknownKID:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
		case tokenKnownKIDWrongSig:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
		case tokenAudOther:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
		case tokenAudFetchingKeys:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
		case tokenExpired:
			return propertyExpectedResult{errCode: authn.ErrCodeTokenExpired}
		}
	}

	if tok == tokenAudOther {
		return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
	}
	if tok == tokenAudFetchingKeys {
		return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
	}
	if tok == tokenExpired {
		return propertyExpectedResult{errCode: authn.ErrCodeTokenExpired}
	}

	if prior == priorKeyFetchFailedInWindow {
		return propertyExpectedResult{errCode: authn.ErrCodeUnavailable, retryable: true}
	}

	if keys != behaviorAnswersCorrectly {
		return propertyExpectedResult{errCode: authn.ErrCodeUnavailable, retryable: true}
	}

	return propertyExpectedResult{errCode: authn.ErrCodeTokenInvalid}
}

func checkCaseResult(t *testing.T, caseKey string, expected propertyExpectedResult, p authn.Principal, err error) {
	t.Helper()
	if expected.contextErr != nil {
		if !errors.Is(err, expected.contextErr) {
			t.Fatalf("[%s] want context error %v bare, got %v", caseKey, expected.contextErr, err)
		}
		if err != context.Canceled && err != context.DeadlineExceeded {
			t.Fatalf("[%s] want bare context error, got %T: %v", caseKey, err, err)
		}
		return
	}

	if expected.success {
		if err != nil {
			t.Fatalf("[%s] expected success, got error: %v", caseKey, err)
		}
		if p.Subject != "u1" {
			t.Fatalf("[%s] got subject %q, want u1", caseKey, p.Subject)
		}
		return
	}

	if err == nil {
		t.Fatalf("[%s] expected error %v, got success (principal: %+v)", caseKey, expected.errCode, p)
	}

	code, ok := errs.CodeOf(err)
	if !ok || code != expected.errCode {
		t.Fatalf("[%s] got code %v, want %v (err: %v)", caseKey, code, expected.errCode, err)
	}

	if expected.retryable && !errs.Retryable(err) {
		t.Fatalf("[%s] expected retryable error, got non-retryable: %v", caseKey, err)
	}
	if !expected.retryable && errs.Retryable(err) {
		t.Fatalf("[%s] expected non-retryable error, got retryable: %v", caseKey, err)
	}
}

func TestVerifierOutageClassificationProperty(t *testing.T) {
	h := newPropertyHarness(t)

	discBehaviors := []endpointBehavior{
		behaviorAnswersCorrectly,
		behaviorAnswers500,
		behaviorRefusesConn,
		behaviorNeverAnswers,
	}

	keysBehaviors := []endpointBehavior{
		behaviorAnswersCorrectly,
		behaviorAnswers500,
		behaviorRefusesConn,
		behaviorNeverAnswers,
	}

	tokens := []propertyTokenKind{
		tokenValidCachedKID,
		tokenValidUnknownKID,
		tokenKnownKIDWrongSig,
		tokenAudOther,
		tokenAudFetchingKeys,
		tokenExpired,
		tokenMalformed,
	}

	priorStates := []propertyPriorState{
		priorFresh,
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

	totalProduct := len(discBehaviors) * len(keysBehaviors) * len(tokens) * len(priorStates) * len(callerContexts)
	impossibleCases := map[string]string{}
	runCount := 0
	skippedCount := 0

	behOK := behaviorAnswersCorrectly
	beh500 := behaviorAnswers500

	for _, disc := range discBehaviors {
		for _, keys := range keysBehaviors {
			for _, tokKind := range tokens {
				for _, prior := range priorStates {
					for _, ctxKind := range callerContexts {
						caseKey := fmt.Sprintf("disc=%s/keys=%s/tok=%s/prior=%s/ctx=%s", disc, keys, tokKind, prior, ctxKind)
						if _, skip := impossibleCases[caseKey]; skip {
							skippedCount++
							continue
						}
						runCount++

						t0 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
						h.setNow(t0)

						issuerURL := h.discServer.URL

						tokValidCached := h.makeToken(t, tokenValidCachedKID, issuerURL)
						tokValidUnknown := h.makeToken(t, tokenValidUnknownKID, issuerURL)

						v, err := authn.NewVerifier(authn.Options{
							Issuers: []authn.IssuerConfig{
								{
									Issuer:   issuerURL,
									Audience: "flowseer-device",
								},
							},
							Client: &http.Client{
								Timeout: 100 * time.Millisecond,
							},
							Clock: h.now,
						})
						if err != nil {
							t.Fatalf("NewVerifier %s: %v", caseKey, err)
						}

						testToken := h.makeToken(t, tokKind, issuerURL)

						var callCtx context.Context
						var callCancel context.CancelFunc
						switch ctxKind {
						case ctxLive:
							callCtx = context.Background()
						case ctxCanceledBefore:
							c, cancel := context.WithCancel(context.Background())
							cancel()
							callCtx = c
						case ctxCanceledMidReq:
							c, cancel := context.WithCancel(context.Background())
							callCancel = cancel
							callCtx = c
						}

						var leaderDone chan struct{}
						switch prior {
						case priorFresh:
						case priorKeyFetchFailedInWindow:
							h.currentDiscBehavior.Store(&behOK)
							h.currentKeysBehavior.Store(&behOK)
							if _, err := v.Verify(context.Background(), tokValidCached); err != nil {
								t.Fatalf("setup %s verify valid: %v", caseKey, err)
							}
							t1 := t0.Add(15 * time.Second)
							h.setNow(t1)
							h.currentKeysBehavior.Store(&beh500)
							if _, err := v.Verify(context.Background(), tokValidUnknown); err == nil {
								t.Fatalf("setup %s expected failure for unknown kid", caseKey)
							}
						case priorKeyFetchFailedPastWindow:
							h.currentDiscBehavior.Store(&behOK)
							h.currentKeysBehavior.Store(&behOK)
							if _, err := v.Verify(context.Background(), tokValidCached); err != nil {
								t.Fatalf("setup %s verify valid: %v", caseKey, err)
							}
							t1 := t0.Add(15 * time.Second)
							h.setNow(t1)
							h.currentKeysBehavior.Store(&beh500)
							if _, err := v.Verify(context.Background(), tokValidUnknown); err == nil {
								t.Fatalf("setup %s expected failure for unknown kid", caseKey)
							}
							h.setNow(t1.Add(15 * time.Second))
						case priorCanceledMidDiscovery:
							discBeh := disc
							keysBeh := keys
							h.currentDiscBehavior.Store(&discBeh)
							h.currentKeysBehavior.Store(&keysBeh)
							if ctxKind == ctxCanceledMidReq && tokKind != tokenMalformed {
								h.cancelMidRequest.Store(&callCancel)
							}
							leaderStarted := make(chan struct{})
							done := make(chan struct{})
							leaderDone = done
							ctxPrior, cancelPrior := context.WithCancel(context.Background())
							hook := func() {
								cancelPrior()
								close(leaderStarted)
							}
							h.discHook.Store(&hook)
							go func() {
								_, _ = v.Verify(ctxPrior, tokValidCached)
								close(done)
							}()
							<-leaderStarted
							h.discHook.Store(nil)
						case priorCanceledMidKeyFetch:
							h.currentDiscBehavior.Store(&behOK)
							h.currentKeysBehavior.Store(&behOK)
							if _, err := v.Verify(context.Background(), tokValidCached); err != nil {
								t.Fatalf("setup %s verify valid: %v", caseKey, err)
							}
							t1 := t0.Add(15 * time.Second)
							h.setNow(t1)
							keysBeh := keys
							h.currentKeysBehavior.Store(&keysBeh)
							if ctxKind == ctxCanceledMidReq && (tokKind == tokenValidUnknownKID || tokKind == tokenKnownKIDWrongSig) {
								h.cancelMidRequest.Store(&callCancel)
							}
							leaderStarted := make(chan struct{})
							done := make(chan struct{})
							leaderDone = done
							ctxPrior, cancelPrior := context.WithCancel(context.Background())
							hook := func() {
								cancelPrior()
								close(leaderStarted)
							}
							h.keysHook.Store(&hook)
							go func() {
								_, _ = v.Verify(ctxPrior, tokValidUnknown)
								close(done)
							}()
							<-leaderStarted
							h.keysHook.Store(nil)
						}

						discBeh := disc
						keysBeh := keys
						h.currentDiscBehavior.Store(&discBeh)
						h.currentKeysBehavior.Store(&keysBeh)
						if ctxKind == ctxCanceledMidReq && prior != priorCanceledMidDiscovery && prior != priorCanceledMidKeyFetch {
							validKeyCached := (prior == priorKeyFetchFailedInWindow || prior == priorKeyFetchFailedPastWindow || prior == priorCanceledMidKeyFetch)
							willMakeNetworkCall := tokKind != tokenMalformed
							if validKeyCached && (tokKind == tokenValidCachedKID || tokKind == tokenAudOther || tokKind == tokenAudFetchingKeys || tokKind == tokenExpired) {
								willMakeNetworkCall = false
							}
							if prior == priorKeyFetchFailedInWindow && (tokKind == tokenValidUnknownKID || tokKind == tokenKnownKIDWrongSig) {
								willMakeNetworkCall = false
							}
							if willMakeNetworkCall {
								h.cancelMidRequest.Store(&callCancel)
							}
						}

						p, callErr := v.Verify(callCtx, testToken)
						h.cancelMidRequest.Store(nil)
						if callCancel != nil {
							callCancel()
						}
						if leaderDone != nil {
							<-leaderDone
						}

						expected := propertyExpectedOutcome(disc, keys, tokKind, prior, ctxKind)
						checkCaseResult(t, caseKey, expected, p, callErr)
					}
				}
			}
		}
	}

	if runCount < totalProduct-skippedCount {
		t.Fatalf("ran %d cases, want at least %d (%d total minus %d impossible)", runCount, totalProduct-skippedCount, totalProduct, skippedCount)
	}
}
