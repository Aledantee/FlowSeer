package snmp

import (
	"fmt"
	"time"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// Version selects the SNMP protocol version a [Session] uses on the wire.
// The zero value [VersionUnset] is invalid; callers pass the version
// to [NewSession] directly. The library does not pick a default to make
// the choice visible in code review (v1 lacks authentication entirely; v2c
// uses community strings; v3 enables USM).
type Version int

const (
	// VersionUnset is the zero value; [NewSession] rejects it.
	VersionUnset Version = iota
	// V1 selects SNMPv1.
	V1
	// V2c selects SNMPv2c (community-string based).
	V2c
	// V3 selects SNMPv3 (USM-based, see [USMConfig]).
	V3
)

// String returns the canonical name of the protocol version.
func (v Version) String() string {
	switch v {
	case VersionUnset:
		return "unset"
	case V1:
		return "v1"
	case V2c:
		return "v2c"
	case V3:
		return "v3"
	}
	return fmt.Sprintf("Version(%d)", int(v))
}

// SessionConfig is the accumulator type [Option] values apply against.
// [NewSession] and [ApplyOptions]
// share this type: each applies the same option slice and reaches the
// same effective config.
//
// Each field is set by exactly one
// exported With* function. Callers should not construct a SessionConfig
// directly — use [ApplyOptions] or pass options to [NewSession].
type SessionConfig struct {
	// Community is the SNMPv1/v2c community string. Ignored for v3.
	Community secret.Value
	// Version is the SNMP protocol version. [VersionUnset] is rejected.
	Version Version
	// Timeout is the per-PDU wire timeout. Zero means "Backend default".
	Timeout time.Duration
	// Retries is the number of retransmits the Backend issues on timeout.
	// A negative value means "Backend default".
	Retries int
	// MaxOIDs caps how many OIDs the Backend packs into a single PDU.
	// Zero means "Backend default".
	MaxOIDs int
	// USM holds the SNMPv3 USM configuration, set by [WithUSM].
	USM *USMConfig
	// MinSecurity is the minimum acceptable USM level enforced by [Dial]
	// before any wire IO. The zero value resolves to [MinSecurityAuthNoPriv].
	MinSecurity MinSecurity

	// IgnoreNonIncreasing, when true, makes Walk/BulkWalk skip a received
	// OID that is not strictly greater than the previous one rather than
	// aborting with [ErrOIDNotIncreasing]. Some HP/Juniper agents emit a
	// momentarily non-increasing OID mid-walk; this is the opt-in skip
	// mode. Backends that do not implement walk guards ignore it.
	IgnoreNonIncreasing bool
	// MaxWalkVars caps the number of varbinds a single Walk/BulkWalk yields
	// before aborting with a budget error, bounding a runaway walk against
	// a misbehaving agent. Zero means no cap.
	MaxWalkVars int
	// ValidateSourceAddr, when true, makes a Session reject a reply whose
	// UDP source address differs from the dialed peer.
	//
	// It defaults to false as a deliberate risk acceptance: many
	// production deployments place an agent behind an HA VIP or
	// a clustered device (Cisco UCS, F5, keepalived) that answers from a
	// non-dialed address, and matching strictly by source would break
	// them. Replies are matched by request-id regardless. Set this true in
	// single-address environments to close off-path reply injection.
	ValidateSourceAddr bool
	// MultiHomedPeer, when true, opens an *unconnected* UDP socket so the
	// Session accepts replies arriving from a source address other than the
	// dialed peer, matching them by request-id. Set it for agents
	// behind an HA VIP or a clustered/multi-homed front-end (Cisco UCS, F5,
	// keepalived) that answer from a non-dialed address.
	//
	// It defaults to false: the socket is connected to the peer, which lets
	// the kernel cache the route (a faster send and reliable cold-start —
	// the unconnected path occasionally drops the very first reply) and drop
	// replies from any other source for free. The connected default makes
	// ValidateSourceAddr redundant (the kernel already enforces it); that
	// option only applies on this multi-homed path.
	MultiHomedPeer bool
	// MaxInFlight bounds the per-session in-flight request registry so a
	// partition or flood cannot grow it without bound.
	// Zero means the Backend default.
	MaxInFlight int

	// TracerProvider is the OpenTelemetry tracer provider a Backend uses to
	// emit per-operation spans. It defaults to a no-op provider
	// ([ApplyOptions] never reads global OTel state); set it with
	// [WithTracerProvider]. Backends depend only on the OTel API, never the
	// SDK.
	TracerProvider trace.TracerProvider
	// MeterProvider is the OpenTelemetry meter provider a Backend uses to
	// emit metrics. It defaults to a no-op provider; set it with
	// [WithMeterProvider].
	MeterProvider metric.MeterProvider

	// usmConfigErr captures a validation error from [WithUSM] so that
	// [NewSession] can surface it before any wire IO. The field is
	// unexported because it is a transport mechanism, not a caller-
	// settable field; read it via [SessionConfig.USMValidationError].
	usmConfigErr error
}

// USMValidationError returns the validation error captured by [WithUSM],
// or nil when no USM option was applied or validation succeeded. Backend
// Dial implementations should surface this error before any wire IO.
func (c *SessionConfig) USMValidationError() error {
	if c == nil {
		return nil
	}
	return c.usmConfigErr
}

// CallConfig is the accumulator type [CallOption] values apply against.
// Each call constructs a fresh CallConfig; CallOptions never mutate a
// [SessionConfig]. Zero values mean: no override; use the session
// default.
type CallConfig struct {
	// Timeout overrides [SessionConfig.Timeout] for a single call.
	Timeout time.Duration
	// TimeoutSet records whether [Timeout] was explicitly set, so a zero
	// override is distinguishable from "no override".
	TimeoutSet bool
	// Retries overrides [SessionConfig.Retries] for a single call.
	Retries int
	// RetriesSet records whether [Retries] was explicitly set.
	RetriesSet bool
	// MaxOIDs overrides [SessionConfig.MaxOIDs] for a single call.
	MaxOIDs int
	// RowBuffer is the [Walker] row-buffer hint applied to walks issued
	// under this call config. Zero means "use the Walker default". The
	// field is reserved for [Walker].
	RowBuffer int
	// IgnoreNonIncreasing overrides [SessionConfig.IgnoreNonIncreasing] for
	// a single Walk/BulkWalk when [IgnoreNonIncreasingSet] is true.
	IgnoreNonIncreasing bool
	// IgnoreNonIncreasingSet records whether [IgnoreNonIncreasing] was
	// explicitly set, so a false override is distinguishable from "no
	// override".
	IgnoreNonIncreasingSet bool
	// MaxWalkVars overrides [SessionConfig.MaxWalkVars] for a single
	// Walk/BulkWalk. Zero means "no override".
	MaxWalkVars int
}

// Option configures a [Session] at [NewSession] time. Options are
// applied in the order they are passed; later options override earlier
// ones for the same field. Construction is commutative across distinct
// fields.
type Option func(*SessionConfig)

// CallOption overrides session-level settings for the duration of a
// single call. CallOptions never mutate the underlying [SessionConfig];
// each call constructs a fresh [CallConfig] before applying them.
type CallOption func(*CallConfig)

// ApplyOptions builds a [SessionConfig] from opts. It is the public
// counterpart used by Backend implementations that need to read option
// values from their own Dial function.
//
// [SessionConfig.Retries] is seeded to -1 so that a caller who omits
// [WithRetries] is distinguishable from one who explicitly passes
// WithRetries(0). Backends inspecting cfg.Retries should branch on
// `cfg.Retries >= 0` to honor the "negative means Backend default"
// contract documented on the field.
func ApplyOptions(opts ...Option) *SessionConfig {
	cfg := &SessionConfig{
		Retries: -1,
		// Default to explicit no-op providers: a Backend resolves a
		// never-nil, never-panic Tracer/Meter even when the caller injects
		// nothing, and the library never reaches for global OTel state.
		// WithTracerProvider/WithMeterProvider override these.
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// ApplyCallOptions builds a [CallConfig] from opts. The companion to
// [ApplyOptions], surfaced so [Session] implementations can build per-call
// configs uniformly.
func ApplyCallOptions(opts ...CallOption) *CallConfig {
	cfg := &CallConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// WithCommunity sets the SNMPv1/v2c community string. Ignored by SNMPv3
// Sessions.
func WithCommunity(community secret.Value) Option {
	return func(c *SessionConfig) { c.Community = community }
}

// WithTimeout sets the per-PDU wire timeout. A zero or negative duration
// means "use the Backend's default".
func WithTimeout(d time.Duration) Option {
	return func(c *SessionConfig) { c.Timeout = d }
}

// WithRetries sets the number of times the Backend retransmits a request
// PDU on timeout. A negative value means "use the Backend's default".
func WithRetries(n int) Option {
	return func(c *SessionConfig) { c.Retries = n }
}

// WithMaxOIDs sets the maximum number of OIDs the Backend packs into a
// single request PDU. A value of 0 means "use the Backend's default".
func WithMaxOIDs(n int) Option {
	return func(c *SessionConfig) { c.MaxOIDs = n }
}

// WithUSM selects an SNMPv3 USM configuration. The config is validated
// when this option is constructed; the resulting [Option] records both
// the config and any validation error against the [SessionConfig]. The
// [NewSession] surfaces the error before any wire IO, ensuring invalid
// combinations are caught at construction time.
func WithUSM(cfg USMConfig) Option {
	err := cfg.Validate()
	cfgCopy := cfg
	return func(c *SessionConfig) {
		c.USM = &cfgCopy
		// Record the first validation error encountered; do not clobber
		// an earlier error with a later success.
		if c.usmConfigErr == nil {
			c.usmConfigErr = err
		}
	}
}

// WithMinSecurity sets the minimum SNMPv3 security level a Backend
// Dial will accept. The default is [MinSecurityAuthNoPriv]; selecting
// [MinSecurityNoAuth] is the explicit opt-in for unauthenticated test
// configurations.
func WithMinSecurity(level MinSecurity) Option {
	return func(c *SessionConfig) { c.MinSecurity = level }
}

// WithIgnoreNonIncreasing sets the session-level walk skip mode: when v is
// true, Walk/BulkWalk skip a non-increasing OID instead of aborting with
// [ErrOIDNotIncreasing]. Last-wins across repeated options.
func WithIgnoreNonIncreasing(v bool) Option {
	return func(c *SessionConfig) { c.IgnoreNonIncreasing = v }
}

// WithMaxWalkVars caps the number of varbinds a single Walk/BulkWalk
// yields before aborting. A value <= 0 means no cap.
func WithMaxWalkVars(n int) Option {
	return func(c *SessionConfig) { c.MaxWalkVars = n }
}

// WithValidateSourceAddr toggles strict reply-source validation. See
// [SessionConfig.ValidateSourceAddr] for the default-off risk acceptance.
// It only applies when [WithMultiHomedPeer] is set; on the connected default
// the kernel already enforces the peer source.
func WithValidateSourceAddr(v bool) Option {
	return func(c *SessionConfig) { c.ValidateSourceAddr = v }
}

// WithMultiHomedPeer opens an unconnected socket so replies from a source
// address other than the dialed peer are still accepted and matched by
// request-id — needed for HA VIP / clustered / multi-homed agents that
// answer from a non-dialed address. The default (connected socket) is faster
// and has a more reliable cold-start; see [SessionConfig.MultiHomedPeer].
func WithMultiHomedPeer() Option {
	return func(c *SessionConfig) { c.MultiHomedPeer = true }
}

// WithMaxInFlight bounds the per-session in-flight request registry.
// A value <= 0 means the Backend default.
func WithMaxInFlight(n int) Option {
	return func(c *SessionConfig) { c.MaxInFlight = n }
}

// WithCallIgnoreNonIncreasing overrides the session walk skip mode for a
// single Walk/BulkWalk.
func WithCallIgnoreNonIncreasing(v bool) CallOption {
	return func(c *CallConfig) {
		c.IgnoreNonIncreasing = v
		c.IgnoreNonIncreasingSet = true
	}
}

// WithCallMaxWalkVars overrides the session walk-varbind budget for a
// single Walk/BulkWalk. A value <= 0 means "no override".
func WithCallMaxWalkVars(n int) CallOption {
	return func(c *CallConfig) { c.MaxWalkVars = n }
}

// WithTracerProvider injects the OpenTelemetry [trace.TracerProvider] a
// Backend resolves its tracer from at Dial time. A nil argument is a no-op
// (the configured or default provider is kept), so the option is safe to
// call unconditionally. Pass only an OTel-API provider; the Backend never
// imports the SDK.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *SessionConfig) {
		if tp != nil {
			c.TracerProvider = tp
		}
	}
}

// WithMeterProvider injects the OpenTelemetry [metric.MeterProvider] a
// Backend resolves its meter from at Dial time. A nil argument is a no-op.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *SessionConfig) {
		if mp != nil {
			c.MeterProvider = mp
		}
	}
}

