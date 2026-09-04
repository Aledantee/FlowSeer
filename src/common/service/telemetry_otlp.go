package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	grpcgzip "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const (
	telemetryRetryLimit       = 30 * time.Second
	telemetryRetryInitialWait = time.Second
	telemetryRetryMaxWait     = 5 * time.Second
	telemetryResponseLimit    = 1 << 20
)

var errTelemetryExportRejected = errors.New("telemetry export rejected")

func newDirectOTLPTransport(
	_ context.Context,
	config normalizedOTLPConnection,
) (otlpTransport, telemetryShutdown, error) {
	if config.protocol == "grpc" {
		return newGRPCOTLPTransport(config)
	}
	transport := newHTTPOTLPTransport(config)
	return transport, func(context.Context) error {
		transport.client.CloseIdleConnections()
		return nil
	}, nil
}

type httpOTLPTransport struct {
	client      *http.Client
	logsURL     string
	metricsURL  string
	tracesURL   string
	headers     map[string]string
	compression bool
	timeout     time.Duration
	now         func() time.Time
	retryPolicy telemetryRetryPolicy
}

func newHTTPOTLPTransport(config normalizedOTLPConnection) *httpOTLPTransport {
	tlsConfig := telemetryTLSConfig(config)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: tlsConfig,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &httpOTLPTransport{
		client:      client,
		logsURL:     telemetrySignalURL(config.endpoint, "logs"),
		metricsURL:  telemetrySignalURL(config.endpoint, "metrics"),
		tracesURL:   telemetrySignalURL(config.endpoint, "traces"),
		headers:     config.headers,
		compression: config.compression == "gzip",
		timeout:     config.timeout,
		now:         time.Now,
		retryPolicy: defaultTelemetryRetryPolicy(),
	}
}

func telemetrySignalURL(endpoint *url.URL, signal string) string {
	derived := *endpoint
	derived.Path = strings.TrimSuffix(derived.Path, "/") + "/v1/" + signal
	derived.RawPath = ""
	return derived.String()
}

func telemetryTLSConfig(config normalizedOTLPConnection) *tls.Config {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    config.rootCAs,
	}
	if config.endpoint != nil {
		tlsConfig.ServerName = config.endpoint.Hostname()
	}
	if config.clientCertificate != nil {
		tlsConfig.Certificates = []tls.Certificate{*config.clientCertificate}
	}
	return tlsConfig
}

func (t *httpOTLPTransport) uploadLogs(ctx context.Context, request *collectorlogspb.ExportLogsServiceRequest) error {
	response := new(collectorlogspb.ExportLogsServiceResponse)
	return t.upload(ctx, t.logsURL, request, response, func() int64 {
		return response.GetPartialSuccess().GetRejectedLogRecords()
	})
}

func (t *httpOTLPTransport) uploadMetrics(ctx context.Context, request *collectormetricspb.ExportMetricsServiceRequest) error {
	response := new(collectormetricspb.ExportMetricsServiceResponse)
	return t.upload(ctx, t.metricsURL, request, response, func() int64 {
		return response.GetPartialSuccess().GetRejectedDataPoints()
	})
}

func (t *httpOTLPTransport) uploadTraces(ctx context.Context, spans []*tracepb.ResourceSpans) error {
	response := new(collectortracepb.ExportTraceServiceResponse)
	return t.upload(ctx, t.tracesURL, &collectortracepb.ExportTraceServiceRequest{ResourceSpans: spans}, response, func() int64 {
		return response.GetPartialSuccess().GetRejectedSpans()
	})
}

