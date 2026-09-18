// Package host assembles the device access agent: it reads what one edge
// deployment is from a file, establishes this edge's identity, and runs the
// loops that keep it in contact with central.
//
// Nothing here decides behavior. Every rule about what the agent does lives in
// the packages this one wires together; what this package owns is the file
// that says which deployment this is, and the order the pieces come up in.
//
// It sits outside internal/ for one reason: the end-to-end test assembles a
// live central and a live agent in one process, and Go's internal rule lets
// no package import both trees, so one of the two hosts has to be reachable
// from the other's. That makes this package public without making it
// reusable. It is this application's own entry surface, not a module to build
// an agent out of: Config and LoadConfig say which deployment this is, Run
// brings it up, and Options carries the seams — the session factories and the
// clock — that a test fills where a packaged deployment leaves them nil.
package host

import (
	"log/slog"
	"os"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/durationpb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the host returns while reading its configuration.
var (
	// ErrCodeConfigLoad is a file that cannot be read or parsed.
	ErrCodeConfigLoad = errs.NewCode("agent/config-load")
	// ErrCodeConfigInvalid is a file that parses but fails its schema rules.
	ErrCodeConfigInvalid = errs.NewCode("agent/config-invalid")
)

// Defaults for the intervals a deployment leaves unset. Each is the value the
// package that consumes it would have used anyway; they are named here so a
// reader of the configuration finds them in one place, and so the schema
// comments have something true to point at.
const (
	defaultHeartbeat       = 30 * time.Second
	defaultDispatchFloor   = time.Second
	defaultDispatchCeiling = 30 * time.Second
)

// Config is one agent deployment: its own file, and the provisioning that file
// names. It is immutable after [LoadConfig] and safe for concurrent use.
type Config struct {
	msg          *agentv1.AgentConfig
	provisioning *edgev1.EdgeProvisioning
}

// LoadConfig reads and validates the prototext configuration at path, and the
// provisioning file it names.
//
// Both are read here, at start, rather than when the agent first needs them.
// An unreadable provisioning file is a deployment that can never enroll, and
// an agent that discovered that at its first call would have already started
// its telemetry, bound a receiver and written to its state directory before
// reporting a failure it could have named immediately.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeConfigLoad).Attr("path", path).Msg("read agent configuration")
	}

	msg := &agentv1.AgentConfig{}
	if err := prototext.Unmarshal(data, msg); err != nil {
		return nil, errs.From(err).Code(ErrCodeConfigLoad).Attr("path", path).
			Msg("parse agent configuration prototext")
	}
	if err := protovalidate.Validate(msg); err != nil {
		// The schema carries every rule about the file's own shape, so
		// nothing is re-checked here.
		return nil, errs.From(err).Code(ErrCodeConfigInvalid).Attr("path", path).
			Msg("agent configuration fails its schema rules")
	}

	provisioning, err := loadProvisioning(msg.GetProvisioningPath())
	if err != nil {
		return nil, err
	}
	return &Config{msg: msg, provisioning: provisioning}, nil
}

// loadProvisioning reads the file the operator wrote into this edge before it
// shipped: where central is, how to trust it, and the key that joins.
func loadProvisioning(path string) (*edgev1.EdgeProvisioning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeConfigLoad).Attr("path", path).Msg("read edge provisioning")
	}
	msg := &edgev1.EdgeProvisioning{}
	if err := prototext.Unmarshal(data, msg); err != nil {
		return nil, errs.From(err).Code(ErrCodeConfigLoad).Attr("path", path).
			Msg("parse edge provisioning prototext")
	}
	if err := protovalidate.Validate(msg); err != nil {
		return nil, errs.From(err).Code(ErrCodeConfigInvalid).Attr("path", path).
			Msg("edge provisioning fails its schema rules")
	}
	return msg, nil
}

// StateDir is the directory the agent owns: its key pair, its enrollment
// answer, and the leaf node's credential file and buffer.
func (c *Config) StateDir() string { return c.msg.GetStateDir() }

// CentralURL is the base URL this edge dials, from its provisioning.
func (c *Config) CentralURL() string { return c.provisioning.GetCentralUrl() }

// SetupKey is the one-time key this edge enrolls with, from its provisioning.
// It is read only when this edge has no enrollment yet; an enrolled edge never
// presents one again.
func (c *Config) SetupKey() string { return c.provisioning.GetSetupKey() }

// ProvisionedAnchors are the SPKI digests this edge was shipped trusting. They
// are what it pins central by until it enrolls, after which the enrollment's
// own set replaces them — so a deployment can rotate its chain without
// re-provisioning every edge in the field.
func (c *Config) ProvisionedAnchors() [][]byte { return c.provisioning.GetTrustAnchors() }

// Heartbeat is how long between heartbeats and the deadline on each one.
func (c *Config) Heartbeat() time.Duration {
	return duration(c.msg.GetIntervals().GetHeartbeat(), defaultHeartbeat)
}

// DispatchBackoff bounds the wait between attempts to reopen the dispatch
// stream.
func (c *Config) DispatchBackoff() (minimum, maximum time.Duration) {
	intervals := c.msg.GetIntervals()
	return duration(intervals.GetDispatchBackoffMin(), defaultDispatchFloor),
		duration(intervals.GetDispatchBackoffMax(), defaultDispatchCeiling)
}

// Buffer bounds what the leaf node may hold locally. A zero is the bus
// module's own default rather than "no buffer": this configuration passes the
// value through, and the module owns the default because the module owns the
// storage.
func (c *Config) Buffer() (maxBytes int64, maxAge time.Duration) {
	buffer := c.msg.GetBuffer()
	return buffer.GetMaxBytes(), buffer.GetMaxAge().AsDuration()
}

// LogLevel is how much this deployment wants written locally. Unset is INFO,
// which the observability convention calls the production default; DEBUG is
// what an engineer raises it to during an incident.
func (c *Config) LogLevel() slog.Level {
	switch c.msg.GetLogLevel() {
	case agentv1.AgentLogLevel_AGENT_LOG_LEVEL_DEBUG:
		return slog.LevelDebug
	case agentv1.AgentLogLevel_AGENT_LOG_LEVEL_WARN:
		return slog.LevelWarn
	case agentv1.AgentLogLevel_AGENT_LOG_LEVEL_ERROR:
		return slog.LevelError
	default:
		// Unspecified, info, and a level from a newer build than this one all
		// answer info: a log level nobody chose must not silence a record.
		return slog.LevelInfo
	}
}

// duration takes the configured value or the default when it is unset. A nil
// message answers zero rather than panicking, so unset and absent are the
// same case here, which is what the schema says they are.
func duration(configured *durationpb.Duration, fallback time.Duration) time.Duration {
	if d := configured.AsDuration(); d > 0 {
		return d
	}
	return fallback
}
