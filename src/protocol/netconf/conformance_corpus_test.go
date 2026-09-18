package netconf_test

import (
	"flag"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/internal/conformance"
)

// updateConformance refreshes the committed CONFORMANCE.md. Run with
//
//	go test ./src/protocol/netconf -run TestConformanceMatrixUpToDate -update-conformance
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
		Unit: "netconf/session", Status: conformance.Covered,
	},
	{
		ID: "nc-lock-denied-retryable", Clause: "RFC 6241 §7.5",
		Provenance: "RFC 6241 lock-denied semantics; multi-manager labs",
		Behavior:   "lock-denied rpc-error maps to the retryable netconf/lock-denied code, never a terminal session error",
		Adversarial: "injected <rpc-error> with error-tag lock-denied " +
			"on <lock>",
		Unit: "netconf/session", Status: conformance.Covered,
	},
	{
		ID: "nc-validate-fail-discard-unlock", Clause: "RFC 6241 §8.6",
		Provenance:  "candidate validation must complete before commit",
		Behavior:    "a validation failure triggers discard-changes plus unlock before surfacing the device error; commit never runs",
		Adversarial: "injected rpc-error on <validate> mid-Apply",
		Unit:        "netconf/session", Status: conformance.Covered,
	},
	{
		ID: "nc-dead-transport-latch", Clause: "RFC 6241 §2",
		Provenance:  "session lifecycle: dead SSH transports latch a terminal error",
		Behavior:    "a peer that stops responding trips the keepalive guard within the configured deadline and latches Err; later RPCs fail fast",
		Adversarial: "transport hanging every RPC until context deadline",
		Unit:        "netconf/session", Status: conformance.Covered,
	},
	{
		ID: "nc-candidate-edit-cycle", Clause: "RFC 6241 §8.3/§8.4",
		Provenance:  "netopeer2 t1 reference server",
		Behavior:    "lock → edit-config → validate → commit → unlock round-trips a fixture edit into running, proven by read-back walk",
		Adversarial: "real candidate datastore on netopeer2 with the yanggen fixture modules installed",
		Unit:        "netconf/integration", Status: conformance.Covered,
	},
	{
		ID: "nc-invalid-edit-discard", Clause: "RFC 7950 §9.2.4",
		Provenance:  "netopeer2 t1 reference server (sysrepo range validation)",
		Behavior:    "an out-of-range leaf fails the edit; the library discards and unlocks; read-back shows running unchanged and the candidate lock is free",
		Adversarial: "port 0 against fixture-types port-number range 1..65535",
		Unit:        "netconf/integration", Status: conformance.Covered,
	},
	{
		ID: "nc-t4-identity", Clause: "device identity reads",
		Provenance:  "Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18",
		Behavior:    "hostname, serial, model, and OS version return as typed values via the generated native and device-hardware bindings",
		Adversarial: "identity read against a device whose models predate the vendored tree by six years (device native 2020-07-02, vendored 2026-02-01); all four fields still decode",
		Unit:        "netconf/lab", Status: conformance.Covered,
	},
	{
		ID: "nc-t4-invalid-rollback", Clause: "rejected edit leaves running unchanged",
		Provenance: "Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18",
		Behavior: "an out-of-range leaf is rejected with an rpc-error the library surfaces as its RPC error code; " +
			"a read-back diff of the whole native subtree proves running unchanged. This device advertises " +
			"writable-running and no candidate datastore, so the edit targets running directly and the proof is the " +
			"read-back diff rather than a candidate discard",
		Adversarial: "username privilege 99 against the uint8 0..15 range; the edit fails and the library reports its RPC error code",
		Unit:        "netconf/lab", Status: conformance.Covered,
	},
	{
		ID: "nc-t4-reversible-edit", Clause: "reversible config edit",
		Provenance: "Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18",
		Behavior: "a create and its delete each round-trip against the running datastore, both proven by read-back; " +
			"the device is left without the fixture username",
		Adversarial: "a username created then removed with nc:operation=delete, read back after each half",
		Unit:        "netconf/lab", Status: conformance.Covered,
	},
	{
		ID: "nc-t4-interface-walk", Clause: "typed interface state walk",
		Provenance:  "Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18",
		Behavior:    "the interface-state Walker completes with typed rows on hardware",
		Adversarial: "walk over a device serving ietf-interfaces 2014-05-08 while the bindings come from the vendored 26.11 tree; five interfaces decode",
		Unit:        "netconf/lab", Status: conformance.Covered,
	},
	{
		ID: "nc-t4-watch-induced", Clause: "interface change detection",
		Provenance:  "Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18 (operator-induced toggle)",
		Behavior:    "an interface state change between ticks emits exactly one Modified for that row on hardware",
		Adversarial: "GigabitEthernet3 taken out of shutdown once inside a three-minute window at a ten-second poll interval; exactly one Modified observed",
		Unit:        "netconf/lab", Status: conformance.Covered,
	},
	{
		ID: "nc-t4-revision-drift", Clause: "model revision comparison",
		Provenance:  "Cisco CSR1000v running IOS-XE 17.3.2, lab device 2026-09-18",
		Behavior:    "device-advertised module revisions diff against the committed lockfile; drift surfaces as warnings",
		Adversarial: "493 device modules compared against the vendored 26.11 lockfile; drift reported for most of them, the widest being CISCO-RF-MIB at vendored 2023-07-13 against device 2005-09-01",
		Unit:        "netconf/lab", Status: conformance.Covered,
	},
}

// netconfFamilies groups the corpus for CONFORMANCE.md.
var netconfFamilies = []conformance.Family{
	{Prefix: "nc-", Title: "NETCONF session and edit behavior"},
}

// netconfAllowlist gates accepted-risk rows; empty until one is
// ratified.
var netconfAllowlist = map[string]bool{}

// TestConformanceCorpusIntegrity is the always-on gate.
func TestConformanceCorpusIntegrity(t *testing.T) {
	conformance.RunIntegrity(t, netconfCorpus, netconfAllowlist, netconfFamilies, conformance.CorpusDirs(t))
}

// TestConformanceMatrixUpToDate keeps CONFORMANCE.md generated.
func TestConformanceMatrixUpToDate(t *testing.T) {
	content := conformance.Markdown("NETCONF library conformance corpus", netconfCorpus, netconfFamilies)
	conformance.VerifyMarkdown(t, filepath.Join(conformance.CorpusDirs(t)[0], "CONFORMANCE.md"), content, *updateConformance)
}
