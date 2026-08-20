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

// restconfCorpus is the library's quirk catalog (R13).
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
		Provenance: "plan assumption: ICX support for depth/fields is unconfirmed",
		Behavior:   "depth and fields are sent only on request; peers ignoring them still get correct results via client-side pruning — to be verified on ICX lab hardware",
		Unit:       "U10", Status: conformance.Pending,
	},
}

// restconfFamilies groups the corpus for CONFORMANCE.md.
var restconfFamilies = []conformance.Family{
	{Prefix: "rc-", Title: "RESTCONF discovery, read, and write behavior"},
}

// restconfAllowlist gates accepted-risk rows; empty until one is
// ratified.
var restconfAllowlist = map[string]bool{}

// corpusDirs returns the package dir and its integration subdir.
func corpusDirs(t *testing.T) []string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(here)
	return []string{dir, filepath.Join(dir, "integration")}
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
