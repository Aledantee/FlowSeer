package gnmi_test

import (
	"flag"
	"path/filepath"
	"runtime"
	"testing"

	"go.aledante.io/FlowSeer/src/common/internal/conformance"
)

// updateConformance refreshes the committed CONFORMANCE.md. Run with
//
//	go test ./src/common/gnmi -run TestConformanceMatrixUpToDate -update-conformance
var updateConformance = flag.Bool("update-conformance", false, "rewrite CONFORMANCE.md from the corpus")

// gnmiCorpus is the library's quirk catalog (R13).
var gnmiCorpus = []conformance.Row{
	{
		ID: "gn-proto-only-encoding", Clause: "gNMI spec §2.2.3",
		Provenance:  "KTD5 encoding negotiation: not every peer offers JSON_IETF",
		Behavior:    "a peer advertising only PROTO still round-trips Get; JSON_IETF is preferred when offered",
		Adversarial: "CapabilityResponse advertising only PROTO, values as scalar TypedValues",
		Unit:        "U7", Status: conformance.Covered,
	},
	{
		ID: "gn-per-path-set-error", Clause: "gNMI spec §3.4.2",
		Provenance:  "deprecated UpdateResult.Message is the only per-path failure channel several implementations use",
		Behavior:    "a Set response carrying a per-path error surfaces which path failed as an attribute",
		Adversarial: "SetResponse whose UpdateResult carries a populated (deprecated) Error message",
		Unit:        "U7", Status: conformance.Covered,
	},
	{
		ID: "gn-stream-termination-latch", Clause: "gNMI spec §3.5",
		Provenance:  "session-lifecycle HTD: a dropped stream never resumes itself",
		Behavior:    "abrupt server termination of a STREAM subscription latches Stream.Err; buffered events stay readable; ONCE completion is clean",
		Adversarial: "server returning a gRPC error after the sync marker",
		Unit:        "U7", Status: conformance.Covered,
	},
	{
		ID: "gn-presync-buffering", Clause: "gNMI spec §3.5.1.4",
		Provenance:  "KTD5: sync_response is the cold-start-complete signal",
		Behavior:    "updates before sync_response buffer as initial state and emit as Added at sync; each post-sync notification batch emits at most one event per affected row",
		Adversarial: "row leaves split across multiple pre-sync batches; a post-sync batch touching two leaves of one row",
		Unit:        "U8", Status: conformance.Covered,
	},
	{
		ID: "gn-subscribe-once-snapshot", Clause: "gNMI spec §3.5.1.5.1",
		Provenance:  "FlowSeer reference target (t1)",
		Behavior:    "Subscribe ONCE assembles the snapshot's leaf updates into typed rows through the generated descriptor and ends cleanly at sync",
		Adversarial: "real gRPC stream from the containerized reference target",
		Unit:        "U9", Status: conformance.Covered,
	},
	{
		ID: "gn-stream-sync-cold-start", Clause: "gNMI spec §3.5.1.5.2",
		Provenance:  "FlowSeer reference target (t1)",
		Behavior:    "STREAM cold start emits Added per row at sync, then the target's periodic leaf change arrives as one Modified per batch",
		Adversarial: "real streaming subscription with device-owned cadence",
		Unit:        "U9", Status: conformance.Covered,
	},
	{
		ID: "gn-aruba-set-capability", Clause: "gNMI spec §3.4",
		Provenance: "R14: external research suggests AOS-CX gNMI is telemetry-oriented",
		Behavior:   "whether Aruba CX accepts config writes via Set is verified early on lab hardware; if not, the write criterion converts per R14 with the fallback recorded",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "gn-t4-identity", Clause: "AE1",
		Provenance: "Aruba CX lab device (pending)",
		Behavior:   "hostname, software version, serial, and part number return via Get over the advertised OpenConfig models",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "gn-t4-set-verdict", Clause: "AE3/R14",
		Provenance: "Aruba CX lab device (pending)",
		Behavior:   "a reversible login-banner Set either round-trips (verified by Get) or the incapacity is recorded and the write criterion converts per R14",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "gn-t4-stream", Clause: "R10",
		Provenance: "Aruba CX lab device (pending)",
		Behavior:   "a STREAM subscription over interface state delivers sync and keeps flowing on hardware",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "gn-t4-revision-drift", Clause: "R8",
		Provenance: "Aruba CX lab device (pending)",
		Behavior:   "advertised model versions diff against the committed lockfile; drift surfaces as warnings",
		Unit:       "U10", Status: conformance.Pending,
	},
}

// gnmiFamilies groups the corpus for CONFORMANCE.md.
var gnmiFamilies = []conformance.Family{
	{Prefix: "gn-", Title: "gNMI encoding, Set, and Subscribe behavior"},
}

// gnmiAllowlist gates accepted-risk rows; empty until one is
// ratified.
var gnmiAllowlist = map[string]bool{}

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
	conformance.RunIntegrity(t, gnmiCorpus, gnmiAllowlist, gnmiFamilies, corpusDirs(t))
}

// TestConformanceMatrixUpToDate keeps CONFORMANCE.md generated.
func TestConformanceMatrixUpToDate(t *testing.T) {
	content := conformance.Markdown("gNMI library conformance corpus", gnmiCorpus, gnmiFamilies)
	conformance.VerifyMarkdown(t, filepath.Join(corpusDirs(t)[0], "CONFORMANCE.md"), content, *updateConformance)
}
