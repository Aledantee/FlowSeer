package authn

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// Authentication error codes returned by the verifier and interceptor.
var (
	// ErrCodeTokenInvalid indicates a malformed or untrusted token.
	ErrCodeTokenInvalid = errs.NewCode("authn/token-invalid")
	// ErrCodeTokenExpired indicates a token that has passed its expiration time.
	ErrCodeTokenExpired = errs.NewCode("authn/token-expired")
	// ErrCodeUnavailable indicates an OIDC provider or network failure.
	ErrCodeUnavailable = errs.NewCode("authn/unavailable")
)

// IssuerConfig defines settings for a trusted OpenID Connect issuer.
type IssuerConfig struct {
	Issuer                string
	Audience              string
	OrganizationClaimName string
}

// PlatformConfig identifies the platform administrator issuer and organization.
type PlatformConfig struct {
	Issuer       string
	ClaimName    string
	Organization string
}

// OrgResolver resolves a tenant record given an issuer URL and organization claim value.
type OrgResolver func(ctx context.Context, issuer, orgClaimValue string) (*identityv1.TenantRecord, error)

// Options configures a Verifier.
type Options struct {
	Issuers  []IssuerConfig
	Platform PlatformConfig
	Resolver OrgResolver
	Client   *http.Client
	Clock    func() time.Time
}

type cachedHTTPResult struct {
	statusCode int
	header     http.Header
	body       []byte
	err        error
	timestamp  time.Time
}

type replayTransport struct {
	base   http.RoundTripper
	window time.Duration
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]*cachedHTTPResult
}

func newReplayTransport(base http.RoundTripper, window time.Duration, clock func() time.Time) *replayTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &replayTransport{
		base:   base,
		window: window,
		now:    clock,
		cache:  make(map[string]*cachedHTTPResult),
	}
}

func (rt *replayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	urlStr := req.URL.String()

	rt.mu.Lock()
	cached, ok := rt.cache[urlStr]
	if ok && rt.now().Sub(cached.timestamp) < rt.window {
		status := cached.statusCode
		header := cached.header.Clone()
		body := cached.body
		err := cached.err
		rt.mu.Unlock()

		if err != nil {
			return nil, err
		}
		resp := &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header:     header,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    req,
		}
		return resp, nil
	}
	rt.mu.Unlock()

	resp, err := rt.base.RoundTrip(req)
	now := rt.now()
	if err != nil {
		rt.mu.Lock()
		rt.cache[urlStr] = &cachedHTTPResult{
			err:       err,
			timestamp: now,
		}
		rt.mu.Unlock()
		return nil, err
	}

	bodyBytes, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		rt.mu.Lock()
		rt.cache[urlStr] = &cachedHTTPResult{
			err:       readErr,
			timestamp: now,
		}
		rt.mu.Unlock()
		return nil, readErr
	}

	rt.mu.Lock()
	rt.cache[urlStr] = &cachedHTTPResult{
		statusCode: resp.StatusCode,
		header:     resp.Header.Clone(),
		body:       bodyBytes,
		timestamp:  now,
	}
	rt.mu.Unlock()

	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return resp, nil
}

func (rt *replayTransport) HasOutage(keyURL string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	cached, ok := rt.cache[keyURL]
	if !ok {
		return false
	}
	if rt.now().Sub(cached.timestamp) >= rt.window {
		return false
	}
	return cached.err != nil || cached.statusCode != http.StatusOK
}

// Verifier verifies operator OIDC tokens against trusted issuers.
type Verifier struct {
	issuers   map[string]IssuerConfig
	platform  PlatformConfig
	resolver  OrgResolver
	client    *http.Client
	transport *replayTransport
	clock     func() time.Time

	providersMu sync.Mutex
	providers   map[string]*providerState
	inflight    map[string]*discoveryCall
}

type providerState struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	jwksURI  string
}

type discoveryCall struct {
	done  chan struct{}
	state *providerState
	err   error
}

