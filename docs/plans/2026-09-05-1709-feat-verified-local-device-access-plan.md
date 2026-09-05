---
title: Verified Local Device Access, Contracts - Plan
type: feat
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Verified Local Device Access, Contracts - Plan

> Implemented. The two empty decision messages on `ResolveDesynchronizationRequest`
> are named `AcceptObservedDecision` and `RestoreExpectedDecision`, because
> a `State` suffix reads as a triad member to the message-sync hook. Review
> found two requirements that contradicted the rest of the same change and
> amended them: requirement 6's unconditional `sequence` on `MutationState`
> could not represent the `INTENT_RECORDED` phase the README's own example
> uses, so `sequence` now carries a CEL rule tying its presence to the phase
> instead of a blanket `required`; requirement 3's unconditional `edge` and
> `firmware_fingerprint` on `Provenance` made the one shared provenance
> message unusable by a cloud-mediated integration, so both are optional on
> `Provenance` and required instead by a CEL rule on `InterfaceObservation`,
> where device access actually needs them.

## Goal

After this plan, the schemas that every later device-access plan builds on
exist and validate: an opaque access-policy handle, the shared operation
vocabulary (phase, disposition, one typed interface intent and its
observation), a richer provenance, the management mode and policy handle on
`DeviceConfig`, and the operator-facing `DeviceService`. The means is three
new schema packages plus two inventory changes, with the two accepted records
amended in the unit that lands the last schema so the tree and the direction
agree. No handler, transport, or adapter is written here; the plan sequence
under Out of scope does that.

Stop if a landed Go package routes on `ManagementEndpoint.protocol`, because
the protocol-neutral binding then needs a migration this plan does not
contain. None does today; the message is referenced only from its own file.

## Decisions

- Split the earlier Slice 1 plan into a sequence, contracts first. Why: each
  of its units was a plan of its own, and the edge attachment work set the
  precedent of contracts before host. The durable decisions moved to
  `docs/architecture/2026-09-05-verified-device-access-direction.md`, which
  this plan follows and which needs a person's acceptance.
- `device/policy/v1` is a dependency leaf holding `AccessPolicyHandle`, an
  opaque key plus version. Credential and host-trust handles wait for the
  plan that reads credentials, because no message here would carry them.
  Why: `api/inventory` references the policy from `DeviceConfig` and
  `device/access` pins it in an intent; neither may import the other's
  boundary (network-model record, import layering).
- The handle is not an entity and does not join `EntityType`. Why: admission
  needs the device service store, the same reason the Edge stays out
  (`spec/proto/flowseer/api/edge/v1/README.md`).
- Provenance stays one message. `flowseer.api.inventory.v1.Provenance` gains
  the protocol that answered, the Edge, and the firmware fingerprint. Why:
  `docs/conventions/protobuf.md` defines one provenance beside Binding that
  every envelope embeds; a second type in `device/access` would drift.
  `ManagementProtocol` is the route kind, so no new enum is needed.
- `device/access/v1` holds values every boundary shares and imports
  `api/inventory`, `api/edge`, and `net/*`. Why: the API, the execution
  envelope, and the audit event must agree on phase and provenance without
  importing one another (direction, decision 12), and a mutation state names
  the Edge responsible for it.
- Intent identity is a client idempotency key plus the per-device sequence
  central assigns; the intent carries the key, the state both. Why: the key
  deduplicates before a sequence exists, the sequence orders after.
- An interface is identified by the device ref plus the interface name as the
  device spells it. Why: the Interface entity and its ref are undecided by
  the network-model record's 2026-09-04 amendment, and `net/interface/v1`
  holds primitives that carry no refs. This plan does not decide that
  boundary.
- `ManagementEndpoint.protocol` stays, documented as the transport the
  address answers on. Why: removing it needs the binding store.
- No secrets and no error wire package here. Why: credential RPCs belong to
  the edge bus plan, and the error-wire record says the transport lands with
  its first consumer, the execution envelope plan.
- `actor` is a typed variant, `Actor`, with an `operator` arm carrying the
  identity provider's stable subject and a `system` arm naming the process
  reason. Why: user-directed on 2026-09-05; no identity package exists, so
  the arm lives here and moves when one lands, a breaking change this
  repository accepts.
