---
title: Central Device Service, Journal, and Hosts - Plan
type: feat
date: 2026-09-06
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-09-05-verified-device-access-direction.md
---

# Central Device Service, Journal, and Hosts - Plan

Slice 1 item 6 of
[the contracts plan](2026-09-05-1709-feat-verified-local-device-access-plan.md):
the central journal on JetStream, the durable audit stream, the outbox
that re-sends what central owes an edge over the edge's own dispatch
stream, and the two hosts that assemble `src/modules/localnet/access`
(edge) and the operator API (central). It
inherits the gaps the
[lane plan](2026-09-05-2252-feat-device-access-lane-plan.md) and the access
module README document; every one of them is decided below, none silently.
The units that change the lane itself are the
[lane host contract plan](2026-09-06-1404-feat-device-access-lane-host-contract-plan.md),
which this plan's edge host unit depends on; the decisions stay here.

## Goal

An operator calls `ApplyInterfaceDescription` on central, central records the
intent durably, dispatches it to the enrolled edge that hosts the device's
lane, the edge writes the description over SSH, verifies it by a fresh read,
central acknowledges the verified disposition, and `GetDeviceAccessStatus`
shows `unresolved` unset, `high_watermark` at or past that sequence
(reads share the counter), and the interface row carrying the observation
that proved it. The audit stream
holds every phase transition, and a central restart between the checkpoint
and the result loses nothing. The means: a per-device lane record in a
JetStream key-value bucket written by compare-and-set, a JetStream audit
stream central alone writes after the edge delivers each record over
Connect, an outbox derived from the record and delivered over the dispatch
stream the edge holds open, and two `src/common/service` hosts.
Stop if `jetstream.KeyValue.Update` cannot express one writer per device
across central replicas (it can: `ErrKeyRevisionMismatch`,
`~/go/pkg/mod/github.com/nats-io/nats.go@v1.53.1/jetstream/kv.go:123-129`),
because the journal would then need a lease this plan does not contain.
Stop also if the hub's source stream cannot pull from an edge stream in
another JetStream domain across a leaf link that drops and returns:
`StreamSource.Domain` exists for exactly that
(`~/go/pkg/mod/github.com/nats-io/nats.go@v1.53.1/jetstream/stream_config.go:406-409`,
"configure a stream source in another JetStream domain") and the accepted
record cites the hub source stream pattern, but neither is proof over an
intermittent leaf; U2's reconnect test is the first evidence, and an
implementer who finds it does not hold stops rather than inventing a
forwarder.

## Decisions

Inherited gaps, each placed:

- **Recovery polls under the device's drain lock between queued items,
  timed by a host-injected `Config.Wait`.** Lane plan U2. Why: decision 3
  makes recovery part of the device's one order, which the single active
  worker keeps; a drainer parked for the horizon would serve no reads,
  and a poll as a queue item can be refused by a full queue on the tick
  that would abandon. `recovery.Runner` gains `OutcomeVerified` and
  `Machine` gains `Retry` (`RECOVERING` to `POSSIBLY_APPLIED`; `Execute`
  opens a fresh grant, which central's gate admits because the record's
  phase stays `POSSIBLY_APPLIED`), so `RECOVERING` has non-abandon exits.
  A pre-mutation read before `Execute` is the corroboration baseline.
- **Firmware epoch is re-probed probe against probe, in `process`, before
  `Execute` and after every successful `Observe`; the fingerprint is
  refreshed in place.** Lane plan U3. Why: the README's analysis holds, and
  the periodic read central dispatches keeps it current with no
  re-`AddDevice`. A change rewrites `deviceState.fingerprint`, calls
  `InvalidateFingerprint`, and emits the telemetry event and the
  `FirmwareEpochChanged` record; before `Execute` it also blocks with
  `FIRMWARE_EPOCH_CHANGED`, reports the error, and waits for central's
  `REJECTED` ack. The lane writes `Provenance.firmware_fingerprint` from
  its probe, overriding the host's `ProvenanceInputs` value; the facade
  functions in `access.go` keep taking it, since they run no probe. Central
  learns the value from every report's observation provenance and from the
  edge's `onboarded` report after `AddDevice`, never from the audit stream.
- **The `PhaseTransitioned` pair per recovery poll goes.** Lane plan U2.
  Why: a re-observation from `RECOVERING` stays there, and 720 records per
  unreachable device is a cost of the stream this plan creates.
- **`Acknowledge` accepts only what its phase allows, set under one lock,
  and a mutation waits for its ack from admission to release.** Lane plan
  U1. `VERIFIED` takes `VERIFIED`; every open phase takes
  `INDETERMINATE_ABANDONED`, which is how `AbandonMutation` reaches the
  edge and which abandons wherever the mutation rests, since abandonment
  is central's authority and abandoned work stays abandoned (decision 5);
  `ADMITTED`, and `POSSIBLY_APPLIED` while `Machine` records that
  `Submit` has not run, take `REJECTED`, after which the machine moves
  through `ACKNOWLEDGED` to `RELEASED`. Today `HandleTerminalAck` only
  finds a waiter after `MarkVerified` (`lane.go:547-611`); the ack is now
  applied at the door by `Machine.Acknowledge` under the machine's own
  lock, and every rest point, the recovery timer included, watches the
  machine's `Done()`. Why: the machine alone knows whether a side effect
  happened, central must be able to end any open mutation, and a stored
  ack would have to be re-validated when the phase moves and re-armed on
  every exit; applying at once leaves nothing to store.
- **`LaneFrozen` gets a producer.** Lane plan U1. `Lane.Freeze` freezes
  first, emits one record per device outside any lock, tracks delivery
  per device so a repeated `Freeze` emits only what is missing, and
  `Unfreeze` resets the tracking. Why: a fence must leave a record for
  item 7's proof, the fence is called when the audit path is likely
  failing, and the deliverer must not run under the barrier lock.
- **Route signals and the telemetry polish go to a follow-up plan, "access
  module route signals and telemetry polish".** Why: the route span's
  boundary across a retry is a design choice of its own, the route is
  already on every provenance, and none of it gates the proof.
- **The freeze-after-unfreeze announce race stays.** The edge host (U8)
  calls `Freeze` and `Unfreeze` from one goroutine; the fix joins the
  follow-up.

Design decisions:

- **Drift is central's.** Central dispatches the periodic `TypedRead`,
  compares against the expected description it holds, emits the
  `DriftDetected` audit record and the `flowseer.device.drift.detected`
  event from its own scope, and records a `SystemActor{RECONCILIATION}`
  intent: held at `ADMITTED` with `DESYNCHRONIZED` under
  `OPERATOR_MANAGED`, dispatched under `AUTHORITATIVE`.
  `ResolveDesynchronization` acts on that sequence: `accept` disposes it
  and rewrites the expectation; `restore` admits the expected description
  as a new `SystemActor{RECONCILIATION}` intent at the next sequence and
  returns it, which serves the drift path and the abandonment path with
  one rule; `replace` admits the carried intent the same way. Disposing a
  held intent means `REJECTED` through `ACKNOWLEDGED` to `RELEASED` in the
  same CAS write as the admission, so the record never fails
  `disposition_matches_phase`; an abandoned sequence is already terminal
  and only its hold is resolved. Drift does not evaluate while any
  mutation holds the lane, abandoned included; the record holds one
  `MutationState`. `Lane.EvaluateDrift`,
  `Config.ManagementMode`, and `internal/drift` are removed, a package the
  lane plan landed the day before; the pre-stability rule in `AGENTS.md`
  asks for exactly that when a landed shape is wrong. Why: the mode and
  the expected state are central facts, only central assigns sequences,
  and the edge's `lastIntent` goes stale the moment an operator chooses
  `accept`, which would report every later read as drift. Decision 6 of
  the verified-access record fixes the modes' semantics, not the detector.
- **The lane reports through a host `Reporter`.** `Reported(*ExecuteResult)`
  fires at `ADMITTED`, at `VERIFIED` before the ack wait, on entering
  `RECOVERING`, at `RELEASED`, at `ABANDONED`, and on error;
  `CheckpointAcked` fires after `Machine.Checkpoint`; `HoldResolvedAcked`
  fires after `ResolveHold`. A coalesced read reports once per joiner, each
  a copy carrying that joiner's sequence. `Submit` still returns the final
  result. The methods return at once: the edge host's `Reporter` hands
  each report to its own re-send queue, and the Connect call to central
  happens from that queue, never on the lane's goroutine. Why: `process` waits for the ack before returning, the
  `CheckpointAck` is discarded today, and a joiner's result otherwise
  carries the owner's sequence (`lane.go:374-385`). The envelope README
  gains: an edge may report more than once per sequence, and central
  applies reports in phase order.
- **`HoldResolved{sequence}` and `HoldResolvedAck{sequence}` join the
  envelope.** Why: every resolution arm needs the edge's hold cleared
  before the next dispatch, and the sender needs a confirmation to stop.
- **Journal: bucket `device-lanes`, one key per device, value
  `DeviceLaneRecord`, every write `Update` with the revision read.** The
  record holds the high watermark, the open `MutationState` with its
  admission time, a durable `dispatched` bit set by the first `ADMITTED`
  report for that sequence, the open mutation's last reported phase
  (reads never touch it), whether the dispatch, the checkpoint, and the
  hold resolution were confirmed, `open_reads` (a map from interface name
  to sequence, `TypedRead`, deadline, and, once closed, the observation
  or the error, which the waiter reads before the entry is removed on
  the next write) the drift poll and `ReadInterface` write, a poll
  skipping an interface whose entry is still open, the expected
  description and last complete observation per managed interface (a
  read of an unmanaged interface leaves no lasting row beyond its closed
  entry), the fingerprint, and the last 64 idempotency keys. A CAS
  retry budget exhausted surfaces as a retryable `errs` code to the
  caller, never as success. Reads draw
  from the same counter, so every read is two KV writes (open, close).
  The budget is per managed interface, not per device: two writes per
  managed interface per `DriftInterval` (five minutes), each rewriting a
  record of roughly 3 KiB plus 1 KiB per managed interface. One managed
  interface is 576 writes a day of a few KiB; 5 000 managed interfaces
  across a fleet is about 34 writes a second, about 15% of one disk's
  fsync budget at the 4.5 ms per durable publish the bus-durability
  solution measured under `SyncAlways`; a 48-port switch with all 48
  managed costs 48 times one interface, about 50 KiB per write, and a
  deployment sizes its poll interval against that count. Accepted because the alternative gives
  reads a second order outside decision 3. `AbandonMutation` writes
  `ABANDONED` with `INDETERMINATE_ABANDONED` and `RECOVERY_HOLD` at once,
  which is the response the API requires; the edge's own abandonment
  follows the ack.
  Why: decision 4 needs a durable write before any side effect and one
  writer per device; revision CAS gives both without a lease.
- **The outbox is derived per row, each row with its own confirmation
  and its own negative confirmation, and rows are independent.** A
  mutation row and read rows may be owed at the same time; nothing
  ranks them. The `ADMITTED` report's own CAS write confirms the dispatch,
  sets `dispatched`, and moves `MutationState.phase` to
  `POSSIBLY_APPLIED` in the same write: that is decision 4's durable
  record that the command may reach the device, it is what makes the
  `CheckpointRequest` row owed atomically with the confirmation, and,
  because the phase only advances, "the phase ever reached
  `POSSIBLY_APPLIED`" is readable from the KV value alone on any replica.
  Rows: a resolved hold not yet confirmed owes `HoldResolved` until
  `HoldResolvedAck` (`Lane.ResolveHold` succeeds on a device with no
  hold, so the ack always comes). An open mutation with no disposition,
  whose dispatch is not confirmed, and whose block reason is none of
  `DESYNCHRONIZED`, `RECOVERY_HOLD`, `EDGE_STALE` owes `ExecuteRequest`,
  with `resume` set exactly when the phase is past `ADMITTED` and the
  original admission time carried; a recovering record
  (`INDETERMINATE`) therefore still owes its resume dispatch after an
  edge restart. A mutation at `POSSIBLY_APPLIED` whose checkpoint is not
  confirmed owes `CheckpointRequest` until `CheckpointAck`, or a
  `Refused` with `access/no-pending-wait` while that mutation's last
  reported phase is at or past `POSSIBLY_APPLIED` (a lost ack, not a lost
  checkpoint; the same refusal at `ADMITTED` leaves the row owed). A
  terminal disposition on a mutation whose `dispatched` bit is set owes
  `TerminalResultAck` until the edge reports `RELEASED` or `ABANDONED`,
  or refuses because it holds no machine or the machine is already
  terminal. For a released disposition any of those closes the record,
  advancing the watermark and clearing the `MutationState`; for an
  abandonment they confirm the ack (the last reported phase becomes
  `ABANDONED`) and the mutation stays in the record with `RECOVERY_HOLD`
  until `ResolveDesynchronization` clears it, since the operator resolves
  by that sequence. A `Refused` answering a
  `TerminalResultAck` never re-disposes the mutation, whatever its code.
  A disposal that owes no `TerminalResultAck` (`dispatched` unset: a held
  intent, a terminal refusal before admission, `AbandonMutation` on an
  intent whose edge never came back) walks `REJECTED` or
  `INDETERMINATE_ABANDONED` through `ACKNOWLEDGED` to `RELEASED` in the
  same write, so the lane is never blocked with nothing owed. Each
  `open_reads` entry owes its `ExecuteRequest` until the read's result or
  error report closes it; a row past its deadline is closed with a
  deadline error in the same CAS write. A `TerminalResultAck` is never
  owed for a held or never-dispatched intent. `Refused` names the dispatch
  kind beside the sequence and the code, because two rows for one
  sequence can be owed at once and a sequence alone would not say which
  row the refusal answers; the kind is what makes a row-level negative
  confirmation expressible. `Onboarded` clears the
  dispatch and checkpoint confirmations for the device and nothing else
  (the hold row derives from the pending resolution alone), so the rows
  above re-derive the resume dispatch and the terminal
  ack from the record alone; an edge restarted while parked before its
  checkpoint therefore resumes into recovery for a command it never sent
  and abandons at the horizon, the conservative direction and the common
  outcome of an edge restart. `Refused` carries the lane's code and the
  code decides, with an unlisted code leaving the row owed: retryable
  are `access/lane-closed`, `lane/overload`, `access/desynchronized` (a
  condition central itself clears through the `HoldResolved` row, which
  may land after the dispatch), `access/unknown-device` while the
  registry lists the device, and a context deadline; terminal are
  `mutation/firmware-epoch` and `access/unknown-device` for a device the
  registry does not list, which dispose the mutation `REJECTED`. These
  codes are wire contract: the edge's lane module emits them and central
  classifies by them, so a change to one side must change the other, or
  the U9 code-equality test fails. A
  retryable row does not self-terminate: an edge that never returns
  leaves it owed until the operator ends it with `AbandonMutation`, the
  asymmetry with `open_reads`, which expire on their deadline. The
  submission gate reads `MutationState.phase`; a `RECOVERING` report and
  an error report with `submitted` true both set only `block_reason`
  `INDETERMINATE` and the last reported phase and leave `phase` at
  `POSSIBLY_APPLIED`, so a retry's fresh grant opens. Why: no dual write,
  nothing is lost while no stream is open because nothing leaves the
  record, every row stops or names its terminator, a lost ack never
  strands a row, and a crash between submit and report resumes into
  recovery instead of submitting twice (decision 4).
