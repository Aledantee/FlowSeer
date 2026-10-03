---
title: Ingest Envelope and Edge Syslog Source - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
parent: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md
---

# Ingest Envelope and Edge Syslog Source - Plan

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
  (`spec/proto/flowseer/model/inventory/v1/provenance.proto`). The new type
  and its effect on the existing producers of `Provenance` are not designed
  yet, and the units below still describe the enum value. (decided by the
  user, 2026-10-03)
- The widened field and the Go that names it change in one unit, U1. Why:
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
  to hold them equal. The units below still describe both fields. (decided
  by the user, 2026-10-03)
- `SyslogRecord` reserves field 2 and the name `record_id`, and its other
  fields keep their numbers. Why: `docs/code-style-proto.md`, Evolution, asks
  for both on a removal and forbids renumbering, inside the pre-release
  window as well. No reader migrates: the only Go that names the field is its
  own rules test (`test/conformance/proto/event_log_rules_test.go:18,34,95,162-176`),
  and intake and the history sink are later phases that read
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
- The syslog source fills `Provenance` with the binding from the device
  index, the receive time (`Observation.ReceivedAt`,
  `src/protocol/syslog/record.go:71`), the agent's edge ref (`edgeRefOf`,
  `src/edge/agent/host/host.go:368`), and syslog as the protocol. Why: the
  onboarder builds the same binding and edge refs for the lane
  (`src/edge/agent/internal/lanehost/onboard.go:249-258`). It writes the
  receive time to `SyslogRecord.received_at` as well and leaves
  `firmware_fingerprint` unset. Both are unconfirmed and repeated under Open
  questions.
- The syslog module receives the `*edgebus.Leaf` from the bus attachment
  `Run` already holds (`src/edge/agent/host/host.go:133-144`). Why: the
  attachment is not stored in `assembly` today.
- A parse failure is a record whose `syslog.Record.Status` is not `Complete`
  (`src/protocol/syslog/record.go:44-53`) or whose mapped field exceeds its
  schema bound. The over-long field is left unset.
- Raw policy defaults: 20 raw-bearing failures per device per minute, then 1
  in 100. Both are configuration. Why: the parent plan fixes the shape, and
  these numbers only bound the worst case at about one raw datagram every
  three seconds per device.
- The source lives in `src/edge/agent/internal/syslogsource`. Why: one host
  assembles it, and `src/modules/README.md` admits a module at two.
- The source starts only when its configuration names a listener.
- U1 amends the ingestion direction record with both wire decisions. Why:
  each changes a wire contract more than one package reads, and this plan is
  deleted once the phase lands.

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
   on `flowseer.edge.syslog.dropped`, unit `{record}`, with reason
   `unknown_source`.
7. A published envelope reaches `FLOWSEER_EDGE_<edge-id>` on the hub under
   `flowseer.<tenant>.edge.<edge-id>.ingest.syslog`, and its JetStream
   message id is the envelope's `record_id`. Example: one envelope published
   twice inside the buffer's duplicate window is stored once.
8. `Provenance` names syslog, and no field typed for a management protocol
   accepts it. Example: a provenance naming syslog validates, one naming no
   protocol fails, and a `RouteSelected` whose protocol is 7 fails
   validation.
9. `SyslogRecord` holds no id. Example: a record with only a device and a
   receive time validates, and an envelope wrapping it fails only on its own
   missing `record_id`.

## Out of scope

- Any central consumer of the `ingest` branch. The OTLP forwarder filters to
  `otel` (`src/modules/edgebus/forwarder.go:199`), so ingest records wait in
  the per-edge stream until intake lands.
- The `RAW_REASON_WINDOW` trigger. The enum value exists and nothing sets it
  until the raw window phase.
- TLS syslog and vendor field mapping beyond what `SyslogRecord` holds.
- Lane records stored before U1. One holds `Provenance.protocol` at field 3
  inside `last_observations`
  (`spec/proto/flowseer/store/device/v1/lane_record.proto:109`) and reads
  back without a protocol under an answer to open question 1 that moves or
  retypes the field. Nothing migrates it (`AGENTS.md`, Agent behavior).
- Input trust: the source reads datagrams from untrusted network senders. The
  parser's limits (`src/protocol/syslog/options.go`) bound them, and only a
  hosted device's address is accepted.

## Units

### U1. Ingest envelope schema and the two reshaped messages

