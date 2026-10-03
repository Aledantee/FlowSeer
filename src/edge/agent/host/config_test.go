package host_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/host"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
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
	if got := cfg.SyslogListeners(); got != nil {
		t.Errorf("SyslogListeners() = %v, want nil for a file that names no syslog block", got)
	}
	if failures, sample := cfg.SyslogRawPolicy(); failures != 20 || sample != 100 {
		t.Errorf("SyslogRawPolicy() = %d/%d, want 20/100 default", failures, sample)
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

func TestSyslogConfig_DefaultsAndAddressOnly(t *testing.T) {
	cfg, err := host.LoadConfig(configFile(t, `syslog {
  listeners { address: "127.0.0.1:514" }
}
`))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	listeners := cfg.SyslogListeners()
	if len(listeners) != 1 {
		t.Fatalf("len(SyslogListeners()) = %d, want 1", len(listeners))
	}
	l := listeners[0]
	if l.Address != "127.0.0.1:514" {
		t.Errorf("Address = %q, want 127.0.0.1:514", l.Address)
	}
	if l.Transport != syslog.UDP {
		t.Errorf("Transport = %v, want UDP", l.Transport)
	}
	if l.Framing != "" {
		t.Errorf("Framing = %q, want empty (unset)", l.Framing)
	}
	failures, sample := cfg.SyslogRawPolicy()
	if failures != 20 || sample != 100 {
		t.Errorf("SyslogRawPolicy() = %d/%d, want 20/100 default", failures, sample)
	}
}

func TestSyslogConfig_ConfiguredNumbersWin(t *testing.T) {
	cfg, err := host.LoadConfig(configFile(t, `syslog {
  listeners { address: "127.0.0.1:514" }
  raw_failures_per_minute: 10
  raw_sample_every: 50
}
`))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	failures, sample := cfg.SyslogRawPolicy()
	if failures != 10 || sample != 50 {
		t.Errorf("SyslogRawPolicy() = %d/%d, want 10/50", failures, sample)
	}
}

func TestSyslogConfig_ListenerCountsEnforced(t *testing.T) {
	// 0 listeners
	_, err := host.LoadConfig(configFile(t, `syslog {}
`))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("0 listeners code = %v, want ErrCodeConfigInvalid", code)
	}

	// 9 listeners
	var nineListeners strings.Builder
	nineListeners.WriteString("syslog {\n")
	for i := 1; i <= 9; i++ {
		nineListeners.WriteString(`  listeners { address: "127.0.0.1:51` + string(rune('0'+i)) + `" }` + "\n")
	}
	nineListeners.WriteString("}\n")
	_, err = host.LoadConfig(configFile(t, nineListeners.String()))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("9 listeners code = %v, want ErrCodeConfigInvalid", code)
	}
}

func TestSyslogConfig_TransportAndFramingMapping(t *testing.T) {
	cases := []struct {
		name          string
		block         string
		wantTransport syslog.Transport
		wantFraming   syslog.Framing
	}{
		{
			name:          "transport unset defaults to UDP",
			block:         `listeners { address: "127.0.0.1:514" }`,
			wantTransport: syslog.UDP,
			wantFraming:   "",
		},
		{
			name:          "explicit UDP",
			block:         `listeners { address: "127.0.0.1:514" transport: AGENT_SYSLOG_TRANSPORT_UDP }`,
			wantTransport: syslog.UDP,
			wantFraming:   "",
		},
		{
			name:          "TCP with unset framing",
			block:         `listeners { address: "127.0.0.1:601" transport: AGENT_SYSLOG_TRANSPORT_TCP }`,
			wantTransport: syslog.TCP,
			wantFraming:   "",
		},
		{
			name:          "TCP with auto framing",
			block:         `listeners { address: "127.0.0.1:601" transport: AGENT_SYSLOG_TRANSPORT_TCP framing: AGENT_SYSLOG_FRAMING_AUTO }`,
			wantTransport: syslog.TCP,
			wantFraming:   syslog.Auto,
		},
		{
			name:          "TCP with octet counting framing",
			block:         `listeners { address: "127.0.0.1:601" transport: AGENT_SYSLOG_TRANSPORT_TCP framing: AGENT_SYSLOG_FRAMING_OCTET_COUNTING }`,
			wantTransport: syslog.TCP,
			wantFraming:   syslog.OctetCounting,
		},
		{
			name:          "TCP with LF framing",
			block:         `listeners { address: "127.0.0.1:601" transport: AGENT_SYSLOG_TRANSPORT_TCP framing: AGENT_SYSLOG_FRAMING_LF }`,
			wantTransport: syslog.TCP,
			wantFraming:   syslog.LF,
		},
		{
			name:          "TCP with CRLF framing",
			block:         `listeners { address: "127.0.0.1:601" transport: AGENT_SYSLOG_TRANSPORT_TCP framing: AGENT_SYSLOG_FRAMING_CRLF }`,
			wantTransport: syslog.TCP,
			wantFraming:   syslog.CRLF,
		},
		{
			name:          "TCP with NUL framing",
			block:         `listeners { address: "127.0.0.1:601" transport: AGENT_SYSLOG_TRANSPORT_TCP framing: AGENT_SYSLOG_FRAMING_NUL }`,
			wantTransport: syslog.TCP,
			wantFraming:   syslog.NUL,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := host.LoadConfig(configFile(t, "syslog {\n"+tc.block+"\n}\n"))
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			listeners := cfg.SyslogListeners()
			if len(listeners) != 1 {
				t.Fatalf("len(listeners) = %d, want 1", len(listeners))
			}
			if listeners[0].Transport != tc.wantTransport {
				t.Errorf("Transport = %v, want %v", listeners[0].Transport, tc.wantTransport)
			}
			if listeners[0].Framing != tc.wantFraming {
				t.Errorf("Framing = %v, want %v", listeners[0].Framing, tc.wantFraming)
			}
		})
	}
}

func TestSyslogConfig_FramingOnNonTCPRefused(t *testing.T) {
	// Framing on unset transport (which is UDP)
	_, err := host.LoadConfig(configFile(t, `syslog {
  listeners { address: "127.0.0.1:514" framing: AGENT_SYSLOG_FRAMING_LF }
}
`))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("framing on unset transport code = %v, want ErrCodeConfigInvalid", code)
	}

	// Framing on explicit UDP
	_, err = host.LoadConfig(configFile(t, `syslog {
  listeners { address: "127.0.0.1:514" transport: AGENT_SYSLOG_TRANSPORT_UDP framing: AGENT_SYSLOG_FRAMING_LF }
}
`))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("framing on explicit UDP code = %v, want ErrCodeConfigInvalid", code)
	}
}

func TestSyslogConfig_ZeroNumbersRefused(t *testing.T) {
	_, err := host.LoadConfig(configFile(t, `syslog {
  listeners { address: "127.0.0.1:514" }
  raw_failures_per_minute: 0
}
`))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("raw_failures_per_minute 0 code = %v, want ErrCodeConfigInvalid", code)
	}

	_, err = host.LoadConfig(configFile(t, `syslog {
  listeners { address: "127.0.0.1:514" }
  raw_sample_every: 0
}
`))
	if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
		t.Fatalf("raw_sample_every 0 code = %v, want ErrCodeConfigInvalid", code)
	}
}

