package snmp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Conformance corpus (SNMP test-completeness & conformance hardening).
//
// This file is the durable, re-runnable coverage map and the
// provenance-citing regression corpus for the native SNMP codec.
// Every cataloged quirk from the hardening plan's Tables 1-3 — plus the
// planning-discovered rows — is one [corpusRow] here. The completeness gate
// ([TestConformanceCorpusIntegrity] + the build-tagged TestConformanceCorpusComplete
// in conformance_complete_test.go) makes completeness provable rather than asserted.
//
// Why a _test.go file and not a buildable conformance_corpus.go: the only
// consumer is the gate and the CONFORMANCE.md generator, both of which live
// in the test binary. Keeping the manifest out of the compiled package
// surface avoids exporting types with no production consumer; the generator
// is the golden-style TestConformanceMatrixUpToDate below.
//
// Provenance rule: a row whose Status is "covered" MUST be cited by a
// `// Covers conformance matrix row: <ID>` comment on the test that exercises
// it, AND must catalog the adversarial input in the row's AdversarialInput
// field. The gate enforces this metadata (marker present + non-empty input); it
// cannot statically prove the cited test actually drives that input, so that the
// pin exercises the threat (not a happy path) remains a review responsibility —
// the AdversarialInput field exists to make that review concrete and diffable.

// corpusStatus is the lifecycle state of a corpus row.
//
//	pending      — cataloged but not yet pinned/closed. Allowed on the dev
//	               branch (the integrity gate stays green); the build-tagged
//	               completeness gate (run at merge time / in CI) forbids it.
//	covered      — a citing test exists AND an adversarial input is named.
//	accepted-risk — deliberately not closed; requires a reason AND membership
//	               on acceptedRiskAllowlist (a reviewable diff, not a free string).
type corpusStatus string

const (
	statusPending      corpusStatus = "pending"
	statusCovered      corpusStatus = "covered"
	statusAcceptedRisk corpusStatus = "accepted-risk"
)

// corpusRow is one cataloged conformance quirk: a paid-for lesson from
// gosnmp/net-snmp/a vendor bug, transcribed into this codec's test suite.
type corpusRow struct {
	ID          string       // stable kebab-case id, also the marker join key
	Clause      string       // RFC/X.690 clause the row enforces
	Provenance  string       // gosnmp #N / net-snmp flag / vendor bug id
	Behavior    string       // expected behavior / tolerance policy
	Adversarial string       // the malformed/misbehaving input the pin MUST drive (required when covered)
	Unit        string       // owning plan implementation unit (U-ID)
	Status      corpusStatus // pending | covered | accepted-risk
	Accepted    string       // why-not-covered rationale (required when accepted-risk)
}

