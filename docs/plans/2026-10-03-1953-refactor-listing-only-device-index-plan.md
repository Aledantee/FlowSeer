---
title: Listing-Only Device Index for the Edge Syslog Source - Plan
type: refactor
date: 2026-10-03
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: no lesson
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# Listing-Only Device Index for the Edge Syslog Source - Plan

> Implemented. 1 unit, 2026-10-03T18:13Z to 2026-10-03T18:13Z.

## Goal

The edge agent accepts syslog from every address its device listing names,
whether or not the management lane has onboarded the device. A datagram from
an address one listed device claims is published as that device's record with
the listed binding. The means: `lanehost.DeviceIndex` becomes a map from
address to the listed rows that claim it, replaced as a whole on each
successful listing, and the `served` flag with its lane plumbing is removed.

**Stop condition:** a consumer of `IngestRecord` needs the lane to have
onboarded the device for the record to be valid. The gate then carries a
requirement and this plan is wrong.

## Decisions

- Resolution depends on the listing alone. A listed device's address
  resolves from the moment the listing is applied, and onboarding no longer
  changes the index. Why: the gate on onboarding authenticates nothing, since
  a UDP datagram is spoofable whether or not the lane logged in to the
  device, and it drops syslog from a listed device whose management session
  is down. `ListedDevice` carries the three fields a record needs,
  `device_id`, `binding_id`, and `ip`
  (`spec/proto/flowseer/edge/attach/v1/device.proto`), so the index needs
  nothing the onboarder learns. This reverses the rule the `DeviceIndex`
  invariant comment states today
  (`src/edge/agent/internal/lanehost/index.go`): that a sole claimant
  resolves only when a lane attempt of this process onboarded it, that this
  state outlives the lane attempt, and that a held device is re-asserted at
  its listed address. (decided by the user, 2026-10-03)
- A device id listed at two addresses resolves from both, each with its own
  row's binding. Why: with the lane out of the index nothing has to pick one
  address, so each row is a claim. This replaces the last-seen-active rule
  the central ingestion parent plan lists first under Follow-ups
  (`docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md`),
  and U1 removes that entry on this decision's authority. (decided by the
  user, 2026-10-03)
- The rule goes into the ingestion direction record as a dated amendment.
  Why: the stop condition constrains every consumer of `IngestRecord` in the
  later phases, and the parent plan decided the two-address question
  differently, so the decision outlives this plan.
- An address two different listed devices claim resolves to no device, and
  the datagram is counted under `ambiguous_source`. Why: unchanged from the
  landed rule, and it no longer depends on which of the two onboarded.
- Two rows with the same device id at the same address are one claim, and
  the later row's binding is the one returned. Why: the claims at an address
  are keyed by device id and rows are read in order. Central cannot send
  such a listing today, and neither can it send one device id at two
  addresses: `ListDevices` builds one row per id the registry returns
  (`src/services/device/internal/edgeapi/service.go`), and the registry keys
  devices by id (`src/services/device/internal/registry/registry.go`,
  `spec/proto/flowseer/store/device/v1/registry.proto`). Both cases are
  covered by hand-built listings only.
- `ApplyListing` takes the listing alone, builds a new claims map, and swaps
  it in under the write lock. Why: a lookup then never sees a partly applied
  listing, and a whole-map swap needs no per-device bookkeeping. The landed
  index applies a listing under one lock for the same reason
  (`src/edge/agent/internal/lanehost/index.go`, `ApplyListing`).
- The zero value of `DeviceIndex` is an empty index. Why: `Lookup` reads a
  nil map as no claim and `ApplyListing` assigns a new map, so nothing needs
  a constructor. `NewDeviceIndex` stays for its callers.
- `DeviceIndex.Add` is removed. Why: its one production caller is the
  onboarder (`src/edge/agent/internal/lanehost/onboard.go`, `onboard`), which
  no longer writes the index. Tests seed an index through `ApplyListing`.
- `Sync` applies the listing before the onboarding loop, as it does today.
  Why: a slow or failing `AddDevice` must not delay resolution
  (`src/edge/agent/internal/lanehost/onboard.go`, `Sync`).
