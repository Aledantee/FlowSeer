package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

func TestHTTPOTLPTransportClassifiesRetryStatuses(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) == 1 {
					writer.WriteHeader(statusCode)
				}
			}))
			defer server.Close()

			var waits []time.Duration
			transport := newTestHTTPOTLPTransport(t, server.URL, func(_ context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return nil
			})
			if err := transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{}); err != nil {
				t.Fatalf("uploadLogs() error: %v", err)
			}
			if got := requests.Load(); got != 2 {
				t.Fatalf("requests = %d, want 2", got)
			}
			if len(waits) != 1 {
				t.Fatalf("retry waits = %v, want one wait", waits)
			}
		})
	}
}

func TestHTTPOTLPTransportDoesNotRetryOtherClientErrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	var waits []time.Duration
	transport := newTestHTTPOTLPTransport(t, server.URL, func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	})
	if err := transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{}); err == nil {
		t.Fatal("uploadLogs() error = nil, want non-retryable status error")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	if len(waits) != 0 {
		t.Fatalf("retry waits = %v, want none", waits)
	}
}

func TestHTTPOTLPTransportPreservesFramingHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Content-Type"); got != "application/x-protobuf" {
			t.Errorf("Content-Type = %q, want application/x-protobuf", got)
		}
		if got := request.Header.Get("Content-Encoding"); got != "gzip" {
			t.Errorf("Content-Encoding = %q, want gzip", got)
		}
	}))
	defer server.Close()

	transport := newTestHTTPOTLPTransport(t, server.URL, func(context.Context, time.Duration) error { return nil })
	transport.compression = true
	transport.headers = map[string]string{
		"content-type":     "text/plain",
		"content-encoding": "br",
	}
	if err := transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{}); err != nil {
		t.Fatalf("uploadLogs() error: %v", err)
	}
}

func TestHTTPOTLPTransportHonorsRetryAfterWithoutJitter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	tests := []struct {
		name       string
		header     string
		wantWait   time.Duration
		wantJitter []time.Duration
	}{
		{name: "seconds", header: "3", wantWait: 3 * time.Second},
		{name: "HTTP date", header: now.Add(4 * time.Second).Format(http.TimeFormat), wantWait: 4 * time.Second},
		{name: "missing", wantWait: 137 * time.Millisecond, wantJitter: []time.Duration{time.Second}},
		{name: "malformed", header: "later", wantWait: 137 * time.Millisecond, wantJitter: []time.Duration{time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) == 1 {
					if tt.header != "" {
						writer.Header().Set("Retry-After", tt.header)
					}
					writer.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()

			var waits []time.Duration
			var jitterInputs []time.Duration
			transport := newTestHTTPOTLPTransport(t, server.URL, func(_ context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return nil
			})
			transport.now = func() time.Time { return now }
			transport.retryPolicy.jitter = func(delay time.Duration) time.Duration {
				jitterInputs = append(jitterInputs, delay)
				return 137 * time.Millisecond
			}
			if err := transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{}); err != nil {
				t.Fatalf("uploadLogs() error: %v", err)
			}
			if len(waits) != 1 || waits[0] != tt.wantWait {
				t.Fatalf("retry waits = %v, want [%v]", waits, tt.wantWait)
			}
			if !slices.Equal(jitterInputs, tt.wantJitter) {
				t.Fatalf("jitter inputs = %v, want %v", jitterInputs, tt.wantJitter)
			}
		})
	}
}

func TestTelemetryFullJitterStaysWithinBackoff(t *testing.T) {
	const backoff = 5 * time.Second
	for range 100 {
		got := telemetryFullJitter(backoff)
		if got < 0 || got >= backoff {
			t.Fatalf("telemetryFullJitter(%v) = %v, want [0,%v)", backoff, got, backoff)
		}
	}
}

func TestHTTPOTLPTransportRefusesRedirects(t *testing.T) {
	targetRequests := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		targetRequests <- struct{}{}
	}))
	defer target.Close()

	sourceBody := make(chan []byte, 1)
	sourceHeader := make(chan string, 1)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sourceBody <- body
		sourceHeader <- request.Header.Get("X-OTLP-API-Key")
		writer.Header().Set("Location", target.URL)
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	transport := newTestHTTPOTLPTransport(t, source.URL, func(context.Context, time.Duration) error { return nil })
	transport.headers = map[string]string{"X-OTLP-API-Key": "credential-secret"}
	err := transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{}},
	})
	if err == nil {
		t.Fatal("uploadLogs() error = nil, want redirect rejection")
	}
	if got := <-sourceHeader; got != "credential-secret" {
		t.Fatalf("source header = %q, want configured credential", got)
	}
	if got := <-sourceBody; len(got) == 0 {
		t.Fatal("source request body is empty")
	}
	select {
	case <-targetRequests:
		t.Fatal("redirect target received the OTLP request")
	default:
	}
}

