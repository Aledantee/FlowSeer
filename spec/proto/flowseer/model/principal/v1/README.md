# Principals

`flowseer.model.principal.v1` names whoever an authorization check is about.
Today that is one message, `OperatorRef`: a person, named by the stable
subject the identity provider assigns. A mutation intent says who asked for
it with one (`model/access`, `Actor.operator`), and a capture session says
who requested it with one (`model/capture`,
`CaptureAuthorization.requested_by`). Both import this leaf, and neither
imports the other, so the edge's capture schemas never reach the access
plane to name a person.

```prototext
subject: "zitadel|usr_123"
```

## Not a shared refs package

The [protobuf conventions](../../../../../../docs/conventions/protobuf.md)
forbid a package that holds every ref, because it would have to know every
entity above it. `OperatorRef` is not an entity's ref. It keys a subject the
identity provider owns, it has no `LocalRef`/`GlobalRef` pair and no triad,
and nothing FlowSeer stores answers for it. The package therefore imports
nothing and knows nothing above it, which is what that rule protects.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: model/access, model/capture

Deliberately absent:

- A verified identity. The subject is what the caller writes; nothing checks
  it until caller authentication lands with the OpenFGA record for the
  operator and admin services.
- A service or workload principal. None is needed yet.
- A tenant. Scope is ambient.
