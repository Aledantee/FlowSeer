---
title: Mutation Shadow Projection - Direction
type: direction
date: 2026-09-09
topic: mutation-shadow-projection
status: proposed-direction
---

# Mutation Shadow Projection - Direction

## Context

A Mutation Intent (see `CONCEPTS.md`) is recorded centrally, sequenced per
device, executed on the Device Lane, and closed only by verified observation or
abandonment. That contract protects the record of what happened. It does not
protect the operator from asking for something harmful: shutting the port
that carries the management VLAN, tagging a trunk with a VLAN nobody created,
or two operators changing the same uplink from both ends in the same minute.
The device answers only after the change is on the wire, and a FastIron switch
applies a running-config line in under a second with no candidate datastore.

The question was how to see the outcome before applying: an internal shadow
of the device, Open vSwitch, or a network analyzer such as Batfish.

## Status

Deferred on 2026-09-09. The gate only pays off once two things exist: the
Interface Config/State triad, so the committed expectation is a stored
`Config` rather than a masked primitive, and a full-network view (resolved
links between managed devices and their addresses) so the checks can compute
network-wide impact rather than one device's. Neither exists yet. This
record keeps the decision so the work is not re-derived; no plan exists for
it, and one is written when both prerequisites have landed.

## Decision

The gate is a shadow projection computed from FlowSeer's own typed network
model, checked against a fixed set of named invariants, and reported as a
preview diff with a confidence tier. No emulator runs.

- **Projected state** is the last committed expectation for a device plus
  every pending intent ahead of the new one in that device's sequence,
  applied in order. A new intent is checked against the projection, never
  against the last raw observation, so two intents in flight see each
  other.
- **The projection holds intended fields only.** With the Interface triad
  in place the committed expectation is the entity's `Config`, and the
  read-back comparator is the per-family map from `State` fields to the
  `Config` fields they prove. Should the gate be needed before the triad
  lands, a static mask over the primitives (`Interface`, `Vlan`,
  `InterfaceAddress`) that drops observed-only fields gives the same view;
  that fallback was considered and set aside with the deferral.
- **Invariants are cross-entity and cross-device rules**, each named, each
  with a stable identifier: the management path to the device survives,
  every reference resolves after the change, aggregation members agree,
  the PoE budget holds, no two managed devices claim one address or
  subnet, and a link between two managed devices keeps both ends up and
  their VLAN sets consistent. A violation names the rule, the entities,
  and the intents that contributed.
- **Compare-and-apply on the read set.** An intent records the projection
  head of every device its check read. When the intent reaches the
  `POSSIBLY_APPLIED` checkpoint, the control plane re-runs the check if any
  of those heads moved. Abandonment of an earlier intent moves the head
  and forces the re-check of every later intent that built on it.
- **Three confidence tiers**, stated on every result: model-checked (the
  shadow found no violation), device-validated (the binding's `validate_only`
  accepted the change, where the protocol has one), verified (the read-back
  after apply matches the projection and closes the sequence). A binding
  without native validation says so; nothing pretends to be validated.
- **Placement.** The projection, invariants, and preview live in the device
  service, under its `internal/`. They are used at intent admission and at
  the checkpoint, both central. They move to `src/modules/` only when a second
  host needs them.

## Alternatives

- **Open vSwitch.** Emulates an OVS dataplane, not a vendor control plane.
  It cannot say that FastIron applies immediately, that a LANCOM port carries
  the management VLAN, or how an SG220 handles a trunk change, and it answers
  "does this frame get forwarded" for OVS rather than for the switch in the
  rack. It solves a forwarding question this gate does not ask.
- **Batfish.** Real forwarding and reachability analysis from vendor configs,
  open source and self-hostable, so it passes the vendor rule. It needs full
  configuration snapshots, runs on a JVM, its parsers do not cover ICX, Huawei,
  LANCOM, or SG220, and its grain is a whole snapshot rather than one intent.
  It is a possible later opt-in whole-network check, not the per-intent gate.
- **Device-native validation only.** NETCONF `validate` and RESTCONF dry-run
  exist on some bindings; SNMP and SSH have nothing. It also sees one device,
  so it cannot catch the cross-device conflicts that motivate the gate. It is
  the second tier, not the gate.
- **Build the gate now over a masked primitive.** Possible, and it was
  planned once. Set aside because a gate that sees one device at a time
  cannot compute network-wide impact, which is the outcome the operator
  needs, and the triad decision would then have to be retrofitted into the
  projection store.

## Consequences

- The device service gains a projection store keyed by device with a
  per-device head, and an intent admission path that returns a preview
  before the intent is durably recorded.
- The device-access plan's typed operation schemas
  (`spec/proto/flowseer/device/access/v1/`) should be shaped as typed
  effects an adapter executes and a projection can apply, so the shadow
  consumes them later without a second shape.
- The read-back comparator per family (which State fields prove which
  intended fields) is the same mask read the other way; the verification
  step of the device-access plan reuses it.
- Whether `InterfaceState` carries a copy of applied config is the
  network-model record's decision; the read-back comparator above works
  either way.
- The invariant set grows by adding a rule with a test; no rule is implied
  by another. A rule that needs data the model lacks (spanning-tree state,
  routed reachability) waits for that data rather than guessing.
