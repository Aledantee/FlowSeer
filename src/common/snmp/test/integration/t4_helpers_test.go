//go:build snmp_integration_t4

package integration

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// t4Target describes one live device the tier should verify against.
// Port defaults to 161 when omitted in the SNMP_T4_TARGETS spec.
type t4Target struct {
	Host      string
	Port      uint16
	Community string
}

// String returns a stable identifier suitable for use as a subtest
// name (slashes are tolerated by go test -run).
func (t t4Target) String() string {
	return fmt.Sprintf("%s:%d", t.Host, t.Port)
}

// Address returns the host:port form [snmp.NewSession] expects.
func (t t4Target) Address() string {
	return fmt.Sprintf("%s:%d", t.Host, t.Port)
}

// parseT4Targets parses the SNMP_T4_TARGETS env var into a slice of
// t4Target. The grammar:
//
//	targets = entry ("," entry)*
//	entry   = hostport "@" community
//	hostport = host | host ":" port | "[" ipv6 "]" | "[" ipv6 "]:" port
//
// IPv6 literals must be bracketed (`[2001:db8::1]@community` or
// `[2001:db8::1]:1161@community`), matching the convention
// [net.SplitHostPort] uses. Returns an error on any unparseable entry;
// an empty input parses to an empty slice with no error.
//
// Parser-level errors never echo the community portion of the input —
// `entry %d` plus the parsed host[:port] is enough for the operator
// to identify the bad entry, and SNMPv2 communities (even weak
// secrets) should not leak to stderr or CI logs on misconfiguration.
func parseT4Targets(s string) ([]t4Target, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []t4Target
	for i, raw := range strings.Split(s, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		at := strings.LastIndex(entry, "@")
		if at < 0 {
			return nil, fmt.Errorf("entry %d: missing '@community' suffix", i)
		}
		hostPort := strings.TrimSpace(entry[:at])
		community := strings.TrimSpace(entry[at+1:])
		if hostPort == "" {
			return nil, fmt.Errorf("entry %d: empty host", i)
		}
		if community == "" {
			return nil, fmt.Errorf("entry %d (%q): empty community", i, hostPort)
		}

		host, port, err := splitHostPortT4(hostPort)
		if err != nil {
			return nil, fmt.Errorf("entry %d (%q): %w", i, hostPort, err)
		}

		out = append(out, t4Target{Host: host, Port: port, Community: community})
	}
	return out, nil
}

// splitHostPortT4 parses a host[:port] string supporting both bare-host
// and bracketed-IPv6 forms. Port defaults to 161 when omitted. Distinct
// from [net.SplitHostPort] in that the port is optional — t4 callers
// frequently pass just the host.
func splitHostPortT4(hp string) (host string, port uint16, err error) {
	port = 161
	// Bracketed IPv6: "[addr]" or "[addr]:port".
	if strings.HasPrefix(hp, "[") {
		close := strings.IndexByte(hp, ']')
		if close < 0 {
			return "", 0, fmt.Errorf("unmatched '[' in host")
		}
		host = hp[1:close]
		if host == "" {
			return "", 0, fmt.Errorf("empty IPv6 literal")
		}
		rest := hp[close+1:]
		if rest == "" {
			return host, port, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("expected ':port' after ']', got %q", rest)
		}
		p, err := parsePortT4(rest[1:])
		if err != nil {
			return "", 0, err
		}
		return host, p, nil
	}
	// Bare IPv6 (contains multiple colons) — reject; operator must
	// bracket it. net.ParseIP catches both standard and zone-id forms
	// without false positives on hostname-with-port.
	if strings.Count(hp, ":") > 1 {
		if ip := net.ParseIP(hp); ip != nil {
			return "", 0, fmt.Errorf("IPv6 literal must be bracketed (e.g. [%s])", hp)
		}
		return "", 0, fmt.Errorf("ambiguous host:port — bracket IPv6 literals")
	}
	// host or host:port.
	if colon := strings.LastIndex(hp, ":"); colon >= 0 {
		host = hp[:colon]
		if host == "" {
			return "", 0, fmt.Errorf("empty host")
		}
		p, err := parsePortT4(hp[colon+1:])
		if err != nil {
			return "", 0, err
		}
		return host, p, nil
	}
	return hp, port, nil
}

// parsePortT4 parses a port string into a uint16 with explicit
// out-of-range rejection. strconv.ParseUint already enforces the upper
// bound via the bit-size argument; the zero check is the only addition.
func parsePortT4(s string) (uint16, error) {
	p, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	if p == 0 {
		return 0, fmt.Errorf("port 0 is not assignable")
	}
	return uint16(p), nil
}

// t4DialV2c dials the live target through the package-level
// public [snmp.NewSession] constructor with SNMPv2c + community and a
// t.Cleanup-registered Close. Dialing goes through the public
// [snmp.NewSession] constructor.
//
// No [snmp.WithTimeout] is passed here: the package's default dial
// timeout (5s × Retries+1 = 20s failure horizon) is sized
// for SNMPv3 USM discovery + high-MaxRepetitions BulkWalk against
// cheap APs, which matches t4's typical envelope. Letting the default
// apply also exercises the zero-Timeout default path live.
func t4DialV2c(t *testing.T, tg t4Target) snmp.Session {
	t.Helper()
	sess, err := snmp.NewSession(context.Background(), tg.Address(), snmp.V2c,
		snmp.WithCommunity(tg.Community),
		snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
	)
	if err != nil {
		t.Fatalf("Dial %s: %v", tg.Address(), err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}
