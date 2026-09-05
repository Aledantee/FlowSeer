---
title: Module Bus Fsync Upgrade Path - Plan
type: fix
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
---

# Module Bus Fsync Upgrade Path - Plan

> Implemented.

## Goal

A service whose local bus store was written under per-message fsync cannot move
to periodic sync without its author choosing to. The means: the fsync policy
loses its implicit default, so a `BusConfig` that does not declare a policy
fails normalization before the store is locked or opened, with an error that
names both choices and what each survives.

This amends the accepted direction. The local-bus paragraph of
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`
("The default is periodic sync on a five-second cadence" and "Upgrades change
the default from per-message fsync to periodic sync") describes the implicit
default this plan removes. U3 rewrites that paragraph; the code and the
direction record change together.

Stop condition: if a caller outside the test suite already runs a zero-valued
`BusConfig` against a populated store, this plan turns an upgrade into a startup
failure for that deployment. Today no such caller exists (`grep -rn "BusConfig{"
src --include='*.go' | grep -v _test.go` finds only the normalizer's own error
returns), so the break lands before anyone depends on the default. If a caller
appears before this ships, stop and decide whether it declares first.

## Decisions

- Require an explicit policy instead of persisting one. Why: the store cannot
  carry the choice. The provenance sidecar (`reconcileStoreProvenance`,
  `src/common/service/manifest.go`) and the runtime manifest
  (`manifestAdditionCompatible`) are both compared with `proto.Equal`, so a new
  field in either makes every existing store migration-required on the next
  start. `docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`
  records this trap. A store-aware rule ("populated store plus undeclared
  policy refuses to start") would also need persisted state to stop refusing
  once the author has chosen, which is the same trap by another route.
- Fail at normalization, not at store open. Why: `normalizeBusConfig`
  (`src/common/service/module.go:427`) runs before `startLocalBus`
  (`src/common/service/service.go:89`), so a refused start never takes the store
  lock, never rewrites provenance, and never advances the manifest journal.
  The store stays exactly as the previous binary left it and a rollback to that
  binary is a plain restart.
- Make the zero value of `BusFsyncPolicy` mean "unspecified". Why: the type is
  a `uint8` enum whose zero currently selects periodic. Renumbering so that
  `BusFsyncPeriodic` and `BusFsyncPerMessage` are both non-zero is the only way
  a Go struct field can distinguish "not chosen" from "chose periodic". This is
  a breaking change to a landed shape; the runtime is pre-stability and the
  break is the point. The earlier plan's open question "Policy API cannot
  distinguish default from invalid zero" closes with it.
- Keep `FsyncInterval` as a nil-able pointer with the five-second default. Why:
  once the author has declared periodic sync, the interval is a tuning knob and
  a default for it hides no durability change. Nothing in the interval
  contract moves.
- Reuse the `service/bus-config` error code. Why: error codes are append-only
  and the failure kind is unchanged, an invalid bus configuration. The message
  text carries the guidance.
- No operator-facing environment override for the policy. Why: every other
  bus setting (`StoreDir`, capacity ceilings, timeouts) is declared in code by
  the service author. The generated `_ENABLED` environment overrides exist for
  module gates because gating is a deployment decision. Adding a lone override
  for durability would create a second source of truth for one field. If a
  deployment needs to pick durability without a rebuild, that is a separate
  plan covering all bus settings.
- Leave telemetry unchanged. Why: the startup record and span attributes
  already report `periodic` or `per_message` and the interval. A refused start
  produces no bus, so there is nothing new to record; the returned error is the
  signal.

## Requirements

1. A `BusConfig` with `FsyncPolicy` left at its zero value fails
   `normalizeBusConfig` with code `service/bus-config`. Example: `BusConfig{StoreDir: dir}`
   returns an error whose message names `BusFsyncPeriodic`, `BusFsyncPerMessage`,
   and states that only per-message fsync survives power loss; no file under
   `dir` is created or modified.
2. `BusFsyncPeriodic` and `BusFsyncPerMessage` are non-zero, and each still
   resolves to the same embedded-server options as today. Example:
   `BusConfig{FsyncPolicy: BusFsyncPeriodic}` yields `SyncAlways: false`,
   `SyncInterval: 5s`; `BusConfig{FsyncPolicy: BusFsyncPerMessage}` yields
   `SyncAlways: true`.
3. A refused start leaves the store untouched. Example: start a helper process
   under per-message fsync, publish acknowledged messages, stop it, then call
   `Run` with an undeclared policy against the same `StoreDir`; `Run` returns
   the `service/bus-config` error, the `.lock` file is not held, the provenance
   sidecar bytes are unchanged, and a subsequent start under
   `BusFsyncPerMessage` reads every message.
4. A store written under per-message fsync opens under a declared periodic
   policy with no migration stop. Example: the same helper store started with
   `BusFsyncPeriodic` opens, every stream configuration compares equal, and the
   messages are delivered. This holds today; the requirement keeps it pinned
   after the renumbering.
5. Every test and example literal that starts a bus declares a policy. Example:
   `go vet ./src/common/service/...` and `go test -race ./src/common/service/...`
   pass with no `BusConfig` literal relying on the zero policy.
6. The package README, the direction record, `CONCEPTS.md`, and the durability
   solution describe the declared-policy contract and the upgrade sequence.
   Example: the README example shows both declarations and no zero-valued
   `BusConfig`; the direction record contains no sentence saying periodic sync
   is the default.

## Out of scope

- Any persisted marker of the policy, in the provenance sidecar, the manifest,
  or a stream configuration.
- An environment or file-based override of bus settings at deploy time.
- The other open questions of the earlier plan: representative-workload
  evidence for the periodic default and proving a loss bound for the interval.
- The latent defects listed under "Not touched" in
  `docs/plans/2026-09-04-1921-perf-module-bus-fsync-policy-plan.md`.

## Units

### U1. Make the policy declaration mandatory

Files: `src/common/service/config.go`, `src/common/service/bus.go`,
`src/common/service/config_test.go`, `src/common/service/bus_test.go`.

Change: `BusFsyncPolicy` gains `BusFsyncUnspecified` as its zero value;
`BusFsyncPeriodic` and `BusFsyncPerMessage` follow. The `BusConfig.FsyncPolicy`
comment says the field must be set and names what each value survives.
`normalizeBusConfig` rejects `BusFsyncUnspecified` with `service/bus-config` and
a message of the form "local bus fsync policy must be declared: BusFsyncPeriodic
survives a process kill, BusFsyncPerMessage also survives power loss". The
range check that rejects values above `BusFsyncPerMessage` stays.
`localBusServerOptions` is unchanged apart from comparing against the renumbered
constants. The subprocess helper in `bus_test.go` maps an empty
`FLOWSEER_BROKER_HELPER_FSYNC_POLICY` to a fatal error instead of periodic, so
no helper mode depends on a default either.

Tests: `TestNormalizeBusConfigCarriesDeclaredFsyncPolicy` gains the undeclared
case asserting the code and that the store directory has no new entries.
`TestLocalBusServerOptionsCarryFsyncPolicy` asserts both declared policies map
to the same options as before. A new `TestRunRefusesUndeclaredFsyncPolicyBeforeStoreOpen`
in `service_test.go` or `bus_test.go` covers requirement 3 with a populated
temporary store: error code, `.lock` acquirable afterwards, provenance bytes
equal before and after.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/service/config.go src/common/service/bus.go src/common/service/config_test.go src/common/service/bus_test.go`