// conformanceCorpus is the master catalog. Rows start "pending" and flip to
// "covered"/"accepted-risk" as their owning unit lands, keeping the integrity
// gate green from this commit forward: the catalog is seeded once and rows
// flip in place, never added lazily.
//
// Editing rules:
//   - never delete a row (a removed quirk loses its institutional memory);
//   - flip Status to "covered" only together with the citing test + Adversarial;
//   - flip Status to "accepted-risk" only together with Accepted + an
//     acceptedRiskAllowlist entry.
var conformanceCorpus = []corpusRow{
	// ---- Table 1: encoding / type conformance (vendor-agnostic) ----
	{ID: "enc-counter64-v1", Clause: "RFC 2576 §3", Provenance: "gosnmp (no check)", Behavior: "Counter64 in a v1 response: decode + typed warning (data preserved), never panic/type-confusion", Adversarial: "SNMPv1 message carrying a Counter64 varbind", Unit: "U4", Status: statusCovered},
	{ID: "enc-zerolen-int", Clause: "X.690 §8.3.1", Provenance: "gosnmp #241; RB11", Behavior: "zero-length signed INTEGER (02 00) errors; zero-length unsigned counter tolerated as 0 (recorded leniency)", Adversarial: "zero-length signed INTEGER TLV (02 00)", Unit: "U3/U4", Status: statusCovered},
	{ID: "enc-nonminimal-int", Clause: "X.690 §8.3.2", Provenance: "gosnmp #371", Behavior: "encode minimal (-1 -> FF); decode tolerates non-minimal", Adversarial: "non-minimal 4-octet INTEGER 0xFFFFFFFF on decode", Unit: "U3", Status: statusCovered},
	{ID: "enc-maxrep-signed", Clause: "RFC 3416", Provenance: "gosnmp #293", Behavior: "GETBULK max-repetitions 128-255 encode unsigned, not negative", Adversarial: "GETBULK with max-repetitions 128/200/255 (high-bit byte)", Unit: "U4", Status: statusCovered},
	{ID: "enc-id-range", Clause: "RFC 3412", Provenance: "gosnmp #272", Behavior: "msgID/request-id stay in 0..2^31-1, no overflow to negative", Adversarial: "request-id at 2^31-1 (math.MaxInt32) round-tripped through encode/decode", Unit: "U3", Status: statusCovered},
	{ID: "enc-ipaddr-longform", Clause: "X.690 §8.1.3", Provenance: "gosnmp #544", Behavior: "long-form BER length on IpAddress (40 81 04 ..) decodes to the 4-byte address", Adversarial: "IpAddress TLV with long-form length (40 81 04 ..) wrapping a 4-octet address", Unit: "U3", Status: statusCovered},
	{ID: "enc-ipaddr-8byte", Clause: "RFC 2578 §7.1.5", Provenance: "gosnmp #544", Behavior: "IpAddress with 8 content bytes -> typed error, not panic (no sane IP)", Adversarial: "IpAddress TLV with 8 content octets", Unit: "U3", Status: statusCovered},
	{ID: "enc-opaque-unknown", Clause: "RFC 2856", Provenance: "gosnmp #374", Behavior: "unknown Opaque sub-type (0x7a) -> raw bytes, not nil", Adversarial: "Opaque value with unknown wrapped-real marker (9F 7A ..)", Unit: "U3", Status: statusCovered},
	{ID: "enc-opaque-zero", Clause: "RFC 2856", Provenance: "gosnmp #453", Behavior: "Opaque float/double 0.0 -> full 4/8-byte IEEE-754, not truncated", Adversarial: "Opaque float/double 0.0 (all-zero IEEE-754 payload)", Unit: "U3", Status: statusCovered},
	{ID: "enc-unsigned-as-signed", Clause: "RFC 2578 §7.1.6", Provenance: "Check Point sk115119; IBM IZ77427", Behavior: "INTEGER-tagged (0x02) value with MSB set decodes to a negative Integer32Var; coercion to uint32 is rejected as ErrLossyConversion (reject, not reinterpret)", Unit: "U3", Status: statusAcceptedRisk, Accepted: "A negative INTEGER-tagged value is indistinguishable from a genuine -1; rejecting (the MikroTik leniency precedent) is safer than silently reinterpreting it as ~4 billion. Reinterpretation would weaken sign-error detection. Pinned by TestEncUnsignedAsSigned_RejectsNegativeIntTagged."},
	{ID: "enc-neg-length", Clause: "X.690 §8.1.3", Provenance: "gosnmp #552", Behavior: "length byte sign-extending to negative int64 -> no negative-index panic (named fuzz seed)", Adversarial: "TLV with long-form length octet 0x88 (04 88 7f ff ..) announcing 8 length octets", Unit: "U3", Status: statusCovered},
	{ID: "enc-dateandtime-lens", Clause: "RFC 2579", Provenance: "snmp_exporter #321", Behavior: "DateAndTime 0-byte (unknown) and 7/other-byte variants handled distinctly", Adversarial: "DateAndTime OCTET STRING of 0 and 7 (and other off-) octets", Unit: "U3", Status: statusCovered},
	{ID: "enc-bits-padding", Clause: "RFC 2579", Provenance: "RFC 2579", Behavior: "BITS with trailing zero padding / short value tolerated", Adversarial: "BIT STRING with trailing zero padding, short, and empty content", Unit: "U4", Status: statusCovered},
	{ID: "enc-timeticks-range", Clause: "RFC 2578", Provenance: "RFC 2578", Behavior: "TimeTicks 5-byte unsigned / out-of-range masked to 32 bits (scoped to TimeTicks)", Adversarial: "5-octet TimeTicks content 0x01_00_00_00_2A (2^32+42)", Unit: "U4", Status: statusCovered},
	{ID: "enc-v1trap-spectrap", Clause: "RFC 2576", Provenance: "gosnmp #182", Behavior: "SNMPv1 Trap specific-trap > 127 not byte-truncated in v1->v2c translation", Adversarial: "SNMPv1 enterpriseSpecific trap with specific-trap=200", Unit: "U4", Status: statusCovered},

	// ---- Table 2: walk / transport behavioral ----
	{ID: "walk-toobig-fallback", Clause: "RFC 3416", Provenance: "MikroTik(>50), Cisco/IOS-XR, Nokia, F5", Behavior: "tooBig -> halve max-repetitions -> fall back to GETNEXT-per-OID; full table, no abort", Adversarial: "agent returning tooBig above a max-rep threshold, and one rejecting GetBulk at every max-rep", Unit: "U5", Status: statusCovered},
	{ID: "walk-mid-pdu-eomv", Clause: "RFC 3416", Provenance: "dense carrier tables", Behavior: "single-chain GETBULK: yield all preceding values, terminate at the EndOfMibView varbind (no data loss)", Adversarial: "GETBULK chain of N values followed by EndOfMibView in one PDU", Unit: "U10", Status: statusCovered},
	{ID: "walk-nosuch-semantics", Clause: "RFC 3416", Provenance: "RFC 3416", Behavior: "noSuchInstance = skip & continue (advance cursor); noSuchObject on subtree root = abort that subtree; classified before the cycle guard", Adversarial: "MIB with noSuchInstance and noSuchObject varbinds interleaved with values", Unit: "U10", Status: statusCovered},
	{ID: "walk-mutate-midwalk", Clause: "RFC 3416", Provenance: "Ruckus/Aruba/UniFi WLAN", Behavior: "rows added/dropped mid-walk -> self-consistent snapshot tolerating index gaps + duplicate indices, no loop/error", Adversarial: "responder dropping and adding rows between successive GETBULK PDUs (maxPerPDU forces multi-PDU)", Unit: "U10", Status: statusCovered},
	{ID: "walk-cycling-oid", Clause: "RFC 3416", Provenance: "gosnmp #401 Juniper; Cisco CSCuf16921", Behavior: "non-increasing/cycling OIDs -> bounded skip-forward, no infinite loop/OOM; cycle distinct from single regress", Adversarial: "scripted GetNext returning an exact-repeat OID, and a non-increasing-then-forward sequence", Unit: "U6", Status: statusCovered},
	{ID: "walk-leaf-start", Clause: "RFC 3416", Provenance: "gosnmp #170", Behavior: "walk starting on a leaf OID returns the next OID, not nothing", Adversarial: "BulkWalk rooted at a scalar leaf node (sysDescr) with only the .0 instance present", Unit: "U6", Status: statusCovered},
	{ID: "walk-large-value-hang", Clause: "RFC 3416", Provenance: "gosnmp #408", Behavior: "BulkWalk must not hang on a varbind with a >1KB OctetString value", Adversarial: "table row carrying a 4096-byte OctetString value", Unit: "U6", Status: statusCovered},
	{ID: "txp-dup-response", Clause: "RFC 3416", Provenance: "gosnmp #417", Behavior: "duplicate/retransmitted response dropped by request-id demux; later genuine reply still resolves", Adversarial: "agent emitting a late duplicate of an already-delivered reply", Unit: "U6", Status: statusCovered},
	{ID: "txp-subtree-exit", Clause: "RFC 3416", Provenance: "RFC 3416", Behavior: "returned OID outside requested subtree prefix -> normal (non-error) termination", Adversarial: "agent returning an OID in a sibling column outside the requested subtree", Unit: "U6", Status: statusCovered},

	// ---- Table 3: SNMPv3 / USM ----
	{ID: "usm-authbit-bypass", Clause: "RFC 3414 §3.2", Provenance: "gosnmp #496", Behavior: "auth/priv downgrade matrix: cleared auth/priv bit on a configured session -> ErrUSMDowngrade before HMAC; Report authNoPriv-on-authPriv allowed", Adversarial: "replies with the auth bit cleared at authNoPriv/authPriv and the priv bit cleared at authPriv; plus an authNoPriv Report on an authPriv session", Unit: "U7", Status: statusCovered},
	{ID: "usm-report-randomid", Clause: "RFC 3414 §4", Provenance: "gosnmp #139", Behavior: "unsolicited/mismatched-id Report dropped-and-counted, never aborts a waiter; genuine reply still resolves", Adversarial: "agent injecting an authenticated Report with a mismatched msgID before the genuine reply", Unit: "U7", Status: statusCovered},
	{ID: "usm-aes-keyext", Clause: "RFC 3826; draft-reeder", Provenance: "gosnmp #424; Cisco/Extreme", Behavior: "AES-192/256 Blumenthal vs Reeder (C) key-extension vectors; distinct AES192 vs AES192C keys (unit-level oracle; no net-snmp cell)", Adversarial: "external pysnmp/hashlib vectors for AES-192/256 across MD5/SHA auth, Blumenthal vs Reeder", Unit: "U8", Status: statusCovered},
	{ID: "usm-keycache-passphrase", Clause: "RFC 3414 §2.6", Provenance: "gosnmp #424", Behavior: "no passphrase-keyed cache exists; keys derived per (engineID,user) from passphrase material", Unit: "U8", Status: statusAcceptedRisk, Accepted: "There is no passphrase-keyed key cache, so the failure mode the quirk guards against (a stale key returned for a changed priv passphrase) cannot occur. Grounded by TestUSM_KeyDerivationPerPrivPassphrase: differing priv passphrases derive different keys."},
	{ID: "usm-3step-discovery", Clause: "RFC 3414 §4", Provenance: "gosnmp #511", Behavior: "initial discovery performs the authenticated boots/time resync before the first real request", Adversarial: "empty-EngineID session forced through probe -> unknownEngineID Report -> authenticated request", Unit: "U8", Status: statusCovered},
	{ID: "usm-trap-reportable", Clause: "RFC 3412 §6.4", Provenance: "gosnmp #391", Behavior: "reportable-flag handling correct on received v3 traps vs informs", Adversarial: "decoded v3 trap (reportable clear) vs v3 inform (reportable set); tampered trap dropped with no Report", Unit: "U8", Status: statusCovered},

	// ---- raw fast path (fused decode / generic fallback boundary) ----
	{ID: "raw-wrong-typed-column", Clause: "RFC 2578 §7.1.6", Provenance: "telegraf #14598; snmp_exporter #338 (proprietary/buggy agents reporting types diverging from the MIB declaration)", Behavior: "column value whose wire tag diverges from the MIB-declared Kind: the fused arm declines (ok=false, never an error) and the generic decoder's coercion rules apply — values and errors identical to the pre-R25 path", Adversarial: "walk where a Counter32-declared column arrives Gauge32-tagged (coerces) and an Integer32-declared column arrives OctetString-tagged (typed mismatch error)", Unit: "R25", Status: statusCovered},
	{ID: "raw-noncanonical-oid-arc", Clause: "X.690 §8.19.2", Provenance: "chemist/snmp #17 (agents emitting BER that is valid but not shortest-form)", Behavior: "response name OID carrying a zero-padded (0x80-prefixed) sub-identifier: mirror validation refuses raw delivery and the read loop decodes eagerly — the walk yields identical data via pre-decoded varbinds, and the byte-order walk guards never see a non-canonical arc", Adversarial: "GetResponse datagram whose varbind name encodes a sub-identifier with a redundant leading 0x80 continuation octet", Unit: "R25", Status: statusCovered},

	// ---- Planning-discovered (deepening 2026-06-17/18, not in origin Tables) ----
	{ID: "usm-timewindow-rollback", Clause: "RFC 3414 §2.2.3", Provenance: "deepening (security-lens)", Behavior: "polling-side engineBaseline.update rejects a boots/time pair that would decrease boots or move time backward", Adversarial: "resync Report with lower engineBoots, and backward engineTime at the same boots", Unit: "U7", Status: statusCovered},
	{ID: "usm-msgid-predictability", Clause: "RFC 3412; KTD-6", Provenance: "deepening (security-lens)", Behavior: "msgID drawn fresh from the CSPRNG per message (not last+1), removing prediction in the unauthenticated window", Adversarial: "sample of consecutive msgIDs checked for previous+1 sequentiality", Unit: "U7", Status: statusCovered},
	{ID: "usm-inform-timewindow", Clause: "RFC 3414 §3.2", Provenance: "deepening (security-lens)", Behavior: "authoritative-role authoritativeTimeOK enforces the ±150s engineTime window + post-restart quarantine; int32 boundary safe (int64 diff)", Adversarial: "informs at window edges, during quarantine, wrong boots, and forged math.MinInt32/MaxInt32 engineTime", Unit: "U8", Status: statusCovered},
	{ID: "usm-authoritative-boots-pinned", Clause: "RFC 3414 §3.2", Provenance: "deepening (security-lens); KTD-12", Behavior: "authoritativeBoots pinned to 2^31-1 -> §3.2 boots-sequence check disabled for the listener role; quarantine reduces but does not close cross-restart inform replay", Unit: "U8", Status: statusAcceptedRisk, Accepted: "authoritativeBoots is pinned to 2^31-1 to avoid cross-restart persistent state; the §3.2 boots-sequence check is therefore disabled for the listener role and the time window is the sole gate. The post-restart quarantine (150s) closes the immediate replay window but not arbitrary pre-restart replays. Full closure needs persistent boots storage (named follow-up). Grounded by TestUSM_AuthoritativeBootsPinned."},
}

