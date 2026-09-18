---
title: YANG gNMI Validation Authority - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: no lesson
execution: mixed
amends: docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md
---

# YANG gNMI Validation Authority - Plan

> Implemented 2026-09-18. All three units landed: the parent's KD2, KD9, R14,
> outcome note, and DoD status are amended; the four `gn-t4-*` rows are
> `covered` against Arista vEOS-lab 4.33.1.1F (172.16.0.31, `LABSW31`) and
> `gn-aruba-set-capability` is an allowlisted `accepted-risk` gap; the
> `gnmi_conformance_complete` gate is green for the first time. The recorded run
> lives in the parent's "gNMI lab pass (2026-09-18)" section. This closed the
> YANG Protocol Libraries plan's last open leg, and the parent is now
> `implemented`.

## Goal

Close the last open leg of the YANG Protocol Libraries plan: the five
`gn-t4-*`/`gn-aruba-set-capability` conformance rows that stay `pending`
because they name Aruba CX, a family that serves no gNMI in the lab. The
means: name Arista vEOS-lab 4.33 as the gNMI validation authority in the
amended plan (KD2, KD9), prove Get, Subscribe, and Set against it in a
recorded t4 run, and record the Aruba-CX gNMI-write incapacity as an
allowlisted accepted-risk gap rather than an open row. This plan is wrong if
the Arista node is unreachable when the run is attempted and no other real
gNMI-serving NOS is available, because then the read/Set legs have no live
authority and the close reverts to a scope deferral.

## Decisions

- Arista vEOS-lab 4.33 is the gNMI validation authority, amending KD2 and KD9.
  Why: KD2 named Aruba CX for gNMI, but AOS-CX gNMI is telemetry-oriented and
  its config plane is proprietary REST (parent plan lines 145, 158; R14's
  predicted escape), and the lab's Aruba surfaces serve no gNMI at all (parent
  line 26: the 10.07 simulator has no `gnmi` command, 830/9339 do not listen).
  Arista vEOS-lab is a real vendor NOS serving OpenConfig over gNMI, and the
  library's Get, Subscribe, leaf-list, and Set/Get round-trip behaviors already
  ran green against it on 2026-09-18 — the covered rows `gn-leaf-list-typed-value`
  and `gn-banner-newline-normalization` in `src/protocol/gnmi/conformance_corpus_test.go`
  cite it. KD9's "real devices for the three families" holds; the gNMI family's
  device is the one that serves the protocol, not the one KD2 first named.
- The Aruba write (Set) criterion is proven on Arista, not held open. Why: the
  user directed proving Set on Arista (which accepts OpenConfig Set — the
  `gn-banner-newline-normalization` row is an Arista login-banner Set/Get
  round-trip) rather than waiting on an Aruba gNMI endpoint that does not
  exist. R14's escape hatch converts the Aruba-specific criterion to a
  documented gap; the gap is recorded once, as a separate row, not as an open
  requirement.
- `gn-aruba-set-capability` becomes an `AcceptedRisk` row on the gNMI
  allowlist, not `Covered` and not deleted. Why: the row records a real,
  device-specific fact — no lab Aruba CX serves gNMI, so its write capability
  is unverifiable here — and the conformance package's `AcceptedRisk` status
  exists for exactly this (a rationale plus an explicit, reviewable allowlist
  entry: `src/protocol/internal/conformance/conformance.go:33-34,85-86`).
  Deleting the row would erase the gap; marking it covered would claim a proof
  that does not exist.
- The four `gn-t4-*` rows become `Covered` against Arista with recorded
  adversarial input, keeping their existing citing markers in
  `t4_lab_test.go`. Why: `Covered` requires a citing test marker and a recorded
  adversarial input (`conformance.go:31-32,49`); the markers already exist and
  the test paths are generic OpenConfig (`system/state/hostname`,
  `components/component/state/...`, `interfaces`), which is why they pass on
  Arista unchanged. Only the provenance, adversarial detail, and status change.

