# Operator and tenant identity

`flowseer.model.identity.v1` holds the identity of the people and the tenants
FlowSeer serves. It sits at the bottom of the model hierarchy as a leaf
package that imports nothing FlowSeer-owned.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: model/access, model/capture

Deliberately absent:

- A tenant entity. The Tenant entity joins this package in follow-on work.
- An identity provider configuration or token format. An operator is named by
  the stable subject the provider assigns, and authentication details stay
  outside the domain model.
- A ref pair and a triad. OperatorRef names an external identity principal
  rather than an entity FlowSeer persists and reconciles.
