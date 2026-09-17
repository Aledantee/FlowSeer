---
title: A Slot That Carries Two Roles Produces a Defect Class, and Each Reader Is a Separate Instance
date: 2026-09-17
last_verified: 2026-09-17
category: architecture-patterns
module: src/common/netsim/vswitch/stp
problem_type: architecture_pattern
component: netsim
severity: high
applies_when:
  - "Reusing an existing identifier, slot, or zero value to mean a second thing, so one field answers two questions depending on mode"
  - "Reviewing a fix for a defect whose shape you have seen before in the same file, and deciding whether to patch it or to make the rule checkable"
  - "Writing a classification table or a reflection test meant to keep a structural rule true as the struct grows"
  - "Deciding whether a lookup that cannot answer should fall back to a default or say so"
related_components: [stp, vswitch, bridge]
tags: [defect-class, invariant, reflection-test, executable-rule, netsim]
---

# One slot, two roles

## The situation

Per-VLAN spanning tree gave VLAN 1's tree the CIST slot, so that every
bridge-level accessor keeps answering about the common tree in all three modes
(`src/common/netsim/vswitch/stp/layer.go:385`, and the direction record at
[`docs/architecture/2026-09-10-virtual-device-direction.md`](../../architecture/2026-09-10-virtual-device-direction.md)).
The decision is sound and is not what this document argues with.

Its consequence is that the CIST slot answers two questions — *what is true of
this link* and *what is true of VLAN 1's tree* — and nothing in the type system
separates them. Six defects followed, each in a different reader or writer:

1. The received priority vector was built in the CIST's slot layout for every
   tree, so a non-VLAN-1 tree compared it against a differently shaped stored
   vector and no BPDU carrying a non-zero root path cost was ever superior.
2. VLAN 1's configured per-port path cost landed on the CIST's port state, which
   is what propagation copies from, so it became every VLAN's cost.
3. A VLAN with no tree silently got VLAN 1's, so a BPDU for an uncarried VLAN
   rewrote VLAN 1's topology.
4. `blockReason` read the tree's own copy of a CIST-only field, so a guarded
   port reported no block reason on any VLAN but 1.
5. The received-BPDU counter was written on the CIST and read per tree, so it
   was permanently zero everywhere else.
6. The migration check wrote `sendRSTP` on the CIST without propagating it, and
   an emission gate added later then silenced every other VLAN for good.

Each was found by inspection. None was found by a test, because each was a
different line and the tests covered behaviour, not the rule.

## What to do instead

**Name the two roles in the type.** The four properties that belong to the link
now live in their own struct, assigned wholesale:

```go
// src/common/netsim/vswitch/stp/layer.go:150
type linkState struct {
	up           bool
	pointToPoint bool
	edge         bool
	sendRSTP     bool
}
```

`syncInstancePorts` (`layer.go:1204`) assigns `mp.linkState = cistP.linkState`,
so a field added to the struct propagates without anyone remembering to copy it.

**Make the classification fail a test, not a review.** A table maps every field
of the enclosing struct to a class, and a reflection walk asserts the class is
true rather than merely recorded:

```go
// src/common/netsim/vswitch/stp/link_state_internal_test.go:138
func walkPortStateFields(t *testing.T, typ reflect.Type, inLinkState bool, seen map[string]bool) {
```

The `inLinkState` argument is the load-bearing part. Without it the table is
inert data: a first version recorded a class per field and never read it, and a
field declared outside the struct but classified as replicated passed green
while reaching no tree.

**Let a lookup that cannot answer say so.** `treeFor` returns `(*tree, bool)`
and answers `false` rather than the CIST for a VLAN it does not run
(`src/common/netsim/vswitch/stp/tree.go:86`). That converts defect 3 from a
wrong answer into a caller's decision.

## What this does not cover, which is the part worth knowing

The gate catches a field **misclassified**. It cannot catch a mutator that
never propagates at all, because it exercises the propagation function's own
per-field behaviour given correct input — it never reaches the call sites.
Defect 6 was exactly that shape, and the repaired gate would still miss it:

> `Mcheck` sets `p.sendRSTP` on the CIST's port and then calls `recomputeAll`,
> which does not propagate. (`src/common/netsim/vswitch/stp/layer.go:904`)

So five of the six defects were misclassification or a wrong-copy read, which
the gate now covers, and one was a missing call, which it does not. Writers of
a link property are statically enumerable in this package today; closing that
half would need a check over write sites, and even then it would not cover a
propagation call placed in the wrong order relative to the recompute that reads
it.

## Why the usual safety nets miss it

- **Every instance is a different line.** Fixing one teaches nothing about the
  next, and two of the fixes re-opened holes their siblings had just closed.
- **Behavioural tests cover the field that motivated them.** Dropping
  `sendRSTP` from propagation fails two behavioural tests today, so the gap
  only bites on the *next* field added, which has none.
- **Planting a violation proves only what the planted case exercises.** A newly
  added field starts at its zero value and is therefore distinguishable; an
  existing field already holding the target value is not. A check that plants a
  value must assert the plant differs from the expected result, or it asserts
  nothing.

## Evidence

- The struct, the propagation, and the two-valued lookup:
  `layer.go:150`, `layer.go:1204`, `tree.go:86`.
- The gate and its structural assertion: `link_state_internal_test.go:62`,
  `:138`.
- Related: a gate can also stop running rather than stop checking; see
  [A Gate Selected By Name Stops Running Silently](../conventions/a-gate-selected-by-name-stops-running-silently.md).
