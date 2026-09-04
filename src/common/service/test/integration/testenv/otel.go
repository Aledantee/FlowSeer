//go:build service_otel_integration

// Package testenv owns external process and environment setup for service
// integration tests.
package testenv

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const otelCollectorImage = "otel/opentelemetry-collector-contrib:0.160.0@sha256:799dc6cf12c96192af37b5bdba804da8c10b3bc563b43cb90c3f3c58d9572ad6"

// OTelCollector is one test-owned Collector and its resolved endpoints.
type OTelCollector struct {
	container    testcontainers.Container
	outputDir    string
	httpEndpoint string
	grpcEndpoint string
	baseEndpoint string
}

// StartOTelCollector starts the repository-pinned Collector fixture and
// registers bounded cleanup with t.
func StartOTelCollector(t *testing.T) *OTelCollector {
	t.Helper()
	if testing.Short() {
		t.Skip("Collector integration test")
	}

	outputDir := t.TempDir()
	if err := os.Chmod(outputDir, 0o777); err != nil {
		t.Fatalf("make Collector output directory writable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	instance, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        otelCollectorImage,
			ExposedPorts: []string{"4317/tcp", "4318/tcp", "4328/tcp", "13133/tcp"},
			Cmd:          []string{"--config=/etc/otelcol-contrib/config.yaml"},
			Files: []testcontainers.ContainerFile{{
				HostFilePath:      otelCollectorConfigPath(t),
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
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer closeCancel()
		if err := instance.Terminate(closeCtx); err != nil {
			t.Errorf("terminate OpenTelemetry Collector: %v", err)
		}
	})

	collector := &OTelCollector{container: instance, outputDir: outputDir}
	collector.httpEndpoint = collector.endpoint(t, "http", "4318/tcp", "")
	collector.grpcEndpoint = collector.endpoint(t, "http", "4317/tcp", "")
	collector.baseEndpoint = collector.endpoint(t, "http", "4328/tcp", "/collector")
	return collector
}

// ClearOTEL clears inherited OpenTelemetry environment settings for a test.
func ClearOTEL(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(key, "OTEL_") {
			t.Setenv(key, "")
		}
	}
}

// HTTPEndpoint returns the common OTLP/HTTP endpoint.
func (c *OTelCollector) HTTPEndpoint() string { return c.httpEndpoint }

// GRPCEndpoint returns the common OTLP/gRPC endpoint.
func (c *OTelCollector) GRPCEndpoint() string { return c.grpcEndpoint }

// BaseEndpoint returns the HTTP endpoint whose base path exercises signal path derivation.
func (c *OTelCollector) BaseEndpoint() string { return c.baseEndpoint }

// OutputDir returns the test-owned Collector sink directory.
func (c *OTelCollector) OutputDir() string { return c.outputDir }

// Logs opens the Collector's container log stream.
func (c *OTelCollector) Logs(ctx context.Context) (io.ReadCloser, error) {
	return c.container.Logs(ctx)
}

func (c *OTelCollector) endpoint(t *testing.T, scheme, port, path string) string {
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
	return filepath.Join(filepath.Dir(source), "..", "testdata", "otel-collector.yaml")
}
