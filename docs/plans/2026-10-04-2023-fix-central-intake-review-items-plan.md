---
title: Central Intake Review Items - Plan
type: fix
date: 2026-10-04
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# Central Intake Review Items - Plan

## Goal

The tests of the edge follower and of central intake hold every rule their
code states, on any host, and the documents around them say what the code
does. The means is three units with no file in common: the follower and its
tests, intake's tests and prose, and the records. No shipped behavior of
intake changes. Stop condition: if a rule below cannot be tested without
changing how intake republishes, refuses, or retries a record, that rule
is a design question and this plan stops for it.

## Decisions

`E` is `src/modules/edgebus`, `I` is `src/services/device/internal/intake`,
`NC` is `~/go/pkg/mod/github.com/nats-io/nats.go@v1.54.0`, and `NS` is
`~/go/pkg/mod/github.com/nats-io/nats-server/v2@v2.15.0/server`, the versions
`go.mod` pins.

- Central intake landed with open review items, and this plan carries
  them. Why: what stayed open is tests and prose, and no item names a
  record that intake loses, refuses, or stores wrongly. (decided by the
  user, 2026-10-04)
- The follower's loop loses its check for a done context before an interval
  pass (`E/follower.go`, `follow`). Why: the pass it prevents is harmless
  for both callers. `discover` hands `attach` the done context. Both
  callers call `Hub.EdgeStream` first (`E/forwarder.go`, `attach`, and
  `I/intake.go`, `attach`). That returns an error for an unknown edge
  before any request (`E/hub.go`, `EdgeStream`), and otherwise makes a
  JetStream API request that returns the context's error before it sends
  (`NC/jetstream/jetstream.go:813`, `NC/context.go:49-51`). A consumer an
  attach did return would be stored and drained by `Close`, which waits
  for the loop. Holding the check needs a test that repeats a random
  `select` twenty times against one hub
  (`E/follower_test.go`, `TestFollowerCloseCancelsIntervalAttach`).
- With the check gone, an `attach` double in a follower test can be called
  once more under a done context. Every such double returns at once when
  its context is done and signals through `sync.Once`. Why: a double that
  closes a channel on each call panics on the second, and one that sends on
  a channel stalls the loop.
- `TestFollowerCloseCancelsIntervalAttach` is one pass and holds two rules:
  `Close` cancels an interval attach in flight, and `Close` returns. It
  runs `Close` on its own goroutine and waits for it with a deadline. Why:
  with `Close` called on the test goroutine, a `Close` that stops
  cancelling hangs the package to its timeout and names no rule.
- No follower or intake test attaches more than two edges to a hub with
  default budgets. Why: an unset store ceiling is 75 percent of the free
  disk under the state directory (`NS/disk_avail.go:30`,
  `NS/jetstream.go:2821-2826`), and every account budget is reserved
  against it (`NS/jetstream.go:2725-2742`). After the one-pass rewrite no
  follower test does, and intake's `startHub` pins its limits
  (`I/intake_test.go`, `startHub`). A later test that needs more edges
  sets all seven budget fields `startStorageTestHub` sets
  (`E/storage_test.go`), with `MaxStoreBytes` equal to the central budget
  plus the edge budget times the edge count.
- The limiter's window is tested in a `testing/synctest` bubble, one bubble
  per case, as `src/protocol/snmp/test/integration/presence_test.go` does,
  since `t.Run` inside a bubble panics. The `Intake` under test is a struct
  literal built inside the bubble, with no hub. Why: `allowLog` reads
  `time.Now`, the window is ten seconds, `docs/code-style.md`, Testing,
  names the bubble for code that reads the clock, and a hub's goroutines
  would keep the fake clock from advancing.
- The three follower behaviors the forwarder had before the follower was
  extracted stay as they are: a discovery error after start is discarded,
  one failing edge ends a pass, and a consumer the server stopped stays in
  the follower's map. Why: each needs the follower to report to its
  callers, which is a design choice with two callers (Open questions).

## Requirements

Each Requirement names the mutation its test must fail on. Line numbers are
those of the tree this plan was written against.