- **Every request and response is ConnectRPC; the bus carries
  observability only.** Central to edge: `DispatchService.Subscribe` in
  `integration/device/v1`, one server stream per edge the edge opens with
  its assertion and holds open, reconnecting with backoff; each
  `Dispatch` names its device by id string and carries one of
  `ExecuteRequest`, `CheckpointRequest`, `TerminalResultAck`,
  `HoldResolved`. Edge to central: `DispatchService.Report`, unary, with
  the device id and one of `ExecuteResult`, `CheckpointAck`,
  `HoldResolvedAck`, `Refused`, or `Onboarded{firmware_fingerprint}`;
  and
  `AuditService.Deliver` in `event/device/v1`, unary, one
  `DeviceOperationEvent` per call, which central writes into JetStream
  stream `FLOWSEER_DEVICE_AUDIT` (subjects
  `flowseer.<tenant>.audit.device.>`, file storage, `Nats-Msg-Id` set to
  `event_id` with a ten-minute duplicate window, longer than any edge
  re-send) and answers only after the stream acks, so audit before
  release stays enforceable at the edge and central is the stream's only
  writer. Central's own records (`DriftDetected`) go the same way. The
  services sit beside their messages because `api/edge` may import
  neither package, and the assertion middleware covers them. Central
  derives no state from the audit stream: the fingerprint comes from
  reports, and the stream is a record someone reads later. U1 amends the device-service record's transport
  section, in its final form: request-response over ConnectRPC,
  observability over the bus. Why: the user's rule on 2026-09-06 that
  the leaf carries logs, metrics, and traces and never a decision; a
  server stream is one-way HTTP/2, which the record allows for Connect.