// acceptedRiskAllowlist gates which rows may carry Status accepted-risk.
// Adding a row here is a deliberate, reviewable diff — the governance bound
// that stops a genuine code gap from being relabeled to green under deadline.
var acceptedRiskAllowlist = map[string]bool{
	"enc-unsigned-as-signed":         true, // reject negative INTEGER-tagged unsigned rather than reinterpret
	"usm-keycache-passphrase":        true, // no passphrase-keyed cache exists
	"usm-authoritative-boots-pinned": true, // boots pinned, quarantine bounds replay (persistent-boots follow-up)
}

// kindBaselineTest maps every non-Unknown Kind to a baseline decode/structural
// test that exercises it. The enumeration guard ([TestConformanceCorpusEnumeration])
// asserts this map covers the full Kind enum, so a NEW Kind added to kind.go
// without baseline coverage fails the gate — the coverage-by-omission backstop
// (per the protobuf-structural-invariant-guards learning). Kinds with no quirk
// row (KindObjectID, KindNsapAddress, KindNull) are reachable here, so the
// guard needs no fake corpus rows for them.
var kindBaselineTest = map[Kind]string{
	KindInteger32:      "TestKind_OneToOneWithVarBind",
	KindUinteger32:     "TestKind_OneToOneWithVarBind",
	KindOctetString:    "TestKind_OneToOneWithVarBind",
	KindObjectID:       "TestKind_OneToOneWithVarBind",
	KindBitString:      "TestKind_OneToOneWithVarBind",
	KindCounter32:      "TestKind_OneToOneWithVarBind",
	KindGauge32:        "TestKind_OneToOneWithVarBind",
	KindTimeTicks:      "TestKind_OneToOneWithVarBind",
	KindCounter64:      "TestKind_OneToOneWithVarBind",
	KindIPAddress:      "TestKind_OneToOneWithVarBind",
	KindNsapAddress:    "TestKind_OneToOneWithVarBind",
	KindOpaque:         "TestKind_OneToOneWithVarBind",
	KindOpaqueFloat:    "TestKind_OneToOneWithVarBind",
	KindOpaqueDouble:   "TestKind_OneToOneWithVarBind",
	KindNull:           "TestKind_OneToOneWithVarBind",
	KindNoSuchObject:   "TestKind_OneToOneWithVarBind",
	KindNoSuchInstance: "TestKind_OneToOneWithVarBind",
	KindEndOfMibView:   "TestKind_OneToOneWithVarBind",
}