### U2. Declare the policy in every existing test and example

Files: every `*_test.go` under `src/common/service/` and
`src/common/service/test/integration/` that constructs a `BusConfig`
(27 literal sites across `manifest_test.go`, `bus_test.go`,
`instrumentation_test.go`, `service_test.go`, `telemetry_sdk_test.go`,
`example_test.go`, `benchmark_test.go`, `supervisor_test.go`,
`delivery_test.go`, `test/integration/durability_test.go`,
`test/integration/otel_test.go`), plus
`src/common/service/test/integration/helper_test.go`.

Change: each literal declares `FsyncPolicy: BusFsyncPeriodic` unless the test is
about per-message fsync. Where a file has several sites, a small unexported
helper returning a periodic `BusConfig` for a `StoreDir` keeps the diff
readable; do not add one helper per file if an existing one in `bus_test.go`
(`testNormalizedBusConfig`) can be reused. `brokerHelperConfig.fsyncPolicy` in
`helper_test.go` becomes required: every call site passes `"periodic"` or
`"per_message"`. The `example_test.go` `Bus: &service.BusConfig{}` becomes a
declared periodic bus so the rendered example matches the README.

Tests: the existing suite is the test. `TestDurableHandlerResumesAfterAbruptProcessExit`
and the durability integration tests must pass unchanged in outcome.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/service`

### U3. Rewrite the durability prose for the declared contract

Files: `src/common/service/README.md`,
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`,
`CONCEPTS.md`,
`docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`.

