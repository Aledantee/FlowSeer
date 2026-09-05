package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestPreflightNormalizesTelemetryBacking(t *testing.T) {
	local := slog.New(slog.DiscardHandler)
	shutdownCalls := 0
	tracerProvider := tracenoop.NewTracerProvider()

	tests := []struct {
		name        string
		config      Config
		env         map[string]string
		wantLogs    signalBacking
		wantMetrics signalBacking
		wantTraces  signalBacking
	}{
		{
			name:        "endpoint absent",
			config:      Config{Identity: testIdentity(), Setup: testSetup(), Logger: local},
			wantLogs:    signalUnavailable,
			wantMetrics: signalUnavailable,
			wantTraces:  signalUnavailable,
		},
		{
			name:        "endpoint manages every signal",
			config:      Config{Identity: testIdentity(), Setup: testSetup()},
			env:         map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example/otlp"},
			wantLogs:    signalManaged,
			wantMetrics: signalManaged,
			wantTraces:  signalManaged,
		},
		{
			name: "injected signal replaces its managed backing",
			config: Config{
				Identity:          testIdentity(),
				Setup:             testSetup(),
				LogHandler:        slog.DiscardHandler,
				MeterProvider:     metricnoop.NewMeterProvider(),
				TracerProvider:    tracerProvider,
				TelemetryShutdown: func(context.Context) error { shutdownCalls++; return nil },
			},
			env:         map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example"},
			wantLogs:    signalInjected,
			wantMetrics: signalInjected,
			wantTraces:  signalInjected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := preflight(context.Background(), tt.config, mapLookup(tt.env))
			if err != nil {
				t.Fatalf("preflight() error: %v", err)
			}
			if got.telemetry.logs.backing != tt.wantLogs || got.telemetry.metrics.backing != tt.wantMetrics || got.telemetry.traces.backing != tt.wantTraces {
				t.Errorf("backings = %v/%v/%v, want %v/%v/%v", got.telemetry.logs.backing, got.telemetry.metrics.backing, got.telemetry.traces.backing, tt.wantLogs, tt.wantMetrics, tt.wantTraces)
			}
			if tt.config.Logger != nil && got.telemetry.localLogger != local {
				t.Error("preflight did not retain the caller's local logger")
			}
			if got.telemetry.localLogger == nil {
				t.Fatal("preflight returned a nil local logger")
			}
			if tt.config.TelemetryShutdown != nil {
				if got.telemetry.injectedShutdown == nil {
					t.Fatal("preflight dropped the aggregate injected shutdown owner")
				}
				if err := got.telemetry.injectedShutdown(context.Background()); err != nil {
					t.Fatalf("injected shutdown: %v", err)
				}
			}
		})
	}

	if shutdownCalls != 1 {
		t.Errorf("aggregate shutdown calls = %d, want 1", shutdownCalls)
	}
}

func TestNormalizeOTLPConnectionValidatesTraceSampling(t *testing.T) {
	connection, _, err := normalizeOTLPConnection(TelemetryConfig{}, mapLookup(nil))
	if err != nil {
		t.Fatalf("normalizeOTLPConnection() error: %v", err)
	}
	if got := connection.traceSampleRatio; got != defaultTraceSampleRatio {
		t.Errorf("default trace sample ratio = %v, want %v", got, defaultTraceSampleRatio)
	}

	for _, test := range []struct {
		name    string
		ratio   float64
		wantErr bool
	}{
		{name: "none", ratio: 0},
		{name: "all", ratio: 1},
		{name: "negative", ratio: -0.01, wantErr: true},
		{name: "above one", ratio: 1.01, wantErr: true},
		{name: "not a number", ratio: math.NaN(), wantErr: true},
		{name: "infinity", ratio: math.Inf(1), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, _, err := normalizeOTLPConnection(TelemetryConfig{TraceSampleRatio: &test.ratio}, mapLookup(nil))
			if test.wantErr {
				assertTelemetryConfigError(t, err, "Telemetry.TraceSampleRatio", "out_of_range", "")
				return
			}
			if err != nil {
				t.Fatalf("normalizeOTLPConnection() error: %v", err)
			}
			if got := connection.traceSampleRatio; got != test.ratio {
				t.Errorf("trace sample ratio = %v, want %v", got, test.ratio)
			}
		})
	}
}

