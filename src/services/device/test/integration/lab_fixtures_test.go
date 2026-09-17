package integration_test

import (
	"os"
	"path/filepath"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	storeedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/edge/v1"
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
		"agent.textproto":        &storeedgev1.AgentConfig{},
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
