---
title: Edge Delivery over Connect, Phase 4, Removal and Record Amendments - Plan
type: refactor
date: 2026-10-04
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-10-03-central-high-availability-direction.md, docs/architecture/2026-10-03-deployment-direction.md
---

# Edge Delivery over Connect, Phase 4, Removal and Record Amendments - Plan

## Goal

Nothing in the tree serves or describes the leaf link. The means are
deleting the WebSocket listener, the per-edge accounts and minted users,
the per-edge streams with their follower and forwarder consumers,
`AttachBus`, and `ServiceListeners.bus`, and amending the records and
documents that describe them.

Stop condition: the plan is wrong if a central module still needs a
per-edge stream after the agent has switched. On 2026-10-16 none does:
the only readers are `intake.attach` (`src/services/device/internal/intake/intake.go:158`)
and `Forwarder.attach` (`src/modules/edgebus/forwarder.go:136`), which
`Handler.Deliver` (`src/services/device/internal/intake/handler.go:97`)
and `src/services/device/internal/otlpapi` replace. The tenant comes from
the edge store (`src/services/device/internal/host/host.go:379-388`).

## Decisions

The parent's decisions apply
([parent plan](2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md)).
This phase adds:

- `EdgeService.AttachBus`, `bus.proto`, `ServiceListeners.bus`, and
  `EdgeEnrollmentPolicy.cluster_urls` are deleted with no `reserved` line.
  Why: `docs/code-style-proto.md` ("Evolution") forbids tombstones before
  the first stable release, and a reserved name would make prototext skip
  `bus:` silently instead of refusing it
  (`google.golang.org/protobuf@v1.36.12/encoding/prototext/decode.go:205-209`).
  `cluster_urls` goes too, since it only feeds `AttachBus`
  (`spec/proto/flowseer/store/device/v1/service_config.proto:401-409`).
  This breaks the wire and every central configuration that sets either
  field. The lab file changes with it.
- The embedded server keeps operator mode with the operator, system, and
  CENTRAL keys, and `mintUser` stays for the central user minted on each
  connect (`src/modules/edgebus/hub.go:351-361`). The per-edge keys,
  `.tenant` files, and their readers go.
- An upgraded central leaves its `keys/edge-*.nk` and `keys/edge-*.tenant`
  files and the former edge accounts' JetStream directories on disk, and
  the edgebus README tells an operator to delete them. Nothing in central
  reads the key files. The server does read the directories: at every
  start it walks its store directory and looks up each account it has not
  loaded (`nats-server/v2@v2.15.0/server/jetstream.go:933-947`). The
  `MemAccResolver` lookup fails, the server logs `Account fetch failed`
  once per directory (`server/server.go:2183-2185`), and the hub passes
  that warning to its logger (`src/modules/edgebus/hub.go:899`). Start is
  not refused, since the branch at `jetstream.go:940` needs a non-nil
  account. Why no removal code: the agent leaves its old `jetstream`
  directory the same way (edge delivery record, 2026-10-16 amendment),
  no deployment outside the lab exists, and code that deletes store
  directories it cannot identify risks the CENTRAL store.
- `src/services/device/internal/intake` keeps the code `handler.go` uses
  (`Publisher`, `ErrCodeStart`, `prepareRecord`, `publication`,
  `recordTypeAndDevice`, `newMetrics`, `allowEvery`, and the reason and
  log-key constants it reads), moved into `prepare.go`. The stream
  consumer (`Config`, `Intake`, `Start`, `handle`, the gate, `prepare`,
  the age histogram) goes, with the constants only it read
  (`reasonForeignSubject`, `reasonMalformed`, `reasonUnknownEdge`,
  `consumeLogKey`), since `unused` is enabled (`.golangci.yml:10`).
- `src/modules/edgebus/cleanup.go`, `collector.go`, and `fsync.go` stay.
  `cleanup.go` closes the typed ingest streams' consumer membership and
  holds nothing per edge. `otlpapi` uses `collector.go`, and `hub.go`
  uses `applyFsync`.
- Accepted records (device service, ingestion) change by a dated entry
  under `## Amendments` that names each passage it replaces, as the
  ingestion record's 2026-10-08 entry does. Proposed records (central high
  availability, deployment) are edited in the body, since a proposed
  record binds nothing (`docs/architecture/README.md`). The edge high
  availability record holds no leaf text and is not edited. The edge
  delivery record's Consequence about it is answered in that record's
  landing amendment, not by editing its body.
