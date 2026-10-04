package integration_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"buf.build/go/protovalidate"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
	"go.aledante.io/FlowSeer/src/services/device/internal/credential"
	"go.aledante.io/FlowSeer/src/services/device/internal/host"
)

// labFixturePath is where one of the files a lab run is assembled from lives.
func labFixturePath(name string) string {
	// Five levels up: integration, test, device, services, src.
	return filepath.Join("..", "..", "..", "..", "..", "deploy", "lab", name)
}

// labFixture is one of the files a lab run is assembled from.
func labFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(labFixturePath(name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return body
}

// The lab deployment files parse as the messages they are for.
//
// They are prototext with no compiler behind them, so a mistyped field name
// or an unbalanced brace is found by whoever runs them — which, for these
// four, is someone standing in front of a powered-on switch with an approval
// that names a date. This is the cheapest possible place to find it instead.
//
// Parsing only. Three of the four carry placeholders that are meant to fail
// their schema rules, and the next test is about that. The central file is the
// exception, and the test after that one holds it to its schema.
func TestTheLabFixturesParse(t *testing.T) {
	t.Parallel()

	for name, msg := range map[string]proto.Message{
		"central.textproto":      &storev1.DeviceServiceConfig{},
		"registry.textproto":     &storev1.DeviceRegistry{},
		"agent.textproto":        &agentv1.AgentConfig{},
		"provisioning.textproto": &edgev1.EdgeProvisioning{},
	} {
		if err := prototext.Unmarshal(labFixture(t, name), msg); err != nil {
			t.Errorf("deploy/lab/%s does not parse: %v", name, err)
		}
	}
}

// The provisioning fixture's placeholders are refused, which is the whole
// reason they are shaped the way they are.
//
// That file is the only lab artifact that ever holds a credential. Its
// placeholder setup key and trust anchor are deliberately invalid — the key
// fails the schema's pattern and the anchor is not a 32-byte digest — so an
// agent handed the file unedited refuses it at load, naming the field, rather
// than starting and failing later somewhere that looks like a different
// problem. A file that fails loudly when unfilled is worth more than one that
// looks filled.
//
// This asserts that property rather than trusting the placeholders to stay
// obviously wrong. Someone tidying the file toward something that looks more
// realistic would otherwise quietly turn a loud failure into a quiet one.
func TestTheLabProvisioningPlaceholdersAreRefused(t *testing.T) {
	t.Parallel()

	provisioning := &edgev1.EdgeProvisioning{}
	if err := prototext.Unmarshal(labFixture(t, "provisioning.textproto"), provisioning); err != nil {
		t.Fatalf("deploy/lab/provisioning.textproto does not parse: %v", err)
	}

	err := protovalidate.Validate(provisioning)
	if err == nil {
		t.Fatal("the provisioning fixture passes its schema rules; its placeholders would be accepted as a real key and anchor")
	}

	// Both placeholders are refused, each by its own rule. A placeholder that
	// happens to be 32 bytes long passes the anchor rule, and the key alone
	// would keep this test green.
	var validation *protovalidate.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("the provisioning fixture is refused with %v, want a validation error", err)
	}
	refused := map[string]bool{}
	for _, violation := range validation.Violations {
		for _, element := range violation.Proto.GetField().GetElements() {
			refused[element.GetFieldName()] = true
		}
	}
	for _, field := range []string{"setup_key", "trust_anchors"} {
		if !refused[field] {
			t.Errorf("the provisioning fixture's %s placeholder passes its schema rule; refused fields: %v", field, refused)
		}
	}
}

// The central fixture satisfies its schema, so the service loads it as soon as
// the two placeholder ids are replaced.
//
// It goes through the loader the service starts with rather than the schema
// alone, so a rule the loader adds is held too. Its authorization ids are
// placeholders by design, but they are refused by the engine client and not
// by the schema, which the next test asserts.
func TestTheLabCentralConfigLoads(t *testing.T) {
	t.Parallel()

	if _, err := host.LoadConfig(labFixturePath("central.textproto")); err != nil {
		t.Fatalf("host.LoadConfig(deploy/lab/central.textproto): %v", err)
	}
}

const dexImage = "dexidp/dex:v2.45.1@sha256:8499afd690c437f52301efd2b05b2455da5bd2dfc20332cd697dc9937f808462"