- `ApplyInterfaceDescription` takes `validate_only` and returns no guarantee
  level. Why: rule 4 of the device-service record asked for both; semantic
  verification replaces the stated atomicity, and the record is amended to
  say so in the unit that lands the service.

## Requirements

1. `device/policy/v1/handle.proto` defines `AccessPolicyHandle` with `key`
   (required, 1 to 128 characters, `^[a-z0-9][a-z0-9._-]*$`) and `version`
   (required uint64, at least 1). Example: key `../root` fails the pattern
   rule; an absent `version` fails the presence rule; key `icx7150-lab`
   version 3 passes.
2. `DeviceConfig` gains `management_mode` (`DeviceManagementMode` with
   `UNSPECIFIED`, `OPERATOR_MANAGED`, `AUTHORITATIVE`; required, zero
   rejected) and `access_policy` (`AccessPolicyHandle`, required). Example: a
   `DeviceConfig` without `management_mode` fails; with `AUTHORITATIVE` and a
   handle it passes.
3. `flowseer.api.inventory.v1.Provenance` gains `protocol`
   (`ManagementProtocol`, required, zero rejected), `edge` (`EdgeGlobalRef`,
   required), and `firmware_fingerprint` (string, required, 1 to 128
   characters). Example: a `Provenance` without `edge` fails; one with
   `MANAGEMENT_PROTOCOL_SSH`, binding, edge, fingerprint, and `observed_at`
   passes.
4. `device/access/v1/operation.proto` defines `OperationPhase`
   (`INTENT_RECORDED`, `ADMITTED`, `POSSIBLY_APPLIED`, `OBSERVING`,
   `RECOVERING`, `VERIFIED`, `ACKNOWLEDGED`, `RELEASED`, `ABANDONED`),
   `Disposition` (`VERIFIED`, `INDETERMINATE_ABANDONED`, `REJECTED`),
   `BlockReason` (`UNACKNOWLEDGED`, `INDETERMINATE`, `RECOVERY_HOLD`,
   `DESYNCHRONIZED`, `FIRMWARE_EPOCH_CHANGED`, `CONFLICTING_READS`,
   `EDGE_STALE`), and `Completeness` (`COMPLETE`, `PARTIAL`). Every enum
   starts with `<ENUM_NAME>_UNSPECIFIED = 0` and every field of an enum type
   uses `defined_only` with `not_in: [0]`. The terminal phases are
   `ACKNOWLEDGED`, `RELEASED`, and `ABANDONED`; a recovery hold is phase
   `ABANDONED` with block reason `RECOVERY_HOLD`. Example: a message with
   `phase` unset fails.
5. `operation.proto` defines `MutationIntent` with `device`
   (`DeviceGlobalRef`, required), `idempotency_key` (required UUID string),
   `actor` (`Actor`, required), `access_policy`
   (`AccessPolicyHandle`, required), `expected_firmware_fingerprint`
   (required string, 1 to 128 characters), and a required `oneof change`
   numbered from 10 whose only arm is `InterfaceDescriptionChange`. `Actor`
   is a standalone required oneof numbered from 1: `operator` (`OperatorRef`
   with `subject`, required, 1 to 256 characters) or `system` (`SystemActor`
   with `reason`, an enum whose only value is `RECONCILIATION`). Example: an
   intent with no `change` arm fails; an `Actor` with neither arm fails.
6. `operation.proto` defines `MutationState` with `intent`, `sequence`
   (required uint64, at least 1), `phase`, `disposition`, `block_reason`,
   `blocked_since`, and `responsible_edge` (`EdgeGlobalRef`, required). CEL
   rules: `mutation_state.disposition_matches_phase` sets `disposition`
   exactly when `phase` is terminal; `mutation_state.blocked_since_matches_reason`
   sets `blocked_since` exactly when `block_reason` is set. Example:
   `phase: VERIFIED` with a disposition fails; `phase: ABANDONED` with
   `INDETERMINATE_ABANDONED`, `RECOVERY_HOLD`, and `blocked_since` passes.
