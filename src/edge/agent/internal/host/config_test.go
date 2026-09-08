package host_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/host"
)

const (
	testSetupKey = "fse1_abcdefghijklmnopqrstuvwxyz_abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
	testAnchor   = "\\x01\\x02\\x03\\x04\\x05\\x06\\x07\\x08\\x09\\x0a\\x0b\\x0c\\x0d\\x0e\\x0f\\x10" +
		"\\x11\\x12\\x13\\x14\\x15\\x16\\x17\\x18\\x19\\x1a\\x1b\\x1c\\x1d\\x1e\\x1f\\x20"
)

// provisioningFile writes the file an operator wrote into this edge before it
// shipped, and returns its path.
func provisioningFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "provisioning.textproto")
	content := `central_url: "https://central.example.test:8443"
setup_key: "` + testSetupKey + `"
trust_anchors: "` + testAnchor + `"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write provisioning: %v", err)
	}
	return path
}

// configFile writes an agent configuration with body appended to the two
// fields every valid file carries.
func configFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.textproto")
	content := `state_dir: "/var/lib/flowseer/agent"
provisioning_path: "` + provisioningFile(t, dir) + `"
` + body
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestAWorkingFileIsTwoLinesAndEverythingElseDefaults is the claim the schema
// makes to an operator. Each default is asserted as a value rather than as
// "not zero", because a default that silently became zero would leave the
// heartbeat freezing the lane and the backoff retrying without pause.
func TestAWorkingFileIsTwoLinesAndEverythingElseDefaults(t *testing.T) {
	cfg, err := host.LoadConfig(configFile(t, ""))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.StateDir(); got != "/var/lib/flowseer/agent" {
		t.Errorf("StateDir() = %q", got)
	}
	if got := cfg.Heartbeat(); got != 30*time.Second {
		t.Errorf("Heartbeat() = %v, want the 30s default", got)
	}
	minimum, maximum := cfg.DispatchBackoff()
	if minimum != time.Second || maximum != 30*time.Second {
		t.Errorf("DispatchBackoff() = %v/%v, want the 1s/30s defaults", minimum, maximum)
	}
	if got := cfg.LogLevel(); got != slog.LevelInfo {
		t.Errorf("LogLevel() = %v, want INFO", got)
	}
	// The bus module's defaults, deliberately passed through as zero: this
	// configuration does not own the buffer's bounds and must not invent one.
	if bytes, age := cfg.Buffer(); bytes != 0 || age != 0 {
		t.Errorf("Buffer() = %d/%v, want zero for a file that names neither", bytes, age)
	}
}

// TestTheProvisioningIsReadThroughTheConfiguredPath covers the decision that
// the configuration names the provisioning rather than restating it: these
// three values have one home, and it is not this file.
func TestTheProvisioningIsReadThroughTheConfiguredPath(t *testing.T) {
	cfg, err := host.LoadConfig(configFile(t, ""))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.CentralURL(); got != "https://central.example.test:8443" {
		t.Errorf("CentralURL() = %q, want the provisioned URL", got)
	}
	if got := cfg.SetupKey(); got != testSetupKey {
		t.Errorf("SetupKey() = %q, want the provisioned key", got)
	}
	anchors := cfg.ProvisionedAnchors()
	if len(anchors) != 1 || len(anchors[0]) != 32 {
		t.Errorf("ProvisionedAnchors() = %v, want the one 32-byte digest", anchors)
	}
}

// TestConfiguredIntervalsWin, so the defaults above are defaults rather than
// the only values this loader can produce.
func TestConfiguredIntervalsWin(t *testing.T) {
	cfg, err := host.LoadConfig(configFile(t, `intervals {
  heartbeat { seconds: 5 }
  dispatch_backoff_min { seconds: 2 }
  dispatch_backoff_max { seconds: 60 }
}
buffer { max_bytes: 1048576 max_age { seconds: 3600 } }
log_level: AGENT_LOG_LEVEL_DEBUG
`))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Heartbeat(); got != 5*time.Second {
		t.Errorf("Heartbeat() = %v, want the configured 5s", got)
	}
	minimum, maximum := cfg.DispatchBackoff()
	if minimum != 2*time.Second || maximum != time.Minute {
		t.Errorf("DispatchBackoff() = %v/%v, want the configured 2s/60s", minimum, maximum)
	}
	if bytes, age := cfg.Buffer(); bytes != 1<<20 || age != time.Hour {
		t.Errorf("Buffer() = %d/%v, want the configured 1MiB/1h", bytes, age)
	}
	if got := cfg.LogLevel(); got != slog.LevelDebug {
		t.Errorf("LogLevel() = %v, want DEBUG", got)
	}
}

// TestABackoffCeilingBelowItsFloorIsRefused. The dispatch loop would raise
// the ceiling itself rather than fail — which is right there, since a loop
// cannot ask an operator anything — so the pair has to be refused at the one
// layer where the operator can still fix it. Accepted, the agent backs off by
// an interval nobody wrote and nothing anywhere says so.
func TestABackoffCeilingBelowItsFloorIsRefused(t *testing.T) {
	_, err := host.LoadConfig(configFile(t, `intervals {
  dispatch_backoff_min { seconds: 30 }
  dispatch_backoff_max { seconds: 5 }
}
`))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("LoadConfig() code = %v (err %v), want %v", code, err, host.ErrCodeConfigInvalid)
	}

	// The same pair the right way round loads, so the rule refuses an
	// inversion rather than the fields.
	cfg, err := host.LoadConfig(configFile(t, `intervals {
  dispatch_backoff_min { seconds: 5 }
  dispatch_backoff_max { seconds: 30 }
}
`))
	if err != nil {
		t.Fatalf("LoadConfig with an ordered pair: %v", err)
	}
	if minimum, maximum := cfg.DispatchBackoff(); minimum != 5*time.Second || maximum != 30*time.Second {
		t.Errorf("DispatchBackoff() = %v/%v, want the configured 5s/30s", minimum, maximum)
	}
}

// TestAFileMissingARequiredFieldIsRefusedAtLoad, naming the field. A
// configuration is validated before anything is opened, bound or written: an
// agent that failed at its first call instead would already have started its
// telemetry and touched its state directory.
func TestAFileMissingARequiredFieldIsRefusedAtLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.textproto")
	if err := os.WriteFile(path, []byte(`provisioning_path: "`+provisioningFile(t, dir)+`"`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := host.LoadConfig(path)
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("LoadConfig() code = %v (err %v), want %v", code, err, host.ErrCodeConfigInvalid)
	}
	// The schema's own rule is what refused it, so the message names the
	// field an operator has to add.
	if got := err.Error(); !strings.Contains(got, "state_dir") {
		t.Errorf("LoadConfig() error = %q, want it to name the missing field", got)
	}
}

// TestAMissingProvisioningFileIsRefusedAtLoadRatherThanAtFirstCall. A
// deployment whose provisioning is absent can never enroll, and enrollment is
// the first thing every other part of the agent depends on.
func TestAMissingProvisioningFileIsRefusedAtLoadRatherThanAtFirstCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.textproto")
	content := `state_dir: "/var/lib/flowseer/agent"
