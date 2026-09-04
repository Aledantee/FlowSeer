//go:build service_otel_integration

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/common/service"
)

const (
	otelCollectorImage = "otel/opentelemetry-collector-contrib:0.160.0@sha256:799dc6cf12c96192af37b5bdba804da8c10b3bc563b43cb90c3f3c58d9572ad6"
	otelArtifactDirEnv = "FLOWSEER_OTEL_TEST_ARTIFACT_DIR"

	telemetrySecretSentinel = "flowseer-otel-artifact-secret"
	collectorPollTimeout    = 15 * time.Second
	collectorStablePeriod   = 300 * time.Millisecond
	maximumArtifactBytes    = 64 << 10
)

type telemetrySignalSet uint8

const (
	telemetryLogs telemetrySignalSet = 1 << iota
	telemetryMetrics
	telemetryTraces

	allTelemetrySignals = telemetryLogs | telemetryMetrics | telemetryTraces
)

func (s telemetrySignalSet) String() string {
	var signals []string
	if s&telemetryLogs != 0 {
		signals = append(signals, "logs")
	}
	if s&telemetryMetrics != 0 {
		signals = append(signals, "metrics")
	}
	if s&telemetryTraces != 0 {
		signals = append(signals, "traces")
	}
	if len(signals) == 0 {
		return "none"
	}
	return strings.Join(signals, ",")
}

type otelCollector struct {
	t            *testing.T
	container    testcontainers.Container
	outputDir    string
	httpEndpoint string
	grpcEndpoint string
	baseEndpoint string
}

var telemetryIdentitySequence atomic.Uint64

func startOTelCollector(t *testing.T) *otelCollector {
	t.Helper()
	if testing.Short() {
		t.Skip("Collector integration test")
	}

	outputDir := t.TempDir()
	if err := os.Chmod(outputDir, 0o777); err != nil {
		t.Fatalf("make Collector output directory writable: %v", err)
	}
	configPath := otelCollectorConfigPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        otelCollectorImage,
			ExposedPorts: []string{"4317/tcp", "4318/tcp", "4328/tcp", "13133/tcp"},
			Cmd:          []string{"--config=/etc/otelcol-contrib/config.yaml"},
			Files: []testcontainers.ContainerFile{{
				HostFilePath:      configPath,
				ContainerFilePath: "/etc/otelcol-contrib/config.yaml",
				FileMode:          0o444,
			}},
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.Mounts = append(hostConfig.Mounts, mount.Mount{
					Type:   mount.TypeBind,
					Source: outputDir,
					Target: "/output",
				})
			},
			WaitingFor: wait.ForHTTP("/").WithPort("13133/tcp").WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start pinned OpenTelemetry Collector: %v", err)
	}

	collector := &otelCollector{t: t, container: container, outputDir: outputDir}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer closeCancel()
		if err := container.Terminate(closeCtx); err != nil {
			t.Errorf("terminate OpenTelemetry Collector: %v", err)
		}
	})
	collector.httpEndpoint = collector.endpoint(t, "http", "4318/tcp", "")
	collector.grpcEndpoint = collector.endpoint(t, "http", "4317/tcp", "")
	collector.baseEndpoint = collector.endpoint(t, "http", "4328/tcp", "/collector")
	t.Cleanup(func() {
		if t.Failed() {
			collector.reportFailureArtifacts()
		}
	})
	return collector
}

func (c *otelCollector) endpoint(t *testing.T, scheme, port, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host, err := c.container.Host(ctx)
	if err != nil {
		t.Fatalf("resolve Collector host: %v", err)
	}
	mapped, err := c.container.MappedPort(ctx, port)
	if err != nil {
		t.Fatalf("resolve Collector port %s: %v", port, err)
	}
	return scheme + "://" + net.JoinHostPort(host, mapped.Port()) + path
}

func otelCollectorConfigPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Collector integration helper source")
	}
	return filepath.Join(filepath.Dir(source), "testdata", "otel-collector.yaml")
}

func uniqueTelemetryIdentity(t *testing.T, purpose string) service.Identity {
	t.Helper()
	return service.Identity{
		Name:      fmt.Sprintf("otel_%s_%d", purpose, telemetryIdentitySequence.Add(1)),
		Namespace: "integration",
		Version:   "test",
	}
}

func clearOTELTestEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(key, "OTEL_") {
			t.Setenv(key, "")
		}
	}
}

