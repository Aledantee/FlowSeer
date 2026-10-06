package integration_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
)

// The scripts under test.
const (
	labRegistryScript     = "write-registry.py"
	labProvisioningScript = "write-provisioning.py"
)

// edgePlaceholder is what the shipped registry template holds where the edge
// identifier central minted belongs.
const edgePlaceholder = "REPLACE-WITH-THE-EDGE-ID-CREATEEDGE-RETURNED"

// createdJSON is the body CreateEdge returns, reduced to what the
// provisioning script reads.
func createdJSON(t *testing.T, setupKey string, anchors ...string) string {
	t.Helper()

	// An empty list, which JSON writes as [] and not as null.
	provisioning := map[string]any{"trustAnchors": append([]string{}, anchors...)}
	if setupKey != "" {
		provisioning["setupKey"] = setupKey
	}
	body, err := json.Marshal(map[string]any{"provisioning": provisioning})
	if err != nil {
		t.Fatalf("encoding the CreateEdge body: %v", err)
	}
	path := filepath.Join(t.TempDir(), "created.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing the CreateEdge body: %v", err)
	}

	return path
}

// byteRange returns the bytes from first to last, inclusive.
func byteRange(first, last byte) []byte {
	var out []byte
	for b := int(first); b <= int(last); b++ {
		out = append(out, byte(b))
	}

	return out
}

// An anchor survives the script byte for byte.
//
// trust_anchors is a bytes field. JSON carries it as base64 and prototext as
// an escaped byte string, and an anchor of the right length and the wrong
// value is refused by the edge with a message about the chain. The two
// anchors cover the high half of the byte range and the control characters,
// where an escape that is read back wrongly shows.
func TestTheLabProvisioningScriptKeepsAnchorBytes(t *testing.T) {
	t.Parallel()

	skipUnlessLabScriptsRun(t)
	anchors := [][]byte{byteRange(0xe0, 0xff), byteRange(0x00, 0x1f)}
	var encoded []string
	for _, anchor := range anchors {
		encoded = append(encoded, base64.StdEncoding.EncodeToString(anchor))
	}
	created := createdJSON(t, "lab-setup-key", encoded...)

	run := runLabScript(t, t.TempDir(), labProvisioningScript, created, "https://central.example:8443")
	if run.exit != 0 {
		t.Fatalf("the provisioning script exited %d:\n%s", run.exit, run.stderr)
	}

	got := &edgev1.EdgeProvisioning{}
	if err := prototext.Unmarshal([]byte(run.stdout), got); err != nil {
		t.Fatalf("the output is not an EdgeProvisioning: %v\n%s", err, run.stdout)
	}
	if got.GetSetupKey() != "lab-setup-key" || got.GetCentralUrl() != "https://central.example:8443" {
		t.Errorf("the output holds setup key %q and central URL %q", got.GetSetupKey(), got.GetCentralUrl())
	}
	if len(got.GetTrustAnchors()) != len(anchors) {
		t.Fatalf("the output holds %d anchors, want %d:\n%s", len(got.GetTrustAnchors()), len(anchors), run.stdout)
	}
	for i, want := range anchors {
		if !bytes.Equal(got.GetTrustAnchors()[i], want) {
			t.Errorf("anchor %d reads back as %x, want %x", i, got.GetTrustAnchors()[i], want)
		}
	}
}

// A bad CreateEdge body writes nothing.
//
// The output holds a live setup key, and a file that carries one with no
// anchors is worse than no file: the edge trusts nothing, refuses central's
// certificate, and cannot enroll after the key has been consumed.
func TestTheLabProvisioningScriptWritesNothingOnBadInput(t *testing.T) {
	t.Parallel()

	skipUnlessLabScriptsRun(t)
	good := base64.StdEncoding.EncodeToString(byteRange(0x00, 0x1f))

	tests := []struct {
		name string
		body string
	}{
		{name: "no setup key", body: createdJSON(t, "", good)},
		{name: "no trust anchors", body: createdJSON(t, "lab-setup-key")},
		{name: "an anchor that is not base64", body: createdJSON(t, "lab-setup-key", "!!!")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := runLabScript(t, t.TempDir(), labProvisioningScript, tt.body, "https://central.example:8443")
			if run.exit == 0 {
				t.Errorf("the provisioning script accepted the input:\n%s", run.stdout)
			}
			if run.stdout != "" {
				t.Errorf("the provisioning script wrote to standard output on a failure:\n%s", run.stdout)
			}
		})
	}
}

// writeTemplate writes a registry template holding body and returns its path.
func writeTemplate(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "template.textproto")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the template: %v", err)
	}

	return path
}

// A caller's own template is rendered with the edge identifier and nothing
// else changed.
//
// The integration test aims the registry at a loopback port this way, so the
// shipped file's placeholder address is not in the way.
func TestTheLabRegistryScriptRendersTheEdge(t *testing.T) {
	t.Parallel()

	skipUnlessLabScriptsRun(t)
	template := writeTemplate(t, "integration {\n  edge { edge { id: \""+edgePlaceholder+"\" } }\n}\n")

	run := runLabScriptWithEnv(t, t.TempDir(), []string{"FLOWSEER_REGISTRY_TEMPLATE=" + template}, labRegistryScript, "edge-7")
	if run.exit != 0 {
		t.Fatalf("the registry script exited %d:\n%s", run.exit, run.stderr)
	}
	const want = "integration {\n  edge { edge { id: \"edge-7\" } }\n}\n"
	if run.stdout != want {
		t.Errorf("the registry script wrote:\n%s\nwant:\n%s", run.stdout, want)
	}

	// sed replaced the first occurrence on a line, which leaves a placeholder
	// that central then reads as an edge it never minted.
	twice := writeTemplate(t, "a: \""+edgePlaceholder+"\" b: \""+edgePlaceholder+"\"\n")
	run = runLabScriptWithEnv(t, t.TempDir(), []string{"FLOWSEER_REGISTRY_TEMPLATE=" + twice}, labRegistryScript, "edge-7")
	if run.exit != 0 || strings.Contains(run.stdout, edgePlaceholder) {
		t.Errorf("a placeholder survived the registry script (exit %d):\n%s", run.exit, run.stdout)
	}
}

// The shipped template is refused while it names the placeholder device.
//
// An unfilled address points the registry at the documentation range, and the
// agent then logs a timed-out identity probe, which is what the runbook prints
// as the expected state while the switch is still off.
func TestTheLabRegistryScriptRefusesTheShippedTemplate(t *testing.T) {
	t.Parallel()

	skipUnlessLabScriptsRun(t)
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, labDir), 0o700); err != nil {
		t.Fatalf("creating the lab directory: %v", err)
	}
	copyFile(t, labFixturePath("registry.textproto"), filepath.Join(tree, labDir, "registry.textproto"))

	run := runLabScript(t, tree, labRegistryScript, "some-id")
	if run.exit != 1 {
		t.Errorf("the registry script exited %d on the shipped template, want 1:\n%s", run.exit, run.stdout)
	}
	if !strings.Contains(run.stderr, "192.0.2.6") {
		t.Errorf("the registry script did not name the placeholder address:\n%s", run.stderr)
	}

	// A second spelling of the shipped path is the shipped template too.
	run = runLabScript(t, tree, labRegistryScript, "some-id", filepath.Join(".", "..", "lab", "registry.textproto"))
	if run.exit != 1 {
		t.Errorf("the registry script exited %d on a second spelling of the shipped path, want 1:\n%s", run.exit, run.stdout)
	}
}
