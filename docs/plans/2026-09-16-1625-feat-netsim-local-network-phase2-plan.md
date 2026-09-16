---
title: Local Network Analysis Phase 2 - Routed Sub-Interfaces - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md
---

# Local Network Analysis Phase 2 - Routed Sub-Interfaces - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A firewall attached to a trunk port routes between its VLAN sub-interfaces.
The means: `routing.Interface` accepts `Port` and `VLAN` together, the switch
classifies frames on a routed port by their outer C-TAG and pushes the tag on
egress, and `netmodel` loads the schema's `Subinterface` kind. The plan is
wrong if the routed-port path cannot classify without the bridge, which
would mean the parent port has to become a switchport and the ban at
`src/common/netsim/vswitch/config.go:316-335` has to fall.

## Decisions

The parent's decision on sub-interfaces applies. In addition:

- The parent port of a sub-interface may also carry one untagged routed
  interface (`Port` set, `VLAN` zero), and (port, VID) pairs are unique
  across VRFs. Why: a firewall commonly has a native VLAN on its trunk, and
  the current `claimedPorts` map (`src/common/netsim/vswitch/routing/config.go:246-255`)
  becomes a map from port to VID set with zero meaning untagged.
- A frame on a routed port whose outer tag names no sub-interface, or that
  is untagged where no untagged interface exists, drops with
  `routing.ReasonNotBridged` and a fact naming the port and VID. Why: the
  port is not a switchport, so there is no bridge to fall back to, and the
  reason already exists (`src/common/netsim/vswitch/switch.go:812-819`).
- Egress pushes one C-TAG with the interface's VID and PCP 0. Why: nothing
  in routing carries a priority decision; a later QoS layer may rewrite it.
- `netmodel` accepts a `Subinterface` whose parent is a physical or LAG
  interface and whose encapsulation is exactly one tag with TPID unset or
  `0x8100`; anything else raises
  `netmodel.routing.unsupported_encapsulation` on the interface's ownership
  scope, and a parent that is itself a VLAN interface or unknown raises
  `netmodel.routing.unsupported_interface_kind`. Why: QinQ and non-C-TAG
  encapsulation have no routing model here, and an unsupported input must
  degrade the interface's own scope rather than fail the load.

## Requirements

Parent R4 and R5, plus:

1. A sub-interface's parent port is refused as a switchport or STP port at
   construction, as a routed port is today. Example: `eth1.10` on `eth1`
   with `eth1` in `Bridge.VLAN.Switchports` fails `Validate` naming
   `routing.vrfs.default.interfaces.eth1.10.port`.
2. Two sub-interfaces with the same (port, VID) fail validation; the same
   VID on two ports is allowed. Example: `eth1.10` and `eth2.10` both load;
   `eth1.10` twice fails.
3. Diff names the sub-interface's `port` and `vlan` fields as it does today
   (`src/common/netsim/vswitch/routing/diff.go:268-281`); no new fact type.
4. The routed-port fast path keeps consulting `PortLookupScope` on a miss,
   so `TestNegativeRoutingCapabilityLookupsRetainScopedUncertainty`
   (`src/common/netsim/vswitch/dependency_metadata_test.go:410`) still holds
   with a tagged miss.

## Out of scope

- A sub-interface on a bridge switchport (router-on-a-stick over the
  bridge), which the VLAN interface already models.
- QinQ sub-interfaces and non-`0x8100` TPIDs.
- Static routes in the schema; `netmodel` still leaves `vrf.Routes` empty.

## Open questions

- Whether `phy` and PoE facts of the parent port need any change when a
  routed port carries tags. The plan expects none.
