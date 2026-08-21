//go:build yang_integration_t4

package integration

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// gnmiT4TargetsEnv discovers the live Aruba CX (and, where enabled,
// IOS-XE) gNMI endpoints to verify against. Format (comma-separated):
//
//	YANG_GNMI_T4_TARGETS="host:port@user:password[,...]"
//
// Unset skips the tier with exit 0; a set-but-malformed value fails
// with exit 1. Lab devices are dialed with TLS verification disabled
// (the documented lab opt-in).
const gnmiT4TargetsEnv = "YANG_GNMI_T4_TARGETS"

// t4Target is one live device.
type t4Target struct {
	Addr     string
	User     string
	Password string
}

// t4Targets is populated by TestMain.
var t4Targets []t4Target

func TestMain(m *testing.M) {
	raw := strings.TrimSpace(os.Getenv(gnmiT4TargetsEnv))
	if raw == "" {
		fmt.Fprintf(os.Stderr, "[yang_integration_t4] %s unset; skipping tier (set to host:port@user:password,... to enable)\n", gnmiT4TargetsEnv)
		os.Exit(0)
	}
	targets, err := parseT4Targets(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[yang_integration_t4] parse %s: %v\n", gnmiT4TargetsEnv, err)
		os.Exit(1)
	}
	t4Targets = targets
	fmt.Fprintf(os.Stderr, "[yang_integration_t4] verifying %d gNMI target(s)\n", len(targets))
	os.Exit(m.Run())
}

// parseT4Targets parses the env contract.
func parseT4Targets(raw string) ([]t4Target, error) {
	var out []t4Target
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		addr, creds, ok := strings.Cut(entry, "@")
		if !ok || addr == "" {
			return nil, fmt.Errorf("entry %q: want host:port@user:password", entry)
		}
		user, pass, ok := strings.Cut(creds, ":")
		if !ok || user == "" || pass == "" {
			return nil, fmt.Errorf("entry %q: want host:port@user:password", entry)
		}
		out = append(out, t4Target{Addr: addr, User: user, Password: pass})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no targets parsed")
	}
	return out, nil
}
