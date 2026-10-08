---
title: Raw Window - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# Raw Window - Plan

## Goal

An authorized operator opens a bounded raw window for a device, an integration,
or an ingestion source on one edge. A separate Connect stream delivers the
control, and the adapter attaches original bytes only while the edge's local
window permits them.

Stop condition: a selector requires central to read integration-kind-specific
configuration. Re-open the ingestion direction before adding that coupling.

## Evidence

- Phase 1 is on this tree through `646d080c`, recorded in the
  [parent state](2026-10-02-2331-feat-central-ingestion-pipeline-plan.state.json).
  This phase is neither landed nor retired on `main`.
- [RawEvidence](../../spec/proto/flowseer/integration/ingest/v1/ingest_record.proto)
  already defines `WINDOW`. The
  [source](../../src/edge/agent/internal/syslogsource/source.go) retains bytes
  only on parse failure, and
  [BuildEnvelope](../../src/edge/agent/internal/syslogsource/mapper.go)
  always labels retained bytes `PARSE_FAILURE`.
- [ListedDevice](../../spec/proto/flowseer/edge/attach/v1/device.proto)
  carries a device and binding id, but no integration id.
  [BindingGlobalRef](../../spec/proto/flowseer/model/inventory/v1/binding.proto)
  is an opaque id, so the edge cannot derive the integration from it.
  The [registry](../../spec/proto/flowseer/store/device/v1/registry.proto)
  holds the integration that owns its bindings.
- [Capture assignments](../../src/edge/agent/internal/capture/subscribe.go)
  already use an edge-opened Connect stream outside the device lane.
  [Dispatch](../../spec/proto/flowseer/edge/dispatch/v1/dispatch.proto)
  requires a device id. The
  [device index](../../src/edge/agent/internal/lanehost/index.go) resolves
  syslog before onboarding succeeds.
- [Intake](../../src/services/device/internal/intake/README.md) already
  separates raw evidence from typed records. No new evidence storage path
  or ClickHouse work belongs here.

## Decisions

- Support device, integration, and source selectors now. Why: the
  [ingestion direction](../architecture/2026-10-02-central-ingestion-pipeline-direction.md)
  names all three scopes. Each window belongs to one edge, and its selector
  is a required typed oneof. An integration selector matches the
  integration associated with the resolved binding, never its kind.
  (decided by the user, 2026-10-08)
- Use a separate edge-opened `RawWindowEdgeService.Subscribe`, with a unary
  `Report`. Why: ingestion must work when a listed device has no running
  lane, and the capture assignment path establishes this transport shape.
  Central serves both RPCs behind signed-edge assertion middleware.
  (decided by the user, 2026-10-08)
- Promote the cross-package contract to
  [Raw Window Control](../architecture/2026-10-08-raw-window-control-direction.md).
  It is accepted. The implementation amends the ingestion and authorization
  records in the same change.
- A source is a stable edge-local adapter key, using the existing
  `ingest.<source>` token. `syslog` covers all listeners of the current
  syslog adapter. Why: listener addresses and record types are not source
  identities. The subscribe request advertises enabled source keys, and
  central refuses an unknown key. This adds no ingestion source entity.
- Add required `ListedDevice.integration_id`, populated from the registry,
  and carry it in each `DeviceEntry`. Why: integration matching needs the
  same listing snapshot as device and binding resolution. Validate a whole
  listing before replacing the index, reject conflicting duplicate claims,
  and preserve the prior snapshot on a failed listing.
- Every window has a UUID ref owned by its edge, immutable configuration,
  and Config/State/Event shapes in `model/rawwindow/v1`. State distinguishes
  pending, active, and terminal with a typed stop reason. Why: an accepted
  API call does not prove the edge has started retaining bytes.
- Open, close, and get use `api/ingest/v1.RawWindowService`. Open authorizes
  `edge#capture`, then `tenant#full_payload` and a fresh
  `FullPayloadActive` store check. Get and close load by ambient tenant and
  window id, then require capture access on the stored owning edge. Why:
  the [authorization record](../architecture/2026-09-30-operator-authorization-direction.md)
  reserves raw payload for an explicit grant. No new OpenFGA relation is
  needed. Principal and tenant come from the admitted context.