1. Removing the final acknowledgement fails a test. Example: a valid record
   handed to the handler as a fake message leaves that message
   acknowledged, not terminated, and with no retry delay. Mutation: delete
   `msg.Ack()` in `handle` (`I/intake.go`).
2. A subject inside the edge's subtree and outside its `ingest` branch is
   refused. Example: `EdgeSubtree(<T>, edge) + ".otel.logs"` with a valid
   envelope is terminated and counts one `foreign_subject`. Mutation: check
   the prefix `EdgeSubtree(tenant, edge) + "."` in `prepare`.
3. An envelope with no edge in its provenance is refused as
   `foreign_provenance`. `provenance.edge` carries no `required` rule
   (`spec/proto/flowseer/model/inventory/v1/provenance.proto:19-21`), so
   the envelope passes validation and reaches the provenance check.
   Example: a valid envelope with `provenance.edge` cleared counts one
   `foreign_provenance`. Mutation: accept an empty edge id in that check.
4. The message id carries the tenant. Example: the stored typed message's
   `Nats-Msg-Id` header reads `<T>.<record_id>`, written in the test as a
   literal. Mutation: build the id from the edge id.
5. The central subject is pinned by a literal. Example: the typed message
   is read back from `flowseer.<T>.ingest.syslog.<device-id>` and the
   evidence message from `flowseer.<T>.evidence.syslog.<device-id>`, each
   written out in the test. Mutation: swap the record type and the device
   id in `EvidenceSubject` (`E/subjects.go`).
6. Each limited log line is limited per edge and per line for ten seconds.
   Example, for each of the refusal event, the retry warning, and a
   non-terminal consume error: two calls for one edge log once, a call for
   a second edge logs, a call one millisecond before ten seconds logs
   nothing, and a call at ten seconds logs. Two different non-terminal
   consume errors for one edge inside the window log once. Mutations, each
   failing a case: drop the edge id from a line's key, delete a line's
   limiter check, add the error type to the consume key, and change
   `refusalLogInterval` to one hour.
7. A refusal is logged under a done context, and a retry warning is not.
   Example: with a cancelled context, `logRefusal` writes one line and
   `logRetry` writes none. Mutations: add the done-context gate to
   `logRefusal`, and delete it from `logRetry`.
8. `intake.Start` returns the follower's error. Example: with one attached
   edge whose stream already holds a durable `ingest_intake` consumer of
   another acknowledgement policy, `Start` returns an error and no
   `Intake`. The server refuses that update with "ack policy can not be
   updated" (`NS/consumer.go:1122-1144`, `:2567-2569`). Mutation: return
   the `Intake` and a nil error when `FollowEdges` fails.
9. The follower's lifetime ends with the caller's context. Example: after
   the caller's context is cancelled, the test waits with a deadline for
   the loop to exit, the lifetime captured in `attach` is done, and an edge
   attached afterwards leaves the `attach` call count unchanged. Mutation:
   derive the run context from `context.Background()` in `FollowEdges`.
10. `Close` drains a consumer that an interval pass returns after cancel.
    Example: an interval `attach` that waits for its context to end and
    then returns a consumer has that consumer drained when `Close`
    returns. Mutation: delete the wait for the loop in `Close`.

## Out of scope

- Any change to how intake republishes, refuses, retries, or logs.
- The three inherited follower behaviors (Decisions, Open questions).
- A publish that fails on payload size and is retried until the edge
  stream evicts the record.
- The forwarder's handler running under a background context.

## Units