// wantKinds is the number of non-Unknown Kind values in kind.go. The
// enumeration guard pins kindBaselineTest to this count so a new Kind added to
// the enum without a baseline-test entry fails the gate. It matches the
// wantCases constant in TestKind_OneToOneWithVarBind.
const wantKinds = 18

// validateRow checks a single row's internal consistency given whether a
// citing marker was found and the accepted-risk allowlist. It is the gate's
// core predicate, factored out so TestConformanceGate_RejectsBadRows can
// prove it bites against synthetic rows.
func validateRow(r corpusRow, hasMarker bool, allowlist map[string]bool) error {
	switch r.Status {
	case statusCovered:
		if !hasMarker {
			return fmt.Errorf("row %q is covered but no `// Covers conformance matrix row: %s` marker was found in any _test.go", r.ID, r.ID)
		}
		if strings.TrimSpace(r.Adversarial) == "" {
			return fmt.Errorf("row %q is covered but catalogs no AdversarialInput — name the malformed/misbehaving input the pin drives", r.ID)
		}
	case statusAcceptedRisk:
		if strings.TrimSpace(r.Accepted) == "" {
			return fmt.Errorf("row %q is accepted-risk but has no Accepted rationale", r.ID)
		}
		if !allowlist[r.ID] {
			return fmt.Errorf("row %q is accepted-risk but is not on acceptedRiskAllowlist (add it as a reviewable diff)", r.ID)
		}
	case statusPending:
		// Allowed on the dev branch; the build-tagged completeness gate forbids it.
	default:
		return fmt.Errorf("row %q has unknown status %q", r.ID, r.Status)
	}
	return nil
}