- A failed `ListDevices` leaves the index as it was. Why: `Sync` returns
  before `ApplyListing` on that path today, and the last listing is the best
  statement of what the edge hosts.
- The index stays in `src/edge/agent/internal/lanehost` and stays owned by
  the agent's assembly for the life of the process
  (`src/edge/agent/host/host.go`, `deviceIndex`). Why: `Sync` is the one
  place a listing arrives, and the syslog source outlives a lane attempt.
- The lane keeps its handling of a device id listed twice: the first row is
  onboarded, and the second finds the device held and logs
  `flowseer.edge.device.listing_diverged` on each `Sync`
  (`src/edge/agent/internal/lanehost/onboard.go`, `onboard`). Why: the lane
  is outside this plan, and central cannot send such a listing.
- The solution
  `docs/solutions/architecture-patterns/a-process-lifetime-index-fed-by-attempt-state-applies-listings-under-one-lock.md`
  is deleted with its row in `docs/solutions/README.md`. Why: it quotes the
  `heldIDs` snapshot and the `served` carry-over, which this plan removes,
  and what remains of it (apply a listing in one step) is one function the
  code states. (decided by the user, 2026-10-03)

## Requirements

1. An address one listed device claims resolves to that device whether or
   not the lane onboarded it. Example: `Sync` over a listing of device D at
   172.16.0.6 with binding B, where the registrar fails `AddDevice` for D.
   `Lookup("172.16.0.6")` returns `LookupFound` with D and B.
2. A device a later listing omits stops resolving. Example: `Sync` over
   [D at 172.16.0.6], then over []. `Lookup("172.16.0.6")` returns
   `LookupUnknown`.
3. A device a later listing moves resolves at the new address only.
   Example: [D at 172.16.0.6], then [D at 172.16.0.7]. The first address
   returns `LookupUnknown` and the second `LookupFound`, with D held by the
   lane throughout.
4. An address two different devices claim resolves to neither, whichever
   onboarded. Example: [D1 at 172.16.0.6, D2 at 172.16.0.6] with `AddDevice`
   failing for D2. `Lookup` returns `LookupAmbiguous`.
5. A device id listed at two addresses resolves from both with each row's
   binding. Example: [D at 172.16.0.6 with B1, D at 172.16.0.7 with B2]. The
   first address returns D with B1 and the second D with B2.
6. A row with an unusable address claims nothing and the other rows apply.
   Example: [D1 at 172.16.0.6], then [D1 with the two-octet address
   `TestOnboard_UnusableAddressIsReportedAndNotOnboarded` builds, D2 at
   172.16.0.7]. 172.16.0.6 returns `LookupUnknown` and 172.16.0.7 returns D2.
7. A failed `ListDevices` leaves the index unchanged. Example: `Sync`
   succeeds over [D at 172.16.0.6], then `ListDevices` returns an error. The
   address still returns D.
8. A listing is applied before onboarding starts. Example: the registrar
   blocks inside `AddDevice` for the first row of [D1, D2]. While it blocks,
   both addresses return `LookupFound`.
9. A listing is applied as a whole. Example: the index holds D1 and D2 at
   one address, and the next listing holds D2 and D3 there. A lookup running
   beside `ApplyListing` returns `LookupAmbiguous` on every read.
10. The index outlives a lane attempt. Example: a first `Onboarder` runs
    `Sync` over [D at 172.16.0.6]. A second `Onboarder` on the same index,
    whose lister returns an error, runs `Sync`, which returns that error.
    The address still returns D.
11. A peer address in IPv4-mapped form or with a zone matches the listed
    plain form. Example: D1 listed at 192.0.2.10 in the V4 arm and D2 at
    fe80::1 in the V6 arm. `Lookup("::ffff:192.0.2.10")` returns D1 and
    `Lookup("fe80::1%en0")` returns D2, after one `ApplyListing` and after a
    second with the same rows.
12. The syslog source publishes a record for a listed device the lane never
    onboarded. Example: a source test seeds the index with one
    `ApplyListing` call and no onboarder, sends one datagram from the listed
    address, and reads one `IngestRecord` naming that device and binding.