Change: the README "Local message bus" section replaces `defaultPeriodicBus`
returning `&service.BusConfig{}` with a declared periodic example, states that
the policy must be declared, and adds an "Upgrading from per-message fsync"
paragraph: the upgraded binary refuses to start with `service/bus-config`
until the author declares a policy; the store is untouched by the refusal; the
previous binary can be restarted meanwhile; `BusFsyncPerMessage` reproduces
the old durability exactly and a store written under it opens under
`BusFsyncPeriodic` without migration. The direction record's local-bus
paragraph replaces "The default is periodic sync on a five-second cadence" with
the declared-policy sentence and drops "Upgrades change the default from
per-message fsync to periodic sync" in favor of the refusal behavior. The
`CONCEPTS.md` Local Bus entry replaces "the default survives a process kill" with
"each service declares whether it flushes periodically or per message".
The solution document updates its `FsyncPolicy` zero-value sentence, the
`defaultPeriodicBus` example, and replaces its closing "The upgrade path
deserves a note" sentence with the shipped behavior.

Tests: none; documentation only.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/service/README.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md CONCEPTS.md docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`

## Verification

- `go test -race ./src/common/service/...` including the subprocess helper
  suite (do not run under `-short`, which skips it).
- `.claude/skills/verify-change/scripts/verify-change.sh -- <every changed path>`.
- Manual check of requirement 3 if the automated test cannot hold the store
  lock assertion on the CI platform: run the integration helper under
  `per_message`, then start the service binary with an undeclared policy and
  confirm the error code and an unchanged `.flowseer-store.pb` checksum.
- `grep -rn "BusConfig{}" src docs CONCEPTS.md` returns nothing.

## Definition of done

- Verifier green for every changed path.
- A zero-valued `FsyncPolicy` cannot start a bus; both declared policies map to
  the same server options as before the change.
- A refused start takes no lock and changes no byte in the store directory,
  proven by a test.
- README, direction record, `CONCEPTS.md`, and the durability solution agree
  with the code in the same change.
- Requirement and unit labels from this plan appear nowhere in code, comments,
  or commit messages.
- Outcome note added to this plan when the change lands.

## Open questions

None the implementer must resolve. Two decisions above were made without the
user and are recorded for review:

- No environment override for the policy (Decisions). Recommended answer: keep
  it out; revisit if a deployment needs runtime durability selection for all
  bus settings, not only this one.
- Reuse `service/bus-config` rather than a new code for the undeclared policy
  (Decisions). Recommended answer: reuse; the failure kind is an invalid
  configuration and callers already branch on that code.

Resolved during implementation without a user question:

- `test/integration/broker_test.go` launches the helper by hand for the
  lock-contention check and was not in U2's file list; it now passes
  `FLOWSEER_BROKER_HELPER_FSYNC_POLICY=periodic`, and `startBrokerHelper`
  always passes `"periodic"` since none of its callers is about fsync.
- Names that said "default" for the periodic policy were renamed so the test
  output does not contradict the contract: `TestPerMessageStoreOpensUnderPeriodicPolicyAfterUpgrade`,
  the `periodic` benchmark and started-bus subtests.
- The refusal test compares a snapshot of every file under the store directory
  rather than the provenance sidecar alone; it is the same proof with a wider
  net and no extra code.
