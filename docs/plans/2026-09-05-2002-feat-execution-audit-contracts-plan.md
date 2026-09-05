---
title: Execution and Audit Contracts, and the Error Wire - Plan
type: feat
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
---

# Execution and Audit Contracts, and the Error Wire - Plan

## Goal

Land the three schema packages [decision 12](../architecture/2026-09-05-verified-device-access-direction.md#decision)
reserves — `errs/v1`, `integration/device/v1`, `event/device/v1` — plus a
total Go encoder/decoder for the error payload in `src/common/errs`, so the
execution envelope between central and an integration and the durable audit
event both exist and both carry errors the same way every other boundary
does. Stop condition: if `MutationState` or `Provenance` needs a field this
plan cannot express without widening `device/access` beyond what every
boundary shares, that widening is a decision for the accepted record, not a
silent addition here — none was found during evidence-gathering, so this
plan proceeds without amending the direction record.

## Decisions

- **The execution envelope carries no device or edge ref.** Central dispatches
  one `ExecuteRequest` to the edge that hosts the device's lane over a
  channel already scoped to that device and that edge (decision 1's NATS
  fabric, subject-per-device-per-edge). Restating `DeviceGlobalRef` or
  `EdgeGlobalRef` on every envelope message would import `api/inventory` and
  `api/edge` directly into `integration/device`, which the amendment's import
  set — `device/access` and `errs`, nothing else — forbids. `MutationIntent`
  already carries the device ref for the mutation case; the typed-read case
  needs no device ref because the transport supplies it. Why: matches the
  amendment's literal import list, and duplicate identity carried once on the
  wire and once in the transport is exactly the kind of field this repo's
  conventions doc says a value should not carry twice.
- **`event/device` carries `DeviceGlobalRef` directly, importing `api/inventory`
  in addition to `device/access` and `errs`.** A durable audit record is read
  and queried outside any live transport context, unlike the ephemeral
  envelope, so it must be self-describing. Why: the amendment's own prose only
  forbids `event/device` reaching `api/edge` *directly* ("every boundary that
  imports device/access reaches api/edge through it and never directly"); it
  says nothing that forbids `api/inventory`, and `api/device` already imports
  `api/inventory` directly today for the same `DeviceGlobalRef` need. This
  interpretation is unconfirmed; flagged under Open questions.
- **A typed read is boundary-shared vocabulary and lives in `device/access`,
  not `integration/device`.** `device/access/v1/interface.proto` gains
  `InterfaceReadIntent` and a `TypedRead` wrapper (a required oneof, following
  the typed-variant convention, numbered from 1 since the oneof is the whole
  message) beside the existing `InterfaceDescriptionChange` and
  `InterfaceObservation`. Why: decision 12 calls `device/access` "the operation
  values every device-access boundary shares... typed intents and
  observations" (plural), and the operator API will need the same read intent
  once it stops treating `ReadInterface` as untyped; defining it once here
  matches the existing pattern for the mutation side.
- **`DeviceOperationEvent` is a typed-variant oneof of nine kinds, not ten
  separate top-level messages nor a flat struct.** The task's list overlaps in
  places — "block reason changes" and "blocked" name the same transition, as
  do "recovery started" and part of "phase transition" — so the kinds are
  consolidated to: `PhaseTransitioned`, `LaneBlocked`, `LaneReleased`,
  `RouteSelected`, `DiscoveryCompleted`, `FirmwareEpochChanged`,
  `RecoveryStarted`, `DriftDetected`, `LaneFrozen`. Why: a oneof arm per
  distinct fact keeps the envelope typed and the hook's triad check
  unconfused (no member ends in Config/State/Event except the event itself),
  while a flat struct would carry every kind's fields on every event row.
- **The errs wire codec ships as two encode functions and one decode
  function**, not a single function with a "trust" flag. `Encode` renders the
  full recursive tree for trusted internal transit — code, message, safe
  attributes, user message, hint, retry, causes, and a symbolized stack per
  origin. `EncodeForClient` renders one flat `ErrorPayload` from the chain's
  already-resolved views (`CodeOf`, `SafeAttributes`, `UserMessage`, `Hint`,
  the outermost retry disposition) with no message, no stack, and no causes.
  `Decode` is total and always produces an `*Error`, mapping an unrecognized
  wire code to a dedicated `ErrCodeUnknownRemote` sentinel rather than
  carrying an arbitrary string into `Error.code`, where it could coincide
  with an unrelated local code and make `errors.Is` match by accident. Why:
  two shapes read as two call sites with two names, rather than one call site
  whose behavior depends on a boolean the reader has to chase to a
  definition; `Decode`'s totality follows the accepted error-wire record.
- **Internal (non-`PubAttr`) attributes never reach the wire, in either
  encoding.** Why: the error-wire record says the payload carries "the
  client-safe attributes," full stop — it does not describe an internal
  attribute channel between trusted peers, and inventing one here would be
  unreviewed scope beyond what the record settled.
- **Attribute values cross as `google.protobuf.Value`.** `structpb.NewValue`
  covers every type the existing call sites attach (string, int, bool);
  anything it rejects falls back to `fmt.Sprint`, so encoding a `PubAttr`
  value never fails. Why: `Value` is the standard "arbitrary JSON-shaped
  scalar" wire type and needs no new schema of our own for something this
  generic.
- **A decoded peer's stack does not populate `Error.stack`.** `stack` is
  typed `[]uintptr`, unsymbolized program counters meaningful only to the
  process that captured them; a peer's already-symbolized frame strings
  cannot become one. `Decode` instead attaches them as an internal (non-safe)
  attribute, `"remote_stack"`, so the existing `LogValue` rendering shows them
  without a new field on `Error`. Why: keeps the wire codec additive to the
  package's public surface rather than reopening the payload shape the
  compound solution document says was settled by ranked review.

## Requirements

1. `spec/proto/flowseer/errs/v1/error.proto` defines `ErrorPayload` (code,
   message, safe attributes, user message, hint, retry disposition, causes,
   stack) and `RetryDisposition`. Acceptance: `buf lint` passes, and an
   `ErrorPayload` with an empty `causes` list and no `code` validates (a leaf
   error with nothing set beyond a message is legal).
2. `src/common/errs/wire.go` provides `Encode`, `EncodeForClient`, and
   `Decode`. Acceptance: encoding a three-level `Wrap` chain built with mixed
   `errs.Error` and a plain `fmt.Errorf` leaf, then decoding it, yields an
   error whose `errors.Is` against the original `Code`s still matches, whose
   `Attributes` contains only what was `PubAttr`-marked, and whose plain-leaf
   cause decodes to a coded-nothing `*Error` carrying only its rendered
   message.
3. Decoding an `ErrorPayload` whose `code` is a nonempty string not present in
   this process's code registry yields an `*Error` matching
   `errs.ErrCodeUnknownRemote` via `errors.Is`, never one matching the raw
   string by coincidence. Acceptance: a payload with `code: "some/other-pkgs-code"`
   that this test binary never calls `NewCode` for decodes to
   `errors.Is(got, ErrCodeUnknownRemote) == true`.
4. `EncodeForClient` never sets `message` or `stack`, and its `user_message`
   falls back to a fixed generic string when the chain names none.
   Acceptance: `EncodeForClient(errs.New().Msg("db conn refused at 10.0.0.5"))`
   has an empty `Message` field and a non-empty generic `UserMessage`.
5. `spec/proto/flowseer/device/access/v1/interface.proto` gains
   `InterfaceReadIntent` and `TypedRead`. Acceptance: `buf lint` passes and a
   `TypedRead` with no arm set fails `protovalidate.Validate`.
6. `spec/proto/flowseer/integration/device/v1/execution.proto` defines
   `ExecuteRequest` (sequence, deadline, idempotency key, a required oneof of
   `MutationIntent` or `TypedRead`), `ExecuteResult` (sequence, phase reached,
   a required oneof of `InterfaceObservation` or `ErrorPayload`),
   `CheckpointRequest`, `CheckpointAck`, and `TerminalResultAck`. Acceptance:
   `buf lint` passes and every message's own literal imports resolve to
   `device/access` and `errs` only (plus well-known types).
7. `spec/proto/flowseer/event/device/v1/operation_event.proto` defines
   `DeviceOperationEvent` (device, event id, sequence, occurred-at,
   correlation ids, bounded attributes, a required oneof of the nine kinds).
   Acceptance: `buf lint` passes, and constructing a `DeviceOperationEvent`
   with a `correlation_ids` map of 9 entries fails validation (bound is 8).
8. `test/conformance/proto/layering_test.go`'s `importOrder` gains `"errs":
   nil`, `"integration/device": {"device/access", "errs"}`, `"event/device":
   {"api/inventory", "device/access", "errs"}`, and `"api/device"` gains
   `"errs"`. Acceptance: `TestImportOrder` and
   `TestImportOrderCoversEveryPackage` pass against the new tree.
9. Every new package (`errs/v1`, `integration/device/v1`, `event/device/v1`)
   has a `README.md` at its directory root, and `device/access/v1/README.md`
   documents the new `TypedRead` addition.

## Out of scope

- Any Connect service, RPC, or handler code. This plan lands schema and the
  Go error codec only; nothing consumes `ExecuteRequest`/`ExecuteResult` or
  emits a `DeviceOperationEvent` yet.
- The internal-code-to-RPC-status mapping table and the Connect validating
  interceptor. Named as deferred follow-ups in the error-wire record and the
  compound solution document; not part of this task's requirement list.
- Broker envelope integration (NATS subject shape, delivery guarantees for
  `DeviceOperationEvent`). The message shape lands; how it is published does
  not.
- Any change to `api/device/v1/device_service.proto`'s RPCs. Only its
  layering permission (may import `errs`) changes, and no call site is added
  in this plan.
- Credential delivery, the SSH shell adapter, and anything from decisions 9,
  10, 11 of the verified-device-access record. Out of this task's requirement
  list.

## Units

### U1. The errs wire payload schema

Files: `spec/proto/flowseer/errs/v1/error.proto`,
`spec/proto/flowseer/errs/v1/README.md`
After: none
Change: Defines `RetryDisposition` and `ErrorPayload` per Requirement 1,
following `docs/code-style-proto.md` and `docs/conventions/protobuf.md` (this
is a value type, not an Entity: no ref pair, no triad — the file-level
comment says so). Regenerate with `buf generate`.
Tests: `test/conformance/proto/errs_rules_test.go` — leaf error validates,
`causes` over 16 items rejected, `stack` items over 512 chars rejected,
`safe_attributes` over 32 pairs rejected.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/errs/v1 generated/go/proto/flowseer/errs test/conformance/proto/errs_rules_test.go`

### U2. The errs wire codec

Files: `src/common/errs/wire.go`, `src/common/errs/wire_test.go`
After: U1
Change: Implements `Encode`, `EncodeForClient`, `Decode`, and
`ErrCodeUnknownRemote` per Requirements 2-4 and the Decisions above. Being in
`package errs`, it constructs `*Error` literals directly rather than through
`Builder`, since `Builder` has no setter for a decoded stack-as-attribute or
for reconstructing a whole tree in one call.
Tests: `src/common/errs/wire_test.go` — round-trip of a `Wrap` chain over a
`Code`d error and a plain `fmt.Errorf` leaf; `EncodeForClient` omits message
and stack; unknown-code decode matches `ErrCodeUnknownRemote`; a `nil` error
encodes and decodes to `nil`; `PubAttr`-only crossing (an `Attr`-only value
never appears in the decoded safe view).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/errs`

### U3. The typed read in device/access

Files: `spec/proto/flowseer/device/access/v1/interface.proto`,
`spec/proto/flowseer/device/access/v1/README.md`
After: none
Change: Adds `InterfaceReadIntent` and `TypedRead` per Requirement 5 and the
third Decision above.
Tests: `test/conformance/proto/device_access_rules_test.go` — new cases for
`TypedRead` (empty oneof rejected, `interface` arm valid) and
`InterfaceReadIntent` (empty name rejected).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/device/access/v1 generated/go/proto/flowseer/device/access test/conformance/proto/device_access_rules_test.go`

### U4. The execution envelope

Files: `spec/proto/flowseer/integration/device/v1/execution.proto`,
`spec/proto/flowseer/integration/device/v1/README.md`
After: U1, U3
Change: Defines `ExecuteRequest`, `ExecuteResult`, `CheckpointRequest`,
`CheckpointAck`, `TerminalResultAck` per Requirement 6 and the first Decision
above.
Tests: `test/conformance/proto/integration_device_rules_test.go` —
`ExecuteRequest` with neither oneof arm rejected; `ExecuteResult` with
neither outcome arm rejected; sequence `0` rejected on every sequence-bearing
message.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/integration/device/v1 generated/go/proto/flowseer/integration test/conformance/proto/integration_device_rules_test.go`

### U5. The durable operation event

Files: `spec/proto/flowseer/event/device/v1/operation_event.proto`,
`spec/proto/flowseer/event/device/v1/README.md`
After: U1, U3
Change: Defines `DeviceOperationEvent` and its nine kind messages per
Requirement 7 and the second and fourth Decisions above. The file-level
comment states the deliberate absence of `DeviceOperationConfig` and
`DeviceOperationState` for the message-sync hook.
Tests: `test/conformance/proto/event_device_rules_test.go` — no-kind-set
rejected; `correlation_ids` bound (8) and `attributes` bound (16) each
enforced; a `LaneFrozen` event validates with no `sequence` (lane-level, not
mutation-scoped).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/event/device/v1 generated/go/proto/flowseer/event test/conformance/proto/event_device_rules_test.go`

### U6. The layering table

Files: `test/conformance/proto/layering_test.go`
After: U1, U4, U5
Change: Adds `errs`, `integration/device`, `event/device` to `importOrder`
and adds `errs` to `api/device`'s entry, per Requirement 8.
Tests: `TestImportOrder`, `TestImportOrderCoversEveryPackage`,
`TestLayeringViolationRules` (extended with cases for the three new
packages: each may import `device/access` and `errs`; `integration/device`
may not import `event/device` and vice versa).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/proto/layering_test.go`

## Verification

```
buf lint
buf generate
go test -race ./test/conformance/proto/... ./src/common/errs/...
```

Plus the verifier command listed per unit, run again on the full changed set
before handoff.

## Definition of done

- [ ] `buf lint`, `buf generate`, and
      `go test -race ./test/conformance/proto/... ./src/common/errs/...` all
      pass.
- [ ] `.claude/skills/verify-change/scripts/verify-change.sh` passes for every
      changed path.
- [ ] Every new package under `spec/proto/flowseer/{errs,integration/device,event/device}/v1/`
      has a `README.md`; `device/access/v1/README.md` documents `TypedRead`.
- [ ] `docs/architecture/2026-08-20-network-model-structure-direction.md` and
      `docs/architecture/2026-09-05-verified-device-access-direction.md` need
      no edits — checked during evidence-gathering; the 2026-09-05 amendment
      already states this package tree.
- [ ] No plan labels (U1, R3) in code, comments, or commit messages.
- [ ] This plan's `status` set to `implemented` with an outcome note.

## Open questions

- Whether `event/device` importing `api/inventory` directly (for
  `DeviceGlobalRef`) is the amendment's intent, or whether the amendment means
  the three packages' imports are *exactly* `{device/access, errs}` and
  nothing else, in which case `DeviceOperationEvent` would need to identify
  its device some other way (an opaque string key, or by relying on the
  broker subject alone, matching the envelope's approach). Recommendation
  taken: import `api/inventory` directly, reasoned above; unconfirmed.
- Whether `ExecuteResult.phase_reached` is meaningful for a typed-read
  dispatch or should be optional/unset there. Recommendation taken: a read
  reports `OPERATION_PHASE_OBSERVING`, since the shared vocabulary already
  names that phase "the edge is reading the affected state over a fresh
  session"; unconfirmed.