## Requirements

- R1. The amended parent plan states Arista vEOS-lab 4.33 as the gNMI
  validation authority in KD2 and KD9, and records the Aruba-CX gNMI-write
  incapacity as a documented gap under R14, without weakening the IOS-XE or ICX
  criteria.
  Acceptance: KD2 and KD9 in the parent name Arista as the gNMI device; a
  reader of R14 finds the recorded Aruba gap and the Arista Set proof; a
  `grep` for "Aruba CX lab device (pending)" in the parent and the corpus
  returns nothing.

- R2. The gNMI conformance corpus carries no `pending` row: the four `gn-t4-*`
  rows are `Covered` with Arista provenance and recorded adversarial input, and
  `gn-aruba-set-capability` is `AcceptedRisk` with a rationale and an allowlist
  entry.
  Acceptance: `go test -tags=gnmi_conformance_complete ./src/protocol/gnmi`
  passes (the complete gate rejects any `pending` row,
  `conformance.go:195-200`); `gnmiAllowlist` contains `gn-aruba-set-capability`.

- R3. The four `gn-t4-*` behaviors run green against the live Arista node
  through the public library only, and the observed detail (hostname, version,
  serial, model; the Set round-trip; the drift comparison) is recorded in the
  amended parent plan as a lab-pass section alongside the RESTCONF and NETCONF
  passes.
  Acceptance: `go test -tags yang_integration_t4 ./src/protocol/gnmi/test/integration/`
  with `YANG_GNMI_T4_TARGETS` set to the Arista node passes; the parent plan
  gains a "gNMI lab pass" section quoting the observed identity fields and
  drift count.

## Out of scope

- Any Aruba CX gNMI work: no lab device serves it, and the config plane is
  proprietary REST, already a Scope Boundary exclusion in the parent.
