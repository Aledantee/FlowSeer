---
title: Central Ingest Async Persistence - Plan
type: perf
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md, docs/architecture/2026-10-03-central-high-availability-direction.md, docs/architecture/2026-10-03-deployment-direction.md, docs/plans/2026-10-03-1533-feat-deployment-phase2-plan.md
---

# Central Ingest Async Persistence - Plan

## Goal

Central's ingest call settles edge records at tens of thousands per second
instead of about two hundred, while the journal keeps per-message sync. The
means are async persistence on the typed ingest and evidence streams only,
synced every 100 ms, and a delivery handler that publishes asynchronously
and reads acks in record order. Stop condition: the plan is wrong if, with
the hub running, the handler can answer a `settled` count that covers a
record central does not hold, since the edge deletes every settled record.

## Decisions

`E` is `src/modules/edgebus`, `I` is `src/services/device/internal/intake`,
`NC` is `~/go/pkg/mod/github.com/nats-io/nats.go@v1.54.0/jetstream`, and
`NS` is `~/go/pkg/mod/github.com/nats-io/nats-server/v2@v2.15.0/server`, the
versions `go.mod` pins.

- The hub server keeps `service.BusFsyncPerMessage`. Only the typed ingest
  streams and the evidence stream switch to `PersistMode: AsyncPersistMode`
  (`NS/stream.go:122-196`, `:1109-1114`). The journal buckets, the lane
  bucket, the audit streams, the operator streams, and the key-value
  buckets keep per-message sync. (decided by the user, 2026-10-15)
- The ingest delivery handler (`I/handler.go`, `deliverOne` about
  `:144-160`) publishes a request's records asynchronously and then waits
  for every ack in record order. Settled stays the in-order prefix of
  records whose every publication (typed and evidence) is acked. The first
  failed ack or publish error stops the count. Context cancellation ends the
  wait. The client's max-pending stall must be respected. (decided by the
  user, 2026-10-15)
- `BenchmarkDeliver` (`I/handler_bench_test.go`) gains a pass floor of
  30,000 records/s. (decided by the user, 2026-10-15)
- Amend the device service record's power-loss rule, scoped to ingest and
  evidence records only. Central's single-node hub accepts losing up to
  about one 100 ms sync interval of acknowledged records on power loss,
  records the edge has already deleted. Process crash loses nothing
  acknowledged. (decided by the user, 2026-10-15) See the blocker under Open
  questions: async persistence breaks the process-crash sentence.
- Clustered central (the HA plans) uses R3 streams with periodic fsync (the
  same 100 ms interval) and relies on replication for durability. The
  residual risk: a power cut that hits every replica at once can lose
  acknowledged writes (NATS concepts, "Syncing data to disk",
  https://github.com/nats-io/nats.docs/blob/master/nats-concepts/jetstream/README.md).
  The rejected alternative is R3 with `sync_interval: always`, which
  group-commits the Raft log (`NS/stream.go:1116-1122`, `NS/raft.go:3454-3455`
  and `:3510-3535`, up to 4096 entries or 256 KiB). (decided by the user,
  2026-10-15)
- Under per-message sync the hub sets the server's `SyncInterval` to a
  100 ms constant after `applyFsync`. Why: an async-persist stream takes the
  server's `SyncInterval` (`NS/stream.go:1101-1112`), `applyFsync` leaves it
  zero under per-message sync (`E/fsync.go:24-25`), and the server turns
  zero into 2 minutes (`NS/opts.go:6192-6193`). Decision 4's window needs
  100 ms. On default-mode streams the timer does no message fsync but runs
  its compaction and idle-file checks every 100 ms instead of every
  2 minutes (`NS/filestore.go:8253-8271`), a cost not measured.
  `service.NormalizeFsync` stays, since the leaf and local bus have no
  async-persist streams.
- Consumer progress on the async-persist streams is never fsynced
  (`NS/filestore.go:14047`, `:14622-14624`), so a power cut can roll back
  `history_sink`'s acks and redeliver records it already handled.
- Clustered central keeps the default persist mode on every stream, with
  the servers' `sync_interval` at 100 ms. Why: nats-server refuses async
  persistence on a replicated stream (`NS/stream.go:2084-2086`), and an R3
  default-mode stream already flushes asynchronously under periodic sync
  (`NS/stream.go:1106-1107`).
- The ingest and evidence streams pass the other refusals: file storage, no
  `AllowAtomicPublish` (`E/hub.go:484-519`, `NS/stream.go:2080-2090`).
- An older store refuses to start the hub, since a stream's persist mode
  cannot change (`NS/stream.go:2602-2604`). No migration is added
  (`AGENTS.md`, breaking changes).
- `HandlerConfig.Central` takes an `AsyncPublisher` (`PublishAsync` only)
  defined in `I/handler.go`. Why: `jetstream.JetStream` satisfies it, so
  `src/services/device/internal/host/serve.go:325-331` compiles unchanged.
- The handler runs in two passes. The first prepares each record and
  publishes its typed message with `PublishAsync`, stopping at the first
  preparation retry or publish error. The second walks the records in order,
  reading each typed future with a `select` on `Ok()`, `Err()`,
  `ctx.Done()`, and the wait bound. Once a raw record's typed ack arrives,
  it publishes the evidence and reads that future before moving on. Why:
  the spike's `deliverAsync` (commit `7e38bf9f8`) without its evidence
  shortcut, which would break `I/README.md:130-131`. `deliverOne` goes.
- Every counter, the refusal log, the duration, and the one retry per call
  come from the walk, for the records it reaches. The walk stops at the
  first record that does not settle and counts its retry once:
  `stream_full` for a typed publication's error, returned or from its
  future, that `edgebus.IsStreamFull` matches, the preparation reason for a
  preparation retry, and `publish_failed` for anything else, a stall,
  cancellation, and the wait bound included. Why: today each record is
  counted when the in-order loop reaches it (`I/handler.go:111-116`).
- The walk waits at most 5 s per future when `ctx` has no deadline. Why:
  the sync path had the client's 5 s default (`NC/jetstream.go:477`), and
  the hub's client sets no async ack timeout (`NC/publish.go:380`,
  `E/hub.go:366`).