func TestTheLabAuthorizationPlaceholdersAreRefused(t *testing.T) {
	t.Parallel()

	cfg := &storev1.DeviceServiceConfig{}
	if err := prototext.Unmarshal(labFixture(t, "central.textproto"), cfg); err != nil {
		t.Fatalf("unmarshal central.textproto: %v", err)
	}

	authzCfg := cfg.GetAuthorization()
	if authzCfg == nil {
		t.Fatal("central.textproto missing authorization section")
	}

	const wellFormedID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

	// Each placeholder id is refused with the other id well formed, so the
	// refusal is the one id's alone.
	for _, tc := range []struct {
		name    string
		storeID string
		modelID string
		field   string
	}{
		{"store id placeholder", authzCfg.GetStoreId(), wellFormedID, "StoreId"},
		{"model id placeholder", wellFormedID, authzCfg.GetModelId(), "Id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := openfga.New(t.Context(), openfga.Options{
				Endpoint: authzCfg.GetEndpoint(),
				StoreID:  tc.storeID,
				ModelID:  tc.modelID,
				KeyFile:  authzCfg.GetPresharedKeyFile(),
				CAFile:   authzCfg.GetCaFile(),
			})
			gotCode, ok := errs.CodeOf(err)
			if !ok || gotCode != openfga.ErrCodeConfig {
				t.Fatalf("openfga.New: got %v (code %v), want %v", err, gotCode, openfga.ErrCodeConfig)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("openfga.New error %q does not name %s", err, tc.field)
			}
		})
	}

	_, err := openfga.New(t.Context(), openfga.Options{
		Endpoint: authzCfg.GetEndpoint(),
		StoreID:  wellFormedID,
		ModelID:  wellFormedID,
		KeyFile:  authzCfg.GetPresharedKeyFile(),
		CAFile:   authzCfg.GetCaFile(),
	})
	gotCode, ok := errs.CodeOf(err)
	if !ok || gotCode != credential.ErrCodeNotFound {
		t.Fatalf("openfga.New with valid IDs and missing keyfile: got %v (code %v), want %v", err, gotCode, credential.ErrCodeNotFound)
	}
}

