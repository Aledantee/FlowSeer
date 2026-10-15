---
title: Central Hub Periodic Fsync - Plan
type: perf
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md, docs/architecture/2026-10-03-central-high-availability-direction.md, docs/architecture/2026-10-03-deployment-direction.md
---

# Central Hub Periodic Fsync - Plan

## Goal

Central's ingest call settles edge records at tens of thousands per second
instead of about two hundred. The means are a hub that syncs its store every
100 ms instead of before every acknowledgement, and a delivery handler that
publishes a request's records asynchronously and reads their acks in record
order. Stop condition: the plan is wrong if the asynchronous handler can
answer a `settled` count that covers a record central does not hold, since
the edge deletes every settled record.

## Decisions

`E` is `src/modules/edgebus`, `I` is `src/services/device/internal/intake`,
`H` is `src/services/device/internal/host`, `NC` is
`~/go/pkg/mod/github.com/nats-io/nats.go@v1.54.0/jetstream`, and `NS` is
`~/go/pkg/mod/github.com/nats-io/nats-server/v2@v2.15.0/server`, the
versions `go.mod` pins.

- Central's edgebus hub moves from `service.BusFsyncPerMessage` to
  `service.BusFsyncPeriodic` with a 100 ms interval. Why: per-message sync
  measured about 215 records/s on an Apple M3 Pro (`F_FULLFSYNC` on darwin),
  periodic sync at 100 ms about 42,000 with synchronous publish and about
  107,000 with asynchronous publish. Below 50 ms the rate falls, because
  nats-server holds the block lock across `fd.Sync()` (`NS/filestore.go:8344-8345`,
  `syncFile` at `:8176-8191`) and re-arms the timer only after `syncBlocks`
  returns (`:8370`, `setSyncTimer` at `:12125-12134`). Today the policy is
  set in `H/host.go:315` and described in `E/hub.go:36-37` and the
  Durability section of `E/README.md`. `E/fsync.go:19-26` and
  `service.NormalizeFsync` (`src/common/service/bus.go:163-186`, 1 ms floor,
  5 s default at `:31`) need no change. (decided by the user, 2026-10-15)
- The ingest delivery handler (`I/handler.go`, `deliverOne` about
  `:144-160`) publishes a request's records asynchronously and then waits
  for every ack in record order. Settled stays the in-order prefix of
  records whose every publication (typed and evidence) is acked. The first
  failed ack or publish error stops the count. Context cancellation ends the
  wait. The client's max-pending stall must be respected. (decided by the
  user, 2026-10-15)
- `BenchmarkDeliver` (`I/handler_bench_test.go`) gains a pass floor of
  30,000 records/s. (decided by the user, 2026-10-15)
- Amend `docs/architecture/2026-08-20-device-service-and-inventory-direction.md:473-478`,
  the power-loss rule. Central's single-node hub accepts losing up to about
  one 100 ms sync interval of acknowledged records on power loss, records
  the edge has already deleted. Process crash loses nothing acknowledged.
  (decided by the user, 2026-10-15)
- Clustered central (the HA plans) uses R3 streams with periodic fsync (the
  same 100 ms interval) and relies on replication for durability. Amend
  `docs/architecture/2026-10-03-central-high-availability-direction.md` and
  give `docs/plans/2026-10-03-1533-feat-central-high-availability-phase2-plan.md`
  and `-phase3-plan.md` the fsync policy where they configure NATS. The
  residual risk: a power cut that hits every replica at once can lose
  acknowledged writes. The NATS concepts page says so under "Syncing data to
  disk" ("If multiple servers fail simultaneously, all due to an OS failure,
  and before their data has been fsync-ed, the cluster may fail to recover
  the most recently acknowledged messages",
  https://github.com/nats-io/nats.docs/blob/master/nats-concepts/jetstream/README.md),
  and the configuration reference says "By default JetStream relies on
  stream replication in the cluster to guarantee data is available after an
  OS crash" and that `always` slows throughput "to a few hundred msg/s"
  (https://github.com/nats-io/nats.docs/blob/master/running-a-nats-service/configuration/README.md,
  the `sync_interval` row). The rejected alternative is R3 with
  `sync_interval: always`. In v2.15.0 a replicated stream under
  `SyncAlways` syncs on flush and snapshot instead (`NS/stream.go:1116-1122`),
  and Raft batches proposals into one append entry of up to 4096 entries
  or 256 KiB (`NS/raft.go:3454-3455`, `:3510-3535`), so its throughput is a group
  commit that was not measured here. (decided by the user, 2026-10-15)
