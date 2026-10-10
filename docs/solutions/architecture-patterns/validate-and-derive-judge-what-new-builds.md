---
title: Validate and Derive Judge What New Builds, Not What the Caller Wrote
date: 2026-09-11
last_verified: 2026-10-10
category: architecture-patterns
module: src/common/sim/device/vswitch
problem_type: bug
component: sim
severity: high
symptoms:
  - "Derive rebuilds a protocol layer from scratch for a configuration identical to the current one, and roles or timers reset"
  - "A lookup keyed by name answers from the wrong instance, and which one differs between runs of the same configuration"
  - "Two nodes carry the same assigned address after Derive although New assigned unique ones"
root_cause: "New fills defaults (an assigned MAC) and indexes state by a key (an interface name across every VRF) that the caller's configuration does not show, while Validate and Derive were written over the caller's view: Derive diffed a filled config against an unfilled one, and Validate claimed keys per container where the layer keys them globally."
resolution_type: code_fix
applies_when:
  - "Writing a constructor that fills a zero field with a default or an assigned value, and a Derive, Diff, or Compare that reads that field"
  - "Building a lookup map in a constructor from a name inside a nested map, and writing the Validate that is supposed to keep that name unique"
  - "Carrying an assigned value from a current instance into a derived one whose new configuration may claim the value explicitly"
  - "A derived simulator state loses roles, timers, or learned entries for a configuration that did not change"
  - "A Validate runs before a Normalize and refuses a form that the standalone constructor, which normalizes first, accepts"
  - "Checking reported capacity in a loader that omits invalid or incomplete contributions before constructing a smaller executable result"
related_components: [routing, fabric, stp]
tags: [derive, validate, assigned-defaults, key-space, netsim, determinism]
---

## The situation

Phase 4 of the network simulation gave `vswitch.New` two jobs beyond copying
its input: assign a base MAC and stamp it on every zero interface and bridge
address, and build the routing layer's lookup maps. Three defects had one
shape: a check or a comparison ran over the caller's configuration, while the
behavior it judged ran over what `New` had built.

## What is true

- `New` fills state the caller never sees in its own value. The switch
  stamps the assigned base MAC onto a zero bridge address and onto every
  zero routed interface (`Config.Normalize` in `src/common/sim/device/vswitch/config.go`).
- `Derive` therefore builds `next` through `NewWithSpec`, which normalizes the
  target, and compares each layer's key on `cur` with its key on `next`, never
  the raw input (`Derive` in `src/common/sim/device/vswitch/derive.go`). Before the fix the raw
  input's zero address diffed against the filled one, and every derived
  switch with an assigned bridge address rebuilt its spanning tree layer.
- `Validate` claims every key the layer indexes on, over the whole
  configuration, not per container. The routing layer keys interfaces by
  name across every VRF (`Layer.ifaces` in `src/common/sim/layer/routing/layer.go`),
  so `Validate` claims each name once across VRFs
  (`claimedIfaces` in `Config.Validate`, `src/common/sim/layer/routing/config.go`). Before the
  fix two VRFs could name an interface alike, and Go map iteration decided
  which VRF the name resolved to on each construction.
- A carried assignment yields to an explicit claim. The fabric copies an
  address from the current fabric only when the new configuration's
  explicit addresses do not already hold it
  (`carry` in `normalizeConstructionSpec`, `src/common/sim/assembly/assemble.go`); otherwise the node
  takes the next free one.

## How to apply

When a constructor fills or derives anything, ask two questions before
writing the functions that judge the result:

1. Does a comparison (`Derive`, `Diff`, `Compare`) read a field the
   constructor fills? Then compare two constructed values, or fill the
   input the same way first.
2. Does the constructor index by a key that the caller's shape nests inside
   a container? Then `Validate` claims that key at the level the index
   lives, and refuses the second claimant.

