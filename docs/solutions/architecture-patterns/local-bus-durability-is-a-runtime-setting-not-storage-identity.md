---
title: Local Bus Durability Is a Runtime Setting, Not Storage Identity
date: 2026-09-05
category: architecture-patterns
module: src/common/service
problem_type: architecture_pattern
component: service_layer
severity: high
applies_when:
  - "adding a tunable to BusConfig or the embedded JetStream server options in src/common/service/bus.go"
  - "deciding whether a new bus setting belongs in the persisted runtime manifest or only in server.Options"
  - "changing manifestAdditionCompatible or any proto.Equal-based manifest comparison in src/common/service/manifest.go"
  - "reasoning about what the local bus guarantees after a process kill versus a power loss"
  - "profiling publish or settlement-journal latency and finding fsync in the hot path"
  - "a service fails to start with service/bus-config saying the local bus fsync policy must be declared"
related_components:
  - messaging
  - data_model
  - testing_framework
  - observability
tags: [jetstream, fsync, durability, runtime-manifest, at-least-once, local-bus, performance, sigkill-test]
---

# Local Bus Durability Is a Runtime Setting, Not Storage Identity

## Context

The local message bus in `src/common/service` embeds a NATS server with two file-backed JetStream streams, a work-queue mailbox and a metadata stream that holds the runtime manifest and the settlement journal. Until this change the server was started with `SyncAlways: true` hardcoded, so every accepted message and every settlement transition paid a full fsync before the broker acknowledged it. Nobody had chosen that cost; it followed from one server option that no configuration could reach.

The cost was measured on APFS on an Apple M4 Pro before the change: a durable publish took 4.51 ms against a 4.15 ms floor for a bare `write` plus `fsync`, so the broker itself accounted for about 8% and the fsync for the rest. With fsync off, file storage published in 11.9 µs. Periodic sync at a one-second interval measured 14.6 µs, and at the server's two-minute default 11.9 µs, so the interval is nearly free across that range. An event fanned out to eight subscribers writes one record per subscriber and cost 39 ms.

The settlement journal doubled the damage. `commitSettlement` publishes a record into the metadata stream on every retry, acknowledgement, and discard (`src/common/service/delivery.go:595`, called from the disposition paths at `delivery.go:251`, `302`, and `329`), so a delivered message paid a second fsync on the way out.

An earlier and larger plan proposed per-module storage tiers, with a memory tier and a file tier chosen by each module. It was dropped for two reasons. Once the fsync policy is fixed, the tier axis buys roughly 4 µs per message, and the memory tier gives up process-kill survival that the file tier keeps. More importantly, recording a tier per module means persisting policy into the runtime manifest, and that runs into the manifest compatibility check described below. The plan lives at `docs/plans/2026-09-04-1921-perf-module-bus-fsync-policy-plan.md`.

## Guidance

Durability policy is a server option and nothing else. `SyncAlways` and `SyncInterval` are fields on `server.Options`; `jetstream.StreamConfig` carries no fsync control, and the only per-stream lever, `PersistMode`, is refused by nats-server v2.14.6 when `AllowAtomicPublish` is set, which the mailbox stream requires (`src/common/service/bus.go:390`). The policy therefore lives on `BusConfig` and is normalized into the embedded server's options. It never appears in a stream configuration, in the persisted manifest, in a protobuf, or in any module-facing API.

Never add a field to the runtime manifest for operational state. `manifestAdditionCompatible` (`src/common/service/manifest.go:414`) clears the module list and module paths from both manifests and then requires `proto.Equal` on everything that remains (`manifest.go:428`). Any new manifest field with a value that differs between the stored manifest and the running binary makes every existing store migration-required on the next start. A durability setting an operator is supposed to be able to change at will would trip that check on every change. The same reasoning applies to `ownedStreamConfigEqual` (`bus.go:362`), which compares only the stream fields that define storage identity; the fsync policy does not appear there because it is not part of that identity, and `TestOwnedStreamConfigsHaveFrozenFsyncIndependentContract` (`src/common/service/bus_test.go:210`) pins both stream configurations to a frozen baseline so a future change cannot slip a policy field in.

