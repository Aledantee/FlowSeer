---
title: "refactor: Clean up FlowSeer proto docs with qualified spec links"
date: 2026-08-26
type: refactor
depth: standard
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: ce-plan-bootstrap
---

# refactor: Clean up FlowSeer proto docs with qualified spec links

## Summary

Bring every FlowSeer-owned schema under `spec/proto/flowseer/` (45 files, five
`net.*.v1` packages, six READMEs) up to the documentation bar in
`docs/code-style-proto.md`: run a full standards audit that grounds every
normalized enum and value against its primary source, place a fully qualified
link inline on each citing comment, collapse the pre-release `reserved`
tombstones while the suspended-breaking-checks window is still open, and fix
the in-flight EUI rename that currently leaves the tree non-compiling. Vendor
mirrors under `spec/proto/ruckus/` are untouched.

---

## Problem Frame

The five network packages were built from a standards research pass
(`docs/architecture/2026-08-26-net-core-package-research.md`), but the schema
comments cite their sources as bare names — "RFC 8344", "IEEE 802.1Q",
"IANA registry" — with no link, and several derived taxonomies (PoE status,
FEC modes, FDB kinds) cite nothing at all. Generated docs are read by
consumers who never open this repo; a bare citation gives them nothing to
follow. Meanwhile an EUI rename (`mac_address`/`eui48_address`/
`eui64_address` → a combined `eui.proto`) sits *staged but uncommitted* in the
primary checkout — there, `buf lint` fails with four errors. On committed HEAD,
where implementation worktrees branch from, the three old files still exist
and `eui.proto` does not, so the rename must be authored by this plan rather
than inherited. Finally, the schemas carry `reserved` tombstones from
pre-release iteration;
once the first stable release turns `buf breaking` on, they can never be
collapsed again.

---

## Requirements

- **R1.** `buf lint` and `buf generate` succeed for the whole workspace: the
  EUI rename is completed in-tree — a combined `eui.proto` family replaces the
  three single-type address files, and both consumers are repointed to it.
- **R2.** Every schema comment or README statement that invokes an external
  standard carries a fully qualified link to that standard's primary source.
- **R3.** Full standards audit: every normalized enum, named registry value,
  and range constraint in the five packages is checked against its primary
  source; misattributed or unsupported claims are corrected, not just linked.
- **R4.** Package READMEs and file doc comments follow the conventions in
  `docs/code-style-proto.md` (contract-stating comments, absence semantics on
  every field, no process narration).
- **R5.** Pre-release `reserved` number/name tombstones in FlowSeer-owned
  files are collapsed and the freed numbers reclaimed by renumbering to
  contiguous, in one reviewed change inside the suspended-breaking-checks
  window.
- **R6.** `import` vs `import option` usage is normalized: plain `import`
  only where the file extends `buf.validate` rules, `import option` where the
  import exists solely to bring options into scope.
- **R7.** Descriptor changes stay confined to `spec/proto/flowseer/`;
  `spec/proto/ruckus/` (including its `nanopb/` subtree) is byte-identical
  before and after, and nothing is written under `frontend/`.
- **R8.** `generated/` is regenerated in the same change as each schema edit
  and `src/common/protoconformance/` passes `go test -race`.

### Assumptions

- The plan baselines on **committed HEAD**, where `buf.gen.go.yaml` exists and
  `buf.gen.yaml` still carries the TypeScript leg. The primary checkout's
  uncommitted trim of those files is in-flight work that does not reach a
  worktree branched from HEAD; regeneration therefore always uses the Go-only
  template (see Verification Contract). If the trim lands first, U5 step 3
  updates the workflow doc; if not, U5 step 3 is skipped and the discrepancy
  flagged to the user rather than resolved in this change.
- No serialized FlowSeer-proto payloads persist anywhere (pre-release, no
  stable consumers), so renumbering in R5 invalidates nothing.

---

## Scope Boundaries

