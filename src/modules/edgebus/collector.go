package edgebus

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// defaultCollectorTimeout is the request deadline of a client that sets none.
const defaultCollectorTimeout = 30 * time.Second

// PostOutcome is the collector's answer to one body, classified by what the
// sender does next.
type PostOutcome int

const (
	// PostAccepted is a 2xx answer: the collector holds the body.
	PostAccepted PostOutcome = iota + 1
	// PostRefused is a 4xx answer that describes this body, such as a
	// malformed or oversized export. Sending it again cannot change the
	// answer.
	PostRefused
	// PostRetryRejected is a 401, 403, 404, 408, or 429 answer. These
	// describe the collector's configuration or load rather than the body,
	// so the body is worth sending again once an operator has acted.
	PostRetryRejected
	// PostRetryUnavailable is a 5xx or any other answer, a transport error,
	// or a request that could not be built. The body is worth sending again.
	PostRetryUnavailable
)

// CollectorConfig declares the collector central ships OTLP bodies to.
// Construct with keyed fields.
type CollectorConfig struct {
	// Endpoint is the collector's base URL, an absolute http or https URL;
	// the signal paths are appended.
	Endpoint string
	// Headers are sent on every request, an authorization header for
	// example.
	Headers map[string]string
	// Client sends the requests. Nil uses a client with a thirty-second
	// timeout. A client with no timeout is copied and given one, so the
	// caller's client, often a shared one, is left alone.
	Client *http.Client
}

// Collector posts OTLP bodies to one collector and classifies its answer. The
// forwarder and central's telemetry call share it, so the two classify an
// answer alike. A Collector is safe for concurrent use.
type Collector struct {
	endpoint string
	headers  map[string]string
	client   *http.Client
}

// NewCollector validates the configuration and returns a Collector. It
// refuses an endpoint that is not an absolute http or https URL, since a
// malformed one would fail every post and look like a refusal of every body.
func NewCollector(cfg CollectorConfig) (*Collector, error) {
	if err := validateCollectorEndpoint(cfg.Endpoint); err != nil {
		return nil, err
	}
	return &Collector{endpoint: cfg.Endpoint, headers: cfg.Headers, client: collectorClient(cfg.Client)}, nil
}

// validateCollectorEndpoint refuses an endpoint that is not an absolute http
// or https URL.
func validateCollectorEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return errs.From(err).Code(ErrCodeConfig).Msg("the collector endpoint is not a URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errs.New().Code(ErrCodeConfig).Msg("the collector endpoint must be an absolute http or https URL")
	}
	return nil
}

// collectorClient returns client when it sets a request timeout, and
// otherwise a client that does. A nil client gets a new one. A client with no
// timeout is copied: the per-request deadline and the forwarder's AckWait are
// both derived from the timeout, so zero would expire every context before
// the request starts, and the caller's client may be shared (the motivating
// case is http.DefaultClient), with its Timeout read without synchronization
// by every request already in flight.
func collectorClient(client *http.Client) *http.Client {
	switch {
	case client == nil:
		return &http.Client{Timeout: defaultCollectorTimeout}
	case client.Timeout == 0:
		copied := *client
		copied.Timeout = defaultCollectorTimeout
		return &copied
	}
	return client
}

// Post sends body unchanged to the collector's path for signal and returns
// how the collector answered.
func (c *Collector) Post(ctx context.Context, signal OTelSignal, body []byte) PostOutcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.endpoint, "/")+"/v1/"+string(signal), bytes.NewReader(body))
	if err != nil {
		return PostRetryUnavailable
	}
	req.Header.Set("Content-Type", otlpContentType)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return PostRetryUnavailable
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return PostAccepted
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden,
		resp.StatusCode == http.StatusNotFound:
		return PostRetryRejected
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return PostRefused
	}
	return PostRetryUnavailable
}
