# Device service

Central's side of verified device access. An operator asks this service to
change an interface description or read one; it records the intent durably,
hands it to the edge that hosts the device, and holds the answer. It also
polls what it manages, so a description that stops matching what central
expects is noticed rather than discovered.

Nothing here touches a device. Every device operation happens on an edge,
over the lane in `src/modules/localnet/access`; this service decides what
should happen, records it before it does, and says whether it did.

## The record is the outbox

Central owes an edge messages — dispatch this mutation, checkpoint it,
acknowledge how it ended, clear that hold. None of them is queued. What
central owes is a pure function of the device's lane record
(`journal.OwedRows`), so any replica computes the same set after any restart,
and a message that was never delivered is not lost because it was never
stored anywhere to lose.

This is the design decision the rest of the service rests on, and the one a
reader cannot recover from the code: `record.go` says what is owed, and only
this can say why that set and not another. A queue beside the record would be
a second source of truth, and the two would disagree exactly when it mattered
— after a crash between writing the record and enqueuing the message. There
is no such window here, because there is nothing to enqueue.

Every row is independent, and more than one can be owed at once: a mutation
and several reads on the same device are unrelated messages that happen to
share a lane. Nothing ranks them.

The one mutation's own rows are the exception, and they are ordered rather
than independent: a mutation owes an `ExecuteRequest` or a
`CheckpointRequest`, never both. Re-dispatch has to precede the checkpoint,
so the checkpoint row is only reached once `dispatch_confirmed` is set. It
matters after an `Onboarded` report clears both confirmations on a record at
`POSSIBLY_APPLIED`: what is owed then is the `ExecuteRequest` alone, carrying
`resume`, and not a checkpoint for a dispatch the edge has not acknowledged.

### What a record owes

| Row | Owed while | Stops on |
| --- | --- | --- |
| `HoldResolved` | the sequence is in `hold_resolution_pending` | `HoldResolvedAck` for that sequence, or a `Refused` answering it, whatever the code: an edge that refuses holds nothing for that sequence |
| `ExecuteRequest` | an open mutation has no disposition, its block reason permits dispatch, and `dispatch_confirmed` is unset | the edge's `ADMITTED` report, which sets `dispatch_confirmed` and moves the phase to `POSSIBLY_APPLIED` in the same write |
| `CheckpointRequest` | `dispatch_confirmed` is set, the mutation's phase is `POSSIBLY_APPLIED`, and `checkpoint_confirmed` is unset | `CheckpointAck`, or a `Refused` carrying `access/no-pending-wait` while the last reported phase is at or past `POSSIBLY_APPLIED` |
| `TerminalResultAck` | the mutation has a disposition and `dispatched` is set | the edge reporting `RELEASED` or `ABANDONED`, or refusing because it holds no machine or the machine is already terminal |
| `ExecuteRequest` (read) | an `open_reads` entry has no outcome and its deadline has not passed | the report that closes it, or the sweep that closes it with a deadline error |

`ExecuteRequest` for a mutation carries `resume` exactly when the phase is
past `ADMITTED`. An edge that restarts loses its lanes and reports
`Onboarded`, which clears the dispatch and checkpoint confirmations and
nothing else — so the rows above re-derive, and a command the edge may have
sent resumes into recovery rather than being sent twice.

### What owes nothing, and why that is not a stall

Three states owe no row, and each names what ends it instead. A state that
owed nothing with nothing to end it would be a lane held forever by a record
nobody is acting on, which is the failure this table exists to make visible.

- **A blocked mutation.** `DESYNCHRONIZED`, `RECOVERY_HOLD`, `EDGE_STALE`
  and the rest do not permit dispatch. The terminator is an operator calling
  `ResolveDesynchronization` or `AbandonMutation`; central sends nothing and
  waits.
- **An abandonment the edge has confirmed.** Its terminal acknowledgement is
  no longer owed, and the mutation stays in the record with `RECOVERY_HOLD`
  until an operator resolves it by that sequence.
- **A read past its deadline.** It owes no row because it is due for the
  sweep, which closes it with a deadline error. The promise is one the relay
  keeps by running the sweep, not one the row keeps by existing.