var markerRe = regexp.MustCompile(`Covers conformance matrix row:\s*([A-Za-z0-9 §/.\-]+?)\s*(?:\(|$|\n|\.)`)

// forEachTestFile parses every _test.go in the package directory (not
// sub-packages), comments retained, and calls fn with each parsed file. It is
// the single package-dir AST scan shared by the corpus gate and the enumeration
// guard (mirroring the go/parser walk in no_gosnmp_test.go).
func forEachTestFile(t *testing.T, fn func(*ast.File)) {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Dir(here)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root {
				return filepath.SkipDir // package dir only, no sub-packages
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		fn(f)
		return nil
	})
	if err != nil {
		t.Fatalf("scanning test files: %v", err)
	}
}

// collectConformanceMarkers returns the set of corpus IDs cited by a
// `// Covers conformance matrix row: <ID>` marker in any _test.go comment.
func collectConformanceMarkers(t *testing.T) map[string]bool {
	t.Helper()
	known := make(map[string]bool, len(conformanceCorpus))
	for _, r := range conformanceCorpus {
		known[r.ID] = true
	}
	found := make(map[string]bool)
	forEachTestFile(t, func(f *ast.File) {
		for _, cg := range f.Comments {
			for _, m := range markerRe.FindAllStringSubmatch(cg.Text(), -1) {
				if id := strings.TrimSpace(m[1]); known[id] {
					found[id] = true
				}
			}
		}
	})
	return found
}

