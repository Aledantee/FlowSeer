package netconf_test

import (
	"flag"
	"path/filepath"
	"runtime"
	"testing"

	"go.aledante.io/FlowSeer/src/common/internal/conformance"
)

// updateConformance refreshes the committed CONFORMANCE.md. Run with
//
//	go test ./src/common/netconf -run TestConformanceMatrixUpToDate -update-conformance
var updateConformance = flag.Bool("update-conformance", false, "rewrite CONFORMANCE.md from the corpus")

// netconfCorpus is the library's quirk catalog: append-only,
// provenance-cited, joined to its pinning tests by
// `// Covers conformance matrix row:` markers.
var netconfCorpus = []conformance.Row{
	{
		ID: "nc-candidate-running-readonly", Clause: "RFC 6241 §8.3",
		Provenance: "Cisco IOS-XE 17.x programmability guide: candidate mode disables writes to running",
		Behavior:   "edit target is capability-driven per session: candidate wins over writable-running; neither → typed unsupported error",
		Adversarial: "hello capability sets for candidate-only, candidate+writable-running, writable-running-only, " +
			"and read-only peers",
		Unit: "U5", Status: conformance.Covered,
	},
	{
		ID: "nc-lock-denied-retryable", Clause: "RFC 6241 §7.5",
		Provenance: "RFC 6241 lock-denied semantics; multi-manager labs",
		Behavior:   "lock-denied rpc-error maps to the retryable netconf/lock-denied code, never a terminal session error",
		Adversarial: "injected <rpc-error> with error-tag lock-denied " +
			"on <lock>",
		Unit: "U5", Status: conformance.Covered,
	},
	{
		ID: "nc-validate-fail-discard-unlock", Clause: "RFC 6241 §8.6",
		Provenance:  "AE2: rejected candidate must leave running unchanged",
		Behavior:    "a validate/commit failure triggers discard-changes plus unlock before surfacing the device error; commit never runs",
		Adversarial: "injected rpc-error on <validate> mid-Apply",
		Unit:        "U5", Status: conformance.Covered,
	},
	{
		ID: "nc-dead-transport-latch", Clause: "RFC 6241 §2",
		Provenance:  "session-lifecycle HTD: dead SSH transports must latch, not block",
		Behavior:    "a peer that stops responding trips the keepalive guard within the configured deadline and latches Err; later RPCs fail fast",
		Adversarial: "transport hanging every RPC until context deadline",
		Unit:        "U5", Status: conformance.Covered,
	},
	{
		ID: "nc-candidate-edit-cycle", Clause: "RFC 6241 §8.3/§8.4",
		Provenance:  "netopeer2 t1 reference server",
		Behavior:    "lock → edit-config → validate → commit → unlock round-trips a fixture edit into running, proven by read-back walk",
		Adversarial: "real candidate datastore on netopeer2 with the yanggen fixture modules installed",
		Unit:        "U9", Status: conformance.Covered,
	},
	{
		ID: "nc-invalid-edit-discard", Clause: "RFC 7950 §9.2.4",
		Provenance:  "netopeer2 t1 reference server (sysrepo range validation)",
		Behavior:    "an out-of-range leaf fails the edit; the library discards and unlocks; read-back shows running unchanged and the candidate lock is free",
		Adversarial: "port 0 against fixture-types port-number range 1..65535",
		Unit:        "U9", Status: conformance.Covered,
	},
	{
		ID: "nc-t4-identity", Clause: "AE1",
		Provenance: "IOS-XE lab device (pending)",
		Behavior:   "hostname, serial, model, and OS version return as typed values via the generated native and device-hardware bindings",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "nc-t4-invalid-rollback", Clause: "AE2",
		Provenance: "IOS-XE lab device (pending)",
		Behavior:   "a staged invalid candidate change is rejected, discarded, and unlocked; read-back diff proves running unchanged",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "nc-t4-reversible-edit", Clause: "R12",
		Provenance: "IOS-XE lab device (pending)",
		Behavior:   "a reversible edit round-trips through candidate/commit and its revert, both proven by read-back",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "nc-t4-interface-walk", Clause: "R11",
		Provenance: "IOS-XE lab device (pending)",
		Behavior:   "the interface-state Walker completes with typed rows on hardware",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "nc-t4-watch-induced", Clause: "AE4",
		Provenance: "IOS-XE lab device (pending; operator-induced toggle)",
		Behavior:   "an interface state change between ticks emits exactly one Modified for that row on hardware",
		Unit:       "U10", Status: conformance.Pending,
	},
	{
		ID: "nc-t4-revision-drift", Clause: "R8",
		Provenance: "IOS-XE lab device (pending)",
		Behavior:   "device-advertised module revisions diff against the committed lockfile; drift surfaces as warnings",
		Unit:       "U10", Status: conformance.Pending,
	},
}

// netconfFamilies groups the corpus for CONFORMANCE.md.
var netconfFamilies = []conformance.Family{
	{Prefix: "nc-", Title: "NETCONF session and edit behavior"},
}

// netconfAllowlist gates accepted-risk rows; empty until one is
// ratified.
var netconfAllowlist = map[string]bool{}

// corpusDirs returns the package and integration suite directories.
func corpusDirs(t *testing.T) []string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(here)
	return []string{dir, filepath.Join(dir, "..", "..", "..", "test", "integration", "netconf")}
}

// TestConformanceCorpusIntegrity is the always-on gate.
func TestConformanceCorpusIntegrity(t *testing.T) {
	conformance.RunIntegrity(t, netconfCorpus, netconfAllowlist, netconfFamilies, corpusDirs(t))
}

// TestConformanceMatrixUpToDate keeps CONFORMANCE.md generated.
func TestConformanceMatrixUpToDate(t *testing.T) {
	content := conformance.Markdown("NETCONF library conformance corpus", netconfCorpus, netconfFamilies)
	conformance.VerifyMarkdown(t, filepath.Join(corpusDirs(t)[0], "CONFORMANCE.md"), content, *updateConformance)
}