- An async ack error is a `*jetstream.APIError` (`NC/publish.go:574-576`),
  and an abandoned future needs no draining (`NC/publish.go:674-700`).
- `ErrTooManyStalledMsgs` ends publishing like any publish error, and the
  4000 max-pending default stays (`NC/publish.go:372-378`,
  `NC/jetstream.go:496`). A record acked past a failure is stored once when
  resent within the 10-minute duplicate window (`NC/publish.go:312-313`,
  `E/hub_test.go:44`), and twice after it.
- The floor applies to runs timed at 500 ms or more, at the default
  benchtime. Why: the first call, with `b.N` 1, measures setup.
- The benchmark sizes its limits from the run: the typed stream holds the
  larger of `b.N * size * 1 KiB` and 112 MiB, the central budget is
  `HubConfig.CentralStreamBytes()` (`E/hub.go:176-182`), and the store
  ceiling adds 256 MiB (`NS/jetstream.go:2821-2822` keeps an explicit
  ceiling). Why: at `-benchtime 200x` and batch 1024 the fixed stream filled.

## Requirements

1. Only ingest and evidence persist asynchronously. Example: on a hub
   started with `BusFsyncPerMessage`, the typed syslog stream and the
   evidence stream report `PersistMode` async, the audit stream and
   `KV_device-lanes` report default, and `server.JetStreamConfig()` reports
   `SyncAlways` true and `SyncInterval` 100 ms.
2. A later ack does not extend the count past an earlier failure, and one
   call counts one retry. Example: A, B, C with B's typed ack failing answer
   `settled` 1, the typed stream holds A and C, and one `publish_failed` is
   counted. Resending B and C answers 2 and counts one duplicate.
3. A publish error stops publishing. Example: a second `PublishAsync` call
   returning `jetstream.ErrTooManyStalledMsgs` leaves two calls for three
   records and `settled` 1.
4. Cancellation and the wait bound end the wait. Example: B's future never
   resolves, and a 100 ms deadline, or no deadline with the bound set to
   100 ms, returns `settled` 1 within a second.
5. A full typed stream ends the count with `stream_full`. Example: a typed
   stream that holds one test record answers two records with `settled` 1.
6. Evidence follows the typed ack and counts toward its record. Example: a
   raw record whose evidence ack fails answers `settled` 0, and a raw record
   whose typed ack fails publishes no evidence.
7. Refused records stay in the prefix, as
   `TestInvalidRecordIsSettledAndRefused` holds today.
8. `BenchmarkDeliver` fails below 30,000 records/s and never fills its
   stream. Example: a run of at least 500 ms below the floor ends in
   `b.Fatalf` naming both rates, and `-benchtime 200x` settles every batch.

## Out of scope

- A configurable ingest sync interval, the edge's and local bus's policies,
  a benchmark gate, and the HA phase plans, which carry their bullet.

## Units