func (c *otelCollector) waitForServiceSignals(t *testing.T, serviceName string, want telemetrySignalSet) telemetrySignalSet {
	t.Helper()
	deadline := time.Now().Add(collectorPollTimeout)
	var stableSince time.Time
	var lastErr error
	for time.Now().Before(deadline) {
		got, err := c.serviceSignals(serviceName)
		switch {
		case err != nil:
			lastErr = err
			stableSince = time.Time{}
		case got&^want != 0:
			t.Fatalf("exported signals for %q = %s, want no signals outside %s", serviceName, got, want)
		case got == want:
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= collectorStablePeriod {
				return got
			}
		default:
			stableSince = time.Time{}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		<-timer.C
	}
	got, err := c.serviceSignals(serviceName)
	if err != nil {
		lastErr = errors.Join(lastErr, err)
	}
	t.Fatalf("Collector signals for %q did not stabilize: got %s, want %s: %v", serviceName, got, want, lastErr)
	return got
}

func (c *otelCollector) serviceSignals(serviceName string) (telemetrySignalSet, error) {
	var signals telemetrySignalSet
	logs, err := readOTLPRequests(c.sinkPath("logs"), func() *collectorlogspb.ExportLogsServiceRequest {
		return new(collectorlogspb.ExportLogsServiceRequest)
	})
	if err != nil {
		return 0, err
	}
	for _, request := range logs {
		for _, resourceLogs := range request.GetResourceLogs() {
			if resourceServiceName(resourceLogs.GetResource()) == serviceName && len(resourceLogs.GetScopeLogs()) != 0 {
				signals |= telemetryLogs
			}
		}
	}
	metrics, err := readOTLPRequests(c.sinkPath("metrics"), func() *collectormetricspb.ExportMetricsServiceRequest {
		return new(collectormetricspb.ExportMetricsServiceRequest)
	})
	if err != nil {
		return 0, err
	}
	for _, request := range metrics {
		for _, resourceMetrics := range request.GetResourceMetrics() {
			if resourceServiceName(resourceMetrics.GetResource()) == serviceName && len(resourceMetrics.GetScopeMetrics()) != 0 {
				signals |= telemetryMetrics
			}
		}
	}
	traces, err := readOTLPRequests(c.sinkPath("traces"), func() *collectortracepb.ExportTraceServiceRequest {
		return new(collectortracepb.ExportTraceServiceRequest)
	})
	if err != nil {
		return 0, err
	}
	for _, request := range traces {
		for _, resourceSpans := range request.GetResourceSpans() {
			if resourceServiceName(resourceSpans.GetResource()) == serviceName && len(resourceSpans.GetScopeSpans()) != 0 {
				signals |= telemetryTraces
			}
		}
	}
	return signals, nil
}

func (c *otelCollector) logRecords(serviceName string) ([]*logspb.LogRecord, error) {
	requests, err := readOTLPRequests(c.sinkPath("logs"), func() *collectorlogspb.ExportLogsServiceRequest {
		return new(collectorlogspb.ExportLogsServiceRequest)
	})
	if err != nil {
		return nil, err
	}
	var records []*logspb.LogRecord
	for _, request := range requests {
		for _, resourceLogs := range request.GetResourceLogs() {
			if resourceServiceName(resourceLogs.GetResource()) != serviceName {
				continue
			}
			for _, scopeLogs := range resourceLogs.GetScopeLogs() {
				records = append(records, scopeLogs.GetLogRecords()...)
			}
		}
	}
	return records, nil
}

func hasModuleLog(records []*logspb.LogRecord, modulePath string) bool {
	for _, record := range records {
		value := modulePathValue(record.GetAttributes())
		if value.GetStringValue() == modulePath {
			return true
		}
	}
	return false
}

func (c *otelCollector) moduleSignals(serviceName, modulePath string) (telemetrySignalSet, error) {
	var signals telemetrySignalSet
	logs, err := c.logRecords(serviceName)
	if err != nil {
		return 0, err
	}
	if hasModuleLog(logs, modulePath) {
		signals |= telemetryLogs
	}
	metrics, err := c.metricRecords(serviceName)
	if err != nil {
		return 0, err
	}
	for _, metric := range metrics {
		if metricHasModulePath(metric, modulePath) {
			signals |= telemetryMetrics
			break
		}
	}
	spans, err := c.spanRecords(serviceName)
	if err != nil {
		return 0, err
	}
	for _, span := range spans {
		if hasModulePathAttribute(span.GetAttributes(), modulePath) {
			signals |= telemetryTraces
			break
		}
	}
	return signals, nil
}

func (c *otelCollector) metricRecords(serviceName string) ([]*metricspb.Metric, error) {
	requests, err := readOTLPRequests(c.sinkPath("metrics"), func() *collectormetricspb.ExportMetricsServiceRequest {
		return new(collectormetricspb.ExportMetricsServiceRequest)
	})
	if err != nil {
		return nil, err
	}
	var metrics []*metricspb.Metric
	for _, request := range requests {
		for _, resourceMetrics := range request.GetResourceMetrics() {
			if resourceServiceName(resourceMetrics.GetResource()) != serviceName {
				continue
			}
			for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
				metrics = append(metrics, scopeMetrics.GetMetrics()...)
			}
		}
	}
	return metrics, nil
}

