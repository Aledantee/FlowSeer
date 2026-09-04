package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	defaultTelemetryTimeout = 5 * time.Second
	defaultTraceSampleRatio = 0.1
	maxTelemetryTimeout     = 30 * time.Second
	maxTelemetryTLSFileSize = 1 << 20
)

var errCodeTelemetryConfig = errs.NewCode("service/telemetry-config")

// TelemetryDeclaration selects whether a telemetry signal is inherited,
// enabled, or disabled. Its zero value inherits from the parent module.
type TelemetryDeclaration uint8

const (
	// TelemetryInherit uses the effective value of the parent module. At the
	// service root, it enables an available backing and disables an unavailable one.
	TelemetryInherit TelemetryDeclaration = iota
	// TelemetryEnabled requires an injected or managed backing for the signal.
	TelemetryEnabled
	// TelemetryDisabled suppresses export for the signal without affecting local logs.
	TelemetryDisabled
)

// TelemetryPolicy declares independent log, metric, and trace emission. Its
// zero value inherits every signal and is safe to copy.
type TelemetryPolicy struct {
	// Logs controls exported log records. Local structured logging remains active.
	Logs TelemetryDeclaration
	// Metrics controls runtime and module-authored measurements.
	Metrics TelemetryDeclaration
	// Traces controls runtime and module-authored spans.
	Traces TelemetryDeclaration
}

// TelemetryConfig configures one managed OTLP destination and the service root
// signal policy. Explicitly set Go fields override their common OTLP
// environment counterparts. Callers must not mutate Headers while a Run using
// it is active.
type TelemetryConfig struct {
	// Endpoint is the common absolute HTTP or HTTPS collector URL. Empty uses
	// OTEL_EXPORTER_OTLP_ENDPOINT and leaves managed export off when that is empty.
	Endpoint string
	// Protocol is http/protobuf or grpc. Empty uses OTEL_EXPORTER_OTLP_PROTOCOL,
	// then defaults to http/protobuf.
	Protocol string
	// Compression is gzip. Empty uses OTEL_EXPORTER_OTLP_COMPRESSION and otherwise
	// leaves compression disabled.
	Compression string
	// Headers supplies common exporter headers. A non-nil map overrides
	// OTEL_EXPORTER_OTLP_HEADERS, including with an empty map.
	Headers map[string]string
	// Timeout bounds one export request. Zero uses OTEL_EXPORTER_OTLP_TIMEOUT,
	// expressed there in milliseconds, then defaults to five seconds.
	Timeout time.Duration
	// Insecure overrides OTEL_EXPORTER_OTLP_INSECURE when non-nil.
	Insecure *bool
	// CertificateFile names a PEM file containing trusted collector certificates.
	CertificateFile string
	// ClientCertificateFile names a PEM file containing the client certificate chain.
	ClientCertificateFile string
	// ClientKeyFile names a PEM file containing the client private key and must be set
	// together with ClientCertificateFile.
	ClientKeyFile string
	// TraceSampleRatio samples this fraction of new root traces. Nil selects 0.1.
	// Values must be finite and between zero and one, inclusive. Parent sampling
	// decisions always take precedence.
	TraceSampleRatio *float64
	// Signals declares the service root emission policy.
	Signals TelemetryPolicy
}

type signalBacking uint8

const (
	signalUnavailable signalBacking = iota
	signalManaged
	signalInjected
)

type normalizedLogSignal struct {
	backing signalBacking
	handler slog.Handler
}

type normalizedMetricSignal struct {
	backing  signalBacking
	provider metric.MeterProvider
}

type normalizedTraceSignal struct {
	backing  signalBacking
	provider trace.TracerProvider
}

type normalizedOTLPConnection struct {
	endpoint          *url.URL
	protocol          string
	compression       string
	headers           map[string]string
	timeout           time.Duration
	insecure          bool
	rootCAs           *x509.CertPool
	clientCertificate *tls.Certificate
	traceSampleRatio  float64
}

type resolvedTelemetryPolicy struct {
	logs    bool
	metrics bool
	traces  bool
}