7. `device/access/v1/interface.proto` defines `InterfaceDescriptionChange`
   (`interface_name`, required, 1 to 64 characters; `description`, required,
   0 to 64 printable ASCII characters where empty clears the description) and
   `InterfaceObservation` (`interface_name`, `description`, `admin_status`
   and `oper_status` from `net/interface/v1`, `provenance`, and
   `completeness`, all required). Example: a description containing `\x1b`
   fails; `uplink to core` passes; an empty description passes.
8. `api/device/v1/device_service.proto` defines `DeviceService` with
   `ReadInterface`, `ApplyInterfaceDescription`, `GetDeviceAccessStatus`,
   `AbandonMutation`, and `ResolveDesynchronization`, each with its own
   request and response. Requests name the device by `DeviceGlobalRef` and
   an interface by `interface_name`. `ApplyInterfaceDescriptionRequest`
   carries the intent and `validate_only`; with it set, the request is
   checked against policy, firmware epoch, and lane state without being
   recorded and the response carries no `MutationState`. Otherwise the
   response returns the `MutationState` at admission.
   `ResolveDesynchronizationRequest` carries `device`, `sequence`, and a
   required `oneof decision` numbered from 10 with `accept`, `restore`, and
   `replace` (a new `MutationIntent`). Example: an `AbandonMutationRequest`
   without `sequence` fails validation.
9. `GetDeviceAccessStatusResponse` carries `high_watermark` (uint64), an
   optional `unresolved` `MutationState`, `firmware_fingerprint`, and
   `repeated InterfaceObservation interfaces`, one row per interface keyed by
   `interface_name`. CEL rule `device_access_status.unresolved_is_open`: when
   `unresolved` is set its phase is not `RELEASED`. Example: `unresolved` in
   phase `RELEASED` fails; in phase `ACKNOWLEDGED` passes.
10. Import layering is acyclic and enforced: `device/policy` imports no
    FlowSeer package; `api/inventory` imports `device/policy` and `api/edge`;
    `device/access` imports `api/inventory`, `api/edge`, `device/policy`, and
    `net/*`; `api/device` imports `device/access`, `api/inventory`,
    `device/policy`, and `net/*`, and never `service`. Example: a test that
    lists `api/device` among `device/access`'s imports fails the coverage
    assertion.
11. The two accepted records say what the direction record says where they
    touch routing, credential delivery, rule 4 of "Rules the contract must
    keep", the sentence that the event envelope never imports `api/edge`,
    and the package tree. Example: the device-service record no longer lists
    credential handling as open.
12. `buf generate` output builds and every new package has a README that
    shows one valid message and names what is deliberately absent (secrets,
    raw protocol paths, tenant selectors, a triad).

## Out of scope

The remaining Slice 1 work, in order; each plan starts from these contracts.

1. SSH interactive session library under `src/protocol/ssh`, with the
   NETCONF client's pinned host-key rule as precedent. Independent of this
   plan.
2. FastIron interface capability in `src/modules/localnet`: SNMPv3 and SSH
   reads, the `port-name` plan against running configuration, verification
   by fresh read, transcripts from FastIron 10.0.10g.
3. Execution and audit contracts: `integration/device/v1`, `event/device/v1`,
   `errs/v1`, and the Go wire codec in `src/common/errs`.
4. Edge bus attachment and credential delivery: `AttachBus`, credential RPCs
   and handles, body-bound assertions, the mounted-file provider.
5. Local-network access module: lane, route evidence, firmware epoch,
   recovery, drift resolution, named OpenTelemetry events.
6. Central device service with the JetStream safety KV, audit stream, and
   outbox, then the central and edge hosts.
7. Fixture calibration and the end-to-end proof on the lab ICX7150, with a
   blast-radius statement and approval before the first live write.

Also outside: Telnet, multi-member failover and fencing, multi-field
intents, other vendors, an Interface entity, and `flowseer.service.v1`.

## Units

Each schema unit runs `buf generate`; `generated/go/proto/` changes only so.

### U1. Policy handle

