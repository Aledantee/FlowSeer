---
title: Device Access Lane Host Contract - Plan
type: feat
date: 2026-09-06
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
amends: docs/plans/2026-09-05-2252-feat-device-access-lane-plan.md
---

# Device Access Lane Host Contract - Plan

Implemented on 2026-09-07 across seven slices: drift removal, the
acknowledgement door and reporter, the freeze audit, per-operation sessions
and credentials, the recovery decisions, the recovery poll, resume with the
dequeue hold re-check, and the epoch re-probe. Two defects in the unit's own
earlier slices were found by reversal rather than by review — a submit latch
whose test could not reach it, and a pre-mutation baseline that never ran —
and both are recorded in the commits that fixed them. The route-evidence
follow-up below was found by enumeration before writing code.

The changes `src/modules/localnet/access` needs before a host can drive it:
an outward reporter, an acknowledgement that reaches a mutation at any open
phase and is decided synchronously, per-operation device sessions, a
recovery loop that keeps the device's drainer free between polls, and the
firmware epoch re-probe. Every decision here is made and argued in the
[central device service plan](2026-09-06-1405-feat-central-device-service-and-hosts-plan.md)
under "Inherited gaps" and the design bullets on the reporter, drift, and
device sessions; this plan carries the units and their tests so the module
can land and be reviewed on its own before the hosts do. Where the review
of the first draft changed a mechanism, the decision is restated here.

## Goal

After this plan, `Lane.Submit` reports each mutation's admission, checkpoint,
verified result, recovery entry, release, and abandonment to a host-supplied
`Reporter` before it waits on central; central's `TerminalResultAck` is
accepted or refused at once against the machine's phase, and an accepted
ack reaches the mutation wherever it rests; a mutation whose effect is
unknown polls through `internal/recovery` as head-priority items on the
device's own queue while reads keep flowing; every device call opens its
session from material acquired for that call; the firmware fingerprint is
re-probed around every side effect and observation and refreshed in place;
and the lane no longer evaluates drift or holds a lane-wide management mode.
Stop if `Machine.Execute` cannot open a second grant for the same sequence
on retry, because central's `OpenDeviceSubmission` then needs a retry
counter this plan does not add; the edge-bus README says a grant is one-use
per stream, not per sequence, so it can.

## Decisions restated after review

- **An ack is applied at the door, under `Machine.mu`, never stored.**
  `deviceState.current` (under `stateMu`, set only by a mutation at
  admission, cleared when it finishes) is the machine central's ack
  addresses. `HandleTerminalAck` calls `Machine.Acknowledge(ctx, ack)` on
  the caller's goroutine. Under `mu` it checks the matrix against the
  phase and the submit latch and either marks the outcome (`cancelled`
  for `REJECTED`, `abandoning` for `INDETERMINATE_ABANDONED`, `verified`
  for `VERIFIED`), or returns `mutation/out-of-order` (a disposition the
  phase does not allow), `access/no-pending-wait` (wrong sequence, no open
  mutation), or `access/already-terminal` (the machine is terminal and its
  report is on the way) with nothing changed. It then delivers the
  terminal audit records and, under `mu` again, sets the terminal phase:
  `VERIFIED` and `REJECTED` walk `ACKNOWLEDGED` to `RELEASED`;
  `INDETERMINATE_ABANDONED` runs `Abandon`, which ends `ABANDONED` with
  `RECOVERY_HOLD` and engages the hold. A failed audit delivery returns
  the error to central, leaves the mark set, and central re-sends. The
  machine exposes `Done()`, closed when it turns terminal; every rest
  point (`awaitCheckpoint`, the freeze wait and grant open inside
  `Execute`, the epoch-block wait, the `VERIFIED` wait, the poll timer)
  selects on it. `REJECTED` and `Submit` are latched under `mu`:
  `Acknowledge` sets `cancelled` only while `submitted` is false and
  cancels the context `Execute` runs its waits under; `Execute` sets
  `submitted` only while `cancelled` is false, immediately before
  `deps.Submit`. Exactly one of "released `REJECTED`" and "command sent"
  wins; the barrier `RLock` inside `Gate.Enter` is not cancellable, so a
  cancel delivered while `Execute` is parked there takes effect when it
  unparks, before any submit. After any step error, `process` reads the
  machine: terminal means the ack already ended it (the terminal report
  goes out and no hold is engaged); `cancelled` but not terminal means an
  audit delivery failed mid-ack, and the mutation waits on `Done()` under
  the detached context for the re-sent ack; otherwise the error is what
  it says. Why: a stored ack must be re-validated when the phase moves
  and re-armed on every path that leaves a rest point, and the
  previous draft did neither; applying at the door under the machine's
  own lock leaves nothing to re-validate and nothing to swallow.