type normalizedTelemetryConfig struct {
	connection       normalizedOTLPConnection
	localLogger      *slog.Logger
	logs             normalizedLogSignal
	metrics          normalizedMetricSignal
	traces           normalizedTraceSignal
	propagator       propagation.TextMapPropagator
	injectedShutdown func(context.Context) error
	rootDeclaration  TelemetryPolicy
	rootPolicy       resolvedTelemetryPolicy
}

// telemetrySetting retains a resolved string and the setting name used for
// error attribution. Unset values retain the Go field name.
type telemetrySetting struct {
	value   string
	setting string
}

// telemetryBoolSetting retains a resolved boolean and whether configuration
// supplied it explicitly instead of relying on endpoint-scheme inference.
type telemetryBoolSetting struct {
	value    bool
	explicit bool
	setting  string
}

func normalizeTelemetryConfig(config Config, envPrefix string, lookup envLookup) (normalizedTelemetryConfig, error) {
	if err := rejectSignalSpecificOTLPEnvironment(lookup); err != nil {
		return normalizedTelemetryConfig{}, err
	}
	if value, ok := lookupTelemetryEnvironment(lookup, "OTEL_SERVICE_NAME"); ok && value != "" && value != config.Identity.Name {
		return normalizedTelemetryConfig{}, telemetryConfigError("OTEL_SERVICE_NAME", "conflict")
	}

	connection, endpointConfigured, err := normalizeOTLPConnection(config.Telemetry, lookup)
	if err != nil {
		return normalizedTelemetryConfig{}, err
	}
	localLogger := config.Logger
	if localLogger == nil {
		localLogger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	propagator := config.Propagator
	if propagator == nil {
		propagator = propagation.TraceContext{}
	}

	normalized := normalizedTelemetryConfig{
		connection:       connection,
		localLogger:      localLogger,
		logs:             normalizedLogSignal{backing: backingFor(endpointConfigured, config.LogHandler != nil), handler: config.LogHandler},
		metrics:          normalizedMetricSignal{backing: backingFor(endpointConfigured, config.MeterProvider != nil), provider: config.MeterProvider},
		traces:           normalizedTraceSignal{backing: backingFor(endpointConfigured, config.TracerProvider != nil), provider: config.TracerProvider},
		propagator:       propagator,
		injectedShutdown: config.TelemetryShutdown,
		rootDeclaration:  config.Telemetry.Signals,
	}
	available := normalized.availableSignals()
	rootPolicy, err := resolveTelemetryPolicy(config.Telemetry.Signals, "Telemetry.Signals", telemetryEnvironmentBase(envPrefix), lookup, available, available)
	if err != nil {
		return normalizedTelemetryConfig{}, err
	}
	normalized.rootPolicy = rootPolicy
	return normalized, nil
}

func (c normalizedTelemetryConfig) availableSignals() resolvedTelemetryPolicy {
	return resolvedTelemetryPolicy{
		logs:    c.logs.backing != signalUnavailable,
		metrics: c.metrics.backing != signalUnavailable,
		traces:  c.traces.backing != signalUnavailable,
	}
}

func backingFor(endpointConfigured, injected bool) signalBacking {
	if injected {
		return signalInjected
	}
	if endpointConfigured {
		return signalManaged
	}
	return signalUnavailable
}

func normalizeOTLPConnection(config TelemetryConfig, lookup envLookup) (normalizedOTLPConnection, bool, error) {
	endpoint := telemetryStringSetting(config.Endpoint, "Telemetry.Endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT", lookup)
	protocol := telemetryStringSetting(config.Protocol, "Telemetry.Protocol", "OTEL_EXPORTER_OTLP_PROTOCOL", lookup)
	compression := telemetryStringSetting(config.Compression, "Telemetry.Compression", "OTEL_EXPORTER_OTLP_COMPRESSION", lookup)
	certificate := telemetryStringSetting(config.CertificateFile, "Telemetry.CertificateFile", "OTEL_EXPORTER_OTLP_CERTIFICATE", lookup)
	clientCertificate := telemetryStringSetting(config.ClientCertificateFile, "Telemetry.ClientCertificateFile", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", lookup)
	clientKey := telemetryStringSetting(config.ClientKeyFile, "Telemetry.ClientKeyFile", "OTEL_EXPORTER_OTLP_CLIENT_KEY", lookup)

	normalized := normalizedOTLPConnection{}
	if endpoint.value != "" {
		parsed, err := url.Parse(endpoint.value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" || strings.TrimSpace(endpoint.value) != endpoint.value {
			return normalizedOTLPConnection{}, false, telemetryConfigError(endpoint.setting, "malformed")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return normalizedOTLPConnection{}, false, telemetryConfigError(endpoint.setting, "unsupported")
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return normalizedOTLPConnection{}, false, telemetryConfigError(endpoint.setting, "unsupported")
		}
		normalized.endpoint = parsed
	}

	if protocol.value == "" {
		protocol.value = "http/protobuf"
	}
	if protocol.value != "http/protobuf" && protocol.value != "grpc" {
		return normalizedOTLPConnection{}, false, telemetryConfigError(protocol.setting, "unsupported")
	}
	normalized.protocol = protocol.value

	if compression.value != "" && compression.value != "gzip" {
		return normalizedOTLPConnection{}, false, telemetryConfigError(compression.setting, "unsupported")
	}
	normalized.compression = compression.value

	headers, err := normalizeTelemetryHeaders(config.Headers, protocol.value, lookup)
	if err != nil {
		return normalizedOTLPConnection{}, false, err
	}
	normalized.headers = headers

	timeout, err := normalizeTelemetryTimeout(config.Timeout, lookup)
	if err != nil {
		return normalizedOTLPConnection{}, false, err
	}
	normalized.timeout = timeout

	insecure, err := normalizeTelemetryInsecure(config.Insecure, lookup)
	if err != nil {
		return normalizedOTLPConnection{}, false, err
	}
	if !insecure.explicit && normalized.endpoint != nil {
		insecure.value = normalized.endpoint.Scheme == "http"
	}
	if insecure.explicit && normalized.protocol == "http/protobuf" && normalized.endpoint != nil && insecure.value != (normalized.endpoint.Scheme == "http") {
		return normalizedOTLPConnection{}, false, telemetryConfigError(insecure.setting, "conflict")
	}
	normalized.insecure = insecure.value
	if normalized.protocol == "http/protobuf" && normalized.endpoint != nil && normalized.endpoint.Scheme == "http" {
		if certificate.value != "" {
			return normalizedOTLPConnection{}, false, telemetryConfigError(certificate.setting, "conflict")
		}
		if clientCertificate.value != "" || clientKey.value != "" {
			setting := "Telemetry.ClientTLS"
			if strings.HasPrefix(clientCertificate.setting, "OTEL_") {
				setting = clientCertificate.setting
			} else if strings.HasPrefix(clientKey.setting, "OTEL_") {
				setting = clientKey.setting
			}
			return normalizedOTLPConnection{}, false, telemetryConfigError(setting, "conflict")
		}
	}

	rootCAs, clientTLS, err := normalizeTelemetryTLS(certificate, clientCertificate, clientKey)
	if err != nil {
		return normalizedOTLPConnection{}, false, err
	}
	if insecure.value && (rootCAs != nil || clientTLS != nil) {
		return normalizedOTLPConnection{}, false, telemetryConfigError(insecure.setting, "conflict")
	}
	normalized.rootCAs = rootCAs
	normalized.clientCertificate = clientTLS

	traceSampleRatio := defaultTraceSampleRatio
	if config.TraceSampleRatio != nil {
		traceSampleRatio = *config.TraceSampleRatio
	}
	if math.IsNaN(traceSampleRatio) || math.IsInf(traceSampleRatio, 0) || traceSampleRatio < 0 || traceSampleRatio > 1 {
		return normalizedOTLPConnection{}, false, telemetryConfigError("Telemetry.TraceSampleRatio", "out_of_range")
	}
	normalized.traceSampleRatio = traceSampleRatio

	return normalized, normalized.endpoint != nil, nil
}

// telemetryStringSetting selects a non-empty Go value before its environment
// fallback and retains the selected setting name.
func telemetryStringSetting(goValue, goSetting, environmentSetting string, lookup envLookup) telemetrySetting {
	if goValue != "" {
		return telemetrySetting{value: goValue, setting: goSetting}
	}
	if value, ok := lookupTelemetryEnvironment(lookup, environmentSetting); ok && value != "" {
		return telemetrySetting{value: value, setting: environmentSetting}
	}
	return telemetrySetting{setting: goSetting}
}

func normalizeTelemetryHeaders(configured map[string]string, protocol string, lookup envLookup) (map[string]string, error) {
	setting := "Telemetry.Headers"
	values := configured
	if configured == nil {
		value, ok := lookupTelemetryEnvironment(lookup, "OTEL_EXPORTER_OTLP_HEADERS")
		if !ok || value == "" {
			return nil, nil
		}
		setting = "OTEL_EXPORTER_OTLP_HEADERS"
		values = make(map[string]string)
		for _, member := range strings.Split(value, ",") {
			keyValue := strings.SplitN(strings.TrimSpace(member), "=", 2)
			if len(keyValue) != 2 {
				return nil, telemetryConfigError(setting, "malformed")
			}
			key, keyErr := url.QueryUnescape(keyValue[0])
			headerValue, valueErr := url.QueryUnescape(keyValue[1])
			if keyErr != nil || valueErr != nil {
				return nil, telemetryConfigError(setting, "malformed")
			}
			if _, duplicate := values[key]; duplicate {
				return nil, telemetryConfigError(setting, "malformed")
			}
			values[key] = headerValue
		}
	}

	headers := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.ToLower(key)
		if !validTelemetryHeaderName(key, protocol) || !validTelemetryHeaderValue(value, protocol) {
			return nil, telemetryConfigError(setting, "malformed")
		}
		if reservedTelemetryHeaderName(key, protocol) {
			return nil, telemetryConfigError(setting, "unsupported")
		}
		if _, duplicate := headers[key]; duplicate {
			return nil, telemetryConfigError(setting, "malformed")
		}
		headers[key] = value
	}
	return headers, nil
}

func validTelemetryHeaderName(value, protocol string) bool {
	if value == "" {
		return false
	}
	allowed := "!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyz"
	if protocol == "grpc" {
		allowed = "-_.0123456789abcdefghijklmnopqrstuvwxyz"
	}
	for _, r := range value {
		if r > unicode.MaxASCII || !strings.ContainsRune(allowed, r) {
			return false
		}
	}
	return true
}

func validTelemetryHeaderValue(value, protocol string) bool {
	for _, r := range value {
		if protocol == "http/protobuf" && r == '\t' {
			continue
		}
		if r < ' ' || r >= unicode.MaxASCII {
			return false
		}
	}
	return true
}

func reservedTelemetryHeaderName(value, protocol string) bool {
	if value == "content-type" || value == "content-encoding" {
		return true
	}
	return protocol == "grpc" && strings.HasPrefix(value, "grpc-")
}

func normalizeTelemetryTimeout(configured time.Duration, lookup envLookup) (time.Duration, error) {
	setting := "Telemetry.Timeout"
	timeout := configured
	if timeout == 0 {
		if value, ok := lookupTelemetryEnvironment(lookup, "OTEL_EXPORTER_OTLP_TIMEOUT"); ok && value != "" {
			setting = "OTEL_EXPORTER_OTLP_TIMEOUT"
			for _, digit := range value {
				if digit < '0' || digit > '9' {
					return 0, telemetryConfigError(setting, "malformed")
				}
			}
			milliseconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return 0, telemetryConfigError(setting, "malformed")
			}
			if milliseconds <= 0 || milliseconds > int64(maxTelemetryTimeout/time.Millisecond) {
				return 0, telemetryConfigError(setting, "out_of_range")
			}
			timeout = time.Duration(milliseconds) * time.Millisecond
		}
	}
	if timeout == 0 {
		return defaultTelemetryTimeout, nil
	}
	if timeout < 0 || timeout > maxTelemetryTimeout {
		return 0, telemetryConfigError(setting, "out_of_range")
	}
	return timeout, nil
}

