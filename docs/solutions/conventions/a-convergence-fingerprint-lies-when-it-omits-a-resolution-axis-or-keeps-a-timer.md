---
title: A State Fingerprint Used as a Convergence Oracle Lies When It Omits a Resolution Axis or Keeps a Timer
date: 2026-09-18
category: conventions
module: src/common/netsim/fabric
problem_type: convention
component: convergence
severity: high
applies_when:
  - "Building or extending a canonical state fingerprint (or hash) that a caller compares across steps to decide convergence, oscillation, or run equality."
  - "Deciding which fields of a snapshot belong in such a fingerprint, especially state that resolves over time (spanning-tree roles, neighbor/ARP resolution, group membership)."
  - "Reviewing a reflection walk that classifies each field include/exclude, to judge whether it actually proves the included fields reach the output."
related_components: [netsim, vswitch, analysis]
tags: [fingerprint, convergence, oracle, false-positive, reflection-walk]
---

A fingerprint that a caller compares step-to-step to decide "has this settled"
is an oracle, and it lies in two opposite ways. Omit an axis of state that is
still resolving and the oracle reads *converged* while that axis moves — a false
positive. Keep a field that advances on a timer and the oracle never repeats, so
convergence *never* fires and oscillation reads spuriously — a false negative.
Both shipped in netsim's first fabric fingerprint and both were review findings,
not build failures: the suite was green each time.

## The two failures, from the tree

**Omitted resolution axis → false convergence.** The fingerprint sampled
spanning-tree roles per VLAN through `configuredVLANs`, but under MST the CIST
was never sampled: the PVST branch seeded `seen[1]` and the MST branch did not,
so no VLAN mapped to the common tree and the CIST's port state never entered the
fingerprint (`src/common/netsim/vswitch/switch.go:3291`, the fix). A CIST-only
transition then changed nothing the oracle could see. The neighbor table had the
same shape: `Device.Neighbors` was excluded, so a fabric still resolving ARP/ND
fingerprinted identically to a resolved one. Both are now included — per-VLAN
tree roles, and neighbor resolution state with hold depth
(`src/common/netsim/fabric/fingerprint.go:242`).

**Kept timer field → convergence never fires.** The counters, the clock, the
arrival queue, and the per-hello BPDU counters (`TxBPDUs`, `RxBPDUs`,
`ForwardTransitions`) and neighbor expiry timestamps all advance without the
network being unsettled, so every one is excluded. `TestFingerprintTimerInsensitive`
(`src/common/netsim/fabric/fingerprint_test.go:770`) and the neighbor test
(`:1324`, resolution state changes the fingerprint, an expiry-only advance does
not) are the evidence.

## The rule

Include exactly the axes whose motion means *unsettled*, and exclude exactly the
fields that advance on a timer. State the split where a reader will find it (here,
the direction record at
`docs/architecture/2026-09-10-virtual-device-direction.md:338-340`), and for a
record that resolves over time, split the resolving state (include) from its
expiry timestamp (exclude) rather than dropping the record whole.

A reflection walk that forces an include/exclude classification on every field —
so a newly added field fails the walk until someone classifies it — is
necessary but not sufficient: it proves the field was *decided*, not that an
`included` field actually reaches the fingerprint. netsim's first walk drove
nothing; the encoder was hand-written, so a field could be marked `included` and
silently never encoded. Close that gap by asserting each included field *changes
the output*: perturb it and require the fingerprint to differ
(`TestFingerprintIncludedFieldsAffectFingerprint`,
`src/common/netsim/fabric/fingerprint_test.go:963`). This is the same
"the table nothing reads is inert" failure as
[a reflection perturbation gate that copies structs by field](./a-perturbation-gate-that-copies-structs-by-field-passes-vacuously.md)
and [a slot that carries two roles](../architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md);
a classification table earns its keep only when something reads it back.

## What it does not cover

The include/exclude decision is domain judgement, not a mechanical rule: only
someone who knows the protocol can say whether a field's motion means unsettled.
The gate keeps the decision honest and the encoding complete; it cannot make the
decision. And "changes the output" proves an included field is encoded, not that
it is encoded *injectively* — separator escaping (the fingerprint escapes every
string field) is a separate property with its own test.
