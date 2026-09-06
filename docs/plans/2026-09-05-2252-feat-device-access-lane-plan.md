---
title: Device Access Lane, Routing, and Recovery - Plan
type: feat
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
---

# Device Access Lane, Routing, and Recovery - Plan

> Outcome: all eleven units landed. `Lane.Submit` (U10) drives the
> ordinary phase-by-phase path automatically; an ambiguous or failed step
> is reported as `Submit`'s own error rather than being retried through
> `internal/recovery` or resolved through `internal/drift` automatically —
> both packages are complete and independently tested, but wiring their
> retry/resolution loop into the synchronous facade needs a real
> clock-driven poll only a host with a live transport can run. See
> `src/modules/localnet/access/README.md`'s "Scope of Lane.Submit's
> automatic handling" section.
>
> Review-cycle addendum: U7's mid-operation firmware-epoch check
> (decision 7, "for the life of the operation") was implemented and then
> removed after review found it compared two values this module never
> reconciles — `CurrentFingerprint` (epoch.Probe's own digest) against an
> observation's `Provenance.firmware_fingerprint` (whatever the host's
> `ProvenanceInputs` supplied, unrelated to the probe). On the documented
> production path the two differ by construction, so the check blocked
> every mutation rather than detecting a real epoch change. This stays an
> open gap: a real check needs a fresh `epoch.Probe` run at observation
> time compared against the earlier probe's own output, which needs the
> same live transport the recovery/drift auto-wiring above is waiting on.
> See the README's "Open gap: no mid-operation firmware-epoch re-check"
> section; the central-service plan inherits both gaps together.

## Goal

