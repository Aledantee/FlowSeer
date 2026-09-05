---
title: Module Bus Fsync Policy - Plan
type: perf
date: 2026-09-04
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
execution: code
---

# Module Bus Fsync Policy - Plan

## Goal Capsule

- **Objective:** Default durable publication latency falls from milliseconds to tens of microseconds while operators can choose and observe the service's power-loss durability.
- **Means:** Declare a service-wide fsync policy, default it to periodic sync, preserve fsync-per-message as an explicit choice, and update the accepted direction record.
- **Authority:** This plan owns the bus fsync policy in `src/common/service`. It amends the local-bus paragraph of `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`.
- **Stop conditions:** Stop and ask if relaxing the fsync policy turns out to require a stream-configuration or manifest change. KTD1 asserts it does not; if that is wrong, the change stops being small and the migration questions this plan avoids all return.
- **Execution profile:** Small and sequenced. U1 first, with U3 and U4 shipping alongside it, then U2.

---

## Product Contract

### Summary

Replace the hardcoded server-wide `SyncAlways` with a declared fsync policy that defaults to periodic sync. Reference measurements put periodic-sync publishes at 14.6 µs with a one-second interval and 11.9 µs with the server's two-minute interval; U2 measures the five-second default. Acked messages still survive a process kill. Power-loss durability stops being implicit and becomes an explicit setting for the deployments that want it.

### Problem Frame

Every accepted message and every settlement transition on the bus costs an fsync, because `startLocalBus` sets `SyncAlways: true` for the whole server and both streams use file storage. Measured on APFS, a durable publish costs 4.51 ms against a 4.15 ms bare-fsync floor — the broker accounts for 8% of that and the fsync for the rest. An event fan-out writes one record per subscriber, so eight subscribers cost 39 ms.

The settlement journal in the metadata stream makes it worse than one fsync per message: `commitSettlement` writes on every retry, acknowledgement, and discard, so a delivered message pays again on the way out.

Nothing chose this cost. It follows from one hardcoded server option that no configuration can reach.

### Key Decisions

- **Ship the fsync policy alone, with no per-module storage tier.** (session-settled: user-directed — chosen over per-module memory and file tiers: once the fsync policy lands, the tier axis was measured to buy about 4 µs per message, and the file tier additionally survives a process kill that the memory tier does not.) Governs R1, R2.
- **Do not build the multi-process seams yet.** (session-settled: user-directed — chosen over shipping an external-server connection mode and shared admission state: the obligation is not to *block* separate-process modules later, and no consumer for that mode exists.) Governs R9.
- **Keep NATS/JetStream as the module transport.** (session-settled: user-directed — chosen over replacing the local bus with channels or core pub/sub: `nats-server` is already a fixed dependency of the embedded leaf node in the accepted direction record.)

### Requirements

**Durability policy**

- R1. The service declares one fsync policy for its file-backed streams.
- R2. The default policy is periodic sync with a bounded interval materially shorter than the server's two-minute default.
- R3. Fsync-per-message stays available and reproduces the current behavior exactly when selected.

**Compatibility**

- R4. Changing the policy alters no stream configuration and does not make an existing store migration-required.
- R5. The policy is a runtime setting, not a persisted storage identity.

**Visibility**

- R6. Startup records the effective policy and the loss window it accepts.

**Documentation**

- R7. The direction record and the package README state what the default policy survives and what it does not.

**Verification**

- R8. A test proves acked messages survive a process kill under the default policy.
- R9. The fsync policy appears only in `BusConfig` and the derived embedded-server options. It adds no module-facing API, protobuf or persisted record, or shared coordination state.

### Scope Boundaries

In scope: the policy setting, its default, its startup record, the durability tests, the direction-record amendment, and the `src/common/service` package README amendment.

**Deferred to Follow-Up Work**

- Per-module storage tiers. Measured at roughly 4 µs over the file path once this plan lands, against a second mailbox stream, per-tier settlement streams, tier-qualified consumers, a proto change, a migration path, and a cross-tier fan-out that gives up atomicity. Revisit only with a workload that sizes the benefit.
- Multi-process modules and the external-server connection mode, including shared admission state and concurrency-safe reconciliation.
- Failing an event whose declared subscribers are all currently inadmissible. It is worth deciding on its own merits as a single-process correctness question; it is not part of this change.

**Not touched**