func TestTheLabOpenFGARequiresAKeyAndTLS(t *testing.T) {
	t.Parallel()

	var compose struct {
		Services map[string]struct {
			Image       string            `yaml:"image"`
			Command     any               `yaml:"command"`
			Environment map[string]string `yaml:"environment"`
			Ports       []string          `yaml:"ports"`
			Healthcheck struct {
				Test []string `yaml:"test"`
			} `yaml:"healthcheck"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(labFixture(t, "compose.yaml"), &compose); err != nil {
		t.Fatalf("unmarshal compose.yaml: %v", err)
	}

	pg, ok := compose.Services["postgres"]
	if !ok {
		t.Fatal("missing postgres service in compose.yaml")
	}
	if pg.Image != postgresImage {
		t.Errorf("postgres image: got %q, want %q", pg.Image, postgresImage)
	}

	fgaMigrate, ok := compose.Services["openfga-migrate"]
	if !ok {
		t.Fatal("missing openfga-migrate service in compose.yaml")
	}
	if fgaMigrate.Image != openFGAImage {
		t.Errorf("openfga-migrate image: got %q, want %q", fgaMigrate.Image, openFGAImage)
	}

	fga, ok := compose.Services["openfga"]
	if !ok {
		t.Fatal("missing openfga service in compose.yaml")
	}
	if fga.Image != openFGAImage {
		t.Errorf("openfga image: got %q, want %q", fga.Image, openFGAImage)
	}

	dex, ok := compose.Services["dex"]
	if !ok {
		t.Fatal("missing dex service in compose.yaml")
	}
	if !strings.Contains(dex.Image, "@sha256:") {
		t.Errorf("dex image %q lacks digest", dex.Image)
	}
	if dex.Image != dexImage {
		t.Errorf("dex image: got %q, want %q", dex.Image, dexImage)
	}

	wantEnv := map[string]string{
		"OPENFGA_AUTHN_METHOD":              "preshared",
		"OPENFGA_GRPC_TLS_ENABLED":          "true",
		"OPENFGA_HTTP_TLS_ENABLED":          "true",
		"OPENFGA_PLAYGROUND_ENABLED":        "false",
		"OPENFGA_CHECK_QUERY_CACHE_ENABLED": "false",
		"OPENFGA_CACHE_CONTROLLER_ENABLED":  "false",
	}
	for k, wantVal := range wantEnv {
		if gotVal := fga.Environment[k]; gotVal != wantVal {
			t.Errorf("openfga env %s: got %q, want %q", k, gotVal, wantVal)
		}
	}

	// Each flag is matched as a whole argument: "-tls" is a prefix of
	// "-tls-ca-cert", so a substring match passes with the plain flag gone.
	probe := fga.Healthcheck.Test
	if !slices.Contains(probe, "grpc_health_probe") {
		t.Errorf("healthcheck probe missing grpc_health_probe: %q", probe)
	}
	if !slices.Contains(probe, "-tls") {
		t.Errorf("healthcheck probe missing flag -tls: %q", probe)
	}
	for _, flag := range []string{"-tls-ca-cert=", "-tls-server-name="} {
		if !slices.ContainsFunc(probe, func(arg string) bool { return strings.HasPrefix(arg, flag) && len(arg) > len(flag) }) {
			t.Errorf("healthcheck probe missing flag %s<value>: %q", flag, probe)
		}
	}

	for svcName, svc := range compose.Services {
		for _, port := range svc.Ports {
			if !strings.HasPrefix(port, "127.0.0.1:") {
				t.Errorf("service %s port %s is not bound to 127.0.0.1", svcName, port)
			}
		}
	}
}

func TestTheLabIssuerMatchesCentral(t *testing.T) {
	t.Parallel()

	var dexCfg struct {
		Issuer string `yaml:"issuer"`
		Web    struct {
			HTTP  string `yaml:"http"`
			HTTPS string `yaml:"https"`
		} `yaml:"web"`
	}

	if err := yaml.Unmarshal(labFixture(t, "dex/config.yaml"), &dexCfg); err != nil {
		t.Fatalf("unmarshal dex/config.yaml: %v", err)
	}

	centralCfg := &storev1.DeviceServiceConfig{}
	if err := prototext.Unmarshal(labFixture(t, "central.textproto"), centralCfg); err != nil {
		t.Fatalf("unmarshal central.textproto: %v", err)
	}

	issuers := centralCfg.GetAuthentication().GetIssuers()
	if len(issuers) == 0 {
		t.Fatal("central.textproto has no issuers configured")
	}

	if dexCfg.Issuer != issuers[0].GetIssuer() {
		t.Errorf("dex issuer %q != central issuer %q", dexCfg.Issuer, issuers[0].GetIssuer())
	}

	if dexCfg.Web.HTTP != "" {
		t.Errorf("dex config defines web.http %q, want none", dexCfg.Web.HTTP)
	}
}

// labScript is one of the scripts a lab run starts from.
func labScript(t *testing.T, name string) string {
	t.Helper()
	return string(labFixture(t, name))
}

// heredocBody returns the lines of the here-document the script writes to
// target, a path expression as the script spells it.
func heredocBody(t *testing.T, script, target string) []string {
	t.Helper()

	var body []string
	in := false
	for line := range strings.SplitSeq(script, "\n") {
		switch {
		case !in && strings.Contains(line, "<< EOF") && strings.HasSuffix(strings.TrimSpace(line), "> "+target):
			in = true
		case in && line == "EOF":
			return body
		case in:
			body = append(body, line)
		}
	}
	t.Fatalf("no here-document written to %s", target)
	return nil
}

// Dex's environment file keeps every value literal.
//
// Compose reads the file and substitutes $name in an unquoted value. A bcrypt
// hash is $2y$10$<salt><digest>, so unquoted it reaches Dex with its first
// segments eaten, and the password grant then fails with a login error that
// names nothing about the file. No test starts Compose, so this holds the
// script's text: every assignment is one single-quoted value.
func TestTheLabSecretsScriptQuotesTheDexEnvFile(t *testing.T) {
	t.Parallel()

	assignment := regexp.MustCompile(`^[A-Z_]+='\$\{[A-Z_]+\}'$`)
	lines := heredocBody(t, labScript(t, "write-lab-secrets.sh"), `"${SECRETS_DIR}/dex.env"`)
	if len(lines) == 0 {
		t.Fatal("dex.env here-document is empty")
	}
	for _, line := range lines {
		if !assignment.MatchString(line) {
			t.Errorf("dex.env line %q is not one single-quoted value", line)
		}
	}
}

// The lab password hashes meet the cost Dex requires, and the passwords never
// appear in a process's arguments.
//
// htpasswd defaults to cost 5 and Dex refuses anything below 10 at login, so a
// default invocation yields a file that loads and a user who cannot sign in.
func TestTheLabSecretsScriptHashesAtDexsCostFromStdin(t *testing.T) {
	t.Parallel()

	var hashing []string
	for line := range strings.SplitSeq(labScript(t, "write-lab-secrets.sh"), "\n") {
		if fields := strings.Fields(line); slices.Contains(fields, "htpasswd") && slices.Contains(fields, "-niB") {
			hashing = fields
		}
	}
	if hashing == nil {
		t.Fatal("no htpasswd invocation that reads the password from stdin (-niB)")
	}
	cost := slices.Index(hashing, "-C")
	if cost < 0 || cost+1 >= len(hashing) || hashing[cost+1] != "10" {
		t.Errorf("htpasswd invocation %q does not set -C 10", hashing)
	}
}

// Every file the README reads from secrets/ is one the script writes.
//
// The README's run is not executed by any test, so a file name that drifts
// between the two fails only for whoever follows the steps.
func TestTheLabReadmeReadsOnlyFilesTheSecretsScriptWrites(t *testing.T) {
	t.Parallel()

	script := labScript(t, "write-lab-secrets.sh")
	named := regexp.MustCompile(`secrets/([A-Za-z0-9_.-]+)`).FindAllStringSubmatch(labScript(t, "README.md"), -1)
	if len(named) == 0 {
		t.Fatal("deploy/lab/README.md names no file under secrets/")
	}
	for _, m := range named {
		if !strings.Contains(script, m[1]) {
			t.Errorf("deploy/lab/README.md reads secrets/%s, which write-lab-secrets.sh does not write", m[1])
		}
	}
}

// The store script keeps the preshared key out of curl's arguments.
func TestTheLabStoreScriptKeepsTheKeyOutOfCurlArguments(t *testing.T) {
	t.Parallel()

	for line := range strings.SplitSeq(labScript(t, "write-openfga-store.sh"), "\n") {
		if strings.Contains(line, "-H") && strings.Contains(line, "PSK") {
			t.Errorf("curl is given the preshared key as an argument: %q", strings.TrimSpace(line))
		}
	}
}

// The README's principal-id pipeline extracts the subject without quotes
// and matches authn.ComputePrincipalID.
func TestTheLabReadmePrincipalIDMatchesComputePrincipalID(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"jq", "shasum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH: %v", tool, err)
		}
	}

	readme := labScript(t, "README.md")
	var subLine, idLine string
	for line := range strings.SplitSeq(readme, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ALICE_SUB=") {
			subLine = trimmed
		}
		if strings.HasPrefix(trimmed, "ALICE_ID=") {
			idLine = trimmed
		}
	}
	if subLine == "" || idLine == "" {
		t.Fatalf("extract principal pipeline from deploy/lab/README.md: ALICE_SUB=%q, ALICE_ID=%q", subLine, idLine)
	}

	issuer := "https://127.0.0.1:8445/dex"
	subject := "alice-subject-123"
	wantID := authn.ComputePrincipalID(issuer, subject)

	header := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9"
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"` + subject + `"}`))
	token := header + "." + payload + ".dummy-sig"

	cmd := exec.Command("sh", "-c", fmt.Sprintf(`
set -eu
ALICE_TOKEN=%q
%s
%s
printf '%%s' "${ALICE_ID}"
`, token, subLine, idLine))

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run principal pipeline from README: %v\n%s", err, out)
	}

	gotID := string(out)
	if gotID != wantID {
		t.Errorf("computed principal ID = %q, want %q", gotID, wantID)
	}
}

