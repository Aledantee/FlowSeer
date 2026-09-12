---
title: Network Simulation Analysis Completeness, Phase 1 - Plan
type: feat
date: 2026-09-12
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 1: Analysis trust contract and semantic trace - Plan

## Goal

Establish the contracts every later netsim capability relies on: strict input
construction, typed completion status, localized uncertainty, structured
evidence, semantic traces, and complete behavioral diffs.

This phase changes public Go APIs before adding more protocol behavior. It claims
parent requirements R1-R7, R9, and R39 and supplies the common vocabulary used by
phases 2-7. Phase 2 owns parent R8 topology uncertainty.

## Decisions

- The parent plan's library-only boundary, breaking-change policy, four-axis
  result contract, evidence placement, and exactness rules govern this phase.
- Keep `trace` as an import-leaf for typed trace atoms. Put status, issue scope,
  evidence catalogs, and result metadata in a sibling `analysis` package that may
  import `trace`. Neither low-level package imports a capability or composer.
- Capability packages own rule IDs, issue IDs, payloads, validation, normalization,
  cloning, and diffs. `vswitch` and `fabric` own their result envelopes.
- Semantic facts use an open `trace.Fact` contract implemented by immutable
  capability-owned value types. Each fact exposes a stable type ID and canonical
  representation for equality and ordering. Reflection, mutable payloads, and
  rendered prose are not semantic keys.
- Derive status from the canonical scoped issue set using the parent's fixed
  precedence. Preserve every issue and keep execution stop reasons and domain
  outcomes separate.
- Use typed issue codes and field or subject references. Rendered messages are
  for people and are not comparison keys.
- Treat explicit standards-based defaults as assumptions with evidence. They are
  executable but distinguishable from observed or configured facts.
- Make every exported constructor that accepts config validate and return an
  error. Any trusted helper used after aggregate validation is unexported.
- Make zero values safe. An unspecified enum is either an explicit `UNSPECIFIED`
  state with defined behavior or rejected by validation; it never inherits
  permissive behavior accidentally.

## Requirements

1. **R1:** Define shared scoped `Status`, `Issue`, `Evidence`, and result metadata
   without forcing domain payloads or stop reasons into one result type.
   **Acceptance example:** switch forwarding and model loading share status
   precedence while retaining every cause and their own payloads.
2. **R2:** Define issue scope precisely enough to preserve unaffected results.
   **Acceptance example:** an issue can identify one node, port, link, protocol
   instance, journey, or field path.
3. **R3:** Replace permissive unknown port behavior with explicit operational
   state and incomplete propagation. **Acceptance example:** `port.Forwards`
   cannot return true for `Unreported` or an unknown zero value.
4. **R4:** Make every exported config-accepting constructor enforce enum,
   cross-reference, and semantic validation. **Acceptance example:** capability
   constructors and `vswitch.New` return errors for invalid bridge admission or
   VLAN references; no exported trusted bypass remains.
5. **R5:** Add one normalization path per configuration type and make later
   state, diff, and fingerprint work consume it. **Acceptance example:** default
   application cannot differ between constructor and derive paths.
6. **R6:** Replace `trace.Step.Detail` and `Change` values based on `any` with
   typed semantic facts, producer-owned rule IDs, opaque evidence references, and
   a separate renderer. **Acceptance example:** equivalent runs compare semantic
   traces without string parsing or centralized capability enums.
7. **R7:** Extend model loading to accept caller source context and return a
   package-owned construction specification plus readiness, affected scope,
   evidence, conflicts, skips, and assumptions. **Acceptance example:** a loader
   returns normalized config and static seeds with `Incomplete` status but no
   error, and core types do not import transport provenance.
9. **R9:** Complete validation, normalization, cloning, and diffs for fields that
   already affect behavior. **Acceptance example:** every current `phy.Config`
   field has a validation and field-matrix diff case, and explicit and assigned
   defaults normalize identically.
39. **R39:** Establish the versioned analysis corpus with one planning, topology
    shadowing, and troubleshooting case plus the admission fields later protocol
    phases must fill. **Acceptance example:** each case names the question,
    current result or false answer, expected status, outcome, and trace invariants.

## Out of scope

- Correcting media, negotiation, PoE, protocol, routing, journey, convergence,
  or comparison semantics beyond the minimum API adaptation needed to compile.
- Resolving known-absence versus unresolved topology behavior, which phase 2 owns.
- Introducing scenario or search APIs.
- Adding service, storage, protobuf, telemetry, or UI contracts.
- Preserving the old constructor, trace, report, or link-state APIs.

## Units

### U2: Replace prose-only trace with semantic trace records

- **Files:**
  `src/common/netsim/trace/trace.go`,
  `src/common/netsim/trace/render.go`,
  `src/common/netsim/trace/trace_test.go`,
  `src/common/netsim/trace/README.md`
- **After:** none
- **Change:** Define stable typed layers, operations, producer-owned rule ID
  values, subjects, semantic input and output facts, changes, opaque evidence
  references, and canonical ordering. Replace `any` and caller-authored detail
  strings in the trace contract and provide a human renderer. Caller migration
  belongs to U4.
- **Tests:** Assert stable semantic equality, deterministic rendering, typed
  changes, opaque evidence links, unknown producer-owned rule IDs, and
  zero-value behavior.
- **Verify:**
  `go test -race ./src/common/netsim/trace`

### U1: Add shared status, issue, and evidence contracts

- **Files:**
  new files under `src/common/netsim/analysis/`
- **After:** U2
- **Change:** Define stable status and issue enums, typed affected subjects,
  evidence catalogs, assumption records, fixed summary precedence, and result
  metadata. Keep stop reasons and domain payloads in their owning packages.
  Document invalid-versus-partial and scoped-status behavior with a working
  example.