func (t *httpOTLPTransport) upload(
	ctx context.Context,
	endpoint string,
	message proto.Message,
	responseMessage proto.Message,
	rejectedCount func() int64,
) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return errors.New("telemetry encoding failed")
	}
	body := payload
	if t.compression {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(payload); err != nil {
			return errors.New("telemetry compression failed")
		}
		if err := writer.Close(); err != nil {
			return errors.New("telemetry compression failed")
		}
		body = compressed.Bytes()
	}
	return retryOTLPWithPolicy(ctx, t.timeout, t.retryPolicy, func(attemptCtx context.Context) (time.Duration, bool, error) {
		request, requestErr := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestErr != nil {
			return 0, false, errors.New("telemetry request failed")
		}
		request.Header.Set("Content-Type", "application/x-protobuf")
		if t.compression {
			request.Header.Set("Content-Encoding", "gzip")
		}
		for key, value := range t.headers {
			request.Header.Set(key, value)
		}
		response, requestErr := t.client.Do(request)
		if requestErr != nil {
			return 0, true, errors.New("telemetry request failed")
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, telemetryResponseLimit+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(responseBody) > telemetryResponseLimit {
			return 0, true, errors.New("telemetry response failed")
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if err := proto.Unmarshal(responseBody, responseMessage); err != nil {
				return 0, false, errors.New("telemetry response failed")
			}
			if rejectedCount() > 0 {
				return 0, false, errTelemetryExportRejected
			}
			return 0, false, nil
		}
		retry := response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusGatewayTimeout
		return parseRetryAfter(response.Header.Get("Retry-After"), t.now()), retry, fmt.Errorf("telemetry response status %d", response.StatusCode)
	})
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return min(time.Duration(seconds)*time.Second, telemetryRetryMaxWait)
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return min(when.Sub(now), telemetryRetryMaxWait)
	}
	return 0
}

type grpcOTLPTransport struct {
	connection *grpc.ClientConn
	logs       collectorlogspb.LogsServiceClient
	metrics    collectormetricspb.MetricsServiceClient
	traces     collectortracepb.TraceServiceClient
	headers    metadata.MD
	timeout    time.Duration
}

func newGRPCOTLPTransport(config normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
	var transportCredentials credentials.TransportCredentials
	if config.insecure {
		transportCredentials = insecure.NewCredentials()
	} else {
		transportCredentials = credentials.NewTLS(telemetryTLSConfig(config))
	}
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithNoProxy(),
		grpc.WithDisableServiceConfig(),
		grpc.WithDisableRetry(),
	}
	if config.compression == "gzip" {
		options = append(options, grpc.WithDefaultCallOptions(grpc.UseCompressor(grpcgzip.Name)))
	}
	connection, err := grpc.NewClient(config.endpoint.Host, options...)
	if err != nil {
		return nil, nil, errors.New("create telemetry connection")
	}
	headers := metadata.New(nil)
	for key, value := range config.headers {
		headers.Set(key, value)
	}
	transport := &grpcOTLPTransport{
		connection: connection,
		logs:       collectorlogspb.NewLogsServiceClient(connection),
		metrics:    collectormetricspb.NewMetricsServiceClient(connection),
		traces:     collectortracepb.NewTraceServiceClient(connection),
		headers:    headers,
		timeout:    config.timeout,
	}
	return transport, func(context.Context) error { return connection.Close() }, nil
}

func (t *grpcOTLPTransport) uploadLogs(ctx context.Context, request *collectorlogspb.ExportLogsServiceRequest) error {
	return t.upload(ctx, func(attemptCtx context.Context) (int64, error) {
		response, err := t.logs.Export(metadata.NewOutgoingContext(attemptCtx, t.headers), request)
		if err != nil {
			return 0, err
		}
		if response == nil {
			return 0, errors.New("telemetry response failed")
		}
		return response.GetPartialSuccess().GetRejectedLogRecords(), nil
	})
}

func (t *grpcOTLPTransport) uploadMetrics(ctx context.Context, request *collectormetricspb.ExportMetricsServiceRequest) error {
	return t.upload(ctx, func(attemptCtx context.Context) (int64, error) {
		response, err := t.metrics.Export(metadata.NewOutgoingContext(attemptCtx, t.headers), request)
		if err != nil {
			return 0, err
		}
		if response == nil {
			return 0, errors.New("telemetry response failed")
		}
		return response.GetPartialSuccess().GetRejectedDataPoints(), nil
	})
}