- Captured solutions:
  `a-restarted-hub-must-re-attach-every-persisted-edge-account.md` is
  removed. The CENTRAL account JWT is signed and stored on every start
  (`src/modules/edgebus/hub.go:234-251`) from a key that always loads
  (`src/modules/edgebus/keys.go:57`), so nothing persisted needs rebuilding.
  `per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`
  is rewritten around the CENTRAL account. CENTRAL keeps a disk limit
  (`src/modules/edgebus/keys.go:283-289`), and the server still checks it
  against `MaxStore` and marks the account incomplete on refusal
  (`nats-server/v2@v2.15.0/server/jetstream.go:2719-2742`,
  `server/accounts.go:3924-3930`). The central high availability phase 2
  plan expects a second JetStream account to join the same sum.
  `conventions/a-concurrency-hook-must-prove-lock-ownership-not-only-pause-the-race.md`
  is removed, since its only instance, `edgeAccountAttachHook`, is deleted
  and no other hook in `src/` is documented to run under a lock.
- Dependency statements: `github.com/nats-io/jwt/v2` and
  `github.com/nats-io/nkeys` stay direct dependencies, imported by
  `src/modules/edgebus/keys.go` and `hub.go` after U3. Their reasons hold.
  Both statements cite `keys.go:10-11`, which moves once U3 drops the
  imports only edge code used (`errors`, `strings`, `sync`, `tenant`), so
  both re-cite the import lines U3 leaves. The jwt statement's "edge-bus
  key handling"
  (`docs/dependencies/statements/go/github.com/nats-io/jwt/v2.md:21`)
  becomes the operator, account, and user claims of the central hub. The
  nats-server statement's import site `leaf.go:12`
  (`docs/dependencies/statements/go/github.com/nats-io/nats-server/v2.md:13`)
  moves to `hub.go`. The convention does not say whether a text edit
  clears `approved` (`docs/conventions/dependencies.md:7-9`), so the edit
  leaves the field as it is and the phase's handoff names the three
  statements for the user to re-read.
- Open plans that assume the leaf link are not re-planned here. Central
  high availability phase 2 names this phase in its `after`, phase 3 and
  the deployment phases wait on it through that chain, and each carries
  `needs-decisions` and the re-planning line. Each re-plans against the
  amended records when its turn comes. Edge high availability phase 1 has
  no leaf assumption. Changing another plan's text or `after` is the
  user's call, so the stale passages are listed under Open questions.
