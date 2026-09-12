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
- Non-empty decisive trace rules, subjects, and semantic facts. Each is bound
  to an exact [StepExpectation] or [ChangeExpectation].
- The complete ordered semantic trace, including each operation, rule,
  subject, input facts, output facts, and evidence references.
- Expected issue codes paired with exact scopes, plus evidence references when
  non-Complete.
- Structured expectations for each returned comparison, model, or forwarding
  axis.
- Executable fixture binding to library packages.

[AssertCase] executes each fixture twice. It compares outcome and reason,
ordered steps and changes, the evaluated metadata scope, canonical issues,
assumptions, and every evidence entry. Composite results compare both sides of
a switch comparison and the model-loading and forwarding metadata separately.