- **Tests:** Table-test multi-cause status combination, scope filtering, issue
  ordering, scope rendering, evidence deduplication, and zero-value behavior.
- **Verify:**
  `go test -race ./src/common/netsim/analysis`

### U3: Make construction strict and normalization singular

- **Files:**
  validators, normalizers, clone methods, and diffs under
  `src/common/netsim/vswitch/port/`,
  `src/common/netsim/vswitch/phy/`, `src/common/netsim/vswitch/mcast/`,
  `src/common/netsim/vswitch/routing/`, `src/common/netsim/vswitch/stp/`,
  `src/common/netsim/vswitch/lag/`, and `src/common/netsim/vswitch/bridge/`;
  corresponding package tests
- **After:** U1, U2
- **Change:** Add missing enum-domain, cross-reference, and semantic checks,
  define safe unspecified states, and give each capability one package-owned
  normalization path. Complete clone and diff coverage without changing
  top-level call sites yet. Phase 5 owns dependency fingerprints and retention.
- **Tests:** One failure case per enum and cross-reference, field-path assertions,
  explicit/default normalization equivalence, invalid zero values, duplicate
  routing interface names across VRFs, and a field matrix for every current
  behavior input.
- **Verify:**
  `go test -race ./src/common/netsim/vswitch/...`

### U4: Migrate public construction and trace producers

- **Files:**
  exported constructors and callers under `src/common/netsim/vswitch/` and
  `src/common/netsim/fabric/`, all trace producers under `src/common/netsim/`,
  corresponding tests
- **After:** U3
- **Change:** Make every exported config-accepting constructor validate and
  return an error, keep any trusted helper unexported, migrate all callers, and
  move trace producers to typed semantic facts. Add a `vswitch`-owned forwarding
  result envelope that combines bridge payload with analysis metadata, then
  migrate `Forward`, `Peek`, fabric journey entries, and callers. Define
  package-owned construction specifications that include normalized config and
  non-config inputs such as observed and static seeds. Remove superseded APIs.
- **Tests:** Public-constructor invalid cases, aggregate error field paths,
  construction-spec seed identity, unknown versus known-down forwarding metadata,
  and representative ingress, FDB, STP, routing, multicast, host, and fabric trace
  steps.
- **Verify:**
  `go test -race ./src/common/netsim/vswitch/... ./src/common/netsim/fabric`

### U5: Localize port uncertainty and model readiness

- **Files:**
  `src/common/netsim/vswitch/port/port.go`,
  `src/common/netsim/vswitch/port/port_test.go`,
  affected switch and fabric forwarding callers and tests,
  `src/common/netsim/vswitch/netmodel/report.go`,
  `src/common/netsim/vswitch/netmodel/report_test.go`,
  `src/common/netsim/vswitch/netmodel/netmodel.go`,
  `src/common/netsim/vswitch/netmodel/netmodel_test.go`,
  `src/common/netsim/vswitch/netmodel/README.md`
- **After:** U4
- **Change:** Add explicit up, down, and unknown operational state. Make `fabric`
  consume the new state without adding phase 2 topology semantics. Make model
  loading accept caller source context and return construction input with
  readiness, evidence, affected scope, skips, assumptions, and conflicts.
- **Tests:** Unknown-port non-forwarding, known-down definite drop,
  unaffected-result preservation, complete and partial loads, stable report
  ordering, and error/result separation.
- **Verify:**
  `go test -race ./src/common/netsim/vswitch/... ./src/common/netsim/fabric`

### U6: Lock the contracts with conformance tests and documentation

- **Files:**
  initial corpus helpers and fixtures under
  `src/common/netsim/internal/netsimtest/`, conformance tests under affected
  `src/common/netsim/` packages,
  `docs/architecture/2026-09-10-virtual-device-direction.md`,
  package READMEs affected by U1-U5
- **After:** U2, U3, U4, U5
- **Change:** Establish the versioned analysis corpus and its admission fields.
  Add phase-local consequence tests, document normalization and construction
  inputs, and amend the architecture record with package ownership, four-axis
  results, evidence, uncertainty, and trace contracts. Record which later
  protocol, topology, and comparison gaps remain absent.
- **Tests:** Status combination, unknown-port and partial-model preservation,
  deterministic change and issue ordering, three baseline corpus cases, and
  public constructor and trace migration audit. Phases 2-7 add domain-specific
  consequence cases.
- **Verify:**
  `go test -race ./src/common/netsim/...`

## Verification

Run focused checks after each unit. Before handoff, run:

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- \
  src/common/netsim \
  docs/architecture/2026-09-10-virtual-device-direction.md
```

Inventory exported constructors across every netsim package and review their
call sites. Review all trace producers so the breaking migration is complete
rather than shimmed.

## Definition of done

- [ ] Parent requirements R1-R7, R9, and R39 pass their phase-local acceptance
      examples.
- [ ] Invalid input is an error; partial valid input has scoped status and a
      lossless issue set independent of stop reason and domain outcome.
- [ ] No unknown or zero-value operational state forwards by accident.
- [ ] All trace producers emit typed semantic facts with stable ordering and
      evidence references.
- [ ] Model reports identify readiness and the exact scope affected by missing,
      assumed, skipped, or conflicting facts.
- [ ] Existing behavior-bearing config fields are covered by validation,
      normalization, clone, and diff matrices.
- [ ] Old constructor and trace APIs are removed without compatibility shims.
- [ ] Package tests, netsim race tests, vet, and the diff-aware verifier pass.