### U1. Follower loop, tests, and edgebus prose
Files: src/modules/edgebus/follower.go, src/modules/edgebus/follower_test.go, src/modules/edgebus/hub.go, src/modules/edgebus/README.md
After: none
Change: `follow` loses the done-context check before `discover`. The
`EdgeFollower` doc comment states that it is safe for concurrent use, and
`mu` names the field it guards. The `FollowEdges` comment states the
interval default of ten seconds and that a failed first pass returns no
follower and the attach error after the drain. `closeDone` goes, since a
second caller of `sync.Once.Do` waits until the first call's function
returns (`go doc sync.Once.Do`). In `hub.go` the `AuditDuplicateWindow`
comment becomes two sentences. The README's two statements of a ten minute
window say that the window is `AuditDuplicateWindow`, ten minutes by
default, capped by the stream's maximum age.
Tests: in `follower_test.go`, `TestFollowerCloseCancelsIntervalAttach` is
one pass: an interval `attach` blocks on its context, `Close` runs through
`spawn.Go`, and the test waits fifteen seconds each for the attach to
start, for it to see its context end, and for `Close` to return. It must
fail with `f.cancel()` removed from `Close`.
`TestFollowerLifetimeEndsWithCallerContext` (R9).
`TestFollowerCloseDrainsALateIntervalConsumer` (R10). Every double follows
the Decision on doubles. Nothing covers a pass that starts after cancel,
which is now allowed.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus`

### U2. Intake tests and prose
Files: src/services/device/internal/intake/intake_test.go, src/services/device/internal/intake/intake.go, src/services/device/internal/intake/README.md
After: none
Change: `intake.go` changes in comments only. The `Start` comment says a
valid record delivered between the context ending and `Close` is retried
and a refused one is still terminated. The README's diagram draws the
evidence publish before the typed publish on one path. It names the retry
warning's message and says retry warnings stop once the lifetime ends. It
no longer calls every error outside the three terminal types non-terminal:
it says such an error logs as `intake consumer error` and that one of
them, a pending-header parse error, stops the consumer
(`NC/jetstream/pull.go:754-757`, `:273-281`). It says the duplicate window
is `AuditDuplicateWindow`, ten minutes by default.
Tests: in `intake_test.go`. `TestValidRecordIsAcknowledged` (R1).
`TestSubjectOutsideTheIngestBranchIsRefused` (R2).
`TestUnsetProvenanceEdgeIsRefused` (R3).
`TestCentralMessageIDCarriesTheTenant` (R4). The typed and evidence tests
that exist read their messages from literal subjects (R5).
`TestLogLinesAreLimitedPerEdge` replaces the three per-edge tests with one
table of three named cases and gains the two-error consume case (R6).
`TestLogLimitWindowIsTenSeconds` runs the same three cases, each as a
subtest that opens its own bubble (R6).
`TestRefusalIsLoggedUnderADoneContext` and
`TestRetryIsNotLoggedUnderADoneContext` (R7).
`TestStartReturnsTheAttachError` (R8). `assertLogEdges` counts an empty log
as zero lines. Each new test's commit line quotes its Requirement's
mutation and the `--- FAIL` line it produced.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/intake`

### U3. Records and the host test
Files: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md, src/services/device/internal/host/host_test.go
After: none
Change: the device service record's lead-in no longer says its Events
bullet records the earlier shape. The ingestion record's sentence that
nothing central reads the `ingest` branch names intake, and its sentence
that the record id is the NATS message id says the central id is
`<tenant>.<record_id>`. The budget solution's example of a 512 MiB central
budget sets the stream limits that fit under it, as `startStorageTestHub`
does, its `hub.go` and `keys.go` line cites match the tree, and its
nats-server cites name v2.15.0 lines. `host_test.go` loses the lint
directive for `gosec` on its TLS configuration line.
Tests: none. The unit changes prose and removes a directive for a linter
`.golangci.yml` does not enable, so `golangci-lint` is the check.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture docs/solutions/architecture-patterns src/services/device/internal/host`

Waves: U1 U2 U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus src/services/device docs/architecture docs/solutions/architecture-patterns
go test -race -count=3 ./src/modules/edgebus/... ./src/services/device/internal/intake/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Each Requirement's mutation fails its test, quoted in the commit.
- [ ] The edgebus and intake READMEs and the two records match the code.
- [ ] This plan's `status` is set with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- How the follower reports to its callers. Today a discovery error after
  start is discarded, one failing edge ends a pass for the edges after it,
  and a consumer the server stopped is never attached again. All three
  were the forwarder's before the follower was extracted, and intake now
  inherits them. A fix gives `FollowEdges` an error sink or a logger from
  its two callers, which is its own plan.
- Unverified: whether `E/forwarder_test.go` or `E/edgebus_test.go` holds a
  double that a pass after cancel would break. U1 reads both before it
  removes the check.
