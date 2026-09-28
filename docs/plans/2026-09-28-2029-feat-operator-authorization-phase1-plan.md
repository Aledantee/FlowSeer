---
title: Operator Authorization Phase 1, Identity Leaf and Records - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: no lesson
execution: mixed
amends: docs/architecture/2026-09-09-remote-packet-capture-direction.md
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 1, Identity Leaf and Records - Plan

> Implemented. 2 units, 2026-09-28T18:58Z to 2026-09-28T19:00Z.

This plan began on 2026-09-18 as the capture operator authorization plan
and was re-planned twice on 2026-09-28. The first re-plan cut its units to
match its Decisions (schema alignment only, no enforcement). The second made
it phase 1 of the operator authorization parent, once
`docs/architecture/2026-09-28-operator-authorization-direction.md` decided
that operator identity comes from the authenticated token. That decision
removes the capture requester field this plan used to add to requests. The
requester is now written by the server from the authenticated context,
which phase 3 of the parent builds.

## Goal

`OperatorRef` lives in a leaf package, `flowseer.model.identity.v1`, that
`model/access` imports for its operator actor and that the later phases
build the tenant entity in. The records that name OpenFGA name the operator
authorization record instead. The capture direction record lists the
relations capture needs, as the authorization record's table gives them.
The means is a package move with its Go consumers, and prose amendments.
Stop condition: this plan is wrong if the operator authorization record,
accepted 2026-09-28, is amended to identify operators by something other
than the identity provider's subject.

## Decisions

The parent's Decisions and the direction record apply. These are this
phase's own:

- `OperatorRef` moves out of `flowseer.model.access.v1` into
  `flowseer.model.identity.v1` (`spec/proto/flowseer/model/identity/v1/operator.proto`),
  a leaf that imports nothing FlowSeer-owned. Its shape is unchanged. Why:
  `model/access` is the vocabulary device-access boundaries share (the
  network model structure record, and the `model/access` row comment in
  `test/conformance/proto/layering_test.go`). An operator's identity is the
  subject for every surface, and the tenant entity joins it in phase 2. The
  user decided this on 2026-09-28 ("nothing is live, you can break
  everything") over importing `model/access` from `model/capture`.
- `Actor.operator` in `model/access` keeps its number and name and takes the
  moved type. Why: the message moves unchanged, so the bytes on the wire stay
  the same; only the Go import path changes.
- `CaptureAuthorization.operator` stays a string in this phase. Why: the
  direction record makes the requester server-written from the token, and
  no token exists until phase 3. Adding a request field now would put back
  the self-asserted value the record removes, and phase 3 would only take
  it out again.
- Capture's relations in the capture direction record are the three rows
  the authorization record's table gives: `edge`/`capture`,
  `capture_session`/`download`, and `tenant`/`full_payload`. They are
  written as separate columns, never as `object#relation`. Why: one table
  is the source, and the capture record points to it rather than keeping a
  second model.
- The prose units cite the operator authorization record by path and name
  no engine in `GOALS.md`, the device README, or the device-service
  record. Why: the engine is the record's decision and may change; the
  schema never names one (parent Decisions).

## Requirements

1. `OperatorRef` lives in `flowseer.model.identity.v1` and nowhere else.
   Acceptance: `buf lint` passes; `grep -rn 'message OperatorRef' spec/proto`
   prints only `model/identity/v1/operator.proto`; and
   `Actor{operator: {subject: "idp|2837"}}` validates as it does today.
2. No engine is named in the schema. Acceptance:
   `grep -rniE 'openfga|spicedb|keto|zed' spec/proto` prints nothing.
3. The records point to the operator authorization record. Acceptance:
   `GOALS.md`, `src/services/device/README.md`, and the device-service
   record's transport diagram no longer contain `OpenFGA`. Each links
   `docs/architecture/2026-09-28-operator-authorization-direction.md`.
   The capture direction record's authorization section has the three
   relation rows in separate columns.

## Out of scope

- The capture requester field, caller authentication, the tenant entity,
  the engine, enforcement, and the operator event stream. Each is a later
  phase of the parent.

## Units

### U1. OperatorRef moves to a model/identity leaf

Files: `spec/proto/flowseer/model/identity/v1/operator.proto`,
`spec/proto/flowseer/model/identity/v1/README.md`,
`spec/proto/flowseer/model/access/v1/operation.proto`,
`spec/proto/flowseer/model/access/v1/README.md`,
`spec/proto/flowseer/model/README.md`,
`test/conformance/proto/layering_test.go`,
`test/conformance/proto/model_access_rules_test.go`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`,
`src/services/device/internal/deviceapi/deviceapi_test.go`,
`src/services/device/internal/dispatchapi/relay_test.go`,
`src/services/device/internal/drift/drift_test.go`,
`src/services/device/internal/host/validation_test.go`,
`src/services/device/internal/journal/journal_test.go`,
`src/services/device/test/integration/e2e_test.go`
After: none
Change:
- `operator.proto` declares `package flowseer.model.identity.v1` and
  `OperatorRef` exactly as `operation.proto` has it now, including its
  comments and validation.
- `operation.proto` drops the message, imports the new file, and declares
  `flowseer.model.identity.v1.OperatorRef operator = 1` in `Actor`.
- The new README follows `spec/proto/flowseer/model/policy/v1/README.md`:
  what the package is for (the identity of the people and, from phase 2,
  the tenants FlowSeer serves), then `## Boundaries` with `Imports:`
  (nothing FlowSeer-owned) and `Imported by: model/access`.
- `model/access`'s README gains `model/identity` on its `Imports:` line, and
  `model/README.md` lists the package.
- `layering_test.go` adds `"model/identity": nil` with a leaf comment and
  adds `model/identity` to the `model/access` row. It also gains a
  `want: true` case for `model/access` importing `model/identity`.
- The network model structure record adds the package to the tree listing
  and to the leaf sentence ("`model/edge`, `model/credential`, and
  `model/policy` are leaves too"). It adds `model/identity` to the
  `model/access` graph line, and a dated `### 2026-09-28` amendment says why
  identity left the access vocabulary.
- `buf generate` regenerates `generated/`, which is never hand-edited.
- Every Go test that builds `accessv1.OperatorRef_builder` builds
  `identityv1.OperatorRef_builder` instead. No production file references
  the type (`journal.go` only calls `GetOperator().GetSubject()`).

Tests: `model_access_rules_test.go` keeps its operator-actor cases, now built
from `identityv1`, and they must pass unchanged. That shows the move kept
`Actor` validation the same. `TestProtoReadmeImports` in
`test/conformance/proto/layout_test.go` checks the new and changed
`Boundaries` lines.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/identity/v1 spec/proto/flowseer/model/access/v1 spec/proto/flowseer/model/README.md test/conformance/proto docs/architecture/2026-08-20-network-model-structure-direction.md src/services/device/internal/deviceapi src/services/device/internal/dispatchapi src/services/device/internal/drift src/services/device/internal/host src/services/device/internal/journal src/services/device/test/integration`

### U2. Records point to the operator authorization record

Files: `docs/architecture/2026-09-09-remote-packet-capture-direction.md`,
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`,
`src/services/device/README.md`,
`GOALS.md`
After: none
Change:
- In the capture direction record's "Every capture is bounded and
  authorized" section, a paragraph after the "served only to a caller
  authorized for that session" bullet gives capture's three relations as a
  table with the object type, the relation, and what it grants in separate
  columns (the `edge`/`capture`, `capture_session`/`download`, and
  `tenant`/`full_payload` rows of the operator authorization record). It
  says the session's requester and every download are recorded as that
  record decides, and that `CaptureService` enforces nothing until it
  lands.
- In the device README's accepted-gap paragraph, "a named follow-up
  (OpenFGA)" becomes a link to the operator authorization record, which
  decides how the gap closes. The paragraph on the missing operator action
  trail points to the record's "Actions leave a trail" section.
- `GOALS.md` line 29 reads "The operator and admin API surfaces are
  authenticated with OIDC tokens and authorized through a Zanzibar-style
  relationship engine, for more than one tenant." Its link becomes the
  operator authorization record, and the "no record decides its shape yet"
  note goes.
- The device-service record's transport diagram says
  `Connect (typed device API; authorized)` where it says `OpenFGA-guarded`,
  and a dated `### 2026-09-28` amendment points to the operator
  authorization record.

Tests: none; this is prose. The verifier's doc checks cover it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-09-remote-packet-capture-direction.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md src/services/device/README.md GOALS.md`

Waves: U1 U2

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model test/conformance/proto docs/architecture src/services/device GOALS.md
go test -race ./src/services/device/... ./test/conformance/...
```

Run the targeted verifier over these paths, never `--full`. The integration
tests under `src/services/device/test/integration` use Docker; run them where
it is reachable.

## Definition of done

- [x] Verifier green for every changed path.
- [x] `OperatorRef` lives only in `model/identity`, and `generated/` is regenerated.
- [x] Package READMEs' `Boundaries` lines match the imports.
- [x] Requirements 2 and 3 hold by their greps.
- [x] This plan's `status` set with an outcome note under its title, and the parent's `Landed:` line filled.
- [x] No plan labels in code.

## Open questions

None.