func normalizeTelemetryInsecure(configured *bool, lookup envLookup) (telemetryBoolSetting, error) {
	setting := "Telemetry.Insecure"
	if configured != nil {
		return telemetryBoolSetting{value: *configured, explicit: true, setting: setting}, nil
	}
	value, ok := lookupTelemetryEnvironment(lookup, "OTEL_EXPORTER_OTLP_INSECURE")
	if !ok || value == "" {
		return telemetryBoolSetting{setting: setting}, nil
	}
	setting = "OTEL_EXPORTER_OTLP_INSECURE"
	switch value {
	case "true":
		return telemetryBoolSetting{value: true, explicit: true, setting: setting}, nil
	case "false":
		return telemetryBoolSetting{explicit: true, setting: setting}, nil
	default:
		return telemetryBoolSetting{}, telemetryConfigError(setting, "malformed")
	}
}

func normalizeTelemetryTLS(certificate, clientCertificate, clientKey telemetrySetting) (*x509.CertPool, *tls.Certificate, error) {
	if (clientCertificate.value == "") != (clientKey.value == "") {
		setting := "Telemetry.ClientTLS"
		if strings.HasPrefix(clientCertificate.setting, "OTEL_") {
			setting = clientCertificate.setting
		} else if strings.HasPrefix(clientKey.setting, "OTEL_") {
			setting = clientKey.setting
		}
		return nil, nil, telemetryConfigError(setting, "incomplete")
	}

	var rootCAs *x509.CertPool
	if certificate.value != "" {
		content, category := readTelemetryTLSFile(certificate.value)
		if category != "" {
			return nil, nil, telemetryConfigError(certificate.setting, category)
		}
		var ok bool
		rootCAs, ok = parseTelemetryCertificates(content)
		if !ok {
			return nil, nil, telemetryConfigError(certificate.setting, "malformed")
		}
	}

	var clientTLS *tls.Certificate
	if clientCertificate.value != "" {
		certificatePEM, category := readTelemetryTLSFile(clientCertificate.value)
		if category != "" {
			return nil, nil, telemetryConfigError(clientCertificate.setting, category)
		}
		keyPEM, category := readTelemetryTLSFile(clientKey.value)
		if category != "" {
			return nil, nil, telemetryConfigError(clientKey.setting, category)
		}
		if _, ok := parseTelemetryCertificates(certificatePEM); !ok || !hasSingleSupportedPrivateKeyPEMBlock(keyPEM) {
			return nil, nil, telemetryConfigError("Telemetry.ClientTLS", "malformed")
		}
		pair, err := tls.X509KeyPair(certificatePEM, keyPEM)
		if err != nil {
			return nil, nil, telemetryConfigError("Telemetry.ClientTLS", "malformed")
		}
		clientTLS = &pair
	}
	return rootCAs, clientTLS, nil
}

