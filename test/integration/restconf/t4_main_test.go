//go:build yang_integration_t4

package integration

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// restconfT4TargetsEnv discovers the live ICX devices to verify
// against. Format (comma-separated):
//
//	YANG_RESTCONF_T4_TARGETS="https://host:port@user:password[,...]"
//
// Unset skips the tier with exit 0; a set-but-malformed value fails
// with exit 1. Lab devices are dialed with TLS verification disabled
// (the documented lab opt-in).
const restconfT4TargetsEnv = "YANG_RESTCONF_T4_TARGETS"

// t4Target is one live device.
type t4Target struct {
	BaseURL  string
	User     string
	Password string
}

// t4Targets is populated by TestMain.
var t4Targets []t4Target

func TestMain(m *testing.M) {
	raw := strings.TrimSpace(os.Getenv(restconfT4TargetsEnv))
	if raw == "" {
		fmt.Fprintf(os.Stderr, "[yang_integration_t4] %s unset; skipping tier (set to https://host:port@user:password,... to enable)\n", restconfT4TargetsEnv)
		os.Exit(0)
	}
	targets, err := parseT4Targets(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[yang_integration_t4] parse %s: %v\n", restconfT4TargetsEnv, err)
		os.Exit(1)
	}
	t4Targets = targets
	fmt.Fprintf(os.Stderr, "[yang_integration_t4] verifying %d RESTCONF target(s)\n", len(targets))
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
		base, creds, ok := strings.Cut(entry, "@")
		if !ok || !strings.HasPrefix(base, "http") {
			return nil, fmt.Errorf("entry %q: want https://host:port@user:password", entry)
		}
		user, pass, ok := strings.Cut(creds, ":")
		if !ok || user == "" || pass == "" {
			return nil, fmt.Errorf("entry %q: want https://host:port@user:password", entry)
		}
		out = append(out, t4Target{BaseURL: base, User: user, Password: pass})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no targets parsed")
	}
	return out, nil
}
