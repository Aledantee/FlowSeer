package snmp

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// defaultPort is the SNMP agent port used when target omits one.
const defaultPort uint16 = 161

// defaultDialTimeout is the per-PDU wire timeout substituted when the
// caller leaves [SessionConfig.Timeout] at zero. It is a sensible
// default (5s): snappy on a LAN while surviving
// WAN round-trips and high-MaxRepetitions bulk walks. The effective
// failure horizon a caller sees is Timeout × (Retries+1).
const defaultDialTimeout = 5 * time.Second

// defaultRetries is the retransmit count substituted when the caller
// leaves [SessionConfig.Retries] negative (the library default). It
// is 3.
const defaultRetries = 3

// Package error sentinels, exported so callers can branch on specific
// failure modes with [errors.Is].
var (
	// ErrCommunityMismatch is returned when an inbound response or trap
	// carries a community string different from the session/listener
	// configuration. The error never carries the community value
	// itself — community strings are cleartext shared secrets and must not
	// appear in error text.
	ErrCommunityMismatch = errs.Msg("response community does not match session")
	// ErrVersionMismatch is returned when an inbound response or trap
	// carries a different SNMP version than configured.
	ErrVersionMismatch = errs.Msg("response version does not match session")
	// ErrBulkUnsupported is returned by GetBulk/BulkWalk on an SNMPv1
	// session — GetBulk is a v2c/v3-only PDU.
	ErrBulkUnsupported = errs.Msg("GetBulk/BulkWalk requires SNMPv2c")
)

// NewSession constructs a [Session]. Options are applied and validated
// before any wire IO, and the target is parsed into host+port. SNMPv3
// is selected by version [V3] + [WithUSM]; the USM keys localize lazily
// on the first op via engine discovery, so NewSession performs no IO
// for v3 either.
//
// version has no default — [VersionUnset] is rejected so the protocol
// choice (and its security posture) is always visible at the call site.
//
// target accepts three forms:
//
//   - "host" — port defaults to 161
//   - "host:port"
//   - "udp://host[:port]"
func NewSession(ctx context.Context, target string, version Version, opts ...Option) (Session, error) {
	cfg := ApplyOptions(opts...)
	cfg.Version = version
	if err := cfg.USMValidationError(); err != nil {
		return nil, err
	}
	if cfg.Version == VersionUnset {
		return nil, errs.Msg("an explicit SNMP version is required")
	}
	if err := cfg.EnforceMinSecurity(); err != nil {
		return nil, err
	}

	// v3 selects the USM path: build the per-session security processor from
	// the (already-validated) USM config. Discovery is deferred to the first
	// op, so Dial stays IO-free exactly as on the v1/v2c path.
	var usm *usmContext
	if cfg.Version == V3 || cfg.USM != nil {
		if cfg.USM == nil {
			return nil, errs.Msg("SNMPv3 requires snmp.WithUSM")
		}
		u, err := newUSMContext(ctx, *cfg.USM)
		if err != nil {
			return nil, err
		}
		usm = u
	}

	host, port, err := splitTarget(target)
	if err != nil {
		return nil, err
	}
	peer, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, errs.Wrapf(err, "resolve %q", target)
	}

	timeout := defaultDialTimeout
	if cfg.Timeout > 0 {
		timeout = cfg.Timeout
	}
	retries := defaultRetries
	if cfg.Retries >= 0 {
		// Zero is a legitimate caller choice (single-shot, no retransmit);
		// only a negative value means "Backend default".
		retries = cfg.Retries
	}

	r, err := newReactor(ctx, reactorConfig{
		peer:        peer,
		validateSrc: cfg.ValidateSourceAddr,
		multiHomed:  cfg.MultiHomedPeer,
		maxInFlight: cfg.MaxInFlight,
		version:     cfg.Version,
		community:   cfg.Community,
		usm:         usm,
	})
	if err != nil {
		return nil, errs.Wrapf(err, "dial %s", target)
	}

	// Resolve the Tracer/Meter once, from the injected providers,
	// sourcing the observable in-flight/dropped gauges from the reactor.
	inst, err := newInstruments(cfg, peer.String(),
		func() int64 { return int64(r.inFlight()) },
		func() int64 { return int64(r.droppedCount()) })
	if err != nil {
		_ = r.close()
		return nil, err
	}
	r.inst = inst

	return &session{
		r:                   r,
		inst:                inst,
		version:             cfg.Version,
		community:           cfg.Community,
		communityWire:       cfg.Community.RevealString(),
		timeout:             timeout,
		retries:             retries,
		ignoreNonIncreasing: cfg.IgnoreNonIncreasing,
		maxWalkVars:         cfg.MaxWalkVars,
		maxOIDs:             cfg.MaxOIDs,
	}, nil
}

// splitTarget parses target into a host and port, applying [defaultPort]
// when the port is absent. It accepts "host", "host:port", and
// "udp://host[:port]"; a non-UDP scheme is rejected.
func splitTarget(target string) (string, uint16, error) {
	if target == "" {
		return "", 0, errs.Msg("target is empty")
	}
	body := target
	if i := strings.Index(body, "://"); i >= 0 {
		scheme := body[:i]
		if scheme != "udp" {
			return "", 0, errs.New().Attr("target", target).Msg("only udp:// scheme is supported")
		}
		body = body[i+3:]
	}
	host, port, err := net.SplitHostPort(body)
	if err != nil {
		if _, errIP := net.ResolveIPAddr("ip", body); errIP == nil || looksLikeBareHost(body) {
			return body, defaultPort, nil
		}
		return "", 0, errs.Wrapf(err, "parse target %q", target)
	}
	if port == "" {
		return host, defaultPort, nil
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", 0, errs.Wrapf(err, "invalid port in %q", target)
	}
	if p == 0 {
		return "", 0, errs.New().Attr("target", target).Msg("port 0 in target")
	}
	return host, uint16(p), nil
}

// looksLikeBareHost reports whether s is plausibly a bare host with no
// port, used as a fallback when net.SplitHostPort rejects a value that
// might simply be missing its ":port" suffix.
func looksLikeBareHost(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == ' ' || r == '/' || r == '@' {
			return false
		}
	}
	return true
}
