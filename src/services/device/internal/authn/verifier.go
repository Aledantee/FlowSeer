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
	"unicode/utf8"

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

	// The clock is caller-supplied code, so it runs outside the lock: a panic
	// inside it must not leave rt.mu held.
	start := rt.now()

	rt.mu.Lock()
	cached, ok := rt.cache[urlStr]
	if ok && start.Sub(cached.timestamp) < rt.window {
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

// Verifier verifies operator OIDC tokens against trusted issuers.
type Verifier struct {
	issuers  map[string]IssuerConfig
	platform PlatformConfig
	resolver OrgResolver
	client   *http.Client
	clock    func() time.Time

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

		spawn.Go(context.Background(), "authn.discovery",
			func() { v.discover(call, issuerURL, audience) },
			spawn.ReportTo(func(err error) {
				v.finishDiscovery(issuerURL, call, nil, discoveryError(err))
			}),
		)
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

// discover runs the discovery every waiter on call shares. It runs on a
// context of its own, as go-oidc does for its key set, so no caller's span,
// values, or cancellation reaches it. Every path ends in finishDiscovery, and a
// panic reaches it through the spawn sink instead.
func (v *Verifier) discover(call *discoveryCall, issuerURL, audience string) {
	ctx, cancel := context.WithTimeout(context.Background(), v.client.Timeout)
	defer cancel()

	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, v.client), issuerURL)
	if err != nil {
		v.finishDiscovery(issuerURL, call, nil, discoveryError(err))
		return
	}

	var claims struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&claims); err != nil || claims.JWKSURI == "" {
		v.finishDiscovery(issuerURL, call, nil,
			errs.New().Code(ErrCodeUnavailable).Retryable().Msg("extract jwks_uri from discovery"))
		return
	}

	v.finishDiscovery(issuerURL, call, &providerState{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{
			ClientID: audience,
			Now:      v.clock,
		}),
		jwksURI: claims.JWKSURI,
	}, nil)
}

// finishDiscovery publishes the outcome of call and releases its waiters. It
// is idempotent, so the normal path and the panic sink cannot both complete
// the call.
func (v *Verifier) finishDiscovery(issuerURL string, call *discoveryCall, state *providerState, err error) {
	v.providersMu.Lock()
	defer v.providersMu.Unlock()

	select {
	case <-call.done:
		return
	default:
	}

	if state != nil {
		v.providers[issuerURL] = state
	}
	if v.inflight[issuerURL] == call {
		delete(v.inflight, issuerURL)
	}
	call.state = state
	call.err = err
	close(call.done)
}

// discoveryError classifies a failed discovery as an outage. The cause keeps
// the text only: the transport's context error, which the replay cache hands to
// every caller inside the window, must not satisfy errors.Is for a caller whose
// own context is live.
func discoveryError(cause error) error {
	return errs.New().Code(ErrCodeUnavailable).Retryable().Cause(errs.Msg(cause.Error())).Msg("discover oidc provider")
}

func isKeyFetchError(err error) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(err.Error(), "failed to verify signature: fetching keys ")
}

func parseClaimValues(raw json.RawMessage, label string) ([]string, error) {
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
				return nil, errs.Msgf("%s array contains non-string item", label)
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
		return nil, errs.Msgf("invalid %s shape", label)
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

		if isKeyFetchError(err) {
			return Principal{}, errs.New().Code(ErrCodeUnavailable).Retryable().Cause(err).Msg("key fetch unavailable")
		}

		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("token signature or claims invalid")
	}

	sub := idToken.Subject
	if sub == "" || utf8.RuneCountInString(sub) > 256 || strings.Contains(sub, "\x00") {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Msg("token subject is empty, exceeds 256 characters, or contains null byte")
	}

	var rawClaims map[string]json.RawMessage
	if err := idToken.Claims(&rawClaims); err != nil {
		return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("unmarshal token claims")
	}

	var orgValues []string
	if issuerCfg.OrganizationClaimName != "" {
		if rawClaim, ok := rawClaims[issuerCfg.OrganizationClaimName]; ok {
			parsedValues, err := parseClaimValues(rawClaim, "organization claim")
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
				if rec.GetState() != nil && rec.GetState().GetLifecycle() == identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE {
					if rec.GetConfig().GetOrganizationClaimName() == issuerCfg.OrganizationClaimName {
						if ref := rec.GetConfig().GetRef(); ref != nil && ref.GetTenant() != nil {
							if tenantID := ref.GetTenant().GetId(); tenantID != "" {
								tenants = append(tenants, tenantID)
							}
						}
					}
				}
			}
		}
		slices.Sort(tenants)
		tenants = slices.Compact(tenants)
	}

	isPlatform := false
	if v.platform.Issuer != "" && issuerCfg.Issuer == v.platform.Issuer && v.platform.ClaimName != "" {
		var platValues []string
		if v.platform.ClaimName == issuerCfg.OrganizationClaimName {
			platValues = orgValues
		} else if rawPlatClaim, ok := rawClaims[v.platform.ClaimName]; ok {
			var err error
			platValues, err = parseClaimValues(rawPlatClaim, "platform claim")
			if err != nil {
				return Principal{}, errs.New().Code(ErrCodeTokenInvalid).Cause(err).Msg("parse platform claim")
			}
		}
		if slices.Contains(platValues, v.platform.Organization) {
			isPlatform = true
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
