package integration_test

import (
	"os"
	"path/filepath"
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
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
	"go.aledante.io/FlowSeer/src/services/device/internal/credential"
)

// labFixture is one of the files a lab run is assembled from.
func labFixture(t *testing.T, name string) []byte {
	t.Helper()
	// Five levels up: integration, test, device, services, src.
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "deploy", "lab", name))
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
// their schema rules, and the next test is about that.
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
	t.Logf("refused as intended: %v", err)
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

	opt := openfga.Options{
		Endpoint: authzCfg.GetEndpoint(),
		StoreID:  authzCfg.GetStoreId(),
		ModelID:  authzCfg.GetModelId(),
		KeyFile:  authzCfg.GetPresharedKeyFile(),
		CAFile:   authzCfg.GetCaFile(),
	}

	_, err := openfga.New(t.Context(), opt)
	gotCode, ok := errs.CodeOf(err)
	if !ok || gotCode != openfga.ErrCodeConfig {
		t.Fatalf("openfga.New with placeholders: got %v (code %v), want %v", err, gotCode, openfga.ErrCodeConfig)
	}

	opt.StoreID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	opt.ModelID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

	_, err2 := openfga.New(t.Context(), opt)
	gotCode2, ok2 := errs.CodeOf(err2)
	if !ok2 || gotCode2 != credential.ErrCodeNotFound {
		t.Fatalf("openfga.New with valid IDs and missing keyfile: got %v (code %v), want %v", err2, gotCode2, credential.ErrCodeNotFound)
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

	// 1. Assert images
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

	// 2. OpenFGA settings
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

	// 3. Healthcheck uses grpc_health_probe with -tls, -tls-ca-cert, -tls-server-name
	hcArgs := strings.Join(fga.Healthcheck.Test, " ")
	if !strings.Contains(hcArgs, "grpc_health_probe") {
		t.Errorf("healthcheck probe missing grpc_health_probe: %q", hcArgs)
	}
	for _, flag := range []string{"-tls", "-tls-ca-cert", "-tls-server-name"} {
		if !strings.Contains(hcArgs, flag) {
			t.Errorf("healthcheck probe missing flag %s: %q", flag, hcArgs)
		}
	}

	// 4. Loopback bind on every published port
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