func TestPreflightRejectsSignalSpecificOTLPEnvironment(t *testing.T) {
	settings := []string{
		"ENDPOINT",
		"PROTOCOL",
		"HEADERS",
		"TIMEOUT",
		"COMPRESSION",
		"CERTIFICATE",
		"CLIENT_CERTIFICATE",
		"CLIENT_KEY",
		"INSECURE",
	}
	for _, signal := range []string{"LOGS", "METRICS", "TRACES"} {
		for _, setting := range settings {
			key := "OTEL_EXPORTER_OTLP_" + signal + "_" + setting
			t.Run(key, func(t *testing.T) {
				_, err := preflight(context.Background(), Config{Identity: testIdentity(), Setup: testSetup()}, mapLookup(map[string]string{key: "secret-value"}))
				assertTelemetryConfigError(t, err, key, "unsupported", "secret-value")
			})
		}
	}
}

func TestPreflightExplicitTelemetryConfigOverridesEnvironment(t *testing.T) {
	got, err := preflight(context.Background(), Config{
		Identity: testIdentity(),
		Setup:    testSetup(),
		Telemetry: TelemetryConfig{
			Endpoint:    "https://go.example/base",
			Protocol:    "grpc",
			Compression: "gzip",
			Headers:     map[string]string{"authorization": "go-secret"},
			Timeout:     7 * time.Second,
		},
	}, mapLookup(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":    "https://env.example",
		"OTEL_EXPORTER_OTLP_PROTOCOL":    "http/json",
		"OTEL_EXPORTER_OTLP_COMPRESSION": "invalid",
		"OTEL_EXPORTER_OTLP_HEADERS":     "broken",
		"OTEL_EXPORTER_OTLP_TIMEOUT":     "999999",
	}))
	if err != nil {
		t.Fatalf("preflight() error: %v", err)
	}
	connection := got.telemetry.connection
	if connection.endpoint.String() != "https://go.example/base" || connection.protocol != "grpc" || connection.compression != "gzip" || connection.timeout != 7*time.Second {
		t.Errorf("normalized connection = %+v", connection)
	}
	if got := connection.headers["authorization"]; got != "go-secret" {
		t.Errorf("authorization header = %q, want Go configuration", got)
	}
}

func TestPreflightRejectsTransportReservedTelemetryHeaders(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		headers  map[string]string
		category string
	}{
		{name: "HTTP content type", headers: map[string]string{"Content-Type": "text/plain"}, category: "unsupported"},
		{name: "HTTP content encoding", headers: map[string]string{"content-encoding": "br"}, category: "unsupported"},
		{name: "gRPC reserved metadata", protocol: "grpc", headers: map[string]string{"grpc-timeout": "1S"}, category: "unsupported"},
		{name: "gRPC HTTP-only name", protocol: "grpc", headers: map[string]string{"x-api$key": "value"}, category: "malformed"},
		{name: "gRPC non-ASCII value", protocol: "grpc", headers: map[string]string{"authorization": "café"}, category: "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := preflight(context.Background(), Config{
				Identity: testIdentity(),
				Setup:    testSetup(),
				Telemetry: TelemetryConfig{
					Endpoint: "https://collector.example",
					Protocol: tt.protocol,
					Headers:  tt.headers,
				},
			}, mapLookup(nil))
			assertTelemetryConfigError(t, err, "Telemetry.Headers", tt.category, "")
		})
	}
}

func TestPreflightDerivesTransportSecurityFromEndpointScheme(t *testing.T) {
	got, err := preflight(context.Background(), Config{
		Identity: testIdentity(),
		Setup:    testSetup(),
		Telemetry: TelemetryConfig{
			Endpoint: "http://collector.example:4317",
			Protocol: "grpc",
		},
	}, mapLookup(nil))
	if err != nil {
		t.Fatalf("preflight() error: %v", err)
	}
	if !got.telemetry.connection.insecure {
		t.Fatal("HTTP endpoint did not select an insecure gRPC transport")
	}
}