func TestHTTPOTLPTransportRejectsPartialSuccessWithoutCollectorText(t *testing.T) {
	const collectorText = "collector-secret-rejection-detail"
	tests := []struct {
		name     string
		response proto.Message
		upload   func(*httpOTLPTransport) error
	}{
		{
			name: "logs",
			response: &collectorlogspb.ExportLogsServiceResponse{PartialSuccess: &collectorlogspb.ExportLogsPartialSuccess{
				RejectedLogRecords: 1,
				ErrorMessage:       collectorText,
			}},
			upload: func(transport *httpOTLPTransport) error {
				return transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{})
			},
		},
		{
			name: "metrics",
			response: &collectormetricspb.ExportMetricsServiceResponse{PartialSuccess: &collectormetricspb.ExportMetricsPartialSuccess{
				RejectedDataPoints: 1,
				ErrorMessage:       collectorText,
			}},
			upload: func(transport *httpOTLPTransport) error {
				return transport.uploadMetrics(context.Background(), &collectormetricspb.ExportMetricsServiceRequest{})
			},
		},
		{
			name: "traces",
			response: &collectortracepb.ExportTraceServiceResponse{PartialSuccess: &collectortracepb.ExportTracePartialSuccess{
				RejectedSpans: 1,
				ErrorMessage:  collectorText,
			}},
			upload: func(transport *httpOTLPTransport) error {
				return transport.uploadTraces(context.Background(), nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := proto.Marshal(tt.response)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write(payload)
			}))
			defer server.Close()

			transport := newTestHTTPOTLPTransport(t, server.URL, func(context.Context, time.Duration) error { return nil })
			err = tt.upload(transport)
			assertTelemetryRejected(t, err)
		})
	}
}

func TestGRPCOTLPTransportRejectsPartialSuccessWithoutCollectorText(t *testing.T) {
	const collectorText = "collector-secret-rejection-detail"
	transport := &grpcOTLPTransport{
		logs: rejectingLogsClient{response: &collectorlogspb.ExportLogsServiceResponse{PartialSuccess: &collectorlogspb.ExportLogsPartialSuccess{
			RejectedLogRecords: 1,
			ErrorMessage:       collectorText,
		}}},
		metrics: rejectingMetricsClient{response: &collectormetricspb.ExportMetricsServiceResponse{PartialSuccess: &collectormetricspb.ExportMetricsPartialSuccess{
			RejectedDataPoints: 1,
			ErrorMessage:       collectorText,
		}}},
		traces: rejectingTracesClient{response: &collectortracepb.ExportTraceServiceResponse{PartialSuccess: &collectortracepb.ExportTracePartialSuccess{
			RejectedSpans: 1,
			ErrorMessage:  collectorText,
		}}},
		timeout: time.Second,
	}

	assertTelemetryRejected(t, transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{}))
	assertTelemetryRejected(t, transport.uploadMetrics(context.Background(), &collectormetricspb.ExportMetricsServiceRequest{}))
	assertTelemetryRejected(t, transport.uploadTraces(context.Background(), nil))
}

func newTestHTTPOTLPTransport(t *testing.T, endpoint string, wait func(context.Context, time.Duration) error) *httpOTLPTransport {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	transport := newHTTPOTLPTransport(normalizedOTLPConnection{endpoint: parsed, timeout: time.Second})
	transport.retryPolicy = telemetryRetryPolicy{
		limit:       time.Hour,
		initialWait: time.Second,
		maximumWait: telemetryRetryMaxWait,
		jitter:      func(delay time.Duration) time.Duration { return delay },
		wait:        wait,
	}
	return transport
}

func assertTelemetryRejected(t *testing.T, err error) {
	t.Helper()
	const collectorText = "collector-secret-rejection-detail"
	if !errors.Is(err, errTelemetryExportRejected) {
		t.Fatalf("error = %v, want telemetry rejection", err)
	}
	if got := telemetryExportFailureCategory(err); got != "rejected" {
		t.Errorf("failure category = %q, want rejected", got)
	}
	if strings.Contains(err.Error(), collectorText) {
		t.Fatalf("error exposed Collector text: %q", err)
	}
}

type rejectingLogsClient struct {
	response *collectorlogspb.ExportLogsServiceResponse
}

func (c rejectingLogsClient) Export(context.Context, *collectorlogspb.ExportLogsServiceRequest, ...grpc.CallOption) (*collectorlogspb.ExportLogsServiceResponse, error) {
	return c.response, nil
}

type rejectingMetricsClient struct {
	response *collectormetricspb.ExportMetricsServiceResponse
}

func (c rejectingMetricsClient) Export(context.Context, *collectormetricspb.ExportMetricsServiceRequest, ...grpc.CallOption) (*collectormetricspb.ExportMetricsServiceResponse, error) {
	return c.response, nil
}

type rejectingTracesClient struct {
	response *collectortracepb.ExportTraceServiceResponse
}

func (c rejectingTracesClient) Export(context.Context, *collectortracepb.ExportTraceServiceRequest, ...grpc.CallOption) (*collectortracepb.ExportTraceServiceResponse, error) {
	return c.response, nil
}
