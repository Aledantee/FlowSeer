# Tenant administration

The `flowseer.api.identity.v1` package holds `TenantService` and
`TenantAdminService`, the Connect services an operator calls to create and
manage tenants and tenant access. The Tenant entity itself, its ref pair,
lifecycle, config, and record live in
[`model/identity/v1`](../../../model/identity/v1/README.md), which this
package imports and returns as `TenantRecord` from every call that hands back
a tenant. Tenant administration returns `Member`, `Role`, and `Partner`
records from the same model package.

## Boundaries

Imports: authz, model/identity

Imported by: nothing

Deliberately absent:

- Tenant deletion. A tenant is suspended rather than deleted so its audit
  trail, historic records, and entity keys stay referentially intact. This
  package has no suspend RPC.
- Identity-provider administration. Operators authenticate against configured
  external identity providers. This service manages FlowSeer membership and
  access records, not provider accounts or tokens.
