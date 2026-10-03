---
title: Ingest Envelope and Edge Syslog Source - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
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
- `SyslogRecord.severity` and `facility` stop being required. Unset means the
  message carried no PRI. Why: a legacy message may omit PRI
  (`src/protocol/syslog/README.md`, "Optional PRI/origin"), and rule 5 of the
  device service record reads unset as "the device does not provide it". A
  default would be a fabricated field.
- `IngestRecord.record_id` is the record's only id, and `SyslogRecord`
  drops its `record_id`. Why: two fields that must always agree need a rule
  to hold them equal. The units below still describe both fields. (decided
  by the user, 2026-10-03)
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

## Requirements

1. `IngestRecord` validates only with a UUID record id, a `Provenance`, and
   exactly one payload arm. Example: an envelope with no payload fails with a
   violation on the `payload` oneof.
2. `RawEvidence` carries the bytes, a reason of `RAW_REASON_PARSE_FAILURE`
   or `RAW_REASON_WINDOW`, and a suppressed count. Example: 70,000 bytes of
   data fail the 65,535 bound, and an empty datagram's zero bytes pass.
3. A complete RFC 5424 message from a hosted device's address maps to a valid
   `SyslogRecord` with no raw evidence. Example: the parent plan's
   requirement 1 datagram yields severity `CRITICAL`, facility `AUTH`,
   hostname `sw1`, app name `app`, and message `link down`.
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
   `flowseer.<tenant>.edge.<edge-id>.ingest.syslog`.

## Out of scope

- Any central consumer of the `ingest` branch. The OTLP forwarder filters to
  `otel` (`src/modules/edgebus/forwarder.go:199`), so ingest records wait in
  the per-edge stream until intake lands.
- The `RAW_REASON_WINDOW` trigger. The enum value exists and nothing sets it
  until the raw window phase.
- TLS syslog and vendor field mapping beyond what `SyslogRecord` holds.
- Input trust: the source reads datagrams from untrusted network senders. The
  parser's limits (`src/protocol/syslog/options.go`) bound them, and only a
  hosted device's address is accepted.

## Units

### U1. Ingest envelope schema

Files: spec/proto/flowseer/integration/ingest/v1/ingest_record.proto, spec/proto/flowseer/integration/ingest/v1/README.md, spec/proto/flowseer/integration/README.md, spec/proto/flowseer/README.md, spec/proto/flowseer/model/inventory/v1/binding.proto, spec/proto/flowseer/model/inventory/v1/README.md, spec/proto/flowseer/model/README.md, spec/proto/flowseer/event/log/v1/syslog_record.proto, spec/proto/flowseer/event/log/v1/README.md, spec/proto/flowseer/event/README.md, test/conformance/proto/layering_test.go, test/conformance/proto/integration_ingest_rules_test.go, test/conformance/proto/event_log_rules_test.go, generated/go/proto
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
`ManagementProtocol` gains `SYSLOG = 7`. `SyslogRecord.severity` and
`facility` lose `required` and keep `defined_only`. `layering_test.go` admits
`integration/ingest` importing `model/inventory` and `event/log`. Every README
whose boundary the new imports cross states them, since
`TestProtoReadmeImports` (`test/conformance/proto/layout_test.go:245`)
compares each `Imported by:` line with the tree: the `model/` root and
`model/inventory/v1` gain `integration/ingest`, and so do the `event/` root
and `event/log/v1`. `generated/go/proto` is the
output of `go tool -modfile=tools/buf/go.mod buf generate`, never a hand edit.
Tests: `integration_ingest_rules_test.go` covers requirement 1 and 2 with one
valid envelope and one case per rule, each input failing only that rule.
`event_log_rules_test.go` replaces "severity absent fails" and "facility
absent fails" with cases that pass, and keeps "severity 8 fails
defined_only".
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/integration spec/proto/flowseer/model/inventory/v1/binding.proto spec/proto/flowseer/event test/conformance/proto generated/go/proto`

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
both the lane assembly and the syslog module, with the attachment's leaf.
The source calls `syslog.Listen` with the configured
endpoints and `CaptureRaw` on, and loops on `Receiver.Next`
(`src/protocol/syslog/receiver.go:81,228`). For each record it resolves the
device from `Observation.Peer` (`src/protocol/syslog/record.go:72`), maps the
record, copies the raw bytes from `Record.Raw`, a `*[]byte` that is nil when
capture is off (`src/protocol/syslog/record.go:170`), applies the raw policy, validates the envelope with protovalidate,
and publishes it with `Leaf.Publish` on `leaf.Subject("ingest.syslog")`. A
publish the buffer refuses is retried with backoff and never dropped
silently. The raw policy takes an injected clock and keeps a per-device
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
validates each result. `rawpolicy_test.go` drives 220 failures in one minute and the
window roll-over with the injected clock. `source_test.go` starts a hub and a
leaf as `edgebus_test.go` does, sends UDP datagrams from a hosted and an
unknown address, and reads the envelope from the hub's edge stream. Nothing
here proves the mapping against a real device's output, since the corpus is a
grammar fixture set (`src/protocol/syslog/README.md`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent`

Waves: U1 U2 | U3

## Verification

```bash
go tool -modfile=tools/buf/go.mod buf lint
go test -race ./test/conformance/proto/... ./src/modules/edgebus/... ./src/edge/agent/...
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src/modules/edgebus src/edge/agent generated/go/proto
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `spec/proto/flowseer/integration/README.md`, the new package README,
      `src/modules/edgebus/README.md`, and `src/edge/agent/README.md`
      describe the envelope, the subject, and the source.
- [ ] `CONCEPTS.md` gains Ingest Record and Raw Evidence.
- [ ] This plan's `status` is set with an outcome note under its title, and
      the parent's `Landed:` line for U1 holds the commit range.
- [ ] No plan labels in code.

## Open questions

- Which type `Provenance.protocol` widens to, and what each existing
  producer of `Provenance` writes into it.
- Which consumers read `SyslogRecord.record_id` today and what they read
  once it is gone.