The latent defects this planning surfaced, none of which this change causes or fixes: the metadata health canary having no capacity tolerance and so terminating the service on a full stream, capacity resizing being migration-required with no migration tool, `ownedStreamConfigEqual` detecting stream drift that nothing ever applies, `finishSettlement` returning nil on acknowledgement failure with no telemetry, and `isCapacityError` matching on a description substring that a server upgrade could change.

### Acceptance Examples

- AE1. **Covers R1, R2, R3.** Given no declared policy, the resolved server options carry periodic sync at the chosen interval; given a declared fsync-per-message policy, they carry `SyncAlways`.
- AE2. **Covers R4, R5.** Given a store written under the current fsync-per-message behavior, a binary running the default policy opens it without a migration stop and every stream configuration compares equal.
- AE3. **Covers R8.** Given a hundred acknowledged publishes under the default policy, killing the process with `SIGKILL` and reopening the store finds all hundred.
- AE4. **Covers R6.** Given each policy, the startup record names the policy and the loss window it accepts.
- AE5. **Covers R9.** Given the final change, the policy is confined to service configuration and embedded-server options; the module messaging API, schemas, manifests, and coordination state are unchanged.

### Success Criteria

- A publish under the default policy performs no fsync, and its measured cost lands within the same order as the 11.9 µs reference in Sources — against the 4.51 ms the same operation costs today.
- An operator reading the startup record can state what a power cut would lose without reading the code.

---

## Planning Contract

### Key Technical Decisions

- KTD1. **The policy is a server option, so nothing persisted changes.** `SyncAlways` and `SyncInterval` live on `server.Options`, not on `jetstream.StreamConfig`. `ownedStreamConfigEqual` compares only stream configuration and `manifestAdditionCompatible` compares the manifest, so neither sees this change. That is what keeps the work small: no proto change, no stream migration, no reconciliation phase. Implements R4, R5.
- KTD2. **Default to periodic sync at five seconds.** The server's own default is two minutes, which is a long loss window for an edge device losing power. A one-second interval measured 14.6 µs against 11.9 µs at the two-minute default, so the interval is nearly free across that range; five seconds keeps the window short without pinning the flush loop to the fastest setting. Implements R2.
- KTD3. **Do not record the policy in the manifest.** It is operational state, not a storage identity. Recording it would place a new field inside `manifestAdditionCompatible`'s whole-manifest `proto.Equal`, which would make every existing store migration-required — the exact trap that sank the larger version of this plan. The startup record in R6 is where the policy becomes visible. Implements R4, R5, R6.
- KTD4. **The metadata stream that stores the settlement journal follows the same policy as the mailbox.** Both are file-backed streams in one server and the option is server-wide, so a settlement write stops fsyncing along with its message. After a power cut a message can therefore outlive its acknowledgement and be handled again, which is inside the declared at-least-once contract and is the behavior R7 must state. Implements R7.

**Durability under each policy**, measured at the pinned versions:

| Policy | Per publish | Survives process kill | Survives power loss |
|---|---|---|---|
| Periodic sync (five-second default) | Measured by U2; references are 14.6 µs at one second and 11.9 µs at two minutes | Yes | Loses up to one interval |
| Fsync per message | ~4,136 µs | Yes | Yes |

### Assumptions

- Measurements were taken on APFS on an Apple M4 Pro. Edge hardware on eMMC or SD typically has a worse fsync profile, which widens the gap this change exploits rather than narrowing it.

### Implementation Constraints

- Error codes are append-only and declared at package level with a string literal. Reuse the existing `service/bus-*` codes where the failure kind is unchanged.
- New telemetry lives under `flowseer.*`, and changing the value set of an existing instrument is a telemetry-schema migration.
- Every goroutine has an owner, a shutdown signal, and a wait. The suite runs under `-race`.

### Risks & Dependencies

| Risk | Consequence | Mitigation |
|---|---|---|
| An existing deployment silently loses its power-loss guarantee on upgrade | Accepted work is lost in a power cut that previously survived | R6 makes the effective policy visible at startup, R7 states it in the direction record, and R3 keeps the old behavior selectable |
| A power cut loses different tails from the mailbox and metadata streams | A recent message may be lost; if its message record survives but its settlement does not, a handler may run twice | U4 states both cases and keeps redelivery inside the at-least-once contract |
| An unrelated change later adds a manifest field without noticing the compatibility trap | Every existing store becomes migration-required | KTD3 records why the policy is deliberately not persisted |