The setting itself is two fields on `BusConfig` (`src/common/service/config.go:87-94`). `FsyncPolicy` is a `BusFsyncPolicy` whose zero value, `BusFsyncUnspecified`, fails normalization; a service declares `BusFsyncPeriodic` or `BusFsyncPerMessage` (`config.go:45-60`). There is no implicit policy because a Go struct field cannot otherwise tell "not chosen" from "chose periodic", and the store cannot carry the choice (see the manifest trap above). `FsyncInterval` is a `*time.Duration` so that "not set" is distinguishable from an explicit zero; nil selects the five-second default (`bus.go:30`). Normalization in `normalizeBusConfig` (`bus.go:132-153`) rejects an undeclared policy, rejects an unknown policy value, rejects a periodic interval below one millisecond, the resolution the startup attribute reports, and rejects any interval supplied with the per-message policy. All four failures carry the `service/bus-config` error code (`bus.go:40`). The normalized values flow straight into `localBusServerOptions` (`bus.go:319-338`):

```go
SyncAlways:   config.fsyncPolicy == BusFsyncPerMessage,
SyncInterval: config.fsyncInterval,
```

Selecting a policy. The first two functions are the package README's example (`src/common/service/README.md:47-59`); the third shows a custom periodic interval:

```go
func periodicBus() *service.BusConfig {
    return &service.BusConfig{
        FsyncPolicy: service.BusFsyncPeriodic,
    }
}

func perMessageBus() *service.BusConfig {
    return &service.BusConfig{
        FsyncPolicy: service.BusFsyncPerMessage,
    }
}

func fastPeriodicBus() *service.BusConfig {
    interval := 500 * time.Millisecond
    return &service.BusConfig{
        FsyncPolicy:   service.BusFsyncPeriodic,
        FsyncInterval: &interval,
    }
}
```

The startup record is where an operator learns the loss window. `Run` attaches the effective policy to the startup lifecycle span (`src/common/service/service.go:74-84`) and, once the bus is up, logs `local bus durability configured` with `flowseer.service.bus.fsync.policy` set to `periodic` or `per_message` and `flowseer.service.bus.fsync.periodic_interval_ms` set to the interval, or zero under per-message (`service.go:94`, `src/common/service/telemetry.go:41-42`, `197-211`). The setting is service-wide and cannot vary per message, so per-message telemetry carries nothing about it.

What each policy survives. Both policies preserve acknowledged records across a process kill, because completed writes sit in the operating system's page cache and the kernel flushes them regardless of what the dead process asked for. Only per-message fsync survives a power loss intact. Under periodic sync a power cut can lose message and settlement records written since the last completed sync, and the configured interval is not a hard bound because scheduling and storage latency can delay the sync past it. The mailbox and metadata streams share one server option, so the settlement journal follows the same policy as the messages it settles. A message can therefore outlive its own acknowledgement after a power cut; on restart the consumer sees an unsettled message and runs the handler again, which is inside the bus's at-least-once contract (`README.md:69-73`).

## Why This Matters

The cost is three orders of magnitude on the default path. Publishes drop from milliseconds to tens of microseconds, and the settlement journal stops adding a second fsync per delivery. Edge hardware on eMMC or SD storage typically has a worse fsync floor than the APFS measurement, so the gap the change closes is wider there.

The migration trap is the part worth remembering after the numbers fade. The manifest is compared as a whole message, so it can only hold values that define what the store is, not how the process currently runs. A policy that operators are expected to change is the canonical example of a value that does not belong there. The earlier per-module tier plan was abandoned exactly because it would have persisted such a value, and the shipped change was small precisely because it touched nothing persisted: no proto change, no stream migration, no reconciliation phase.

The original `SyncAlways` pin came from the Jepsen report on NATS 2.12.1 (December 2025), which showed JetStream losing acknowledged data under crash and power loss with its two-minute default sync interval, and recommended `sync_interval: always` plus replicated streams on the hub (`docs/architecture/2026-08-20-device-service-and-inventory-direction.md:382-389`). That advice is right for the central hub, where losing an acknowledged write is a correctness failure. The direction record already distinguished the edge buffer: telemetry lost on an edge power cut is a gap in history, not a correctness failure, and edge buffers may run lazier. The local bus is that kind of buffer, listener-free and scoped to one process, so the accepted direction allows periodic sync there and keeps per-message fsync for deployments that want the old guarantee (`direction record:404-425`). The declaration is mandatory so that an upgrade cannot change durability silently. A binary started against a configuration that names no policy fails normalization with `service/bus-config` before the store lock is taken, so the store stays as the previous binary wrote it and that binary can be restarted; the author then declares `BusFsyncPerMessage` to keep the old durability or `BusFsyncPeriodic` to give it up. `TestRunRefusesUndeclaredFsyncPolicyBeforeStoreOpen` (`src/common/service/bus_test.go:297`) pins the refusal: a populated per-message store, an undeclared `Run`, an unchanged directory snapshot while the test holds the store lock, and a reopen that finds every message.