func parseTelemetryCertificates(content []byte) (*x509.CertPool, bool) {
	pool := x509.NewCertPool()
	parsed := false
	remaining := bytes.TrimSpace(content)
	for len(remaining) != 0 {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, false
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, false
		}
		certificates, err := x509.ParseCertificates(block.Bytes)
		if err != nil || len(certificates) == 0 {
			return nil, false
		}
		for _, certificate := range certificates {
			pool.AddCert(certificate)
		}
		parsed = true
		remaining = bytes.TrimSpace(rest)
	}
	return pool, parsed
}

func hasSingleSupportedPrivateKeyPEMBlock(content []byte) bool {
	remaining := bytes.TrimSpace(content)
	if !bytes.HasPrefix(remaining, []byte("-----BEGIN ")) {
		return false
	}
	block, rest := pem.Decode(remaining)
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return false
	}
	switch block.Type {
	case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
		return true
	default:
		return false
	}
}

// readTelemetryTLSFile reads one bounded TLS file. Its error category never
// includes file contents.
func readTelemetryTLSFile(path string) ([]byte, string) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "unreadable"
	}

	content, err := io.ReadAll(io.LimitReader(file, maxTelemetryTLSFileSize+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, "unreadable"
	}
	if len(content) > maxTelemetryTLSFileSize {
		return nil, "too_large"
	}
	return content, ""
}