### System-Wide Impact

- **Operators** gain a durability setting and lose an implicit guarantee: the default no longer survives a power cut intact. The startup record is how they learn this.
- **Module authors** see no API change. Durability stays a property of the service, not of a call site.
- **Accepted direction** changes in one sentence: the `SyncAlways` pin becomes the declared policy and its default.

### Sources & Research

- Measured on this worktree at the pinned versions, APFS on Apple M4 Pro: bare `write`+`fsync` 4.15 ms; durable publish 4.51 ms; file storage with fsync off 11.9 µs; periodic sync at a one-second interval 14.6 µs; fsync per message 4,136 µs; event fan-out 8.4/11.9/21.9/39.1/70.3 ms at 1/2/4/8/16 subscribers.
- `SIGKILL` survival of 100 acked messages: file storage with fsync off, 100/100; fsync per message, 100/100. Async persist mode, by contrast, lost 100/100 and is not used here.
- `SyncAlways` and `SyncInterval` are fields of `server.Options`; `jetstream.StreamConfig` carries no fsync control. The only per-stream lever, `PersistMode`, is rejected in combination with `AllowAtomicPublish` (`err_code=10052`), which the mailbox stream requires.
- `src/common/service/bus.go` — `startLocalBus`, `mailboxStreamConfig`, `metadataStreamConfig`, `ownedStreamConfigEqual`.
- `src/common/service/delivery.go` — `commitSettlement`, the per-delivery journal write this change also relieves.
- `src/common/service/manifest.go` — `manifestAdditionCompatible`, whose whole-manifest `proto.Equal` is the reason KTD3 keeps the policy out of the manifest.
- `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`, "Local service runtime boundary", is the paragraph U4 amends.

---

## Implementation Units

### U1. Declare the fsync policy and wire it to the server

**Goal:** A service can declare its fsync policy, and the default stops fsyncing per message.

**Requirements:** R1, R2, R3, R4, R5, R9.

**Dependencies:** none.

**Files:** `src/common/service/config.go`, `src/common/service/bus.go`, `src/common/service/config_test.go`, `src/common/service/bus_test.go`.

**Approach:**

1. Add the policy to `BusConfig` as an explicit choice between periodic sync with an interval and fsync per message, with the zero value meaning the R2 default.
2. Normalize it in `normalizeBusConfig` alongside the existing budget settings, rejecting an interval below one millisecond because NATS applies sub-millisecond values unsafely and the startup attribute reports milliseconds.
3. Derive `SyncAlways` and `SyncInterval` in `startLocalBus` from the normalized policy instead of the current hardcoded `SyncAlways: true`.
4. Leave every stream configuration and the manifest untouched, per KTD1 and KTD3.

**Execution note:** This is a one-flag behavior flip that an existing test can absorb silently. Assert the resolved `server.Options` directly rather than inferring the policy from latency.

**Test scenarios:**

- An undeclared policy resolves to periodic sync at the default interval, and the resolved server options carry it.
- A declared fsync-per-message policy resolves to `SyncAlways` with no interval override.
- A declared interval is carried through to the resolved options.
- An interval below one millisecond is rejected during normalization, before any broker starts.
- Both mailbox and metadata stream configurations are byte-identical to the current ones under every policy.

**Verification:** The resolved server options match the declared policy, no stream configuration differs from today's, and review confirms that the policy changes no module-facing API, schema, persisted record, or shared coordination state.

### U2. Prove the durability boundary

**Goal:** What each policy survives is asserted, not assumed.

**Requirements:** R3, R4, R8.

**Dependencies:** U1.

**Files:** `src/common/service/bus_test.go`, `src/common/service/test/integration/durability_test.go`, `src/common/service/test/integration/helper_test.go`.

**Approach:**

1. Extend the existing subprocess broker helper so a helper process can be started under a chosen policy, publish acknowledged messages, and be killed abruptly.
2. Assert survival after `SIGKILL` under the default policy, using the existing helper modes rather than inventing a second mechanism.
3. Assert that a store written under fsync-per-message opens under the default policy with no migration stop and no stream drift.
4. Record the publish cost under each policy with the existing benchmark, so the Success Criteria figure is reproducible.

**Test scenarios:**