- **Recovery polls take the drain lock between items; they are not
  queue items.** On ambiguity `process` enters `RECOVERING`, reports
  progress, keeps the hold engaged, leaves the submission open (no
  outcome is sent), and returns. A per-device timer goroutine, driven by
  `Config.Wait` under a context detached from the submitter
  (`context.WithoutCancel`, bounded by the horizon plus one poll
  interval) and cancellable by `Close` and by the ack path, acquires
  `ds.draining` with a blocking `Lock` each `RecoveryPollInterval`,
  checks its own context and the lane's `closed` flag after acquiring
  and before any step (a timer parked in `Lock` when `Close` cancels it
  must release without running), runs one `Runner.Attempt`, unlocks, and
  then calls `l.drain(ds)`; on `OutcomeRetry` it also runs
  `Retry`, `Execute`, and `Observe` while holding the lock; on
  `OutcomeVerified` it marks verified and reports, and the `VERIFIED` ack
  completes it; on `OutcomeAbandoned` it reports and completes. `Attempt`
  returning an error (a failed fence, an abandonment whose audit delivery
  failed) keeps the timer and retries the same step next tick, so a
  horizon-crossing abandonment is retried until it sticks. Whoever turns
  the machine terminal, the poll or `HandleTerminalAck`, completes the
  submission exactly once (`sync.Once`), sends the outcome, and clears
  `current`. An `Execute` error before the submit latch (a revoked
  authority, a freeze wait that ended, a grant that failed to open)
  provably sent nothing: it is reported with `submitted` false and never
  enters recovery. Every error after the latch, a `MarkVerified` whose
  audit delivery failed included, enters recovery, and the `VERIFIED`
  wait runs under the detached context so the submitter's deadline
  cannot end it; the lane therefore never reports an error with
  `submitted` true, and central's rule for one is defensive. A resumed
  mutation (`resume` set) takes `since` from the carried submission
  time, so an edge that crash-loops does not reset the horizon. Its
  retry path is fence-only, and nothing in this slice wires
  `Config.Fenced` (decision 8 defers fencing), so a resumed mutation
  either verifies through `OutcomeVerified` or abandons at the horizon;
  no retry is available to it. `Close` cancels every device's timer before returning; a
  poll already holding the lock finishes its current step, and no step
  starts after `Close`. A mutation dequeued while the hold is active
  fails with `access/desynchronized` instead of executing, closing the
  window in which two mutations admitted before the first failed would
  both run. Why: a parked drainer serves no reads for the horizon; a
  queue item for the poll needs a payload type, a capacity reservation,
  and an outcome contract the drain loop does not have, and can be
  refused by a full queue on the tick that would abandon; the lock gives
  the same single active worker with none of that. Argued against the
  head-priority re-admission the coordinator asked for on those grounds.
  The release rule is a property of the lock, not of the drain loop: any
  acquirer of `ds.draining` must, on release, drain the queue and
  re-check its length, because a submitter whose `TryLock` loses exits
  at once and never retries, so an item admitted while the poll held the
  lock would otherwise have no drainer until its caller gave up. The
  timer meets it by calling `l.drain(ds)` after unlocking; the sentence
  goes next to `drain`'s own comment so a third acquirer finds it.