- The two HA phase plans carry a one-bullet fsync decision since this plan
  was written. No unit edits them again.
- `HandlerConfig.Central` takes an `AsyncPublisher` defined in
  `I/handler.go`, whose one method is `jetstream.JetStream`'s `PublishAsync`.
  `Publisher` in `I/intake.go` stays for the stream consumer. Why:
  `jetstream.JetStream` satisfies both, so `H/serve.go:325-331` compiles
  unchanged and `I/intake.go` stays out of this plan.
- The handler runs in two passes. The first prepares each record and
  publishes its typed message with `PublishAsync`, stopping at the first
  preparation retry (marshal, validator failure that is not a rule
  violation) or publish error. The second walks the records in order. It
  reads each typed future with a `select` on `Ok()`, `Err()`, `ctx.Done()`,
  and the wait bound. Once a raw record's typed ack arrives, it publishes the
  evidence with `PublishAsync` and reads that future before moving on. Why:
  the spike's `deliverAsync` (branch `worktree-agent-afdd6a6e4f97d567a`,
  commit `7e38bf9f8`) has this shape without the evidence rule. Evidence
  after the typed ack keeps the rule in `I/README.md:130-131`, that a full
  typed stream never stores evidence again on a retry, and the benchmark's
  records carry no raw evidence. The spike's `Async` switch and
  `deliverOne` go.
- Every counter, the refusal log, the duration, and the one retry per call
  come from the walk, for the records it reaches. The walk stops at the
  first record that does not settle and counts that record's retry once:
  `stream_full` for a full typed stream, `publish_failed` for any other ack
  error, a publish error, a stall, cancellation, or the wait bound, and the
  preparation reason for a preparation retry. Why: today each record is
  counted when the in-order loop reaches it (`I/handler.go:111-116`), and a
  refusal counted in the first pass would count again on the resend.
- The walk waits at most 5 s for any one future when `ctx` has no deadline.
  Why: the synchronous path bounded each publish by the client's 5 s
  default (`NC/publish.go:182`, `NC/jetstream.go:477`), and an async ack
  times out only when the client sets an ack timeout (`NC/publish.go:380`),
  which the hub's client does not (`E/hub.go:366`).
- An abandoned future needs no draining. Why: `Ok()` and `Err()` create
  one-slot buffered channels (`NC/publish.go:674-700`).
- A server error on an async ack is the same `*jetstream.APIError` a
  synchronous publish returns (`NC/publish.go:574-576`), so
  `edgebus.IsStreamFull` (`E/cleanup.go:127`) keeps telling a full typed
  stream from other failures, and the `stream_full` and `publish_failed`
  retry reasons keep their meaning.
- `jetstream.ErrTooManyStalledMsgs` is a publish error with reason
  `publish_failed`, and nothing raises the client's max-pending limit. Why:
  `PublishAsync` waits 200 ms by default once more than max-pending acks are
  outstanding, then returns that error (`NC/publish.go:305-310`,
  `:372-378`). The default is 4000 (`NC/jetstream.go:496`), and the hub
  builds its client with no publisher options (`E/hub.go:366`).
- Message ids are unchanged, so a record acked after an earlier record's
  failure is stored once when the edge resends it within the typed stream's
  duplicate window. A resend after more than 10 minutes stores it twice. Why: `WithMsgID` sets
  `Nats-Msg-Id` on the async path too (`NC/publish.go:312-313`), the server
  checks it per stream (`NS/stream.go:5553`), and the typed ingest stream's
  duplicate window is 10 minutes (`E/hub_test.go:44`).
- `flowseer.intake.record.duration` runs from a record's preparation to
  the walk reading its outcome. Why: the instrument's description
  (`I/intake.go:472`) already reads that way.
- The pass floor applies to every run of the benchmark whose timed part
  lasted at least 500 ms, and the README's command runs at the default
  one-second benchtime. Why: `testing.B` calls the function with `b.N` 1
  first, a 64-record run of a few milliseconds that measures setup, and a
  run under `-benchtime 1s` lasts at least a second, so the final run is
  always checked.