**In scope:** `spec/proto/flowseer/**` (protos + READMEs), `spec/proto/README.md`,
`docs/code-style-proto.md` Workflow section, `src/common/protoconformance/`,
regenerated `generated/go/proto/flowseer/**`.

**Out of scope:** vendor mirrors (`spec/proto/ruckus/`), `spec/mib/`,
`spec/yang/`, any new message/field/package design (the research doc's
"defer" lists stay deferred), frontend, and `docs/conventions/protobuf.md`
(triad/ref model — these packages are Primitives and predate that layer).

### Deferred to Follow-Up Work

- `addr` package conformance coverage beyond what the EUI rename requires
  (a full `addr_rules_test.go` sweep of prefix/range/lifetime CEL rules is
  worthwhile but is test work, not doc cleanup).
- Any schema additions the audit reveals as *missing* (e.g. unnamed DSCP
  pool values, additional EtherTypes) — the audit corrects claims, it does
  not grow registries.

---

## Key Technical Decisions

- **KTD1 — Links live inline on the citing comment.** The URL sits in the
  doc comment of the exact field, enum, or value it grounds, so it survives
  into generated docs at the point of use. No file-header "See:" blocks.
  *(session-settled: user-directed — chosen over a per-file reference block:
  the link must reach the generated-docs reader at the value it justifies.)*
- **KTD2 — Audit depth is full, not mechanical.** Each named value is checked
  against the primary source (IANA registry entry, RFC section, IEEE
  standard), and a claim that does not hold is fixed in the comment — or
  surfaced as a schema question if the *value* is wrong. *(session-settled:
  user-directed — chosen over link-only and link-plus-derived variants.)*
- **KTD3 — Reserved tombstones are collapsed and numbers reclaimed now.**
  `breaking.use: []` until first stable release is the one-time window;
  fields and enum values are renumbered to contiguous and the `reserved`
  statements dropped, with `generated/` and conformance tests regenerated in
  the same change. *(session-settled: user-directed — chosen over keeping
  tombstones or docs-only cleanup.)*
- **KTD4 — The EUI-rename fix is unit one.** A green `buf lint` is the
  precondition for verifying everything else. *(session-settled:
  user-directed — chosen over treating the rename as an external
  precondition or reverting it.)*
- **KTD5 — Canonical link forms.** RFCs → `https://www.rfc-editor.org/rfc/rfcNNNN.html`
  (section anchors where a specific section is the claim); IANA →
  the registry page under `https://www.iana.org/assignments/…`; IEEE →
  the standard's page on `https://standards.ieee.org/` (content is paywalled,
  but it is the authoritative identifier; public IEEE YANG mirrors on
  `ieee802.org` may be cited *additionally* where they are the actual
  modeling source); protobuf/protovalidate → `protobuf.dev` /
  `protovalidate.com`. One link form per source family across the tree.
- **KTD6 — No new survey research; live registries for registry values.**
  The repo's net-core research doc (2026-08-26) already enumerates the
  primary sources with URLs; the audit verifies against those rather than
  re-surveying. Because those same sources authored the comments under
  audit, registry-assigned values (IANA numbers, EtherTypes, DSCP names)
  are additionally checked against the **live registry pages**, and any
  claim with no source recorded in the research doc is surfaced as an open
  question rather than linked on inference.

---

## Implementation Units

### U1. Complete the EUI rename and restore a green tree

