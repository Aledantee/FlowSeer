package host_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/host"
)

// baseConfig is the shortest file that has core paths and listeners.
const baseConfig = `
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

// validConfig is the shortest file that starts a service: the three paths, the
// two listeners, what an edge is told, and the required authentication and
// authorization sections. Everything else has a default.
const validConfig = baseConfig + `
authentication {
  issuers {
    issuer: "https://auth.example.test"
    audience: "flowseer-device"
  }
}
authorization {
  endpoint: "https://authz.example.test:8081"
  store_id: "0192e6a0000070008000000000000001"
  model_id: "0192e6a0000070008000000000000002"
  preshared_key_file: "/etc/flowseer/authz.key"
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

// A named capture_sweep reaches the host as the duration it was written as, so
// the sweeper runs on the operator's cadence rather than the built-in minute.
func TestCaptureSweepIntervalIsParsed(t *testing.T) {
	withSweep := validConfig + "intervals {\n  capture_sweep { seconds: 15 }\n}\n"
	cfg, err := host.LoadConfig(writeConfig(t, withSweep))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Intervals().CaptureSweep; got != 15*time.Second {
		t.Fatalf("CaptureSweep = %v, want the configured fifteen seconds", got)
	}
}

func TestRelationshipReconcileIntervalIsParsed(t *testing.T) {
	withReconcile := validConfig + "intervals {\n  relationship_reconcile { seconds: 15 }\n}\n"
	cfg, err := host.LoadConfig(writeConfig(t, withReconcile))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Intervals().RelationshipReconcile; got != 15*time.Second {
		t.Fatalf("RelationshipReconcile = %v, want the configured fifteen seconds", got)
	}
}

func TestLoadConfigRefusals(t *testing.T) {
	cases := map[string]struct {
		body string
		code errs.Code
	}{
		"not prototext": {body: "{{{", code: host.ErrCodeConfigLoad},
		"unknown field": {
			body: strings.Replace(validConfig, "authentication {\n", "authentication {\n  ca_fle: \"/etc/ssl/certs/ca.pem\"\n", 1),
			code: host.ErrCodeConfigLoad,
		},
		"no state dir": {
			body: strings.Replace(validConfig, `state_dir: "/var/lib/flowseer/device"`, "", 1),
			code: host.ErrCodeConfigInvalid,
		},
		"relative state dir": {
			body: strings.Replace(validConfig, `state_dir: "/var/lib/flowseer/device"`, `state_dir: "var/lib"`, 1),
			code: host.ErrCodeConfigInvalid,
		},
		"no listeners": {
			body: strings.Replace(validConfig, `listeners {
  api: "0.0.0.0:8443"
  bus: "0.0.0.0:8444"
}`, "", 1),
			code: host.ErrCodeConfigInvalid,
		},
		"no cluster url": {
			body: strings.Replace(validConfig, `  cluster_urls: "wss://central.example.test:8444"`+"\n", "", 1),
			code: host.ErrCodeConfigInvalid,
		},
		// The pairing is a schema rule, so it refuses here without the host
		// spelling it: a certificate whose key is not named cannot be served,
		// and finding that out at the listener means finding it out from a
		// failed start with no edge able to connect.
		"certificate without its key": {
			body: strings.Replace(validConfig, `listeners {
  api: "0.0.0.0:8443"
  bus: "0.0.0.0:8444"
}`, `listeners {
  api: "0.0.0.0:8443"
  bus: "0.0.0.0:8444"
  certificate_file: "/tls.crt"
}`, 1),
			code: host.ErrCodeConfigInvalid,
		},
		// A capture sweep faster than a second is refused by the schema's
		// duration bound, so the host never spins the sweeper on a sub-second
		// tick that walks every session record.
		"capture sweep below one second": {
			body: validConfig + "intervals {\n  capture_sweep { nanos: 500000000 }\n}\n",
			code: host.ErrCodeConfigInvalid,
		},
		"relationship reconcile below one second": {
			body: validConfig + "intervals {\n  relationship_reconcile { nanos: 500000000 }\n}\n",
			code: host.ErrCodeConfigInvalid,
		},
		"no authentication": {
			body: baseConfig + `authorization {
  endpoint: "https://authz.example.test:8081"
  store_id: "0192e6a0000070008000000000000001"
  model_id: "0192e6a0000070008000000000000002"
  preshared_key_file: "/etc/flowseer/authz.key"
}`,
			code: host.ErrCodeConfigInvalid,
		},
		"no authorization": {
			body: baseConfig + `authentication {
  issuers {
    issuer: "https://auth.example.test"
    audience: "flowseer-device"
  }
}`,
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

// TestAListenerWithoutAPortIsRefused covers a typo that starts a
// healthy-looking service. An address with no port reads as "pick a free
// one", so the bus would listen somewhere unpredictable while every edge
// dialed the cluster_urls the same file named — with nothing logging a
// mismatch, because from the service's side nothing was wrong.
func TestAListenerWithoutAPortIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		from    string
		to      string
		refused bool
	}{
		{"bus with no port", `bus: "0.0.0.0:8444"`, `bus: "central.example.test"`, true},
		{"api with no port", `api: "0.0.0.0:8443"`, `api: "central.example.test"`, true},
		{"a port of zero is still a port", `api: "0.0.0.0:8443"`, `api: "0.0.0.0:0"`, false},
		{"ipv6 keeps its brackets", `api: "0.0.0.0:8443"`, `api: "[::1]:8443"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(validConfig, tc.from, tc.to, 1)
			if body == validConfig {
				t.Fatalf("the fixture no longer contains %q; this test is not exercising what it names", tc.from)
			}
			_, err := host.LoadConfig(writeConfig(t, body))
			if tc.refused && err == nil {
				t.Fatal("LoadConfig() error = nil, want the address without a port refused at load")
			}
			if !tc.refused && err != nil {
				t.Fatalf("LoadConfig() error = %v, want it accepted", err)
			}
		})
	}
}

func TestPlatformAdminConfigurationIgnoresReservedDevTenant(t *testing.T) {
	withAdmin := validConfig + `
platform_admin {
  issuer: "https://auth.example.test"
  organization: "org_alpha"
  subject: "admin@example.test"
  organization_claim_name: "org_id"
}
dev_tenant: "0192e6a0-0000-7000-8000-000000000001"
`
	cfg, err := host.LoadConfig(writeConfig(t, withAdmin))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	admin := cfg.PlatformAdmin()
	if admin == nil {
		t.Fatal("PlatformAdmin() returned nil")
	}
	if admin.GetIssuer() != "https://auth.example.test" {
		t.Errorf("issuer = %q, want https://auth.example.test", admin.GetIssuer())
	}
	if admin.GetOrganization() != "org_alpha" {
		t.Errorf("organization = %q, want org_alpha", admin.GetOrganization())
	}
	if admin.GetSubject() != "admin@example.test" {
		t.Errorf("subject = %q, want admin@example.test", admin.GetSubject())
	}
	if admin.GetOrganizationClaimName() != "org_id" {
		t.Errorf("organization_claim_name = %q, want org_id", admin.GetOrganizationClaimName())
	}
}

func TestOperatorAuthenticationAndAuthorization(t *testing.T) {
	accepted := baseConfig + `
platform_admin {
  issuer: "https://auth.example.test"
  organization: "org_alpha"
  subject: "admin@example.test"
  organization_claim_name: "org_id"
}
authentication {
  issuers {
    issuer: "https://auth.example.test"
    audience: "flowseer-device"
    organization_claim_name: "org_id"
  }
  ca_file: "/etc/ssl/certs/ca.pem"
}
authorization {
  endpoint: "https://authz.example.test:8081"
  store_id: "0192e6a0000070008000000000000001"
  model_id: "0192e6a0000070008000000000000002"
  preshared_key_file: "/etc/flowseer/authz.key"
  ca_file: "/etc/ssl/certs/ca.pem"
}
`
	cfg, err := host.LoadConfig(writeConfig(t, accepted))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	authn := cfg.Authentication()
	if authn == nil {
		t.Fatal("Authentication() returned nil")
	}
	if len(authn.GetIssuers()) != 1 {
		t.Fatalf("issuers count = %d, want 1", len(authn.GetIssuers()))
	}
	iss := authn.GetIssuers()[0]
	if iss.GetIssuer() != "https://auth.example.test" {
		t.Errorf("issuer = %q, want https://auth.example.test", iss.GetIssuer())
	}
	if iss.GetAudience() != "flowseer-device" {
		t.Errorf("audience = %q, want flowseer-device", iss.GetAudience())
	}
	if iss.GetOrganizationClaimName() != "org_id" {
		t.Errorf("organization_claim_name = %q, want org_id", iss.GetOrganizationClaimName())
	}
	if authn.GetCaFile() != "/etc/ssl/certs/ca.pem" {
		t.Errorf("ca_file = %q, want /etc/ssl/certs/ca.pem", authn.GetCaFile())
	}

	authz := cfg.Authorization()
	if authz == nil {
		t.Fatal("Authorization() returned nil")
	}
	if authz.GetEndpoint() != "https://authz.example.test:8081" {
		t.Errorf("endpoint = %q, want https://authz.example.test:8081", authz.GetEndpoint())
	}
	if authz.GetStoreId() != "0192e6a0000070008000000000000001" {
		t.Errorf("store_id = %q, want 0192e6a0000070008000000000000001", authz.GetStoreId())
	}
	if authz.GetModelId() != "0192e6a0000070008000000000000002" {
		t.Errorf("model_id = %q, want 0192e6a0000070008000000000000002", authz.GetModelId())
	}
	if authz.GetPresharedKeyFile() != "/etc/flowseer/authz.key" {
		t.Errorf("preshared_key_file = %q, want /etc/flowseer/authz.key", authz.GetPresharedKeyFile())
	}
	if authz.GetCaFile() != "/etc/ssl/certs/ca.pem" {
		t.Errorf("ca_file = %q, want /etc/ssl/certs/ca.pem", authz.GetCaFile())
	}

	// Each case changes one property of the accepted file, so the rule it
	// names is the only one that can refuse it. Deleting that rule from the
	// schema turns the case green, which is what makes it hold the rule. The
	// platform_admin issuer must keep naming a configured issuer, or the
	// cross-reference rule would refuse the file as well.
	type edit struct{ replace, with string }
	const (
		adminIssuer = `platform_admin {
  issuer: "https://auth.example.test"`
		platformAdminWithoutIssuer = `platform_admin {`
		platformAdminWithoutClaim  = `platform_admin {
  issuer: "https://auth.example.test"
  organization: "org_alpha"
  subject: "admin@example.test"`
		httpAdminIssuer = `platform_admin {
  issuer: "http://auth.example.test"`
		onlyIssuer = `  issuers {
    issuer: "https://auth.example.test"
    audience: "flowseer-device"
    organization_claim_name: "org_id"
  }`
		adminBlock = `platform_admin {
  issuer: "https://auth.example.test"
  organization: "org_alpha"
  subject: "admin@example.test"
  organization_claim_name: "org_id"
}
`
	)
	refusals := []struct {
		name             string
		edits            []edit
		field            string
		rule             string
		allowMessageRule string
	}{
		{
			name: "http issuer refused",
			edits: []edit{
				{`authentication {
  issuers {
    issuer: "https://auth.example.test"`, `authentication {
  issuers {
    issuer: "http://auth.example.test"`},
				{adminIssuer, httpAdminIssuer},
			},
			field: "authentication.issuers.issuer",
			rule:  "string.prefix",
		},
		{
			name:  "http endpoint refused",
			edits: []edit{{`endpoint: "https://authz.example.test:8081"`, `endpoint: "http://authz.example.test:8081"`}},
			field: "authorization.endpoint",
			rule:  "string.pattern",
		},
		{
			name:  "endpoint with path refused",
			edits: []edit{{`endpoint: "https://authz.example.test:8081"`, `endpoint: "https://authz.example.test:8081/path"`}},
			field: "authorization.endpoint",
			rule:  "string.pattern",
		},
		{
			name:  "endpoint without port refused",
			edits: []edit{{`endpoint: "https://authz.example.test:8081"`, `endpoint: "https://authz.example.test"`}},
			field: "authorization.endpoint",
			rule:  "string.pattern",
		},
		{
			name:  "relative key path refused",
			edits: []edit{{`preshared_key_file: "/etc/flowseer/authz.key"`, `preshared_key_file: "authz.key"`}},
			field: "authorization.preshared_key_file",
			rule:  "string.pattern",
		},
		{
			name: "two issuers with one URL refused",
			edits: []edit{{onlyIssuer, onlyIssuer + `
  issuers {
    issuer: "https://auth.example.test"
    audience: "flowseer-device-2"
  }`}},
			field: "authentication",
			rule:  "operator_authentication.unique_issuers",
		},
		{
			name: "platform_admin issuer no issuer names refused",
			edits: []edit{{adminIssuer, `platform_admin {
  issuer: "https://unlisted.example.test"`}},
			rule: "device_service_config.platform_admin_issuer_configured",
		},
		{
			name:             "platform_admin without issuer refused",
			edits:            []edit{{adminIssuer, platformAdminWithoutIssuer}},
			field:            "platform_admin.issuer",
			rule:             "required",
			allowMessageRule: "device_service_config.platform_admin_issuer_configured",
		},
		{
			name:  "platform_admin without organization claim name refused",
			edits: []edit{{adminBlock, platformAdminWithoutClaim + "\n}"}},
			field: "platform_admin.organization_claim_name",
			rule:  "required",
		},
		{
			name:  "empty audience refused",
			edits: []edit{{`audience: "flowseer-device"`, `audience: ""`}},
			field: "authentication.issuers.audience",
			rule:  "string.min_len",
		},
		{
			// With no issuer left there is nothing for platform_admin to name,
			// so the block goes too. The cross-reference rule is not what this
			// case is about.
			name:  "no issuers refused",
			edits: []edit{{onlyIssuer, ""}, {adminBlock, ""}},
			field: "authentication.issuers",
			rule:  "repeated.min_items",
		},
	}

	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			body := accepted
			for _, e := range tc.edits {
				next := strings.Replace(body, e.replace, e.with, 1)
				if next == body {
					t.Fatalf("replacement target %q was not found in accepted config", e.replace)
				}
				body = next
			}
			_, err := host.LoadConfig(writeConfig(t, body))
			if err == nil {
				t.Fatalf("LoadConfig() error = nil, want %s refused", tc.name)
			}
			if code, _ := errs.CodeOf(err); code != host.ErrCodeConfigInvalid {
				t.Fatalf("LoadConfig() error code = %v, want %v", code, host.ErrCodeConfigInvalid)
			}

			var validation *protovalidate.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("LoadConfig() error = %v, want a schema validation error", err)
			}
			// Every violation sits on the one field the case changed, except for
			// an explicitly allowed cross-field rule. One violation is the rule it
			// names. Rules on that field may overlap.
			ruled := false
			for _, violation := range validation.Violations {
				var names []string
				for _, element := range violation.Proto.GetField().GetElements() {
					names = append(names, element.GetFieldName())
				}
				if got := strings.Join(names, "."); got != tc.field && violation.Proto.GetRuleId() != tc.allowMessageRule {
					t.Errorf("violation %q on %q, want it on %q", violation.Proto.GetRuleId(), got, tc.field)
				}
				ruled = ruled || violation.Proto.GetRuleId() == tc.rule
			}
			if !ruled {
				t.Errorf("no violation of %q on %q: %v", tc.rule, tc.field, validation.Violations)
			}
		})
	}
}
