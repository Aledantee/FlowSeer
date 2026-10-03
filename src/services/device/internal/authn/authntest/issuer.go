// Package authntest provides an in-memory OIDC TLS test issuer for authentication tests.
package authntest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// Issuer is a TLS-backed OIDC test issuer.
type Issuer struct {
	Server *httptest.Server
	RSAKey *rsa.PrivateKey
	ECKey  *ecdsa.PrivateKey
	RSAKID string
	ECKID  string

	KeyFetchCount       atomic.Int64
	DiscoveryFetchCount atomic.Int64
	DiscoveryErr        atomic.Bool
	KeysErr             atomic.Bool

	mu            sync.Mutex
	keyHandler    http.HandlerFunc
	customKeyResp *customResponse
}

type customResponse struct {
	statusCode  int
	contentType string
	body        []byte
}

func mustGenerateRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

func mustGenerateECDSAKey() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}

func mustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func mustRSASign(priv *rsa.PrivateKey, digest []byte) []byte {
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest)
	if err != nil {
		panic(err)
	}
	return sig
}

func mustECDSASign(priv *ecdsa.PrivateKey, digest []byte) []byte {
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest)
	if err != nil {
		panic(err)
	}
	sigBytes := make([]byte, 64)
	r.FillBytes(sigBytes[:32])
	s.FillBytes(sigBytes[32:])
	return sigBytes
}

// New creates and starts a new TLS OIDC test issuer, registering server cleanup
// on the provided testing.TB.
func New(t testing.TB) *Issuer {
	t.Helper()

	rsaKey := mustGenerateRSAKey()
	ecKey := mustGenerateECDSAKey()

	iss := &Issuer{
		RSAKey: rsaKey,
		ECKey:  ecKey,
		RSAKID: "test-rsa-key-1",
		ECKID:  "test-ec-key-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss.DiscoveryFetchCount.Add(1)

		if iss.DiscoveryErr.Load() {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                iss.Server.URL,
			"jwks_uri":                              iss.Server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256", "ES256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	})

	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		iss.KeyFetchCount.Add(1)

		iss.mu.Lock()
		kh := iss.keyHandler
		resp := iss.customKeyResp
		iss.mu.Unlock()

		if kh != nil {
			kh(w, r)
			return
		}
		if resp != nil {
			if resp.contentType != "" {
				w.Header().Set("Content-Type", resp.contentType)
			}
			w.WriteHeader(resp.statusCode)
			_, _ = w.Write(resp.body)
			return
		}
		if iss.KeysErr.Load() {
			http.Error(w, "jwks error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": iss.RSAKID,
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(iss.RSAKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(iss.RSAKey.E)).Bytes()),
				},
				{
					"kty": "EC",
					"crv": "P-256",
					"kid": iss.ECKID,
					"use": "sig",
					"alg": "ES256",
					"x":   base64.RawURLEncoding.EncodeToString(iss.ECKey.X.FillBytes(make([]byte, 32))),
					"y":   base64.RawURLEncoding.EncodeToString(iss.ECKey.Y.FillBytes(make([]byte, 32))),
				},
			},
		})
	})

	iss.Server = httptest.NewTLSServer(mux)
	t.Cleanup(iss.Server.Close)
	return iss
}

// URL returns the base HTTPS URL of the test issuer.
func (iss *Issuer) URL() string {
	return iss.Server.URL
}

// Client returns an HTTP client configured to trust the issuer's TLS certificate.
func (iss *Issuer) Client() *http.Client {
	return iss.Server.Client()
}

// CACert returns the PEM-encoded TLS certificate of the test issuer.
func (iss *Issuer) CACert() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: iss.Server.Certificate().Raw,
	})
}

// WriteCACert writes the issuer's PEM-encoded TLS certificate to the specified file path.
func (iss *Issuer) WriteCACert(path string) error {
	return os.WriteFile(path, iss.CACert(), 0o600)
}

// WriteCACertFile writes the issuer's PEM certificate to a new file in t.TempDir and returns its path.
func (iss *Issuer) WriteCACertFile(t testing.TB) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "issuer-ca.crt")
	if err := iss.WriteCACert(p); err != nil {
		t.Fatalf("write ca cert: %v", err)
	}
	return p
}

// SetDiscoveryError toggles simulated server error responses on discovery.
func (iss *Issuer) SetDiscoveryError(err bool) {
	iss.DiscoveryErr.Store(err)
}

// SetKeysError toggles simulated 500 error responses on the /keys endpoint.
func (iss *Issuer) SetKeysError(err bool) {
	iss.KeysErr.Store(err)
}

// SetKeyResponse configures a custom HTTP response to be returned by the /keys endpoint.
func (iss *Issuer) SetKeyResponse(statusCode int, contentType string, body []byte) {
	iss.mu.Lock()
	defer iss.mu.Unlock()
	iss.customKeyResp = &customResponse{
		statusCode:  statusCode,
		contentType: contentType,
		body:        body,
	}
}

// SetKeyHandler overrides the /keys endpoint with a custom HTTP handler.
func (iss *Issuer) SetKeyHandler(h http.HandlerFunc) {
	iss.mu.Lock()
	defer iss.mu.Unlock()
	iss.keyHandler = h
}

// ResetKeyEndpoint clears any custom key response, custom handler, or error state.
func (iss *Issuer) ResetKeyEndpoint() {
	iss.mu.Lock()
	defer iss.mu.Unlock()
	iss.customKeyResp = nil
	iss.keyHandler = nil
	iss.KeysErr.Store(false)
}

// Sign signs the given claims map as a JWT using the issuer's RSA key and key ID.
func (iss *Issuer) Sign(claims map[string]any) string {
	return iss.SignWithKey(iss.RSAKey, iss.RSAKID, claims)
}

// SignWithKey signs the given claims map as a JWT using the specified RSA key and key ID.
func (iss *Issuer) SignWithKey(priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	hJSON := mustMarshalJSON(header)
	cJSON := mustMarshalJSON(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hJSON) + "." + base64.RawURLEncoding.EncodeToString(cJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig := mustRSASign(priv, digest[:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// SignECDSA signs the given claims map as a JWT using the issuer's ECDSA key and key ID.
func (iss *Issuer) SignECDSA(claims map[string]any) string {
	return iss.SignECDSAWithKey(iss.ECKey, iss.ECKID, claims)
}

// SignECDSAWithKey signs the given claims map as a JWT using the specified ECDSA key and key ID.
func (iss *Issuer) SignECDSAWithKey(priv *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	header := map[string]any{"alg": "ES256", "typ": "JWT", "kid": kid}
	hJSON := mustMarshalJSON(header)
	cJSON := mustMarshalJSON(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hJSON) + "." + base64.RawURLEncoding.EncodeToString(cJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig := mustECDSASign(priv, digest[:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// BadSigner returns a newly generated RSA private key not registered in the issuer's key set.
func (iss *Issuer) BadSigner() *rsa.PrivateKey {
	return mustGenerateRSAKey()
}

// SignBad signs the given claims map with an unregistered key.
func (iss *Issuer) SignBad(claims map[string]any) string {
	return iss.SignWithKey(iss.BadSigner(), "unregistered-kid", claims)
}

// UnsignedToken constructs an unsigned JWT string from the provided header and claims maps.
func UnsignedToken(header, claims map[string]any) string {
	hJSON := mustMarshalJSON(header)
	cJSON := mustMarshalJSON(claims)
	return base64.RawURLEncoding.EncodeToString(hJSON) + "." + base64.RawURLEncoding.EncodeToString(cJSON) + "."
}