### U1. Ingest and evidence persist asynchronously
Files: src/modules/edgebus/hub.go, src/modules/edgebus/fsync.go, src/modules/edgebus/hub_test.go, src/modules/edgebus/hub_internal_test.go, src/modules/edgebus/README.md, docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md
After: none
Change: the typed ingest and evidence stream configurations set
`PersistMode: jetstream.AsyncPersistMode`. `StartHub` sets
`opts.SyncInterval` to a 100 ms package constant when the policy is
per-message, and the `FsyncPolicy` comment says so, as does `applyFsync`'s
comment (`E/fsync.go:16-18`). `E/README.md` Durability states which
streams persist asynchronously, the 100 ms sync, the loss on power loss and
on a crash as the blocker settles it, the consumer redelivery after a power
cut, and that an older store refuses to start. The device service record's hub bullet
(`:436-443`), `:341-342`, and `:551` say the same and keep the Jepsen
finding as history, with an Amendments entry and `updated: 2026-10-15`. Its
local bus section (`:449-479`) stays. The edge delivery record's first Open
question (`:148-157`) becomes a Consequences line with the
`BenchmarkDeliver` rate and its command.
Tests: `hub_test.go` asserts the persist mode of the typed, evidence,
audit, and lane streams (requirement 1). `hub_internal_test.go` asserts
`SyncAlways` and `SyncInterval` on `h.server.JetStreamConfig()` for a
per-message hub, and that a periodic hub keeps its own interval.
`TestOlderIngestStreamRefusesStart` pre-creates the typed stream in default
mode and expects `StartHub` to fail. Nothing tests the loss on power loss
or a crash. The record states it from the source.
Verify: `uv run tools/scripts/run.py verify change -- src/modules/edgebus/hub.go src/modules/edgebus/hub_test.go src/modules/edgebus/hub_internal_test.go src/modules/edgebus/README.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md`

### U2. The handler publishes asynchronously
Files: src/services/device/internal/intake/handler.go, src/services/device/internal/intake/handler_test.go, src/services/device/internal/intake/README.md
After: none
Change: `Deliver` runs the two passes and the accounting rule Decisions
state, with `jetstream.WithMsgID` on every publication and the 5 s bound in
a handler field tests can shorten. The comments and the README's delivery
paragraph say typed records publish asynchronously, evidence follows each
typed ack, and acks are read in order.
Tests: `failingPublisher` becomes an async wrapper of the hub's client that
returns a publish error, a failing future, or a never-resolving future on a
chosen call. New cases: `TestLaterAckDoesNotSettlePastAFailure`
(requirement 2, which also asserts one retry when a later publish errors),
`TestStalledPublishStopsTheCount` (3), `TestCanceledCallEndsTheWait` and
`TestUnboundedCallStopsAtTheWaitBound` (4),
`TestFullTypedStreamStopsTheCountAsync` on a real hub (5), and
`TestEvidenceFollowsTheTypedAck` (6). The existing cases keep their
assertions. Nothing tests that production keeps the 4000 max-pending
default, which rests on `E/hub.go:366`.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/intake/handler.go src/services/device/internal/intake/handler_test.go src/services/device/internal/intake/README.md`

### U3. The benchmark has a floor and fits its stream
Files: src/services/device/internal/intake/handler_bench_test.go, src/services/device/internal/intake/README.md
After: U1, U2
Change: `BenchmarkDeliver` keeps the per-message hub, which now persists
ingest asynchronously, sizes its limits from `b.N * size`, and after
`b.StopTimer` fails when a run of at least 500 ms measured under 30,000
records/s, or when the typed stream holds more than `b.N * size * 1 KiB`.
Its comment drops "sets no threshold". The README's benchmark paragraph
names the floor and the command without `-benchtime 20x`. No environment
switch from the spike enters.
Tests: the benchmark, at the default benchtime and at `-benchtime 200x`.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/intake/handler_bench_test.go src/services/device/internal/intake/README.md`

### U4. The clustered records name the policy
Files: docs/architecture/2026-10-03-central-high-availability-direction.md, docs/architecture/2026-10-03-deployment-direction.md, docs/plans/2026-10-03-1533-feat-deployment-phase2-plan.md
After: U1
Change: the central high availability record's "NATS leaves the device
service" (`:67-69`) says every server sets `sync_interval: 100ms`, streams
run R3 in the default persist mode because async persistence is refused on
replicated streams, and durability relies on replication. Its Alternatives
gain R3 with `sync_interval: always`. Its Consequences quote both loss
modes the NATS concepts page names (several servers failing at once, and
one OS-failed server rejoining a partitioned one as a majority) and say
what they leave of the criterion at `:48`. Its Sources add both NATS pages.
The deployment record's `:106-107` and the deployment phase 2 plan's NATS
decision (`:29-34`) and requirement 6 (`:67-70`, its sentence included) say
`sync_interval: 100ms`.
Tests: none, prose only. `grep -rn "sync_interval: always" docs/architecture`
returns only the Jepsen history and the rejected alternative.
Verify: `uv run tools/scripts/run.py verify change -- docs/architecture/2026-10-03-central-high-availability-direction.md docs/architecture/2026-10-03-deployment-direction.md docs/plans/2026-10-03-1533-feat-deployment-phase2-plan.md`

