# FlowSeer Protobuf Packages

Every package under `spec/proto/flowseer/` is organized along a single axis at
the root: the kind of contract it declares. Roots answer what kind of boundary
a schema governs — reusable primitives, domain models, northbound APIs,
edge-plane services, durable events, integration fabrics, storage formats,
errors, or local runtime mailboxes — before any reader needs to inspect a
message.

## The package tree

```
spec/proto/flowseer/
  net/           Ref-free network primitives with no identity, lifecycle, or tenant
  model/         Entities carrying identity, refs, triads, handles, and shared values
  errs/          Canonical error wire payload
  event/         Durable stream records that are not an entity's own transition
  api/           Northbound Connect services for operators, the web app, and workflows
  edge/          Connect services between central and an enrolled edge, in either direction
  integration/   Reserved for the integration fabric contract; holds only a README
  store/         Private records one process writes or reads at start
  runtime/       Process-local bus contracts and durable mailboxes
```

## Import order between roots

Imports flow strictly upward across boundaries; lower roots never depend on
higher roots. Primitives under `net/` and error payloads under `errs/` are
leaves. `model/` defines identity and shared values that upper boundaries
embed. `event/`, `integration/`, `api/`, and `edge/` are boundary consumers
that import `model/`, `errs/`, and `net/` as needed; `edge/audit` also imports
`event/access` for the record it delivers, so a boundary consumer may import
another when its own contract carries that other's record. A package that
declares a Connect service is a sink and is imported by nothing. `store/`
records embed models and primitives but are imported by no other package.
`runtime/` sits outside the import order as a process-local runtime contract.

The authoritative import order and layering constraints are documented in the
[network model structure record](../../../docs/architecture/2026-08-20-network-model-structure-direction.md)
and enforced by `test/conformance/proto/layering_test.go`.