func TestPreflightRejectsContradictoryHTTPTransportSecurity(t *testing.T) {
	secure := false
	insecure := true
	tests := []struct {
		name     string
		endpoint string
		value    *bool
		env      map[string]string
		setting  string
	}{
		{name: "HTTPS explicitly insecure", endpoint: "https://collector.example", value: &insecure, setting: "Telemetry.Insecure"},
		{name: "HTTP explicitly secure", endpoint: "http://collector.example", value: &secure, setting: "Telemetry.Insecure"},
		{name: "HTTPS environment insecure", endpoint: "https://collector.example", env: map[string]string{"OTEL_EXPORTER_OTLP_INSECURE": "true"}, setting: "OTEL_EXPORTER_OTLP_INSECURE"},
		{name: "HTTP environment secure", endpoint: "http://collector.example", env: map[string]string{"OTEL_EXPORTER_OTLP_INSECURE": "false"}, setting: "OTEL_EXPORTER_OTLP_INSECURE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := preflight(context.Background(), Config{
				Identity: testIdentity(),
				Setup:    testSetup(),
				Telemetry: TelemetryConfig{
					Endpoint: tt.endpoint,
					Insecure: tt.value,
				},
			}, mapLookup(tt.env))
			assertTelemetryConfigError(t, err, tt.setting, "conflict", "")
		})
	}
}

func TestPreflightPreservesExplicitGRPCTransportSecurity(t *testing.T) {
	secure := false
	insecure := true
	tests := []struct {
		name         string
		endpoint     string
		value        *bool
		wantInsecure bool
	}{
		{name: "HTTPS authority with plaintext gRPC", endpoint: "https://collector.example:4317", value: &insecure, wantInsecure: true},
		{name: "HTTP authority with TLS gRPC", endpoint: "http://collector.example:4317", value: &secure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := preflight(context.Background(), Config{
				Identity: testIdentity(),
				Setup:    testSetup(),
				Telemetry: TelemetryConfig{
					Endpoint: tt.endpoint,
					Protocol: "grpc",
					Insecure: tt.value,
				},
			}, mapLookup(nil))
			if err != nil {
				t.Fatalf("preflight() error: %v", err)
			}
			if got.telemetry.connection.insecure != tt.wantInsecure {
				t.Errorf("insecure = %t, want %t", got.telemetry.connection.insecure, tt.wantInsecure)
			}
		})
	}
}

func TestPreflightRejectsTLSMaterialForPlaintextHTTP(t *testing.T) {
	certificatePEM, keyPEM := testTLSMaterial(t)
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "certificate.pem")
	keyFile := filepath.Join(directory, "key.pem")
	writeTestFile(t, certificateFile, certificatePEM)
	writeTestFile(t, keyFile, keyPEM)

	tests := []struct {
		name      string
		telemetry TelemetryConfig
		setting   string
	}{
		{
			name: "CA certificate",
			telemetry: TelemetryConfig{
				Endpoint:        "http://collector.example",
				CertificateFile: certificateFile,
			},
			setting: "Telemetry.CertificateFile",
		},
		{
			name: "client certificate",
			telemetry: TelemetryConfig{
				Endpoint:              "http://collector.example",
				ClientCertificateFile: certificateFile,
				ClientKeyFile:         keyFile,
			},
			setting: "Telemetry.ClientTLS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := preflight(context.Background(), Config{
				Identity:  testIdentity(),
				Setup:     testSetup(),
				Telemetry: tt.telemetry,
			}, mapLookup(nil))
			assertTelemetryConfigError(t, err, tt.setting, "conflict", "")
		})
	}
}