// WithCallTimeout overrides the session timeout for a single call.
func WithCallTimeout(d time.Duration) CallOption {
	return func(c *CallConfig) {
		c.Timeout = d
		c.TimeoutSet = true
	}
}

// WithCallRetries overrides the session retry count for a single call.
func WithCallRetries(n int) CallOption {
	return func(c *CallConfig) {
		c.Retries = n
		c.RetriesSet = true
	}
}

// WithCallMaxOIDs overrides the session max-OIDs-per-PDU for a single
// call. The override only applies to calls that pack multiple OIDs into
// a single PDU.
func WithCallMaxOIDs(n int) CallOption {
	return func(c *CallConfig) { c.MaxOIDs = n }
}

// WithRowBuffer is a buffer-size hint for [Walker]; see [Walker] for
// the full semantics. The value is passed to the walker the call
// builds; a value <= 0 falls back to the package default buffer.
func WithRowBuffer(n int) CallOption {
	return func(c *CallConfig) { c.RowBuffer = n }
}

// EnforceMinSecurity checks the session's effective security level
// against the configured minimum and returns [ErrSecurityPolicy] when
// the level is below the floor. [NewSession] calls this
// before any wire IO to defend in depth against an out-of-policy
// session.
//
// The level is resolved as follows:
//
//   - When [SessionConfig.USM] is set, the level comes from
//     [USMConfig.Level] (computed from the configured auth / priv
//     protocols).
//   - When USM is nil — the typical v1/v2c case — there is no
//     authentication on the wire, so the level is treated as
//     [SecurityLevelNoAuthNoPriv].
//
// This means [MinSecurityAuthNoPriv] (the default floor) rejects v1 /
// v2c sessions unless the caller explicitly opts into the laxer policy
// via [WithMinSecurity]([MinSecurityNoAuth]).
func (c *SessionConfig) EnforceMinSecurity() error {
	if c == nil {
		return nil
	}
	floor := c.MinSecurity
	if floor == MinSecurityUnset {
		floor = MinSecurityAuthNoPriv
	}
	var level SecurityLevel
	if c.USM == nil {
		// v1 / v2c have no auth/priv on the wire — equivalent to USM's
		// noAuthNoPriv.
		level = SecurityLevelNoAuthNoPriv
	} else {
		level = c.USM.Level()
		if level == SecurityLevelUnknown {
			return errs.Wrapf(ErrSecurityPolicy,
				"USM level is invalid (auth=%s priv=%s)", c.USM.AuthProtocol, c.USM.PrivProtocol)
		}
	}
	if !meetsMinSecurity(level, floor) {
		return errs.Wrapf(ErrSecurityPolicy,
			"level %s below minimum %s", level, floor)
	}
	return nil
}

// meetsMinSecurity reports whether level satisfies the minimum floor.
// The mapping mirrors RFC 3414's secMinLevel ordering:
//
//	noAuthNoPriv < authNoPriv < authPriv
func meetsMinSecurity(level SecurityLevel, floor MinSecurity) bool {
	switch floor {
	case MinSecurityUnset, MinSecurityAuthNoPriv:
		return level == SecurityLevelAuthNoPriv || level == SecurityLevelAuthPriv
	case MinSecurityNoAuth:
		return level == SecurityLevelNoAuthNoPriv ||
			level == SecurityLevelAuthNoPriv ||
			level == SecurityLevelAuthPriv
	case MinSecurityAuthPriv:
		return level == SecurityLevelAuthPriv
	}
	return false
}

// ErrSecurityPolicy is returned by [Dial] when the active USM
// configuration's [SecurityLevel] is below the configured
// [MinSecurity] floor. Inspect the error string for the offending
// level/minimum pair.
var ErrSecurityPolicy = errs.Msg("USM security level below configured minimum")
