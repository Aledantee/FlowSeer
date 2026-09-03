package restconf_test

import (
	"flag"
	"path/filepath"
	"runtime"
	"testing"

	"go.aledante.io/FlowSeer/src/common/internal/conformance"
)

// updateConformance refreshes the committed CONFORMANCE.md. Run with
//
//	go test ./src/common/restconf -run TestConformanceMatrixUpToDate -update-conformance
var updateConformance = flag.Bool("update-conformance", false, "rewrite CONFORMANCE.md from the corpus")

// restconfCorpus is the library's quirk catalog: device and server
// quirks recorded with provenance and covered by cited tests.
var restconfCorpus = []conformance.Row{
	{
		ID: "rc-host-meta-discovery", Clause: "RFC 8040 §3.1",
		Provenance:  "clixon t1 reference server",
		Behavior:    "the API root is discovered from /.well-known/host-meta's restconf link, with a /restconf probe fallback; neither → typed discovery error",
		Adversarial: "real XRD document from clixon; unit tests drive host-meta-absent and no-root peers",
		Unit:        "U9", Status: conformance.Covered,
	},
	{
		ID: "rc-nonconformant-error-body", Clause: "RFC 8040 §7.1",
		Provenance:  "Ruckus FastIron RESTCONF Programmers Guide 09.0.10 (nonconformant error payloads expected on ICX)",
		Behavior:    "a conformant ietf-restconf:errors body decodes to typed attributes; a malformed body is preserved raw on the error for the corpus",
		Adversarial: "HTML error page in place of the errors JSON on a 400",
		Unit:        "U6", Status: conformance.Covered,
	},
	{
		ID: "rc-stale-write-conflict", Clause: "RFC 8040 §3.4.1.2",
		Provenance:  "KTD9 conditional-write discipline",
		Behavior:    "a 412 on If-Match maps to the retryable restconf/conflict code",
		Adversarial: "server rejecting a stale ETag with 412 plus an errors body",
		Unit:        "U6", Status: conformance.Covered,
	},
	{
		ID: "rc-etag-absent-unconditional", Clause: "RFC 8040 §3.4.1.2",
		Provenance:  "KTD9: peers without ETag support",
		Behavior:    "missing ETag support degrades to an unconditional write, and the read-back verification still runs",
		Adversarial: "server offering no ETag header on the capture GET",
		Unit:        "U6", Status: conformance.Covered,
	},
	{
		ID: "rc-absent-resource-404", Clause: "RFC 8040 §4.3",
		Provenance:  "watcher semantics: an absent optional subtree is data",
		Behavior:    "a 404 on a data resource is (nil, nil), not an error; the Watcher turns it into Removed events",
		Adversarial: "GET of a data resource answering 404",
		Unit:        "U6", Status: conformance.Covered,
	},
	{
		ID: "rc-edit-read-back", Clause: "RFC 8040 §4.5/§4.7",
		Provenance:  "clixon t1 reference server",
		Behavior:    "PUT creates and DELETE removes, each proven by re-reading the resource rather than trusting the status code (R12)",
		Adversarial: "real clixon datastore round-trip including delete verification",
		Unit:        "U9", Status: conformance.Covered,
	},
	{
		ID: "rc-depth-fields-unverified", Clause: "RFC 8040 §4.8.2/§4.8.3",
		Provenance:  "Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6",
		Behavior:    "resolved on hardware: FastIron 10.0.10g honors the depth parameter, so the depth/fields path is exercised for real; the client-side-pruning fallback remains for peers that ignore it (see rc-t4-depth-fields)",
		Adversarial: "a live device whose depth support was unknown until measured; the same GET that would silently return the full tree on an ignoring peer",
		Unit:        "U10", Status: conformance.Covered,
	},
	{
		ID: "rc-t4-identity", Clause: "AE1",
		Provenance:  "Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6",
		Behavior:    "hostname is read as a typed value end to end (dial → host-meta discovery → GET → RFC 7951 decode), from /system/config/hostname because FastIron mirrors it there and leaves /system/state empty. Documented FastIron surface gap: serial-no, part-no, and software-version are not populated in this device's openconfig RESTCONF surface (CLI-only); model appears only under icx-openconfig-platform-aug:switch-model, an augmentation absent from the vendored 9.0.x YANG corpus (device/corpus version skew), so the typed bindings cannot surface it. The ICX system deviation removes only dns/server port, so these are unimplemented runtime state, not modeled deviations.",
		Adversarial: "a live openconfig surface that leaves /system/state empty and omits every standard platform identity leaf, forcing the config-mirror hostname fallback and returning components with no serial/part/software-version",
		Unit:        "U10", Status: conformance.Covered,
	},
	{
		ID: "rc-t4-reversible-edit", Clause: "R12",
		Provenance:  "Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6",
		Behavior:    "a reversible interface-description edit (PATCH) and its revert are each proven by read-back, using a conditional If-Match write. FastIron rejects writes to openconfig-system config leaves (login-banner/hostname return \"invalid internal value\"), so the device's documented, non-disruptive writable leaf is used; the port's enabled state is captured and restored so forwarding is never touched.",
		Adversarial: "a live device that rejects openconfig-system config writes with \"invalid internal value\", exercised through a hardware PATCH/revert cycle where only read-back, never the status code, proves each write",
		Unit:        "U10", Status: conformance.Covered,
	},
	{
		ID: "rc-t4-depth-fields", Clause: "RFC 8040 §4.8.2/§4.8.3",
		Provenance:  "Ruckus ICX7150-24P, FastIron 10.0.10g, lab device 172.16.0.6 (resolves rc-depth-fields-unverified)",
		Behavior:    "measured on hardware: FastIron 10.0.10g honors depth — a GET of /openconfig-system:system returned 899 bytes full versus 80 bytes at depth=2 — so the depth path is verified against a real device; the client-side-pruning fallback still covers peers that ignore it.",
		Adversarial: "a live depth=2 GET against hardware free to reject or ignore the parameter; honoring is proven by the measured 899-byte full versus 80-byte shallow responses",
		Unit:        "U10", Status: conformance.Covered,
	},
}

// restconfFamilies groups the corpus for CONFORMANCE.md.
var restconfFamilies = []conformance.Family{
	{Prefix: "rc-", Title: "RESTCONF discovery, read, and write behavior"},
}

// restconfAllowlist gates accepted-risk rows; empty until one is
// ratified.
var restconfAllowlist = map[string]bool{}

// corpusDirs returns the package and integration suite directories.
func corpusDirs(t *testing.T) []string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(here)
	return []string{dir, filepath.Join(dir, "..", "..", "..", "test", "integration", "restconf")}
}

// TestConformanceCorpusIntegrity is the always-on gate.
func TestConformanceCorpusIntegrity(t *testing.T) {
	conformance.RunIntegrity(t, restconfCorpus, restconfAllowlist, restconfFamilies, corpusDirs(t))
}

// TestConformanceMatrixUpToDate keeps CONFORMANCE.md generated.
func TestConformanceMatrixUpToDate(t *testing.T) {
	content := conformance.Markdown("RESTCONF library conformance corpus", restconfCorpus, restconfFamilies)
	conformance.VerifyMarkdown(t, filepath.Join(corpusDirs(t)[0], "CONFORMANCE.md"), content, *updateConformance)
}