- **A pre-mutation read is taken before `Execute`.** It is the recovery
  baseline `Runner.Attempt` corroborates against and the read the
  pre-`Execute` epoch probe accompanies; `Compare` keeps `cached` nil for
  a mutation, since a read expected to hold the old description is not a
  reading the new one must agree with. Why: one SNMP round trip per
  mutation buys corroboration-driven retry and the epoch check; the
  earlier claim that it also served the conflicting-reads rule was
  wrong and is withdrawn.
- **`Retry` and the ordinary poll both keep the machine record-free in
  recovery.** `EnterRecovering` and `Machine.Retry` set `inRecovery`;
  while set, `Observe` emits no records and returns to `RECOVERING`;
  `MarkVerified` and `Abandon` clear it. `Retry` itself emits one
  `PhaseTransitioned` to `POSSIBLY_APPLIED`. `OutcomeVerified` is decided
  before the horizon check, so a verifying observation on the crossing
  poll verifies. Central's submission gate admits a sequence in recovery
  (its `MutationState.phase` stays `POSSIBLY_APPLIED`), so the retry's
  fresh grant opens.
- **`FingerprintOverride` is deleted.** Tests seed a fake SNMP session
  whose `sysDescr` and `sysObjectID` the test controls, through
  `DeviceSession.OpenSNMP`. Why: an override makes the re-probe compare a
  probe digest with a host string, the bug the lane plan's review removed.
- **A post-`Observe` epoch change discards the observation.** The probe
  runs before `recordEvidence`; on change the lane refreshes the
  fingerprint, invalidates evidence, emits the record and the event, and
  a read fails with `mutation/firmware-epoch` and a mutation enters
  recovery, where the next poll re-observes under the new epoch. Why: an
  observation taken across an epoch boundary has no honest fingerprint
  label.
- **`Reporter` never does I/O inline.** Its methods return nothing and
  return at once; the host's implementation hands the report to its own
  re-send queue. A nil `Reporter` is a no-op; the lane never fails an
  operation on it. The audit deliverer keeps its blocking contract.
  Per-joiner results are `proto.Clone`d. The `ADMITTED` and `RECOVERING`
  reports carry the `progress` arm.
