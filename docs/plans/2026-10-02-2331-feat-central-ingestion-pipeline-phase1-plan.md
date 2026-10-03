---
title: Ingest Envelope and Edge Syslog Source - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-08-20-network-model-structure-direction.md
parent: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md
---

# Ingest Envelope and Edge Syslog Source - Plan

> Implemented. 4 units, 2026-10-03T11:22Z to 2026-10-03T12:32Z.

## Goal

An edge agent listens for syslog, maps each message from a device it hosts
into a `SyslogRecord` inside an `IngestRecord`, and publishes it to its
buffer, from where the hub's per-edge stream sources it. The means: a new
`integration/ingest/v1` package, an `ingest.syslog` subject in `edgebus`, and
a syslog source module in the agent.

**Stop condition:** the agent holds no address for a device it hosts at the
time a datagram arrives. Device resolution then belongs in central and this
phase's mapper cannot fill `SyslogRecord.device`.

## Decisions

The parent plan's Decisions apply. These are local to the phase.

- The envelope is `flowseer.integration.ingest.v1.IngestRecord`, with a typed
  `oneof payload` whose first arm is `flowseer.event.log.v1.SyslogRecord`.
  Why: `spec/proto/flowseer/integration/README.md` admits an event contract a
  distributed integration and central exchange over NATS, and a typed arm
  keeps rule 9 of the device service record ("kinds are typed or not
  shipped").
- `Provenance.protocol` widens its type so that it can name syslog.
  `ManagementProtocol` gains no `SYSLOG` value. Why: syslog is not a
  management protocol, and `Provenance` requires a non-zero protocol
  (`spec/proto/flowseer/model/inventory/v1/provenance.proto`). (decided by
  the user, 2026-10-03)
- The widened type is a required `oneof protocol` on `Provenance`, with arms
  `ManagementProtocol management = 10` and the new enum at 11. Field 3 goes
  with no `reserved` line for the number or for the name `protocol`. Why: no
  management value is written twice, and `BindingState.address` is the same
  shape in the same package
  (`spec/proto/flowseer/model/inventory/v1/binding.proto:148-159`). (decided
  by the user, 2026-10-03)
- The new enum is `LogProtocol` with `LOG_PROTOCOL_SYSLOG = 1`, and its arm
  is `log`. Why: "log" is the tree's word for the domain
  (`spec/proto/flowseer/net/log/v1`, `spec/proto/flowseer/event/log/v1`), and
  a trap or a webhook still names SNMP or HTTP in the management arm.
  (decided by the user, 2026-10-03)
- `LogProtocol` is declared in `provenance.proto` with
  `LOG_PROTOCOL_UNSPECIFIED = 0`, and each arm rejects the zero value and an
  undefined one. Why: an enum lives beside its owner and numbers its zero as
  unspecified (`docs/conventions/protobuf.md`, Enums), and `NextHop.target`
  carries the same rule on an enum arm
  (`spec/proto/flowseer/net/routing/v1/next_hop.proto:31-37`). `buf build` at
  the pinned v1.73.0 (`tools/buf/go.mod`) accepts a oneof named `protocol` in
  a message with no field and no reservation of that name, and U2's `buf
  lint` repeats the check on the real file.
- The widened field and the Go that names it change in one unit, U2. Why:
  `docs/code-style-proto.md`, Workflow, lands a schema change with its
  regenerated code, and the regenerated message no longer compiles against
  its one producer
  (`src/modules/localnet/access/internal/capability/interfaces/adapter.go:113-117`)
  or its one consumer (`src/modules/localnet/access/lane.go:2185`).
  `ManagementEndpoint.protocol`
  (`spec/proto/flowseer/model/inventory/v1/binding.proto:91`) and
  `RouteSelected.protocol`
  (`spec/proto/flowseer/event/access/v1/operation_event.proto:102`) are other
  fields, and the decision widens neither.
- `SyslogRecord.severity` and `facility` stop being required. Unset means the
  message carried no PRI. Why: a legacy message may omit PRI
  (`src/protocol/syslog/README.md`, "Optional PRI/origin"), and rule 5 of the
  device service record reads unset as "the device does not provide it". A
  default would be a fabricated field.
- `IngestRecord.record_id` is the record's only id, and `SyslogRecord`
  drops its `record_id`. Why: two fields that must always agree need a rule
  to hold them equal. (decided by the user, 2026-10-03)
- A removal before the first stable release leaves no `reserved` line, and a
  number is still never reused. `SyslogRecord.record_id` and
  `Provenance.protocol` go that way, and U1 amends `docs/code-style-proto.md`,
  Evolution, with every other document that states the old rule. Why:
  nothing outside this repository reads the schemas yet (`AGENTS.md`, Agent
  behavior), so a tombstone protects no consumer. (decided by the user,
  2026-10-03)
- Three documents state reserve-on-removal, and nothing enforces it on
  FlowSeer schemas: `docs/code-style-proto.md:185,198-199`,
  `docs/conventions/protobuf.md:379-381`, and the network model record
  (`docs/architecture/2026-08-20-network-model-structure-direction.md:426,522`).
  Why this is the whole list: `buf.yaml` suspends breaking checks for the
  FlowSeer module (lines 41-49) and applies `WIRE`, the category that holds
  the reserved rules, to the vendor module alone (lines 27-29). No configured
  lint rule, no hook under `tools/hooks/`, and no test under `test/` reads
  `reserved`, and `AGENTS.md` does not state the rule.
- The 24 `reserved` lines that earlier removals left in 14 files under
  `spec/proto/flowseer/` stay. Why: U1 changes the rule for removals from
  this phase on, and no removal of this phase touches those files. (decided
  by the user, 2026-10-03)
- `SyslogRecord`'s other fields keep their numbers, and no reader migrates.
  Why: the only Go that names `record_id` is its own rules test
  (`test/conformance/proto/event_log_rules_test.go:18,34,95,162-176`), and
  intake and the history sink are later phases that read
  `IngestRecord.record_id`.
- `IngestRecord.record_id` is a UUIDv7 from `uuid.NewV7`
  (`github.com/google/uuid` v1.6.0, `go doc` confirms the function), and is
  passed as the message id to `Leaf.Publish`
  (`src/modules/edgebus/leaf.go:218`).
- The agent resolves the device from the datagram's peer address against the
  devices its lane host has onboarded. A message from any other address is
  counted and dropped. Why: the lane host already holds each device's address
  and binding (`src/edge/agent/internal/lanehost/onboard.go:249-271`), and
  `SyslogRecord.device` is required.
- The lookup is a `lanehost.DeviceIndex` the agent's assembly owns for the
  life of the process. The onboarder writes it and the syslog source reads
  it. Why: the `Onboarder` is built inside the lane module's attempt
  (`src/edge/agent/host/host.go:235`) and its `held` map
  (`src/edge/agent/internal/lanehost/onboard.go:96-98`) ends with that
  attempt, so a sibling module cannot hold a reference to it. The index keeps
  its entries across a lane restart, since the devices stay hosted.
- The index follows the listing. `Add` drops any other address that carries
  the same device id, and `Sync` prunes the index to the listed device ids
  after a successful `ListDevices`. A failed listing prunes nothing. Why: the
  index outlives the onboarder's `held` map, so without removal a datagram
  from an address a device no longer has is published as that device's
  record, which requirement 6 forbids. (decided by the user, 2026-10-03)
- An address two listed devices share resolves to no device. The datagram is
  counted and dropped on `flowseer.edge.syslog.dropped` with
  `flowseer.edge.syslog.reason` of `ambiguous_source`, never attributed to the
  device listed last. Why: `ListedDevice` carries one `ip` and per-device
  ports (`spec/proto/flowseer/edge/attach/v1/device.proto:45-47`), so two
  rows may name one address, and a peer address cannot tell them apart. The
  value is snake case and bounded, as `docs/conventions/observability.md`
  (lines 81-90) asks of an attribute. (decided by the user, 2026-10-03)
- The index carries the listed address and binding of every listed device,
  a held one included. Each successful `Sync` records one claim per listed
  device with a usable address, whether or not the device onboards, and
  drops the device's claim on any other address. An address with two or
  more claimants resolves to no device. An address with one claimant
  resolves to it only when the device was onboarded by a lane attempt of
  this process and is listed at this address, and otherwise the datagram is
  dropped as `unknown_source`. That state outlives the lane
  attempt, so a lane restart and a process restart differ: after a lane
  restart a device the new attempt has not yet onboarded still resolves,
  and after a process restart the index starts empty
  (`src/edge/agent/host/host.go:148`) and the device resolves once it
  onboards. `Sync` applies a whole listing to the index in one step, the
  prune, the claims, and the held devices it re-asserts, before it onboards.
  Why: a held device re-listed at a new address keeps its lane session on
  the old one until the attempt restarts
  (`src/edge/agent/internal/lanehost/onboard.go:212-220`), and a datagram
  from the address it left must not be published as its record. The same
  rule covers a device re-listed from one address to another whose
  onboarding at the new one fails, or whose new address is unusable: its
  claim on the old address goes with the listing. The index and the lane
  may then name different addresses for one device until the attempt
  restarts. (decided by the user, 2026-10-03)
- Phase 1 keeps the parser's default options, and a vendor line the parser
  reports partial is a parse failure like any other. Why: choosing vendor
  parse options needs real device output, which this phase has none of.
  (decided by the user, 2026-10-03)
- The syslog source fills `Provenance` with the binding from the device
  index, the receive time (`Observation.ReceivedAt`,
  `src/protocol/syslog/record.go:71`), the agent's edge ref (`edgeRefOf`,
  `src/edge/agent/host/host.go:368`), and `LOG_PROTOCOL_SYSLOG` in the `log`
  arm. Why: the onboarder builds the same binding and edge refs for the lane
  (`src/edge/agent/internal/lanehost/onboard.go:249-258`).
- `SyslogRecord.received_at` stays beside `Provenance.observed_at`, and the
  source writes the receive time to both. Why: the contracts differ for a
  cloud-mediated source, where `observed_at` is when the platform saw the
  payload (`spec/proto/flowseer/model/inventory/v1/provenance.proto:15-18`)
  and `received_at` is when the collector received the record
  (`spec/proto/flowseer/event/log/v1/syslog_record.proto:32-33`). (decided by
  the user, 2026-10-03)
- A syslog record's `Provenance.firmware_fingerprint` stays unset, and the
  field's comment says unset means the producer holds no fingerprint. Why:
  the field is optional (`provenance.proto:32-40`), and the fingerprint lives
  inside `access.Lane`, which exports no accessor for it
  (`src/modules/localnet/access/lane.go`, exported methods). (decided by the
  user, 2026-10-03)
- The syslog module receives the `*edgebus.Leaf` from the bus attachment
  `Run` already holds (`src/edge/agent/host/host.go:133-144`). Why: the
  attachment is not stored in `assembly` today.
- A parse failure is a record whose `syslog.Record.Status` is not `Complete`
  (`src/protocol/syslog/record.go:44-53`) or whose mapped field exceeds its
  schema bound. The over-long field is left unset, except `message`, which
  the mapper cuts to 65,527 bytes and marks with `message_truncated`, as the
  field's contract says
  (`spec/proto/flowseer/event/log/v1/syslog_record.proto:73-79`). A record
  with a cut message is a parse failure like any other over-long field, so
  the raw policy applies to it. Only a stream transport reaches that length.
- Raw policy defaults: 20 raw-bearing failures per device per minute, then 1
  in 100. Both are configuration. Why: the parent plan fixes the shape, and
  these numbers only bound the worst case at about one raw datagram every
  three seconds per device.
- The source lives in `src/edge/agent/internal/syslogsource`. Why: one host
  assembles it, and `src/modules/README.md` admits a module at two.
- The source starts only when its configuration names a listener.
- The listener and the raw policy numbers are fields of
  `flowseer.store.agent.v1.AgentConfig`, in a message of their own at field
  6, as `intervals` and `buffer` are. Why: `host.Config` wraps that message
  and reads its own settings from nothing else
  (`src/edge/agent/host/config.go:54,72`). A listener address is a string, as
  `syslog.ListenConfig.Address` is (`src/protocol/syslog/receiver.go:20-25`),
  so `store/agent` keeps importing nothing FlowSeer-owned
  (`test/conformance/proto/layering_test.go:179`).
- The listeners are a `repeated AgentSyslogListener`, each an address and a
  transport enum. Why: a listener is a transport and an address in the
  library as well (`syslog.ListenConfig`), so a later transport adds an enum
  value and no field. (decided by the user, 2026-10-03)
- `AgentSyslogTransport` declares UDP and TCP, and this phase serves both.
  The source sets the receiver's `MaxPayload` to 65,535, and
  `source_test.go` gains a TCP case. Why: the library serves both transports
  (`src/protocol/syslog/README.md:83`), and its default `MaxPayload` of 64
  KiB (`src/protocol/syslog/options.go:12,54`) is one byte over what
  `RawEvidence.data` takes. (decided by the user, 2026-10-03)
- A listener carries a framing enum with the library's five values, unset
  means auto, and this phase tests each. Why: auto frames an LF-delimited
  payload only when it starts with `<`
  (`src/protocol/syslog/README.md:112-114`), so a device that sends legacy
  lines without PRI over TCP needs an explicit framing. (decided by the user,
  2026-10-03)
- A listener's unset `transport` means UDP. Why: the file's own enum reads
  its zero value as a default
  (`spec/proto/flowseer/store/agent/v1/agent_config.proto:119-121`). (decided
  by the user, 2026-10-03)
- The enums are `AgentSyslogTransport` and `AgentSyslogFraming`. Each has an
  `_UNSPECIFIED = 0` the agent reads as the default beside the value that
  names that default, the framing values follow the library's order (auto,
  octet counting, LF, CRLF, NUL), and a file names at most eight listeners.
  Why: the package prefixes its own types and keeps its own enums
  (`agent_config.proto:76,104,113-119`), `AgentLogLevel` reads its zero as
  INFO and declares INFO as well (`agent_config.proto:119-126`), the
  library declares the five in that order
  (`src/protocol/syslog/framing.go:15-26`), and `syslog.Listen` refuses more
  than `Limits.MaxListeners`, which defaults to eight
  (`src/protocol/syslog/receiver.go:89`,
  `src/protocol/syslog/options.go:14-15`).
- A listener that sets `framing` and is not a TCP listener is refused at
  load, by a schema rule on `AgentSyslogListener`. Why: a datagram is its
  own frame (`src/protocol/syslog/README.md:115`), so the library would
  ignore the value, and the file refuses a backoff ceiling below its floor
  "rather than corrected" so that no agent runs on a value nobody wrote
  (`agent_config.proto:92-96`). (decided by the user, 2026-10-03)
- The source counts its publish retries. Why:
  `docs/conventions/observability.md`, Required measurement families, asks a
  component that publishes durable messages for its retried operations.
- The host exports the receiver's own losses, one observable counter per
  statistic through `observe`. Why: a wrong framing closes the connection on
  every message (`src/protocol/syslog/README.md:116`) and leaves no record
  to count, and the host's `observe` helper reads one value per counter
  (`src/edge/agent/host/host.go:308-317`). (decided by the user, 2026-10-03)
- A metric attribute of this source is named under its metrics' namespace,
  `flowseer.edge.syslog.reason`. Why: `docs/conventions/observability.md`
  (lines 81-90) admits no bare custom key, and the forwarder names its own
  `flowseer.edgebus.reason` (`src/modules/edgebus/forwarder.go:297`).
- U2 amends the ingestion direction record with both wire decisions, and U1
  amends the network model record with the reserved rule. Why: each changes
  a contract more than one package reads, and this plan is deleted once the
  phase lands.

## Requirements

1. `IngestRecord` validates only with a UUID record id, a `Provenance`, and
   exactly one payload arm. Example: an envelope with no payload fails with a
   violation on the `payload` oneof.
2. `RawEvidence` carries the bytes, a reason of `RAW_REASON_PARSE_FAILURE`
   or `RAW_REASON_WINDOW`, and a suppressed count. Example: 70,000 bytes of
   data fail the 65,535 bound, and an empty datagram's zero bytes pass.
3. A complete RFC 5424 message from a hosted device's address maps to a valid
   `SyslogRecord` with no raw evidence, in an envelope whose provenance names
   the device's binding, this edge, the receive time, and syslog. Example:
   the parent plan's requirement 1 datagram yields severity `CRITICAL`,
   facility `AUTH`, hostname `sw1`, app name `app`, and message `link down`.
4. A message with no PRI maps to a record with severity and facility unset
   and raw evidence of reason `RAW_REASON_PARSE_FAILURE` when the parser reports it
   partial.
5. The raw policy keeps raw for the first 20 failures per device per minute,
   then for every 100th, and reports the number suppressed since the last
   kept one. Example: the parent plan's requirement 2.
6. A datagram from an address no onboarded device has is dropped and counted
   on `flowseer.edge.syslog.dropped`, unit `{record}`, with
   `flowseer.edge.syslog.reason` of `unknown_source`.
7. A published envelope reaches `FLOWSEER_EDGE_<edge-id>` on the hub under
   `flowseer.<tenant>.edge.<edge-id>.ingest.syslog`, and its JetStream
   message id is the envelope's `record_id`. Example: one envelope published
   twice inside the buffer's duplicate window is stored once.
8. `Provenance` names syslog, and no field typed for a management protocol
   accepts it. Example: a provenance whose `log` arm is `LOG_PROTOCOL_SYSLOG`
   validates, one with neither arm fails on the `protocol` oneof, and a
   `RouteSelected` whose protocol is 7 fails `defined_only`.
9. `SyslogRecord` holds no id. Example: a record with only a device and a
   receive time validates, and an envelope wrapping it fails only on its own
   missing `record_id`.
10. The agent listens only where `AgentConfig.syslog` names a listener, over
    UDP or TCP, and an unset `transport` means UDP. Example: a file with no
    `syslog` block starts no source, and a listener that names only a
    loopback address receives the requirement 3 datagram over UDP.
11. After the phase no schema it touched holds a `reserved` line for a field
    it removed, and no document asks for one before the first stable release.
    Example: `syslog_record.proto` has no field 2 and no `reserved` line, and
    `docs/code-style-proto.md`, Evolution, says a pre-release removal leaves
    none and a number is never reused.
12. A TCP listener frames by its `framing`, and unset means the library's
    detection. Example: with LF framing the line
    `Oct  3 10:00:00 sw1 app: up` and a line feed arrive as one record with
    severity unset. Under auto the same bytes close the connection and
    yield no record.
13. A listener that sets `framing` and is not a TCP listener is refused at
    load. Example: `framing: AGENT_SYSLOG_FRAMING_LF` with `transport` unset
    fails `LoadConfig`.
14. A message longer than 65,527 bytes is cut to that length and marked
    truncated, the record counts as a parse failure, and its raw evidence
    fits. Example: a 65,535-byte
    octet-counted TCP frame with no recognizable envelope yields a
    65,527-byte `message`, `message_truncated` true, and 65,535 bytes of raw
    evidence in an envelope that validates.

## Out of scope

- Any central consumer of the `ingest` branch. The OTLP forwarder filters to
  `otel` (`src/modules/edgebus/forwarder.go:199`), so ingest records wait in
  the per-edge stream until intake lands.
- The `RAW_REASON_WINDOW` trigger. The enum value exists and nothing sets it
  until the raw window phase.
- TLS syslog and vendor field mapping beyond what `SyslogRecord` holds. The
  transport enum declares no TLS value.
- Lane records stored before U2. One holds `Provenance.protocol` at field 3
  inside `last_observations`
  (`spec/proto/flowseer/store/device/v1/lane_record.proto:109`) and reads
  back with neither arm set. Nothing migrates it (`AGENTS.md`, Agent
  behavior).
- Removing the `reserved` lines earlier removals left under
  `spec/proto/flowseer/`. They stay, as the Decisions say.
- Vendor parse options. The source passes the parser no option beyond
  `CaptureRaw` (`src/edge/agent/internal/syslogsource/source.go:69-74`), so
  well-formed vendor output parses partial and draws on the raw budget: a
  leading Cisco counter (`src/protocol/syslog/legacy.go:16-18`), a zone token
  after the clock (`src/protocol/syslog/timestamp.go:245`,
  `src/protocol/syslog/legacy.go:49-54`), a Cisco tag with components
  (`src/protocol/syslog/vendor.go:129-131`). A legacy timestamp yields no
  `sent_at` without a year and UTC
  (`src/protocol/syslog/timestamp.go:131-153`). Follow-up: plan vendor parse
  options against real device output.
- Input trust: the source reads datagrams from untrusted network senders. The
  parser's limits (`src/protocol/syslog/options.go`) bound them, and only a
  hosted device's address is accepted.

## Units

### U1. Reserved rule for the pre-release window

Files: docs/code-style-proto.md, docs/conventions/protobuf.md, docs/architecture/2026-08-20-network-model-structure-direction.md
After: none
Change: Evolution in `docs/code-style-proto.md` says a removal before the
first stable release leaves no `reserved` line, for the number or for the
name (line 185). The sentence that kept the old rule inside that window goes
(lines 198-199). A number is still never reused or renumbered, the
2026-08-26 collapse stays the one dated exception to renumbering, and the
rule from the first stable release on is unchanged. Field numbering in
`docs/conventions/protobuf.md` (lines 379-381) says the same. The network
model record gains a dated amendment, in the form its `### <date>` entries
have (lines 746-996): before the first stable release a deleted number
leaves no `reserved` line and is still never reused, which replaces the
last clause of convention 10 (line 522) and the cost line 426 names for
removing a field. The two convention documents' `last_updated` moves to the
day of the change. Nothing checks that a number is not reused, since breaking checks
are suspended for the module (`buf.yaml:41-49`). Review holds the rule, as
`docs/code-style-proto.md:209-213` says of the others.
Tests: none. The unit changes prose, and the verifier's link and prose
checks are its gate.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/code-style-proto.md docs/conventions/protobuf.md docs/architecture/2026-08-20-network-model-structure-direction.md`

### U2. Ingest envelope schema and the two reshaped messages

Files: spec/proto/flowseer/integration/ingest/v1/ingest_record.proto, spec/proto/flowseer/integration/ingest/v1/README.md, spec/proto/flowseer/integration/README.md, spec/proto/flowseer/README.md, spec/proto/flowseer/model/inventory/v1/provenance.proto, spec/proto/flowseer/model/inventory/v1/README.md, spec/proto/flowseer/model/README.md, spec/proto/flowseer/event/log/v1/syslog_record.proto, spec/proto/flowseer/event/log/v1/README.md, spec/proto/flowseer/event/README.md, test/conformance/proto/layering_test.go, test/conformance/proto/integration_ingest_rules_test.go, test/conformance/proto/event_log_rules_test.go, test/conformance/proto/model_inventory_rules_test.go, test/conformance/proto/event_access_rules_test.go, src/modules/localnet/access/internal/capability/interfaces/adapter.go, src/modules/localnet/access/internal/capability/interfaces/adapter_test.go, src/modules/localnet/access/lane.go, src/modules/localnet/access/read_route_test.go, src/modules/localnet/access/README.md, src/services/device/internal/journal/resolve_test.go, src/services/device/internal/deviceapi/deviceapi_test.go, src/services/device/internal/drift/drift_test.go, docs/conventions/protobuf.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, CONCEPTS.md, generated/go/proto
After: U1
Change: `IngestRecord` has `record_id` (required UUID), `provenance`
(required `flowseer.model.inventory.v1.Provenance`), a required `oneof
payload` with `SyslogRecord syslog = 10`, and `RawEvidence raw`.
`RawEvidence` has `data` (bytes, at most 65,535, where empty means the
device sent an empty datagram), a required `RawReason`
(`RAW_REASON_UNSPECIFIED = 0`, which validation rejects,
`RAW_REASON_PARSE_FAILURE = 1`, `RAW_REASON_WINDOW = 2`, as
`docs/code-style-proto.md` requires of enum values), and
`suppressed_since_last` (uint64).
`Provenance` drops field 3 with no `reserved` line and gains a required
`oneof protocol` with `ManagementProtocol management = 10` and
`LogProtocol log = 11`. Each arm is `defined_only` and rejects the zero
value. `provenance.proto` declares `LogProtocol` with
`LOG_PROTOCOL_UNSPECIFIED = 0` and `LOG_PROTOCOL_SYSLOG = 1`.
`ProvenanceInputs.provenance` (`adapter.go:104-120`) sets the `management`
arm to SNMP or SSH for the route, and its default case sets the arm to the
zero value, which validation rejects as it does today. `Lane.recordEvidence`
(`lane.go:2185`) reads the `management` arm and hands it to
`evidence.Store.Record`
(`src/modules/localnet/access/internal/evidence/store.go:121`), whose key
keeps its type. The three service tests set the `management` arm of their
fixture provenance to SSH. The `firmware_fingerprint` comment says unset
means the producer holds no fingerprint.
`SyslogRecord` drops `record_id` with no `reserved` line, and no other field
changes its number.
`severity` and `facility` lose `required` and keep `defined_only`, and their
comments say unset means the message carried no PRI. `layering_test.go` admits
`integration/ingest` importing `model/inventory` and `event/log`. Every README
whose boundary the new imports cross states them, since
`TestProtoReadmeImports` (`test/conformance/proto/layout_test.go:245`)
compares each `Imported by:` line with the tree: the `model/` root and
`model/inventory/v1` gain `integration/ingest`, and so do the `event/` root
and `event/log/v1`.
`event/log/v1/README.md` drops its `record_id` bullet (line 27), names the
envelope's id under "Deliberately absent", and stops calling the PRI fields
mandatory (line 31). The `Provenance` paragraph of
`model/inventory/v1/README.md` (lines 201-210) and "Provenance rides the
envelope" in `docs/conventions/protobuf.md` describe the oneof and its two
arms, and `src/modules/localnet/access/README.md` (lines 257-261) names the
`management` arm where it names the field today. The direction record
gains an `## Amendments` section, as the device service record keeps one,
with one dated entry: a payload message carries no id of its own, and
`Provenance` names a protocol through a oneof of `ManagementProtocol` and
`LogProtocol`. `CONCEPTS.md`
gains Ingest Record and Raw Evidence beside Syslog Record.
`generated/go/proto` is the
output of `go tool -modfile=tools/buf/go.mod buf generate`, never a hand edit.
Tests: `integration_ingest_rules_test.go` covers requirements 1, 2, and 9
with one valid envelope and one case per rule, each input failing only that
rule. `event_log_rules_test.go` drops `RecordId` from its three builders and
its two `record_id` cases, replaces "severity absent fails" and "facility
absent fails" with cases that pass, and keeps "severity 8 fails
defined_only". `model_inventory_rules_test.go` builds its fixture with the
`management` arm and covers requirement 8 on `Provenance`: the `log` arm
with syslog passes, the `management` arm with SSH passes, neither arm fails
on the oneof, and the zero value and an undefined value fail on each arm.
`event_access_rules_test.go` gains the `RouteSelected` case of requirement
8. `adapter_test.go:98,123` and `read_route_test.go:78,133` still assert
SNMP for the SNMP route and SSH for the fall-through, read from the
`management` arm. No test reads the evidence store after a lane read, since nothing
outside the `evidence` package's own tests calls `Consult`
(`lane.go:2173-2176`), so nothing in this unit proves which route
`recordEvidence` stores.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto src/modules/localnet/access src/services/device docs/conventions/protobuf.md docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md CONCEPTS.md generated/go/proto`

### U3. Ingest subject in edgebus

Files: src/modules/edgebus/subjects.go, src/modules/edgebus/edgebus_test.go, src/modules/edgebus/README.md, src/edge/agent/internal/busattach/busattach_test.go
After: none
Change: `IngestSubject(tenant, edgeID, source)` returns
`EdgeSubtree(tenant, edgeID) + ".ingest." + source`, and
`EdgePublishSubjects` adds the logical name `ingest.syslog`. The buffer
already holds the branch (`src/modules/edgebus/leaf.go:203`). The README's
subject list names `ingest.syslog` in place of "a future ingestion source".
Tests: `edgebus_test.go` gains a case that a record published on
`leaf.Subject("ingest.syslog")` arrives in the hub's edge stream, and one that
`EdgePublishSubjects` holds four entries that all pass `TenantFromSubjects`.
A third publishes one payload twice on that subject with one message id and
finds one message in the hub's edge stream, as
`TestAuditStreamStoresADuplicateEventOnce` does for the audit stream. The
buffer sets no `Duplicates` (`src/modules/edgebus/leaf.go:201-209`), so the
server's two-minute default applies, or the buffer's `MaxAge` when that is
shorter (`github.com/nats-io/nats-server/v2` v2.15.0,
`server/stream.go:1884,1986-2000`).
`busattach_test.go:204` builds its response from `EdgePublishSubjects` and
must still pass.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus src/edge/agent/internal/busattach`

### U4. Syslog source in the agent

Files: spec/proto/flowseer/store/agent/v1/agent_config.proto, spec/proto/flowseer/store/agent/v1/README.md, generated/go/proto, src/edge/agent/internal/lanehost/index.go, src/edge/agent/internal/lanehost/index_test.go, src/edge/agent/internal/syslogsource/source.go, src/edge/agent/internal/syslogsource/mapper.go, src/edge/agent/internal/syslogsource/rawpolicy.go, src/edge/agent/internal/syslogsource/mapper_test.go, src/edge/agent/internal/syslogsource/rawpolicy_test.go, src/edge/agent/internal/syslogsource/source_test.go, src/edge/agent/internal/lanehost/onboard.go, src/edge/agent/internal/lanehost/onboard_test.go, src/edge/agent/host/host.go, src/edge/agent/host/config.go, src/edge/agent/host/config_test.go, src/edge/agent/host/capture_test.go, src/edge/agent/host/options.go, src/edge/agent/README.md
After: U2, U3
Change: `AgentConfig` gains `AgentSyslog syslog = 6`, where unset means the
agent runs no syslog source. It holds `repeated AgentSyslogListener
listeners`, one to eight, `raw_failures_per_minute` (unset means 20), and
`raw_sample_every` (unset means 100). Both numbers are `uint32` with a floor
of one, as every number in the file has
(`agent_config.proto:91,97-98,107,110`). A listener has `address`, a
`host:port` string, `transport` (`AgentSyslogTransport`: UDP = 1, TCP = 2,
unset or zero means UDP), and `framing` (`AgentSyslogFraming`: auto = 1,
octet counting = 2, LF = 3, CRLF = 4, NUL = 5, unset or zero means auto). A
rule on the message refuses a `framing` on a listener that is not TCP.
`host.Config` exposes the listeners as `syslog.ListenConfig` values and the
two numbers. An unset framing stays empty there, which the library reads as
auto for TCP (`src/protocol/syslog/framing.go:32-39`).
`generated/go/proto` is regenerated, never hand-edited.
`lanehost.DeviceIndex` maps a peer address to a device id and
binding ref, safe for concurrent use. `OnboardConfig` takes one, and the
onboarder records a device in it where it writes `held`
(`src/edge/agent/internal/lanehost/onboard.go:225`). The index outlives
`held`, which ends with the lane attempt, so it has a removal path of its
own: `Add` drops any other address of the same device, `Sync` prunes the
index to the listed device ids after a successful `ListDevices`, before it
onboards, and an address two listed devices share resolves to no device,
whether or not both onboard. A held device's entry takes the listed address
and binding on every `Sync`. `Lookup` tells a
shared address from an unknown one, and the source drops the first under
`ambiguous_source` and the second under `unknown_source`. `Run` creates the index and passes it to
both the lane assembly and the syslog module, with the attachment's leaf and
the edge ref from `edgeRefOf`.
The source calls `syslog.Listen` with one `ListenConfig` per configured
listener, `CaptureRaw` on, and `Limits.MaxPayload` at 65,535, and loops on
`Receiver.Next` (`src/protocol/syslog/receiver.go:81,228`). For each record
it resolves the device from `Observation.Peer`
(`src/protocol/syslog/record.go:72`), over either transport, and maps the
record. The mapper sets `source_address` from the peer and cuts a `message`
over 65,527 bytes, as the Decisions say. The source copies the raw bytes
from `Record.Raw`, a `*[]byte` that is nil when
capture is off (`src/protocol/syslog/record.go:170`), applies the raw policy,
and wraps the record in an envelope. The envelope gets one `uuid.NewV7` as
its `record_id` and the provenance the Decisions describe. The mapper sets
no id on the `SyslogRecord`. The source validates the envelope with
protovalidate and publishes it with `Leaf.Publish` on
`leaf.Subject("ingest.syslog")`, passing the `record_id` as the message id. A
publish the buffer refuses is retried with the same id and with backoff, and
never dropped silently. The raw policy takes an injected clock and keeps a per-device
window. The host passes it `Options.Clock`, whose comment in `options.go`
(lines 77-80) names the raw window as a reader. The host adds `{Name: "syslog", Gate: ..., Leaf: ...}` beside `lane`
and `capture` (`src/edge/agent/host/host.go:493-497`), gated on a configured
listener. Every goroutine starts through `src/common/spawn`. Counters follow
`docs/conventions/observability.md`, which keeps the unit out of the name:
`flowseer.edge.syslog.published` and `flowseer.edge.syslog.dropped` with
unit `{record}` and a `flowseer.edge.syslog.reason` attribute on the second,
and `flowseer.edge.syslog.raw.kept` and `flowseer.edge.syslog.raw.suppressed`,
both with unit `{record}`.
`flowseer.edge.syslog.publish.retries`, unit `{attempt}`, counts each
repeated publish. The host's `observe` helper
(`src/edge/agent/host/host.go:308-317`) reads `Receiver.Stats()`
(`src/protocol/syslog/stats.go:7-20,33`) into one observable counter per
statistic, with no attribute:
`flowseer.edge.syslog.receiver.received`, `.oversized`, and
`.framing_errors` with unit `{frame}`, `.udp_dropped` with unit
`{datagram}`, and `.pressure_closed` and `.connections_rejected` with unit
`{connection}`. While the source retries a
publish it does not call `Next`, so the receiver drops UDP datagrams and
holds TCP senders until `PressureTimeout`, 30 seconds by default, closes
them (`src/protocol/syslog/README.md:89-91`,
`src/protocol/syslog/options.go:44-46,81`).
No device id is an attribute.
Tests: `config_test.go` covers a file with no `syslog` block, which starts
no source and leaves `TestAWorkingFileIsTwoLinesAndEverythingElseDefaults`
passing, a block whose one listener names only an address and comes out as
UDP with both defaults, configured numbers that win, and a block with no
listener or with nine, which `LoadConfig` refuses. One table maps each
transport and each of the five framings to its library value and leaves an
unset framing empty, one case shows requirement 13, and a number set to
zero is refused. `TestModules_DeclaresLaneAndCapture`
(`capture_test.go:64-79`) expects the third module. `onboard_test.go`
builds its `OnboardConfig` with an index (line 209) and finds an onboarded
device in it under its listed address. `index_test.go` covers add, replace, and a lookup that
survives a second onboarder built against the same index. It also covers a
device added at a second address, whose first address then resolves to
nothing, a prune that keeps the listed ids and drops the rest, and an
address two devices share, which resolves to no device until one of them
leaves it. `onboard_test.go` shows a `Sync` whose listing drops a device
removing its address from the index, a `Sync` whose `ListDevices` fails
leaving the index as it was, and two listed devices at one address
resolving to neither. It also shows a device a listing drops and a later
listing names again resolving once more, a held device re-listed at a new
address resolving from the new address and no longer from the old, two
listed devices at one address resolving to neither when one of them fails
to onboard, and a device re-listed at a new address whose onboarding fails
no longer resolving from the old one. `mapper_test.go`
parses payloads it writes itself with the `src/protocol/syslog` parser: the
parent plan's requirement 1 datagram, a legacy line without PRI, an RFC 5424
line whose hostname is 256 characters, and an empty datagram. The corpus
manifest (`src/protocol/syslog/testdata/corpus/manifest.json`) holds none of
the last three. The test validates each envelope. Each case checks that the provenance names the
indexed binding and the edge, that its `log` arm is `LOG_PROTOCOL_SYSLOG`,
that `firmware_fingerprint` is unset, and that `observed_at` and
`received_at` both equal the case's receive time. A fifth payload of
65,535 bytes with no envelope shows the cut `message` and its flag. `rawpolicy_test.go` drives 220 failures in one minute and the
window roll-over with the injected clock. `source_test.go` starts a hub and a
leaf as `edgebus_test.go` does, sends UDP datagrams from a hosted and an
unknown address, and reads the envelope from the hub's edge stream. A
datagram from an address two indexed devices share yields no envelope and
one `flowseer.edge.syslog.dropped` with reason `ambiguous_source`. It checks
that the stored message's `Nats-Msg-Id` header equals the envelope's
`record_id`. Over TCP it sends one message under each of the five framings
and reads each envelope, with the two forms auto accepts both sent under
auto. It shows requirement 12 with the LF line, which under auto yields no
envelope and a `receiver.framing_errors` count of one, and
requirement 14 with the 65,535-byte frame. A publisher stub that refuses
the first attempt sees one message id twice and one `publish.retries`. A sourced copy keeps its headers and gains `Nats-Stream-Source`
(`github.com/nats-io/nats-server/v2` v2.15.0, `server/stream.go:4886-4893`).
Nothing
here proves the mapping against a real device's output, since every payload
is written from the grammar.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/agent src/edge/agent generated/go/proto`

Waves: U1 U3 | U2 | U4

## Verification

```bash
go tool -modfile=tools/buf/go.mod buf lint
go test -race ./test/conformance/proto/... ./src/modules/localnet/access/... ./src/services/device/... ./src/modules/edgebus/... ./src/edge/agent/...
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src/modules/localnet/access src/services/device src/modules/edgebus src/edge/agent docs/code-style-proto.md docs/conventions/protobuf.md docs/architecture/2026-08-20-network-model-structure-direction.md docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md CONCEPTS.md generated/go/proto
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `docs/code-style-proto.md`, `docs/conventions/protobuf.md`, and the
      network model record state the reserved rule as decided.
- [ ] `spec/proto/flowseer/integration/README.md`, the new package README,
      `src/modules/edgebus/README.md`, and `src/edge/agent/README.md`
      describe the envelope, the subject, and the source.
- [ ] `spec/proto/flowseer/store/agent/v1/README.md` describes the syslog
      block of the agent configuration.
- [ ] `spec/proto/flowseer/event/log/v1/README.md`,
      `spec/proto/flowseer/model/inventory/v1/README.md`,
      `src/modules/localnet/access/README.md`, and
      `docs/conventions/protobuf.md` describe `SyslogRecord` without an id
      and `Provenance.protocol` as a oneof.
- [ ] The ingestion direction record holds the dated amendment.
- [ ] `CONCEPTS.md` gains Ingest Record and Raw Evidence.
- [ ] This plan's `status` is set with an outcome note under its title, and
      the parent's `Landed:` line for U1 holds the commit range.
- [ ] No plan labels in code.
