# Operator identity

`flowseer.model.identity.v1` holds operator identity (people, named by the
identity provider's subject). It sits at the bottom of the model hierarchy as a
leaf package that imports nothing FlowSeer-owned.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: model/access, model/capture

Deliberately absent:

- A tenant entity. The keyless TenantRef and Tenant sit in model/inventory/v1
  until the tenant entity the
  [operator authorization record](../../../../../../docs/architecture/2026-09-28-operator-authorization-direction.md)
  decides replaces them here.
- An identity provider configuration or token format. An operator is named by
  the stable subject the provider assigns, and authentication details stay
  outside the domain model.
- A ref pair and a triad. OperatorRef names an external identity principal
  rather than an entity FlowSeer persists and reconciles.