- **`Freeze` freezes first and reports delivery separately.** `Lane.Freeze`
  copies the device keys under `l.mu`, freezes the gate, emits one
  `LaneFrozen` per device outside any lock (even when `Gate.Freeze`
  returned the context's error, since the gate is frozen regardless),
  remembers per device which records were delivered, and returns the
  joined delivery errors with the gate still frozen; a repeated `Freeze`
  emits only the missing records; `Unfreeze` clears the set so the next
  fence records again; `AddDevice` on a frozen lane emits the new
  device's record at once. Why: the fence is called when the audit path
  is likely failing, and a partial emission must be recoverable.

## Requirements

1. `Lane.Submit` calls `Reporter.Reported` at `ADMITTED` and on entering
   `RECOVERING` (both with the `progress` arm), at `VERIFIED` before the
   ack wait, at `RELEASED`, at `ABANDONED`, and on error, and for a read
   once with the observation at `OBSERVING`; `CheckpointAcked` after
   `Machine.Checkpoint`; `HoldResolvedAcked` after `ResolveHold`; a
   coalesced read reports once per joiner with a `proto.Clone` carrying
   that joiner's sequence, which is also what that joiner's `Submit`
   returns. A nil `Reporter` is a no-op. Example: a reporter fake sees
   `ADMITTED`, `CheckpointAck{seq 3}`, `VERIFIED` with the observation,
   then `RELEASED`, and the last arrives only after `HandleTerminalAck`;
   two joiners see sequences 4 and 5 on distinct messages.
2. `HandleTerminalAck` applies at the door: `VERIFIED` at `VERIFIED`,
   `INDETERMINATE_ABANDONED` at every open phase, `REJECTED` at
   `ADMITTED` or at `POSSIBLY_APPLIED` before the submit latch; anything
   else returns `mutation/out-of-order` with nothing changed; a terminal
   machine returns `access/already-terminal`; no open mutation returns
   `access/no-pending-wait`. Applying sets the terminal phase per the
   decision and completes the submission once. A `REJECTED` ack accepted
   before the latch ends the mutation `RELEASED` with `REJECTED` and the
   command is never sent; one offered after the latch is refused. A
   concurrent read never sees `ACKNOWLEDGED` with `UNSPECIFIED`. Example:
   central acks `REJECTED` while `Execute` is blocked in the freeze wait;
   the wait is cancelled, `Submit` is never called, `process` finds the
   machine terminal and engages no hold, the report says `RELEASED`; the
   same ack after `Submit` returned is refused and a later `VERIFIED` ack
   is accepted; `INDETERMINATE_ABANDONED` while a retry sits at
   `POSSIBLY_APPLIED` abandons it before its observation is compared and
   the poll timer stops; with an audit deliverer that fails the terminal
   record, the ack returns an error, the next identical ack succeeds, and
   the first `RELEASED` report follows it. In U1 the `RECOVERING` arm is
   provable at the machine only, since `process` has no `RECOVERING`
   rest point until U2. An error the lane reports before the submit
   latch carries `ExecuteResult.submitted` false, so central disposes it
   `REJECTED`; after the latch the lane enters recovery instead.
3. An `Execute` error after the submit latch, a failed `Observe`, or an
   unverified `Compare` enters `RECOVERING`, reports progress, returns
   without an outcome, and
   a per-device timer polls `Runner.Attempt` under the drain lock every
   `RecoveryPollInterval` under the detached context until verified,
   retried, or abandoned; a verifying observation wins over the horizon;
   a read submitted meanwhile is served between polls; a mutation
   dequeued under the hold fails with `access/desynchronized`; `Close`
   cancels the timer. An `ExecuteRequest` with `resume` set (central
   re-dispatching after an edge restart past a confirmed checkpoint)
   admits straight into `RECOVERING` with no baseline and `since` set to
   the carried admission time; with no fence wired it verifies or
   abandons. Example: with a fake `Wait`, two observations matching the
   pre-mutation read one minute apart retry once, emitting exactly one
   `PhaseTransitioned` (to `POSSIBLY_APPLIED`) and no record for the
   observations before or after; a device unreachable for the horizon
   ends `ABANDONED` with one `PhaseTransitioned` after `RecoveryStarted`;
   a read admitted during recovery returns before the next poll; a
   second mutation admitted before the first failed is refused at
   dequeue; a read admitted while a poll holds the lock completes without
   its caller's context expiring; `Close` during recovery stops the
   timer and no `Submit` fake is called afterwards, including when the
   timer was parked in `Lock` at the moment of `Close`; the submitter's context expiring does not
   stop the loop; an `Abandon` whose audit delivery fails on the
   horizon-crossing poll succeeds on the next tick; a resumed mutation
   with no fence ends `ABANDONED` at the horizon.
4. A changed probe before `Execute` blocks with `FIRMWARE_EPOCH_CHANGED`,
   emits `flowseer.device.firmware.epoch_changed` and the
   `FirmwareEpochChanged` record, invalidates evidence, reports
   `mutation/firmware-epoch` with `submitted` false, and waits on `Done()`
   for the `REJECTED` ack; a changed probe after `Observe` does the same,
   discards the observation, fails a read with `mutation/firmware-epoch`,
   and sends a mutation into recovery; an unchanged probe labels the observation's
   provenance with the probed fingerprint. Example: fingerprint `a` at
   `AddDevice`, the probe after a read returns `b`; the read fails with
   `mutation/firmware-epoch`, the record names `a` and `b`, evidence
   recorded under `a` no longer answers `Consult`, and the next `Submit`
   admits an intent expecting `b` and a read then returns provenance `b`.
5. `Lane.Freeze` freezes the gate before any emission, emits `LaneFrozen`
   once per registered device, returns the joined delivery errors with
   the gate frozen, a repeated `Freeze` emits only the records not yet
   delivered, and `Unfreeze` resets the set. Example: a deliverer that
   fails for the second of three devices leaves `Freeze` returning an
   error and the gate frozen; a second `Freeze` with a working deliverer
   emits exactly one record; after `Unfreeze` a third `Freeze` emits
   three; a device added while frozen gets its record at `AddDevice`.
6. Every read acquires a credential through `Config.ReadCredentials` with
   the handle `TypedRead.access_policy` carries, opens its session through
   `DeviceSession.OpenSNMP` (and `OpenShell` only on fallback), and closes
   it; `AddDevice` does the same for the onboarding probe with the handle
   `DeviceSession.AccessPolicy` carries; `Deps.Submit` receives the grant
   and opens the shell from its material; a nil `ReadCredentials` defaults
   to a no-op source. Example: a fake credential source is called once per
   read and once per onboarding with the right handle, and a fake shell
   factory sees the grant's bytes on submit.

## Out of scope

The hosts, the bus, the journal, and the route signals; see the central
plan and its named follow-up. Removing `EvaluateDrift` here was planned to
leave a window with no component detecting drift, accepted because the
edge's detection was already wrong after an operator's `accept`. The window
does not exist: this plan was sequenced after the central plan's units
rather than alongside them, so central's drift poll was already live at
`13c4c949` when the removal landed, and detection passed from one component
to the other with no gap.

## Units

The central plan's U1 (the schema unit) is the shared first commit of
both plans; nothing here lands before it.

### U1. Lane contract: reporter, acknowledgement, sessions, freeze audit
Files: `src/modules/localnet/access/lane.go`, `lane_test.go`,
`internal/mutation/machine.go`, `machine_test.go`,
`internal/freeze/freeze.go`, `freeze_test.go`,
`internal/capability/interfaces/adapter.go`, `access.go`, `README.md`
After: U1 of the central plan (the `progress` arm,
`ExecuteResult.submitted`, `ExecuteRequest.resume`, and
`TypedRead.access_policy`)
Change: `Config.Reporter`, nil-safe and non-blocking; `deviceState.current`
under `stateMu`; `Machine.Acknowledge` applying at the door with the
matrix, the `cancelled`, `abandoning`, and `verified` marks, the submit
latch, `Done()`, and the terminal walks per disposition; `Execute`'s
waits under a cancellable context; `awaitCheckpoint` selecting on
`Done()`; `process` reading the machine after any step error; the
submission completed once by whoever turns the machine terminal;
`DeviceSession.OpenSNMP`, `OpenShell`, and `AccessPolicy` — no endpoint
field, contrary to this line's first draft: the host builds the factories
per device and captures the address in them, so a field here would be an
address the lane holds and never reads, and a second place for it to be
wrong;
`ReadCredentials` defaulted and used at read time, `Deps.Submit` taking
the grant; `FingerprintOverride` deleted and tests reworked to a fake
probe session; `Lane.Freeze` per its decision, with the delivered set
reset on `Unfreeze`; the lane overriding
`ProvenanceInputs.FirmwareFingerprint` from its probe, with the adapter
doc saying the facade callers set it themselves; `EvaluateDrift`,
`ManagementMode`, `internal/drift`, and `deviceState.lastIntent`
removed, `block`'s comment in `machine.go` no longer justified by
`EvaluateDrift`, and `telemetry.View.DriftDetected` deleted with them;
the README's opening paragraph, exported-surface listing, and signal
list no longer claim drift.
Tests: requirements 1, 2, 5, 6.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/**')`

### U2. Recovery polls under the drain lock
Files: `src/modules/localnet/access/lane.go`, `lane_test.go`,
`internal/recovery/recovery.go`, `recovery_test.go`,
`internal/mutation/machine.go`, `machine_test.go`, `README.md`
After: U1
Change: `Config.Wait`, `Config.RecoveryPollInterval`; the pre-mutation
read; `process` entering recovery and returning without an outcome; the
per-device timer taking `ds.draining`, checking cancellation after
acquiring, and calling `l.drain(ds)` on release, with the release
property written beside `drain`'s comment; its cancel func held on
`deviceState`, cancelled by `Close` and by an abandoning ack; the
hold re-check at dequeue; `Runner.Attempt` returning `OutcomeVerified`
ahead of the horizon check; `Machine.Retry` and `inRecovery` set by
`EnterRecovering` too; admission with `resume`; `Lane.Close` cancelling
timers and its doc and `ShutdownReport` saying so; the README's "Scope
of Lane.Submit" section rewritten to describe the loop and the
drainer's liveness during it.
Tests: requirement 3 with a fake `Wait`; the audit record count per poll
and per retry.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/**')`

### U3. Epoch re-probe and refresh
Files: `src/modules/localnet/access/lane.go`, `lane_test.go`,
`internal/mutation/machine.go`, `README.md`
After: U1
Change: `process` probes before `Execute` (alongside the pre-mutation
read) and after `Observe`, before `recordEvidence`, over a session opened
for the probe; refreshes the fingerprint under `stateMu`, invalidates
evidence, emits the event and the record, blocks or discards per the
decision; `Lane`'s doc and `machine.go`'s "nothing in this module does"
comment updated; the README's "Open gap" section becomes "Firmware epoch"
and states the two probe points and the discard rule.
Tests: requirement 4, including that evidence under the old fingerprint
no longer answers `Consult`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/**')`

## Verification

```bash
go build ./... && go vet ./...
go test -race -count=1 ./src/modules/localnet/access/...
.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/**')
```

Run these outside the agent Bash sandbox.
`internal/capability/fastiron` binds an SSH test listener and fails there
with `bind: operation not permitted`, which is the sandbox and not the
package. `-count=1` is not decoration: a cached PASS from an earlier
unsandboxed run makes that package report `ok` under the sandbox without
running a thing, so a sandboxed run with the cache warm looks green and has
tested nothing.

## Definition of done

- Verifier green for the module; the README's opening, exported surface,
  signal list, "Scope of Lane.Submit", and "Open gap" sections match the
  code.
- This plan's `status` set with an outcome note under its title; the lane
  plan's outcome note gains one line pointing here.
- At `compound`, the `ds.draining` release rule (a second acquirer must
  drain and re-check on release or it recreates the lost wakeup) is added
  as the fifth trap in
  `docs/solutions/architecture-patterns/single-active-drainer-must-not-leak-its-context-or-its-shutdown-flag.md`,
  with the `Close`-after-acquire check beside it.
- No requirement or unit labels in code, comments, or commit messages.

## Follow-ups

**A parked checkpoint wait holds the device's drain lock.** `process` runs
inside the drain loop, which holds `ds.draining` for the whole call
including `awaitCheckpoint`. So a mutation waiting for central's
`CheckpointRequest` holds that device's drain lock for as long as central
takes, and no read for that device is served in the meantime. This predates
these plans and is not what the recovery-poll unit changes — but it is the
same shape one call site over, which is why it is written down here rather
than left to be found in production.

Stated as behaviour, not as a remedy, because the remedy is a design
question this slice cannot answer: whether a read may be admitted alongside
a parked mutation at all, given the lane's per-device ordering guarantee.
Reads and mutations are already distinguished at admission (a hold refuses
mutations and lets reads through), so the answer is probably yes, but it
needs deciding rather than assuming.

**Route evidence is written and never read.** `evidence.Store.Record` has
one caller and `Consult` has none outside the store's own tests, so the
cache the lane maintains answers no question. The epoch unit wires
`InvalidateFingerprint` into the re-probe, which is correct and currently
unobservable — removing it fails no test, because nothing downstream can
tell an invalidated cache from a populated one.

It is left wired rather than removed: recording evidence under an epoch that
has changed, and remembering to invalidate it when a reader finally arrives,
is the harder thing to get right. What the follow-up owes is the reader —
`Consult` in route selection, so a route proven under the current epoch can
answer without a fresh probe — and only then does the invalidation have a
test that can fail.

Found by enumerating what writes evidence, what reads it, and what keys it,
before touching the invalidation. That enumeration is cheap and has now
turned up two dead mechanisms in this module.

## Open questions

None beyond the central plan's decisions, which this plan follows.