- **`src/modules/edgebus` is the bus carrier both hosts assemble.** The
  leaf node carries the agent's own OpenTelemetry logs, metrics, and
  traces today and is the buffer for device logs, SNMP traps, and change
  events as each ingestion source lands; `src/protocol/syslog` already
  exists for the first of them. Two hosts assemble the same thing, the
  hub listener and forwarder on central and the leaf starter and
  exporters on the edge, which is the admission test in
  `src/modules/README.md`. Central side: the embedded `nats-server` in
  operator mode, one operator key and one `default` account generated on
  first start, `MemAccResolver`, `SyncAlways`, a leaf listener over
  WebSocket with Connect's certificate, JetStream domain `hub`, the
  journal bucket, the audit stream, one stream `FLOWSEER_EDGE_<edge-id>`
  per attached edge sourcing that edge's buffer alone (attribution by
  stream, decided during U2 after a forged-header probe showed one
  aggregate could not tell which edge delivered a record), and a forwarder
  that follows every edge stream, drops a record whose subject lies
  outside its stream's edge, and reads the OTel
  subjects from it and pushes OTLP to central's configured endpoint
  through the `src/common/service` client. Edge side: `nats-server` as a
  library with one `RemoteLeafOpts` to the hub, its own JetStream domain
  `edge-<edge-id>` (a leaf without a distinct domain silently extends
  the hub's), a file-backed stream `EDGE_BUFFER` on
  `flowseer.<tenant>.edge.<edge-id>.>` bounded by bytes and age, and a
  loopback OTLP/HTTP receiver: the agent points its managed
  `service.TelemetryConfig.Endpoint` at it, and the receiver publishes
  each `/v1/{logs,metrics,traces}` body as received to
  `flowseer.<tenant>.edge.<edge-id>.otel.{logs,metrics,traces}` through
  the local stream, so the runtime's own batching, retry, and transform
  are reused and `src/common/service` exports nothing new. The receiver
  binds `127.0.0.1` on port 0, and the port the kernel assigns is what
  the agent hands its runtime as the endpoint, so a collision cannot stop
  the agent. Central's
  forwarder posts the same bodies unchanged. Both embedded servers
  declare their fsync policy with `service.BusFsyncPolicy` and the same
  normalization the local bus uses (declared, a server option, never
  storage identity, per the bus-durability solution): the hub
  `BusFsyncPerMessage`, the edge buffer `BusFsyncPeriodic`, which is the
  record's "edge buffers may run lazier". Ingestion sources later publish
  under
  `flowseer.<tenant>.edge.<edge-id>.ingest.<source>.>` on the same
  stream and need no new carrier. Why: the accepted record's attachment
  decision and its store-and-forward rationale; `StreamSource.Domain`
  sources a stream across domains (see the Stop condition). R3 on the hub
  waits for a deployment plan.
- **The minted JWT's permissions follow the traffic directions.** Publish:
  the edge's subtree `flowseer.<tenant>.edge.<edge-id>.>`, its own
  JetStream API `$JS.edge-<edge-id>.API.>` and `_INBOX.<edge-id>.>` for
  publish acks, and the reply subjects the hub's source consumer names.
  Subscribe: `$JS.edge-<edge-id>.API.>` and `_INBOX.<edge-id>.>`, so the
  hub's sourcing requests reach the edge domain and nothing else does.
  `account_jwt` is the `default` account's JWT signed by the operator
  key; the leaf's remote verifies the hub with the same SPKI pin verifier
  the Connect client uses (`EnrollResponse.trust_anchors` are 32-byte
  digests, so a `VerifyPeerCertificate` shared between the two dialers,
  not a root pool), and the hub's WebSocket listener serves Connect's
  certificate. With one account the `<tenant>`
  token carries no broker enforcement in this slice. Why: one grant per
  edge lets each ingestion source plug in without a credential change
  while the broker still confines the edge to its own subjects, and a
  synchronous JetStream publish needs its reply inbox; `AttachBus`'s
  subject map names `otel` today and grows a key per source.
- **`DeviceCredential.material` becomes a typed
  `flowseer.device.credential.v1.CredentialMaterial` (`snmp_v3` or
  `shell`), and the SSH host-key pin `ssh_host_key_sha256` sits on both
  `AcquireReadCredentialResponse` and `SubmissionGrant`, set exactly when
  the material is the `shell` arm (on each response,
  `!has(this.credential) || (has(this.credential.material.shell) == has(this.ssh_host_key_sha256))`).**
  The mounted file holds the whole `CredentialMaterial` as prototext,
  user name and protocol identifiers included, and `Provider.Get` returns
  the parsed message; the registry carries only the key and version. Why
  one file: the provider already models one file per credential version,
  and splitting the non-secret fields into the registry would give a
  rotation two places to agree; a rotation that must update two places in
  agreement is a rotation that will eventually be half-done, however
  tidy the split looks on paper.
  `device/credential` imports nothing FlowSeer-owned, so `api/edge`
  importing it keeps the graph acyclic the way `device/policy` does. The
  handle names the trust version; the pin is the material of that
  version. This retires the opaque-`bytes` decision of the merged
  edge-bus credentials plan, which chose opacity because no adapter yet
  needed a shape; two consumers now do (the SNMP session factory and the
  shell factory), and an opaque field every consumer must agree about
  out of band cannot carry a validation rule. Why: the adapters need a
  shape now, decision 10 needs a governed host key on the write path
  too, and nothing delivers the pin.
- **Device sessions open per operation from delivered material.**
  `DeviceSession` carries `OpenSNMP(ctx, material)` and `OpenShell(ctx,
  material)` factories and the binding's endpoint; the lane acquires a read
  credential through `Config.ReadCredentials` for the onboarding probe and
  for every read, and `Machine.Execute` hands the grant to `Deps.Submit`.
  Why: decision 9 forbids a standing credential, and the grant is
  authority-only otherwise.
- **Registry: a prototext `DeviceRegistry` (devices, bindings, integration,
  policies, credential keys, delayed-apply horizon per binding) plus bucket
  `edges`.** An absent horizon refuses `ApplyInterfaceDescription` with
  `device/horizon-unset`. Why: no inventory service exists, and enrollment
  must survive a restart.
- **Storage messages live in `flowseer.store.device.v1`, a new `store`
  root in the layering table that imports `api/inventory`, `api/edge`,
  `device/access`, `device/policy`, `device/credential`, and `errs` (a
  read that closes with a failure carries its `ErrorPayload`); the
  `importOrder` keys are `store/device` and `device/credential` (the
  table strips at the version segment), `device/credential` imports
  nothing, and `api/edge` gains it; decision 12 of the verified-access
  record gains both packages and the api/edge sentence.** Why:
  `spec/proto` is the source of truth for every message, and
  `flowseer/service` is excluded from the ordered roots by decision 12.
- **Placement:** `src/modules/edgebus`, `src/services/device`, and
  `src/edge/agent`, all in the main Go module.

## Requirements

1. `integration/device/v1` gains `HoldResolved`, `HoldResolvedAck`,
   `Onboarded`, `Refused` with the lane's code, `Dispatch`, `Report`,
   `ExecuteRequest.resume` and `admitted_at`, `ExecuteResult.submitted`,
   an empty `progress` arm on
   `ExecuteResult.outcome` for a non-terminal report, and `DispatchService`
   with `Subscribe` and `Report`; `device/access/v1` gains
   `TypedRead.access_policy` (required), the handle a read is admitted
   under; `event/device/v1` gains `AuditService.Deliver`;
   the envelope README documents the two directions, the disposition
   matrix, the reports an edge must send (one per phase reached, a read's
   with its result, `Refused` with its code for a dispatch it cannot
   apply, and which codes are retryable), and the `resume` rule with the
   carried admission time; its "one subject per device per edge" line
   goes; the owed-row derivation is central's policy and lives in
   `src/services/device/README.md`. `device/credential/v1` and
   `store/device/v1` exist with READMEs; `store` joins `orderedRoots`.
   Example: removing `store` from the table fails
   `TestOrderedRootsCoverEveryTopLevelTree`.
2. `journal.Admit` assigns the next sequence and stores the intent at
   `ADMITTED` in one CAS write; the same idempotency key returns the
   recorded state; a second, different intent is refused while a mutation
   holds the lane, so two concurrent `Admit` calls on one device yield one
   admission and one refusal.
3. A `Subscribe` handler sends every owed row on open, sends within one
   backoff step of a record change, re-sends while owed, stops on the
   row's confirmation or negative confirmation, sends a mutation row and
   read rows side by side, and never sends a `TerminalResultAck` for a
   held or never-dispatched intent. Example: close the stream after `CheckpointRequest` was sent
   and its ack lost, reopen it, the duplicate is refused with
   `no-pending-wait` at a reported phase past `POSSIBLY_APPLIED`, and the
   record marks the checkpoint confirmed; a message owed while no stream
   is open is sent on the next open; `Onboarded` at `POSSIBLY_APPLIED`
   re-dispatches with `resume` and the original admission time, and at
   `ADMITTED` without `resume`; a read owed past its deadline is dropped
   and recorded failed; a disposed held intent owes nothing; an open
   read never delays a `CheckpointRequest`.
4. `AuditService.Deliver` answers only after the stream acks the record,
   a duplicate `event_id` is stored once, and central reads nothing back
   from the stream. Example: a fake stream that refuses the publish makes
   `Deliver` fail and the edge's transition stays at its last phase.
5. `DispatchService.Report` applies reports in phase order and updates the
   fingerprint from `Onboarded` and from every observation's provenance:
   `ADMITTED` confirms dispatch of a mutation, sets `dispatched`, and
   moves the phase to `POSSIBLY_APPLIED` in one write; the phase-order
   check is scoped to the open mutation's sequence, and a read's
   `OBSERVING` report with its observation, or its error, writes only its
   `open_reads` entry;
   `VERIFIED` records the disposition and moves to `ACKNOWLEDGED`;
   `RECOVERING` sets `INDETERMINATE` and the last reported phase only; an
   error report with `submitted` false (`mutation/firmware-epoch` the
   first case, then an `Execute` failure before the latch, `lane-closed`,
   `out-of-order`) disposes `REJECTED` and moves to `ACKNOWLEDGED`; an
   error report with `submitted` true at any phase is applied like a
   `RECOVERING` report (`INDETERMINATE`, phase left at `POSSIBLY_APPLIED`),
   never `REJECTED`; `RELEASED` confirms the ack and closes the record,
   `ABANDONED` confirms it and leaves the held mutation for resolution;
   `Refused` per the
   outbox decision, a refused `TerminalResultAck` closing the record the
   way `RELEASED` does and never re-disposing; `Dispose` of a mutation the
   edge never reported admitted (`dispatched` unset) closing the lane and
   owing `HoldResolved` in the same write, so an edge that already holds
   the dispatch is released; a report that would regress a terminal
   mutation, or that arrives before the edge's admission is recorded, and
   any stale or duplicate report, is ignored. Example: a retry after a `RECOVERING` report opens a second
   grant for the same sequence.
6. `DeviceService` behaves as its README says, plus:
   `ApplyInterfaceDescription` refuses a stale
   `expected_firmware_fingerprint` and an unset horizon;
   `GetDeviceAccessStatus` reflects the record; `AbandonMutation` refuses
   only a mutation that is already terminal and otherwise writes the
   terminal state at any open phase (an edge that never comes back is
   the case that needs it); `ResolveDesynchronization` handles all three
   arms as decided; `ReadInterface` writes an `open_reads` entry, waits on
   the record's key watch (the report may land on another replica) up to
   the request deadline, and returns the observation or the error.
7. The drift poll dispatches one read per managed interface every
   `DriftInterval`; a differing complete observation with no open
   mutation emits `DriftDetected` and records a reconciliation intent per
   the mode.
8. `EdgeService`, `DispatchService`, and `AuditService` are served behind an
   HTTP middleware that verifies
   the assertion against the raw request body and path before Connect
   decodes it, for unary and stream-open calls alike. `EdgeAdminService` is
   not, and this requirement's first draft was wrong to say so: an operator
   holds no edge key, so that check would refuse every admin call. It and
   `DeviceService` are served in front of the middleware with no
   authorization check of their own, which `src/services/device/README.md`
   states as a deployment constraint — the network boundary stands in until
   OpenFGA lands, which this plan puts out of scope. `Enroll` is also in
   front of the middleware, since it happens before central holds a key to
   verify it with, and carries its own proof;
   `Enroll` is idempotent per the api/edge README; `AttachBus` returns a
   user JWT scoped to the edge's subtree; the credential RPCs resolve
   handles through
   `internal/credential.Provider`; a submission opens only when the record
   shows `POSSIBLY_APPLIED` for that sequence, and `AbandonMutation`
   revokes an open grant.
9. The edge host enrolls on first start (persisting its key pair first),
   heartbeats, attaches and starts its leaf node with the returned
   credential, exports its own OTel through it, onboards every device
   central's registry lists for it,
   holds `Subscribe` open and drives the lane from its dispatches, keeps
   a per-device map from sequence to the in-flight or last report,
   populated at dispatch so a second `Onboarded`-driven dispatch finds it
   before `process` runs, and answers a duplicate dispatch by re-sending
   that report instead of admitting a second lane entry, answers a dispatch it cannot apply with
   `Refused`, delivers every audit record over `AuditService`, re-sends
   its last report every `ReportResend` until confirmed, and freezes the lane
   after two missed heartbeats, unfreezing on the next success.
10. An in-process end-to-end test (central host, edge host, fake device
    factories) proves: apply until `unresolved` is unset and the interface
    row carries the observation; central killed between checkpoint and
    result then restarted, same outcome; abandonment with a fake clock;
    `restore` after abandonment; the audit stream accounts for the change in
    order.

    That last clause is narrower than "holds the expected kinds in order",
    which is how it read first, and the difference is deliberate. A
    transcript of kinds asserts whatever the system happened to do on the day
    it was written: it passes by construction, breaks on every unrelated
    change, and teaches its readers to update it rather than read it. What is
    asserted instead is that the named records are present, that the device
    was identified before anything was done to it, and that a release closes
    its operation's account — nothing about that operation appears after it.
    Other records may appear between the named ones.

    The ordering claim is confined to the lane's own records, and soundly:
    the edge's audit deliverer blocks until central answers and central
    answers only once the stream holds the record, so the lane cannot move
    past a record that is not yet durable. Central writes records of its own
    at its own moments, and the stream's sequence orders those against the
    lane's without any causal guarantee, so no claim is made across the two.

    Three of those five are blocked on U8f: a mutation currently never
    verifies, so there is no resolved apply, no result to kill central
    between a checkpoint and, and no abandonment to restore after. The
    assembled run itself is not blocked and landed first — onboarding and
    an operator's read both work end to end — but neither of those is a
    clause this requirement names.

    The end-to-end's `apply` helper carries no `validate_only` argument. It
    was removed rather than left unused: nothing passed anything but the
    default, and `unparam` is right that an argument with one caller-supplied
    value is not a parameter. The dry run is the runbook's, and it comes back
    with the test that needs it rather than as an argument waiting for one.

    Only the edge needs a substituted clock. Central's device service holds
    one `time.Now()` outside its tests, for certificate validity; the
    journal and the drift poller both already take an injected clock and the
    host passes nil to each. What that clock drives is `admitted_at`,
    `blocked_since` and `readExpired`, none of which abandonment depends on
    — `AbandonMutation` decides on phase, not on elapsed time. So central's
    seam exists and is merely unexposed through `Run`. The likely first
    caller is requirement 3's expiring read rather than anything here, and
    the work when it comes is exposing `journal.New`'s existing parameter.

## Out of scope

Item 7 itself. Multi-node hub and R3 replicas. The ingestion plane
(receiving syslog, decoding traps, modelling change events); this plan
leaves them the buffered carrier. An inventory service and its RPCs.
OpenFGA guards on `DeviceService`. Announce subjects and capability
advertisement. A second tenant. The route signals and telemetry polish
named above. Telnet, other vendors, multi-field intents.

## Units

U1 is the shared first commit of this plan and the lane host contract
plan: neither lands alone, and the lane plan's U1 sits after it.

### U1. Schemas and record amendment
Files: `spec/proto/flowseer/integration/device/v1/{execution.proto,dispatch.proto,README.md}`,
`spec/proto/flowseer/device/access/v1/{interface.proto,README.md}`,
`spec/proto/flowseer/event/device/v1/{audit_service.proto,README.md}`,
`spec/proto/flowseer/api/edge/v1/{credential.proto,bus.proto,README.md}`,
`spec/proto/flowseer/device/credential/v1/{material.proto,README.md}`,
`spec/proto/flowseer/store/device/v1/{lane_record.proto,registry.proto,edge_record.proto,README.md}`,
`test/conformance/proto/{layering_test.go,api_edge_rules_test.go}`, new
rules tests,
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`,
`docs/architecture/2026-09-05-verified-device-access-direction.md`,
`src/modules/localnet/access/internal/credential/{connect_adapter.go,connect_adapter_test.go}`,
`generated/go/proto/`
After: none
Change: requirement 1; the drift poll and `ReadInterface` fill
`access_policy` from the registry; `ssh_host_key_sha256` on both
credential responses with the CEL coupling; the api/edge README's
"deliberately absent" entry on a typed credential body is replaced by a
pointer to `device/credential`, and its assertion section says the edge
sends requests uncompressed and the conformance vector grows a non-empty
body and a stream open;
`AttachBusResponse.subjects`' comment says the edge publishes on them and
drops the `execute` example, since the leaf carries observability only;
the device-service record's transport section says request-response
over ConnectRPC and observability over the bus, in place of
`exec.<integration-id>` and the events subjects, and records that R3
waits for a deployment plan; its attachment section is untouched; the
verified-access record's decision 12 lists the two new packages; the
access module's credential adapter and its test follow the typed
material so the module builds after this unit.
Tests: one valid and one invalid case per new rule; the layering cases.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'spec/proto/flowseer/**' 'test/conformance/proto/**' 'docs/architecture/**')`

### U2. edgebus
Files: `src/modules/edgebus/{hub.go,leaf.go,jwt.go,receiver.go,forwarder.go,fsync.go,doc.go,README.md}`,
`src/modules/README.md`, and tests
After: none
Change: the module per its decision: hub starter creating the bucket, the
audit stream, and the source stream; JWT minting; leaf starter with its
own domain and bounded buffer; the loopback OTLP receiver; the central
forwarder; the shared fsync declaration; a test starter for each side
with ephemeral stores.
Tests: a leaf joins with a minted JWT; a publish to another edge's
subtree and a subscribe to `flowseer.>` are denied while the hub's
sourcing still flows; the shared pin verifier rejects a certificate
whose SPKI digest is not in the anchors; a log record exported on the edge reaches the
hub source stream and the forwarder's fake endpoint byte for byte; records
published while the hub link is down arrive after reconnect (the Stop
condition's evidence); an undeclared fsync policy fails start; the audit stream
stores a duplicate `event_id` once; the bucket survives a stop and start.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/edgebus/**') src/modules/README.md`

### U3. Journal
Files: `src/services/device/internal/journal/{journal.go,record.go,doc.go}` and tests
After: U1, U2
Change: bucket `device-lanes`; `Admit`, `ApplyReport` (whose `ADMITTED`
arm is the write that reaches `POSSIBLY_APPLIED`), `ConfirmCheckpoint`,
`ConfirmHoldResolved`, `Dispose`, `ResolveHold`, `SetExpected`,
`SetFingerprint`, `OpenRead` and `CloseRead`, all CAS with one retry
loop; phase-order validation scoped to the open sequence; `Owed()` on
the record.
Tests: requirements 2 and 5 against the embedded hub, and the owed-row
table, which is the proof of the outbox decision rather than one test
among others: table-driven and exhaustive over the enumerated record
states (no mutation; `ADMITTED` unconfirmed; `POSSIBLY_APPLIED`
checkpoint unconfirmed; `POSSIBLY_APPLIED` recovering; each terminal
disposition with `dispatched` set, awaiting `RELEASED`; the same pair
after `Onboarded` cleared the confirmations; a recovering record after
`Onboarded`; a disposal with `dispatched` unset; a held reconciliation
intent; a hold resolved and unconfirmed; the restore write that leaves
`HoldResolved` and a new `ExecuteRequest` owed together with the
dispatch; the abandon-before-dispatch close; each `open_reads` state
including a colliding poll and an expired entry) as one shared slice,
each row naming the rows owed with their `Resume` and `Disposition`
payloads. Two assertions run over that slice and, decisively, over a
generated cross product of every mutation the journal can write — phase,
disposition, and block reason taken from the enum descriptors so a value
added to the schema enters the space on its own, times the dispatch
facts, times a hold and read background — filtered by an explicit
reachability predicate that excludes a combination only for a stated
schema rule or a fact about which journal method writes it. The invariant
is quantified per open sequence: for the mutation's sequence, the hold's,
and each unclosed read's, that sequence owes a row, or (the mutation)
names a terminator, or (a read) is past its deadline and due for the
sweep — so an owed row on one sequence cannot mask a stranded state on
another. A terminator is an invocable RPC, not a label: a non-terminal
mutation is ended by `AbandonMutation`, bounded by the recovery horizon
when it is blocked; an abandoned mutation whose ack the edge confirmed is
ended by `ResolveDesynchronization`; a separate test invokes each and
shows it moves the record forward. The generated sweep is the proof that
no reachable state strands — the reachable behaviors are exactly the six
that owe a row and the one that owes nothing but names a terminator; the
stranded signature is provably absent, and reintroducing it turns the
sweep red. The readable table additionally must exhibit every reachable
behavior, so a new enum value that produces an unhandled behavior fails
until a row demonstrates it; a wholly new record field still escapes,
which only field reflection would catch.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/internal/journal/**')`

### U4. Dispatch, report, and audit handlers
Files: `src/services/device/internal/dispatchapi/`, `internal/auditapi/` and tests
After: U2, U3
Change: the `Subscribe` handler over `Owed()` per requirement 3; the
`Report` handler per requirement 5; the `Deliver` handler per
requirement 4.
Tests: requirements 3 and 4; requirement 5 is proven at the journal in U3
and exercised here through the handler.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/internal/dispatchapi/**' 'src/services/device/internal/auditapi/**')`

### U5. Registry, edge store, and EdgeService handlers
Files: `src/services/device/internal/registry/`, `internal/edgestore/`,
`internal/edgeapi/`, `internal/edge/{verifier.go,verifier_test.go}`,
`internal/credential/{provider.go,provider_test.go}` and tests
After: U1, U2, U3
Change: registry loader with validation; bucket `edges` with setup keys
and public keys; the body-verifying middleware around
`internal/edge.Verifier`, with `body_sha256` covering the uncompressed
HTTP body bytes as received, enveloped for a stream open, a compressed
request refused, and the verifier doc saying the same;
`Provider.Get` returning a parsed `CredentialMaterial` from a prototext
file;
the six `EdgeService` and six `EdgeAdminService` RPCs per requirement 8;
grant streams pulse every `PulseInterval`.

The foundation landed as five stand-alone commits (verifier body-digest
alignment; the registry loader/`DeviceResolver`; `Provider.Get` parsing
`CredentialMaterial`; the edge store behind the verifier's key lookup; the
assertion middleware). The RPCs remain, and these constraints are the
brief, recorded here rather than carried so they survive a fresh session:

- Grant stream (the seam that bit the access module and is flagged twice):
  central sends a positive `SUBMISSION_AUTHORITY_AUTHORIZED` pulse —
  authority is a value the edge reads, never inferred from what has not
  arrived. A revocation is a `REVOKED` pulse; silence is not a revocation
  and must never be the only signal, because the edge fails closed on
  silence and the two halves would then disagree in the write-blocking
  direction. The cadence makes the edge's "a positive `AUTHORIZED`
  immediately before `Submit`" rule reachable rather than racy, and a
  stream that ends carries a reason rather than closing bare, since the
  edge treats a clean end as non-authorized. The grant is one-use per
  stream, not per sequence; the recovery retry opens a second grant for the
  same sequence, so the submission gate admits a sequence in recovery
  (requirement 8).
- Every write in the RPCs answers the two questions the whole plan turns
  on: can it leave an obligation with no reachable terminator, and can it
  leave the record owing nothing while something outside it still holds
  open work.
- `Enroll` is idempotent: a second `Enroll` with the same setup key returns
  the same identity, never a fresh one. `Rekey` leaves the old key refusing,
  not working. `AttachBus` mints the edge-scoped JWT through `edgebus` with
  the permission set narrowed subject by subject in U2 — not widened to
  suit an RPC without flagging it. `AcquireReadCredential` and
  `OpenDeviceSubmission` resolve through the provider and the registry, so
  the host-key pin coupling and the `CredentialMaterial` arms are already
  contracted; build to them.

Successor note for the RPCs (written at handoff, from reading the twelve
contracts). Build them in three slices, and treat the first two as crypto,
not CRUD — that is the fact that decides the level of care:

1. The six `EdgeAdminService` RPCs, together, because they share the
   setup-key machinery and the lifecycle state machine and splitting them
   would split that. `CreateEdge`/`IssueSetupKey` generate a setup key,
   show it once in `EdgeProvisioning`, and never return it again; the store
   holds only its SHA-256 (`StoredEdge.setup_key_hash`). The key string is
   `fse1_<26-char id>_<52-char secret>`, lowercase base32 (`a-z2-7`) without
   padding — the id is `SetupKey.key_id`, safe to log; the secret is not.
   Generate the secret from `crypto/rand`; weak randomness passes every
   unit test. `IssueSetupKey` replaces an unused key and returns a retired
   edge to pending; `RevokeSetupKey` clears the unused key; `RetireEdge`
   moves to retired so the edge's key is refused from the next call (the
   verifier's lifecycle step already enforces "enrolled only", so retiring
   is a store write, not new verifier code). `EdgeProvisioning` also carries
   `central_url` and the trust anchors (SPKI SHA-256 digests) the edge pins;
   those are deployment config the host supplies, not minted here. `GetEdge`
   and `ListEdges` are store reads; `ListEdges` pages over the bucket keys in
   a stable order with an opaque token.
   These six also maintain the index enrollment needs, because they are the
   only writes that change which key is live. `EnrollRequest` carries the key
   string and no edge ref, so an enrollment can reach a record only through
   the key's identifier; without an index that is a scan of every edge on the
   one RPC with no assertion in front of it, which an unauthenticated caller
   can drive. The `edges` bucket therefore holds a second key class,
   `setupkey_<identifier>` naming the edge, written after the record and
   dropped when the key is withdrawn. A crash between the two writes, and a
   replaced key's leftover entry, both fail closed: the index is a lookup
   hint, and what admits an enrollment is the digest comparison against the
   edge the hint reached.
2. `Enroll`, `Rekey`, `Heartbeat`, `AttachBus`, `AcquireReadCredential`.
   `Enroll` consumes the setup key: hash the presented key and compare to
   the stored hash in constant time (`crypto/subtle.ConstantTimeCompare`) —
   an early-return compare accepts and rejects correctly in every test and
   leaks the secret by timing. It is idempotent per the api/edge README: a
   second `Enroll` with the same key and the same public key returns the
   same enrolled identity, never a fresh one; enrollment installs the edge's
   Ed25519 public key into `EdgeState`, which the verifier's key lookup then
   reads. `Rekey` replaces the public key and leaves the old one refusing.
   `AttachBus` mints the edge-scoped JWT through `edgebus.MintEdgeUser` with
   the U2 permission set — do not widen it. `AcquireReadCredential` resolves
   the read-credential handle through the registry and `credential.Provider`
   and carries the host-key pin coupled to a `shell` material arm.
3. `OpenDeviceSubmission` alone — the grant stream, to the pulse-not-silence
   rules recorded above. Highest risk; give it its own slice and the two
   questions at every write.

These are all authenticated behind the assertion middleware (landed), which
puts the verified assertion in the context; `EdgeAdminService` is the operator,
not an edge, so its authorization is the operator middleware, not the edge
verifier.

U5 is complete. What follows is for whoever takes U6, and it is the part that
is not recoverable from the diff.

**Where U6 starts.** Everything it consumes is in place: `edgestore` holds the
edge records and the setup-key index, `edgeapi` serves both services,
`journal` is untouched by U5. Its first move is the `errs`-to-Connect mapping
above, because that is a live leak and it replaces
`edgeapi.connectErr`, which every U5 handler calls. `connectErr`'s default
arm is deliberately `Internal`: an unmapped code is never quietly reported as
something the caller can fix, and every new code has to be added to the switch
or its tests fail loudly. Keep that property.

**The comments that are load-bearing, and why they are where they are.** Three
say things that no reader can recover from the code around them, and each sits
where someone would otherwise do the wrong thing. `EdgeForSetupKey` says the
index is a lookup hint and never an authentication decision, because an index
hit is the natural thing to mistake for proof. `consumeSetupKey` says the
check and the write it authorizes must be one decision, at the point where the
next gate would be added. The grant's contract says why the same credential on
two streams is not one secret twice, and what would change if central ever
minted per-grant material. Move them with the code if the code moves.

**The recurring defect, four times in one unit.** Every one was a claim that
had stopped being true, not a broken mechanism: the api/edge README saying
setup-key minting is audited when nothing records it; `EdgeContact` documented
as derived when nothing derived it, so an edge silent for a year reported
active; `StoredEdge.setup_key_hash` documented as cleared on consumption when
idempotent enrollment needs it kept; and this unit's own comment claiming a
uniform-cost lookup the path in front of the comparison does not provide. The
enrollment race was the same shape one layer down — a check that was true when
it ran and false when it was relied on. Reading the prose against the code,
rather than the code against itself, is what found all five. Do it to U6's
README before writing its handlers.

**Two things that will look like defects and are not.** The setup-key digest
survives the key's consumption, deliberately: a repeated `Enroll` has to
recognize the key it already consumed, and the digest buys a thief nothing
because the `CONSUMED` branch also requires a proof signed by the registered
private half. And a device's `contact` is never read back from storage; it is
derived on every admin read, so a record's stored value is a leftover and not
the answer.

**The verifier lies when handed a directory.** Every `Verify:` line names files
through `git ls-files`; check any new one actually selects gates before
trusting it. The same applies to a test: three tests in this unit were written,
passed, and only caught their defect after the fix was reverted to see them
fail. Two of them did not fail at all the first time and had to be rewritten.

Tests: requirement 8; the README's idempotent-enroll and stolen-key cases.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/internal/**' 'spec/proto/flowseer/api/edge/v1/**')`

### U6. DeviceService handlers and drift
Files: `src/services/device/internal/deviceapi/`, `internal/drift/`,
`src/services/device/README.md` and tests
After: U3, U4, U5
Change: requirements 6 and 7; `errs` codes map to Connect codes per the
error-wire record; the owed-row table in `src/services/device/README.md`.
The `errs`-to-Connect mapping is a fix here, not a feature. U5's handlers
build their Connect errors by passing the `errs` error straight to
`connect.NewError`, whose message is `Error()` — the error's own text
followed by its whole cause chain, which the error-wire record calls
trusted-internal. So central already leaks internal text to untrusted
callers, sharpest on `Enroll`, the one RPC with no assertion in front of it:
a bucket failure returns the NATS transport detail to an unauthenticated
caller, and a credential prototext parse failure quotes the offending line
of a credential file. The mapping U6 builds must go through
`errs.EncodeForClient`, keeping the code-to-`connect.Code` switch and using
the sanitized user message, and it must replace `edgeapi.connectErr` rather
than sitting beside it.
Also emits central's audit record for a dispatch central disposes itself,
alongside the `DriftDetected` one and from central's own scope: when a
terminal `Refused` disposes a mutation `REJECTED` (`RejectDispatch`, U4), a
`DeviceOperationEvent` for that sequence carrying the `REJECTED` disposition
and the refusing code, so a firmware-epoch rejection leaves a durable trace
and an operator resubmitting the idempotency key after the lane closed reads
the rejection rather than only `RELEASED`. U4's `RejectDispatch` returns the
disposed state for this; the report handler is not central's audit-emission
site, so the event is emitted here. The carrier is U6's to shape — a
disposition-and-code field on `LaneReleased`, or the event's attributes.
Also tells an operator what retiring an edge left behind. `RetireEdge` ends
the edge's standing and withdraws its setup key, and it deliberately abandons
no mutation: an admin call that silently abandons mutations across device
records is a blast radius nobody asked for, and `AbandonMutation` is already
the RPC for an edge that never comes back. But the operator must learn what
they now have to abandon rather than finding it later in a stuck lane, so
`DeviceService` grows the read that names, for one edge, the devices whose
records hold an open mutation, and the central host wires it behind
`RetireEdge`'s response. The shape is U6's to choose; what is fixed is that
retirement is not silent about the work it orphans.

`RetireEdge` also drops the pending hold resolutions on that edge's devices.
A hold exists to tell an edge to clear its own; with no edge the obligation
is moot rather than pending, and leaving it owed asserts an obligation
against a peer that cannot discharge it. Retirement is the moment central
learns the edge will not acknowledge, so it is where to act on it. It also
makes the pending-hold wall unreachable in the one case that reaches it: the
set fills at `maxPendingHolds`, drains only through the edge's
`HoldResolvedAck`, and refuses further abandons — so an operator working
through a dead edge, which is exactly who abandons repeatedly, would
otherwise wall themselves in. Retiring the edge they are working around
clears it.

That is a cascade from an edge record into lane records, which the paragraph
above says retirement does not do, and the next reader will take it for a
change of mind. It is not. Abandoning a live mutation destroys work an
operator did not ask to lose and hides a device that may carry a
half-applied change; dropping a hold removes an instruction addressed to a
peer that no longer exists. The first is a decision only the operator can
make, the second is a fact retirement establishes.
Tests: each RPC's happy path and its refusal cases; the drift table for
both modes and all three resolution arms; retiring an edge that holds an
open mutation names that device.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/internal/**')`

### U7. Central host
Files: `src/services/device/cmd/device/main.go`, `src/services/device/internal/host/`,
`src/services/README.md`
After: U6
Change: `service.Run` with modules `hub`, `forwarder`, `journal`,
`connect`, `drift`;
prototext config; self-signed certificate on first start with its SPKI
pin printed once; no local bus is declared.
The host's logging interceptor must unwrap the errors the handlers return,
not format them. U6 made a handler's error render its client-facing
sentence from `Error()`, so `%v` or `%s` on one logs the sanitized text and
silently drops the cause chain. So does `errs.From(err).Msg(...)`, which was
this brief's first suggestion and is wrong for the same reason: it recovers
the code and the merged attributes but renders its own message through the
client-facing wrapper, whose `Error()` is the sanitized sentence and stops
there. What works is `errors.As` to the `*errs.Error` in the chain, logged as
a value so its `LogValue` renders the whole tree. A logging line that
looks right and records nothing useful is found during an incident, not
before one.

Beside it, a validating interceptor. The handlers rely on the request-level
schema rules and none of them re-states one, which is the right division and
holds only if something enforces them; nothing did. An intent with no
idempotency key reached the journal and spent one of the sixty-four
remembered slots on an empty string the schema says must be a uuid, and an
intent with no change arm was admitted with nothing to apply.
U7 also owes the drift telemetry event, which U6 deliberately did not build.
The audit half is done — `centralaudit.DriftDetected` writes the durable
record — and the telemetry half waits here because the meter and the tracer
are the host's to wire: an emitter built in the poller would either have no
provider or construct an ad-hoc one, which is how the access module ended up
with nine named events, three of them with no production emitter and some at
the wrong level with unbounded labels. This is urgent rather than tidy,
because the companion plan deletes the edge's own detector: between U6 and
U7 there is no drift telemetry anywhere. What it needs:

- Event `flowseer.device.drift.detected`, emitted from the device service's
  own instrumentation scope — central detects drift, so the record and the
  event are both central's, and neither is the edge's any more.
- The emission site is `drift.Poller.judge`, beside the audit record it
  already writes, so an event exists exactly when a detection does.
- Attributes: the device id, the interface name, and the expected and
  observed descriptions. Only the interface name is bounded enough to be a
  metric label, and even that is per-device; the two descriptions are
  operator-written text and belong on the event and the log, never on a
  metric. A counter, if one is wanted, is labeled by the device's management
  mode, which has two values.

Tests: config validation; a start-and-serve smoke test; the interceptor
records the internal cause of a failure whose client sentence names none of
it, through a real Connect round trip; an unvalidated request never reaches
its handler; a detection emits the event once, with the descriptions off the
metric.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/**')`

U6 is complete. What follows is for whoever takes U7.

**Where U7 starts.** Everything it assembles exists: `journal`, `edgestore`,
`registry`, `credential`, `edgeapi`, `dispatchapi`, `auditapi`, `deviceapi`,
`centralaudit`, `drift`, and the `connecterr` mapping every handler answers
through. Nothing in U6 wires a server, a provider, or a config file, so U7 is
assembly and the two obligations above. The one seam with no production
implementation is `deviceapi.RecordWatcher`; `deviceapi.NewKVWatcher` is it,
and it wants the lane bucket.

**Four seams a fresh session will meet cold.** Each exists because the
alternative was found to be wrong, not because it was the first shape tried.

- `journal.ResolveDesynchronization` is one CAS write that disposes a held
  sequence, records its hold, rewrites the expectation, and admits the
  replacement. It replaced `ResolveHold`, which is gone: resolving and
  admitting as two writes leaves a window where the lane is free, the hold
  resolved, and the operator's intent unrecorded, and `Admit` refuses over a
  held mutation so there was no third shape. Do not add a second door.
- Two admission doors, `Admit` and `AdmitBlocked`, share one implementation.
  The blocked one is how a drift intent takes the lane without being
  dispatched; the owed-row derivation refuses to dispatch a blocked mutation,
  so nothing else enforces the hold.
- The expectation writer lives in the `ReportVerified` arm. A verified change
  is what central expects from then on, and that write is also what makes an
  interface managed as far as the record is concerned — which is what gates
  `keepLastObservation`. Three behaviors hang off one map: what drift compares
  against, which interfaces the status call reports, and which reads leave a
  row behind.
- `connecterr.Table` per serving package, with a cross-service check that one
  code answers the same thing everywhere. The check found a real disagreement
  the day it was written.

**What U6 learned that is not already recorded.** The recurring defect showed
up twice more, both times as a claim nothing kept true: a `MutationState`
synthesized for a resubmission that failed three of its own schema rules and
had never been validated because it only ever existed in a response, and an
idempotency echo that described the caller's new intent as the recorded one
because nothing compared them. Both were found by asking what a change
inherits rather than by a failing test. The generalizable one is the write
ordering: when two writes can each fail, order them so the survivable failure
is the one that happens, and pin the order with a test that fails when it is
reversed — the ordering is invisible in the happy path, so nothing else holds
it in place.

**Three things that will look like defects and are not.** An interface central
holds no expected description for is polled but never judged: central has
applied nothing to it, and adopting whatever the first read found would be an
expectation nobody asked for. `RetireEdge` drops pending holds but ends no
mutation — dropping an instruction addressed to a peer that no longer exists
is not the same as destroying work an operator did not ask to lose. And the
audit record for a detection goes out before the intent is admitted, on
purpose.

**A verifier sharp edge U7 will hit.** Its `Verify` line names
`src/services/device/**`, which now includes proto-adjacent paths only
indirectly — but if a `Verify` line ever names a path under
`spec/proto/flowseer/api/device/` or `spec/proto/flowseer/store/`, the
`buf breaking` step fails with "no .proto files were targeted", because
`master` has neither package. The Verification section above records it.

### U8. Edge host
Files: `src/edge/agent/cmd/agent/main.go`, `src/edge/agent/internal/{identity,busattach,dispatch,report,lanehost}/`,
`src/edge/agent/README.md`, `src/edge/README.md`
After: U2, U4, U5, and U3 of the lane host contract plan
Change: requirement 9; `AttachBus` then the leaf from `edgebus`, with the
agent's telemetry endpoint pointed at the loopback receiver; the `Subscribe` client loop with
backoff,
demultiplexing dispatches by device id; the `Reporter` as a re-send
queue drained by a Connect client and the audit `Deliverer` as a
blocking Connect client; session factories over
`src/protocol/snmp` and `src/protocol/ssh` decoding `CredentialMaterial`
and pinning the host key; a test hook to inject fake factories.
Tests: enrollment persistence order; freeze on missed heartbeats;
re-send until confirmed; a dispatch owed while the stream was down
arriving after reconnect; a second `ExecuteRequest` for a live sequence
re-sends the report and admits no second lane entry; a resumed dispatch
enters recovery and a non-resumed one after a restart at `ADMITTED`
executes once; a refused `Deliver` holding the machine at its
last phase; the agent's own log records reaching the hub after a link
drop.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/edge/agent/**' 'src/edge/README.md')`

U7 is complete. What follows is for whoever takes U8.

**What U8 will meet, not what U7 built.** Central runs: `cmd/device` reads a
prototext `DeviceServiceConfig`, obtains a certificate, and starts five
modules. The edge host talks to it across seams that are all in place —
`EdgeService` for enrollment, rekey, heartbeat, `AttachBus` and the credential
calls; `DispatchService.Subscribe` and `Report`; `AuditService.Deliver`. The
things below are the ones a fresh session gets wrong because nothing in the
signature says them.

**The certificate pin is the edge's whole trust anchor, and it is persisted.**
Central serves one key on the API listener and the bus listener alike, so an
edge pins one SPKI digest for both. The digest is logged once, at generation,
and recomputable from the certificate file; a restart says which certificate
it loaded and deliberately not its digest. What matters for U8: a central that
generated a fresh key on each start would refuse every edge already in the
field, with no way back but re-provisioning each by hand. If the edge host's
tests spin central up repeatedly against one state directory, that is the
property they are relying on. `EnrollResponse.trust_anchors` replaces the set
an edge was provisioned with, and `edgebus.PinnedTLSConfig` is the client side
for both the Connect dialer and the leaf remote — the same verifier, so the
two cannot drift apart.

**`Enroll` is served in front of the assertion middleware; everything else on
those three services is behind it.** An edge signs every other call with the
key central registered, over the request body bytes as received and the
invoked procedure. The edge must send uncompressed — central refuses a
compressed request before verifying, because the bytes hashed have to be the
bytes on the wire. For a server-stream open, the body is the one
Connect-enveloped request message. The api/edge README carries two worked
header vectors, one unary and one stream-open, and a conformance test fails if
they drift.

**The operator surface has no authorization check.** `DeviceService` and
`EdgeAdminService` sit in front of the middleware, because an operator holds
no edge key. Nothing replaced it; the deployment's network boundary is what
stands in until OpenFGA lands, and `src/services/device/README.md` states it
as a deployment constraint. Requirement 8's first draft said otherwise and was
corrected — it described a design nobody could build.

**Errors are sanitized outward and unwrapped inward, and the two are one
mechanism.** A handler's error renders its client-facing sentence from
`Error()`, so anything that formats it — `%v`, `%s`, or `errs.From(err).Msg()`
— logs the sanitized text and drops the cause. The host interceptor reaches
past the client-facing wrapper with `errors.As` to the `*errs.Error` and logs
that. An edge-side reporter that logs a Connect error it received will hit the
same trap from the other end: what arrives on the wire is the sanitized
sentence plus an `ErrorPayload` detail, and the code is what to branch on.

**The hub handshake, and why the supervision strategy is load-bearing.** The
service runtime starts a supervisor's children concurrently — it schedules
every child and returns before any `Setup` begins — so declaration order is
not startup order. Central's four hub-dependent modules wait on a handle
bounded by their own attempt context. The part that is easy to break: a
dependent reads the hub's resources once and uses them for the whole attempt,
which is only safe because the root supervisor is `RestForOne` with the hub
first, so a rebuilt hub takes every module after it. Change that strategy and
the modules behind it hold closed connections.

This paragraph then said the edge host has the same shape, with the leaf, the
reporter queue and the `Subscribe` loop all depending on the attachment. It
does not, and they do not. The decision above — every request and response is
ConnectRPC, the bus carries observability only — means the `Subscribe` loop,
the report queue and the audit deliverer are Connect clients over pinned TLS,
built from the enrollment's trust anchors. None of them touches the leaf.
Confirmed by construction once all three existed: nothing outside
`internal/busattach` references an `Attachment` or a `Leaf`.

So the attachment has exactly one consumer, the agent's own telemetry endpoint
— the receiver binds a fresh loopback port on every start, so an exporter
pointed at an old one exports into nothing. That is a supervision ordering for
the entrypoint to arrange, not a handle: a mechanism for publishing one
generation's resources to several waiters, given one waiter, is a variable.

Left as the record of a claim that was reasonable when written and false once
the code existed. The rule it illustrates is the one this build keeps
producing: the plan is the artifact that changes when the code disagrees with
it, and a successor note is a pointer to what to check rather than a
description of what is there.

**Method, not confession: three tests here passed for reasons adjacent to the
ones they named.** A certificate test that asserted the pin is logged only at
generation could not fail, because the load path had no logger and so could
not log anything — the absence it proved was of a capability, not a behaviour.
A generation test for the hub handle passed with the fix removed, because a
later nil check did the work the channel swap was credited with. And the
fixture problem U6 left: `held()` built a mutation the edge had not
acknowledged, so every resolve test treated an illegal state as legal. All
three were found by reverting the fix and watching the test fail, and all
three look identical to a test that works. Assume a test written for a fix
does not test it until you have watched it fail without it — U8's re-send
queue, freeze-on-missed-heartbeat and duplicate-dispatch cases are exactly
that shape.

**A gate that reports success on something the wide gate refuses.** Error
codes are unique across the repository, enforced by a source scan in a test
inside `src/common/errs`. A per-slice verifier over changed packages never
runs it, so this unit's `telemetry/instrument` collided with the access
module's and passed six slices of green checks before `go test -race ./...`
refused it. The same holds for every repository-wide namespace: telemetry
scope names, metric and event names, bus subjects, bucket names. A unit that
adds a name from one of those runs the wide gate.

**Two smaller facts.** The edge no longer detects drift — central does, from
its own instrumentation scope, and `telemetry.View.DriftDetected` on the
access module is deleted by the lane host contract plan. And
`registry.Devices` now refuses an edge id the registry does not describe
rather than answering with an empty list, so an edge host test that invents an
edge id gets an error instead of silence.

**U8's enrollment ordering, decided before the code.** The edge persists its
Ed25519 key pair, then calls `Enroll`, then persists the response. Every
crash point in that order recovers:

| Crash point | A restart finds | What it does |
| --- | --- | --- |
| before the key is written | no key, no enrollment | generates a key and enrolls; the setup key is still `ISSUED` |
| after the key, before `Enroll` | a key, no enrollment | enrolls with that key; the setup key is still `ISSUED` |
| after `Enroll` returns, before its response is written | a key, no enrollment | enrolls again with the same key; central's setup key is `CONSUMED` and the registered key matches, so the same answer comes back |
| after the response is written | a key and an enrollment | skips enrollment and heartbeats |

The reverse order has a point with no way back. Enroll first, crash before
the key is written, and central holds a public key whose private half never
reached disk. The edge cannot sign, so it cannot call anything — including
`Rekey`, which needs an assertion signed by the key it is replacing. The
only remedy is an operator retiring the edge and issuing a new setup key,
because `IssueSetupKey` refuses an `ENROLLED` one. One order loses a round
trip; the other loses the edge.

What makes the third row recover is `Enroll`'s idempotency, and that is
central's property rather than the edge's: the same setup key with the same
public key returns the same response and writes nothing
(`edgeapi/enroll.go`'s `consumeSetupKey`, and the api/edge README states it).
The edge's crash recovery therefore depends on a remote invariant, which is
worth naming because nothing on the edge would fail if central stopped
holding it — the edge would simply be unable to re-enroll after a crash in
that one window, in the field, on a path no edge test exercises.

Named on central's side rather than only here, because a note on the
depending side is read by someone who already knows. The line is at the
handler's `CONSUMED` branch in `edgeapi/enroll.go`, which is where an edge's
retry is actually answered — not at `consumeSetupKey`'s equality check, which
serves the replica race its own comment describes and which an edge retrying
never reaches. Getting that wrong was found by reverting each branch in turn
and seeing which one a test noticed.

**And the freeze must be distinguishable from never having fired.** Freezing
the lane after two missed heartbeats is a mechanism whose whole purpose is to
act when nothing is happening, which is the shape that hid the drift
detector, the evidence cache and the pre-mutation baseline in this build. A
test that asserts "no mutation was applied" passes for a lane that froze, a
lane whose device was unreachable, and a lane that was never asked. The
freeze gets its own observable — the `lane.frozen` record already exists per
device — so a test asserts the freeze happened rather than that nothing else
did.

**The re-send queue's bound, decided before it exists.** An unbounded queue
on a device with a lifetime is a leak with good manners, so the shape is
settled here rather than discovered when a central is gone for a day.

The queue supersedes rather than accumulates: it is a map keyed by device,
sequence and report kind, holding each operation's *latest* report, which is
what requirement 9 asks it to re-send. A mutation that reports admission,
verification and release occupies one entry, not three.

That makes the real bound structural rather than numeric — reports are caused
by dispatches, and a central that cannot be reached also cannot dispatch, so
the set of outstanding operations stops growing while it is away. The two
exceptions are operations already in flight when contact was lost, and the
`Onboarded` an edge sends at start. Both are bounded by devices times
in-flight operations.

A hard ceiling still exists, as a defense against a bug producing unbounded
distinct keys rather than against an absent central. At it, the oldest entry
is dropped and counted, because almost every report is one central will ask
for again: the outbox re-derives what it holds no report for, the edge answers
the re-dispatch from its registry, and the report is re-queued. `Onboarded` is
the exception and is never dropped — it is edge-initiated, central does not
know to ask for it, and losing it leaves central's record believing the edge
still holds state it lost at restart.

Stated as what each choice means for central rather than as a policy. Drop
newest would discard the terminal report while keeping progress ones, which is
the opposite of useful. Stopping the dispatch loop at the ceiling would make
one device's stuck report cost every other device on the edge. Spilling to
disk is a durability promise this queue does not make and the journal already
does.

**The host handle is not built, because its trigger turned out to be false.**
It was deferred from E1 and E2 on the argument that a coordination mechanism
with fewer dependents than it coordinates is tested against a situation that
does not exist, and made an E3 deliverable on a written trigger: three
dependents on one bus attachment — the leaf, the `Subscribe` loop and the
re-send queue.

Checking that trigger rather than assuming it, once all three existed, showed
only one dependent. The bus carries observability only, so the `Subscribe`
loop, the queue and the deliverer are all Connect clients over pinned TLS from
the enrollment's trust anchors, and nothing outside `internal/busattach`
references an `Attachment` or a `Leaf`. What the attachment actually feeds is
the agent's own telemetry endpoint, which changes on every start because the
receiver binds a fresh loopback port.

So the entrypoint arranges it as supervision ordering — the telemetry export
belongs with the attachment and ends with it — and there is no handle. Writing
the trigger down is what made this findable: a judgment re-made each slice
would have been re-made a third time and the mechanism eventually built.

**A precondition for the lab run, checkable before the switch is powered
on.** This system manages devices at SNMPv3 authPriv only — every field of
`SnmpV3Credential` is required — with AES-128, 192 or 256 for privacy. The
lab ICX7150 must be configured that way before item 7, and confirming it
costs nothing beforehand.

Afterwards it costs a session and looks like something else entirely: an edge
refuses to open the SNMP session, on a credential-delivery path with nothing
wrong with it, and the first place anyone looks is central's credential
population. The runbook checks the device's SNMPv3 security level as a step,
alongside powering the switch on.

**A number for the lab run to settle.** The agent's heartbeat bounds each
attempt by the heartbeat interval, so a central that is alive but answers
slower than one interval is indistinguishable from one that is down, and two
such answers freeze the lane. That is the right default and the wrong thing to
tune from a desk. If the lab shows heartbeat latency anywhere near the
interval, the deadline and the interval want separating rather than both being
raised — a longer interval also delays detecting a central that really is
gone. Obvious once measured, invisible until then, so the runbook records the
observed latency whether or not it looks interesting.

### U8b. What the edge is told about a device
Files: `spec/proto/flowseer/api/edge/v1/edge_service.proto`,
`spec/proto/flowseer/api/edge/v1/README.md`, `generated/**`,
`src/services/device/internal/edgeapi/`, `src/edge/agent/internal/lanehost/`
After: U8's session factories
Change: `EdgeService.ListDevices`, and the agent using it.

This unit exists because the agent could open a session to a device it
cannot locate. `RegistryDevice` holds the management address, the ports, the
binding, the access policy and the measured horizon; nothing sends any of it
to the edge, and requirement 9's "onboards every device central's registry
lists for it" assumed a listing RPC that does not exist. Absent data has no
failing test and no compile error, so it survived every gate until something
had to use it.

**Why a listing rather than the credential response.** The address and the
host-key pin are needed at the same moment and look like a pair, but they
fail in opposite directions. A changed host key fails closed: the pin refuses
and the caller gets an error. A changed address fails open, silently, against
a different real device that answers normally. Riding the same per-operation
mechanism would put a stable fact on a revocable call — and a mutation's
`Execute` and its verifying `Observe` acquire separate credentials, so an
address that changed between them sends the command to one device and reads
the verification from another, with the lane reporting VERIFIED about a box
it never touched. Agent-side configuration was rejected for making the
address a second source of truth; carrying it on each dispatch, for having
the same per-operation hazard and repeating a stable fact on every message.

**What one listed device carries**, all of it read from the registry the
edge cannot see:

- the device id, its management address, and the SNMP and SSH ports;
- the access policy handle, which `DeviceSession.AccessPolicy` needs for the
  onboarding probe and has no other source;
- the binding ref, which `OpenDeviceSubmission` requires as a UUID and
  `ProvenanceInputs.Binding` puts on every observation — a second gap the
  enumeration found, with the same shape as the address;
- the measured delayed-apply horizon, which is the third.

**The horizon is not only a missing field.** `RegistryDevice.delayed_apply_horizon`
is per device and fixture-measured — its own comment says an unmeasured one
refuses mutations — while `access.Config.DelayedEffect` is lane-wide, one
horizon for every device the edge serves. Delivering it is not enough: the
horizon has to move onto `DeviceSession`, or an edge serving two devices with
different measured horizons abandons one of them at the other's. That is a
change to the access module rather than to a wire, and it is why this unit
touches `lanehost` as well.

An unmeasured horizon refuses the mutation, not the device. The first
implementation refused the device at `AddDevice`, which is a wider rule than
the fact supports: the horizon bounds how long a *mutation's* effect may stay
unknown, a read has no effect to become visible, and refusing the device stops
its drift polling and answers central's read with `access/unknown-device` —
false, since the device is registered and the edge is holding it, and a
sentence that sends whoever debugs it looking for a registration bug there is
no trace of. It also puts the edge and central's registry, which refuses only
the mutation, in disagreement about what the same fact means.

**Staleness is the accepted answer.** The edge lists after attachment and
again on each `Subscribe` reconnection, and holds what it was last told
between listings. Stale-but-consistent beats fresh-but-inconsistent within
one operation, which is the same argument that kept the address off the
credential response. Say it in the message comment rather than leaving it to
be rediscovered.

Breaking `api/edge/v1` after U1 is a fact rather than a risk: nothing outside
this repository consumes it, and the standing rule welcomes the change.

Tests: an agent that lists and onboards what it was told; a device listed
with no horizon onboarded and read, with a mutation on it refused; a listing
served only to the edge the assertion names; two devices with different
horizons, each abandoning at its own.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/edge/agent/**' 'src/services/device/internal/edgeapi/**' 'spec/proto/flowseer/api/edge/**')`

### U8c. Updating a registered device's session
Files: `src/modules/localnet/access/lane.go`, `src/modules/localnet/access/README.md`,
`src/edge/agent/internal/lanehost/`
After: U8b
Change: a `Lane` call that updates a registered device's session, and the
agent using it in place of U8b's divergence signal.

U8b's onboarder holds the devices it has already added and adds only the
new ones, because a second `AddDevice` for a registered key replaces its
`deviceState` wholesale and orphans that device's queue and any in-flight
drainer. So a device whose horizon or address is corrected centrally keeps
the values it was first listed with until the agent restarts. U8b makes
that visible rather than silent — a re-list carrying a device the edge
already holds with different fields records which fields diverged — and
the operator's experience without that signal is the reason: they measure
the horizon, set it centrally, mutations keep failing
`access/horizon-unmeasured`, and nothing connects the two facts.

The signal is not the fix, and the fix is not plumbing. An update must not
disturb the device's queue, its drainer, its firmware fingerprint, or an
open mutation's state, and what happens to a mutation already in flight
against the old address and the old horizon is a correctness question
rather than a detail.

**It forks on `Machine.Submitted()`**, which the lane already uses to tell
"provably nothing was sent" from "the effect is unknown". Same fact, same
fork:

- An update applies to operations admitted after it. A queued item has
  captured nothing yet and picks up the new session when it is admitted.
- An open mutation that has not submitted is refused, and central
  re-dispatches under the new values. Nothing went to the device, so there
  is no effect to strand.
- An open mutation that has submitted finishes under the values it started
  with. Not a concession: the address the command went to is the only
  address whose read can verify or refute it, and verifying at the new one
  would answer a question about a different box — the hazard that decided
  `ListDevices`. The horizon likewise, since the command was sent under a
  bound that re-bounding afterwards would silently reinterpret.
- A recovery poll keeps the horizon it started under, one level down from
  the same reasoning.

A mutation that finishes under superseded values says so, on its result or
its span. Otherwise the edge reports an operation against an address
central no longer believes in and nothing outside can tell that is what it
is reading, which is the failure shape this build keeps meeting: the
behavior is right and the two cases are indistinguishable.

Tests: an update that leaves a queued item and a running drainer alone; a
device whose horizon is corrected mid-life accepting a mutation it
previously refused; an update arriving before submission refusing the open
mutation, and one arriving after it not refusing, with the result saying
its values were superseded.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/**' 'src/edge/agent/**')`

### U8d. The agent's configuration
Files: `spec/proto/flowseer/store/edge/v1/agent_config.proto`,
`spec/proto/flowseer/store/edge/v1/README.md`, `generated/**`,
`test/conformance/proto/layering_test.go`, `src/edge/agent/host/config.go`
After: U8b
Change: the prototext file an edge host is deployed with, and its loader.

The agent has no configuration surface at all: no schema, no loader, no
flags. One prototext file per deployment is the answer central already
gives (`DeviceServiceConfig`), and splitting the same deployment across a
file and a flag set would mean two places to look and two to get wrong.

**It points at the provisioning file rather than restating it.**
`flowseer.api.edge.v1.EdgeProvisioning` already carries the central URL, the
setup key and the trust anchors — everything about joining — and it is what
an operator receives at issue time.

Two messages carried that name. `store/device/v1`'s held central's own side
— the URL it publishes, the assertion audience and skew it checks, the
cluster URLs it hands out — and shared exactly one field with the api-side
one, so a sentence naming "EdgeProvisioning" was ambiguous between a message
an edge reads and a message central is configured with. The store-side one
is now `EdgeEnrollmentPolicy`, because the api-side name is what an operator
sees on the artifact in their hands and central's is the policy that
produces it. Both comments say which produces which, since the person who
would break the correspondence is editing one of them and has no reason to
look at the other. Copying those three fields into a second file would
make the provisioned values and the configured ones two sources of truth
for the same facts, with no mechanism keeping them equal. So `AgentConfig`
names a path to it, and what it holds itself is what the provisioning does
not describe: the state directory, the intervals, the local buffer's
bounds, and the log level.

**The setup key's handling follows the precedent, not a fresh invention.**
`ServiceTelemetry.headers` says values are secret and belong in a file only
where the file itself is a secret; the provisioning file is that, and its
schema comment says so. The edge's exposure is worse than central's,
because the file sits on the least-trusted machine in the system — and
bounded, because the key is single-use and consumed at enrollment, after
which the file is no longer what an attacker wants. The identity store is:
it holds the private key that *is* this edge. Both facts belong in the
schema comment, since the operator reading it is the one choosing the
file's mode.

**No OTLP endpoint.** The agent's telemetry goes to the loopback receiver
`busattach` binds, which forwards over the leaf; the endpoint is the
receiver's own bound address and changes on every start. A configurable one
would be a second, wrong answer to a question already answered.

Lane bounds — queue capacity, recovery poll interval, operation timeout —
stay on the access module's defaults, for the reason `DeviceServiceConfig`
gives about the bus's storage bounds: a knob nobody has needed is one field
away when a deployment needs it, and an unused field is a thing to keep
correct.

`store/edge` is a new schema package, so the import-order table gains a
line: it reaches `api/edge` for nothing yet and the primitives for nothing
yet, which is worth saying out loud rather than leaving the table to imply
a dependency the file does not have.

Tests: a file missing a required field refused at load, naming the field; a
file naming a provisioning path that does not exist refused at load rather
than at first call; defaults applied for every unset duration.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'spec/proto/flowseer/store/edge/**' 'src/edge/agent/host/**' 'test/conformance/proto/**')`

### U8e. The agent entrypoint
Files: `src/edge/agent/cmd/agent/main.go`, `src/edge/agent/host/`,
`src/edge/agent/README.md`, `src/edge/README.md`
After: U8d
Change: `cmd/agent/main.go` and the host that assembles the agent, closing
U8.

**One ordered chain, written in one place with each link's reason.** Each
link is forced by a different fact, and none of them is visible from the
call it constrains:

1. The receiver binds before the exporter is built. The receiver's loopback
   port is fresh on every start, so an exporter pointed at the previous
   one exports into nothing — and exports into nothing quietly.
2. The identity is established before the attachment. `AttachBus` is a
   signed call.
3. The lane exists before the dispatch loop runs. A dispatch for a device
   with no lane has nowhere to go.
4. The onboarder has the lane, and the dispatch loop has the onboarder. The
   onboarder adds devices to the lane, and the loop re-lists before every
   attempt so a dispatch never arrives for a device the edge has not
   onboarded.

**Enrollment and attachment failing are fatal; nothing else is.** Without
an identity there is nothing to carry on with — every call but `Enroll` is
signed, so an agent that could not enroll can do nothing but exit and let
its supervisor try again. A first `ListDevices` that fails is not fatal for
the same reason a later one is not: an edge that can still serve the
devices it holds is worth more than one that stops. But at startup it holds
none, so it comes up serving nothing and refusing every dispatch, which
looks exactly like an edge with no devices assigned. The
`flowseer.edge.devices.listing_failed` event must therefore fire on the
startup listing too, and the agent README says a first listing failure is
not fatal and what it looks like when it happens.

Tests: the startup chain as far as a fake central can see it, which is up
to the attachment and no further — the order the calls arrived in and which
of them carried an assertion; a refused enrollment ending the run, asserted
on the error's code rather than on what did not happen, because an agent
that carried on would fail a moment later at the empty anchor set and look
identical from outside; the attachment pinning the anchors the enrollment
returned rather than the provisioned ones.

Everything past the attachment needs a live hub — the receiver binding, the
exporter pointed at its address, the lane, and the three loops — so it is
asserted by the unit tests of the pieces rather than by an assembled run:
`dispatch` covers the re-list before every attempt and a failed listing
still opening the stream, `lanehost` covers the onboarder and the endpoint
its session factories are built with. **U9 proves the assembled run**, and
a hub fixture built here would be most of that harness built twice, with
the second one the real one.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/edge/agent/**' 'src/edge/README.md')`

### U8f. The access policy a mutation's own observation reads under
Files: `src/modules/localnet/access/lane.go`, its tests,
`src/services/device/test/integration/testdata/mutation-verification-repro/`
After: U8e. **Before U8g and U9**, which are both blocked on it.
Change: a mutation applies to the device and is never verified. The
end-to-end found it; no unit test on either side can, because both halves
are individually correct.

**The symptom.** An operator's `ApplyInterfaceDescription` reaches the
device and the description is written — the fixture device holds it and
recorded the command. The mutation then sits at `POSSIBLY_APPLIED` with
`BLOCK_REASON_INDETERMINATE` and never leaves, through the recovery
poll's whole budget.

**The cause**, from central's own log at `LOG_LEVEL_DEBUG`:

```
rpc.method: flowseer.api.edge.v1.EdgeService/AcquireReadCredential
rpc.response.status_code: invalid_argument
error: request fails its schema rules: access_policy: value is required
```

`machineDeps` builds one `Read` closure for every operation and sources
the policy handle from `req.GetRead().GetAccessPolicy()` at `lane.go:1691`.
An `ExecuteRequest` carries exactly one of `mutation` or `read`, so on a
mutation `GetRead()` is nil, the handle is nil, and central refuses the
acquisition before any session is opened. The read path works because
central puts the handle on the `TypedRead` it dispatches; the mutation
path has no `TypedRead` to carry one.

The handle the closure needs is already in the request:
`MutationIntent.access_policy` is required, and it is the policy version
the intent was admitted under, which is the one this observation should
read under. `DeviceSession.AccessPolicy` is the onboarding probe's and is
the wrong answer here — central decides per operation which policy
admitted it, and an observation taken under the session's handle would
report the wrong one whenever a policy version has moved since onboarding.

**One defect, seen twice.** The mutation's own post-write observation and
every recovery poll's re-observation go through the same closure, so both
fail identically. That is why the mutation is neither verified nor
abandoned: three acquisitions over a hundred seconds, all refused the same
way. The recovery poll does run — an earlier reading that it did not was
made on a twenty-second window against a thirty-second
`defaultRecoveryPollInterval`.

Tests: an applied description is verified and the lane left free, which
is requirement 10's first clause and currently unreachable; and the
acquisition carries the mutation's own handle rather than the session's,
asserted on the handle's version so a fix that passed the session's
would fail. The second is what stops the obvious wrong fix.

**Reversal to run before claiming it fixed:** with the fix in, change the
registry policy version so the session's handle and the intent's differ,
and confirm the acquisition carries the intent's.

The reproduction is committed under `testdata/` with a README saying how
to run it and what it prints. It is kept because the assembled run is the
only thing that shows this, and rebuilding it costs more than keeping it.
One caution is recorded there: the fixture's session counter counts SNMP
session opens rather than credential acquisitions, and reading it as
acquisitions sent this investigation down a wrong path once.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/*.go')`

### U8g. The Onboarded report the edge never sends
Files: `src/modules/localnet/access/lane.go`, `src/edge/agent/host/report.go`,
`src/edge/agent/internal/report/`, `src/edge/agent/internal/dispatch/`
After: U8f.
Change: nothing in production code builds an `Onboarded` report.
`SetOnboarded` appears twice in the repository and both are tests. Central's
`applyOnboarded` is complete, the edge's report queue classifies the kind and
documents it as never dropped, and `access.Reporter` has no onboarding method
— so the lane cannot tell its host that a device was onboarded, and the
fingerprint its identity probe learned never leaves the edge.

Two consequences. Central learns a fingerprint only from a read's provenance,
so an operator's first mutation on a freshly onboarded device is impossible
until they have done a read, and nothing says so. And `MarkOnboarded` is never
called, so requirement 3's "`Onboarded` at `POSSIBLY_APPLIED` re-dispatches
with `resume` and the original admission time" has no trigger: the
edge-restart recovery path cannot run.

The four questions, settled before building:

The retention the queue's comment asserts holds on both paths, and needed no
change. `evictOldestLocked` skips `kindOnboarded` and logs an error rather
than dropping one when the queue is full of them, and the only other removal
is in `drain`, which deletes a report solely after central has accepted it.

Nothing orders an `Onboarded` against a dispatch the edge has already
answered, and the cost is one redundant re-dispatch rather than a wrong state.
`MarkOnboarded` clears `dispatch_confirmed` and `checkpoint_confirmed` while
leaving `dispatched` set, so central re-sends what is open; the edge answers a
duplicate from its per-device map of the in-flight or last report instead of
running the operation again, which requirement 9 already requires of it.

The report carries the fingerprint. `Onboarded.firmware_fingerprint` is
required, and it has to be the edge's: central has no way to derive an epoch
it has not been told, which is exactly the gap this unit closes.

The test is an edge restarting mid-mutation, and it asserts the count of
writes to the device. A central that forgot the mutation, or re-dispatched it
without `resume`, would write the interface twice; one write is the only
outcome that separates a resumed mutation from a repeated one.

This was called U8f in discussion before the mutation-path defect was found.
It is renamed rather than reordered, so that the unit letters keep running in
the order the units do, as U8b through U8e already do.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/edge/agent/**')`

### U8h. The hub a host started, and the audit stream's order
Files: `src/services/device/internal/host/host.go`,
`src/services/device/test/integration/`
After: U8g. Closes requirement 10's last clause.
Change: central's audit records go to the `FLOWSEER_DEVICE_AUDIT` stream
inside the hub, `host.Run` owns the hub, and nothing reaches it from
outside — so "the audit stream holds the expected kinds in order" cannot be
written. The test cannot mint itself bus credentials either, since
`MintEdgeUser` is the hub's, and it cannot borrow the edge's, since those
live inside the agent.

`Options` gains `Hub func(*edgebus.Hub)`, called once per hub-module attempt
after the hub is up and its resources are built and before anything uses
them. It is the same category as `Options.Bound`: a host telling its embedder
what it constructed, defensible for an admin surface or a health check with
no test in sight.

**The staleness rule is the load-bearing part of its doc.** The modules below
the hub are supervised `RestForOne` exactly because a rebuilt hub invalidates
everything holding its resources, and `hubHandle` exists internally to stop a
stale one being used. Handing an embedder a raw handle invites the same bug
the internal machinery exists to prevent, so the doc says the handle is valid
until the next call and a caller that caches it across a hub restart holds a
dead server.

**The test asserts order, not presence, and a subsequence rather than a
transcript.** "The expected kinds are all there" passes for a stream that
emitted them in any sequence. An exact transcript is the other failure: it
asserts whatever the system happens to do, so it passes on the first run by
construction and breaks on every unrelated change. What it asserts is the
ordering the accountability rests on — for one applied description, that the
device was identified before anything was done to it, that the record saying
the change was applied precedes the record saying the lane was released, and
that no release appears before its own mutation's evidence. Other records may
appear between them.

That property is what makes the lab write accountable: a stream where a
release can precede the evidence for it is a stream that cannot answer what
was done to the device, and nothing else in this plan checks it. The pieces
cover that each record is emitted; the assembled run is the only place their
order is observable.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/internal/host/*.go' 'src/services/device/test/integration/*.go')`

### U8i. A mutation that enters recovery gets a poll, whatever the audit stream is doing
Files: `src/modules/localnet/access/internal/mutation/machine.go`,
`src/modules/localnet/access/lane.go`, their tests
After: U8h. **Before the runbook revision and before the interface-name
misclassification**, because both change what the recovery section can promise
and neither can be honest until recovery is.

Change: a transient audit outage strands a mutation permanently.
`enterRecovery` returns early when `EnterRecovering` fails, so
`startRecoveryPoll` is never reached and no poll goroutine is ever created.
`EnterRecovering` fails when the `LaneBlocked` record it delivers is not
taken, which a central that has gone away does. `block()` has meanwhile
already written the block state, deliberately, so the block survives the
failed delivery — and the mutation is left `POSSIBLY_APPLIED` and
`INDETERMINATE` with nothing scheduled to look at the device again.

Both halves are correct alone. Pinned by
`TestAnAuditOutageWhileEnteringRecoveryLeavesNoPoll`.

**The fix: start the poll on the state, not on the record.** `block()` writes
the block before delivering precisely so the block is real whether or not the
record lands; a mutation whose block is written is a mutation in recovery, and
the poll should follow the state that exists rather than the delivery that
may not have happened. The caller still fails — `Submit` cannot return an
outcome it does not have, and that half of the existing comment is right.

**The record stays owed, and it cannot go where the reports go.** The report
queue holds `ReportRequest` values and *supersedes* per key, dropping at its
ceiling; an audit record is a distinct durable fact that must be neither
superseded nor dropped, so sharing that path is not a small change but a
second queue with opposite retention. The trade is real and should not be
made by accident.

The cheaper way keeps the guarantee without a new component: **the recovery
poll retries the undelivered record.** The poll already runs on an interval
with a budget, and a retry is safe because the audit stream deduplicates on
`event_id` — but only if the *same event value* is retried. `newEvent` mints
a fresh UUID per construction, so a rebuilt record is a second record rather
than a duplicate. The machine must retain the event it could not deliver and
re-emit that value.

Tests: a mutation whose `LaneBlocked` delivery fails still gets a poll, and
the device is read again — the reproduction's assertion inverted. And the
retained record is delivered once the stream takes it, with the same
`event_id`, so the account has no hole where the outage was.
**Reversal:** with the fix in, drop the retention and rebuild the event on
retry; the stream then holds two `lane_blocked` records for one block, which
is what a fresh id per construction produces.

What this unit does not fix, and must not be read as fixing: the deliverer's
missing bound, below.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/*.go' 'src/modules/localnet/access/internal/mutation/*.go')`

### U8j. A refusal the device is certain about is not an ambiguity
Files: `src/modules/localnet/access/internal/capability/interfaces/adapter.go`,
`src/modules/localnet/access/internal/capability/fastiron/adapter.go`,
`src/modules/localnet/access/internal/mutation/machine.go`, their tests
After: U8i. **Before the runbook revision**, because it changes what the
recovery section has to teach.

Change: the likeliest failure of a first live write is recorded as the wrong
kind of failure. `managed_interfaces` is asserted and never checked against
the device, so a wrong or mistyped interface name is the most probable thing
to go wrong — and when it does, the device records as `POSSIBLY_APPLIED` /
`INDETERMINATE` rather than rejected.

`Execute` sets `m.submitted = true` before calling `m.deps.Submit`
(`machine.go:505`). The FastIron adapter refuses a bad interface name at
`SelectInterfaceCommand`, **before `PortNameCommand` is constructed**
(`fastiron/adapter.go:94`), so nothing was written and the adapter knows it.
`afterStep` then sees `Submitted()` and enters recovery, and central records
an effect nobody can establish for a device that provably was not changed.

**One name covering two certainties is the recurring trap in this codebase,
and this is the fourth instance.** The two `ErrCodeNoExpectation` branches
behind one user message; the `restore`-refusal advice written by someone who
had only met the other refusal; the two `ErrCodeAmbiguousSubmission` sites
here. Each time the two halves are individually reasonable and the harm is
that a caller cannot tell them apart. A reader adding an error code in this
module should treat it as a known hazard rather than a coincidence: before
reusing a code, check whether the second site is the same fact or merely a
similar-looking one.

**One error code covers two different certainties, and that is the root of
it.** `ErrCodeAmbiguousSubmission` is returned from two places. At
`adapter.go:94` the select was refused and the `port-name` command never
existed: nothing reached the device. At `adapter.go:105` the `port-name`
command *was* sent and the device did not return to the config-if prompt:
whether it took effect is genuinely unknown. Only the second is ambiguous.

**The fix.** `capability/interfaces` — which already declares the
`ShellAdapter` contract, and which `internal/mutation` already imports — gains
an error code meaning *provably nothing was sent*. FastIron's select-refusal
returns it; its `port-name` refusal keeps `ErrCodeAmbiguousSubmission`.
`Execute` clears `submitted` when, and only when, the submit error carries
that code, under the same lock that set it. Central needs no change: it
already disposes an error report with `submitted` false as `REJECTED`.

**The conservative default must stay the default, not become the exception.**
Any submit error that carries nothing leaves `submitted` true and the effect
unknown, which is what a transport that may have delivered deserves. An
adapter that grows a new refusal path and forgets to mark it must land
conservative and wrong in the safe direction — never silently rejected. That
is what the second test below exists for, and it is the one to write first.

**One race to state rather than fix.** The latch sets `submitted` before the
call precisely so a concurrent `Acknowledge` cannot cancel a command that may
already have gone out. Clearing it afterwards can lose a race with an
`Acknowledge` that already read it as true and declined to cancel — which is
harmless here, because the mutation ends rejected either way, and worth
saying so nobody reads the clear as making the latch two-sided.

Tests: an adapter refusing the interface name is disposed `REJECTED` with the
lane closing on its own, not left indeterminate; **and an adapter error
carrying no code leaves the mutation indeterminate**, which is the default
holding. Reversal: mark the `port-name` refusal as not-submitted too, and the
second test fails — a command that reached the device is reported as one that
did not.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/internal/capability/**' 'src/modules/localnet/access/internal/mutation/*.go')`

### U8k. The mechanics an operator performs, executed by a test
Files: `docs/runbooks/lab-icx7150-first-write.md`,
`src/services/device/test/integration/`
After: U8j. Before the runbook's revision, which describes steps that use these.

Change: the runbook documents the decisions an operator makes and not the
mechanics of making them. No tool is named for any RPC and there is no
operator client, so everything from the dry run onward is unperformable as
written; the firmware fingerprint every intent must carry has no named source;
and the deployment sequence is missing and circular — central will not start
without an edge id, and the edge id only exists once central runs.

Those are one unit because they are one gap: for two days these calls have
been Go in a test fixture, which is exactly why nobody noticed they needed a
tool.

**No client is needed, and this was measured rather than assumed.** `buf curl`
speaks Connect, takes the schema from `spec/proto` since central serves no
reflection, and trusts the certificate central generates. Run against a live
fixture central it answers first time:

```
buf curl --schema spec/proto --cacert <state_dir>/tls.crt \
  --data '{"device":{"device":{"id":"…"}}}' \
  https://127.0.0.1:8443/flowseer.api.device.v1.DeviceService/GetDeviceAccessStatus
{"highWatermark":"0","firmwareFingerprint":"7a81590c…"}
```

So the unit adds no binary. A client would have to justify itself against
that, and cannot.

**The acceptance criterion, which is the whole point: the test executes what
the runbook prints, character for character.** Not an equivalent Go call, not
the same RPC assembled differently — the literal line. Anything else proves
the RPC works and leaves the documented command unverified, which is the state
that produced twelve findings.

The way to guarantee it is for the two to have one source: **the test reads
the runbook, extracts its executable blocks, and runs them.** Drift then
cannot happen, because editing the document edits the test. Commands are
written with shell variables the runbook tells the operator to export and the
test exports itself, so the same line serves a person and a harness. Blocks
the test must not run — commands typed at the switch — are fenced
differently, and the runbook says which fence means what.

**The bootstrap sequence belongs here, not in the runbook.** `assemble`
resolves the circularity with a throwaway edge id and a restart, in a place no
human would look. Whatever sequence an operator is given must be the sequence
the test performs: write the credential files with their modes and sidecars,
start central, create the edge, write the returned id into the registry,
restart central, issue the provisioning, start the agent, wait for onboarding.
If the fixture's shape and the operator's differ, the difference is a second
thing nobody has run.

**The fingerprint's source is a step, not a footnote.** Every intent must
carry one, and it reaches central from the edge's onboarding report — so the
step is to read it back from `GetDeviceAccessStatus` and use that value, with
the runbook saying what an empty one means: the edge has not onboarded the
device yet, and no mutation is possible until it has.

Tests: every executable block in the runbook runs against a fixture
deployment, in order, and the ones that produce output produce the output the
document claims. Reversal: change a documented flag to one `buf curl` does not
accept and the test fails on the line the document prints.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'docs/runbooks/**' 'src/services/device/test/integration/*.go')`

### U8l. The bootstrap, performed by the binaries
Files: `src/services/device/cmd/device/main.go`,
`src/edge/agent/cmd/agent/main.go`,
`docs/runbooks/lab-icx7150-first-write.md`,
`src/services/device/test/integration/`
After: U8k. Before the runbook's revision.

Change: the deployment sequence is missing from the runbook and it is missing
because it cannot be written honestly yet — the suite runs `host.Run`
in-process and an operator runs binaries, so a documented sequence would be
prose nothing executes. This unit builds both commands and drives them as
processes.

**It is bigger than the missing paragraph and the reason is what it covers.**
Nothing in this repository runs either binary. `grep -rl 'cmd/device' --include
'*_test.go'` finds nothing, and the same for the agent. So flag parsing,
loading a config from a path, the exit-2 contract for a bad config, signal
handling and shutdown have never been exercised by anything — and those are
exactly what an operator meets first and what an in-process fixture cannot
reach.

**A defect was reported here during specification and it was not real. The
correction is kept because the mistake is instructive.** Neither `main.go`
imports `os/signal` and both call `host.Run(context.Background(), …)`, and two
readers concluded from that that the documented clean-stop-on-signal did not
exist. It does: `service.Run` installs
`signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` itself, and says so
in its own doc. The commands delegate, correctly, and their package comments
are accurate.

What went wrong is worth naming, because it is this build's own recurring
shape from the other side: behaviour was inferred from the *absence* of a call
at one layer without reading the layer it delegates to. The same caution that
was applied to `Hub.ListenPort()` — an inherited property asserted by our own
comment — applies to an inherited property asserted to be missing.

**What caught it was the reversal.** Removing the "fix" should have made the
test fail and did not, and chasing that found the runtime's handler. A
reversal that refuses to fail is the third thing in this build to find
something its test could not.

So this unit adds no signal handling. What it does is check the contract:
nothing had ever sent a signal to either binary, so "an interrupt is a clean
stop and exits 0" was an unverified claim about code that happened to be
right.

**The bootstrap sequence: a workaround, and to be labelled as one.**
`RegistryIntegration.edge` is required, `CreateEdge` mints the id, and central
reads its registry once at start — so the registry cannot name the edge until
central has run, and cannot be re-read without a restart. The fixture resolves
this with a throwaway id and a restart.

That is convenient rather than right, and the operator's sequence should not
copy it exactly. Central starts happily with an integration whose edge does
not exist, and `devices` may be empty — every CEL rule on `DeviceRegistry` is
vacuous over an empty list — so the honest first registry names the
integration and no devices, rather than listing a real device to an edge that
does not exist. **The fixture changes to match the document, not the reverse.**

The circularity itself is filed rather than fixed: an idempotent `CreateEdge`
taking a caller-supplied id, or an optional `integration.edge` for an
integration serving no devices, would remove the restart entirely. Either is a
schema change and neither belongs in a unit about running binaries. Written
down so a tested procedure is not later read as an endorsed design.

Tests: the runbook's bootstrap blocks are `sh` and are executed — both
binaries built once, started as processes, driven through the whole sequence,
and stopped by signal with the exit status the package comments promise —
which is now checked rather than assumed. And a
malformed config exits 2 before anything binds, which is a contract nothing
has ever checked.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/cmd/**' 'src/edge/agent/cmd/**' 'docs/runbooks/**' 'src/services/device/test/integration/*.go')`

### U8m. One shell session per device, held and reused
Files: `src/modules/localnet/access/lane.go`, its tests, the access README
After: the runbook revision. **Not a blocker for the lab run** — see below.

Change: the lane opens an SSH session per operation and closes it with the
operation. The user's direction is one session per device, held for a period
and reused, with concurrent operations serialized onto it.

**The device is the reason, and it is measured rather than argued.** FastIron
caps concurrent SSH sessions, and a session that does not exit cleanly makes
the next login fail with `Permission denied` — which reads as a wrong password
and is not one. A session per operation spends a scarce device-side resource,
and drift polling across a fleet becomes a login storm.

**It is a lifetime change and not a concurrency one, which was checked rather
than assumed.** A device's shell is opened in exactly two places, both inside
the closures `machineDeps` builds — the read's fallback route and the
mutation's submission — and both are driven from `process` or from `pollOnce`,
each of which holds `ds.draining` for the whole operation. The onboarding and
epoch probes use SNMP, not the shell. So **nothing can overlap on a device's
shell today**, the serialization the direction asks for already exists, and
this unit must not add a second lock for it. If a later operation needs the
shell outside the drain lock, that is where the concurrency question comes
back.

**What the per-operation shape was for, which the new shape has to satisfy
rather than discard.** `DeviceSession` became a set of factories because a
standing session outlives the credential it was opened with: a credential
central revoked goes on working until something drops the connection, so
revocation means nothing in the meantime. That reasoning is still correct.

The shape that keeps it: **the per-operation credential acquisition stays
exactly as it is** — it is the authority check, it is cheap, and it is what
makes revocation land. What changes is only what the credential is used for.
Today it opens a connection; instead it authorizes use of one already open.
An acquisition that fails closes the held session, so a revoked credential
drops the connection at the next operation rather than whenever something
happens to. And the idle hold is bounded, because between operations nothing
is checking anything at all — the number needs a reason written next to it,
not a round figure.

**The residual, to be stated and not implied away.** A session opened under
credential A can serve an operation authorized by credential B; the device
only knows the login it accepted. So revocation takes effect within an
operation rather than within a connection, which is a weaker guarantee than
today's. That belongs in the access README beside the factories' own
rationale, because a reader who finds the factories will otherwise conclude
the old guarantee still holds.

Tests: a second operation on one device reuses the session the first opened;
a failed credential acquisition closes it; the idle bound closes it; and the
existing per-operation acquisition count does not change — that last is the
one that catches a reuse that also stopped acquiring.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/*.go' 'src/modules/localnet/access/*.md')`

### U8n. A read must not be lost to a shell it never needed
Files: `src/modules/localnet/access/lane.go`,
`src/modules/localnet/access/internal/capability/interfaces/adapter.go`,
`src/modules/localnet/access/access.go`, their tests
Before U8m, with which it agrees — see below. **Blocks the lab write.**

Change: `ReadInterface` cannot succeed against the lab ICX7150, and the write
path fails the same way for the same reason. Found by running the system
against the switch; no fixture produced it, and ours could not have.

**Two defects composing, neither sufficient alone.**

The lane's `Read` closure opens the shell whenever a factory exists, before
`interfaces.Read` is called — and the comment directly above it says the
opposite: *"The shell is opened only for the fallback route… interfaces.Read
decides whether it needs it; opening it unconditionally would mean an SSH
login on every SNMP read."* `interfaces.Read` takes the adapter and builds a
fallback only `if shell != nil`, so nil was always supported and the decision
the comment describes was never given to it.

And that open is a hard return. A shell that cannot be opened fails the whole
read — including a read the SNMP route has already answered COMPLETE, whose
observation is then discarded.

On this device the shell is opened with the *read* credential, which is SNMP
material because the identity probe acquires under the same handle. So the
open always fails, and no read can ever complete. Measured against the switch:
`ReadSNMP` walks 33 interfaces and returns COMPLETE for `ethernet 1/1/1`,
while the lane's read fails with `agent/device-session` before consulting it.

**One change fixes both.** `interfaces.Read` takes a `ShellOpener` — something
it can open *if* it takes the fallback — instead of an already-open adapter,
which gives it the decision its own comment describes. The fatal failure then
stops existing on the route that does not need the shell: nothing was opened,
so nothing can have failed to open, and a read the SNMP route answered
succeeds without a login.

A read that genuinely needs the fallback still fails when the shell will not
open, and that is deliberate. The point is not that a failed login stops
mattering; it is that it stops mattering to reads that were never going to use
it. Swallowing it everywhere would turn a device with no working route into
one that quietly returns partial observations.

**This agrees with U8m and neither should be written as if the other did not
exist.** An SSH login on every SNMP read is exactly the login storm the
held-session direction exists to prevent, against a switch that caps
concurrent sessions. U8m makes the login cheap; this makes it rare. Whichever
lands second should not undo the first.

**The reversal that matters is the second one.** Assert that `OpenShell` is
never called when the SNMP route completes — that pins the comment's intent as
behaviour. "The read succeeds when the shell open fails" passes for an
implementation that still opens eagerly and swallows the error, which is half
a fix wearing the whole one's clothes.

Two notes from the run that verified this, because both are the kind that
gets dropped as noise and later explains a confusing log. The deployment's
processes were invisible to `ps` inside the sandbox while they were in fact
running, so a second central and agent were started on top of the first pair;
the second central failed to bind its bus port and never listened. All four
were stopped and one clean pair brought up. And the agent logs at INFO, so the
absence of an SSH line in its log is not evidence that no login was attempted
— what shows the shell was not opened is the unit test, plus the observation
coming back with SNMP provenance.

Tests, at the lane — where the defect lived and where the capability's own
tests could not see it: a device whose SNMP answers completely is read without
`OpenShell` ever being called; a device whose shell open fails is still read
when SNMP answers; and a device whose SNMP is incomplete still falls through
and its observation comes back over SSH. The last is the partner test: without
it, an implementation that never opens the shell at all passes the first two
and has silently deleted decision 1's fallback route.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/*.go' 'src/modules/localnet/access/internal/capability/interfaces/*.go')`

### U9. End-to-end and item 7 readiness
Files: `src/services/device/test/integration/e2e_test.go`,
`docs/runbooks/lab-icx7150-first-write.md`, `deploy/lab/{central.textproto,registry.textproto,agent.textproto}`
After: U7, U8
Change: requirement 10; the runbook's first step is scheduling, not a
command: the lab switch is normally powered off and the user must be
asked in advance to power it up, and an agent that reaches item 7 and
finds the device unreachable asks rather than retries (an unreachable
approved device is not a transient error to hammer); then the blast
radius statement (one interface description on one lab switch, the
state before and after, the restore command), the `validate_only` dry
run, the horizon measured before the first write, the restore path
proven before the change, and the approval checkpoint, which records the
approval the user gave on 2026-09-06 for this device and this change on
the grounds that it is a test device, and covers nothing beyond them;
the lab config files carry placeholders, never addresses or secrets.
Also a code-equality test, cheap only here because the end-to-end binary
already links both sides: central's refusal-code constants
(`dispatchapi`) must equal the lane module's exported codes for the codes
that cross the wire (`access/no-pending-wait`, `access/unknown-device`,
`mutation/firmware-epoch`) — a code that crosses a process boundary is
public contract whatever package produces it. This is the real link the U4
review's finding 9 left as documented constants.

This unit adds the test and nothing else. The draft said it also required
the lane module to export the codes it emits; that landed three units
earlier, and `src/modules/localnet/access/lane.go` already re-exports
`ErrCodeFirmwareEpoch` from `internal/mutation` making the same argument.
What the test does need is central's three constants exported from
`dispatchapi`, which were unexported strings read only by `report.go`.

The end-to-end test also forces a package move, because Go's internal rule
lets no package import both hosts: `src/edge/agent/internal/host` becomes
`src/edge/agent/host`, chosen over exporting central's host because the
agent's exported surface is `Config`, `LoadConfig` and `Run` and central's
is fourteen accessors, certificates, intervals and two interceptors.
**What the horizon buys, which the runbook has to say.** The number an
operator measures for `delayed_apply_horizon` is documented as one thing —
the longest a mutation may take to become visible — and now decides two
others. It sizes recovery's budget, so it is how long the system keeps
looking before the write becomes a person's problem; and through the derived
poll interval it sets how many times it looks, at least six and more for a
horizon past three minutes.

An operator measuring the ICX7150 on a fixture will read the field's
documentation, measure honestly, and not know they are also choosing how many
chances the first live write has to be confirmed. If the switch's true
horizon is a few seconds, a few seconds is the correct answer to the question
the field asks and a poor answer to the question it also decides: the write
gets its looks inside a window shorter than one distracted moment, and a
central restart or a brief unreachability inside it ends with the interface
changed and the lane held.

So the runbook says what the number buys rather than only how to measure it,
and the lab registry's horizon is chosen with the second question in view.
The honest fix is for the two to stop being one field, which is out of scope
here and belongs with the blind-attempt follow-up below — an attempt that
could not look spends the same budget, and both are the horizon being asked to
mean more than it says.

Tests: requirement 10.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/services/device/test/integration/**' 'docs/runbooks/**' 'deploy/lab/**')`

## Verification

```bash
buf format -d --exit-code && buf lint && buf generate
go build ./... && go vet ./...
go test -race ./src/modules/localnet/access/... ./src/modules/edgebus/... ./src/services/device/... ./src/edge/agent/... ./test/conformance/proto/...
.claude/skills/verify-change/scripts/verify-change.sh --full
```

Two things about running those, both of which look like broken code when
met cold.

A third: `src/edge/` holds two Go modules. `src/edge/netpen/` has its own
`go.mod`, so a glob of `src/edge/**` hands the verifier packages the main
module does not contain and it fails listing them. U8's `Verify` line names
`src/edge/agent/**` for that reason, and a wider glob is not a wider check —
it is a broken one.

The verifier classifies its arguments by file extension, so a directory
selects no gates: it runs the whitespace check, prints "FlowSeer
verification passed", and exits 0 having run no build, vet, race, or lint.
Every `Verify:` line above therefore names files, through `git ls-files`;
a line naming a bare directory is not a gate.

The verifier also runs `buf breaking --against master` over the proto paths it
is given, and `spec/proto/flowseer/api/device/` and `spec/proto/flowseer/store/`
do not exist on `master` — U1 adds them on the branch. Path-filtered breaking
over either package therefore fails with "no .proto files were targeted", which
reads like a broken change and is not: unfiltered `buf breaking --against
master` passes, and so does a path `master` does have. It is the directory
problem's relative — a gate whose output misdescribes the change — except that
this one fails loudly rather than passing silently.

The sweep that followed found one thing: an import ordering in the access
module's credential adapter test. golangci-lint over every package of every
module, and gofumpt and goimports over every non-generated file, report
nothing else. So the debt the directory arguments hid was this work's own —
the packages that had drifted are the ones these plans have been editing,
not ones nobody has touched.

The race suite must run outside the agent Bash sandbox. `httptest` cannot
bind `[::1]:0` under it, so `src/common/service`, `src/modules/edgebus`,
and `src/services/device/internal/dispatchapi` panic with
`failed to listen on a port: bind: operation not permitted`, and
`src/modules/localnet/access/internal/capability/fastiron` fails the same
way binding its SSH test listener. That is the sandbox, not those four
packages; they pass unsandboxed. Run the wide gate with `-count=1`: a
cached PASS from an earlier unsandboxed run makes a sandbox-hostile package
report `ok` under the sandbox without executing anything, which is the third
gate in this build to report success for a reason unrelated to the code.

Run the race suite as one invocation over the package trees rather than one
tree at a time:

```bash
go test -race -count=1 ./src/services/device/... ./src/edge/agent/... ./src/modules/localnet/access/...
```

Concurrency is a condition, not a convenience. `TestAMutationSurvivesCentralRestartingUnderIt`
passed three times in isolation and failed at 160s under that invocation, and
the difference was the finding: a recovery budget that bought one attempt, so
a central restart slow enough to outlast it left a mutation that had applied
resting as an operator's problem. A green run of the packages one at a time is
a statement about that invocation, not about the code, and the machine that
runs the lab will not be idle.

A changed-path run is not confined to the paths it is given: the verifier
builds, vets and races the *dependent* packages of the changed ones too. So
sandbox-hostility is a property of the dependency closure rather than of the
files named on the command line, and any run whose closure reaches a TCP
listener or a UDP socket needs the unsandboxed setting. The four packages
above are examples, not the list — a run over
`src/services/device/internal/dispatchapi` pulls in
`src/services/device/internal/host`, whose `httptest` server panics with the
same `bind: operation not permitted`, and naming a fifth package here would
only leave the next session to find a sixth.

A listener that can be asked for any free port is still not one every test
can use. The API reports what it bound, which is what makes port 0 usable —
and the end-to-end still cannot use it, because the agent reads central's
address from its provisioning file before anything binds and has to keep
dialing that one address across a central restart. Same shape as the bus and
its `cluster_urls`: the constraint is not the listener's, it is that somebody
wrote the address down first.

Two `ErrCodeNoExpectation` branches in `resolve.go` return distinct internal
messages behind one user message: "the sequence names no interface to restore"
and "central holds no expected description for this interface". That is the
right trade in both directions — an operator does not need the distinction and
a diagnosis does — and it is what separated a test racing ahead of the edge
from a defect losing the mutation. The obvious cleanup is to unify the
internal messages to match the user one; do not.

`golangci-lint` runs `misspell` with a US dictionary, over prose in comments
and test messages as well as identifiers. It rejects British spellings
("unrecognised", "behaviour"), which is worth knowing before writing a
runbook rather than one commit after.

The message-sync hook must report nothing for the two new schema packages;
their file comments name the absent triads.

## Definition of done

- Verifier green for every changed path; `generated/` changed only by
  `buf generate`.
- The access module README, the integration/device, event/device, and
  api/edge READMEs, the device-service record's transport section,
  `src/modules/README.md`, `src/services/README.md`, `src/edge/README.md`
  updated in the same
  change; the follow-up plan for route signals and telemetry polish named
  in the access README where the gaps were.
- This plan's `status` set with an outcome note under its title.
- No requirement or unit labels in code, comments, or commit messages.

## What the assembled run caught

U9 was one unit for an end-to-end test and two documents. It took six more,
and the reason is worth recording for the next person deciding whether an
assembled test earns its cost.

Four of those were defects, and each lived in the space between two halves
that were individually correct and separately tested. No unit test on either
side could see any of them, because in each case the failure is only visible
to something that holds both ends at once.

- **A mutation applied to the device and was never verified.** One `Read`
  closure served every operation and took its access-policy handle from the
  request's read arm, which a mutation request does not have. Central refused
  the credential acquisition, nothing was ever observed, and the lane rested
  `INDETERMINATE`. On the lab switch this would have been an interface changed
  on real hardware with the system unable to say whether it had changed — and
  `validate_only` would not have caught it, since nothing is dispatched on
  that path. The module's own fixtures had been building requests with no
  access policy at all, which is how it survived every test on both sides.
- **Nothing ever sent an `Onboarded` report.** Central's handler was
  complete, the edge's queue classified the kind and exempted it from
  eviction, and no code built one. Central learned a device's epoch only from
  a read's provenance, so an operator's first change to a freshly onboarded
  device was impossible for a reason nothing surfaced; and requirement 3's
  edge-restart recovery had no trigger at all.
- **Recovery looked once.** The poll interval was a flat thirty seconds and
  the budget is the horizon plus one interval, so a device whose measured
  horizon was near thirty seconds got exactly one attempt — and a switch whose
  true horizon is seconds would have got one or none. One unlucky moment then
  costs the whole budget.
- **The API listener could not report the port it bound**, while the schema
  documented port 0 as supported.

The other two were seams the assembled run needed and nothing else had asked
for: the agent's device-transport and clock substitution, and the hub the host
started. Both are defensible without reference to any test, and both were
written that way.

Two of the four defects would have reached the switch. That is the argument.

## Follow-ups

**Authorization for the operator and admin surfaces (OpenFGA).** `DeviceService`
and `EdgeAdminService` are served with no authorization check, and the
reachable consequence is not the three RPC effects the first draft of the
service README listed. A caller that reaches the API port runs `RetireEdge`,
then `IssueSetupKey` — which refuses an `ENROLLED` edge but accepts a
`RETIRED` one, returns it to `PENDING`, and hands back the key — then
`Enroll` with its own key, and is that edge. Every device the registry binds
to it then yields its `CredentialMaterial` through `AcquireReadCredential`
and `OpenDeviceSubmission`. The assertion middleware cannot help, because the
attacker registered the key it checks. The body limit wraps only the
middleware paths, so those two services also accept an unbounded body.

Accepted for now, on the user's decision of 2026-09-07, with the deployment's
network boundary standing in. Written down as a decision rather than left as
an omission: a gap someone plans around is a different thing from one they
panic about or quietly "fix" in a way nobody reviewed. The service README
states the full radius, so an operator deciding where to put this knows the
boundary is protecting device credentials rather than an operator API.
U9's runbook carries the lab consequence: the lab run puts real switch
credentials in this service's registry, and the port that serves the operator
API is the one that yields them, so "the port is not exposed beyond the host"
is a step in that runbook rather than background.

**One assertion module holding the signer and the verifier.** They are two
halves of one protocol living in two packages — `src/edge/agent/internal/identity`
signs, `src/services/device/internal/edge` verifies — with the Authorization
scheme mirrored between them because the second is internal to the device
service. What keeps them honest today is the api/edge README's worked header
vector, tested as a literal from both sides: the signer must produce that
string and the verifier must accept it. That is real coverage of the wire
contract and it is enough for now.

What it does not do is make them one implementation. Moving the shared
constant somewhere neutral would be worse than the honest copy — it would
look like consolidation while leaving both encoders and both parsers exactly
where they are. The real change is a module owning the scheme, the signing
and the verification together, which is a design change rather than a
refactor, and out of scope for the units that built these two.

**Operator action trail.** Nothing records that an operator created an edge,
issued or revoked a setup key, or retired an edge. Minting a setup key is the
most privileged operator action there is, and after an incident there is no
way to answer who minted which key for which edge. It is not in this plan
because the audit stream here is device-scoped by design — it answers what was
done to a device, with the retention and access that question deserves — and
an operator-action trail is a different scope with a different retention and
access story. Building one inside a unit about edge lifecycle would put it in
the wrong place permanently, and a scope invented to close a review line
outlives the review. The api/edge README now says plainly that the trail does
not exist rather than claiming the action is audited.

**The audit deliverer has no bound.** `Deliverer.Emit` passes its context
straight through, and the agent's HTTP clients are `&http.Client{Transport:
…}` with no `Timeout`, so the only bound on an audit delivery is whatever
context reaches it. A central that stops answering — rather than refusing —
stalls the lane for as long as that context allows, and the lane holds its
state transition until the delivery returns by design.

This is **not** what produced the stranded mutations U8i fixes: those came
from a delivery that returned an error, not one that hung, and the two look
nothing alike in a goroutine dump. It is written separately so that U8i is not
read as having addressed it. "The lane cannot move past a record that is not
yet durable" is the right rule and still needs a ceiling, or a central that
never answers stops the lane indefinitely.

**A recovery attempt that could not look spends the budget as if it had.**
Recovery polls until the horizon runs out, and an attempt that failed to
observe — central unreachable so no read credential could be acquired, the
device unreachable, the credential refused — consumes an interval exactly like
an attempt that observed and found the change absent. Those are different
facts. The horizon is a statement about how long a device may take to show a
change, not about how long the observer may be unavailable, and spending it
while blind records "we could not look" as "we looked and it had not applied".

The consequence is a mutation that applied cleanly ending as a lane held for
an operator because something unrelated to the device was down. This is plan
1404's design rather than this plan's, and the fix is not obvious — an attempt
that cannot observe might not count against the horizon, or might count
against a separate allowance, and either changes what the horizon means. It is
written down because the argument is the part that will not survive being
rediscovered.

**An operator cannot see when a resolution will be accepted.** After
`AbandonMutation`, central refuses `ResolveDesynchronization` until the edge
has acknowledged how the mutation ended, which is right: resolving against a
mutation whose fate the edge has not accepted decides without the fact the
decision is about. But `GetDeviceAccessStatus` does not carry whether that
terminal acknowledgement is still owed, so the only signal an operator has
that resolving is safe is that the call stops being refused. They retry blind,
and the end-to-end test retries exactly as they would. The runbook carries it
as a step; the API wants a field.

**A policy names one read credential, and the read route wants two.** The
primary route is SNMP and the fallback is a shell login, so a device needs
both kinds of material to have both routes — and `RegistryPolicy` has exactly
one `read_credential`. The wire is not the obstacle:
`AcquireReadCredentialResponse.credential` carries a `CredentialMaterial`,
which is either arm, and its `ssh_host_key_sha256` is documented as "set
exactly when the material is a shell login" — so a shell-material read
credential was anticipated. What was not is a device wanting one of each.

So a deployment chooses which route can work. This device's reads are SNMP, so
its fallback can never open; a device whose read credential were a shell login
would have the mirror problem. After U8n the fallback stops breaking reads and
becomes a route that cannot succeed for the devices most likely to need it,
which is worth a decision rather than a quiet dead branch: a second handle on
the policy, or an explicit statement that a device has one read route and the
fallback exists only for devices whose read credential is a shell login.

**A lane freezes on a latency nobody can measure.** Two consecutive missed
heartbeats freeze a device's lane, and each attempt is bounded by the
heartbeat interval — so a central answering slower than one interval is
indistinguishable from a central that is down. Nothing records how long a
heartbeat takes. `RunHeartbeat` logs only the freeze and the restoration; the
success path records no duration at any level, so raising the agent to DEBUG
produces nothing (measured 2026-09-09: an agent at DEBUG across several
intervals, zero heartbeat lines, and none from central either). The mechanism
whose whole job is to notice a central that has become too slow decides on a
number no operator can see, and the first evidence is a frozen lane. The fix
is a duration recorded around each attempt, not a log level. Until then the
runbook says the measurement is unavailable rather than asking for it, and a
loopback round trip is not a substitute: this is a network-distance property,
and a fraction-of-a-millisecond figure from a single-host deployment would
misinform the decision it sits beside.

**The one operation with physical consequences is the least observable.** The
first shell login this system ever made to a real device opened, sent a
`port-name` command, and closed, and left no record: the session's open,
command and close are all below INFO in the agent, and central logs per-RPC
lines only on failure. The write was confirmed by the description having
changed, which is the only evidence there is. An operator debugging a write
that did not apply would have nothing to read. A mutation's shell session
should say at INFO that it opened, what it sent, and that it closed —
measured on the live run of 2026-09-09, where the whole trail was the status
API plus two lane lines.

**A second witness needs a clock, and ours did not have one.** The live write
was watched independently over SNMP from another session, to cross-check when
the change actually landed against when the lane said it did. That check could
not be made: the watch loop polled every three seconds and printed elapsed
time from its own start without ever stamping that start against the wall
clock, so the change is known to have been detected within a three-second
window that cannot be placed against the lane's observation timestamp. No
discrepancy was found and none was ruled out, which are different results. Any
future second witness against live hardware records absolute time, not elapsed.

**An operator is told the edge failed when the edge did not.** A read that
fails anywhere in the lane's closure surfaces as `"the edge could not read
this interface"` with an empty detail, while the wire code naming the actual
cause — `agent/device-session` — reaches only central's DEBUG log. In the
defect U8n fixes, the edge is behaving correctly and is the component the
message accuses; anyone meeting it goes to the agent and finds nothing wrong.
One message covering two causes is this build's recurring shape, and a message
that points at the wrong component is worse than a vague one.

**A read's shell fallback is handed the read credential.** When the SNMP
route fails and the interface read falls through to the shell, the lane opens
that session with the credential it acquired for the read. For a device whose
read credential is SNMP material — which is the ordinary case, since the
identity probe acquires under the same handle — that is material an SSH login
cannot use, so the fallback route cannot succeed for exactly the devices most
likely to need it.

It fails safely rather than doing anything unsafe, and it is reachable only
after the SNMP read has already failed, which is why it is here rather than in
a unit. Found while fixing the mutation observation's policy handle and
deliberately not followed: a third defect found while fixing a second is a
thing to write down, not to chase. What it needs is a decision about whether a
policy names one credential per route or whether the fallback is only for
devices whose read credential is a shell one.

Carried out of U2 (edgebus), to log rather than lose:

- A raw-frame security probe that constructs an actual JetStream
  flow-control control message (inlined `NATS/1.0 100 ` status, empty body)
  against the hub's WebSocket listener, to exercise the reflection path end
  to end. It needs a hand-rolled WebSocket handshake and raw HPUB frame,
  since the `nats.go` client cannot emit an inlined status; containment does
  not rest on it (the account boundary and `TestAccountsCarryNoImportsOrExports`
  do), so it is belt-and-braces, not a debt.
- At `compound`, two edgebus learnings alongside the `ds.draining` release
  rule: per-account JetStream `DiskStorage` reserves against the shared
  `JetStreamMaxStore`, so budgets that sum to the store ceiling deny a later
  account "jetstream not enabled" (a scaling failure a second edge surfaces);
  and a hub that has forgotten its edge accounts cannot let a leaf
  re-authenticate, so a restart must re-attach every persisted edge, which
  only a restart test finds.

## Open questions

None. The four questions of the first draft (leaf node, central-owned
drift, abandonment as a terminal ack, reads consuming sequences) were
decided by the coordinator on 2026-09-06 and are recorded in Decisions;
the leaf-node answer reversed the draft's recommendation, and the user's
later rule that the bus carries observability only moved every decision
path to ConnectRPC while the leaf node stays in this slice as the
buffered carrier.