## Out of scope

- Syslog a device sends from an address other than its listed management
  address. It is dropped as `unknown_source`, as today.
- Syslog from a site device another edge hosts. The agent's only inventory
  is the listing central sends this edge.
- Validation of `ListDevicesResponse` and a uniqueness rule on device ids.
  It stays a follow-up in the central ingestion parent plan.
- The lane's own handling of a held device whose listing changes, and of a
  device id listed twice. `held`, the divergence log, and `AddDevice` stay
  as they are.
- Input trust: `ApplyListing` reads the listing central returns to an
  enrolled edge. Central is trusted and the listing is not validated, as the
  follow-up above records.

## Units

### U1. Index from the listing alone

Files: src/edge/agent/internal/lanehost/index.go, src/edge/agent/internal/lanehost/index_test.go, src/edge/agent/internal/lanehost/onboard.go, src/edge/agent/internal/lanehost/onboard_test.go, src/edge/agent/internal/syslogsource/source_test.go, src/edge/agent/host/host.go, src/edge/agent/host/syslog_test.go, src/edge/agent/README.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/solutions/architecture-patterns/a-process-lifetime-index-fed-by-attempt-state-applies-listings-under-one-lock.md, docs/solutions/README.md, docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md
After: none
Change: `DeviceIndex` holds one map from normalized address to the claims on
it, the claims at an address keyed by device id, each a device ref and a
binding ref, guarded by one `sync.RWMutex`. `deviceRecord`, the `devices`
map, the `served` field, `setDeviceLocked`, `dropClaimLocked`,
`addClaimLocked`, and `Add` are gone. `ApplyListing(devices
[]*attachv1.ListedDevice)` skips a nil row, a row with an empty device id,
and a row whose address `addressOf` refuses, builds a new map from the
remaining rows in order, and replaces the old map under the write lock.
`Lookup` returns `LookupFound` with the claim when the address has one
claimant, `LookupAmbiguous` for two or more, and `LookupUnknown` for none.
The doc comments on `DeviceEntry`, `LookupResult`, `DeviceIndex`,
`ApplyListing`, and `Lookup` state these rules and that the zero value is an
empty index. `Sync` calls `Index.ApplyListing(devices)` with no held
snapshot, and `onboard` no longer writes the index. The comments on `Sync`,
`onboard`, and `OnboardConfig.Index` drop the held re-assert and say the
index follows the listing. The comment on `onboardConfig` in `host.go`,
which says a device is a known sender "from the moment it is onboarded",
says from the moment it is listed. `source.go` is unchanged, since `Lookup`
keeps its signature. `src/edge/agent/README.md`, in its syslog section and
its paragraph on a held device whose listing changes, says an address
resolves when one listed device claims it, that a device id at two addresses
resolves from both, that a lane restart keeps the last listing, and that a
process restart starts with an empty index until the first listing. The
ingestion direction record gains a dated amendment: the edge resolves a
syslog sender from its device listing alone, an address two devices claim
resolves to neither, and a consumer of `IngestRecord` may not assume the
lane serves the device a record names. The solution file is deleted with its
row in `docs/solutions/README.md`. In the central ingestion parent plan's
Follow-ups, the last-seen-active entry is removed on the second Decision's
authority, and the validation entry stays with its last sentence replaced by
the registry citation the Decisions give.
Tests: every test below seeds through `ApplyListing` with all rows of one
listing in one call, and no test calls `Add`.
In `index_test.go`, kept with that change:
`TestDeviceIndex_PruneKeepsListedIDsAndDropsTheRest` (requirement 2),
`TestDeviceIndex_ApplyListing_SharerReplacedRemainsAmbiguous` (requirement
9, now failing on any result other than `LookupAmbiguous`),
`TestDeviceIndex_PruneBesideLookupConcurrent`,
`TestDeviceIndex_MappedIPv4MatchesPlainIPv4` and
`TestDeviceIndex_LinkLocalIPv6WithZone` (requirement 11, seeded with V4-arm
and V6-arm rows), `TestDeviceIndex_ApplyListingSkipsNilRowAndEmptyDeviceID`,
`TestDeviceIndex_ApplyListingWithEmptyBindingIDKeepsARef`, and
`TestDeviceIndex_SharedAddressAcrossTwoOnboarders`. Rewritten with a full
listing per step: `TestDeviceIndex_SharedAddressResolvesToNoDeviceUntilOneLeaves`.
New: requirement 5, requirement 6, the same-id same-address pair returning
the later binding, and a zero-value index that resolves after one
`ApplyListing`. Removed, since they pin the removed rule:
`TestDeviceIndex_AddAndLookup`, `TestDeviceIndex_Replace`,
`TestDeviceIndex_DeviceAddedAtSecondAddressRemovesFirstAddress`,
`TestDeviceIndex_LookupSurvivesSecondOnboarder`, and
`TestDeviceIndex_AddWithEmptyDeviceIDIsNoOp`.
Requirement 9's test fails against a two-step apply only by chance, so the
whole-map swap is held by reading `ApplyListing`, not by this test.
In `onboard_test.go`, kept as they are:
`TestSync_ListingDropsDeviceRemovesItsAddressFromIndex` (requirement 2),
`TestSync_HeldDeviceRelistedAtNewAddressResolvesFromNewAddressAndNoLongerOld`
(requirement 3),
`TestSync_TwoListedDevicesAtOneAddressResolveToNeitherWhenOneFailsOnboarding`
(requirement 4), `TestSync_FailedListDevicesLeavesIndexAsItWas` (requirement
7), and `TestSync_MappedIPv4NormalizedAcrossSyncs`. Rewritten:
`TestSync_DeviceRelistedAtNewAddressFailingOnboardingNoLongerResolvesFromOld`
(the new address now returns `LookupFound`),
`TestSync_DuplicateDeviceInListingPinsCurrentBehavior` (both addresses
return the device after each `Sync`, and its lane assertions stay), and
`TestSync_PruneAndRecordClaimsBeforeOnboarding` (requirement 8, both
addresses `LookupFound` while `AddDevice` blocks). New: requirement 1 and
requirement 10. Removed:
`TestSync_LaneRestartCarriesServedStatusWithTheListedBinding`. Every other
test in the file that calls `Lookup` is kept and its expectation checked
against these requirements. A test whose expectation these requirements do
not settle is a blocker, not a judgment call.
In `syslogsource/source_test.go` (package `syslogsource_test`) and
`host/syslog_test.go` (package `host`), the twelve `index.Add` calls become
one helper per package that takes every row of one listing, validates each
`ListedDevice`, and calls `ApplyListing` once. The shared-address source
test passes both devices in one call. One new source case proves
requirement 12. Nothing in this unit tests a real device's syslog, as the
landed tests do not.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent/internal/lanehost/index.go src/edge/agent/internal/lanehost/index_test.go src/edge/agent/internal/lanehost/onboard.go src/edge/agent/internal/lanehost/onboard_test.go src/edge/agent/internal/syslogsource/source_test.go src/edge/agent/host/host.go src/edge/agent/host/syslog_test.go src/edge/agent/README.md docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md docs/solutions/README.md docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md`

Waves: U1

## Verification

- `go test -race ./src/edge/agent/...`
- `.claude/skills/verify-change/scripts/verify-change.sh --` on every changed
  path.
- `git grep -n 'unserved\|heldIDs\|onboarded by a lane\|re-assert\|moment it is onboarded' -- src/edge/agent`
  prints nothing.

## Definition of done

- [ ] The verifier is green for every changed path.
- [ ] `src/edge/agent/README.md` and the comment on `onboardConfig` state
      the listing-only rule and name no onboarding condition for syslog.
- [ ] The ingestion direction record holds the dated amendment.
- [ ] The solution file and its row in `docs/solutions/README.md` are gone.
- [ ] The central ingestion parent plan's Follow-ups no longer list the
      last-seen-active rule.
- [ ] This plan's `status` is set with an outcome note under its title.
- [ ] No plan labels in code.