- Open and close enter the existing operator action trail. An unavailable
  attempt publisher refuses the action before state is created. A close
  changes the durable desired state before delivery, and remains owed until
  reported or expired. Why: the
  [retained-delivery lesson](../solutions/architecture-patterns/state-a-transient-refusal-must-not-block-is-state-nothing-retries.md)
  rules out making a transient send failure strand the transition.
- Expiry and positive `max_bytes` are mandatory, with no implicit duration
  or byte default. Central admits expiry at most one hour after admission.
  The edge also bounds its initial elapsed deadline to one hour, and delayed
  delivery or replay cannot extend the original expiry. Why: operators need
  time to reproduce a failure while collection remains bounded.
  (decided by the user, 2026-10-08)
- Deployment configuration supplies a required aggregate byte allowance per
  edge. Operators choose each window's positive budget within that allowance.
  Reserve full declared budgets until terminal acknowledgement or expiry,
  including a close awaiting delivery. Opens fail without an allowance or
  when their combined reservations would exceed it. Why: delayed controls
  must not overbook the edge, and reported consumption must not replenish
  admission capacity. There is no implicit allowance or fixed product ceiling.
  (decided by the user, 2026-10-08)
- A byte budget counts original payload octets selected into an envelope,
  once per record, before publishing. It is not a promise of durable
  delivery. Retries of the same serialized record consume no further
  budget. A payload that does not fit ends that window without truncating
  the payload. Normal typed ingestion and parse-failure policy continue.
- All matching eligible windows are charged, with bytes aggregated across
  every device in each integration or source scope. One raw copy names the
  sorted distinct refs of the windows that paid for it. An exhausted window
  stops while another matching window can retain the record. `WINDOW`
  takes precedence over `PARSE_FAILURE`, with suppression count absent.
  The parse-failure policy is evaluated only when no window retains the
  payload. Why: overlapping windows must not duplicate evidence or evade
  their individual budgets.
- Central stores one CAS record per tenant and edge in a dedicated KV bucket.
  It contains the process epoch, monotonically increasing control revision,
  and at most 16 unexpired windows, including close tombstones. Terminal rows
  remain queryable for up to 24 hours, capped at the newest 64 per edge.
  The serialized record is bounded at 256 KiB, evicting oldest expired
  terminal rows first. Reason text is at most 2048 characters. Why: the
  pinned nats-server v2.15.0 defaults to a 1 MiB payload limit
  (`server/const.go`, `MAX_PAYLOAD_SIZE`), and one CAS admits a window and
  bounds the concurrent aggregate without races between keys.
- The edge persists an increasing boot generation before connecting, then
  creates a UUID process epoch. Subscribe carries both and enabled sources.
  Central accepts only increasing generations or a repeat of the current
  generation with the same epoch, so a delayed old subscribe cannot reclaim
  it. The relay rechecks the fence before every send and closes a superseded
  stream. Each control carries generation, epoch, and per-window revision,
  window ref, and immutable open configuration or close intent. Central
  sends an initial ready frame even with no windows, resends owed controls
  every five seconds, and rejects reports for another edge or epoch.
- Same-epoch reconnects preserve counters and original deadlines. Duplicate
  starts report current state without renewing either. A greater revision for
  the same window supersedes older controls. Revisions of other windows do
  not discard an undelivered start. Close creates a tombstone through the
  original expiry, so a delayed start cannot reopen it. Central cancels
  previous-epoch windows on an edge process restart and does not reissue
  them. Opening another window is a new audited operator action. Why:
  replaying a start into empty memory would replenish a budget.
- At first admission the edge derives an elapsed-time deadline from the
  remaining wall-clock expiry, and checks both deadlines per record. Wall
  admission uses a nondecreasing high-water reading, so reclaimed expired
  tombstones cannot be revived after clock rollback. A duplicate assignment
  never extends the elapsed deadline.
  Go 1.27.1's `/opt/homebrew/Cellar/go/1.27.1/libexec/src/time/time.go`,
  inspected with `go doc time.Time`, documents
  `time.Now`'s monotonic reading and `Time.Add` preserving it. Tests supply
  independent wall and elapsed clocks rather than racing a clock callback.

## Requirements

1. Each selector matches only its scope. Example: device A and B belong to
   integration I, C belongs to J. An I window includes A and B, not C. A
   source `syslog` window includes those records but not a synthetic second
   adapter's records. A device A window includes neither B nor C.
2. Only uniquely resolved senders retain evidence. Example: an ambiguous or
   unknown sender produces no envelope even during a source-wide window.
   A listed device with failed onboarding can still match a window.