// The sample DeviceServiceConfig in spec/proto/flowseer/store/device/v1/README.md
// is valid and passes host.LoadConfig.
func TestTheStoreDeviceReadmeSampleLoads(t *testing.T) {
	t.Parallel()

	readmePath := filepath.Join(repoRoot, "spec", "proto", "flowseer", "store", "device", "v1", "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read store/device/v1/README.md: %v", err)
	}

	const section = "## The service configuration"
	secIdx := strings.Index(string(content), section)
	if secIdx < 0 {
		t.Fatalf("no %q section in %s", section, readmePath)
	}

	const marker = "```prototext\n"
	start := strings.Index(string(content)[secIdx:], marker)
	if start < 0 {
		t.Fatalf("no %s block under %q in %s", marker, section, readmePath)
	}
	start += secIdx + len(marker)
	end := strings.Index(string(content)[start:], "\n```")
	if end < 0 {
		t.Fatalf("unclosed prototext block in %s", readmePath)
	}
	body := string(content)[start : start+end]

	tmp := filepath.Join(t.TempDir(), "device.textproto")
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := host.LoadConfig(tmp)
	if err != nil {
		t.Fatalf("host.LoadConfig(%s): %v", readmePath, err)
	}
	if cfg.StateDir() != "/var/lib/flowseer/device" {
		t.Errorf("StateDir = %q, want /var/lib/flowseer/device", cfg.StateDir())
	}
}