- The benchmark sizes its limits from the run: the typed stream holds the
  larger of `b.N * size * 1 KiB` and 112 MiB, the central budget is
  `HubConfig.CentralStreamBytes()` (`E/hub.go:176-182`), and the store
  ceiling adds 256 MiB. Why: at `-benchtime 200x` and batch 1024 the fixed
  112 MiB stream filled, and an explicit ceiling is taken as given
  (`NS/jetstream.go:2821-2822`).
- The spike's atomic batch variant (`82724e0d8`) is not kept. Why: one
  stored message id fails the whole batch, and it leaves evidence unbatched.

## Requirements

1. Central's hub runs periodic sync at 100 ms. Example: the hub
   configuration the device service host builds has `FsyncPolicy`
   `service.BusFsyncPeriodic` and `FsyncInterval` 100 ms, and a hub started
   with it reports `SyncAlways` false and `SyncInterval` 100 ms from
   `server.JetStreamConfig()`.
2. A later ack does not extend the count past an earlier failure, and one
   call counts one retry. Example:
   records A, B, C where B's typed ack fails with an injected error and C's
   succeeds answer `settled` 1, the typed stream holds A and C, and one
   `publish_failed` retry is counted. Delivering B and C again answers 2,
   counts one duplicate, and the typed stream holds three messages.
3. A publish error stops publishing. Example: a publisher whose second
   `PublishAsync` call returns `jetstream.ErrTooManyStalledMsgs` sees
   exactly two calls for three records, and the call answers `settled` 1
   with one `publish_failed` retry.
4. Context cancellation ends the wait. Example: B's future never resolves
   and a 100 ms deadline makes `Deliver` return within a second, `settled` 1.
5. A full typed stream ends the count with `stream_full`. Example: a typed
   stream that holds one test record answers two records with `settled` 1.
6. An evidence ack counts toward its record, and evidence follows the typed
   ack. Example: a raw record whose evidence ack fails answers `settled` 0,
   though its typed message is stored. A raw record whose typed ack fails
   publishes no evidence.
7. Refused records stay in the prefix, as
   `TestInvalidRecordIsSettledAndRefused` holds today.
8. `BenchmarkDeliver` fails below 30,000 records/s and never fills its
   stream. Example: a run of at least 500 ms below the floor ends with
   `b.Fatalf` naming the measured rate and the floor, and
   `-benchtime 200x` at batch 1024 settles every batch.
9. The records state one durability story. Example:
   `grep -rn "sync_interval: always" docs/architecture` returns only the
   Jepsen history in the device service record and the rejected alternative
   in the central high availability record.
10. The wait is bounded without a deadline. Example: with the bound set to
   100 ms in the test and B's future never resolving, `Deliver` on a
   context without deadline returns `settled` 1 and one `publish_failed`.

## Out of scope

- A configurable hub sync interval, and the edge's and local bus's policies.
- Running the benchmark in a gate. No verifier runs benchmarks.

## Units