Files: spec/proto/flowseer/integration/ingest/v1/ingest_record.proto, spec/proto/flowseer/integration/ingest/v1/README.md, spec/proto/flowseer/integration/README.md, spec/proto/flowseer/README.md, spec/proto/flowseer/model/inventory/v1/provenance.proto, spec/proto/flowseer/model/inventory/v1/README.md, spec/proto/flowseer/model/README.md, spec/proto/flowseer/event/log/v1/syslog_record.proto, spec/proto/flowseer/event/log/v1/README.md, spec/proto/flowseer/event/README.md, test/conformance/proto/layering_test.go, test/conformance/proto/integration_ingest_rules_test.go, test/conformance/proto/event_log_rules_test.go, test/conformance/proto/model_inventory_rules_test.go, test/conformance/proto/event_access_rules_test.go, src/modules/localnet/access/internal/capability/interfaces/adapter.go, src/modules/localnet/access/internal/capability/interfaces/adapter_test.go, src/modules/localnet/access/lane.go, src/modules/localnet/access/read_route_test.go, src/modules/localnet/access/README.md, src/services/device/internal/journal/resolve_test.go, src/services/device/internal/deviceapi/deviceapi_test.go, src/services/device/internal/drift/drift_test.go, docs/conventions/protobuf.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, generated/go/proto
After: none
Change: `IngestRecord` has `record_id` (required UUID), `provenance`
(required `flowseer.model.inventory.v1.Provenance`), a required `oneof
payload` with `SyslogRecord syslog = 10`, and `RawEvidence raw`.
`RawEvidence` has `data` (bytes, at most 65,535, where empty means the
device sent an empty datagram), a required `RawReason`
(`RAW_REASON_UNSPECIFIED = 0`, which validation rejects,
`RAW_REASON_PARSE_FAILURE = 1`, `RAW_REASON_WINDOW = 2`, as
`docs/code-style-proto.md` requires of enum values), and
`suppressed_since_last` (uint64).
`Provenance.protocol` takes the type open question 1 settles and names
syslog in it. It stays required, defined, and never the zero value.
`ProvenanceInputs.provenance` (`adapter.go:104-120`) writes SNMP or SSH for
the route in that type, and its default case still yields a provenance that
validation rejects. `Lane.recordEvidence` (`lane.go:2185`) reads the
management protocol back and hands it to `evidence.Store.Record`
(`src/modules/localnet/access/internal/evidence/store.go:121`). The three
service tests build a fixture provenance naming SSH and change only in how
they spell it.
`SyslogRecord` drops `record_id` and reserves field 2 and the name.
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
envelope" in `docs/conventions/protobuf.md` say how a protocol outside
`ManagementProtocol` is named, and `src/modules/localnet/access/README.md`
(lines 257-261) spells the field as the schema now does. The direction record
gains an `## Amendments` section, as the device service record keeps one,
with one dated entry: a payload message carries no id of its own, and how
`Provenance` names a protocol no management endpoint speaks.
`generated/go/proto` is the
output of `go tool -modfile=tools/buf/go.mod buf generate`, never a hand edit.
Tests: `integration_ingest_rules_test.go` covers requirements 1, 2, and 9
with one valid envelope and one case per rule, each input failing only that
rule. `event_log_rules_test.go` drops `RecordId` from its three builders and
its two `record_id` cases, replaces "severity absent fails" and "facility
absent fails" with cases that pass, and keeps "severity 8 fails
defined_only". `model_inventory_rules_test.go` covers requirement 8 on
`Provenance`: syslog passes, a management protocol passes, no protocol
fails, the zero value fails, and an undefined value fails.
`event_access_rules_test.go` gains the `RouteSelected` case of requirement
8. `adapter_test.go:98,123` and `read_route_test.go:78,133` still assert
SNMP for the SNMP route and SSH for the fall-through, read through the new
type. No test reads the evidence store after a lane read, since `Consult`
has no caller (`lane.go:2173-2176`), so nothing in this unit proves which
route `recordEvidence` stores.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto src/modules/localnet/access src/services/device docs/conventions/protobuf.md docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md generated/go/proto`

### U2. Ingest subject in edgebus

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

### U3. Syslog source in the agent

Files: src/edge/agent/internal/lanehost/index.go, src/edge/agent/internal/lanehost/index_test.go, src/edge/agent/internal/syslogsource/source.go, src/edge/agent/internal/syslogsource/mapper.go, src/edge/agent/internal/syslogsource/rawpolicy.go, src/edge/agent/internal/syslogsource/mapper_test.go, src/edge/agent/internal/syslogsource/rawpolicy_test.go, src/edge/agent/internal/syslogsource/source_test.go, src/edge/agent/internal/lanehost/onboard.go, src/edge/agent/host/host.go, src/edge/agent/host/config.go, src/edge/agent/host/options.go, src/edge/agent/README.md, CONCEPTS.md
After: U1, U2
Change: `lanehost.DeviceIndex` maps a peer address to a device id and
binding ref, safe for concurrent use. `OnboardConfig` takes one, and the
onboarder records a device in it where it writes `held`
(`src/edge/agent/internal/lanehost/onboard.go:225`). Nothing removes a held
device today, so the index has no removal path either. `Run` creates the index and passes it to
both the lane assembly and the syslog module, with the attachment's leaf and
the edge ref from `edgeRefOf`.
The source calls `syslog.Listen` with the configured
endpoints and `CaptureRaw` on, and loops on `Receiver.Next`
(`src/protocol/syslog/receiver.go:81,228`). For each record it resolves the
device from `Observation.Peer` (`src/protocol/syslog/record.go:72`), maps the
record, copies the raw bytes from `Record.Raw`, a `*[]byte` that is nil when
capture is off (`src/protocol/syslog/record.go:170`), applies the raw policy,
and wraps the record in an envelope. The envelope gets one `uuid.NewV7` as
its `record_id` and the provenance the Decisions describe. The mapper sets
no id on the `SyslogRecord`. The source validates the envelope with
protovalidate and publishes it with `Leaf.Publish` on
`leaf.Subject("ingest.syslog")`, passing the `record_id` as the message id. A
publish the buffer refuses is retried with the same id and with backoff, and
never dropped silently. The raw policy takes an injected clock and keeps a per-device
window. The host adds `{Name: "syslog", Gate: ..., Leaf: ...}` beside `lane`
and `capture` (`src/edge/agent/host/host.go:493-497`), gated on a configured
listener. Every goroutine starts through `src/common/spawn`. Counters follow
`docs/conventions/observability.md`, which keeps the unit out of the name:
`flowseer.edge.syslog.published` and `flowseer.edge.syslog.dropped` with
unit `{record}` and a `reason` attribute on the second, and
`flowseer.edge.syslog.raw.kept` and `flowseer.edge.syslog.raw.suppressed`.
No device id is an attribute.
Tests: `index_test.go` covers add, replace, and a lookup that
survives a second onboarder built against the same index. `mapper_test.go`
maps fixtures from `src/protocol/syslog/testdata/corpus`
for RFC 5424, legacy without PRI, one over-long hostname, and an empty datagram, and
validates each envelope. Each case checks that the provenance names the
indexed binding, the edge, and syslog, and that `observed_at` and
`received_at` both equal the fixture's receive time. `rawpolicy_test.go` drives 220 failures in one minute and the
window roll-over with the injected clock. `source_test.go` starts a hub and a
leaf as `edgebus_test.go` does, sends UDP datagrams from a hosted and an
unknown address, and reads the envelope from the hub's edge stream. It checks
that the stored message's `Nats-Msg-Id` header equals the envelope's
`record_id`. A sourced copy keeps its headers and gains `Nats-Stream-Source`
(`github.com/nats-io/nats-server/v2` v2.15.0, `server/stream.go:4886-4893`).
Nothing
here proves the mapping against a real device's output, since the corpus is a
grammar fixture set (`src/protocol/syslog/README.md`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent CONCEPTS.md`

