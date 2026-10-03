# Identity

`flowseer.model.identity.v1` holds operator and tenant identity. It sits at the
bottom of the model hierarchy as a leaf package that imports nothing
FlowSeer-owned.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/identity, event/operator, model/access, model/capture

Deliberately absent:

- An identity provider configuration or token format. An operator is named by
  the issuer and stable subject the provider assigns, and authentication
  details stay outside the domain model.
- A ref pair and triad for `OperatorRef`. `OperatorRef` names an external
  identity principal rather than an entity FlowSeer persists and reconciles;
  `Tenant` holds the package's ref pair and triad.