### U1. The hub syncs every 100 ms
Files: src/services/device/internal/host/host.go, src/services/device/internal/host/hub_internal_test.go, src/modules/edgebus/hub.go, src/modules/edgebus/fsync_internal_test.go, src/modules/edgebus/README.md, docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md
After: none
Change: `setupHub` builds its `edgebus.HubConfig` in a new method
`(h *assembly) hubConfig() edgebus.HubConfig` that declares
`service.BusFsyncPeriodic` and a package constant `hubFsyncInterval` of
100 ms. The `FsyncPolicy` comment in `E/hub.go` says central declares
periodic sync at 100 ms. `E/README.md` Durability says the hub syncs every
100 ms, that a process crash loses nothing acknowledged, and that a power
cut can lose about one interval of acknowledged writes, ingest records the
edge has already deleted and whatever else the blocker under Open
questions settles. The device service record carries decision 4 in its
hub hardening bullet (`:436-443`), and `:341-342` and `:551` say periodic
sync at 100 ms with R3 relying on replication, keeping the Jepsen finding
as history. Its local bus section (`:449-479`) stays as it is. The record
gets `updated: 2026-10-15` and an Amendments entry dated 2026-10-15. The
edge delivery record's first Open question (`:148-157`) becomes a
Consequences line stating the landed policy and the `BenchmarkDeliver`
rate with its command, and drops rates no command in the tree reproduces.
Tests: `TestHubConfigSyncsPeriodically` in `hub_internal_test.go` asserts
`FsyncPolicy` and `*FsyncInterval` of `hubConfig()`.
`TestHubRunsTheDeclaredSyncInterval` in `fsync_internal_test.go` starts a
hub with periodic sync at 100 ms and asserts `SyncAlways` false and
`SyncInterval` 100 ms on `h.server.JetStreamConfig()`. No test proves the
loss window on power loss. That needs a power cut, and the record states
the window as a bound the cadence does not guarantee.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/host/host.go src/services/device/internal/host/hub_internal_test.go src/modules/edgebus/hub.go src/modules/edgebus/fsync_internal_test.go src/modules/edgebus/README.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md`

### U2. The handler publishes asynchronously
Files: src/services/device/internal/intake/handler.go, src/services/device/internal/intake/handler_test.go, src/services/device/internal/intake/README.md
After: none
Change: `Deliver` runs the two passes and the accounting rule Decisions
state, with `jetstream.WithMsgID` on every publication and the 5 s wait
bound in a handler field tests can shorten. The handler's comments and the
README's delivery paragraph say typed records are published
asynchronously, evidence follows each typed ack, and acks are read in
order.
Tests: in `handler_test.go`, `failingPublisher` becomes an async wrapper of
the hub's client that can return a publish error or a future that fails on
a chosen call, plus a never-resolving future. New cases:
`TestLaterAckDoesNotSettlePastAFailure` (requirement 2),
`TestStalledPublishStopsTheCount` (requirement 3),
`TestCanceledCallEndsTheWait` (requirement 4),
`TestFullTypedStreamStopsTheCountAsync` against a real hub with a one-record
typed stream (requirement 5), and `TestFailedEvidenceAckLeavesTheRecord`
(requirement 6), and `TestUnboundedCallStopsAtTheWaitBound`
(requirement 10). `TestPublishFailureStopsTheCount`,
`TestFullTypedStreamStopsTheCount`, and
`TestBatchDeliveredTwiceIsStoredOnce` keep their assertions on the async
publisher. Requirement 2's case also asserts one retry when B fails and a
later publish errors. Nothing tests that production keeps the 4000
max-pending default, which rests on `E/hub.go:366`.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/intake/handler.go src/services/device/internal/intake/handler_test.go src/services/device/internal/intake/README.md`