Files: `spec/proto/flowseer/device/policy/v1/handle.proto`,
`spec/proto/flowseer/device/policy/v1/README.md`,
`test/conformance/proto/device_policy_rules_test.go`, `generated/go/proto/`
After: none
Change: Defines `AccessPolicyHandle` per requirement 1; the file comment
says a handle is an opaque key into a service-owned store, with no triad.
Tests: one valid and one invalid case per rule (empty key, uppercase key,
traversal, version 0, absent version).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/device/policy/v1 test/conformance/proto/device_policy_rules_test.go`

### U2. Management mode, policy, and provenance on inventory

Files: `spec/proto/flowseer/api/inventory/v1/device.proto`,
`spec/proto/flowseer/api/inventory/v1/binding.proto`,
`spec/proto/flowseer/api/inventory/v1/provenance.proto`,
`spec/proto/flowseer/api/inventory/v1/README.md`,
`test/conformance/proto/api_inventory_rules_test.go`, `generated/go/proto/`
After: U1
Change: `DeviceConfig` gains the enum and the handle per requirement 2;
`Provenance` gains the three fields per requirement 3; the
`ManagementEndpoint.protocol` comment says the value describes the transport
the address answers on and does not select a route. The README's Devices
section explains both modes with the drift example from the direction
record and its provenance section lists the new fields.
Tests: the `DeviceConfig` and `Provenance` cases from requirements 2 and 3.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/inventory/v1 test/conformance/proto/api_inventory_rules_test.go`

### U3. Shared operation values

Files: `spec/proto/flowseer/device/access/v1/operation.proto`,
`spec/proto/flowseer/device/access/v1/interface.proto`,
`spec/proto/flowseer/device/access/v1/README.md`,
`test/conformance/proto/device_access_rules_test.go`, `generated/go/proto/`
After: U2
Change: Defines the enums and messages of requirements 4 to 7. The README
walks one description change through every phase with the message that
exists at each step, and shows the abandoned-then-held state.
Tests: one valid and one invalid case per rule, a case per phase for the
disposition rule, the control-character and empty description cases.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/device/access/v1 test/conformance/proto/device_access_rules_test.go`

### U4. The operator-facing service

Files: `spec/proto/flowseer/api/device/v1/device_service.proto`,
`spec/proto/flowseer/api/device/v1/README.md`,
`test/conformance/proto/api_device_rules_test.go`, `generated/go/proto/`
After: U3
Change: Defines `DeviceService` per requirements 8 and 9. The README says
every response carries provenance, a read may fall through from SNMP to SSH
inside one call, apply returns at admission and the caller polls status,
`validate_only` records nothing, and no RPC takes a protocol or raw command.
Tests: request validation cases, the `validate_only` response shape, and
the `unresolved_is_open` rule.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/device/v1 test/conformance/proto/api_device_rules_test.go`

### U5. Boundary enforcement and record amendments

Files: `test/conformance/proto/layering_test.go`,
`docs/conventions/protobuf.md`,
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`
After: U4
Change: The layering test's import-order table grows a second root covering
`api/*` and `device/*` with a coverage test for packages missing from it;
requirement 10 is the table. The conventions doc names `device/policy` beside
the Edge's non-admission to `EntityType` and lists the new provenance fields.
The device-service record's execute bullet says the idempotency key
deduplicates and verification proves; rule 4 keeps `validate_only` and
replaces the atomicity level with verification, the read-back diff being the
observation in status; the credential open question is answered by decision
9 of the direction record; the Binding row says the endpoint's protocol is
the address's transport. The network-model record gains a dated amendment
with the boundary packages and import graph from decision 12, and its
sentence that the event envelope never imports `api/edge` now says it does
so through `device/access` only.
Tests: the requirement 10 negative case as a table assertion.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/proto docs/conventions/protobuf.md docs/architecture`

## Verification

```bash
buf format -d --exit-code && buf lint && buf generate
go build ./... && go vet ./...
go test -race ./test/conformance/proto/...
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto docs/architecture docs/conventions/protobuf.md
```

The message-sync hook must report nothing; the new file comments say why.

## Definition of done

- Verifier green for every changed path; `generated/` and `buf.lock` changed
  only by `buf generate`.
- Package READMEs, the inventory README, `docs/conventions/protobuf.md`, and
  both accepted records updated in the same change.
- This plan's `status` set to `implemented` with an outcome note under the
  title.
- No R or U labels in code, comments, or commit messages.

## Open questions

None.
