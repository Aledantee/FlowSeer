package gnmi_test

import (
	"flag"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/internal/conformance"
)

// updateConformance refreshes the committed CONFORMANCE.md. Run with
//
//	go test ./src/protocol/gnmi -run TestConformanceMatrixUpToDate -update-conformance
var updateConformance = flag.Bool("update-conformance", false, "rewrite CONFORMANCE.md from the corpus")

// gnmiCorpus is the library's quirk catalog: device and server quirks
// recorded with provenance and covered by cited tests.
//
// Two kinds of row live here and are held to different bars. A quirk
// row states how the library behaves against a device that does
// something awkward, and any device exhibiting it closes the row. A
// `gn-t4-*` row instead asserts what a named device family does, so
// only that family's hardware closes it — which is why an observation
// on one vendor can close the first kind and leave the second
// pending.
var gnmiCorpus = []conformance.Row{
	{
		ID: "gn-proto-only-encoding", Clause: "gNMI spec §2.2.3",
		Provenance:  "encoding negotiation: not every peer offers JSON_IETF",
		Behavior:    "a peer advertising only PROTO still round-trips Get; JSON_IETF is preferred when offered",
		Adversarial: "CapabilityResponse advertising only PROTO, values as scalar TypedValues",
		Unit:        "gnmi/session", Status: conformance.Covered,
	},
	{
		ID: "gn-leaf-list-typed-value", Clause: "gNMI spec §2.2.3 (leaflist_val)",
		Provenance: "Arista vEOS-lab 4.33.1.1F serves /interfaces/interface/ethernet/state/supported-speeds as a leaf-list",
		Behavior: "a leaflist_val update decodes into the update's Values in wire order, an empty leaf-list included, " +
			"and the scalar Value stays nil; the row codec renders those values as the JSON array a LeafList field " +
			"expects, and a leaf-list written through PathValue.Values encodes back to leaflist_val",
		Adversarial: "supported-speeds carrying two enum elements, and an empty ScalarArray",
		Unit:        "gnmi/session", Status: conformance.Covered,
	},
	{
		ID: "gn-banner-newline-normalization", Clause: "gNMI spec §3.4 (Set/Get round-trip)",
		Provenance:  "Arista vEOS-lab 4.33.1.1F, lab device 2026-09-18",
		Behavior:    "a device may normalize a written leaf rather than store it verbatim; EOS appends a trailing newline to /system/config/login-banner, so a Set/Get round-trip compares modulo that normalization instead of by exact equality",
		Adversarial: "login-banner set to \"flowseer-t4\" and read back as \"flowseer-t4\\n\"",
		Unit:        "gnmi/lab", Status: conformance.Covered,
	},
	{
		ID: "gn-per-path-set-error", Clause: "gNMI spec §3.4.2",
		Provenance:  "deprecated UpdateResult.Message is the only per-path failure channel several implementations use",
		Behavior:    "a Set response carrying a per-path error surfaces which path failed as an attribute",
		Adversarial: "SetResponse whose UpdateResult carries a populated (deprecated) Error message",
		Unit:        "gnmi/session", Status: conformance.Covered,
	},
	{
		ID: "gn-stream-termination-latch", Clause: "gNMI spec §3.5",
		Provenance:  "session lifecycle: a dropped stream never resumes itself",
		Behavior:    "abrupt server termination of a STREAM subscription latches Stream.Err; buffered events stay readable; ONCE completion is clean",
		Adversarial: "server returning a gRPC error after the sync marker",
		Unit:        "gnmi/session", Status: conformance.Covered,
	},
	{
		ID: "gn-presync-buffering", Clause: "gNMI spec §3.5.1.4",
		Provenance:  "sync_response is the cold-start-complete signal",
		Behavior:    "updates before sync_response buffer as initial state and emit as Added at sync; each post-sync notification batch emits at most one event per affected row",
		Adversarial: "row leaves split across multiple pre-sync batches; a post-sync batch touching two leaves of one row",
		Unit:        "gnmi/watch", Status: conformance.Covered,
	},
	{
		ID: "gn-subscribe-once-snapshot", Clause: "gNMI spec §3.5.1.5.1",
		Provenance:  "FlowSeer reference target (t1)",
		Behavior:    "Subscribe ONCE assembles the snapshot's leaf updates into typed rows through the generated descriptor and ends cleanly at sync",
		Adversarial: "real gRPC stream from the containerized reference target",
		Unit:        "gnmi/integration", Status: conformance.Covered,
	},
	{
		ID: "gn-stream-sync-cold-start", Clause: "gNMI spec §3.5.1.5.2",
		Provenance:  "FlowSeer reference target (t1)",
		Behavior:    "STREAM cold start emits Added per row at sync, then the target's periodic leaf change arrives as one Modified per batch",
		Adversarial: "real streaming subscription with device-owned cadence",
		Unit:        "gnmi/integration", Status: conformance.Covered,
	},
	{
		ID: "gn-aruba-set-capability", Clause: "gNMI spec §3.4",
		Provenance: "Aruba CX lab capability check — no lab device serves gNMI",
		Behavior:   "records that Aruba CX gNMI write capability is unverifiable in this lab, and where the config write goes instead",
		Accepted: "No lab Aruba CX serves gNMI: the 10.07 switch simulator has no gnmi command and neither 830 nor 9339 listens. " +
			"AOS-CX gNMI is telemetry-oriented and its config plane is proprietary REST, so write capability cannot be proven here. " +
			"The Aruba write criterion converts to a documented gap; gNMI Set is proven on Arista vEOS-lab instead (row gn-t4-set-verdict).",
		Unit: "gnmi/lab", Status: conformance.AcceptedRisk,
	},
	{
		ID: "gn-t4-identity", Clause: "device identity reads",
		Provenance:  "Arista vEOS-lab 4.33, lab device 2026-09-18",
		Behavior:    "hostname, software version, serial, and part number return via Get over the advertised OpenConfig models",
		Adversarial: "live Get of system/state/{hostname,software-version} and components/component/state/{serial-no,part-no} against the Arista node",
		Unit:        "gnmi/lab", Status: conformance.Covered,
	},
	{
		ID: "gn-t4-set-verdict", Clause: "config write capability",
		Provenance:  "Arista vEOS-lab 4.33, lab device 2026-09-18",
		Behavior:    "a reversible login-banner Set is verified by Get; EOS normalizes the banner with a trailing newline, so the round-trip compares modulo that normalization, and the banner is restored",
		Adversarial: "live Set of /system/config/login-banner to \"flowseer-t4\", read back and restored, against the Arista node",
		Unit:        "gnmi/lab", Status: conformance.Covered,
	},
	{
		ID: "gn-t4-stream", Clause: "interface state subscription",
		Provenance:  "Arista vEOS-lab 4.33, lab device 2026-09-18",
		Behavior:    "a STREAM subscription over interface state delivers sync and keeps flowing on hardware",
		Adversarial: "live STREAM subscription over interfaces against the Arista node, counting leaf-list updates past sync",
		Unit:        "gnmi/lab", Status: conformance.Covered,
	},
	{
		ID: "gn-t4-revision-drift", Clause: "model revision comparison",
		Provenance:  "Arista vEOS-lab 4.33, lab device 2026-09-18",
		Behavior:    "advertised model versions diff against the committed lockfile; drift surfaces as warnings",
		Adversarial: "live Capabilities model set from the Arista node diffed against the committed lockfile",
		Unit:        "gnmi/lab", Status: conformance.Covered,
	},
}

// gnmiFamilies groups the corpus for CONFORMANCE.md.
var gnmiFamilies = []conformance.Family{
	{Prefix: "gn-", Title: "gNMI encoding, Set, and Subscribe behavior"},
}

// gnmiAllowlist gates accepted-risk rows. gn-aruba-set-capability is
// ratified: no lab Aruba CX serves gNMI, so its write capability is
// unverifiable here and gNMI Set is proven on Arista instead.
var gnmiAllowlist = map[string]bool{
	"gn-aruba-set-capability": true,
}

// TestConformanceCorpusIntegrity is the always-on gate.
func TestConformanceCorpusIntegrity(t *testing.T) {
	conformance.RunIntegrity(t, gnmiCorpus, gnmiAllowlist, gnmiFamilies, conformance.CorpusDirs(t))
}

// TestConformanceMatrixUpToDate keeps CONFORMANCE.md generated.
func TestConformanceMatrixUpToDate(t *testing.T) {
	content := conformance.Markdown("gNMI library conformance corpus", gnmiCorpus, gnmiFamilies)
	conformance.VerifyMarkdown(t, filepath.Join(conformance.CorpusDirs(t)[0], "CONFORMANCE.md"), content, *updateConformance)
}
