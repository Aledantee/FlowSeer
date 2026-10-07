# Identity

`flowseer.model.identity.v1` holds operator and tenant identity plus the
tenant-scoped access records that connect operators to roles, full-payload
grants, and service-provider tenants. It sits at the bottom of the model
hierarchy as a leaf package that imports no other FlowSeer-owned package.

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
- Config, State, and Event siblings for `Member`, `Role`, `FullPayloadGrant`,
  and `Partner`. These are tenant-partitioned records whose changes are
  carried by the operator action event stream.

## Access records

`Role` grants one to four tenant relations to every assigned member.
`Member` records enrollment, role assignments, and an optional time-bounded
full-payload grant. `Partner` records the customer's one-sided link to a
service-provider tenant and grants that provider's active administrators one
to three operational relations. The API and store supply tenant ownership and
timestamps. These messages carry the record itself.
