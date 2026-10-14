---
title: A State Fingerprint Used as a Convergence Oracle Lies When It Omits a Resolution Axis or Keeps a Timer
date: 2026-09-18
category: conventions
module: src/common/sim/fabric
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


A convergence fingerprint can report a settled network while a missing axis
still resolves. It can also keep changing after settlement when it includes
periodic timer state. Both failures shipped in netsim's first fabric
fingerprint and passed a green suite.

## The two failures

The fingerprint sampled spanning-tree roles per VLAN through
`configuredVLANs`. Under MST, the CIST was not sampled because only the PVST
branch added VLAN 1. A CIST-only transition then changed nothing the oracle
could see. The neighbor table had the same omission: `Device.Neighbors`
was excluded, so unresolved and resolved bindings could fingerprint alike.

The switch's current `State` samples the CIST through `Roles` and stores
sampled trees by `device.TreeRef`
(`Switch.State` in `src/common/sim/device/vswitch/state.go`). A VLAN 1 assignment to an
MSTI therefore keeps both trees in the snapshot.
`TestStateKeepsCISTWhenVLANOneIsAssignedToMSTI`
(in `src/common/sim/device/vswitch/state_test.go`) asserts CIST 0 and MSTI 1
with their port rows. `State` classifies both Roles and TreeRoles, and
neighbors including VRF and hold depth, as behavioral
(`stateFields` in `src/common/sim/device/state.go`).

The clock, arrival queue, port counters, and relay counters can change
without the network becoming unsettled. So can the four STP counters:
TxBPDUs, RxBPDUs, ForwardTransitions, and BadBPDUs. They remain diagnostic,
and layer timer timestamps stay outside the behavioral projection
(`portInfoFields` and `appendStateValues` in `src/common/sim/device/state.go`).
`TestFingerprintTimerInsensitive`
(`src/common/sim/fabric/fingerprint_test.go`) checks that diagnostic
changes leave the fingerprint alone. `TestFingerprintNeighborResolutionState`
distinguishes resolution changes from an expiry-only advance.
`TestFingerprintUnchangedAcrossHelloWake` asserts an Up link and
a BPDU crossing before comparing periodic hello fingerprints.

## The rule

Include the axes whose motion means unsettled. Exclude timestamps and
counters that change while the behavioral state stays settled. The
[virtual-device direction](../../architecture/2026-09-10-virtual-device-direction.md)
states this convergence split in its "Scenarios, run completion, and convergence" section. A record that resolves over
time keeps its resolution state in the projection while exposing its timer
in the snapshot.

A field-classification walk proves that every field was classified. It
cannot prove that an encoder reads those decisions. Netsim's first walk
drove nothing, and the hand-written encoder could omit a field marked
included. The current `State.Fingerprint` and `State.Diff` both read
`stateFields` and `portInfoFields` through the same field walker
(`State.Fingerprint`, `State.Diff`, and `projection` in `src/common/sim/device/state.go`).

Perturb each behavioral field and require both outputs to differ. Perturb
each diagnostic field and require both outputs to stay equal.
`TestStateProjection` (`src/common/sim/device/state_test.go`) checks
18 cases and rejects an unclassified field. Removing the Entries class
produces `State.Entries has no class`. Including TxBPDUs fails its
diagnostic case. The fabric-level field-perturbation test is preserved in
`TestFingerprintIncludedFieldsAffectFingerprint`
(`src/common/sim/fabric/fingerprint_test.go`).

This is the same unused-table failure as
[a reflection perturbation gate that copies structs by field](./a-perturbation-gate-that-copies-structs-by-field-passes-vacuously.md)
and [a slot that carries two roles](../architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md).
A classification table earns its keep when the projection reads it.

## What it does not cover

The classification is domain judgment. The checks keep the declared split
complete and observable. They cannot decide which motion means unsettled.
A changed output also does not prove an injective encoding. The projection
quotes field paths and values independently
(`fingerprintStateValues` in `src/common/sim/device/state.go`), and
`TestFingerprintEscapesTheTreeKind`
(`src/common/sim/fabric/fingerprint_test.go`) checks a tree-kind string
containing separators through the public fingerprint.