**Goal:** The workspace compiles; a new combined `eui.proto` family is the
sole address-identity surface.
**Requirements:** R1, R6, R8.
**Dependencies:** none.
**Files:** new `spec/proto/flowseer/net/addr/v1/eui.proto`;
deleted `spec/proto/flowseer/net/addr/v1/mac_address.proto`,
`spec/proto/flowseer/net/addr/v1/eui48_address.proto`,
`spec/proto/flowseer/net/addr/v1/eui64_address.proto`;
`spec/proto/flowseer/net/l2/v1/fdb_entry.proto`,
`spec/proto/flowseer/net/l3/v1/neighbor_entry.proto`,
`src/common/protoconformance/l2_rules_test.go`,
`src/common/protoconformance/l3_rules_test.go`,
new `src/common/protoconformance/addr_rules_test.go`,
regenerated `generated/go/proto/flowseer/**`.
**Approach:**
1. Author `eui.proto` in the worktree: `Eui48Address` and `Eui64Address`
   (required 6-/8-octet `bytes octets`), plus a tagged `EuiAddress` wrapper
   (required oneof) carrying a doc comment mirroring `IpAddress`'s
   tagged-wrapper rationale. The primary checkout's staged-but-uncommitted
   rename is the reference shape; this unit authors it on the branch rather
   than inheriting it. Delete `mac_address.proto`, `eui48_address.proto`,
   and `eui64_address.proto` in the same change. Use `import option` for
   `buf/validate` (the file only consumes options — same fix pattern applies
   to `ip.proto`/`oui.proto` in U2).
2. Repoint `fdb_entry.proto`'s import from `eui48_address.proto` to
   `eui.proto` (its `Eui48Address` reference is otherwise unchanged).
3. In `neighbor_entry.proto`, replace `MacAddress` with the tagged
   `EuiAddress` wrapper. Rationale for the field comment: the neighbor
   cache's link-layer column is width-variable at its sources (RFC 4293's
   PhysAddress; IPv6 ND on non-Ethernet media reports EUI-64), and the
   tagged wrapper keeps the variant explicit instead of inferred from byte
   count — the addr package's own stated design rule.
4. Regenerate Go output with the Go-only template, update the two
   conformance suites' imports, and add `addr_rules_test.go` covering the new
   wrapper.
**Test scenarios:**
- `EuiAddress` with no arm set fails validation (oneof required).
- `EuiAddress` with a 6-octet `eui48` arm passes; 5- and 7-octet payloads fail.
- `EuiAddress` with an 8-octet `eui64` arm passes; a 6-octet payload fails.
- Existing `FdbEntry` unicast-MAC CEL case still passes against the repointed
  import.
- `NeighborEntry` with an absent link-layer address remains valid.
**Verification:** `buf lint` exits clean for the whole workspace;
`go test -race ./src/common/protoconformance/...` passes.
**Execution note:** smoke-first — lint and generate before touching tests.

### U2. Standards audit and qualified links: registry packages (`addr`, `packet`)

**Goal:** Every registry-derived value in the two vocabulary packages is
verified against and linked to its primary source.
**Requirements:** R2, R3, R4, R6.
**Dependencies:** U1.
**Files:** `spec/proto/flowseer/net/addr/v1/ip.proto`,
`spec/proto/flowseer/net/addr/v1/eui.proto`,
`spec/proto/flowseer/net/addr/v1/oui.proto`,
`spec/proto/flowseer/net/packet/v1/ether_type.proto`,
`spec/proto/flowseer/net/packet/v1/icmp.proto`,
`spec/proto/flowseer/net/packet/v1/ip_dscp.proto`,
`spec/proto/flowseer/net/packet/v1/ip_ecn.proto`,
`spec/proto/flowseer/net/packet/v1/ip_protocol.proto`,
`spec/proto/flowseer/net/packet/v1/tcp_flags.proto`,
`spec/proto/flowseer/net/packet/v1/transport_port.proto`;
regenerated `generated/go/proto/flowseer/**` (comments are emitted verbatim
into generated Go, so comment edits change generated output — R8 applies).
**Approach:**
1. Audit against the primary sources already collected in the research doc:
   `IpVersion` ↔ IANA Address Family Numbers; `IpLifetime` semantics ↔
   RFC 4861 §4.6.2 / RFC 8415; `IpDscp` names ↔ IANA DSCP registry (incl.
   LE = RFC 8622, NQB, VOICE-ADMIT); `IpEcn` ↔ RFC 3168; `IpProtocol` ↔ IANA
   Protocol Numbers; `EtherType` values ↔ IANA IEEE 802 Numbers; the
   1536-boundary claim ↔ IEEE 802.3 length/type rule; `TcpFlag` bit
   positions ↔ RFC 9293 §3.1; ICMP octet widths ↔ IANA ICMP registries;
   EUI-48/EUI-64/OUI ↔ the IEEE RA EUI guidelines.