3. Does `Validate` run before `Normalize`? Then it refuses input the
   constructor accepts. Validate a normalized copy, and judge on the
   submitted form only the checks whose answer or field path normalization
   changes.

```go
next := New(cfg)
// Both sides as New filled them, so an assigned default is not a change.
if len(stp.Diff(*cur.cfg.STP, *next.cfg.STP)) == 0 { ... }
```

## Evidence

- `src/common/sim/device/vswitch/derive_test.go`
  `TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress`: a switch with a
  zero bridge address, converged as root port, keeps Root/Forwarding through
  `Derive` with the identical input. It failed before the fix with
  `Disabled/Discarding`.
- `src/common/sim/layer/routing/config_test.go`
  `duplicate interface name across VRFs`: `Validate` refuses it. A probe on
  2026-09-11 (macOS, go 1.27) had `ByVLAN(20)` answer the VLAN 10 interface
  in 10 of 50 constructions before the fix.
- `src/common/sim/fabric/routing_test.go`
  `TestDeriveDoesNotCarryAnAddressTheNewConfigurationClaims`: the switch
  takes the next free address when a host claims the carried one.

The third question came from two later defects. `assembly.Config.Validate`
judged a route prefix with host bits set (`10.0.20.5/24`) as submitted, so
`assembly.New` refused what a switch built alone accepted. It now accepts a
configuration valid after `Normalize`, and judges the cable fault, the
accepted multicast list and `fabric.Uncabled` as submitted
(`src/common/sim/assembly/config.go:340-344`). `fabric.Record.Normalize`
validates again after it fills defaults (`src/common/sim/fabric/record.go:63-65`),
so a defaulted `CapturedLen` above a set `OriginalLen` is refused. Commits
`d26f21c1` and `bfe71bcc` record the failing tests:
`TestRecordNormalizeRefusesDefaultedCapturedAboveOriginal` and
`TestRecordNormalizeAcceptsCapturedLenAlone`
(`src/common/sim/fabric/record_test.go:104`). A route prefix and a fault
parameter are the inputs that tell the two orders apart in
`src/common/sim/assembly/normalize_first_test.go`.

## Reported bounds survive omissions

A bound on reported input belongs to the submitted form. Checking only the
constructed result can accept an over-capacity report after another refusal
shrinks it. The MST loader had this defect: 65 distinct admitted IDs became
64 after a conflict, missing allocation, invalid priority or refused port.
Empty allocations preserved the digest, so the identity check did not catch
that loss (commit `b3ee57d5`). The completeness and capacity contract is
stated in `src/common/sim/netmodel/README.md:148-158`. Loading uncertainty
follows the accepted [model-loading direction](../../architecture/2026-09-10-virtual-device-direction.md#uncertainty-localization-and-model-loading).

Count distinct admitted IDs in range before settings validation and fact
resolution. Keep the final candidate check too. `loadMSTInstances` records
`reported[row.GetMstId()] = true` before `validateMSTInstance`
(`src/common/sim/netmodel/load_mst.go:229-232`). `loadMST` then checks both forms
at `:38`:

```go
if reportedCount > bpdu.MaxMSTIRecords || len(m.Instances) > bpdu.MaxMSTIRecords {
```

`TestMSTInstanceCapacity` (`src/common/sim/netmodel/load_mst_test.go:394-463`)
uses empty allocations and checks nine variants, including duplicates and
refused domains that must not inflate the count. The public construction test
at `src/common/sim/netmodel/load_stp_test.go:209-278` checks missing and refused
ports. Commit `b3ee57d5` quotes the disabled-count mutation result:

```text
--- FAIL: TestMSTInstanceCapacity/conflict (0.00s)
```

## What it does not cover

The rule says where a check runs, not what it checks. A constructor that
fills nothing, or that validates its input itself, has no gap here. The
fabric already applies the first question to switches by handing `Derive`
the config it built (`src/common/sim/assembly/assemble.go`), so the defect
was standalone-switch only.