// NewVerifier creates a new token verifier without network I/O.
func NewVerifier(opts Options) (*Verifier, error) {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	issuers := make(map[string]IssuerConfig)
	for _, ic := range opts.Issuers {
		issuers[ic.Issuer] = ic
	}

	baseTransport := http.DefaultTransport
	timeout := 10 * time.Second
	if opts.Client != nil {
		if opts.Client.Transport != nil {
			baseTransport = opts.Client.Transport
		}
		if opts.Client.Timeout != 0 {
			timeout = opts.Client.Timeout
		}
	}

	replay := newReplayTransport(baseTransport, 10*time.Second, clock)
	httpClient := &http.Client{
		Transport: replay,
		Timeout:   timeout,
	}

	return &Verifier{
		issuers:   issuers,
		platform:  opts.Platform,
		resolver:  opts.Resolver,
		client:    httpClient,
		transport: replay,
		clock:     clock,
		providers: make(map[string]*providerState),
		inflight:  make(map[string]*discoveryCall),
	}, nil
}

func (v *Verifier) getProvider(ctx context.Context, issuerURL string, audience string) (*providerState, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	v.providersMu.Lock()
	if state, ok := v.providers[issuerURL]; ok {
		v.providersMu.Unlock()
		return state, nil
	}
	call, ok := v.inflight[issuerURL]
	if !ok {
		call = &discoveryCall{done: make(chan struct{})}
		v.inflight[issuerURL] = call

		spawn.Go(context.WithoutCancel(ctx), "authn.discovery", func() {
			discCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), v.client.Timeout)
			defer cancel()
			discoveryCtx := oidc.ClientContext(discCtx, v.client)
			provider, err := oidc.NewProvider(discoveryCtx, issuerURL)
			if err != nil {
				discErr := errs.New().Code(ErrCodeUnavailable).Retryable().Cause(err).Msg("discover oidc provider")
				v.providersMu.Lock()
				delete(v.inflight, issuerURL)
				call.err = discErr
				close(call.done)
				v.providersMu.Unlock()
				return
			}

			var claims struct {
				JWKSURI string `json:"jwks_uri"`
			}
			if err := provider.Claims(&claims); err != nil || claims.JWKSURI == "" {
				claimsErr := errs.New().Code(ErrCodeUnavailable).Retryable().Msg("extract jwks_uri from discovery")
				v.providersMu.Lock()
				delete(v.inflight, issuerURL)
				call.err = claimsErr
				close(call.done)
				v.providersMu.Unlock()
				return
			}

			verifier := provider.Verifier(&oidc.Config{
				ClientID: audience,
				Now:      v.clock,
			})

			state := &providerState{
				provider: provider,
				verifier: verifier,
				jwksURI:  claims.JWKSURI,
			}

			v.providersMu.Lock()
			v.providers[issuerURL] = state
			delete(v.inflight, issuerURL)
			call.state = state
			close(call.done)
			v.providersMu.Unlock()
		})
	}
	v.providersMu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if call.err != nil {
			return nil, call.err
		}
		return call.state, nil
	}
}

func isKeyFetchError(err error) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(err.Error(), "failed to verify signature: fetching keys ")
}

func parseClaimValues(raw json.RawMessage) ([]string, error) {
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	switch v := val.(type) {
	case string:
		return []string{v}, nil
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, errs.Msg("organization claim array contains non-string item")
			}
			values = append(values, s)
		}
		return values, nil
	case map[string]any:
		values := make([]string, 0, len(v))
		for k := range v {
			values = append(values, k)
		}
		slices.Sort(values)
		return values, nil
	default:
		return nil, errs.Msg("invalid organization claim shape")
	}
}