// TestConformanceCorpusIntegrity is the always-on gate. It fails
// when any row is internally inconsistent: a covered row without a citing
// marker or without a named adversarial input, or an accepted-risk row without
// a rationale or allowlist entry. Pending rows are permitted here — the
// build-tagged TestConformanceCorpusComplete (run at the merge gate) forbids them.
func TestConformanceCorpusIntegrity(t *testing.T) {
	// Duplicate-id guard.
	seen := make(map[string]bool, len(conformanceCorpus))
	for _, r := range conformanceCorpus {
		if seen[r.ID] {
			t.Errorf("duplicate corpus row id %q", r.ID)
		}
		seen[r.ID] = true
	}

	markers := collectConformanceMarkers(t)
	for _, r := range conformanceCorpus {
		if err := validateRow(r, markers[r.ID], acceptedRiskAllowlist); err != nil {
			t.Error(err)
		}
		// Family-membership guard: a row outside every rendered family
		// would vanish from CONFORMANCE.md's tables while still being
		// counted in its status tally.
		inFamily := false
		for _, fam := range conformanceFamilies {
			if strings.HasPrefix(r.ID, fam.prefix) {
				inFamily = true
				break
			}
		}
		if !inFamily {
			t.Errorf("row %q matches no conformanceFamilies prefix — it would be omitted from every CONFORMANCE.md table", r.ID)
		}
	}
}