func rejectSignalSpecificOTLPEnvironment(lookup envLookup) error {
	settings := [...]string{
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
	for _, signal := range [...]string{"LOGS", "METRICS", "TRACES"} {
		for _, setting := range settings {
			key := "OTEL_EXPORTER_OTLP_" + signal + "_" + setting
			if value, ok := lookupTelemetryEnvironment(lookup, key); ok && value != "" {
				return telemetryConfigError(key, "unsupported")
			}
		}
	}
	return nil
}

func validateTelemetryPolicy(policy TelemetryPolicy, setting string) error {
	declarations := [...]struct {
		name  string
		value TelemetryDeclaration
	}{
		{name: "Logs", value: policy.Logs},
		{name: "Metrics", value: policy.Metrics},
		{name: "Traces", value: policy.Traces},
	}
	for _, declaration := range declarations {
		if declaration.value > TelemetryDisabled {
			return telemetryConfigError(setting+"."+declaration.name, "invalid_declaration")
		}
	}
	return nil
}

func resolveTelemetryPolicies(
	modules []plannedModule,
	lookup envLookup,
	parent resolvedTelemetryPolicy,
	available resolvedTelemetryPolicy,
) ([]plannedModule, error) {
	resolved := make([]plannedModule, len(modules))
	for i, module := range modules {
		resolved[i] = module
		policy, err := resolveTelemetryPolicy(module.telemetryDeclaration, "Module.Telemetry", telemetryModuleEnvironmentBase(module.envKey), lookup, parent, available)
		if err != nil {
			return nil, err
		}
		resolved[i].telemetryPolicy = policy
		children, err := resolveTelemetryPolicies(module.children, lookup, policy, available)
		if err != nil {
			return nil, err
		}
		resolved[i].children = children
	}
	return resolved, nil
}

func resolveTelemetryPolicy(
	declaration TelemetryPolicy,
	declarationSetting string,
	environmentBase string,
	lookup envLookup,
	parent resolvedTelemetryPolicy,
	available resolvedTelemetryPolicy,
) (resolvedTelemetryPolicy, error) {
	logs, err := resolveTelemetrySignal(declaration.Logs, declarationSetting+".Logs", environmentBase+"LOGS_ENABLED", lookup, parent.logs, available.logs)
	if err != nil {
		return resolvedTelemetryPolicy{}, err
	}
	metrics, err := resolveTelemetrySignal(declaration.Metrics, declarationSetting+".Metrics", environmentBase+"METRICS_ENABLED", lookup, parent.metrics, available.metrics)
	if err != nil {
		return resolvedTelemetryPolicy{}, err
	}
	traces, err := resolveTelemetrySignal(declaration.Traces, declarationSetting+".Traces", environmentBase+"TRACES_ENABLED", lookup, parent.traces, available.traces)
	if err != nil {
		return resolvedTelemetryPolicy{}, err
	}
	return resolvedTelemetryPolicy{logs: logs, metrics: metrics, traces: traces}, nil
}

func resolveTelemetrySignal(
	declaration TelemetryDeclaration,
	declarationSetting string,
	environmentKey string,
	lookup envLookup,
	parent bool,
	available bool,
) (bool, error) {
	enabled := parent
	setting := declarationSetting
	explicit := declaration != TelemetryInherit
	switch declaration {
	case TelemetryEnabled:
		enabled = true
	case TelemetryDisabled:
		enabled = false
	}
	if value, ok := lookupTelemetryEnvironment(lookup, environmentKey); ok {
		setting = environmentKey
		explicit = true
		switch value {
		case "true":
			enabled = true
		case "false":
			enabled = false
		default:
			return false, telemetryConfigError(setting, "malformed")
		}
	}
	if enabled && !available && explicit {
		return false, telemetryConfigError(setting, "unavailable")
	}
	return enabled, nil
}

func telemetryEnvironmentBase(envPrefix string) string {
	return strings.TrimSuffix(envPrefix, "_") + "_TELEMETRY_"
}

func telemetryModuleEnvironmentBase(gateEnvironmentKey string) string {
	return strings.TrimSuffix(gateEnvironmentKey, "ENABLED") + "TELEMETRY_"
}

func lookupTelemetryEnvironment(lookup envLookup, key string) (string, bool) {
	if lookup == nil {
		return "", false
	}
	return lookup(key)
}

func telemetryConfigError(setting, category string) error {
	return errs.New().
		Code(errCodeTelemetryConfig).
		Attr("setting", setting).
		Attr("category", category).
		Msg("telemetry configuration is invalid")
}