// deploy/lab/README.md prints accurate expected answers for OpenFGA preshared key
// checks and edge administration.
func TestTheLabReadmeExpectedPresharedKeyAndEdgeResponses(t *testing.T) {
	t.Parallel()

	readme := labScript(t, "README.md")

	if strings.Contains(readme, "grpc-status: 16") {
		t.Errorf("deploy/lab/README.md contains 'grpc-status: 16', want 1010 and 1500")
	}
	if !strings.Contains(readme, "grpc-status: 1010") {
		t.Errorf("deploy/lab/README.md missing expected 'grpc-status: 1010'")
	}
	if !strings.Contains(readme, "grpc-status: 1500") {
		t.Errorf("deploy/lab/README.md missing expected 'grpc-status: 1500'")
	}

	if strings.Contains(readme, `"Bearer token is missing"`) {
		t.Errorf("deploy/lab/README.md contains '\"Bearer token is missing\"', want 'missing bearer token'")
	}
	if strings.Contains(readme, `"Unauthorized"`) {
		t.Errorf("deploy/lab/README.md contains '\"Unauthorized\"', want 'unauthenticated'")
	}
	if !strings.Contains(readme, `"missing bearer token"`) {
		t.Errorf("deploy/lab/README.md missing '\"missing bearer token\"'")
	}

	if strings.Contains(readme, `"edgeId":`) {
		t.Errorf("deploy/lab/README.md contains '\"edgeId\":' under provisioning, which EdgeProvisioning lacks")
	}
	if !strings.Contains(readme, `"issuedAt":`) || !strings.Contains(readme, `"expiresAt":`) {
		t.Errorf("deploy/lab/README.md setupKey expected answer missing issuedAt or expiresAt")
	}

	if strings.Contains(readme, `-d '{"edge":{"edge":{"id":"0192e6a0-0000-7000-8000-000000000001"}}}'`) {
		t.Errorf("deploy/lab/README.md hardcodes edge id in GetEdge, must capture from CreateEdge answer")
	}
}

// deploy/lab/README.md uses directory-relative path for starting device service and
// explains bootstrap registry creation.
func TestTheLabReadmeStartCommandAndBootstrapRegistry(t *testing.T) {
	t.Parallel()

	readme := labScript(t, "README.md")

	if strings.Contains(readme, "go run ./src/services/device/cmd/device") {
		t.Errorf("deploy/lab/README.md start command uses ./src/services/device/cmd/device, should use ../../src/services/device/cmd/device")
	}

	if !strings.Contains(readme, "registry.textproto") || !strings.Contains(readme, "0192e6a0-0000-7000-8000-00000000dead") {
		t.Errorf("deploy/lab/README.md does not show or cite the bootstrap registry with placeholder edge")
	}
}