A retryable row does not end itself. An edge that never comes back leaves its
`ExecuteRequest` owed until an operator ends the mutation, which is
deliberately unlike a read: a read expires because its caller is waiting and
will be told, and a mutation does not because abandoning an operator's change
is a decision only they can make.

## What a mutation's disposal owes

A disposal that the edge never held — a drift intent held at `ADMITTED`, a
terminal refusal before admission, an abandonment of a mutation whose edge
never came back — walks `REJECTED` or `INDETERMINATE_ABANDONED` through
`ACKNOWLEDGED` to `RELEASED` in the same write. The lane is never left
blocked with nothing owed and nothing to wait for.

A resolution refuses while the terminal acknowledgement is still owed. The
edge is waiting to be told how its sequence ended, and closing the record
would withdraw the row that tells it, leaving the edge parked while central
dispatched a replacement into a lane it still occupies.

## Reads share the lane's order

A read draws its sequence from the same counter as a mutation, so a device
has one order and not two. That costs two record writes per read — one to
open, one to close — and the write budget is sized against it: two writes per
managed interface per poll interval, each rewriting a few kilobytes. A
48-port switch with every port managed is 96 writes per interval. A
deployment with many managed interfaces lengthens `intervals.drift`
accordingly.

A read of an interface central holds no expectation for leaves no lasting
row. Only a verified mutation creates an expectation, and only an interface
with one is judged for drift: adopting whatever the first read happened to
find would be central inventing an expectation nobody asked for.

## Drift is central's

Both halves of the comparison are central's. The expectation is what a
verified mutation left behind, and the management mode that decides what to
do about a difference is the operator's configuration. An edge deciding for
itself would compare against a value that goes stale the moment an operator
accepts an observed state, and would then report every later read as drift.

The poll dispatches an ordinary read per managed interface, judges what the
last one returned, and on a difference writes the audit record first and
reports the signal after. Under `AUTHORITATIVE` it admits a reconciliation
intent and dispatches it; under `OPERATOR_MANAGED` it admits one held
`DESYNCHRONIZED`, which takes the lane and waits. A device whose lane already
holds a mutation is skipped whole, abandoned ones included: central's own
change is the obvious explanation for a difference, and reporting it would be
central detecting itself.

## Deployment

The operator surface (`DeviceService`, `EdgeAdminService`, and `CaptureService`)
enforces authentication and authorization on every call. Each request carries a
bearer token verified against configured OIDC issuers (`internal/authn`) and a
tenant identifier in the `X-FlowSeer-Tenant` header. Calls are checked against
OpenFGA (`internal/authz`, `internal/authz/openfga`) per the service options.

Connect decodes the request before any interceptor runs, so an unauthenticated
caller can still send a large body. `CaptureEdgeService.UploadCapture` is mounted
in front of the assertion middleware too, and carries its own per-message bound.

Every call admitted by authorization on `EdgeAdminService` and `CaptureService`
records its attempt and completion into the operator action trail
(`FLOWSEER_OPERATOR_ACTIONS` stream, published on
`flowseer.<tenant>.operator.action.<action>` by `internal/actiontrail`). An
unauthenticated or unauthorized call leaves no action trail entry, avoiding
trail pollution by unverified callers.

The edge-facing services (`EdgeService`, `DispatchService`, `AuditService`,
`CaptureEdgeService`) are verified: every call carries a fresh assertion signed
by the key central registered at enrollment, checked against the request body
and the invoked procedure before Connect decodes anything.

Two procedures are exceptions. `Enroll` carries its own proof, because it
happens before central holds a key. `UploadCapture` carries its assertions as
messages on the stream rather than as a header: there is no whole body to hash on
a stream an edge holds open for the length of a capture, so its opening
assertion is checked from the stream's first message and a fresh one has to
arrive inside every 60-second window, with a read deadline closing the stream
when none does. Both are bounded explicitly in place of the middleware's limit.

The service reads one prototext `DeviceServiceConfig`
([schema](../../../spec/proto/flowseer/store/device/v1/README.md)) and takes its
certificate from it or generates a self-signed pair on first start, printing the
digest an edge pins. That digest is what every edge is provisioned with, so the
pair is persisted: a service that generated a fresh key each start would refuse
every edge in the field.

