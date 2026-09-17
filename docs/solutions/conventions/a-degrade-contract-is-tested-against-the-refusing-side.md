---
title: A Contract That Must Never Reach a Refusal Is Tested By Enumerating the Refusals, Not the Reports That Reached One
date: 2026-09-17
last_verified: 2026-09-17
category: conventions
module: src/common/netsim/vswitch/netmodel
problem_type: convention
component: netmodel
severity: high
applies_when:
  - "Writing or reviewing a translation boundary that promises to degrade bad input into recorded issues rather than fail, such as a loader between an external report and a validated configuration"
  - "A second or third review round finds another input that reaches a refusal the boundary was supposed to guard, and each fix covers the instance in front of it"
  - "Deciding what test holds a contract phrased as a negative, that some component never reaches some other component's error path"
related_components: [analysis, vswitch, conformance-gates]
tags: [testing, contract, translation-boundary, enumeration, degrade]
---

# A contract that must never reach a refusal is tested by enumerating the refusals

## The situation

`netmodel.Load` translates a device report into a switch configuration, and
promises that it returns an error only for three conditions that make
construction impossible; everything else becomes a recorded issue
(`src/common/netsim/vswitch/netmodel/netmodel.go:169-172`). The configuration it
builds is then handed to `Validate`, which refuses dozens of shapes. The
contract is therefore a negative: no device report may reach any of those
refusals.

Five review rounds each found another report that did. A parent port reporting
a switchport facet, two rows sharing a name, two sub-interfaces claiming one
parent and VLAN id, a port reported in the spanning-tree table, a directly
attached link-aggregation member, a VLAN interface with an out-of-range id, two
VLAN interfaces on one id, and an IPv4-mapped address or neighbour. Each round
fixed what it was shown, and the next round found the next one. Two rounds found
a defect in the previous round's fix.

## What to do instead

Derive the test from the refusing side, not from the failing side. Open the
validator, enumerate every rule that can refuse, and for each one record a row
that proves the boundary degrades, a guard that makes it unreachable, or an
argument for why no input can express it. Collecting reports that happened to
fail only ever finds what someone already saw.

`TestLoad_RoutedPortRefusalRulesBecomeIssues`
(`src/common/netsim/vswitch/netmodel/routing_test.go:1112`) is that shape. Its
doc comment carries nineteen numbered rules read out of
`src/common/netsim/vswitch/config.go` and
`src/common/netsim/vswitch/routing/config.go`, each with one of the three
dispositions, and its table drives the reachable ones:

```go
// 12. routing.Config.Validate: an interface prefix that is invalid, or
//     IPv4-mapped. The IPv4-mapped case was reachable and unguarded before
//     [parseIP] refused an IPv4-mapped sixteen-octet address ...
//     covered by IPv4MappedInterfaceAddress below. A plain malformed
//     prefix is unreachable: [parsePrefix] only ever hands the VRF a
//     [netip.Prefix] built from an address [parseIP] already accepted.
```

The unreachability arguments carry as much weight as the rows, so write them to
be checkable. Several here rest on one claim, that the loader's capability
inference always implies the relay and VLAN layers alongside routing, which
means those dispositions stand or fall together. Say so where it is true: a
reviewer can then test one thing instead of four.

## Why the instance-at-a-time habit is so durable

A review names the input it found. A fix brief repeats it. A worker closes it
and writes the test that covers it. Nothing in that chain asks what else engages
the same rule, so the class survives every round that does not name it. In this
work the same omission happened three times, twice within one phase, and the
briefs were written by the session that had just seen it happen.

The enumeration is also the only artifact that makes the remaining risk legible.
Once written, the open question stops being "what have we missed" and becomes
"which of these dispositions is wrong", which a reviewer can answer. Two rounds
did exactly that, finding first that the list omitted two rules and had one row
filed under a rule it could not trip, then that it omitted eight more.

## Evidence

- The contract: `src/common/netsim/vswitch/netmodel/netmodel.go:169-172`.
- The enumeration and its dispositions:
  `src/common/netsim/vswitch/netmodel/routing_test.go:1112` onward, nineteen
  rules, nine of them driven as table rows.
- That a row holds its own rule: deleting the VLAN claim tracking at
  `netmodel.go` fails `DuplicateVLANClaim` alone, watched on 2026-09-16, darwin.
- That the boundary was genuinely broken: before `parseIP` refused the mapped
  form, an address row reported as IPv4-mapped parsed, reached the VRF, and was
  refused by `src/common/netsim/vswitch/routing/config.go:315`, so `Load`
  returned an error for an ordinary report.
- A row can be filed under a rule it cannot trip. The invalid-VLAN row first
  used a tag id of 0, which leaves the VLAN at its zero value and reads to the
  validator as no VLAN rather than an invalid one, so deleting the rule's guard
  left validation passing.

## What this does not cover

It says nothing about whether the validator's rules are the right rules; it only
holds the boundary to them. It suits a contract with a bounded, readable set of
refusals on the far side. Where that set is large or open, the enumeration
becomes its own maintenance burden and a generated state space is the better
tool. It is also distinct from
[a gate selected by name](a-gate-selected-by-name-stops-running-silently.md),
which is about a check that stops running; here every check ran and passed, and
the gap was in what they covered.