// docs/runbooks/lab-icx7150-first-write.md contracts: tenant id is deployment UUID,
// links to lab README, requires authentication/authorization config, and states
// tenant-record limits.
func TestTheRunbookAuthenticationAndTenantContracts(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(repoRoot, "docs", "runbooks", "lab-icx7150-first-write.md"))
	if err != nil {
		t.Fatalf("read runbook: %v", err)
	}
	runbook := string(content)

	if strings.Contains(runbook, "export TENANT=default\n") {
		t.Errorf("docs/runbooks/lab-icx7150-first-write.md contains 'export TENANT=default', want deployment's tenant UUID")
	}

	if !strings.Contains(runbook, "deploy/lab/README.md") {
		t.Errorf("docs/runbooks/lab-icx7150-first-write.md missing link to deploy/lab/README.md")
	}

	if !strings.Contains(runbook, "authentication") || !strings.Contains(runbook, "authorization") {
		t.Errorf("docs/runbooks/lab-icx7150-first-write.md missing mention of central authentication/authorization config")
	}

	for _, cite := range []string{
		"tenantstore",
		"TenantService",
		"verifier.go",
		"model.json",
	} {
		if !strings.Contains(runbook, cite) {
			t.Errorf("docs/runbooks/lab-icx7150-first-write.md missing citation of %s for tenant record limit", cite)
		}
	}
}

// docs/architecture/2026-09-30-operator-authorization-direction.md contracts:
// correct citations and inclusion of plan decisions.
func TestTheDirectionRecordCitationsAndDecisions(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(repoRoot, "docs", "architecture", "2026-09-30-operator-authorization-direction.md"))
	if err != nil {
		t.Fatalf("read direction record: %v", err)
	}
	text := string(content)

	if strings.Contains(text, "src/modules/edgebus/hub.go:42") {
		t.Errorf("direction record cites src/modules/edgebus/hub.go:42, should cite subjects.go and hub.go:400")
	}

	if strings.Contains(text, "mutual TLS") {
		t.Errorf("direction record claims edges authenticate through mutual TLS, want signed assertions citing src/services/device/README.md")
	}

	if strings.Contains(text, "the plan table") {
		t.Errorf("direction record cites 'the plan table' without path")
	}

	if strings.Contains(text, "cannot evict setup key issuance records from another tenant") {
		t.Errorf("direction record claims per-subject cap is for cross-tenant eviction; should be edge viewers evicting key issuance records")
	}

	for _, decision := range []string{
		"Sync",
		"5 s",
		"Reconciled",
		"256",
		"AbandonMutationRequest",
		"OPERATOR_ACTION_OUTCOME_DENIED",
	} {
		if !strings.Contains(text, decision) {
			t.Errorf("direction record missing decision keyword %q", decision)
		}
	}
}

// src/services/device/test/integration/fixture_test.go comment contracts.
func TestTheFixtureTestClientCommentReused(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(repoRoot, "src", "services", "device", "test", "integration", "fixture_test.go"))
	if err != nil {
		t.Fatalf("read fixture_test.go: %v", err)
	}
	if strings.Contains(string(content), "built once and reused") {
		t.Errorf("fixture_test.go says 'built once and reused' while start() rebuilds it")
	}
}

// spec/proto README contracts: no semicolons in operator event README and central
// sets Actor in access model README.
func TestTheAccessAndEventReadmes(t *testing.T) {
	t.Parallel()

	eventReadme, err := os.ReadFile(filepath.Join(repoRoot, "spec", "proto", "flowseer", "event", "operator", "v1", "README.md"))
	if err != nil {
		t.Fatalf("read event README: %v", err)
	}
	if strings.Contains(string(eventReadme), ";") {
		t.Errorf("spec/proto/flowseer/event/operator/v1/README.md contains semicolons")
	}

	accessReadme, err := os.ReadFile(filepath.Join(repoRoot, "spec", "proto", "flowseer", "model", "access", "v1", "README.md"))
	if err != nil {
		t.Fatalf("read access README: %v", err)
	}
	if strings.Contains(string(accessReadme), "The client builds a `MutationIntent`: the device ref, a fresh UUID as\n   `idempotency_key`, an `Actor`") ||
		strings.Contains(string(accessReadme), "The client builds a `MutationIntent`: the device ref, a fresh UUID as `idempotency_key`, an `Actor`") {
		t.Errorf("spec/proto/flowseer/model/access/v1/README.md claims client builds Actor")
	}
}