3. Expiry is checked by the edge at the raw selection point. Example: a
   record selected one tick before expiry carries `WINDOW`, one at expiry
   does not, even when close is lost. Retained records buffered before
   expiry may arrive at central later. An expiry exactly one hour after
   admission is allowed, while one tick beyond it is refused. Delayed delivery
   preserves the original expiry and gives the edge only the remaining time.
4. Budgets cover whole scopes. Example: an integration window with ten bytes
   accepts six bytes from A, then four from B, and ends. The next byte from
   either device does not carry raw because of that window. A six-byte
   record with five remaining bytes is not truncated.
5. Overlap has one raw copy and independent accounting. Example: windows W1
   and W2 both match a four-byte record and each has four bytes remaining.
   Both debit four and the envelope names both refs. If W1 has only three
   remaining, it ends and the envelope names W2 alone.
6. Replay cannot renew collection. Example: a duplicate start after six of
   ten bytes were spent leaves four remaining. Close revision 8 followed
   by start revision 7 stays closed. A new process epoch starts empty and
   central cancels windows from the previous epoch. A delayed subscribe with
   an older boot generation is refused.
7. Authorization is checked against stored ownership. Example: a tenant
   admin without full-payload access cannot open. An expired grant is
   refused even while its projected relationship remains. A caller cannot
   change the edge in a close or get request to gain access.
8. Operator identity and audit survive failure. Example: a spoofed
   requested-by value is replaced with the admitted principal. A failed
   attempt publication creates no window. A lost close delivery leaves
   a durable close intent that is resent without waiting for another call.
9. Central reports pending until an edge acknowledgement arrives. Example:
   a lost active report leaves the API pending and triggers start replay,
   which preserves the edge counter. A terminal edge report dominates a
   delayed active report. Reports include control revision and cumulative
   counters, and counters never decrease within an epoch.
10. Existing intake produces evidence with window refs and a typed envelope
    without raw. Example: one valid syslog record in a window yields one
    evidence publication and one raw-free typed publication with the same
    tenant and record id, using the existing evidence retention limits.
11. Admission stays within the configured edge allowance. Example: budgets
    six and four reserve an allowance of ten, so another byte is refused.
    A lost close retains its reservation until acknowledged or expired.
    Missing configuration refuses opens instead of supplying a default.

## Out of scope

New collectors, unknown-sender discovery, central-hosted adapters, evidence
tail or download APIs, UI, history storage, device mutation lanes, and concurrent agent processes
sharing one enrollment/state directory. The epoch file is metadata only and
never restores windows or counters.
The inputs are authenticated operator requests and enrolled-edge controls,
plus untrusted device syslog. Validation enforces tenant ownership and
malformed-input bounds. A compromised enrolled edge is not made trustworthy
by this control protocol. Byte limits govern window evidence, not existing
parse-failure evidence, serialized envelope overhead, or transport retries.

## Units

### U1. Raw-window contracts and schema admission