## When to Apply

- Changing `BusConfig` or `normalizeBusConfig`: keep new settings out of the stream configuration and manifest unless they define storage identity, and keep `TestOwnedStreamConfigsHaveFrozenFsyncIndependentContract` passing without editing its baseline.
- Adding a field to `RuntimeManifest`: check `manifestAdditionCompatible` first and ask whether the field's value can differ between two starts of the same store; if it can, it is operational state and does not belong there.
- Choosing durability for a new service: the declaration is required, so pick one. `BusFsyncPeriodic` is right for edge buffers; `BusFsyncPerMessage` when an acknowledged record must survive a power cut.
- Reading a durability benchmark: the per-message number includes a full fsync per publish and per settlement, so compare against the bare `write`+`fsync` floor on the same filesystem before attributing cost to the broker.
- Investigating a handler that ran twice after a power cut: check the startup record for the policy, then expect a surviving message with a lost settlement rather than a delivery bug.

## Examples

The process-kill test in `src/common/service/test/integration/durability_test.go:69-102` runs once per policy. It builds the broker helper binary, starts it in `batch_publish` mode with the policy passed through `FLOWSEER_BROKER_HELPER_FSYNC_POLICY` (`helper_test.go:73`), waits for the helper to print `READY 100` after a hundred acknowledged publishes, then calls `killAndWait`, which sends `Process.Kill` and fails if the helper somehow exits cleanly (`helper_test.go:115-123`). A second helper in `batch_reopen` mode opens the same store under the same policy and must print `FOUND 100`. Both policies pass, which is the evidence that a process kill is not what separates them.

The companion test at `durability_test.go:104-133` writes a store under `per_message` with version `v1`, shuts it down cleanly, and reopens it under `periodic` as version `v2`. It must find all hundred messages with no migration stop, which is the runtime evidence that the policy is not storage identity.

The validation tests at `bus_test.go:57-83` list the rejected configurations: an explicit zero interval, a negative interval, an interval one nanosecond under a millisecond, an undeclared policy, an unknown policy value, and a custom interval combined with `BusFsyncPerMessage`. Each must fail normalization with code `service/bus-config`. The messages an operator sees are `normalize local bus fsync policy: fsync policy must be declared: BusFsyncPeriodic survives a process kill, BusFsyncPerMessage also survives power loss`, `fsync policy is invalid`, `fsync interval must be at least one millisecond`, and `fsync interval requires periodic sync`, produced by the exported `service.NormalizeFsync` (`src/common/service/bus.go`), which the device fabric's hub and leaf servers in `src/modules/edgebus` call as well so that every embedded JetStream server in the repository follows one durability rule. `TestLocalBusServerOptionsCarryFsyncPolicy` (`bus_test.go:142`) and `TestStartedLocalBusCarriesFsyncPolicy` (`bus_test.go:164`) assert the resolved and the running server options directly rather than inferring the policy from latency.

The change landed as three local commits (the repository has no remote, so the SHAs are only meaningful in this checkout): `1dc0ce95` added the policy and wired it to the server, `512cc3e6` added the durability boundary tests and benchmark, and `7bd1bef5` moved the helper's settings into `brokerHelperConfig`. `c301c417` later tightened the tests and documentation as part of a broader telemetry hardening pass.

## Related

- `docs/plans/2026-09-04-1921-perf-module-bus-fsync-policy-plan.md`: the measurements, the abandoned tier plan, and the reviewer objections about the upgrade path and the interval not being a hard loss bound.
- `src/common/service/README.md`, "Local message bus": the operator-facing contract and the selection example.
- `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`, "Local service runtime boundary" and the Jepsen note under the integration fabric: why the hub stays on always-fsync and the edge buffer does not.
- `docs/solutions/architecture-patterns/errs-package-architecture-and-error-conventions.md`: the append-only error-code convention the `service/bus-config` code follows.