provisioning_path: "` + filepath.Join(dir, "absent.textproto") + `"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := host.LoadConfig(path)
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigLoad {
		t.Fatalf("LoadConfig() code = %v (err %v), want %v", code, err, host.ErrCodeConfigLoad)
	}
}

// TestAProvisioningFileThatFailsItsOwnRulesIsRefused: it is validated as its
// own message, not merely parsed. A setup key of the wrong shape is a file an
// operator mistyped, and the agent would otherwise present it to central and
// read the refusal as a rejected edge.
func TestAProvisioningFileThatFailsItsOwnRulesIsRefused(t *testing.T) {
	dir := t.TempDir()
	provisioning := filepath.Join(dir, "provisioning.textproto")
	if err := os.WriteFile(provisioning, []byte(`central_url: "https://central.example.test:8443"
setup_key: "not-a-setup-key"
trust_anchors: "`+testAnchor+`"
`), 0o600); err != nil {
		t.Fatalf("write provisioning: %v", err)
	}
	path := filepath.Join(dir, "agent.textproto")
	if err := os.WriteFile(path, []byte(`state_dir: "/var/lib/flowseer/agent"
provisioning_path: "`+provisioning+`"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := host.LoadConfig(path)
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("LoadConfig() code = %v (err %v), want %v", code, err, host.ErrCodeConfigInvalid)
	}
}