Files: spec/proto/flowseer/model/rawwindow/v1/, spec/proto/flowseer/api/ingest/v1/, spec/proto/flowseer/edge/ingest/v1/, spec/proto/flowseer/integration/ingest/v1/ingest_record.proto, spec/proto/flowseer/event/operator/v1/operator_action_event.proto, spec/proto/flowseer/store/device/v1/raw_window_record.proto, spec/proto/flowseer/store/agent/v1/raw_window_epoch.proto, generated/go/proto/, test/conformance/proto/layering_test.go, test/conformance/proto/integration_ingest_rules_test.go, test/conformance/proto/event_operator_rules_test.go, test/conformance/proto/raw_window_rules_test.go
After: none
Change: Model refs, selectors, budgets, lifecycle, control/report variants, operator RPC rules, and typed audit targets are defined. RawEvidence carries window refs exactly for WINDOW and rejects a suppression count on that reason. Schema import rows admit the new leaf and service packages. Boot generation and epoch have a storage message under store/agent. Only buf generation writes generated files.
Tests: raw_window_rules_test.go proves absent selectors, invalid timestamps, zero byte budgets, invalid epochs/revisions, missing refs, and lifecycle invariants. integration_ingest_rules_test.go updates the existing WINDOW case and rejects inconsistent reason/ref combinations. layering_test.go proves the added boundaries. No executable files enter spec/proto.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/rawwindow/v1 spec/proto/flowseer/api/ingest/v1 spec/proto/flowseer/edge/ingest/v1 spec/proto/flowseer/integration/ingest/v1 spec/proto/flowseer/event/operator/v1 spec/proto/flowseer/store/device/v1/raw_window_record.proto spec/proto/flowseer/store/agent/v1/raw_window_epoch.proto generated/go/proto test/conformance/proto`

### U2. Integration metadata in a validated listing

Files: spec/proto/flowseer/edge/attach/v1/device.proto, generated/go/proto/flowseer/edge/attach/v1/, src/services/device/internal/registry/registry.go, src/services/device/internal/registry/registry_test.go, src/services/device/internal/edgeapi/service.go, src/services/device/internal/edgeapi/listdevices_test.go, src/edge/agent/internal/lanehost/index.go, src/edge/agent/internal/lanehost/index_test.go, src/edge/agent/internal/lanehost/onboard.go, src/edge/agent/internal/lanehost/onboard_test.go, src/edge/agent/internal/syslogsource/source_test.go, src/edge/agent/host/syslog_test.go, test/conformance/proto/edge_attach_rules_test.go
After: U1
Change: Required integration_id joins ListedDevice, with a registry accessor supplying it to ListDevices. Every existing listing builder and fixture in these paths supplies it. A whole listing is validated before index replacement or onboarding. DeviceEntry preserves integration with device and binding from the same row. Conflicting duplicates are refused instead of choosing the last.
Tests: Registry and listing tests pin integration ownership. Index and onboard tests cover malformed responses, duplicate claims, address changes, multiple bindings, failed refresh, and offline onboarding. Source and host fixtures retain their existing outcomes. edge_attach_rules_test.go checks the new field and listing consistency rules.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/edge/attach/v1 generated/go/proto/flowseer/edge/attach/v1 src/services/device/internal/registry src/services/device/internal/edgeapi src/edge/agent/internal/lanehost src/edge/agent/internal/syslogsource/source_test.go src/edge/agent/host/syslog_test.go test/conformance/proto/edge_attach_rules_test.go`

### U3. Central window state, operator API, and assignment relay

Files: src/services/device/internal/rawwindowapi/, src/modules/edgebus/hub.go, src/modules/edgebus/hub_test.go, src/modules/edgebus/subjects.go, src/modules/edgebus/subjects_test.go, src/modules/edgebus/README.md, spec/proto/flowseer/store/device/v1/service_config.proto, generated/go/proto/flowseer/store/device/v1/, src/services/device/internal/host/config.go, src/services/device/internal/host/config_test.go
After: U1 U2
Change: A required deployment byte allowance enables open, with no implicit allowance. The tenant/edge CAS store admits and closes bounded windows, prunes terminal rows, and returns pending/active/terminal state. Operator handlers resolve selectors through registry data, enforce capture and fresh full-payload checks, and stamp the principal. The edge relay verifies ownership, advertises readiness, fences epochs, sends owed controls, and applies monotonic reports.
Tests: Store tests race concurrent opens at the live-window limit, test aggregate byte admission and missing allowance, retain reservations across lost close, restart central from persisted state, and pin terminal pruning and the serialized size bound. Closed-but-unexpired tombstones retain their slot. Relay tests also supersede a still-open old stream. Operator tests cover the one-hour expiry boundary, tenant mismatch, unsupported source, grant expiry, forged actor, and idempotent close. Relay tests lose start/report/close messages, reconnect, change epochs, reorder revisions, and refuse another edge's report. rawwindowapi/store_test.go exercises the bucket as a consumer outside edgebus.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/rawwindowapi src/modules/edgebus spec/proto/flowseer/store/device/v1/service_config.proto generated/go/proto/flowseer/store/device/v1 src/services/device/internal/host/config.go src/services/device/internal/host/config_test.go`

### U4. Edge matching, budgets, and raw selection

