# netsimtest

Package `netsimtest` provides the versioned conformance test corpus and execution
helpers for verifying network simulation contracts.

The package is internal to `src/common/netsim`. It is test support owned by the
simulation library, not a public service, wire schema, benchmark, or UI.

## Admitted cases

The corpus maintains executable scenarios locking the simulation contracts
established across the library:

- `planning/port-vlan-change`: Reconfigures an access switchport from VLAN 10 to
  VLAN 20 and asserts both forwarding sides, their ordered traces, and typed
  diff facts ([bridge.PVIDFact], [bridge.VLANsFact]) without string parsing.
- `topology-shadowing/partial-model-unknown-port`: Loads a device model with one
  unknown operational port via [netmodel.Load]. Proves that the construction
  specification remains usable, readiness is Incomplete strictly for the
  affected port, and sibling known-up ports retain Complete readiness.
- `topology-shadowing/unresolved-transceiver`: A [fabric.Fabric] journey crosses
  a cable with no stated medium. Both ends still agree on an observed speed, so
  the frame is delivered, but the journey is Incomplete with
  `propagation-unknown`: its timing rests on an unidentified transceiver. One
  [Case] asserts one journey, so the sibling delivery the acceptance example
  describes is a second, registered case,
  `topology-shadowing/unresolved-transceiver-known-delivery`, sharing the same
  fixture. It sends between two fully resolved hosts on the same switch and
  stays Complete.
- `topology-shadowing/uncabled-port-definite-drop`: A switch port named in
  `Config.Uncabled` drops a known-unicast frame Complete with `port-down`.
  `Fabric.Metadata` also carries `adjacency-unresolved` for a sibling port
  that is merely omitted; the journey never depends on that port, and its own
  metadata stays clear of it.
- `topology-shadowing/unreported-negotiation`: A host reports no Ethernet facts
  at all, so its link stays Unknown with `capability-unknown` instead of the
  false answer of an assumed 1 Gb/s full-duplex default. A known-unicast frame
  toward it drops Incomplete.
- `topology-shadowing/unknown-uplink-stp`: A redundant uplink between two
  spanning-tree switches is Unknown because one end reports no Ethernet facts.
  A journey forwarded over the other uplink stays Incomplete. It carries
  `protocol-link-unknown` for every STP port its hops consulted, even the ones
  the unknown link never touches.
- `troubleshooting/host-rejects-foreign-unicast`: A fully resolved, Complete
  network path carries a known-unicast frame onto a host's port for a MAC
  that is not the host's own address. The host refuses it under
  `host.mac.unicast_not_addressed`: a rejection decision, isolated from any
  topology uncertainty.
- `troubleshooting/unicast-fdb-forwarding`: Evaluates an access-to-trunk frame
  traversal. Asserts that the decisive lookup rule (`unicast-hit`), VLAN
  classification, egress tag rewrite, and Complete readiness are exposed in
  the trace.

The five `fabric`-based cases execute a [fabric.Fabric] and populate
[ExecutionResult.Journey] and [ExecutionResult.FabricMetadata] alongside the
admitted primary metadata. Journey is the recorded traversal; its own
`Metadata` is what the case's `ExpectedMetadata` asserts. FabricMetadata is
[fabric.Fabric.Metadata], scoped over the whole topology, so it can carry an
issue a given journey never depended on. Both fields are informational: only
[AssertCase]'s determinism check covers them, not admission's exact-match
expectations.

## Admission bar

Every admitted case must define:

- Stable case identifier.
- Use-case class (`planning`, `topology-shadowing`, or `troubleshooting`).
- Evaluated question and false answer prevented.
- Exact primary-result metadata and the domain outcome. The metadata expectation
  includes the evaluated scope, derived status, issues, evidence contents, and
  assumptions.
- Non-empty decisive trace rules, subjects, and semantic facts. Each is bound
  to an exact [StepExpectation] or [ChangeExpectation].
- The complete ordered semantic trace, including each operation, rule,
  subject, input facts, output facts, and evidence references.
- Exact status, scope, issues, evidence contents, and assumptions for each
  returned comparison, model, or forwarding axis. A non-Complete side must
  declare at least one expected issue.
- Executable fixture binding to library packages.

[AssertCase] executes each fixture twice. It compares outcome and reason,
ordered steps and changes, the evaluated metadata scope, canonical issues,
assumptions, and every evidence entry. Composite results compare both sides of
a switch comparison and the model-loading and forwarding metadata separately.