func (t *grpcOTLPTransport) uploadTraces(ctx context.Context, spans []*tracepb.ResourceSpans) error {
	request := &collectortracepb.ExportTraceServiceRequest{ResourceSpans: spans}
	return t.upload(ctx, func(attemptCtx context.Context) (int64, error) {
		response, err := t.traces.Export(metadata.NewOutgoingContext(attemptCtx, t.headers), request)
		if err != nil {
			return 0, err
		}
		if response == nil {
			return 0, errors.New("telemetry response failed")
		}
		return response.GetPartialSuccess().GetRejectedSpans(), nil
	})
}

func (t *grpcOTLPTransport) upload(ctx context.Context, export func(context.Context) (int64, error)) error {
	return retryOTLP(ctx, t.timeout, func(attemptCtx context.Context) (time.Duration, bool, error) {
		rejected, err := export(attemptCtx)
		if err == nil {
			if rejected > 0 {
				return 0, false, errTelemetryExportRejected
			}
			return 0, false, nil
		}
		return grpcRetryDelay(err), retryGRPCError(err), errors.New("telemetry RPC failed")
	})
}

func retryGRPCError(err error) bool {
	switch status.Code(err) {
	case codes.Canceled, codes.DeadlineExceeded, codes.Aborted, codes.OutOfRange, codes.Unavailable, codes.DataLoss:
		return true
	case codes.ResourceExhausted:
		return grpcRetryDelay(err) > 0
	default:
		return false
	}
}

func grpcRetryDelay(err error) time.Duration {
	statusValue, ok := status.FromError(err)
	if !ok {
		return 0
	}
	for _, detail := range statusValue.Details() {
		if retry, ok := detail.(*errdetails.RetryInfo); ok && retry.RetryDelay != nil {
			return min(retry.RetryDelay.AsDuration(), telemetryRetryMaxWait)
		}
	}
	return 0
}

// retryOTLP bounds the complete retry loop separately from each export request.
func retryOTLP(
	ctx context.Context,
	requestTimeout time.Duration,
	attempt func(context.Context) (time.Duration, bool, error),
) error {
	return retryOTLPWithPolicy(ctx, requestTimeout, defaultTelemetryRetryPolicy(), attempt)
}

func defaultTelemetryRetryPolicy() telemetryRetryPolicy {
	return telemetryRetryPolicy{
		limit:       telemetryRetryLimit,
		initialWait: telemetryRetryInitialWait,
		maximumWait: telemetryRetryMaxWait,
		jitter:      telemetryFullJitter,
		wait:        realSupervisorClock{}.Wait,
	}
}

type telemetryRetryPolicy struct {
	limit       time.Duration
	initialWait time.Duration
	maximumWait time.Duration
	jitter      func(time.Duration) time.Duration
	wait        func(context.Context, time.Duration) error
}

// retryOTLPWithPolicy retries only attempts classified as transient, honors a
// bounded server delay, and applies full-jitter capped exponential backoff
// otherwise.
func retryOTLPWithPolicy(
	ctx context.Context,
	requestTimeout time.Duration,
	policy telemetryRetryPolicy,
	attempt func(context.Context) (time.Duration, bool, error),
) error {
	retryCtx, cancel := context.WithTimeout(ctx, policy.limit)
	defer cancel()
	backoff := policy.initialWait
	jitter := policy.jitter
	if jitter == nil {
		jitter = func(delay time.Duration) time.Duration { return delay }
	}
	for {
		attemptCtx, attemptCancel := context.WithTimeout(retryCtx, requestTimeout)
		retryAfter, retry, err := attempt(attemptCtx)
		attemptCancel()
		if err == nil || !retry {
			return err
		}
		wait := retryAfter
		if wait <= 0 {
			wait = jitter(backoff)
		}
		if err := policy.wait(retryCtx, wait); err != nil {
			return errors.New("telemetry retry deadline exceeded")
		}
		backoff = min(backoff*2, policy.maximumWait)
	}
}

func telemetryFullJitter(backoff time.Duration) time.Duration {
	if backoff <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(backoff)))
}

func telemetryExportFailureCategory(err error) string {
	if errors.Is(err, errTelemetryExportRejected) {
		return "rejected"
	}
	return "unavailable"
}
