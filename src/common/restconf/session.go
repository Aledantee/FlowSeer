package restconf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// yangDataJSON is the RFC 8040 media type for RFC 7951 payloads.
const yangDataJSON = "application/yang-data+json"

// Session is a RESTCONF client bound to one peer's discovered API
// root. Construct via [Dial]. Safe for concurrent use; RESTCONF is
// stateless, so requests carry their own errors and there is no
// latched terminal state.
type Session struct {
	base   string // scheme://host[:port], no trailing slash
	root   string // discovered API root path, e.g. /restconf
	client *http.Client
	opts   Options
	tracer trace.Tracer
}

// Dial verifies the peer and discovers its API root. base is the
// origin, e.g. "https://switch.example:443".
func Dial(ctx context.Context, base string, opts Options) (*Session, error) {
	opts = opts.withDefaults()
	client, err := opts.httpClient()
	if err != nil {
		return nil, err
	}
	base = strings.TrimSuffix(base, "/")

	tp := opts.TracerProvider
	if tp == nil {
		tp = tracenoop.NewTracerProvider()
	}
	s := &Session{
		base:   base,
		client: client,
		opts:   opts,
		tracer: tp.Tracer("go.aledante.io/FlowSeer/src/common/restconf"),
	}

	dialCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	root, err := discoverRoot(dialCtx, client, base, s.authorize)
	if err != nil {
		return nil, err
	}
	s.root = root
	return s, nil
}

// Root returns the discovered API root path.
func (s *Session) Root() string { return s.root }

// Close releases pooled connections. Idempotent.
func (s *Session) Close() error {
	s.client.CloseIdleConnections()
	return nil
}

// authorize stamps basic auth onto every request when configured.
func (s *Session) authorize(req *http.Request) {
	if s.opts.Username != "" {
		req.SetBasicAuth(s.opts.Username, s.opts.Password)
	}
}

// GetOptions selects server-side filtering for one read. Its zero value
// requests the full subtree. It may be shared between concurrent reads
// if it is not modified.
type GetOptions struct {
	// Depth bounds subtree depth via the RFC 8040 depth query
	// parameter when > 0. Peers that ignore it return the full
	// subtree; callers prune client-side.
	Depth int
	// Fields selects sub-resources via the fields query parameter
	// when non-empty.
	Fields string
	// ConfigOnly restricts to content=config.
	ConfigOnly bool
}

// query renders the parameters.
func (g GetOptions) query() string {
	var parts []string
	if g.Depth > 0 {
		parts = append(parts, fmt.Sprintf("depth=%d", g.Depth))
	}
	if g.Fields != "" {
		parts = append(parts, "fields="+g.Fields)
	}
	if g.ConfigOnly {
		parts = append(parts, "content=config")
	}
	if len(parts) == 0 {
		return ""
	}
	return "?" + strings.Join(parts, "&")
}

// dataURL renders the data-resource URL for a path.
func (s *Session) dataURL(p yang.Path) string {
	uri := p.RESTCONFURI()
	if uri == "/" {
		uri = ""
	}
	return s.base + s.root + "/data" + uri
}

// Get reads a data resource as RFC 7951 JSON. A 404 on a data
// resource yields (nil, nil): an absent optional subtree is data, not
// an error — the Watcher turns it into row removal.
func (s *Session) Get(ctx context.Context, p yang.Path, opts GetOptions) ([]byte, error) {
	body, status, _, err := s.do(ctx, http.MethodGet, s.dataURL(p)+opts.query(), nil, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, nil
	case status >= 400:
		return nil, deviceError("GET "+p.RESTCONFURI(), status, body)
	}
	return body, nil
}

// WriteResult reports how a write was performed and what the peer
// holds afterwards. It is safe for concurrent reads if ReadBack is not
// modified.
type WriteResult struct {
	// UsedIfMatch reports whether the peer supplied an ETag and the
	// write was conditional. False means the peer offered no
	// ETag and the write degraded to unconditional.
	UsedIfMatch bool
	// ReadBack is the resource's RFC 7951 JSON after the write — the
	// caller proves the edit by decoding and diffing it, never by
	// trusting the status code. Nil after Delete verification found
	// the resource gone.
	ReadBack []byte
}

