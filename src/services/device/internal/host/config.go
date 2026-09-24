// Package host assembles the device service: it reads what one deployment is
// from a file, obtains the certificate the edges pin, and starts the modules
// that serve them.
//
// Nothing here decides behavior. Every rule about what the service does lives
// in the packages this one wires together, so a reader who wants to know what
// happens to a mutation reads the journal, and a reader who wants to know what
// this deployment is reads the file this package parses.
package host

import (
	"log/slog"
	"os"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"

	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the host returns while reading its configuration.
var (
	// ErrCodeConfigLoad is a configuration file that cannot be read or parsed.
	ErrCodeConfigLoad = errs.NewCode("host/config-load")
	// ErrCodeConfigInvalid is a configuration that parses but fails its schema
	// rules.
	ErrCodeConfigInvalid = errs.NewCode("host/config-invalid")
)

// defaultAssertionClockSkew is how much clock difference an edge assertion may
// carry when the configuration names none. The verifier has no default of its
// own — a zero tolerance would refuse every edge whose clock is a second off —
// so this is the one interval the host supplies rather than passes through.
const defaultAssertionClockSkew = time.Minute

// Config is one deployment of the device service, loaded and validated. It is
// immutable after [LoadConfig] and safe for concurrent use.
type Config struct {
	msg *storev1.DeviceServiceConfig
}

// LoadConfig reads and validates the prototext configuration at path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeConfigLoad).Attr("path", path).Msg("read service configuration")
	}
	return parseConfig(data, path)
}

func parseConfig(data []byte, path string) (*Config, error) {
	msg := &storev1.DeviceServiceConfig{}
	if err := prototext.Unmarshal(data, msg); err != nil {
		return nil, errs.From(err).Code(ErrCodeConfigLoad).Attr("path", path).
			Msg("parse service configuration prototext")
	}
	if err := protovalidate.Validate(msg); err != nil {
		// The schema carries every rule, including the one pairing a
		// certificate with its key, so nothing is re-checked here. A
		// configuration is validated before anything is opened or bound: a
		// service that fails at its first request instead of at start is one
		// an operator finds out about from a user.
		return nil, errs.From(err).Code(ErrCodeConfigInvalid).Attr("path", path).
			Msg("service configuration fails its schema rules")
	}
	return &Config{msg: msg}, nil
}

// StateDir is the directory the service owns: the bus store, the keys it mints
// accounts with, and the certificate it serves.
func (c *Config) StateDir() string { return c.msg.GetStateDir() }

// LogLevel is how much this deployment wants written locally. Unset is INFO,
// which is what the observability convention calls the production default;
// DEBUG is what an engineer raises it to during an incident to see the
// per-call reason behind a refusal.
func (c *Config) LogLevel() slog.Level {
	switch c.msg.GetLogLevel() {
	case storev1.LogLevel_LOG_LEVEL_DEBUG:
		return slog.LevelDebug
	case storev1.LogLevel_LOG_LEVEL_WARN:
		return slog.LevelWarn
	case storev1.LogLevel_LOG_LEVEL_ERROR:
		return slog.LevelError
	case storev1.LogLevel_LOG_LEVEL_UNSPECIFIED, storev1.LogLevel_LOG_LEVEL_INFO:
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}

// RegistryPath is the prototext registry file.
func (c *Config) RegistryPath() string { return c.msg.GetRegistryPath() }

// CredentialRoot is the directory the credential files are mounted under.
func (c *Config) CredentialRoot() string { return c.msg.GetCredentialRoot() }

// APIAddress is the host:port the Connect APIs are served on.
func (c *Config) APIAddress() string { return c.msg.GetListeners().GetApi() }

// BusAddress is the host:port the bus's WebSocket listener binds.
func (c *Config) BusAddress() string { return c.msg.GetListeners().GetBus() }

// CertificateFiles names the certificate and key the deployment supplies, and
// reports whether it supplies one at all. When it does not, the host generates
// and persists a self-signed pair; the schema pairs the two fields, so either
// both are named or neither is.
func (c *Config) CertificateFiles() (certificate, key string, supplied bool) {
	listeners := c.msg.GetListeners()
	return listeners.GetCertificateFile(), listeners.GetPrivateKeyFile(), listeners.HasCertificateFile()
}

// CentralURL is the base URL an edge dials this deployment at.
func (c *Config) CentralURL() string { return c.msg.GetEdges().GetCentralUrl() }

// AssertionAudience is the value an edge puts in every assertion and this
// service checks.
func (c *Config) AssertionAudience() string { return c.msg.GetEdges().GetAssertionAudience() }

// ClusterURLs are the endpoints an edge's leaf node dials, in preference
// order.
func (c *Config) ClusterURLs() []string { return c.msg.GetEdges().GetClusterUrls() }

// AssertionClockSkew is how much clock difference an assertion may carry,
// defaulted here because the verifier treats zero as no tolerance at all.
func (c *Config) AssertionClockSkew() time.Duration {
	if skew := c.msg.GetEdges().GetAssertionClockSkew(); skew != nil {
		return skew.AsDuration()
	}
	return defaultAssertionClockSkew
}

// TelemetryEndpoint is the collector's base URL, or empty when the deployment
// runs without one.
func (c *Config) TelemetryEndpoint() string { return c.msg.GetTelemetry().GetEndpoint() }

// TelemetryHeaders are the headers sent with every export.
func (c *Config) TelemetryHeaders() map[string]string { return c.msg.GetTelemetry().GetHeaders() }

// Intervals are the background cadences, zero where the deployment named none.
//
// Zero is passed through rather than resolved here. Each component names its
// own default and applies it — the drift package holds the poll's, the
// dispatch relay the resend and sweep ones — so a default has one home
// and the schema comment an operator reads describes that one rather than a
// copy this package would have to keep in step.
type Intervals struct {
	Drift             time.Duration
	DriftReadDeadline time.Duration
	DispatchResend    time.Duration
	ReadSweep         time.Duration
	SubmissionPulse   time.Duration
	EdgeStaleAfter    time.Duration
	EdgeDormantAfter  time.Duration
	CaptureSweep      time.Duration
}

// Intervals reads the configured cadences.
func (c *Config) Intervals() Intervals {
	i := c.msg.GetIntervals()
	return Intervals{
		Drift:             i.GetDrift().AsDuration(),
		DriftReadDeadline: i.GetDriftReadDeadline().AsDuration(),
		DispatchResend:    i.GetDispatchResend().AsDuration(),
		ReadSweep:         i.GetReadSweep().AsDuration(),
		SubmissionPulse:   i.GetSubmissionPulse().AsDuration(),
		EdgeStaleAfter:    i.GetEdgeStaleAfter().AsDuration(),
		EdgeDormantAfter:  i.GetEdgeDormantAfter().AsDuration(),
		CaptureSweep:      i.GetCaptureSweep().AsDuration(),
	}
}