Waves: U1 U2 | U3

## Verification

```bash
go tool -modfile=tools/buf/go.mod buf lint
go test -race ./test/conformance/proto/... ./src/modules/localnet/access/... ./src/services/device/... ./src/modules/edgebus/... ./src/edge/agent/...
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src/modules/localnet/access src/services/device src/modules/edgebus src/edge/agent docs/conventions/protobuf.md docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md CONCEPTS.md generated/go/proto
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `spec/proto/flowseer/integration/README.md`, the new package README,
      `src/modules/edgebus/README.md`, and `src/edge/agent/README.md`
      describe the envelope, the subject, and the source.
- [ ] `spec/proto/flowseer/event/log/v1/README.md`,
      `spec/proto/flowseer/model/inventory/v1/README.md`,
      `src/modules/localnet/access/README.md`, and
      `docs/conventions/protobuf.md` describe `SyslogRecord` without an id
      and `Provenance.protocol` in its widened type.
- [ ] The ingestion direction record holds the dated amendment.
- [ ] `CONCEPTS.md` gains Ingest Record and Raw Evidence.
- [ ] This plan's `status` is set with an outcome note under its title, and
      the parent's `Landed:` line for U1 holds the commit range.
- [ ] No plan labels in code.

## Open questions

The first two block U1 and U3. The units already hold the recommended answer
to the last two.

1. Which type does `Provenance.protocol` widen to?
   (a) A required `oneof protocol` on `Provenance`, with
   `ManagementProtocol management = 10` and a new enum arm at 11. Field 3 and
   the name `protocol` are reserved. Recommended. `BindingState.address` is
   the same shape in the same package
   (`spec/proto/flowseer/model/inventory/v1/binding.proto:148-159`),
   `NextHop.target` already carries an enum arm with its own rule
   (`spec/proto/flowseer/net/routing/v1/next_hop.proto:31-37`), and
   `docs/conventions/protobuf.md` (Typed variants, Field numbering) gives the
   arm numbers. No management value is written twice, `recordEvidence` reads
   the management arm, and `evidence.Store` keeps its `ManagementProtocol`
   key (`src/modules/localnet/access/internal/evidence/store.go:62`). Cost:
   every site in U1's file list changes its accessor, and a stored lane
   record loses its protocol (Out of scope). `buf build` at the pinned
   v1.73.0 (`tools/buf/go.mod`) accepts a oneof named `protocol` beside
   `reserved protocol;` and rejects a field of that name.
   (b) A wrapper message at field 3 holding the same oneof, arms numbered
   from 1. It is the typed-variant shape as `IpAddress` has it and keeps the
   field's number and name. Cost: a message with one user, since `Provenance`
   is the only provenance message (`docs/conventions/protobuf.md`,
   "Provenance rides the envelope"), a nested read at every site, and
   `required` stated on the field and on the oneof.
   (c) A new enum for this field that lists the six management values and
   syslog. The field stays one scalar, and a stored record keeps its meaning
   when values 1 to 6 keep their numbers. Cost: six values exist in two enums
   with nothing holding them equal, and `recordEvidence` converts at
   `lane.go:2185` or `evidence.Store` takes the wider key
   (`store.go:62,99,121`) and admits syslog as a route.
   (d) Rename `ManagementProtocol` to a wider name and add the syslog value.
   One enum and no mapping. Cost: `ManagementEndpoint.protocol` and
   `RouteSelected.protocol` each need `not_in: [0, 7]`, U1 also edits
   `binding.proto`, `operation_event.proto`, `evidence/store.go`,
   `telemetry/events.go`, `audit/event.go`, their tests, and `lane_test.go:743`,
   and `spec/proto/flowseer/event/access/v1/README.md:63` and
   `docs/solutions/conventions/document-intentional-schema-deviations-with-comment-and-test.md`
   cite the old name. It is the enum the decision keeps syslog out of, under
   another name.
2. What is the new enum called, and its arm under 1(a) or 1(b)? It is
   declared in `provenance.proto` beside its one user
   (`docs/conventions/protobuf.md`, Enums). Under 1(c) the question is the
   enum's name alone, and under 1(d) the wider name.
   (a) `LogProtocol` with `LOG_PROTOCOL_SYSLOG = 1`, arm `log`. Recommended:
   "log" is the tree's word for the domain (`spec/proto/flowseer/net/log/v1`,
   `spec/proto/flowseer/event/log/v1`), and a trap or a webhook still names
   SNMP or HTTP in the management arm.
   (b) `EventProtocol`, arm `event`, after the `event/` schema root. Cost: a
   trap and a webhook are events too and arrive over SNMP and HTTP
   (`binding.proto:69,74`), so the arm of a trap is ambiguous.
   (c) `TelemetryProtocol`, arm `telemetry`, wide enough for a later flow
   export source. Cost: "telemetry" in this tree means FlowSeer's own
   OpenTelemetry signals (`src/common/service/telemetry_config.go`, the
   `otel` branch at `src/modules/edgebus/leaf.go:203`).
3. Unconfirmed: does `SyslogRecord.received_at` stay beside
   `Provenance.observed_at`? The reason given for `record_id` fits it, since
   an edge source writes one instant to both.
   (a) Keep both. Recommended: the contracts differ for a cloud-mediated
   source, where `observed_at` is when the platform saw the payload
   (`provenance.proto:15-18`) and `received_at` is when the collector
   received the record (`syslog_record.proto:32-33`).
   (b) Drop `received_at` and reserve field 3. Cost: a `SyslogRecord` then
   carries only the device's own clock (`sent_at`), and the cloud-mediated
   receive time has no field.
4. Unconfirmed: what does a syslog record's `Provenance.firmware_fingerprint`
   hold?
   (a) Nothing. Recommended: the field is optional (`provenance.proto:32-40`)
   and the fingerprint lives inside `access.Lane`, which exports no accessor
   for it (`src/modules/localnet/access/lane.go`, exported methods). U1 then
   rewords the comment, which reads unset as "no identity probe has run", to
   say the producer holds no fingerprint.
   (b) The lane publishes the fingerprint into the device index. Every record
   then names its firmware epoch. Cost: a new exported surface on
   `src/modules/localnet/access` and an index entry that changes after
   onboarding.
