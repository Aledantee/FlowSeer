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
				return
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
