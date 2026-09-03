//go:build yang_integration_t4

package integration

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

// netconfT4TargetsEnv discovers the live IOS-XE devices to verify
// against. Format (comma-separated):
//
//	YANG_NETCONF_T4_TARGETS="host:port@user:password[,host:port@user:password...]"
//
// Unset skips the tier with exit 0; a set-but-malformed value fails
// with exit 1 — a misconfiguration to fix, not a soft skip. Lab
// devices are dialed with host-key verification disabled (the
// documented lab opt-in).
const netconfT4TargetsEnv = "YANG_NETCONF_T4_TARGETS"

// t4Targets is populated by TestMain.
var t4Targets []t4Target

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		fmt.Fprintln(os.Stderr, "[yang_integration_t4] skipping live tier in short mode")
		os.Exit(0)
	}
	raw := strings.TrimSpace(os.Getenv(netconfT4TargetsEnv))
	if raw == "" {
		fmt.Fprintf(os.Stderr, "[yang_integration_t4] %s unset; skipping tier (set to host:port@user:password,... to enable)\n", netconfT4TargetsEnv)
		os.Exit(0)
	}
	targets, err := parseT4Targets(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[yang_integration_t4] parse %s: %v\n", netconfT4TargetsEnv, err)
		os.Exit(1)
	}
	t4Targets = targets
	fmt.Fprintf(os.Stderr, "[yang_integration_t4] verifying %d NETCONF target(s)\n", len(targets))
	os.Exit(m.Run())
}
