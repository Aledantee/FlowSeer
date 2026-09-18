//go:build yang_integration_t4

package integration

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// gnmiT4TargetsEnv discovers the live gNMI endpoints to verify against
// (Arista vEOS-lab, and where enabled IOS-XE). Format (comma-separated):
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
	Password secret.Value
}

// t4Targets is populated by TestMain.
var t4Targets []t4Target

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

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
	for i, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		addr, creds, ok := strings.Cut(entry, "@")
		if !ok || addr == "" {
			return nil, fmt.Errorf("entry %d: want host:port@user:password", i+1)
		}
		user, pass, ok := strings.Cut(creds, ":")
		if !ok || user == "" || pass == "" {
			return nil, fmt.Errorf("entry %d: want host:port@user:password", i+1)
		}
		out = append(out, t4Target{Addr: addr, User: user, Password: secret.NewString(pass)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no targets parsed")
	}
	return out, nil
}