2. Add the qualified link inline per KTD1/KTD5 on each claim; correct any
   comment the audit falsifies.
3. Normalize `import option` in `ip.proto` and `oui.proto` (options-only
   consumers); `ether_type.proto` and `tcp_flags.proto` keep plain `import`
   (they extend rules), and so does `vlan_id.proto` in U3.
4. If the audit falsifies a *value*, number, or range (not just a comment),
   record it as an open question carried into U4's single reviewed
   wire-change commit — never land a wire change inside this comment-audit
   unit.
5. Regenerate Go output in the same change (R8).
**Test scenarios:** Test expectation: none beyond regression — comment-only
plus import-kind edits; existing packet/validation conformance suites and
`buf lint` (including `PROTOVALIDATE` CEL compilation) must stay green.
**Verification:** a comment-block-aware check (not a single-line grep) shows
every standards mention in the two packages' `//` doc comments — all four
KTD5 source families: RFC, IANA, IEEE, and protobuf/protovalidate — resolves
to a fully qualified URL in the same comment block; protovalidate CEL
`message:`/`expression:` string literals are exempt (URLs do not belong in
runtime error text); `buf lint` clean; `generated/` shows no residual diff.

### U3. Standards audit and qualified links: interface packages (`phy`, `l2`, `l3`)

**Goal:** The normalized taxonomies in the three interface-facing packages
are grounded and linked, including the enums that currently cite nothing.
**Requirements:** R2, R3, R4.
**Dependencies:** U1.
**Files:** all `.proto` under `spec/proto/flowseer/net/phy/v1/`,
`spec/proto/flowseer/net/l2/v1/`, and `spec/proto/flowseer/net/l3/v1/`;
regenerated `generated/go/proto/flowseer/**` (R8).
**Approach:**
1. Ground the currently-uncited derived taxonomies: PoE status/class/role ↔
   IEEE 802.3 (clause 33 / 802.3bt terms) and RFC 3621 Power Ethernet MIB;
   FEC modes ↔ IEEE 802.3 clause 74 / RS-FEC clauses; duplex and
   auto-negotiation ↔ RFC 3635 / IEEE 802.3 clause 28; VLAN ranges, PCP,
   DEI, TPID, frame admission, ingress filtering ↔ IEEE 802.1Q;
   `VlanRegistration` and FDB kind/status ↔ RFC 4363 Q-BRIDGE-MIB;
   `AddressOrigin`/`AddressStatus`/`NeighborOrigin`/`NeighborReachability` ↔
   RFC 8344 (exact leaf names) and RFC 4293; IPv4 MTU floor ↔ RFC 8344's
   68..65535 range, IPv6 floor ↔ RFC 8200 §5.
2. Add qualified links inline; correct falsified claims. Where a value is
   FlowSeer-normalized rather than registry-assigned (e.g. `SwitchportMode`),
   the comment says so instead of borrowing a citation.
3. If the audit falsifies a *value*, number, or range, record it as an open
   question carried into U4's single reviewed wire-change commit — never
   land a wire change inside this comment-audit unit.
4. Regenerate Go output in the same change (R8).
**Test scenarios:** Test expectation: none beyond regression — comment-only
edits; l2/l3/phy conformance suites and `buf lint` stay green.
**Verification:** same gate as U2 — the comment-block-aware check covers all
four KTD5 source families in `//` doc comments, exempts CEL string literals,
and finds no bare standards name; `buf lint` clean; `generated/` shows no
residual diff.

### U4. Collapse pre-release reserved tombstones and renumber