### U5. The durability solution states the new rule
Files: docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md, docs/solutions/README.md
After: U1, U2, U3
Change: the solution's Guidance (`:41`) says `PersistMode` is a per-stream
lever that the hub uses on ingest and evidence, with the v2.15.0 refusals.
Its history paragraph (`:86`), "Choosing durability" (`:92`), "Sizing
central ingest" (`:95`), and `:111` give the hub's split policy and the rate
`BenchmarkDeliver` reports with its floor. `last_verified` moves to the
landing date, and the README row's "Read when" names `PersistMode`.
Tests: none, prose only.
Verify: `uv run tools/scripts/run.py verify change -- docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md docs/solutions/README.md`

Waves: U1 U2 | U3 U4 | U5

## Verification

```bash
go test -race ./src/services/device/internal/intake/ ./src/modules/edgebus/
go test -run '^$' -bench BenchmarkDeliver ./src/services/device/internal/intake/
go test -run '^$' -bench BenchmarkDeliver -benchtime 200x ./src/services/device/internal/intake/
uv run tools/scripts/run.py verify change -- <every changed path>
```

## Definition of done

- [ ] Verifier green for every changed path, READMEs, records, and the
      solution updated in the change that invalidates them.
- [ ] Outcome recorded with `uv run tools/scripts/run.py plan record
      implemented <plan> --units 5 --from <t> --to <t>` or `partial`.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Blocker: decision 4's "Process crash loses nothing acknowledged" does not
  hold for async-persist streams. The server's own comment says writes
  "could be lost if they weren't flushed prior to a hard kill of the server"
  (`NS/stream.go:193-195`). The ack goes out before the write reaches the
  operating system (`NS/filestore.go:7800`, flush in place is off). The
  flush loop sleeps 1, 2, 4, then 8 ms while fewer than 16 KiB wait, so up
  to about 15 ms, with no byte cap under load (`NS/filestore.go:344-347`,
  `:6751-6764`). Recovery expects lost unflushed blocks
  (`NS/filestore.go:538-540`). The hub runs in central's process, so a
  central crash is a hard kill. Options:
  1. Accept it and amend the sentence: a crash can lose the ingest and
     evidence records acknowledged in roughly the last 15 ms per stream, a
     bound the flush cadence does not guarantee. Recommended: no code beyond
     this plan, and the window is a fraction of the accepted power-loss one.
  2. Replace decision 1: run ingest and evidence in default persist mode on
     a second embedded JetStream server with periodic sync at 100 ms, where
     writes reach the operating system before the ack. The crash sentence
     holds, and the edge delivery record measured about 42,000 records/s
     under periodic sync (`:153`). It voids the `SyncInterval` and older-store
     decisions, requirement 1, and U1, and costs a second server, account,
     budget, and the intake and history sink wiring.
- Target-hardware rates are unverified. The measurements below were taken
  on an Apple M3 Pro under darwin, where Go syncs with `F_FULLFSYNC`.
- Whether a stalled publish deserves its own retry reason.
- `docs/plans/2026-10-04-2023-fix-central-intake-review-items-plan.md`
  shares `E/hub.go`, `E/README.md`, `I/README.md`, and the device service
  record. Whichever lands second merges them.
- U4 changes the deployment record and phase 2 plan, which decision 5 does
  not name, since they configure the same servers.
- Whether `history_sink` is idempotent under the redelivery a power cut
  causes. Unverified.
- The HA phase 2 and 3 bullets cite U4's amendment. Phase 2's state file
  does not wait for this plan, since the user asked that it stay unchanged.
- A lost ack holds one max-pending slot until the connection closes.
  `jetstream.WithPublishAsyncTimeout` on the hub's client would free it.

## Measurements

Spike branch `worktree-agent-afdd6a6e4f97d567a`, commit `364185c91`,
per-message server, `BENCH_PERSIST=async BENCH_LIMIT_SCALE=4` (plus
`BENCH_ASYNC=1` for async publish), `go test -run '^$' -bench
BenchmarkDeliver -count 3 ./src/services/device/internal/intake/`, records/s:

| Publish | Batch 64 | Batch 1024 |
|---|---|---|
| Synchronous | 48,391 / 43,001 / 41,701 | 51,871 / 52,786 / 53,540 |
| Asynchronous | 103,597 / 103,667 / 101,338 | 105,129 / 99,198 / 109,643 |
| Async, default persist (control, `-benchtime 20x`, one run) | 224 | 229 |