Seven modules run under the service runtime: the bus hub, the telemetry
forwarder, the journal's read sweeper, the Connect listener, the drift poll, the
capture artifact sweeper, and the relationship projector (`internal/projector`),
supervised `RestForOne` with the hub first. That is not a default: the six
modules after it hold resources the hub owns, so a hub that is rebuilt must take
them with it.

## Remote packet capture

Central coordinates bounded packet captures run by edges. An operator requests
a capture session via `CaptureService` (`CreateCaptureSession`), bounding it by
packet count, byte count, or duration. The session record is persisted in
JetStream KV (`captures` bucket) in the `PENDING` lifecycle.

What central owes an edge is derived from open session records:
`CaptureEdgeService.SubscribeCaptureAssignments` derives owed assignments for
the calling edge (start for `PENDING` sessions, stop for cancellations).

Packets stream back over `CaptureEdgeService.UploadCapture`, which authenticates
via in-stream `SignedEdgeAssertion`s rather than HTTP header assertions, and
whose stream is closed by a read deadline when one window passes without a
fresh assertion — an edge that simply goes quiet is the case a check on arrival
would never see. Every chunk's `session.edge` must name the calling edge. On
the first chunk upload, central transitions the session to `RUNNING`,
withdrawing the start assignment. Uploaded packets are appended into a retained
pcapng file on central at `<StateDir>/captures/<tenant_id>/<session_id>.pcapng` and
broadcast in memory to active `TailCaptureSession` subscribers. A tail slower
than the upload loses chunks rather than stalling it, and central logs when it
does.

When the edge marks upload complete (`final: true`), central finalizes the
pcapng artifact, syncs it, writes the SHA-256 digest, byte size, and packet
count to the session state, and sets the retention expiration. From then on the
session is closed to further uploads: a second stream naming it is refused, so
the stored bytes and the digest describing them cannot diverge. Operators
download stored pcapng files in chunks up to 1MiB via
`DownloadCaptureSession`, which answers from the session record rather than the
file — a capture still running is `FailedPrecondition`, one whose payload is
gone is `NotFound`.

A background sweeper module (`capture_sweeper`) unlinks pcapng payload files
past their `expires_at` and stamps `purged_at` on the artifact descriptor,
leaving the session record and its counters. That is the capture direction
record's retention rule, which departs from retire-is-not-purge deliberately:
the record is what an audit needs, the payload is what an audit is about.

## Layout

| Package | What it owns |
| --- | --- |
| `internal/journal` | the lane record, every write to it, and the owed-row derivation |
| `internal/registry` | the operator-written device registry, read once at start |
| `internal/edgestore` | the edge records and the setup-key index |
| `internal/credential` | the mounted credential files |
| `internal/edge` | the assertion verifier |
| `internal/edgeapi` | `EdgeService`, `EdgeAdminService`, and the assertion middleware |
| `internal/dispatchapi` | `DispatchService`: the outbox relay and the report handler |
| `internal/auditapi` | `AuditService`: the edge's audit deliveries onto the stream |
| `internal/deviceapi` | `DeviceService`: the operator's calls |
| `internal/captureapi` | `CaptureService`, `CaptureEdgeService`, pcapng artifact store, and live tail broadcaster |
| `internal/centralaudit` | the audit records central writes on its own behalf |
| `internal/drift` | the poll that compares a managed interface against its expectation |
| `internal/connecterr` | the errs-to-Connect mapping every handler answers through |
| `internal/authn` | the token verifier, interceptor, and caller identity carrier |
| `internal/authn/authntest` | test TLS OIDC issuer and token minting |
| `internal/authz` | the operator authorization obligations |
| `internal/authz/openfga` | the OpenFGA authorization engine checker and embedded model |
| `internal/authz/authztest` | test in-memory authorization engine and relationship recorder |
| `internal/telemetry` | this service's instrumentation scope |
| `internal/host` | configuration, certificate, interceptors, and the module assembly |
| `internal/actiontrail` | the operator action trail interceptor and JetStream publisher |
| `internal/projector` | relationship projection from store records to the authorization engine |
| `internal/tenantstore` | active tenant organization lookup |
| `test/integration` | end-to-end integration and runbook tests |