**Goal:** The one-time pre-release window is used: no `reserved` statements
remain in FlowSeer-owned files, and numbering is contiguous.
**Requirements:** R5, R7, R8.
**Dependencies:** U1; sequence after U2/U3 so the audit's comment corrections
land before the mechanical renumber (one regen churn, cleaner review).
**Files:** `spec/proto/flowseer/net/l2/v1/vlan.proto`,
`spec/proto/flowseer/net/l3/v1/address_origin.proto`,
`spec/proto/flowseer/net/l3/v1/neighbor_entry.proto`,
`spec/proto/flowseer/net/addr/v1/ip.proto` (`Ipv4Prefix`/`Ipv6Prefix`
`reserved 3`),
`spec/proto/flowseer/net/phy/v1/ethernet_facet.proto`,
`spec/proto/flowseer/net/phy/v1/ethernet_medium.proto`,
`spec/proto/flowseer/net/phy/v1/poe_facet.proto`,
`spec/proto/flowseer/net/phy/v1/transceiver_facet.proto`,
regenerated `generated/go/proto/flowseer/**`, and the conformance suites the
renumber reaches: `src/common/protoconformance/addr_rules_test.go` (new in
U1), `l2_rules_test.go`, `l3_rules_test.go`, `phy_rules_test.go`.
**Approach:**
0. Precondition, fail closed: record an inventory confirming no serialized
   FlowSeer-proto payloads exist anywhere — test fixtures, caches, stored
   captures, external consumers. Any hit stops this unit until resolved.
1. Enumerate every `reserved` (numbers and names) in the module; classify
   each affected enum as FlowSeer-normalized or registry pass-through, and
   assert no registry pass-through value is renumbered (today none carries a
   reserved gap — this step makes that checked, not assumed). Then drop each
   statement and renumber the surviving fields/enum values to close the gap.
2. Regenerate and mechanically update conformance-test builders/enum
   references (generated Go identifiers do not change — only tags and enum
   wire numbers do — so edits should be nil-to-small).
3. `TestNeighborEntryReservedStateTag` in `l3_rules_test.go` is
   wire-number-dependent by design: it unmarshals raw bytes carrying the
   reserved neighbor-entry tag and asserts nothing populates. The renumber
   retires it. Because `src/common/protoconformance/` is an AGENTS.md policy
   surface, raise its removal as an explicit guardrail review before U4
   lands — never edit it away quietly inside the renumber commit.
4. Confirm `spec/proto/ruckus/` descriptors are untouched (`git status`
   scope check).
**Test scenarios:**
- All existing conformance cases except the retired reserved-tag guard pass
  unmodified in expectation (validity outcomes are number-independent).
- One spot-check per renumbered enum: the highest surviving value
  round-trips through the generated Go binding with its new number.
**Verification:** a grep for `reserved` *statements* (lines beginning with
`reserved`) under `spec/proto/flowseer/` returns nothing — prose and CEL
`message:` uses of the word legitimately remain; `buf lint`, `buf generate`,
and `go test -race` green; the diff is confined to `spec/proto/flowseer/**`,
`generated/**`, and conformance tests.
**Execution note:** this is the only unit that changes wire numbering — keep
it a single reviewable commit, separate from the comment-audit units.

### U5. README and workflow-doc refresh

**Goal:** The package-boundary READMEs and the style guide's Workflow section
match the audited schemas and the current toolchain.
**Requirements:** R2, R4.
**Dependencies:** U1, U2, U3 (link forms settled).
**Files:** `spec/proto/README.md`, `spec/proto/flowseer/README.md`, the five
`spec/proto/flowseer/net/*/v1/README.md`, `docs/code-style-proto.md`.
**Approach:**
1. Convert the flowseer README's "Standards grounding" list to the KTD5
   canonical link forms and extend it with the sources U2/U3 actually cited
   per package; give each package README a short sources list of only the
   standards that package draws on (fully qualified, no bare names).
