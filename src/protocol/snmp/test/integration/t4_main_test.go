//go:build snmp_integration_t4

package integration

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

// snmpT4TargetsEnv is the environment variable t4 reads to discover
// which live devices to verify against. Format:
//
//	SNMP_T4_TARGETS="host[:port]@community[,host[:port]@community,...]"
//
// Examples:
//
//	SNMP_T4_TARGETS="10.20.0.219@tegi"
//	SNMP_T4_TARGETS="10.20.0.219:161@tegi,10.20.0.1@tegi"
const snmpT4TargetsEnv = "SNMP_T4_TARGETS"

// t4Targets is the parsed target list, populated by TestMain and read
// by t4_manual_verify_test.go.
var t4Targets []t4Target

// TestMain owns the t4 tier's opt-in behavior. Unlike t1/t2/t3 there
// is no container lifecycle to manage — the live device is operator-
// supplied. When SNMP_T4_TARGETS is unset, the tier exits 0 with a
// skip note so 'make t4' (and any CI workflow that opportunistically
// runs the t4 build tag) does not fail in environments without a
// reachable device.
//
// When SNMP_T4_TARGETS is set but unparseable, the tier exits 1 with
// a diagnostic — that is a misconfiguration the operator should fix,
// not a soft skip.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	raw := strings.TrimSpace(os.Getenv(snmpT4TargetsEnv))
	if raw == "" {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t4] %s unset; skipping tier (set to host[:port]@community,... to enable)\n", snmpT4TargetsEnv)
		os.Exit(0)
	}

	targets, err := parseT4Targets(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t4] parse %s: %v\n", snmpT4TargetsEnv, err)
		os.Exit(1)
	}
	if len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t4] %s parsed to zero targets; nothing to verify\n", snmpT4TargetsEnv)
		os.Exit(1)
	}
	t4Targets = targets
	fmt.Fprintf(os.Stderr, "[snmp_integration_t4] verifying %d target(s)\n", len(t4Targets))

	os.Exit(m.Run())
}
