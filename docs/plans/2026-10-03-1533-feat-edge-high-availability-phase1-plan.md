---
title: Edge High Availability, Phase 1, Site, Groups, and Read Failover - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-09-05-verified-device-access-direction.md
---

# Edge High Availability, Phase 1, Site, Groups, and Read Failover - Plan

## Goal

Central assigns reads to healthy members of a Site's Edge group and moves
reads after a member becomes stale. Typed Site and group identities and
compare-and-set placement keep all replicas on one assignment.

Stop condition: moving a read can change mutation authority or bypass an
existing mutation barrier.

## Decisions

The [parent's decisions](2026-10-03-1533-feat-edge-high-availability-plan.md) apply. This phase requires the final
central HA phase to land first. Re-check its elected background-module and
watch contracts before implementation. The source paths here describe the
current tree, not a prediction of that phase's final interfaces.

- Add `Site` and `SiteEvent` in `model/site/v1/site.proto`, with typed
  `SiteLocalRef`/`SiteGlobalRef`. Site has its ref and optional display name.
  Event carries Site before and after a change. The file-level doc comment
  declares `SiteConfig` and `SiteState` deliberately absent because Site
  is pure intent, following the [triad convention](../conventions/protobuf.md#the-triad).
  Why: ownership needs identity, not a new reachability or lifecycle
  taxonomy. The leaf package imports no Edge or Integration, avoiding the
  generated Go import cycle that putting Site beside Integration would create.
- Add `EdgeGroup` in `model/edge/v1/group.proto`, with Config/State/Event and
  typed LocalRef/GlobalRef, required Site ref, and one to 64 distinct Edge
  refs in Config. State
  reports healthy member count, and Event describes membership changes.
  `EdgeConfig.site` is required. Why: registry validation can reject a group
  whose members belong to different Sites. Do not add either entity to
  `EntityType` until its existence and cascade contracts exist.
- Registry config holds the Site and group definitions for its integration.
  The integration names the group instead of one Edge. Static configuration
  is deployment-owned and identical across replicas. Membership changes
  require a controlled restart and preserve existing write owners. Why:
  `registry.Registry` loads one immutable prototext snapshot today.
- Store `read_placement` and `write_owner` inside each `DeviceLaneRecord`.
  Each identifies Edge and enrollment. Read placement also has a monotonically
  increasing generation and assignment time. Why: authorization and lane
  updates must compare one durable snapshot. Enrollment follows the consumed
  setup key, as `host.edgeEnrollment` already does for raw windows.
- Initial placement orders device IDs and active members by ID, repeatedly
  chooses the least-loaded member, and uses member ID to break ties. It sets
  both owners once. Write owner never changes in this phase, even after
  its retirement or re-enrollment. Why: a new enrollment does not revoke
  an earlier enrollment's device credential.
- One elected placement pass runs every two seconds. It derives active
  membership through the existing contact thresholds, moves only reads whose
  owner is inactive, and preserves assignments when no replacement exists.
  New and moved reads choose the least-loaded active member. Why: initial
  spread and failure recovery are deterministic without continuous churn.
- Close an open read on movement with a typed retryable placement-change
  result. The operator read handler retries under a fresh journal sequence
  within its original deadline. Concurrent callers join that replacement. Dispatch and reports carry
  `read_placement_generation`. Why: results from a prior assignment must
  never satisfy the replacement read, including an A to B to A cycle.
- Onboarding, dispatch, and credential checks distinguish read ownership
  from write ownership. An onboard report for a read successor cannot clear
  the original writer's mutation confirmations or firmware evidence. Why:
  `dispatchapi.Report` and `journal.MarkOnboarded` currently share one lane.
- Existing read credentials may serve the initial read-only phase. Write
  credentials remain available only to the original write owner. Phase 2
  installs the node-specific accounts. Why: account provisioning is itself
  a typed mutation that must use the existing writer and barrier first.
- Report authorization and placement comparison happen inside the journal's
  compare-and-set, before any fingerprint or retained observation update.
  Why: `dispatchapi/report.go` currently checks `Hosts` before separate writes,
  leaving a check/use race if `Hosts` becomes dynamic.

## Requirements

1. Invalid membership fails at load. Example: duplicate Edge refs, unknown
   Site, cross-Site member, or a member with another tenant is rejected.
2. Initial placement of thirty devices across three active members assigns
   ten to each. An existing assignment is unchanged by adding a member.
3. A becomes stale at `edge_stale_after`, and within two seconds B owns its
   reads. A report from the old generation cannot change observations,
   fingerprint, read completion, or mutation state.
4. The original writer's mutation and every owed control remain routed to
   that writer after a read moves. Its valid pending reports remain accepted.
   A read successor's onboard report cannot reset those confirmations.
5. Ordinary read credential acquisition names an open sequence and placement
   generation. The wrong Edge, enrollment, generation, binding, or tenant
   receives no secret. Typed onboarding requests are the explicit exception:
   only the current read owner or original write owner can probe its role.
   A read-only successor never acquires a submission credential.
6. A read at or beyond its original deadline is closed as failed rather than
   retried. A mutation barrier that disallowed a read before movement still
   disallows it afterwards. Waiters do not hang on the replaced sequence.
7. Two replicas racing to move a read commit one next generation. A failed
   store read or lost leadership cannot create a second assignment or write
   owner. A restart rebuilds placement from the lane record.
8. An inactive writer's reads can move while its writes remain unavailable.
   A retired or re-enrolled writer cannot acquire old submission authority.
   Returning read members never reclaim assignments automatically.
9. Operator requests use authenticated tenant context and existing view or
   mutation authorization. Changing placement cannot change the resolved
   tenant, including a deployment whose group has no active member.

## Out of scope

- Account creation, fence credentials, and automatic write-owner changes.
- Shared TLS or inform identity, address movement, and FreeBSD validation.
- Site or group administration RPCs, dynamic registry reload, and general
  inventory persistence. Registry configuration is trusted deployment input.
  Edge RPC payloads remain untrusted and validated at their boundaries.

## Units

### U1. Site, group, and assignment schemas
Files: spec/proto/flowseer/model/site/v1/site.proto, spec/proto/flowseer/model/site/v1/README.md, spec/proto/flowseer/model/inventory/v1/integration.proto, spec/proto/flowseer/model/edge/v1/group.proto, spec/proto/flowseer/model/edge/v1/edge.proto, spec/proto/flowseer/model/edge/v1/README.md, spec/proto/flowseer/store/device/v1/registry.proto, spec/proto/flowseer/store/device/v1/lane_record.proto, spec/proto/flowseer/edge/dispatch/v1/dispatch.proto, spec/proto/flowseer/edge/dispatch/v1/execution.proto, spec/proto/flowseer/edge/attach/v1/edge_service.proto, spec/proto/flowseer/edge/attach/v1/credential.proto, generated/go/proto/flowseer/, src/services/device/internal/registry/, src/services/device/internal/edgeapi/admin_test.go, src/services/device/internal/edgeapi/service_test.go, src/services/device/internal/edgestore/store_test.go, src/services/device/internal/host/enrollment_internal_test.go, src/services/device/internal/host/host_test.go, src/services/device/internal/authz/authz_test.go, src/services/device/test/integration/bootstrap_env_test.go, src/services/device/test/integration/fixture_test.go, src/edge/agent/internal/syslogsource/source_test.go, src/edge/agent/internal/lanehost/onboard_test.go, src/edge/agent/host/syslog_test.go, deploy/lab/registry.textproto, deploy/lab/write-registry.py, spec/proto/flowseer/model/inventory/v1/README.md
After: none
Change: define the identity families and durable owners above. ListedDevice, read ExecuteRequest, read result and onboard reports, and AcquireReadCredentialRequest carry assignment generation. Credential acquisition has an operation oneof: open read sequence, or a typed onboarding scope naming read-owner or write-owner role. Read requests and read-owner onboarding carry read placement generation. Write-owner onboarding checks the durable writer and enrollment instead. Registry validation resolves Site and group refs. Runtime validation resolves member tenant and enrollment from the edge store before assignment. Audit every registry prototext fixture and builder for the replaced single-edge shape and include all matches in this unit before editing. Regenerate bindings with the pinned buf tool.
Tests: buf lint enforces schema rules. Registry tests cover group bounds, missing refs, cross-Site and cross-tenant membership, and controlled-restart configuration that would remove an existing writer. Existing fixture consumers compile after regeneration.
Verify: `uv run tools/scripts/run.py verify change --full`

### U2. Durable placement and operation ownership
Files: src/services/device/internal/placement/, src/services/device/internal/journal/, src/services/device/internal/dispatchapi/, src/services/device/internal/deviceapi/, src/services/device/internal/edgeapi/, src/services/device/internal/host/, spec/proto/flowseer/store/device/v1/service_config.proto, generated/go/proto/flowseer/store/device/v1/
After: U1
Change: implement the elected two-second placement pass, journal CAS operations, per-operation routing, and atomic report checks. Movement closes the old read with the typed placement-change result. Waiting operator handlers retry within the original deadline and join the replacement through OpenRead. Route mutation controls by the immutable writer. Derive operator tenancy from authenticated context and validate group/member tenancy without selecting an arbitrary active member. Gate ordinary read credential requests on open sequence, assignment, and enrollment. For the typed onboarding scope, check its read or write ownership role and enrollment without requiring an existing read. Use central HA's landed election and watch seams.
Tests: placement, journal, dispatchapi, edgeapi, deviceapi, and host tests cover all requirements, including report-vs-move races, A to B to A, no healthy replacement, enrollment replacement, concurrent replicas, stale fingerprint refusal, unexpired read credentials acquired before a move, mutation barriers, and original-writer reports after read movement. Test concurrent CAS ordering rather than only serial stale calls.
Verify: `uv run tools/scripts/run.py verify change --full`

### U3. Agent assignment refresh and read onboarding
Files: src/edge/agent/internal/lanehost/, src/edge/agent/internal/dispatch/, src/edge/agent/internal/subscribeloop/, src/edge/agent/cmd/agent/, src/modules/localnet/access/
After: U2
Change: refresh listed assignments while connected, onboard newly assigned reads, and echo their generation through reads and reports. Remove obsolete read sessions while retaining mutation state owned by the node. A generation change at the same Edge replaces the read session. Separate read-only onboarding from mutation recovery. Obtain ordinary read credentials for the open sequence. Onboarding uses the typed role-scoped credential request and returns a report bound to that ownership role, so probing the successor cannot reset mutation recovery.
Tests: agent and access tests cover assignment gain/loss without reconnect, generation replacement, cancellation, same-device original mutation plus successor read, and zero credential persistence. A credential refusal or onboarding failure leaves the read owed until its deadline.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/agent src/modules/localnet/access`

### U4. Replica and edge failover integration
Files: src/services/device/test/integration/, deploy/lab/compose.yaml, deploy/lab/central.textproto, deploy/lab/README.md
After: U3
Change: run two central replicas and three separately enrolled agents against one group. Provide the minimal Site and group registry configuration. Demonstrate read failover and retained write ownership without depending on device account management or moving the virtual IP.
Tests: stop a read owner, reconnect streams through another replica, return the node, and race a late report with reassignment. A mutation admitted before failure never executes at a successor. Exercise two tenants with identical local fixture device names. Restart both central processes and prove durable assignments and pending waiter recovery. Use embedded protocol fixtures for ownership checks and record that these tests prove no device-native fence.
Verify: `uv run tools/scripts/run.py verify change --full`

### U5. Direction amendments and runbook
Files: docs/architecture/2026-09-05-verified-device-access-direction.md, docs/architecture/2026-08-20-device-service-and-inventory-direction.md, CONCEPTS.md, src/services/device/README.md, src/edge/agent/README.md, src/services/device/internal/placement/README.md
After: U4
Change: amend accepted records with the implemented Site/group and read-only failover scope. Document unavailable writes, controlled membership changes, contact thresholds, assignment generation, and limits of already-issued credentials. Keep unsupported account and fence procedures explicit.
Tests: prose checks and a runbook walkthrough against the integration fixture.
Verify: `uv run tools/scripts/run.py verify change -- CONCEPTS.md docs/architecture src/services/device/README.md src/edge/agent/README.md src/services/device/internal/placement/README.md`

Waves: U1 | U2 | U3 | U4 | U5

The units stay in one connected cluster because schemas, journal ownership,
agent routing, and integration checks must agree on the same wire contract.

## Verification

Run focused package tests during each unit. Run the device integration tier
using the actual command in its README, with its required containers. Finish
with `uv run tools/scripts/run.py verify change --full`.

## Definition of done

- Verifier receipts cover all changed paths and the full schema check.
- Every requirement has a passing test, including concurrent placement/report.
- Accepted records and package READMEs describe read-only failover limits.
- `plan record implemented` records the five units and commit range.

## Open questions

- Accept the proposed edge HA direction before implementation.
- Central HA is not landed. Confirm the elected pass and cross-replica watch
  interfaces after it lands. A materially different contract requires re-plan.