2. Update the addr README: the contents list still describes the pre-rename
   file split ("EUI-48 and EUI-64 values, a tagged EUI/MAC address wrapper")
   — align it with the `eui.proto` shape U1 lands.
3. In `docs/code-style-proto.md`, amend the Evolution rule "Never reuse or
   renumber a field" with a dated pre-release carve-out naming this plan's
   one-time reserved-collapse (U4), and state the prohibition is absolute
   from the first stable release onward — so the next reader can tell the
   renumber was sanctioned rather than a violation.
4. Only if the primary checkout's buf.gen trim has landed by then: update
   the same doc's Workflow section to match the trimmed `buf.gen.yaml`.
   Otherwise skip and flag (per the Assumptions).
**Test scenarios:** Test expectation: none — documentation-only;
`src/common/protoconformance/layout_test.go` (README placement rules) and
the repo's Stop-hook layout package must stay green.
**Verification:** every standard named in a README carries its qualified
link; no README describes a file that no longer exists.

---

## Verification Contract

- `buf lint` clean across both modules (includes `PROTOVALIDATE` CEL
  compilation) — after every unit.
- Regeneration uses the Go-only template —
  `buf generate --template buf.gen.go.yaml --path spec/proto/flowseer` —
  and produces a `generated/` tree with no residual diff against the
  committed output — after **every** unit that touches a `.proto` file
  (bare `buf generate` at HEAD would emit an out-of-scope TypeScript tree
  under `frontend/web/`).
- `go test -race ./...` (protoconformance carries the schema invariants,
  `layout_test.go` carries the source-tree rules) — after U1, U4, and at the
  end.
- `spec/proto/ruckus/` (including `nanopb/`) shows no diff at any point, and
  nothing appears under `frontend/` (R7).
- `src/common/protoconformance/` is an AGENTS.md **policy surface**: U1's new
  `addr_rules_test.go` and U4's suite edits (including the retired
  reserved-tag guard) require an explicit guardrail review — budget it in
  the review of those units.
- Repo gate: run the `verify-change` skill before reporting completion.

## Definition of Done

R1–R8 hold; the comment-block-aware link check passes: no standards mention
in a `//` doc comment under `spec/proto/flowseer/` — any of the four KTD5
source families (RFC, IANA, IEEE, protobuf/protovalidate) — lacks a fully
qualified link in the same comment block (CEL `message:`/`expression:`
string literals exempt); no `reserved` statement remains under
`spec/proto/flowseer/`; all Verification Contract gates green; vendor
mirrors byte-identical.

---

## Open Questions

- **Deferred to implementation:** exact IEEE link targets per clause —
  `standards.ieee.org` product pages vs. a public normative mirror where one
  exists (KTD5 sets the families; the audit picks per-claim).
- **User-facing, non-blocking:** confirm the uncommitted `buf.gen.yaml`
  trim / `buf.gen.go.yaml` deletion is intended (see Assumptions); U5 step 4
  depends on it.

### From 2026-08-26 review

- Should the full audit produce a per-package audit matrix (value → source →
  status) as a recorded review artifact? Links-present plus lint-green cannot
  distinguish a semantic audit from link-only cleanup; the live-registry
  requirement in KTD6 partially covers this, and the matrix is a
  process-weight choice left to the user. (cross-model reviewer)

## Sources & Research

- `docs/architecture/2026-08-26-net-core-package-research.md` — primary-source
  URL inventory per package; boundary rationale.
- `docs/code-style-proto.md` — comment contract, editions rules, evolution
  rules, its own Sources list (link-form precedent).
- `docs/architecture/2026-08-20-network-model-structure-direction.md` —
  accepted direction these packages implement.
- Local findings this session: failing `buf lint` (4 errors) in the primary
  checkout's staged EUI-rename WIP (committed HEAD lints clean),
  import-kind inconsistency (3 options-only files using plain `import`),
  missing `addr_rules_test.go`, 12 reserved statements across 8 files.