// Put replaces a data resource after capturing its ETag and returns
// the read-back body. Capture failures other than 404 stop the edit;
// a missing ETag permits an unconditional write.
func (s *Session) Put(ctx context.Context, p yang.Path, body []byte) (WriteResult, error) {
	return s.write(ctx, http.MethodPut, p, body)
}

// Patch merges into a data resource (plain YANG patch semantics of
// RFC 8040 §4.6.1), with the same conditional-write discipline as
// [Session.Put].
func (s *Session) Patch(ctx context.Context, p yang.Path, body []byte) (WriteResult, error) {
	return s.write(ctx, http.MethodPatch, p, body)
}

// Delete removes a data resource, verified by a read-back that the
// resource is gone. It captures the ETag before editing and stops on
// capture failures other than 404. An already absent resource succeeds.
func (s *Session) Delete(ctx context.Context, p yang.Path) error {
	etag, err := s.captureETag(ctx, p)
	if err != nil {
		return err
	}
	headers := map[string]string{}
	if etag != "" {
		headers["If-Match"] = etag
	}
	body, status, _, err := s.do(ctx, http.MethodDelete, s.dataURL(p), nil, headers)
	if err != nil {
		return err
	}
	if status >= 400 && status != http.StatusNotFound {
		return deviceError("DELETE "+p.RESTCONFURI(), status, body)
	}
	readBack, err := s.Get(ctx, p, GetOptions{})
	if err != nil {
		return errs.Wrap(err, "read back after delete")
	}
	if readBack != nil {
		return errs.New().Code(ErrCodeDevice).Msgf("DELETE %s reported success but the resource is still present", p.RESTCONFURI())
	}
	return nil
}

// write is the shared conditional-write path: ETag capture, If-Match
// when available, read-back after the edit.
func (s *Session) write(ctx context.Context, method string, p yang.Path, body []byte) (WriteResult, error) {
	etag, err := s.captureETag(ctx, p)
	if err != nil {
		return WriteResult{}, err
	}
	headers := map[string]string{"Content-Type": yangDataJSON}
	if etag != "" {
		headers["If-Match"] = etag
	}
	respBody, status, _, err := s.do(ctx, method, s.dataURL(p), body, headers)
	if err != nil {
		return WriteResult{}, err
	}
	if status >= 400 {
		return WriteResult{}, deviceError(method+" "+p.RESTCONFURI(), status, respBody)
	}
	readBack, err := s.Get(ctx, p, GetOptions{})
	if err != nil {
		return WriteResult{}, errs.Wrap(err, "read back after write")
	}
	return WriteResult{UsedIfMatch: etag != "", ReadBack: readBack}, nil
}

// A missing resource can be created without an ETag; other read failures
// must stop the write because they cannot establish the current version.
func (s *Session) captureETag(ctx context.Context, p yang.Path) (string, error) {
	body, status, headers, err := s.do(ctx, http.MethodGet, s.dataURL(p), nil, nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", nil
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", deviceError("capture ETag for "+p.RESTCONFURI(), status, body)
	}
	return headers.Get("ETag"), nil
}

// do performs one HTTP round trip with timeout defaulting, tracing,
// auth, and body capture.
func (s *Session) do(ctx context.Context, method, url string, body []byte, headers map[string]string) (respBody []byte, status int, responseHeaders http.Header, err error) {
	ctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()

	ctx, span := s.tracer.Start(ctx, "restconf."+method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("http_method", method)))
	defer span.End()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, nil, errs.From(err).Code(ErrCodeTransport).Msgf("build %s request", method)
	}
	req.Header.Set("Accept", yangDataJSON)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	s.authorize(req)

	resp, err := s.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, 0, nil, s.transportError(method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err = io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, 0, nil, s.transportError("read "+method+" response", err)
	}
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, resp.Status)
	}
	return respBody, resp.StatusCode, resp.Header, nil
}

// transportError maps client-level failures, keeping caller context
// cancellation unwrapped.
func (s *Session) transportError(method string, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errs.From(err).Code(ErrCodeTransport).Msgf("%s transport failed", method)
}
