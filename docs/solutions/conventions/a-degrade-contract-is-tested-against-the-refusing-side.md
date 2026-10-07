---
title: A Contract That Must Never Reach a Refusal Is Tested By Enumerating the Refusals, Not the Reports That Reached One
date: 2026-09-17
last_verified: 2026-10-04
category: conventions
module: src/common/sim/netmodel
problem_type: convention
component: netmodel
severity: high
applies_when:
  - "Writing or reviewing a translation boundary that promises to degrade bad input into recorded issues rather than fail, such as a loader between an external report and a validated configuration"
  - "A second or third review round finds another input that reaches a refusal the boundary was supposed to guard, and each fix covers the instance in front of it"
  - "Deciding what test holds a contract phrased as a negative, that some component never reaches some other component's error path"
  - "Reconciling engine-owned tuples when source-store identifier rules reject some engine object keys"
  - "Maintaining an ownership table whose every relation must be checked for stale-tuple deletion"
related_components: [analysis, vswitch, conformance-gates, authz, testing]
tags: [testing, contract, translation-boundary, enumeration, degrade, projector, identity-gate]
---

# A contract that must never reach a refusal is tested by enumerating the refusals

## The situation

`netmodel.Load` translates a device report into a switch configuration. It
returns errors for three construction failures and records other invalid
shapes as issues (`src/common/sim/netmodel/netmodel.go:169-172`). Its negative
contract is that no report reaches a validator refusal.

Five review rounds found separate reports that violated this contract. Two
rounds also found defects in earlier fixes.

## What to do instead

Derive the test from the refusing side. Enumerate every rule that can refuse
and record a row that proves degradation, an unreachable guard, or an argument
that no input can express the rule. Collecting only observed failures finds
only the cases already seen.

`TestLoad_RoutedPortRefusalRulesBecomeIssues`
(`src/common/sim/netmodel/routing_test.go:1112`) enumerates nineteen rules from
the switch and routing validators and drives the reachable rows:

```go
// 12. routing.Config.Validate: an interface prefix that is invalid, or
//     IPv4-mapped. The IPv4-mapped case was reachable and unguarded before
//     [parseIP] refused an IPv4-mapped sixteen-octet address ...
//     covered by IPv4MappedInterfaceAddress below. A plain malformed
//     prefix is unreachable: [parsePrefix] only ever hands the VRF a
//     [netip.Prefix] built from an address [parseIP] already accepted.
```

The unreachability arguments must be checkable. A shared capability inference
can make several validator rules rise or fall together, which should be stated
so a reviewer can test the shared premise once.

## Reconciliation coverage includes identities the source store refuses

A projector has the same negative contract when it owns engine tuples derived
from records. An engine can retain an object key that the source store would
refuse to read. Validate that identity before the source lookup and return no
desired tuples, so reconciliation deletes the stale tuple.

`desiredTuples` applies this gate to the tenant, UUID, and platform identities
(`src/services/device/internal/projector/projector.go:212-230`). The property
test uses real stores, crosses every owned relation with identity cases, checks
the exact surviving tuple set, and asserts literal case counts
(`src/services/device/internal/projector/projector_test.go:956-1076`). Its
comment distinguishes an identity-gate deletion from an empty-store deletion.

```go
if obj.Tenant != "" && tenant.Validate(obj.Tenant) != nil {
	return nil, nil
}
```

For any bounded ownership table, tie the test relation list to production,
enumerate each identity class, compare the whole post-pass tuple set, and use
independent literal counts so a dropped axis cannot pass silently.

## Why the instance-at-a-time habit is so durable

A review names the input it found, so the next fix often covers only that
instance. An enumeration makes the remaining risk legible. The question becomes
which disposition is wrong rather than which case was missed.

## Evidence

- The netmodel contract and refusal matrix are at
  `src/common/sim/netmodel/netmodel.go:169-172` and
  `src/common/sim/netmodel/routing_test.go:1112`.
- The projector ownership table is at
  `src/services/device/internal/projector/reconcile.go:29-41`.
- The projector identity matrix and literal guards are at
  `src/services/device/internal/projector/projector_test.go:980-1076`.
- The adapter's model-defined usersets are documented separately in
  `docs/architecture/2026-09-30-operator-authorization-direction.md:970-974`.

## What this does not cover

It does not judge whether validator rules are correct. It holds the boundary to
the rules that exist. Use it where the refusing set is bounded and readable.
The projector case does not define the OpenFGA userset grammar, which belongs
to the authorization direction record.