func TestPreflightResolvesTelemetryPolicyInheritanceAndEnvironment(t *testing.T) {
	cfg := Config{
		Identity: testIdentity(),
		Telemetry: TelemetryConfig{Signals: TelemetryPolicy{
			Logs:    TelemetryDisabled,
			Metrics: TelemetryEnabled,
			Traces:  TelemetryEnabled,
		}},
		Modules: []Module{{
			Name:      "branch",
			Telemetry: TelemetryPolicy{Logs: TelemetryEnabled, Metrics: TelemetryDisabled},
			Branch: &Branch{Children: []Module{{
				Name:      "leaf",
				Telemetry: TelemetryPolicy{Metrics: TelemetryEnabled, Traces: TelemetryDisabled},
				Leaf:      &Leaf{Setup: testSetup()},
			}}},
		}},
	}
	env := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":                         "https://collector.example",
		"FLOWSEER_EDGE_TELEMETRY_LOGS_ENABLED":                "true",
		"FLOWSEER_EDGE_BRANCH_TELEMETRY_METRICS_ENABLED":      "true",
		"FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_METRICS_ENABLED": "false",
		"FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_TRACES_ENABLED":  "true",
	}

	got, err := preflight(context.Background(), cfg, mapLookup(env))
	if err != nil {
		t.Fatalf("preflight() error: %v", err)
	}
	branch := got.modules[0]
	leaf := branch.children[0]
	if got.telemetry.rootPolicy != (resolvedTelemetryPolicy{logs: true, metrics: true, traces: true}) {
		t.Errorf("root policy = %+v", got.telemetry.rootPolicy)
	}
	if branch.telemetryPolicy != (resolvedTelemetryPolicy{logs: true, metrics: true, traces: true}) {
		t.Errorf("branch policy = %+v", branch.telemetryPolicy)
	}
	if leaf.telemetryPolicy != (resolvedTelemetryPolicy{logs: true, metrics: false, traces: true}) {
		t.Errorf("leaf policy = %+v", leaf.telemetryPolicy)
	}
	if branch.telemetryDeclaration != cfg.Modules[0].Telemetry || leaf.telemetryDeclaration != cfg.Modules[0].Branch.Children[0].Telemetry {
		t.Error("planned modules did not retain their declarations")
	}
}

func TestPreflightRejectsUnavailableExplicitSignal(t *testing.T) {
	t.Run("service root", func(t *testing.T) {
		_, err := preflight(context.Background(), Config{
			Identity:  testIdentity(),
			Setup:     testSetup(),
			Telemetry: TelemetryConfig{Signals: TelemetryPolicy{Traces: TelemetryEnabled}},
		}, mapLookup(nil))
		assertTelemetryConfigError(t, err, "Telemetry.Signals.Traces", "unavailable", "")
	})

	t.Run("module", func(t *testing.T) {
		_, err := preflight(context.Background(), Config{
			Identity: testIdentity(),
			Modules: []Module{{
				Name:      "worker",
				Telemetry: TelemetryPolicy{Metrics: TelemetryEnabled},
				Leaf:      &Leaf{Setup: testSetup()},
			}},
		}, mapLookup(nil))
		assertTelemetryConfigError(t, err, "Module.Telemetry.Metrics", "unavailable", "")
	})
}

func TestPreflightValidatesTelemetryOverrideBelowDisabledGate(t *testing.T) {
	_, err := preflight(context.Background(), Config{
		Identity: testIdentity(),
		Modules: []Module{{
			Name: "branch",
			Gate: FixedGate(false),
			Branch: &Branch{Children: []Module{{
				Name: "leaf",
				Leaf: &Leaf{Setup: testSetup()},
			}}},
		}},
	}, mapLookup(map[string]string{
		"FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_LOGS_ENABLED": "yes",
	}))
	assertTelemetryConfigError(t, err, "FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_LOGS_ENABLED", "malformed", "yes")
}

func TestPreflightTelemetryConfigErrorsAreBoundedAndNonleaking(t *testing.T) {
	const secret = "telemetry-sentinel-password"
	tests := []struct {
		name     string
		config   TelemetryConfig
		env      map[string]string
		setting  string
		category string
	}{
		{name: "URL userinfo", config: TelemetryConfig{Endpoint: "https://user:" + secret + "@collector.example"}, setting: "Telemetry.Endpoint", category: "unsupported"},
		{name: "URL query", config: TelemetryConfig{Endpoint: "https://collector.example/?token=" + secret}, setting: "Telemetry.Endpoint", category: "unsupported"},
		{name: "URL fragment", config: TelemetryConfig{Endpoint: "https://collector.example/#" + secret}, setting: "Telemetry.Endpoint", category: "unsupported"},
		{name: "malformed headers", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example", "OTEL_EXPORTER_OTLP_HEADERS": "authorization=%" + secret}, setting: "OTEL_EXPORTER_OTLP_HEADERS", category: "malformed"},
		{name: "bad timeout", env: map[string]string{"OTEL_EXPORTER_OTLP_TIMEOUT": secret}, setting: "OTEL_EXPORTER_OTLP_TIMEOUT", category: "malformed"},
		{name: "timeout too large", config: TelemetryConfig{Timeout: 31 * time.Second}, setting: "Telemetry.Timeout", category: "out_of_range"},
		{name: "bad compression", config: TelemetryConfig{Compression: secret}, setting: "Telemetry.Compression", category: "unsupported"},
		{name: "bad protocol", config: TelemetryConfig{Protocol: "http/json"}, setting: "Telemetry.Protocol", category: "unsupported"},
		{name: "incomplete TLS", config: TelemetryConfig{ClientCertificateFile: secret}, setting: "Telemetry.ClientTLS", category: "incomplete"},
		{name: "bad CA certificate", config: TelemetryConfig{CertificateFile: "/missing/" + secret}, setting: "Telemetry.CertificateFile", category: "unreadable"},
		{name: "service name conflict", env: map[string]string{"OTEL_SERVICE_NAME": secret}, setting: "OTEL_SERVICE_NAME", category: "conflict"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config.Endpoint == "" && (tt.config.CertificateFile != "" || tt.config.ClientCertificateFile != "") {
				tt.config.Endpoint = "https://collector.example"
			}
			_, err := preflight(context.Background(), Config{Identity: testIdentity(), Setup: testSetup(), Telemetry: tt.config}, mapLookup(tt.env))
			assertTelemetryConfigError(t, err, tt.setting, tt.category, secret)
		})
	}
}

