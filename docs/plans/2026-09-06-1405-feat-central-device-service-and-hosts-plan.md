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
  registry does not list, which dispose the mutation `REJECTED`. A
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
   recorded state; two concurrent `Admit` calls on one device yield n and
   n+1.
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
   way `RELEASED` does and never re-disposing; a disposal with
   `dispatched` unset walking to `RELEASED` in the same write; a stale or
   duplicate report is ignored. Example: a retry after a `RECOVERING` report opens a second
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
8. `EdgeService`, `DispatchService`, `AuditService`, and
   `EdgeAdminService` are served behind an HTTP middleware that verifies
   the assertion against the raw request body and path before Connect
   decodes it, for unary and stream-open calls alike;
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
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto docs/architecture`

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
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus src/modules/README.md`

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
dispatch refused `access/desynchronized` first; each `open_reads` state
including a colliding poll and an expired entry; every `Refused` code
against each row it can answer), one row per state naming the rows owed
and each row's terminating condition, plus one case asserting the
invariant over the whole table: every state either owes something,
names the operator as its terminator, or is closed, and no state owes
two rows for one sequence. Exactly two states land on the operator arm,
each naming `ResolveDesynchronization` as the RPC that ends it: the
abandoned mutation whose terminal ack is confirmed, and the held
reconciliation intent under `OPERATOR_MANAGED`. Both owe nothing by
design; a state that owes nothing without naming its terminator fails
the invariant, which is what keeps a recovering record with cleared
confirmations from passing as intentional. A state added without a
table row fails the exhaustiveness check.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/journal`

### U4. Dispatch, report, and audit handlers
Files: `src/services/device/internal/dispatchapi/`, `internal/auditapi/` and tests
After: U2, U3
Change: the `Subscribe` handler over `Owed()` per requirement 3; the
`Report` handler per requirement 5; the `Deliver` handler per
requirement 4.
Tests: requirements 3 and 4; requirement 5 is proven at the journal in U3
and exercised here through the handler.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/dispatchapi src/services/device/internal/auditapi`

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
Tests: requirement 8; the README's idempotent-enroll and stolen-key cases.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal spec/proto/flowseer/api/edge/v1`

### U6. DeviceService handlers and drift
Files: `src/services/device/internal/deviceapi/`, `internal/drift/`,
`src/services/device/README.md` and tests
After: U3, U4, U5
Change: requirements 6 and 7; `errs` codes map to Connect codes per the
error-wire record; the owed-row table in `src/services/device/README.md`.
Tests: each RPC's happy path and its refusal cases; the drift table for
both modes and all three resolution arms.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal`

### U7. Central host
Files: `src/services/device/cmd/device/main.go`, `src/services/device/internal/host/`,
`src/services/README.md`
After: U6
Change: `service.Run` with modules `hub`, `forwarder`, `journal`,
`connect`, `drift`;
prototext config; self-signed certificate on first start with its SPKI
pin printed once; no local bus is declared.
Tests: config validation; a start-and-serve smoke test.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device`

### U8. Edge host
Files: `src/edge/agent/cmd/agent/main.go`, `src/edge/agent/internal/{identity,busattach,dispatch,lanehost}/`,
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
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent`

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
Tests: requirement 10.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/test/integration docs/runbooks deploy/lab`

## Verification

```bash
buf format -d --exit-code && buf lint && buf generate
go build ./... && go vet ./...
go test -race ./src/modules/localnet/access/... ./src/modules/edgebus/... ./src/services/device/... ./src/edge/agent/... ./test/conformance/proto/...
.claude/skills/verify-change/scripts/verify-change.sh --full
```

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