### U3. The benchmark has a floor and fits its stream
Files: src/services/device/internal/intake/handler_bench_test.go, src/services/device/internal/intake/README.md
After: U2
Change: `BenchmarkDeliver` starts its hub with periodic sync at 100 ms, sizes
the typed stream, central budget, and store ceiling from `b.N * size` as
Decisions state, and after `b.StopTimer` fails with `b.Fatalf` when a run of
at least 500 ms measured under 30,000 records/s. It also fails when the
typed stream's stored bytes exceed `b.N * size * 1 KiB`, which pins the
per-record bound the sizing rests on. Its comment drops "sets no threshold".
The README's benchmark paragraph names the hub's policy, the floor, and the
command without `-benchtime 20x`. No environment switch from the spike
enters.
Tests: the benchmark itself, run as Verification shows, at the default
benchtime and at `-benchtime 200x`.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/intake/handler_bench_test.go src/services/device/internal/intake/README.md`

### U4. The clustered records name the policy
Files: docs/architecture/2026-10-03-central-high-availability-direction.md, docs/architecture/2026-10-03-deployment-direction.md, docs/plans/2026-10-03-1533-feat-deployment-phase2-plan.md
After: none
Change: the central high availability record's "NATS leaves the device
service" (`:67-69`) says every server sets `sync_interval` to 100 ms and
streams run R3, relying on replication. Its Alternatives gain R3 with
`sync_interval: always`, with the group-commit source from Decisions. Its
Consequences quote both loss modes the NATS concepts page names under
"Syncing data to disk": an OS failure on several servers at once, and one
OS-failed server rejoining with a partitioned one as a majority that never
saw a write. They say what that leaves of the readiness criterion that
losing one replica loses no acknowledged write (`:48`). Its Sources add
both NATS pages. The deployment record's `:106-107` and the deployment
phase 2 plan's NATS decision (`:29-34`) say `sync_interval: 100ms` in the
`jetstream` block, and that plan's requirement 6 (`:67-70`) says every
server syncs every 100 ms, with the rendered block as its example.
Tests: none, prose only. Requirement 9's grep checks the result.
Verify: `uv run tools/scripts/run.py verify change -- docs/architecture/2026-10-03-central-high-availability-direction.md docs/architecture/2026-10-03-deployment-direction.md docs/plans/2026-10-03-1533-feat-deployment-phase2-plan.md`

### U5. The durability solution states the new rate
Files: docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md, docs/solutions/README.md
After: U1, U2, U3
Change: the solution's history paragraph (`:86`), "Sizing central
ingest" bullet (`:95`), and "Choosing durability" bullet (`:92`) say the hub declares periodic sync at
100 ms, that the handler publishes a request asynchronously, and give the
rates `BenchmarkDeliver` reports with its floor. Its `last_verified` moves to
the landing date. The README row's "Read when" keeps the throughput trigger.
Tests: none, prose only.
Verify: `uv run tools/scripts/run.py verify change -- docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md docs/solutions/README.md`

Waves: U1 U2 U4 | U3 | U5

## Verification

```bash
go test -race ./src/services/device/internal/intake/ ./src/services/device/internal/host/ ./src/modules/edgebus/
go test -run '^$' -bench BenchmarkDeliver ./src/services/device/internal/intake/
go test -run '^$' -bench BenchmarkDeliver -benchtime 200x ./src/services/device/internal/intake/
grep -rn "sync_interval: always" docs/architecture
uv run tools/scripts/run.py verify change -- <every changed path>
```

## Definition of done

- [ ] Verifier green for every changed path, READMEs, records, and the
      solution updated in the change that invalidates them.
- [ ] Outcome recorded with `uv run tools/scripts/run.py plan record
      implemented <plan> --units 5 --from <t> --to <t>` or `partial`.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Blocker: what the power-loss window covers beyond ingest records. The
  fsync policy is server-wide (`E/fsync.go:24-25`), and the hub's server
  also holds the lane and other journal buckets (`E/hub.go:397-405`), the
  audit stream, and the operator streams. The verified device access
  record makes the journal the barrier, with `POSSIBLY_APPLIED` checkpointed
  before a command is submitted
  (`docs/architecture/2026-09-05-verified-device-access-direction.md:48-53`).
  A power cut can now lose that checkpoint after the edge submitted.
  Decision 4 speaks of ingest records only. Options:
  1. Keep the server at per-message sync and opt the typed ingest and
     evidence streams out with `PersistMode: AsyncPersistMode`
     (`NS/stream.go:1109-1114`). Recommended: the journal keeps its
     guarantee and ingest gets the throughput. It changes decision 1's
     mechanism, and its rate is unmeasured.
  2. Accept the window for every hub write. U1 then also amends the
     verified device access record and states the recovery path for a lost
     checkpoint. No code beyond this plan.
  3. Accept it for audit and operator streams but run the journal buckets
     on a second, per-message JetStream server. Most code, and a second
     store to operate.
- The plan runs past 300 lines. The re-plan that answers the blocker cuts
  it below that or splits it into phases, since option 1 rewrites U1.
- Decision 4 cites the device service record's `:473-478`, which is the
  local bus paragraph (`:449-479`). U1 amends the hub's rule at `:436-443`
  instead and leaves the local bus paragraph alone.
- An ack that never arrives keeps its slot among the client's 4000
  max-pending until the connection closes, since the hub's client sets no
  ack timeout. `jetstream.WithPublishAsyncTimeout` on the hub's client
  would free it.
- Target-hardware rates are unverified. Every rate above was measured on an
  Apple M3 Pro under darwin, where Go syncs with `F_FULLFSYNC`. A Linux
  server's `fsync` cost differs, and the 30,000 floor may need a re-measure
  there.
- Whether a stalled publish deserves its own retry reason, so an operator
  can tell an overloaded central from a failing store. This plan keeps
  `publish_failed`.
- `docs/plans/2026-10-04-2023-fix-central-intake-review-items-plan.md`
  shares `E/hub.go`, `E/README.md`, `I/README.md`, and the device service
  record with this plan. Whichever lands second merges those files. It does
  not share `I/handler.go` or `I/intake.go`.
- The deployment record and its phase 2 plan were not named in the user's
  decision 5. U4 changes them because they configure the same clustered
  servers and would otherwise keep `sync_interval: always`.
