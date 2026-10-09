---
title: A Frame Is Attributed to the Layer That Builds It, Not the Layer Whose Call Was Running
date: 2026-10-09
category: architecture-patterns
module: src/common/sim/device/vswitch
problem_type: bug
component: sim
severity: high
symptoms:
  - "A Derive that rebuilt spanning tree and kept LAG returned a BPDU of the replaced configuration"
  - "A Derive that rebuilt LAG dropped a BPDU the kept spanning tree still owed"
root_cause: "The advance loop stamped every emission with the layer table slot whose timer was running. A LAG transition calls the spanning tree and loop-protection effects from inside the LAG layer's call, so a BPDU carried the LAG slot."
resolution_type: code_fix
applies_when:
  - "Tagging an output, trace step, or emission with the component that owns it, when one component's handler runs another's effects"
  - "Filtering a Derive or replay output by which layers the target rebuilt"
  - "Fixing the same mechanism a second time after a test passed for the case the first fix named"
related_components: [derive, stp, lag, loopprotect, routing]
tags: [derive, attribution, provenance, emission, property-tests]
---

# A frame is attributed to the layer that builds it

`Derive` advances a clone of the source to the derive time and prepends what
that advance emitted. A layer the target rebuilt must not speak with its old
configuration, so `Derive` removes the protocol frames of rebuilt layers.
Doing that needs the owner of each frame, and the owner is not the layer whose
timer was running.

## What happened

Commit `8413dbbe` stamped each emission with the table slot of the layer whose
timer produced it. A LAG state change makes `applyLAGEffects` call
`updateLagState`, which runs the spanning tree and loop-protection effects.
The BPDU that followed carried the LAG slot. Commit `775f1dfd` records both
wrong results: rebuilding spanning tree while keeping LAG returned the old
BPDU, and rebuilding LAG dropped a BPDU the kept spanning tree still owed.

## The rule

The function that builds a frame sets its owner. No loop that calls into a
layer stamps what comes out.

```go
s.emit(emission{Port: em.Port, Frame: egress, Protocol: true, slot: tableSTP + 1})
```

`src/common/sim/device/vswitch/timers.go:67,75` (spanning tree),
`:117` (loop protection), `:123` (LAG), and `emitRouting`. The field comment at
`timers.go:45-50` states the invariant: "The function that builds a frame sets
it, so the owner does not depend on which layer's timer ran the call." `Derive`
then withholds on that slot (`src/common/sim/device/vswitch/derive.go:112`):

```go
return em.Hold == 0 && em.slot > 0 && !layerKept[em.slot-1]
```

A released held frame carries a hold token and is never withheld, whatever the
layer that released it.

## Prove it by generating the cases

Two fix rounds on one mechanism ended only when the cases were generated from
the dimensions. `TestDeriveReturnsNoProtocolFrameOfRebuiltLayer`
(`src/common/sim/device/vswitch/derive_layer_ownership_test.go:94`) takes five
protocol sources by the sets of rebuilt layers, a held frame that times out in
the replayed advance, and a neighbor solicitation with routing kept and
rebuilt, 21 cases in all. It classifies each returned frame by decoding it
with the layer's own codec (`decodeProtocolLayer`, `:41`), never by the slot,
so the check cannot share the stamp's mistake. The same approach closed the
mirror defect: `TestMirrorCopiesFrameOncePerDestinationAcrossHistories`
(`src/common/sim/device/vswitch/mirror_once_test.go`) crosses five selectors
with six histories, and commits `362af42e` and `bd3a2f74` needed two rounds
before it existed.

Mutation recorded in `775f1dfd`: stamp the table position on everything an
apply step emits, as the advance loop did.

```text
--- FAIL: TestDeriveReturnsNoProtocolFrameOfRebuiltLayer/spanning_tree_through_a_LAG_transition/rebuilt_{stp:true_lag:false_loop:false}
```

## What it does not cover

The slot says which layer built the frame, not whether the layer's new
configuration would still send it. A kept layer's frame survives a rebuild of
another layer on purpose. `loopprotect.LinkChange` returns no effects today, so
the loop-protection-through-LAG case guards a probe that does not exist yet.