func TestPreflightNormalizesValidatedTLSMaterial(t *testing.T) {
	certificatePEM, keyPEM := testTLSMaterial(t)
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "certificate.pem")
	keyFile := filepath.Join(directory, "key.pem")
	writeTestFile(t, certificateFile, certificatePEM)
	writeTestFile(t, keyFile, keyPEM)

	got, err := preflight(context.Background(), Config{
		Identity: testIdentity(),
		Setup:    testSetup(),
		Telemetry: TelemetryConfig{
			Endpoint:              "https://collector.example",
			CertificateFile:       certificateFile,
			ClientCertificateFile: certificateFile,
			ClientKeyFile:         keyFile,
		},
	}, mapLookup(nil))
	if err != nil {
		t.Fatalf("preflight() error: %v", err)
	}
	if got.telemetry.connection.rootCAs == nil || got.telemetry.connection.clientCertificate == nil {
		t.Fatal("preflight did not retain validated TLS material")
	}
}

func TestPreflightRejectsMalformedTLSMaterialWithoutLeakingIt(t *testing.T) {
	const secret = "malformed-pem-password"
	certificatePEM, _ := testTLSMaterial(t)
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "certificate.pem")
	validCertificateFile := filepath.Join(directory, "valid-certificate.pem")
	keyFile := filepath.Join(directory, "key.pem")
	writeTestFile(t, certificateFile, append(certificatePEM, secret...))
	writeTestFile(t, validCertificateFile, certificatePEM)
	writeTestFile(t, keyFile, []byte(secret))

	t.Run("certificate", func(t *testing.T) {
		_, err := preflight(context.Background(), Config{
			Identity: testIdentity(),
			Setup:    testSetup(),
			Telemetry: TelemetryConfig{
				Endpoint:        "https://collector.example",
				CertificateFile: certificateFile,
			},
		}, mapLookup(nil))
		assertTelemetryConfigError(t, err, "Telemetry.CertificateFile", "malformed", secret)
	})

	t.Run("client key", func(t *testing.T) {
		_, err := preflight(context.Background(), Config{
			Identity: testIdentity(),
			Setup:    testSetup(),
			Telemetry: TelemetryConfig{
				Endpoint:              "https://collector.example",
				ClientCertificateFile: validCertificateFile,
				ClientKeyFile:         keyFile,
			},
		}, mapLookup(nil))
		assertTelemetryConfigError(t, err, "Telemetry.ClientTLS", "malformed", secret)
	})
}

func assertTelemetryConfigError(t *testing.T, err error, setting, category, secret string) {
	t.Helper()
	if err == nil {
		t.Fatal("preflight() succeeded, want telemetry configuration error")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code.String() != "service/telemetry-config" {
		t.Fatalf("error code = %q, %t, want service/telemetry-config", code, ok)
	}
	attrs := errs.Attributes(err)
	if attrs["setting"] != setting || attrs["category"] != category {
		t.Errorf("error attributes = %v, want setting=%q category=%q", attrs, setting, category)
	}
	combined := err.Error() + fmt.Sprint(attrs)
	if secret != "" && strings.Contains(combined, secret) {
		t.Errorf("error leaked supplied telemetry content: %q", combined)
	}
}

func testTLSMaterial(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "collector.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal test key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func writeTestFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}