Files: src/edge/agent/internal/rawwindow/, src/edge/agent/internal/identity/store.go, src/edge/agent/internal/identity/store_internal_test.go, src/edge/agent/internal/syslogsource/source.go, src/edge/agent/internal/syslogsource/source_test.go, src/edge/agent/internal/syslogsource/mapper.go, src/edge/agent/internal/syslogsource/mapper_test.go, src/edge/agent/internal/syslogsource/rawpolicy_test.go
After: U1 U2
Change: An identity-store method durably advances the boot generation before use, refusing corruption and overflow. A process-owned controller handles controls, matches device/integration/source, debits aggregate payload bytes, preserves tombstones and deadlines, and queues cumulative reports. Syslog selects WINDOW evidence before falling back to parse-failure policy. A publish retry reuses its envelope and debit.
Tests: Identity-store tests cover a failed rename/directory sync, restart, corruption, and overflow, with no collection after a failed generation write. Controller tests pin the three selectors, overlap, byte boundaries, stale controls, concurrent matching, wall rollback, elapsed expiry, and process restart. Source tests exercise unknown/ambiguous senders, failed onboarding, empty datagrams, WINDOW precedence, fallback sampling, and a refused publish retried without double charging. Mapper tests pin sorted window refs and absence of suppression count.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent/internal/rawwindow src/edge/agent/internal/identity src/edge/agent/internal/syslogsource`

### U5. Host assembly and operator audit

Files: src/services/device/internal/host/host.go, src/services/device/internal/host/serve.go, src/services/device/internal/host/host_test.go, src/services/device/internal/host/serveconnect_internal_test.go, src/services/device/internal/actiontrail/interceptor.go, src/services/device/internal/actiontrail/actiontrail_test.go, src/edge/agent/host/host.go, src/edge/agent/host/syslog_test.go, src/edge/agent/host/wiring_test.go, src/edge/agent/host/host_test.go
After: U3 U4
Change: Central assembles the API and relay behind existing interceptors. The edge starts one controller and epoch outside retried module attempts, opens the signed control stream, and shares the controller with its adapter. Open/close audit actions include immutable target, reason, expiry, and byte budget without raw payload. Existing subscribe-loop events use observability conventions.
Tests: Host tests open a real signed stream, prove empty-stream readiness, and verify module retries preserve budgets while a process restart does not. Action-trail tests cover attempt failure, handler denial, successful open/close, failed completion publication, principal stamping, and payload-free events.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/host src/services/device/internal/actiontrail src/edge/agent/host`

### U6. Cross-process proof and direction amendments

Files: src/services/device/test/integration/raw_window_test.go, src/services/device/internal/intake/intake_test.go, src/services/device/README.md, src/edge/agent/README.md, CONCEPTS.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-09-30-operator-authorization-direction.md, docs/architecture/2026-08-20-network-model-structure-direction.md
After: U5
Change: A tagged integration test sends syslog through an agent and intake under each scope. Package docs show open/get/close and pending versus active state. Accepted records acquire dated amendments for the raw-window transport, authorization, and schema packages after Raw Window Control is accepted.
Tests: raw_window_test.go proves WINDOW evidence and raw-free typed output, lost close, budget exhaustion across devices, duplicate start, and a new process epoch. intake_test.go pins preserved window refs in evidence and their absence from typed publications.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/test/integration/raw_window_test.go src/services/device/internal/intake/intake_test.go src/services/device/README.md src/edge/agent/README.md CONCEPTS.md docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md docs/architecture/2026-09-30-operator-authorization-direction.md docs/architecture/2026-08-20-network-model-structure-direction.md`

Waves: U1 | U2 | U3 U4 | U5 | U6

## Verification

- Generate bindings with `go tool -modfile=tools/buf/go.mod buf generate`.
  Run `go tool -modfile=tools/buf/go.mod buf lint` for schema enforcement.
- Run focused race tests for rawwindowapi, rawwindow, syslogsource, lanehost,
  both hosts, and actiontrail as each unit lands.
- Run `go test -race -tags=integration ./src/services/device/test/integration -run TestRawWindow`.
  The new test uses the existing real agent/central fixture and names any
  container prerequisite in that package's README.
- Run the diff-aware verifier on every changed path, then review against
  this plan and the accepted records. Adding import-order rows is schema
  admission, not a lint exclusion.
- No implementation unit changes a policy surface. If current source requires
  one, obtain explicit guardrail review before making that edit.

## Definition of done

- [x] Explicit expiry, the one-hour maximum, and byte admission are decided.
- [x] Raw Window Control is accepted before its contract is implemented.
- [ ] All requirements have focused tests and the tagged cross-process proof.
- [ ] Every changed path passes the diff-aware verifier.
- [ ] Package documentation and accepted direction amendments land with code.
- [ ] Record the six-unit implementation through `plan record implemented`.
- [ ] Unit and requirement labels appear only in this plan.