func (c *otelCollector) spanRecords(serviceName string) ([]*tracepb.Span, error) {
	requests, err := readOTLPRequests(c.sinkPath("traces"), func() *collectortracepb.ExportTraceServiceRequest {
		return new(collectortracepb.ExportTraceServiceRequest)
	})
	if err != nil {
		return nil, err
	}
	var spans []*tracepb.Span
	for _, request := range requests {
		for _, resourceSpans := range request.GetResourceSpans() {
			if resourceServiceName(resourceSpans.GetResource()) != serviceName {
				continue
			}
			for _, scopeSpans := range resourceSpans.GetScopeSpans() {
				spans = append(spans, scopeSpans.GetSpans()...)
			}
		}
	}
	return spans, nil
}

func metricHasModulePath(record *metricspb.Metric, modulePath string) bool {
	for _, point := range record.GetGauge().GetDataPoints() {
		if hasModulePathAttribute(point.GetAttributes(), modulePath) {
			return true
		}
	}
	for _, point := range record.GetSum().GetDataPoints() {
		if hasModulePathAttribute(point.GetAttributes(), modulePath) {
			return true
		}
	}
	for _, point := range record.GetHistogram().GetDataPoints() {
		if hasModulePathAttribute(point.GetAttributes(), modulePath) {
			return true
		}
	}
	for _, point := range record.GetExponentialHistogram().GetDataPoints() {
		if hasModulePathAttribute(point.GetAttributes(), modulePath) {
			return true
		}
	}
	for _, point := range record.GetSummary().GetDataPoints() {
		if hasModulePathAttribute(point.GetAttributes(), modulePath) {
			return true
		}
	}
	return false
}

func hasModulePathAttribute(attributes []*commonpb.KeyValue, want string) bool {
	return modulePathValue(attributes).GetStringValue() == want
}

func readOTLPRequests[T proto.Message](path string, newRequest func() T) ([]T, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open OTLP sink: %w", err)
	}
	defer func() { _ = file.Close() }()

	var requests []T
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		request := newRequest()
		if err := protojson.Unmarshal(line, request); err != nil {
			return nil, fmt.Errorf("decode OTLP sink: %w", err)
		}
		requests = append(requests, request)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read OTLP sink: %w", err)
	}
	return requests, nil
}

func resourceServiceName(resource *resourcepb.Resource) string {
	if resource == nil {
		return ""
	}
	for _, attr := range resource.GetAttributes() {
		if attr.GetKey() == "service.name" {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}

func modulePathValue(attributes []*commonpb.KeyValue) *commonpb.AnyValue {
	for _, attr := range attributes {
		if attr.GetKey() == "flowseer.module.path" {
			return attr.GetValue()
		}
	}
	return nil
}

func (c *otelCollector) sinkPath(signal string) string {
	return filepath.Join(c.outputDir, signal+".json")
}

func (c *otelCollector) reportFailureArtifacts() {
	artifacts := make(map[string][]byte)
	for _, signal := range []string{"logs", "metrics", "traces"} {
		content, err := readBoundedFile(c.sinkPath(signal), maximumArtifactBytes)
		if err == nil && len(content) != 0 {
			artifacts[signal+".json"] = content
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logs, err := c.container.Logs(ctx)
	if err == nil {
		defer func() { _ = logs.Close() }()
		content, readErr := io.ReadAll(io.LimitReader(logs, maximumArtifactBytes+1))
		if readErr == nil {
			artifacts["collector.log"] = content[:min(len(content), maximumArtifactBytes)]
		}
	}
	for _, content := range artifacts {
		if bytes.Contains(content, []byte(telemetrySecretSentinel)) {
			c.t.Log("Collector diagnostics withheld because the synthetic secret sentinel was present")
			return
		}
	}
	for _, name := range sortedKeys(artifacts) {
		c.t.Logf("Collector failure artifact %s:\n%s", name, artifacts[name])
	}
	destination := os.Getenv(otelArtifactDirEnv)
	if destination == "" {
		return
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		c.t.Logf("create Collector artifact directory: %v", err)
		return
	}
	for name, content := range artifacts {
		if err := os.WriteFile(filepath.Join(destination, name), content, 0o600); err != nil {
			c.t.Logf("write Collector artifact %s: %v", name, err)
		}
	}
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, limit))
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