// TestConformanceCorpusEnumeration is the coverage-by-omission backstop: every
// non-Unknown Kind must be reachable from a baseline test (or a corpus row),
// and every named baseline test must actually exist. A new Kind in kind.go
// without baseline coverage, or a stale baseline test name, fails here.
func TestConformanceCorpusEnumeration(t *testing.T) {
	// Backstop against a new Kind escaping coverage. The Kind enum is a
	// contiguous iota block [KindInteger32 .. wantKinds]; a Kind added beyond it
	// needs a String() case to be usable, so Kind(wantKinds+1) stops rendering as
	// the "Kind(?)" fallback. That trips this guard and forces wantKinds and
	// kindBaselineTest to be updated together — so a bare enum addition cannot
	// sail through green.
	if Kind(wantKinds+1).String() != "Kind(?)" {
		t.Errorf("a Kind value beyond %d exists in kind.go — bump wantKinds and add it to kindBaselineTest", wantKinds)
	}
	if len(kindBaselineTest) != wantKinds {
		t.Errorf("kindBaselineTest has %d entries, want %d — keep it in lockstep with the Kind enum", len(kindBaselineTest), wantKinds)
	}
	// Each referenced baseline test exists as a func in some _test.go.
	funcs := collectTestFuncNames(t)
	for k, name := range kindBaselineTest {
		if !funcs[name] {
			t.Errorf("kindBaselineTest[%v] references %q which is not a test function in the package", k, name)
		}
	}
}

// collectTestFuncNames returns the set of `func TestXxx(t *testing.T)` names
// declared in the package's _test.go files.
func collectTestFuncNames(t *testing.T) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	forEachTestFile(t, func(f *ast.File) {
		for _, dcl := range f.Decls {
			if fn, ok := dcl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				names[fn.Name.Name] = true
			}
		}
	})
	return names
}