// Verify validates rawToken, resolves organizations, and constructs the Principal.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (Principal, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Principal{}, ctxErr
	}

	parts := strings.Split(rawToken, ".")
	if len(parts) < 2 {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Msg("malformed token")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payloadBytes, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("decode token payload")
		}
	}

	var unverified struct {
		Iss string `json:"iss"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payloadBytes, &unverified); err != nil {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("unmarshal token payload")
	}

	if unverified.Iss == "" {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Msg("token missing issuer")
	}

	issuerCfg, ok := v.issuers[unverified.Iss]
	if !ok {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Msg("untrusted token issuer")
	}

	state, err := v.getProvider(ctx, issuerCfg.Issuer, issuerCfg.Audience)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Principal{}, ctxErr
		}
		return Principal{}, err
	}

	clientCtx := oidc.ClientContext(ctx, v.client)
	idToken, err := state.verifier.Verify(clientCtx, rawToken)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Principal{}, ctxErr
		}

		var expiredErr *oidc.TokenExpiredError
		if errors.As(err, &expiredErr) {
			return Principal{}, errs.New().Code(ErrCodeTokenExpired).Cause(err).Msg("token expired")
		}

		if isKeyFetchError(err) && v.transport.HasOutage(state.jwksURI) {
			return Principal{}, errs.New().Code(ErrCodeUnavailable).Retryable().Cause(err).Msg("key fetch unavailable")
		}

		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("token signature or claims invalid")
	}

	sub := idToken.Subject
	if sub == "" || strings.Contains(sub, "\x00") {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Msg("token subject is empty or contains null byte")
	}

	var rawClaims map[string]json.RawMessage
	if err := idToken.Claims(&rawClaims); err != nil {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("unmarshal token claims")
	}

	var orgValues []string
	if issuerCfg.OrganizationClaimName != "" {
		if rawClaim, ok := rawClaims[issuerCfg.OrganizationClaimName]; ok {
			parsedValues, err := parseClaimValues(rawClaim)
			if err != nil {
				return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("parse organization claim")
			}
			orgValues = parsedValues
		}
	}

	distinctOrgs := make(map[string]struct{})
	for _, val := range orgValues {
		distinctOrgs[val] = struct{}{}
	}
	if len(distinctOrgs) > 99 {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Msg("organization claim exceeds 99 distinct values")
	}

	var tenants []string
	if v.resolver != nil && len(distinctOrgs) > 0 {
		for orgVal := range distinctOrgs {
			rec, err := v.resolver(ctx, issuerCfg.Issuer, orgVal)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return Principal{}, ctxErr
				}
				return Principal{}, errs.New().Code(ErrCodeUnavailable).Retryable().Cause(err).Msg("lookup tenant organization")
			}
			if rec != nil && rec.GetConfig() != nil {
				if rec.GetConfig().GetOrganizationClaimName() == issuerCfg.OrganizationClaimName {
					if ref := rec.GetConfig().GetRef(); ref != nil && ref.GetTenant() != nil {
						if tenantID := ref.GetTenant().GetId(); tenantID != "" {
							tenants = append(tenants, tenantID)
						}
					}
				}
			}
		}
		slices.Sort(tenants)
		tenants = slices.Compact(tenants)
	}

	isPlatform := false
	if v.platform.Issuer != "" && issuerCfg.Issuer == v.platform.Issuer {
		platClaimName := v.platform.ClaimName
		if platClaimName == "" {
			platClaimName = issuerCfg.OrganizationClaimName
		}
		if platClaimName != "" {
			var platValues []string
			if platClaimName == issuerCfg.OrganizationClaimName {
				platValues = orgValues
			} else if rawPlatClaim, ok := rawClaims[platClaimName]; ok {
				var err error
				platValues, err = parseClaimValues(rawPlatClaim)
				if err != nil {
					return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("parse platform claim")
				}
			}
			if slices.Contains(platValues, v.platform.Organization) {
				isPlatform = true
			}
		}
	}

	principalID := ComputePrincipalID(issuerCfg.Issuer, sub)

	return Principal{
		ID:       principalID,
		Issuer:   issuerCfg.Issuer,
		Subject:  sub,
		Tenants:  tenants,
		Platform: isPlatform,
	}, nil
}
