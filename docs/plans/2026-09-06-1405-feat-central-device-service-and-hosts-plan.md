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
    `restore` after abandonment; the audit stream holds the expected kinds
    in order.

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
open mutation's state — and the question it turns on is what happens to a
mutation already in flight against the old address and the old horizon at
the moment the update arrives. It finishes under the values it started
with, it is refused, or it is re-targeted mid-operation, and only the
first two are defensible: this is the same hazard that decided against
riding the address on the credential response, where a mutation's
`Execute` and its verifying `Observe` acquire separately and an address
that changed between them reports a device verified that was never
touched. Whichever answer this unit takes, the horizon a recovery poll is
already running under must not change under it.

Tests: an update that leaves a queued item and a running drainer alone; a
device whose horizon is corrected mid-life accepting a mutation it
previously refused; an update arriving while a mutation is in flight,
asserting which values that mutation finished under.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- $(git ls-files -co --exclude-standard 'src/modules/localnet/access/**' 'src/edge/agent/**')`

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
`mutation/firmware-epoch`), which requires the lane module to export the
ones it emits — a code that crosses a process boundary is public
contract whatever package produces it. This is the real link the U4
review's finding 9 left as documented constants.
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