// TestConformanceGate_RejectsBadRows proves the gate predicate bites: each
// synthetic malformed row must produce an error, and each well-formed row must
// not. This verifies that the gate logic is correct independent of the real
// corpus state.
func TestConformanceGate_RejectsBadRows(t *testing.T) {
	allow := map[string]bool{"ok-accepted": true}
	cases := []struct {
		name    string
		row     corpusRow
		marker  bool
		wantErr bool
	}{
		{"covered-no-marker", corpusRow{ID: "x", Status: statusCovered, Adversarial: "bad byte vector"}, false, true},
		{"covered-no-adversarial", corpusRow{ID: "x", Status: statusCovered}, true, true},
		{"covered-ok", corpusRow{ID: "x", Status: statusCovered, Adversarial: "bad byte vector"}, true, false},
		{"accepted-no-reason", corpusRow{ID: "ok-accepted", Status: statusAcceptedRisk}, false, true},
		{"accepted-off-allowlist", corpusRow{ID: "x", Status: statusAcceptedRisk, Accepted: "because"}, false, true},
		{"accepted-ok", corpusRow{ID: "ok-accepted", Status: statusAcceptedRisk, Accepted: "because"}, false, false},
		{"pending-ok", corpusRow{ID: "x", Status: statusPending}, false, false},
		{"unknown-status", corpusRow{ID: "x", Status: corpusStatus("bogus")}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRow(c.row, c.marker, allow)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateRow err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}

// --- CONFORMANCE.md generation ---

// conformanceFamilies partitions corpus rows into the rendered matrix's
// sections by ID prefix. Every row MUST match exactly one prefix — the
// integrity gate enforces this, because a row outside every family
// would be counted in the status tally yet silently dropped from all
// tables (the generator's one truncation hazard).
var conformanceFamilies = []struct{ prefix, title string }{
	{"enc-", "Encoding / type conformance (RFC 2576/2578/2579/3417, X.690)"},
	{"walk-", "Walk / transport behavior (RFC 3416)"},
	{"txp-", "Transport demux (RFC 3416)"},
	{"raw-", "Raw fast path fused/fallback boundary (R25, X.690 §8.19)"},
	{"usm-", "SNMPv3 / USM (RFC 3412/3414/3826)"},
}

// renderConformanceMatrix renders the committed coverage artifact: a table
// grouped by row-id family plus the accepted-risk allowlist. Deterministic so
// TestConformanceMatrixUpToDate can golden-compare it.
func renderConformanceMatrix(rows []corpusRow) string {
	var b strings.Builder
	b.WriteString("# SNMP Conformance Coverage Map\n\n")
	b.WriteString("Generated from `conformance_corpus_test.go`. Do not edit by hand —\n")
	b.WriteString("run `UPDATE_CONFORMANCE=1 go test ./common/snmp/ -run TestConformanceMatrixUpToDate`.\n\n")

	// Status tally.
	var covered, pending, accepted int
	for _, r := range rows {
		switch r.Status {
		case statusCovered:
			covered++
		case statusAcceptedRisk:
			accepted++
		default:
			pending++
		}
	}
	fmt.Fprintf(&b, "**Status:** %d covered · %d accepted-risk · %d pending · %d total\n\n",
		covered, accepted, pending, len(rows))

	for _, fam := range conformanceFamilies {
		fmt.Fprintf(&b, "## %s\n\n", fam.title)
		b.WriteString("| id | status | clause | provenance | behavior |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, r := range rows {
			if !strings.HasPrefix(r.ID, fam.prefix) {
				continue
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
				r.ID, r.Status, r.Clause, r.Provenance, r.Behavior)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Accepted-risk allowlist\n\n")
	b.WriteString("A row may carry `accepted-risk` status only if it appears here (a reviewable diff).\n\n")
	var allow []string
	for id := range acceptedRiskAllowlist {
		if acceptedRiskAllowlist[id] {
			allow = append(allow, id)
		}
	}
	sort.Strings(allow)
	if len(allow) == 0 {
		b.WriteString("_(none yet)_\n")
	} else {
		for _, id := range allow {
			fmt.Fprintf(&b, "- `%s`\n", id)
		}
	}
	return b.String()
}

// TestConformanceMatrixUpToDate keeps CONFORMANCE.md in lockstep with the
// manifest (durable, re-runnable). Set UPDATE_CONFORMANCE=1 to regenerate.
func TestConformanceMatrixUpToDate(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	path := filepath.Join(filepath.Dir(here), "CONFORMANCE.md")
	want := renderConformanceMatrix(conformanceCorpus)

	if os.Getenv("UPDATE_CONFORMANCE") != "" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatalf("writing CONFORMANCE.md: %v", err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading CONFORMANCE.md (run UPDATE_CONFORMANCE=1 go test to generate): %v", err)
	}
	if string(got) != want {
		t.Errorf("CONFORMANCE.md is stale — run `UPDATE_CONFORMANCE=1 go test ./common/snmp/ -run TestConformanceMatrixUpToDate`")
	}
}