- `docs/research/` and `docs/plans/` are outside requirement 4.
  Research files are dated evidence (`docs/README.md`, "Authority and
  freshness"), and open plans change when they are re-planned. The edge
  delivery record's body is outside it too, since its Context, Decision,
  and Alternatives describe the leaf link as the thing removed.
- `GOALS.md:57-58` and `:75-78` change in U4 with the device service and
  ingestion amendments. Why: both lines cite those accepted records, and
  once the amendments land they would contradict them. The edge delivery
  record's "That line changes when this record is accepted" assumed
  acceptance would come first. Acceptance itself is asked at this phase's
  handoff, as the parent and `.claude/skills/plan/references/direction-record.md`
  say. Only a person sets `accepted-direction`, so no unit edits the
  record's status or its index row's status.
- The units form a chain. Each removes the callers of what the next one
  deletes: the host stops starting intake and the forwarder before intake
  loses its consumer, and intake stops calling `FollowEdges` and
  `EdgeTenant` before edgebus deletes them. The record and solution units
  cite the code U3 leaves, so they come last.

### Overlap with other work

| Work | Shared files | Handling |
| --- | --- | --- |
| `docs/plans/2026-10-04-2023-fix-central-intake-review-items-plan.md` (implementation-ready, no branch) | `follower.go`, `follower_test.go`, `hub.go`, `edgebus/README.md`, `intake.go`, `intake_test.go`, `intake/README.md`, two records, the budget solution | Its U1 follower changes and its U2 consumer tests (R1, R2, R8, the consume-error limiter) die with U2 and U3 here. Its `hub.go` comment, the README window text, R3 to R7 (which hold in `prepareRecord` and the handler), and its `host_test.go` directive survive. Whichever lands second merges. Whether it lands first or is re-planned is the user's call (parent Open questions). |
| `docs/plans/2026-10-15-1542-perf-hub-periodic-fsync-plan.md` (implementation-ready, no branch) | `hub.go`, `hub_test.go`, `fsync.go`, `edgebus/README.md`, `handler.go`, `intake/README.md`, the device service and edge delivery records | It edits code this phase keeps. Neither needs the other, so whichever lands second merges. Its decision that `NormalizeFsync` stays "since the leaf and local bus have no async-persist streams" names the leaf. |
| Branch `Aledantee/bp-streams` (also `parked/bp-streams`, tip `06adf5e0`, 2026-10-14) | `edgebus/{hub.go,cleanup.go,cleanup_test.go,hub_test.go,edgebus_test.go,README.md}`, `intake/{intake.go,intake_test.go,README.md}` | It is not the Postgres read model branch, whose state names `codex/postgres-state-plan`. Its edgebus and intake hunks are an unverified checkpoint of the bounded ingest backpressure work, which landed on `main` and was retired in `5a15fc4e`. `06adf5e0` is not an ancestor of `main`. No plan claims it, so this phase adds no `after`. Its `intake.go` hunks have nothing to apply to once U2 lands. |

- U3 re-points the operator-authorization record's line citations into
  `src/modules/edgebus/subjects.go` and `hub.go`
  (`docs/architecture/2026-09-30-operator-authorization-direction.md:485`
  and `:869-870`) to the lines they name in the reduced files. Only the
  citation text changes, and nothing the record decides. Why:
  `TestTheDirectionRecordCitationsAndDecisions`
  (`src/services/device/test/integration/lab_fixtures_test.go:707-755`)
  fails on a citation past the end of its file, and U3 shortens
  `subjects.go` below line 108. (decided by the user, 2026-10-16)

## Requirements

The parent's requirement 8 is claimed here.

1. Central binds one edge-facing listener. Example: a central
   configuration with `listeners { bus: "0.0.0.0:7422" }` fails to load
   with an unknown-field error naming `bus` (the default
   `prototext.Unmarshal` at `src/services/device/internal/host/config.go:56`),
   and the hub's server options carry no WebSocket and no leaf port.
2. The schema holds no bus attachment. Example:
   `spec/proto/flowseer/edge/attach/v1/bus.proto` does not exist,
   `go tool -modfile=tools/buf/go.mod buf lint` passes, and
   `git grep -n AttachBus -- src spec test` prints nothing.
3. No per-edge NATS state is created. Example: after an edge enrolls and
   delivers a syslog record in the end-to-end test, central's
   `<state_dir>/keys` holds no file named `edge-*`. A started hub's server
   lists only `$G`, the system account, and CENTRAL (`Accountz`,
   `nats-server/v2@v2.15.0/server/monitor.go:2799`, with `$G` registered at
   `server/server.go:1243-1245`).
4. The documents match the tree. Example: `git grep -l -i -E
   'leaf node|leaf link|AttachBus|FLOWSEER_EDGE_<|listeners\.bus|cluster_urls'
   -- '*.md' '*.proto' ':!docs/research' ':!docs/plans'
   ':!docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md'`
   lists only the two accepted records' passages that a dated amendment names as replaced,
   the amendment text itself, and lines that state the leaf link is gone.

## Out of scope

- The process-local bus in `src/common/service`.
- Renaming `src/modules/edgebus`.
- Removing orphaned edge key files and JetStream directories on an
  upgraded central (Decisions).
- Line citations into `hub.go`, `keys.go`, and `subjects.go` in records
  and research other than the ones this phase edits and the
  operator-authorization record (Decisions). They drift with the
  deletions.

## Units

### U1. Schema, edge API, and host wiring

Files: spec/proto/flowseer/edge/attach/v1/bus.proto, spec/proto/flowseer/edge/attach/v1/edge_service.proto, spec/proto/flowseer/edge/attach/v1/README.md, spec/proto/flowseer/api/edge/v1/README.md, spec/proto/flowseer/store/device/v1/service_config.proto, spec/proto/flowseer/store/device/v1/README.md, generated/go/proto/flowseer/edge/attach/v1/, generated/go/proto/flowseer/store/device/v1/service_config.pb.go, test/conformance/proto/edge_attach_rules_test.go, src/services/device/internal/edgeapi/service.go, src/services/device/internal/edgeapi/enroll.go, src/services/device/internal/edgeapi/errors.go, src/services/device/internal/edgeapi/errors_test.go, src/services/device/internal/edgeapi/service_test.go, src/services/device/internal/edgeapi/admin_test.go, src/services/device/internal/host/host.go, src/services/device/internal/host/serve.go, src/services/device/internal/host/certificate.go, src/services/device/internal/host/config.go, src/services/device/internal/host/certificate_test.go, src/services/device/internal/host/config_test.go, src/services/device/internal/host/host_test.go, src/services/device/internal/host/history_internal_test.go, src/services/device/internal/host/enrollment_internal_test.go, src/services/device/README.md, src/services/device/test/integration/e2e_test.go, src/services/device/test/integration/fixture_test.go, src/services/device/test/integration/bootstrap_env_test.go, src/services/device/test/integration/authz_enforcement_test.go, src/services/device/test/integration/testdata/mutation-verification-repro/fixture_test.go.repro, test/conformance/proto/layering_test.go, deploy/lab/central.textproto, src/edge/agent/internal/identity/assertion_test.go, src/edge/agent/host/host_test.go
After: none
Change: `bus.proto` is deleted, `EdgeService` loses `AttachBus` and the
import, `ServiceListeners` loses `bus`, and `EdgeEnrollmentPolicy` loses
`cluster_urls`, with no `reserved` line. Their comments stop naming the
leaf, per-edge accounts, and the bus (`service_config.proto:19-22`,
`:33-34`, `:310-312`, `:331-336`, `:367`). `buf generate` rewrites
`generated/`. `edgeapi.NewService` takes no bus, and `AttachBus`,
`BusMinter`, `ServiceConfig.ClusterURLs`, and `ErrCodeBus` go. Enroll no
longer calls `AttachEdge` (`enroll.go:123`, `:139`). The host drops the
intake and forwarder modules (`host.go:218-219`), `forwarderGate`,
`setupForwarder`, `setupIntake`, and the hub's listener fields, so the hub
binds no WebSocket listener (port 0 disables it, `src/modules/edgebus/hub.go:274`).
`BusAddress`, `ClusterURLs`, `portOf`, `listenHostOf`, and the bus SAN go.
The integration tests' per-edge user check (`e2e_test.go:994-1012`) and
the `bus` and `cluster_urls` lines go. The `hub.EdgeTenant` assertion in `admin_test.go:1034-1041` goes, since
enroll no longer attaches the edge. The `ListenPort: 0` lines in
`admin_test.go:136` and `enrollment_internal_test.go:42` go here, since
these files are this unit's and the zero value is the default. The device README lists two
listeners and the modules the host still runs. The proto READMEs and the
layering test's `EdgeService` comment (`layering_test.go:94`) drop the bus
attachment. The `.repro` fixture drops its `bus` and `cluster_urls` lines
(`fixture_test.go.repro:212`, `:217`) so it still loads when copied in.
The agent host test's `AttachBus` loop (`src/edge/agent/host/host_test.go:209-213`)
is deleted, since the procedure no longer exists.
Tests: `config_test.go` gains a case where a configuration carrying
`listeners { bus: ... }` is refused with an unknown-field error (R1).
`TestAttachBusResponseRules`, the `AttachBus` service tests,
`TestEnrollBusAttachFailureUnavailable`, and `TestIntakeRepublishesAnEdgeRecord`
are deleted. The Connect ingest and telemetry host tests
(`host_test.go:2108-2232`) and the edgeapi enroll tests stay green.
`assertion_test.go` signs another procedure in place of `AttachBus`.
Verify: `uv run tools/scripts/run.py verify change -- spec/proto/flowseer/edge/attach/v1 spec/proto/flowseer/api/edge/v1 spec/proto/flowseer/store/device/v1 test/conformance/proto src/services/device/internal/edgeapi src/services/device/internal/host src/services/device/README.md src/services/device/test/integration deploy/lab/central.textproto src/edge/agent/internal/identity src/edge/agent/host`

### U2. Intake becomes the ingest handler alone

Files: src/services/device/internal/intake/intake.go, src/services/device/internal/intake/prepare.go, src/services/device/internal/intake/handler.go, src/services/device/internal/intake/intake_test.go, src/services/device/internal/intake/prepare_test.go, src/services/device/internal/intake/handler_test.go, src/services/device/internal/intake/handler_bench_test.go, src/services/device/internal/intake/README.md
After: U1
Change: `intake.go` and `intake_test.go` are deleted. `prepare.go` holds
the helpers the Decisions list, and `handler.go`'s comment at `:47-49`
stops naming the stream consumer. The test helpers `handler_test.go`
and `handler_bench_test.go` borrow from `intake_test.go` (`startHub`,
`centralStreams`, `validEnvelope`, `validateEnvelope`, `metricValue`,
`testBindingID`, and the other test constants) move into `handler_test.go`.
The moved `startHub` and `handler_bench_test.go` drop `ListenPort` and
`EdgeBudgetBytes`, which U3 deletes. The `retried` and `duration` metric
descriptions (`intake.go:468`, `:472`) stop naming the stream path. The
README describes the `Deliver` handler, its settle rule, and its metrics,
with no follower or per-edge stream.
Tests: `prepare_test.go` ports the record proofs the handler suite lacks:
raw evidence (`intake_test.go:503`), window refs (`:549`), a validator
failure (`:117`), and two tenants (`:693`). `handler_test.go` passes
unchanged in its cases.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/intake`

### U3. Edgebus reduced to the CENTRAL account

Files: src/modules/edgebus/leaf.go, src/modules/edgebus/follower.go, src/modules/edgebus/follower_test.go, src/modules/edgebus/forwarder.go, src/modules/edgebus/forwarder_internal_test.go, src/modules/edgebus/storage_test.go, src/modules/edgebus/attribution_test.go, src/modules/edgebus/hub.go, src/modules/edgebus/hub_internal_test.go, src/modules/edgebus/keys.go, src/modules/edgebus/keys_internal_test.go, src/modules/edgebus/subjects.go, src/modules/edgebus/fsync.go, src/modules/edgebus/collector.go, src/modules/edgebus/doc.go, src/modules/edgebus/edgebus_test.go, src/modules/edgebus/README.md, src/modules/README.md, src/services/device/internal/otlpapi/handler.go, src/services/device/internal/otlpapi/README.md, src/services/device/test/integration/delivery_test.go, src/services/device/internal/accessstore/store_test.go, src/services/device/internal/auditapi/service_test.go, src/services/device/internal/captureapi/artifact_invariant_internal_test.go, src/services/device/internal/captureapi/operator_service_test.go, src/services/device/internal/captureapi/store_test.go, src/services/device/internal/deviceapi/deviceapi_test.go, src/services/device/internal/dispatchapi/relay_test.go, src/services/device/internal/drift/drift_test.go, src/services/device/internal/edgestore/store_test.go, src/services/device/internal/historysink/sink_test.go, src/services/device/internal/historysink/README.md, src/services/device/internal/identityapi/identityapi_test.go, src/services/device/internal/journal/journal_test.go, src/services/device/internal/projector/projector_test.go, src/services/device/internal/rawwindowapi/store_test.go, src/services/device/internal/tenantstore/claim_internal_test.go, src/services/device/internal/tenantstore/store_test.go, docs/architecture/2026-09-30-operator-authorization-direction.md
After: U1, U2
Change: `leaf.go`, `follower.go`, `forwarder.go`, and their tests,
`storage_test.go`, and `attribution_test.go` are deleted. `HubConfig`
loses `ListenHost`, `ListenPort`, `TLS`, `EdgeBudgetBytes`,
`EdgeStreamMaxBytes`, and `EdgeStreamMaxAge`. `Hub` loses its edge map,
attach lock, attach hook, the re-attach loop (`hub.go:321-345`), the
leaf and WebSocket options, `EdgeTenant`, `ListenURL`, `ListenPort`,
`ensureEdgeAccount`, `createEdgeStream`, `MintEdgeUser`, `AttachEdge`,
`AttachedEdges`, `EdgeStream`, `LeafCount`, and `ErrCodeStorage`.
`keys.go` keeps the operator, system, and central seeds and `mintUser`,
and loses the edge keys, `.tenant` files, and `validEdgeID`. `subjects.go`
keeps `OTelSignal`, the signal constants, `HubDomain` (whose comment stops
naming the leaf), and the central names, and loses `EdgeBufferStream`,
`HubEdgeStreamPrefix`, `EdgeSubtree`, `OTelSubject`, `IngestSubject`,
`EdgePublishSubjects`, `HubEdgeStream`, `edgeOfHubStream`, `belongsToEdge`,
`TenantFromSubject`, `TenantFromSubjects`, `EdgeDomain`, and `signalOf`.
`mintUser` returns an unexported credentials type in place of
`EdgeCredentials`. `fsync.go` and `collector.go` stop naming the leaf
and the forwarder. The edgebus README says what an upgraded central's
stray edge files are and that the operator deletes them. The historysink
README's "per-edge age" (`README.md:157`) names the bound it now means. `doc.go`, the edgebus README, and `src/modules/README.md`
describe a hub with one data account and no edge-facing listener. The
README keeps its CENTRAL account and stream sections. The other packages'
tests drop the removed `HubConfig` fields.
Tests: `hub_internal_test.go` proves the server options carry no
WebSocket and no leaf port (R1), takes the central half of
`TestAccountsCarryNoImportsOrExports` (`attribution_test.go:141`), proves
that `Accountz(nil)` lists only `$G`, the system account, and CENTRAL
after start, and proves that a hub whose state directory holds a stray
`keys/edge-x.nk`, `keys/edge-x.tenant`, and a former edge account's
JetStream directory starts, logs one `Account fetch failed` warning for
the directory, and serves CENTRAL. The leaf and edge tests in
`edgebus_test.go` are deleted. `keys_internal_test.go` keeps
its three-seed check. `delivery_test.go` replaces the `LeafCount` check
with R3: no `edge-*` file under central's `keys` directory after delivery.
The central tests in `edgebus_test.go`, `hub_test.go`, and
`cleanup_test.go` stay green.
Verify: `uv run tools/scripts/run.py verify change -- src/modules/edgebus src/modules/README.md src/services/device/internal/otlpapi src/services/device/test/integration/delivery_test.go src/services/device/internal/accessstore src/services/device/internal/auditapi src/services/device/internal/captureapi src/services/device/internal/deviceapi src/services/device/internal/dispatchapi src/services/device/internal/drift src/services/device/internal/edgestore src/services/device/internal/historysink src/services/device/internal/identityapi src/services/device/internal/journal src/services/device/internal/projector src/services/device/internal/rawwindowapi src/services/device/internal/tenantstore`

### U4. Record and schema prose amendments

Files: GOALS.md, docs/architecture/README.md, docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-10-03-central-high-availability-direction.md, docs/architecture/2026-10-03-deployment-direction.md, docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md, spec/proto/flowseer/edge/dispatch/v1/README.md, spec/proto/flowseer/integration/README.md, spec/proto/flowseer/integration/ingest/v1/README.md, src/protocol/syslog/README.md
After: U3
Change: the device service record gains a dated amendment that replaces
its leaf Decision paragraph, the transport diagram, the 2026-09-06
paragraph's "leaf's local buffer", the Events bullet's edge branch, "Edge
attachment and enrollment", the enrollment RPC sentence, "Exposing NATS",
"Edge leaf buffers", the local bus line, sequencing step 4, and the leaf
open question and sources. The ingestion record gains a dated amendment
that replaces its Context, the tenant-from-stream sentence, the stage
diagram, the intake paragraph, the open question, and the per-edge stream
sentences of its 2026-10-04, 2026-10-14, and 2026-10-15 amendments, and
the `forwarder.go` source (`:199`).
Intake is the handler of the ingest call. The central high availability
record drops per-edge claims, the WebSocket listener, and the forwarder,
and central is a plain client of the cluster. The deployment record
exposes one edge-facing address and drops the wss leaf link, the NATS
keys, and the open question about them. The edge delivery record gains a
landing amendment naming the removed paths, the kept CENTRAL account, the
orphaned files and their start warning, the `GOALS.md` change, and that
the edge high availability record needed no edit. `GOALS.md` states
Connect as the edge delivery path in place of NATS as the fabric
(`:57-58`) and no longer says intake reads only per-Edge streams
(`:75-78`). The index row's Read-when for the edge delivery record stops
naming `AttachBus` and the bus listener as present (`docs/architecture/README.md:60`). The proto READMEs say ingest records travel in `Deliver`. Amendments
cite files and symbols, not line numbers in code this phase changes.
Tests: none. `verify change` runs `check-prose` and
`check-markdown-links` on every file. The R4 grep is run by hand.
Verify: `uv run tools/scripts/run.py verify change -- GOALS.md docs/architecture spec/proto/flowseer/edge/dispatch/v1/README.md spec/proto/flowseer/integration src/protocol/syslog/README.md`

### U5. Solutions and dependency statements

Files: docs/solutions/architecture-patterns/a-restarted-hub-must-re-attach-every-persisted-edge-account.md, docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md, docs/solutions/conventions/a-concurrency-hook-must-prove-lock-ownership-not-only-pause-the-race.md, docs/solutions/architecture-patterns/state-a-transient-refusal-must-not-block-is-state-nothing-retries.md, docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md, docs/solutions/architecture-patterns/a-multi-key-uniqueness-claim-needs-one-conditional-batch.md, docs/solutions/README.md, docs/dependencies/statements/go/github.com/nats-io/jwt/v2.md, docs/dependencies/statements/go/github.com/nats-io/nkeys.md, docs/dependencies/statements/go/github.com/nats-io/nats-server/v2.md
After: U3
Change: the re-attach and concurrency-hook solutions and their index rows
are deleted. The disk-budget solution states the CENTRAL budget's rule:
it must fit under the store ceiling, a ceiling taken from shrinking free
disk can refuse CENTRAL at restart, and the refusal surfaces as the
`create key-value bucket` hub error. It links the stream-sum check
(`src/services/device/internal/host/config.go:70-79`,
`TestHubRefusesACentralBudgetBelowTheStreamSum`) instead of restating it,
cites nats-server v2.15.0 lines, and its index row drops the edge count
and `edgebus/storage` triggers. The transient-refusal solution loses its
two links to the deleted file, and its `Account fetch failed` trigger
names the one case left, a former edge account's directory on an
upgraded central. The local-bus solution names the hub alone. The uniqueness
solution's `hub.go` citation matches the tree. Each checked solution gets
`last_verified`. The two statements change as the Decisions say.
Tests: none. `verify change` runs `check-markdown-links` on each changed
file, and `test/conformance/dependencies/statements_test.go` checks the
statements.
Verify: `uv run tools/scripts/run.py verify change -- docs/solutions docs/dependencies/statements/go/github.com/nats-io`

Waves: U1 | U2 | U3 | U4 U5

## Verification

- `uv run tools/scripts/run.py verify change -- <every changed path>`.
- `go test -race ./...` passes.
- `go tool -modfile=tools/buf/go.mod buf lint` passes.
- The R2 and R4 greps print what the requirements say.
- The parent's lab run: `deploy/lab` enrolls one agent, a syslog datagram
  from a lab device reaches its typed stream, and central listens on the
  API and edge addresses only.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Package READMEs and the amended records updated in the units that
      change them.
- [ ] `GOALS.md` no longer states NATS as the fabric between edge and
      central (U4).
- [ ] The handoff asks the user to accept the edge delivery record and
      to re-read the jwt, nkeys, and nats-server statements. The record's
      status and index row change only on the user's answer.
- [ ] This plan's outcome recorded with
      `uv run tools/scripts/run.py plan record implemented <plan> --units <n> --from <t> --to <t>`
      or `partial`.
- [ ] No plan labels in code.

## Open questions

- For the user, not blocking this phase: whether
  `docs/plans/2026-10-04-2023-fix-central-intake-review-items-plan.md`
  lands first or is re-planned after this phase, which deletes its U1
  follower changes and part of its U2 (Overlap with other work).
- For the user, not blocking this phase: stale leaf passages in plans
  that re-plan on their turn. Central high availability phase 2
  (stop condition, per-edge claims, WebSocket listener, R2 to R4, and its
  citations of the two budget and re-attach solutions), central high
  availability phase 3 (the forwarder question), deployment phases 1 and 2
  (the WebSocket listener and leaf-node operator mode), the deployment
  parent's plural LoadBalancers, and the fsync plan's `NormalizeFsync`
  decision.