- The candidate/commit NETCONF path (parent's Junos follow-up) and the
  container-tier (t1) gNMI target, both already settled in the parent.
- Widening gNMI coverage beyond the parent's R3/R10/R11/R12 surface.

## Units

### U1. Amend the parent plan's decisions and outcome record
Files: `docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md`
After: none
Change: KD2 keeps IOS-XE, ICX, and Aruba CX as the library-coverage families
but records that the gNMI validation authority is Arista vEOS-lab 4.33, because
no named family serves gNMI in the lab; KD9 names Arista as the gNMI device
under its lab-hardware-authority rule; R14's escape hatch is marked exercised —
the Aruba gNMI-write criterion is a documented gap and Set is proven on Arista
instead. The outcome note under the title and the DoD status section change
from "AE3 and R14 open" to closed, pointing at the new lab-pass section. A
"gNMI lab pass (2026-09-18)" section is added with the observed detail left as
placeholders U3 fills. No requirement is weakened; the amendment is additive to
KD2/KD9 and converts one criterion under R14.
Tests: none (documentation); `verify-change` runs the doc-style and layout
gates on the changed plan.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md docs/plans/2026-09-18-1335-feat-yang-gnmi-validation-authority-plan.md`

### U2. Reframe the corpus rows and the t4 test labels
Files: `src/protocol/gnmi/conformance_corpus_test.go`, `src/protocol/gnmi/test/integration/t4_lab_test.go`
After: none
Change: the four pending rows (`gn-t4-identity`, `gn-t4-set-verdict`,
`gn-t4-stream`, `gn-t4-revision-drift`) get Arista provenance and a recorded
`Adversarial` input matching the generic OpenConfig paths the tests already
walk, staying `Pending` until U3's run flips them; `gn-aruba-set-capability`
becomes `Status: AcceptedRisk` with an `Accepted` rationale ("no lab Aruba CX
serves gNMI; AOS-CX gNMI is telemetry-oriented and its config plane is
proprietary REST, so write capability is unverifiable here") and is added to
`gnmiAllowlist`. In `t4_lab_test.go`, `TestT4ArubaSetCapability` is renamed to
`TestT4SetCapability`, and the "Aruba"/"AOS-CX" comments on `identityPaths`
and the test become "Arista vEOS / OpenConfig"; the citing markers
("Covers conformance matrix row: gn-t4-*") are unchanged so coverage still
resolves. The integrity gate (`TestConformanceCorpusIntegrity`, always on)
stays green: pending rows are allowed and the accepted-risk row is now
allowlisted.
Tests: `src/protocol/gnmi/conformance_corpus_test.go` —
`TestConformanceCorpusIntegrity` (the accepted-risk row must be allowlisted or
it fails, `conformance.go:85-86`) and `TestConformanceMatrixUpToDate`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/gnmi/conformance_corpus_test.go src/protocol/gnmi/test/integration/t4_lab_test.go`

### U3. Run the Arista t4 suite, flip the rows, regenerate the matrix
Files: `src/protocol/gnmi/conformance_corpus_test.go`, `src/protocol/gnmi/CONFORMANCE.md`, `docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md`
After: U1, U2
Change: with `YANG_GNMI_T4_TARGETS` set to the live Arista node, the t4 suite
runs green through the public library; the four `gn-t4-*` rows flip from
`Pending` to `Covered` with the run's observed detail; `CONFORMANCE.md` is
regenerated (`-update-conformance`); the `gnmi_conformance_complete` gate goes
green for the first time. The observed identity fields, the Set round-trip
result, and the drift count are written into U1's "gNMI lab pass" section,
replacing the placeholders.
Tests: `go test -tags yang_integration_t4 ./src/protocol/gnmi/test/integration/`
(live, `YANG_GNMI_T4_TARGETS` set); `go test -tags=gnmi_conformance_complete
./src/protocol/gnmi` (the complete gate);
`TestConformanceMatrixUpToDate` proves `CONFORMANCE.md` regenerated.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/gnmi/conformance_corpus_test.go src/protocol/gnmi/CONFORMANCE.md docs/plans/2026-08-20-1245-feat-yang-protocol-libraries-plan.md`

Waves: U1 U2 | U3

## Verification

- `go test ./src/protocol/gnmi/...` (unit + corpus integrity, always on).
- `go test -tags=gnmi_conformance_complete ./src/protocol/gnmi` — the
  completeness gate, green only when no row is `pending`.
- `go test -tags yang_integration_t4 ./src/protocol/gnmi/test/integration/`
  with `YANG_GNMI_T4_TARGETS` set to the Arista node — the live proof (U3);
  unset, the tier skips with exit 0.
- The diff-aware verifier on every changed path per unit.
- Lab check: one read-only Get/Subscribe pass plus one reversible login-banner
  Set/restore against the Arista node. The Set is a device write; it captures
  and restores the banner (the existing test's `snapshotRestore` cleanup), and
  Arista vEOS-lab is a virtual lab node, so no advance-notice power-on applies.

## Definition of done

- [ ] Verifier green for every changed path in U1–U3.
- [ ] Parent plan KD2, KD9, R14, outcome note, and DoD status amended; the
      "gNMI lab pass" section carries the observed detail; no
      "Aruba CX lab device (pending)" string remains in the parent or corpus.
- [ ] `gnmi_conformance_complete` gate green; `CONFORMANCE.md` regenerated;
      `gn-aruba-set-capability` is `AcceptedRisk` and on `gnmiAllowlist`.
- [ ] Parent plan `status` set to `implemented` with an outcome note, since
      this was its last open leg; this plan's `status` set with its outcome.
- [ ] No plan labels (R1, U2, KD-ids of this plan) in code or commit messages.

## Open questions

- The Arista node's `host:port@user:password` for `YANG_GNMI_T4_TARGETS` is not
  recorded in the parent plan; the operator supplies it at U3, as the RESTCONF
  and NETCONF passes were run. If the node is unreachable when U3 runs, U3 stops
  and surfaces it (the Goal's stop condition), leaving U1/U2 landed and the rows
  `pending` rather than guessing a pass.
