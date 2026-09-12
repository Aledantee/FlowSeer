# netsimtest

Package `netsimtest` provides the versioned conformance test corpus and execution
helpers for verifying network simulation contracts.

The package is internal to `src/common/netsim`. It is test support owned by the
simulation library, not a public service, wire schema, benchmark, or UI.

## Admitted cases

The corpus maintains executable scenarios locking the simulation contracts
established across the library:

- `planning/port-vlan-change`: Reconfigures an access switchport from VLAN 10 to
  VLAN 20 and asserts behavioral divergence alongside typed diff facts
  ([bridge.PVIDFact], [bridge.VLANsFact]) without string parsing.
- `topology-shadowing/partial-model-unknown-port`: Loads a device model with one
  unknown operational port via [netmodel.Load]. Proves that the construction
  specification remains usable, readiness is Incomplete strictly for the
  affected port, and sibling known-up ports retain Complete readiness.
- `troubleshooting/unicast-fdb-forwarding`: Evaluates an access-to-trunk frame
  traversal. Asserts that the decisive lookup rule (`unicast-hit`), VLAN
  classification, egress tag rewrite, and Complete readiness are exposed in
  the trace.

## Admission bar

Every admitted case must define:

- Stable case identifier.
- Use-case class (`planning`, `topology-shadowing`, or `troubleshooting`).
- Evaluated question and false answer prevented.
- Expected analysis status and domain outcome.
- Decisive trace rules, subjects, and semantic facts.
- Expected issue codes, scopes, and evidence references when non-Complete.
- Deterministic reproducibility invariants.
- Executable fixture binding to library packages.
