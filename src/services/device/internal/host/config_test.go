package host_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/host"
)

// validConfig is the shortest file that starts a service: the three paths, the
// two listeners, and what an edge is told. Everything else has a default.
const validConfig = `
state_dir: "/var/lib/flowseer/device"
registry_path: "/etc/flowseer/registry.textproto"
credential_root: "/etc/flowseer/credentials"
listeners {
  api: "0.0.0.0:8443"
  bus: "0.0.0.0:8444"
}
edges {
  central_url: "https://central.example.test"
  assertion_audience: "flowseer-device-central"
  cluster_urls: "wss://central.example.test:8444"
}
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "central.textproto")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfigReadsTheDeployment(t *testing.T) {
	cfg, err := host.LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.StateDir(); got != "/var/lib/flowseer/device" {
		t.Errorf("StateDir = %q", got)
	}
	if got := cfg.APIAddress(); got != "0.0.0.0:8443" {
		t.Errorf("APIAddress = %q", got)
	}
	if got := cfg.AssertionAudience(); got != "flowseer-device-central" {
		t.Errorf("AssertionAudience = %q", got)
	}
	if got := cfg.ClusterURLs(); len(got) != 1 || got[0] != "wss://central.example.test:8444" {
		t.Errorf("ClusterURLs = %v", got)
	}
	if _, _, supplied := cfg.CertificateFiles(); supplied {
		t.Error("a file naming no certificate reported one")
	}
}

// The verifier treats a zero tolerance as none at all, which would refuse
// every edge whose clock is a second off, so this is the one interval the host
// resolves rather than passing through.
func TestAssertionClockSkewDefaultsWhereTheVerifierWouldNot(t *testing.T) {
	cfg, err := host.LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.AssertionClockSkew(); got != time.Minute {
		t.Fatalf("AssertionClockSkew = %v, want one minute", got)
	}

	withSkew := strings.Replace(validConfig,
		`assertion_audience: "flowseer-device-central"`,
		`assertion_audience: "flowseer-device-central"`+"\n  assertion_clock_skew { seconds: 5 }", 1)
	named, err := host.LoadConfig(writeConfig(t, withSkew))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := named.AssertionClockSkew(); got != 5*time.Second {
		t.Fatalf("AssertionClockSkew = %v, want the configured five seconds", got)
	}
}

// An unset interval stays zero here. Each component names and applies its own
// default, so resolving them here would be a second copy to keep in step with
// the first.
func TestUnsetIntervalsArePassedThroughAsZero(t *testing.T) {
	cfg, err := host.LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Intervals(); got != (host.Intervals{}) {
		t.Fatalf("Intervals = %+v, want every field zero", got)
	}
}

func TestLoadConfigRefusals(t *testing.T) {
	cases := map[string]struct {
		body string
		code errs.Code
	}{
		"not prototext": {body: "{{{", code: host.ErrCodeConfigLoad},
		"no state dir": {
			body: `registry_path: "/r" credential_root: "/c" listeners { api: "a:1" bus: "b:2" } edges { central_url: "https://c.test" assertion_audience: "a" cluster_urls: "wss://c.test" }`,
			code: host.ErrCodeConfigInvalid,
		},
		"relative state dir": {
			body: `state_dir: "var/lib" registry_path: "/r" credential_root: "/c" listeners { api: "a:1" bus: "b:2" } edges { central_url: "https://c.test" assertion_audience: "a" cluster_urls: "wss://c.test" }`,
			code: host.ErrCodeConfigInvalid,
		},
		"no listeners": {
			body: `state_dir: "/s" registry_path: "/r" credential_root: "/c" edges { central_url: "https://c.test" assertion_audience: "a" cluster_urls: "wss://c.test" }`,
			code: host.ErrCodeConfigInvalid,
		},
		"no cluster url": {
			body: `state_dir: "/s" registry_path: "/r" credential_root: "/c" listeners { api: "a:1" bus: "b:2" } edges { central_url: "https://c.test" assertion_audience: "a" }`,
			code: host.ErrCodeConfigInvalid,
		},
		// The pairing is a schema rule, so it refuses here without the host
		// spelling it: a certificate whose key is not named cannot be served,
		// and finding that out at the listener means finding it out from a
		// failed start with no edge able to connect.
		"certificate without its key": {
			body: `state_dir: "/s" registry_path: "/r" credential_root: "/c" listeners { api: "a:1" bus: "b:2" certificate_file: "/tls.crt" } edges { central_url: "https://c.test" assertion_audience: "a" cluster_urls: "wss://c.test" }`,
			code: host.ErrCodeConfigInvalid,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := host.LoadConfig(writeConfig(t, tc.body))
			if code, _ := errs.CodeOf(err); code != tc.code {
				t.Fatalf("error = %v, want code %v", err, tc.code)
			}
		})
	}
}

func TestLoadConfigReportsAMissingFile(t *testing.T) {
	_, err := host.LoadConfig(filepath.Join(t.TempDir(), "absent.textproto"))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigLoad {
		t.Fatalf("error = %v, want code %v", err, host.ErrCodeConfigLoad)
	}
}

// TestLogLevelIsConfigurable covers the field the interceptor's DEBUG
// grading depends on. Refusals are logged at DEBUG on the argument that an
// operator can raise the level when they need the detail; with the level
// hard-coded to INFO that argument was false, and the per-refusal reason an
// engineer wants during an incident could not be turned on at all.
func TestLogLevelIsConfigurable(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want slog.Level
	}{
		{"unset is info", "", slog.LevelInfo},
		{"explicit info", "log_level: LOG_LEVEL_INFO\n", slog.LevelInfo},
		{"debug", "log_level: LOG_LEVEL_DEBUG\n", slog.LevelDebug},
		{"warn", "log_level: LOG_LEVEL_WARN\n", slog.LevelWarn},
		{"error", "log_level: LOG_LEVEL_ERROR\n", slog.LevelError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := host.LoadConfig(writeConfig(t, validConfig+tc.line))
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got := cfg.LogLevel(); got != tc.want {
				t.Errorf("LogLevel() = %v, want %v", got, tc.want)
			}
		})
	}
}