Give `src/modules/localnet/access/` the edge-side runtime that sits behind
the execution envelope in `spec/proto/flowseer/integration/device/v1/`: a
bounded, ordered per-device lane that admits reads and mutations, resolves
route evidence and firmware epochs, drives the interface capability under
`internal/capability` through the mutation state machine (plan, checkpoint,
execute, observe, compare, result), recovers from ambiguity, detects and
resolves drift under both management modes, honors a control-plane freeze,
and reports every named OpenTelemetry signal plus the durable
`flowseer.event.device.v1.DeviceOperationEvent` audit record before
releasing the lane. The means: new internal packages composed behind a
`Lane` type the module's facade exposes, each internal package independently
testable against a fake of the interface it owns (central's envelope
messages, `EdgeService`'s credential RPCs, and the audit/telemetry sinks).
Stop condition: if the lab's FastIron fixture cannot supply a bounded
delayed-apply horizon for `InterfaceDescriptionChange` (the plan assumes one
is configured, not measured here), mutation stays gated behind that
configuration and this plan's recovery logic is unreachable in production
until it exists — the code and tests do not depend on measuring it.

## Decisions

- **The lane, route evidence, mutation state machine, recovery, drift, and
  freeze all live in this module, each in its own internal package,
  composed by a new `Lane` type in the module's facade.** Why: the task
  assigns them to `src/modules/localnet/access/`, and `doc.go` currently
  says a per-device lane belongs to "the central and edge processes that
  will call the facade" — that sentence predates this module becoming the
  edge process's device-access runtime and is corrected in U11 rather than
  contradicted; the accepted direction record's decisions 3, 4, 9 do not
  say which Go package implements them, only which process (central versus
  edge) owns each responsibility, and this module is the edge side.
- **Lane position is a local `uint64` counter, distinct from and unrelated
  to `MutationState.sequence`.** Why: the task requires this distinction
  explicitly; central assigns `sequence` at `ADMITTED` and it is a fact the
  edge receives on `ExecuteRequest`, never assigns. The lane position is
  purely an edge-local admission-order marker used for FIFO ordering and
  poll coalescing keys before dispatch is even known to have a central
  sequence (a lane may admit local system work, such as an identity probe,
  that never gets one).
- **Priority is a small closed enum (`PriorityHigh`, `PriorityNormal`,
  `PriorityLow`) compared only inside the admission queue's insertion, never
  again.** Why: decision 3 says priority applies at admission and never
  reorders after a sequence is assigned; a `container/heap` ordered by
  `(priority, admission time)` at insertion and FIFO by lane position
  thereafter satisfies this without a runtime resequencing path that could
  violate it. Unconfirmed default: a mutation is `PriorityNormal` and an
  operator-abandon or freeze-release re-admission is `PriorityHigh`; ordinary
  polls are `PriorityLow`. A caller may override per submission.
- **Poll coalescing dedupes on `(device, operation kind, target key)` while a
  matching poll is queued or in flight, returning the same result to every
  coalesced caller.** Why: the task requires poll coalescing and the
  existing capability layer already treats a read as idempotent evidence
  (`interfaces.Read`); coalescing at the lane avoids duplicate device
  round-trips for concurrent identical reads without inventing a new
  completeness rule. Unconfirmed: only `TypedRead` operations coalesce;
  `MutationIntent` never does, because two intents are distinguished by
  idempotency key even when they target the same field.
- **Route evidence is a bounded in-memory map keyed by `(device, firmware
  fingerprint, operation, route)` storing the route's last completeness and
  an expiry from a configured lifetime; an operation with no configured
  lifetime for its kind cannot mutate.** Why: the task states this exactly,
  and it composes with the existing `interfaces.Freshness`/`DelayedEffect`
  pattern already in `internal/capability/interfaces/completeness.go` rather
  than duplicating it — evidence answers "which route can complete this
  operation," which is a distinct question from "is this cached observation
  still fresh."
- **The firmware identity probe is a new capability-shaped function,
  `internal/epoch.Probe`, that reads identity over whichever route answers
  first among a small fixed candidate set (SNMP `sysDescr`/`sysObjectID`
  today), independent of any specific capability's route evidence.** Why:
  decision 7 requires the probe to be "route-independent" of the capability
  being gated, so it cannot reuse `interfaces.SelectRoute`, which is scoped
  to the interface capability's own completeness. An epoch change clears
  every route-evidence entry for the device and emits
  `FirmwareEpochChanged` and `discovery.completed`; it does not touch the
  lane's position counter.
- **Explicit route pins are a field the caller passes into `Lane.Submit`,
  not a proto field.** Why: no pin field exists in `MutationIntent` or
  `TypedRead` in the landed contracts, and adding one would touch a policy
  surface (the schema) beyond this plan's scope; an operator-level pin is
  therefore an edge-local override the host (central, through some future
  channel) supplies alongside the envelope message. Unconfirmed: modeled as
  `PinnedRoute route.Route` in the `Lane.Submit` options; a failed pinned
  route returns the failure as the operation's result with no fallback
  attempt, per the task.
- **`Lane.Submit`'s options carry the whole `*integrationv1.ExecuteRequest`,
  not a bare `MutationIntent`/`TypedRead`.** Why: `sequence` is central's,
  assigned before dispatch and delivered to the edge only on
  `ExecuteRequest` (`execution.proto`'s field comment: "The device's lane
  sequence this operation was admitted at"); `SubmissionCredentialSource.Open`,
  the `CheckpointRequest` match, and `DeviceOperationEvent.sequence` all need
  that value, so admission must receive it rather than the edge inventing or
  omitting one. `SubmitOptions{Request *integrationv1.ExecuteRequest, Priority,
  PinnedRoute}` is the corrected shape every later unit builds against.
- **The mutation state machine models `OperationPhase` transitions as an
  explicit typestate, not a switch on the proto enum scattered across
  functions.** Why: `MutationState`'s own CEL rules (disposition set exactly
  in three phases, `blocked_since` set exactly with `block_reason`) show the
  phase graph is a real invariant; a typestate makes an illegal transition a
  compile-time or construction-time error instead of a runtime bug matrix.
- **Checkpoint and terminal acknowledgement are driven by two small
  interfaces this package owns (`CheckpointGate`, distinct from the
  credential interfaces below) that the host feeds `CheckpointRequest` and
  `TerminalResultAck` into and reads `CheckpointAck`/`ExecuteResult` out of;
  this plan does not implement NATS or Connect transport.** Why: the task
  scopes this module to speaking the execution envelope, not to owning the
  bus; `src/edge` (out of scope here) is presumably the host that wires a
  real transport. Modeling it as a synchronous call-and-return keeps the
  state machine unit-testable without a fake broker.
- **Credential acquisition is behind two interfaces this module owns,
  `ReadCredentialSource` and `SubmissionCredentialSource`, each with one
  fake for tests; no code here imports `edgev1connect` directly outside the
  adapter that satisfies these interfaces.** Why: the task requires this,
  and the survey confirms no such interface exists anywhere in the tree
  today — the generated Connect client is the only thing to adapt.
  `SubmissionCredentialSource` models `OpenDeviceSubmission`'s server stream
  as `Open(ctx, device, binding, sequence) (grant, pulses <-chan
  *edgev1.AuthorityPulse, error)` so a fake can feed pulses without a real
  stream.
- **Recovery, drift, and freeze are internal packages that operate on the
  same typestate rather than a shared "orchestrator god object."** Why:
  each has an independent test list (ambiguous recovery, delayed effect,
  conflict block, control-plane freeze, cancellation, abandonment, both
  management modes) and independent invariants; composing them at the
  `Lane` level keeps each package's tests from needing the others' fakes.
- **The durable audit event is delivered through an injected `Deliverer`
  interface (`Deliver(ctx, *eventv1.DeviceOperationEvent) error`), and the
  state machine's phase-release step blocks on that call returning before
  the lane state that phase describes is considered released.** Why:
  decision 13 requires delivery before release; a synchronous call with a
  test fake that can inject a delay or failure is the simplest way to prove
  the ordering, and it matches decision 13's requirement that OpenTelemetry
  (unlike this call) may fail without blocking work.
- **OpenTelemetry instrumentation lives in one `internal/telemetry` package,
  built before anything that uses it, used by every other internal package
  through a `telemetry.View`-shaped parameter.** Why: the module has no
  dependency on `src/common/service` today (it is a library other things
  call), so it cannot assume it always runs inside that runtime's attempt
  context; a package-local view that the host constructs from
  `service.TracerProvider(ctx)`/`service.MeterProvider(ctx)` when it does, or
  from a caller-supplied provider otherwise, keeps the module usable outside
  a `service.Run` host. `View` builds its own instrumentation scope —
  `go.aledante.io/FlowSeer/src/modules/localnet/access`, with
  `semconv.SchemaURL` — from the injected `TracerProvider`/`MeterProvider`
  rather than taking `service.Tracer(ctx)`/`service.Meter(ctx)` directly,
  because those are scoped to the service runtime itself
  (`src/common/service/context.go`) and the observability convention
  requires a module that owns a scope to name it itself. Every non-standard
  metric attribute is namespaced `flowseer.device.*` (`flowseer.device.operation`,
  `flowseer.device.outcome`, `flowseer.device.route`, `flowseer.device.reason`);
  only `error.type` stays a bare standard key, since
  `docs/conventions/observability.md` forbids bare custom keys and an
  invented `outcome`/`reason` key is exactly that.

## Requirements

1. `lane.Queue` admits work in priority order among items still waiting at
   admission and never reorders after a position is assigned; two
   `PriorityNormal` submissions land in submission order. Acceptance: insert
   low, then high, then normal into a `Queue` before any `Dequeue` call, then
   dequeue three times; the order observed is high, normal, low. A fourth
   item submitted after the first dequeue queues behind whatever is still
   waiting regardless of its priority relative to the already-dequeued item.
2. `Lane.Submit` rejects new work once the per-device queue is at its
   configured bound, returning a typed overload error, and never silently
   drops an already-admitted mutation. Acceptance: fill the queue to its
   bound, submit one more mutation; the call returns an overload error and
   every previously admitted item is still present and later executed.
3. Two concurrent `TypedRead` submissions for the same interface on the
   same device coalesce into one device round-trip and both callers observe
   the same result. Acceptance: submit two reads for `ethernet 1/1/1`
   before either is dispatched; the fake session records exactly one `Get`
   sequence for that interface.
4. Route evidence with an elapsed lifetime is not reused to select a route,
   and an operation kind with no configured lifetime blocks mutation before
   any device contact. Acceptance: evidence recorded at `t0` with a
   two-minute lifetime is not consulted at `t0 + 3m`; a mutation kind with
   `Lifetime: 0` (unset) returns a typed "no evidence policy" error without
   calling the capability.
5. An SNMP read that is valid but incomplete falls through to SSH within
   the same operation and the caller receives one result with one
   provenance; a fallback where SSH also cannot complete surfaces the SSH
   route's own outcome, not the SNMP one. Acceptance: `interfaces.Read`'s
   existing fallback behavior is reused unmodified; a lane-level test drives
   it through `Lane` and asserts the `RouteSelected` event carries
   `fell_through: true`.
6. An explicit route pin that fails is reported as failed with no fallback
   attempted. Acceptance: pin `RouteSSH` on a device whose shell adapter
   errors; the fake SNMP session records zero calls and the operation's
   result is the SSH failure.
7. A firmware fingerprint change invalidates every route-evidence entry for
   the device, blocks a mutation admitted under the old fingerprint with
   `BLOCK_REASON_FIRMWARE_EPOCH_CHANGED`, and forces `epoch.Probe` again.
   Acceptance: admit evidence under fingerprint A, change to fingerprint B
   via a probe result, submit a mutation whose intent still names A; the
   mutation is blocked and every route-evidence entry recorded under A is
   gone. (Decision 7's further rule that central's own `sequence` is never
   reset by this is central's bookkeeping, out of scope here per "Out of
   scope"; this plan only owns the edge-local lane position, which
   `lane.Queue`'s own tests cover directly, not through this requirement.)
8. The mutation state machine only reaches `POSSIBLY_APPLIED` after this
   package emits a `CheckpointAck` for a `CheckpointRequest` naming the same
   sequence, and only submits a command after `SubmissionCredentialSource`
   has delivered a grant whose latest `AuthorityPulse` is
   `SUBMISSION_AUTHORITY_AUTHORIZED`. Acceptance: feed a `CheckpointRequest`,
   observe a `CheckpointAck` with the same sequence, then a pulse of
   `SUBMISSION_AUTHORITY_REVOKED` before the grant's deadline; the state
   machine does not call the shell adapter's `SetPortName`.
9. A connection loss after submission but before observation does not fail
   the mutation; the state machine enters `RECOVERING`, observes before any
   retry, and retries only after a device-native fence or repeated fresh
   observations across the delayed-apply horizon show the state unchanged.
   Acceptance: simulate a submit that returns a transport error; the next
   call is an observation read, not a resubmission; two fresh observations
   an interval apart both showing the pre-mutation description permit one
   retry; a single stale-then-fresh pair does not.
10. Two complete observations of the same field that disagree yield no
    authority: the state machine reports `BLOCK_REASON_CONFLICTING_READS`
    rather than treating either as verified. Acceptance:
    `interfaces.ConflictingReads` (existing) drives a lane-level test where
    a cached complete observation and a fresh complete observation disagree
    on `description`; the mutation blocks instead of verifying.
11. An authorized cancellation during recovery abandons the mutation
    (`ABANDONED`, `DISPOSITION_INDETERMINATE_ABANDONED`,
    `BLOCK_REASON_RECOVERY_HOLD`) and the hold is cleared only by an
    explicit resolution call this package exposes (`AcceptObserved`,
    `RestoreExpected`, or `Replace`), never by a retry. Acceptance: cancel
    during `RECOVERING`; the lane stays blocked across a subsequent
    `Lane.Submit` attempt for the same device until one resolution call
    runs.
12. Under `DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED`, a managed field
    observed changed with no admitted mutation explaining it blocks the
    lane with `BLOCK_REASON_DESYNCHRONIZED` and waits for a resolution call.
    Under `DEVICE_MANAGEMENT_MODE_AUTHORITATIVE`, the same observation
    admits an ordinary reconciliation `SystemActor` intent instead of
    blocking. Acceptance: feed an out-of-band observation whose description
    differs from the last known expected value under each mode; assert the
    block in one and the auto-admitted intent in the other.
13. A control-plane freeze stops new admission outright and pauses (never
    fails or disposes) a checkpointed mutation's submission step until
    `Unfreeze`, while an already-checkpointed mutation whose observation is
    already known still reaches its terminal acknowledgement while frozen.
    Acceptance: freeze the lane after a mutation reaches `VERIFIED` but
    before `ACKNOWLEDGED`; feed the `TerminalResultAck`; the lane still
    reaches `RELEASED` for that mutation. Separately, freeze the lane after
    `CheckpointAck` but before submission for a second mutation; `Execute`
    blocks (no command sent, no phase transition, no disposition) until
    `Unfreeze`, then proceeds normally; a concurrent new `Submit` call is
    rejected until `Unfreeze`.
14. Cancellation delivered between `CheckpointAck` and the submission call
    is honored (the command is never sent) and cancellation delivered after
    submission is not (the state machine proceeds to observe, per
    requirement 9's ambiguity rule). Acceptance: cancel the context
    immediately after `CheckpointAck` returns; the shell adapter records no
    call; cancel it immediately after a successful submit; the state
    machine still calls the observation read.
15. The `DeviceOperationEvent` for a phase's terminal step is delivered
    (the fake `Deliverer` observes the call) before `Lane` reports that
    phase's state as released to a caller polling status. Acceptance: a
    `Deliverer` fake that blocks until signaled proves the state is not
    yet visible as released while the call is blocked, and becomes visible
    immediately after it returns.
16. Onboarding a device with no prior evidence runs `epoch.Probe`, records
    `discovery.completed`, and only then allows the first mutation.
    Acceptance: `Lane.Submit` for a mutation on a device with empty evidence
    triggers exactly one probe call before the mutation's own route
    resolution.
17. An exporter failure does not block or fail any operation; only the
    `Deliverer` failure in requirement 15 can. Acceptance: a `View` built
    from a real SDK `TracerProvider`/`MeterProvider` whose configured
    exporter always returns an error still lets a full mutation reach
    `RELEASED`; the span and metric point are simply never exported.
18. `Lane.Close` (or context cancellation of the lane's run loop) stops
    admitting new work, waits for in-flight operations up to a configured
    deadline, and returns rather than blocking forever if a fake `Deliverer`
    or credential source never responds. An operation whose audit delivery
    has not completed when the deadline elapses is reported as undelivered
    and is never marked released (consistent with requirement 15); "undelivered"
    is deliberately not "abandoned," which names a distinct terminal
    disposition (`OPERATION_PHASE_ABANDONED`) this is not. Acceptance: a
    `Deliverer` fake that never returns still lets `Close` return once its
    deadline elapses, reporting that operation as undelivered and not
    released.

## Out of scope

- NATS or Connect transport wiring; the checkpoint/terminal-ack and
  credential interfaces are consumed, not implemented, by this plan.
- The `DeviceService` (central, operator-facing) implementation; this
  module is the edge side of `integration/device/v1` only.
- Positive fencing's actual mechanism (decision 8's device/network/session/
  host fence). This plan's recovery package takes a fence result as an
  input (a boolean-ish `Fenced` value from an injected checker) rather than
  implementing lease or STONITH logic itself; the single-edge-per-integration
  deployment rule makes this plan's freeze package (control-plane freeze on
  stale contact) the only fencing behavior this module needs to ship now.
- A second firmware or shell DSL; `fastiron` remains the only adapter.
- Any change to the landed proto contracts under `spec/proto/flowseer/`.
  Every message this plan consumes already exists.
- Measuring the real delayed-apply horizon and recovery bounds on the lab
  ICX7150; this plan's `DelayedEffect` and fence inputs stay caller-supplied
  configuration, consistent with `interfaces.DelayedEffect` today.

## Units

### U1. Route evidence store and firmware epoch
Files: `src/modules/localnet/access/internal/evidence/{doc.go,store.go,store_test.go}`,
`src/modules/localnet/access/internal/epoch/{doc.go,probe.go,probe_test.go}`
After: none
Change: `evidence.Store` holds `(device, firmware fingerprint, operation,
route) -> {completeness, expiresAt}` with a per-operation-kind configured
`Lifetime`; `Store.Consult` returns "no policy" when the kind has no
configured lifetime and "stale" when elapsed; `Store.Record` and
`Store.InvalidateFingerprint` implement requirement 4 and 7's clearing.
`epoch.Probe` reads identity over a small fixed SNMP candidate set,
independent of `interfaces.SelectRoute`, and returns the fingerprint plus
whether it differs from the store's last-known one for the device.
Tests: lifetime expiry, missing-policy block, fingerprint invalidation
clearing every route for a device, probe route-independence (probe
succeeds when the interface capability's own SNMP route would not, using a
minimal fake session for just `sysDescr`/`sysObjectID`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/evidence src/modules/localnet/access/internal/epoch`

### U2. Lane admission and poll coalescing
Files: `src/modules/localnet/access/internal/lane/{doc.go,queue.go,queue_test.go,coalesce.go,coalesce_test.go}`
After: none
Change: `lane.Queue` is a bounded per-device admission structure ordered by
`(Priority, admission time)` at insertion, assigning a monotonic local
`Position` on admission that never changes after; `Submit` returns a typed
overload error at capacity without dropping existing entries. `coalesce.Key`
computes `(device, operation kind, target)` and a coalescing map returns an
existing in-flight ticket for a matching `TypedRead` instead of admitting a
second one.
Tests: priority ordering across mixed priorities, FIFO among equal
priority, overload rejection preserves existing entries, poll coalescing
returns the same ticket and result to two callers, a `MutationIntent` never
coalesces even with an identical interface target.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/lane`

### U3. Credential source interfaces
Files: `src/modules/localnet/access/internal/credential/{doc.go,source.go,source_test.go,connect_adapter.go}`
After: none
Change: `ReadCredentialSource` (one method wrapping
`AcquireReadCredential`) and `SubmissionCredentialSource` (`Open(ctx,
device, binding, sequence) (*edgev1.SubmissionGrant, <-chan
*edgev1.AuthorityPulse, error)`) as the interfaces this module owns;
`connect_adapter.go` adapts `edgev1connect.EdgeServiceClient` to both,
translating the server stream into the channel shape and closing it on
context cancellation. Fakes for both interfaces live in `source_test.go`'s
test helpers (exported via an internal test-only file, or duplicated per
`interfaces`' own `fake_session_test.go` precedent) for later units to
import from `internal/mutation`'s tests — package-private, so `mutation`'s
tests build their own fakes satisfying the same interfaces rather than
importing test code across packages.
Tests: adapter translates a real `OpenDeviceSubmissionResponse` stream's
first message to a grant and subsequent messages to pulses; adapter surfaces
a stream error as a channel close plus a returned error captured before the
channel closes; context cancellation stops the adapter's goroutine (no
leaked goroutine, asserted with a done-channel).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/credential`

### U4. OpenTelemetry instrumentation
Files: `src/modules/localnet/access/internal/telemetry/{doc.go,view.go,events.go,spans.go,metrics.go,telemetry_test.go}`
After: none
Change: `telemetry.View` bundles a `trace.Tracer`, `metric.Meter`,
`*slog.Logger`, and `propagation.TextMapPropagator`, each built by the host
from its own `TracerProvider`/`MeterProvider` (via `service.TracerProvider(ctx)`/
`service.MeterProvider(ctx)` when run under `src/common/service`, or a
directly supplied provider otherwise) with this module's own instrumentation
scope name and `semconv.SchemaURL` — never `service.Tracer(ctx)`/
`service.Meter(ctx)` directly, which are scoped to the service runtime
itself. Every other internal package takes a `telemetry.View` rather than
reading a global. `events.go` emits the nine named events
(`flowseer.device.route.selected`, `flowseer.device.route.fallback`,
`flowseer.device.discovery.completed`, `flowseer.device.firmware.epoch_changed`,
`flowseer.device.recovery.started`, `flowseer.device.drift.detected`,
`flowseer.device.lane.frozen`, `flowseer.device.lane.blocked`,
`flowseer.device.lane.released`) as `otel.event.name`-tagged log records per
the observability convention. `spans.go` starts `flowseer.device.route`
(CLIENT — the actual outbound SNMP/SSH call to the device) and
`flowseer.device.operation` (INTERNAL — the bounded admission-to-result
stage that is not itself a remote call), the route span a child of the
operation span on the ordinary path and linked rather than parented across a
recovery retry boundary, with the extract-before-policy rule from the
trace-propagation solution when the envelope carries a `traceparent`.
`metrics.go` defines a `flowseer.device.operation.duration` histogram
(unit `s`) and a `flowseer.device.route.selections` counter (unit
`{selection}`), each capped to at most two attributes drawn only from
`{flowseer.device.operation, flowseer.device.outcome, flowseer.device.route,
flowseer.device.reason, error.type}` — every non-standard key namespaced,
per the observability convention's ban on bare custom keys.
Tests: every metric recording call asserts its attribute set has at most two
keys, each from the allowlist and each `flowseer.*`-namespaced except
`error.type` (a table-driven test enumerating every call site); event and
span names match the task's literal list via a constants table; a `View`
built from a real SDK `TracerProvider`/`MeterProvider` whose exporter always
errors still lets a full mutation reach `RELEASED` (requirement 17); a trace
propagated through a `traceparent` on `ExecuteRequest` links
`flowseer.device.route` to it per the durable-hop rule.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/telemetry`

### U5. Control-plane freeze
Files: `src/modules/localnet/access/internal/freeze/{doc.go,freeze.go,freeze_test.go}`
After: U4
Change: `freeze.Gate` exposes `AllowSideEffect() bool` and
`AllowAcknowledgement() bool` (the latter always `true` — the acknowledgement
barrier is never gated) plus `AwaitSideEffect(ctx) error`, which blocks until
`AllowSideEffect()` is true, `ctx` ends, or `Unfreeze` is called, so a caller
in `mutation.Execute` (U7) pauses at the submission step while frozen instead
of failing or transitioning phase — freezing manufactures no terminal
disposition for work it merely delays. `Freeze`/`Unfreeze` are idempotent and
each transition emits `flowseer.device.lane.frozen`/`.lane.released` via the
injected `telemetry.View`.
Tests: `AwaitSideEffect` blocks while frozen and returns immediately once
`Unfreeze` is called or immediately if never frozen; `ctx` cancellation while
blocked returns its error rather than hanging; double-freeze and
double-unfreeze are no-ops and emit the event once each; `AllowAcknowledgement`
is always `true` regardless of freeze state.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/freeze`

### U6. Audit event construction and pre-release delivery
Files: `src/modules/localnet/access/internal/audit/{doc.go,event.go,event_test.go}`
After: none
Change: `audit.Deliverer` interface (`Deliver(ctx,
*eventv1.DeviceOperationEvent) error`); `audit.Build*` functions construct
each of the nine `detail` kinds from typed inputs (`BuildPhaseTransitioned`,
`BuildLaneBlocked`, ..., `BuildLaneFrozen`), filling `event_id` fresh,
`occurred_at` from an injected clock, and bounded `attributes`/
`correlation_ids` (idempotency key, trace id if present) within the proto's
`max_pairs` limits. This package has no dependency on `mutation`; U7 takes an
`audit.Deliverer` as a constructor input and calls it from its own release
step, rather than this unit amending mutation code after the fact.
Tests: each `Build*` function against its message's `buf.validate` rules
(field presence, bounded maps) using the generated validators; a fake
`Deliverer` returning an error, used by U7's requirement 17/15 tests.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/audit`

### U7. Mutation state machine
Files: `src/modules/localnet/access/internal/mutation/{doc.go,state.go,state_test.go,checkpoint.go,checkpoint_test.go}`
After: U1, U3, U4, U5, U6
Change: a typestate over `accessv1.OperationPhase` with one constructor per
legal entry (`Admitted(req *integrationv1.ExecuteRequest, deps)`, taking the
whole `ExecuteRequest` so `sequence` arrives with the work — see the
Decisions section's `SubmitOptions` fix) and one method per legal transition
(`Checkpoint(CheckpointRequest) (CheckpointAck, error)`,
`Execute(ctx) error`, `Observe(ctx) (*InterfaceObservation, error)`,
`Compare() Disposition`, `Result() *ExecuteResult`), each rejecting a call
out of order with a typed programming-error rather than silently
transitioning. `deps` bundles a `telemetry.View` (U4), a `freeze.Gate` (U5),
and an `audit.Deliverer` (U6) as constructor inputs, so freeze and audit
integration are part of the type from the start rather than a later
amendment. `Execute` first calls `freeze.Gate.AwaitSideEffect(ctx)`, then
`SubmissionCredentialSource.Open`, checks the latest pulse is `AUTHORIZED`
before calling the capability's `SetDescriptionChange`/shell adapter, and
stops (no call) if the pulse is `REVOKED` or the context is canceled before
submission. Route resolution inside `Execute`/`Observe` consults
`evidence.Store` first, honors an explicit pin carried on `SubmitOptions`
(the facade, U10) with no fallback on a pinned failure, and otherwise calls
`interfaces.SelectRoute`/`Read`. The release step calls
`audit.Deliverer.Deliver` and blocks on its return before reporting a phase
released (requirement 15).
Tests: full happy path phase-by-phase; checkpoint-then-revoked-pulse blocks
submission; cancellation before submission blocks it, cancellation after
does not (requirement 14); conflicting reads yield `BLOCK_REASON_
CONFLICTING_READS` (requirement 10); pinned route failure with no fallback
(requirement 6); calling a transition out of order returns the typed error
without panicking; `Execute` blocks on a frozen `Gate` and proceeds once
unfrozen with no phase transition recorded meanwhile (requirement 13); a
`Deliverer` error at release leaves the reported phase at its last durable
value, not `RELEASED` (requirement 17's audit half).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/mutation`

### U8. Recovery and abandonment
Files: `src/modules/localnet/access/internal/recovery/{doc.go,recovery.go,recovery_test.go}`
After: U4, U7
Change: `recovery.Runner` takes a state-machine handle in `RECOVERING`, an
injected `Fenced func() bool` (decision 8's positive-fence input, out of
scope to implement here), and `DelayedEffect`; `Observe` is always called
before any retry attempt; `MayRetry` reports true only when `Fenced()` is
true or when two observations at least the configured interval apart both
still show the pre-mutation state within the horizon; `Abandon(actor)`
transitions to `ABANDONED`/`DISPOSITION_INDETERMINATE_ABANDONED`/
`BLOCK_REASON_RECOVERY_HOLD` and is only reversible through
`AcceptObserved`, `RestoreExpected`, or `Replace`.
Tests: observe-before-retry ordering (a spy proves `Observe` precedes any
resubmission call); fence-authorized immediate retry; two fresh
observations across the horizon permit retry, one stale one does not
(requirement 9); cancellation mid-recovery abandons and the hold survives a
later unrelated `Lane.Submit` for the same device until resolved
(requirement 11).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/recovery`

### U9. Drift detection and management modes
Files: `src/modules/localnet/access/internal/drift/{doc.go,drift.go,drift_test.go}`
After: U4
Change: `drift.Evaluate(observed *accessv1.InterfaceObservation, lastIntent
*accessv1.InterfaceDescriptionChange, mode inventoryv1.DeviceManagementMode)
(*Outcome)` compares a fresh observation against the last *acknowledged
intent* — reusing `interfaces.DescriptionApplied`'s comparison rather than
fabricating a synthetic "expected observation" the device never produced,
since `InterfaceObservation` has required, CEL-constrained fields
(`completeness`, provenance) that no invented value can honestly satisfy —
with no in-flight mutation explaining a difference. Under `OPERATOR_MANAGED`
it returns a block outcome (`BLOCK_REASON_DESYNCHRONIZED`); under
`AUTHORITATIVE` it returns an auto-admit outcome carrying a `MutationIntent`
built with a `SystemActor{reason: SYSTEM_REASON_RECONCILIATION}` restoring
`lastIntent`'s value, for the caller (`Lane`) to submit at `PriorityHigh`.
Tests: no drift when observation matches `lastIntent`; block outcome under
`OPERATOR_MANAGED` (requirement 12); auto-admit outcome under
`AUTHORITATIVE` with the reconciliation intent's fields asserted; an
in-flight mutation on the same field suppresses drift (it is the mutation's
own observation, not drift); a `PARTIAL` observation never drifts (partial
carries no authority, per `interfaces.ConflictingReads`'s doc comment).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/internal/drift`

### U10. Lane orchestrator and facade
Files: `src/modules/localnet/access/lane.go`, `src/modules/localnet/access/lane_test.go`,
edits to `src/modules/localnet/access/access.go`
After: U1, U2, U3, U4, U5, U6, U7, U8, U9
Change: `Lane` composes U1-U9 behind `NewLane(Config) *Lane`,
`Submit(ctx, SubmitOptions) (Ticket, error)`, `HandleCheckpoint`,
`HandleTerminalAck`, `Freeze`/`Unfreeze`, `Status(device) LaneStatus`, and
`Close(ctx) (ShutdownReport, error)` (requirement 18's bounded shutdown).
`Config` carries every fake-able dependency (credential sources, evidence
lifetimes, telemetry view, deliverer, clock) with keyed-literal construction
per `src/common/service`'s extensible-struct convention. This is the only
new exported surface; every `internal/*` package stays internal.
Tests: end-to-end ordering (requirement 1), overload (requirement 2), poll
coalescing (requirement 3), onboarding (requirement 16), bounded shutdown
under a hung deliverer (requirement 18); one test per named scenario in the
task description that is not already covered at the unit level, wired
through `Lane` so the facade itself is proven, not just its parts.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access`

### U11. Documentation: doc.go, package README, and metric cardinality
Files: `src/modules/localnet/access/doc.go`, `src/modules/localnet/access/README.md` (new)
After: U10
Change: `doc.go`'s architecture paragraph is corrected: the lane, journal
interaction, and recovery now belong to this package's `Lane` type, citing
decisions 3, 4, 5, 6, 7, 8, 9, 13; the capability packages remain the
protocol-agnostic read/write primitives `Lane` drives. `README.md` documents
the module's exported surface, the internal package map, and — per the
task's explicit requirement — a cardinality table per metric: its name,
attributes, each attribute's allowed value set, and the resulting
worst-case series count per device.
Tests: none (docs); `verify-change` still runs to catch doc-style and lint
issues in the changed files.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access/doc.go src/modules/localnet/access/README.md`

## Verification

```
go test -race ./src/modules/localnet/access/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access
```

No lab device is touched; every test in this plan runs against fakes for
SNMP sessions, shell adapters, credential sources, checkpoint/terminal-ack
input, and the audit deliverer.

## Definition of done

- [ ] `go test -race ./src/modules/localnet/access/...` passes.
- [ ] `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/localnet/access` passes.
- [ ] Every requirement above has at least one passing test named in a unit.
- [ ] `doc.go` and `README.md` updated in the same change (U11).
- [ ] This plan's `status` set to `implemented` (or `partially-implemented`
      with an outcome note) once the units land.
- [ ] No unit label (U1, U2, ...) appears in code, comments, or commit
      messages.

## Open questions

- Whether priority has exactly three levels or should be an open int scale
  is unconfirmed; three fixed levels are assumed because nothing in the
  direction record or the landed contracts names more, and a closed enum is
  easier to bound in telemetry cardinality.
- Whether poll coalescing should also apply across two different callers
  requesting overlapping but not identical interface sets (e.g., "all
  interfaces" vs. one interface) is unconfirmed; this plan coalesces only
  exact target-key matches and leaves broader coalescing for a later plan
  if measurement shows it matters.
- The exact configured values for route-evidence lifetime, delayed-apply
  horizon, and recovery observation interval are left as `Config` fields
  with no shipped default beyond "unset blocks mutation" (requirement 4);
  the lab-measured values belong to whichever plan wires this module to the
  real FastIron fixture, not this one.