- A hundred acknowledged publishes under the default policy all survive `SIGKILL` and reopening.
- The same holds under fsync-per-message, establishing that both policies survive a process kill while their power-loss durability differs.
- A store created under fsync-per-message opens under the default policy without a migration-required error.
- Queued messages survive an orderly restart and a binary upgrade under the default policy, with retry counts intact.
- The publish benchmark under the default policy shows no per-message fsync cost.

**Verification:** Process-kill survival holds under both policies, and the existing store opens unchanged.

### U3. Record the effective policy at startup

**Goal:** An operator can see the durability the service is actually running with.

**Requirements:** R6.

**Dependencies:** U1.

**Files:** `src/common/service/bus.go`, `src/common/service/service.go`, `src/common/service/telemetry.go`, `src/common/service/instrumentation_test.go`.

**Approach:**

1. Attach the effective policy and its accepted loss window to the existing service startup span owned in `service.go`, using the established attribute-key constants.
2. Keep it to the startup path; per-message telemetry gains nothing here because the policy is service-wide and cannot vary per message.

**Patterns to follow:** the attribute-key constants at the top of `src/common/service/telemetry.go`. The package README notes `flowseer.module.path` is a legacy key and not a precedent for new keys.

**Test scenarios:**

- The startup record under the default policy names periodic sync and the interval.
- The startup record under fsync-per-message names that policy and a zero loss window.
- No new attribute carries a store path or any other unbounded value.

**Verification:** A recorded startup shows the policy and its loss window under both settings.

### U4. Amend the direction record and the package README

**Goal:** The accepted direction matches what the runtime does.

**Requirements:** R7.

**Dependencies:** U1.

**Files:** `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`, `src/common/service/README.md`.

**Approach:**

1. Replace the sentence pinning the embedded server with `SyncAlways` with the declared policy, its default, and what that default survives.
2. State separately that a power cut can lose recent records and that a handler may run again when its message survives but its settlement does not — inside the existing at-least-once contract, per KTD4.
3. Leave the listener-free, one-process sentence unchanged; this plan does not alter it.

**Test scenarios:** Test expectation: none — documentation only, covered by the repository's markdown link and style checks.

**Verification:** No sentence in the local-bus paragraph contradicts the shipped behavior.

---

## Verification Contract

- Focused checks while working: `go test -race ./src/common/service/...`.
- Before handoff, the diff-aware verifier for every changed path: `.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>`.
- Durability claims use the subprocess broker helper suite. These may skip under `testing.Short()`, but the deterministic tests stay in the default race suite.
- Performance: a publish under the default policy must perform no fsync. Record the measured cost against the 11.9 µs reference in Sources.
- No schema work: this change touches no `.proto` file and requires no `buf generate`. If it appears to, stop — that contradicts KTD1.

## Definition of Done

- Every requirement R1-R9 is implemented or explicitly deferred in Scope Boundaries.
- A service declares its fsync policy, the default performs no per-message fsync, and fsync-per-message reproduces today's behavior.
- An existing store opens under the default policy with no migration stop and no stream drift.
- Acked messages survive `SIGKILL` under the default policy, proven by the subprocess suite.
- The startup record names the policy and its loss window.
- The direction record and the package README match the shipped behavior.
- The suite passes under `-race`, and `verify-change` passes for every changed path.
- Abandoned experimental code from approaches that did not pan out is removed, not left in the diff.

---

## Deferred / Open Questions

### From 2026-09-04 review

- **Default policy weakens existing deployments without a choice**

  Existing deployments can lose acknowledged data during a power failure immediately after an upgrade even though they did not select a weaker policy. A startup record explains the effective setting after the new default has been applied, but it does not make that durability change explicit for the upgrade path.

- **Configured interval is not a guaranteed loss bound**

  Operators can read five seconds as the maximum possible loss window, but scheduler delay and slow or contended storage can postpone synchronization beyond the configured interval. The process-kill test does not exercise power loss or prove that background synchronization completes within that interval.

- **Policy API cannot distinguish default from invalid zero**

  An implementer must make an unplanned exported-API decision before coding. The current service configuration uses scalar durations whose zero values select defaults, so one scalar interval cannot make an undeclared zero valid while rejecting an explicitly supplied zero interval.

- **Durability trade lacks representative workload evidence**

  The plan can achieve a large microbenchmark improvement without improving service-level latency, throughput, resource use, or reliability if synchronous persistence does not constrain a representative workload. The APFS measurement also may not represent the deployment platform, so the value of weakening the default power-loss guarantee remains unproven.
