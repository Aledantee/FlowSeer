---
title: Validate and Derive Judge What New Builds, Not What the Caller Wrote
date: 2026-09-11
category: architecture-patterns
module: src/common/netsim/vswitch
problem_type: bug
component: netsim
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
related_components: [routing, fabric, stp]
tags: [derive, validate, assigned-defaults, key-space, netsim, determinism]
---

## The situation

Phase 4 of the network simulation gave `vswitch.New` two jobs beyond copying
its input: assign a base MAC and stamp it on every zero interface and bridge
address, and build the routing layer's lookup maps. Three defects came out of
the review, all one shape: a check or a comparison ran over the caller's
configuration, while the behavior it judged ran over what `New` had built.

## What is true

- `New` fills state the caller never sees in its own value. The switch
  stamps the assigned base MAC onto a zero bridge address and onto every
  zero routed interface (`src/common/netsim/vswitch/switch.go:71`, `:77`).
- `Derive` therefore compares `cur.cfg`, which `New` filled, against the
  configuration `New` produced from the new input, never against the raw
  input (`src/common/netsim/vswitch/derive.go:29`). Before the fix the raw
  input's zero address diffed against the filled one, and every derived
  switch with an assigned bridge address rebuilt its spanning tree layer.
- `Validate` claims every key the layer indexes on, over the whole
  configuration, not per container. The routing layer keys interfaces by
  name across every VRF (`src/common/netsim/vswitch/routing/layer.go:118`),
  so `Validate` claims each name once across VRFs
  (`src/common/netsim/vswitch/routing/config.go:99`, `:133`). Before the
  fix two VRFs could name an interface alike, and Go map iteration decided
  which VRF the name resolved to on each construction.
- A carried assignment yields to an explicit claim. The fabric copies an
  address from the current fabric only when the new configuration's
  explicit addresses do not already hold it
  (`src/common/netsim/fabric/fabric.go:94`, `:98`); otherwise the node
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

```go
next := New(cfg)
// Both sides as New filled them, so an assigned default is not a change.
if len(stp.Diff(*cur.cfg.STP, *next.cfg.STP)) == 0 { ... }
```

## Evidence

- `src/common/netsim/vswitch/switch_test.go:1243`
  `TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress`: a switch with a
  zero bridge address, converged as root port, keeps Root/Forwarding through
  `Derive` with the identical input. It failed before the fix with
  `Disabled/Discarding`.
- `src/common/netsim/vswitch/routing/config_test.go:305`
  `duplicate interface name across VRFs`: `Validate` refuses it. A probe on
  2026-09-11 (macOS, go 1.27) had `ByVLAN(20)` answer the VLAN 10 interface
  in 10 of 50 constructions before the fix.
- `src/common/netsim/fabric/routing_test.go:575`
  `TestDeriveDoesNotCarryAnAddressTheNewConfigurationClaims`: the switch
  takes the next free address when a host claims the carried one.

## What it does not cover

The rule says where a check runs, not what it checks. A constructor that
fills nothing, or that validates its input itself, has no gap here. The
fabric already applies the first question to switches by handing `Derive`
the config it built (`src/common/netsim/fabric/derive.go`), so the defect
was standalone-switch only.
