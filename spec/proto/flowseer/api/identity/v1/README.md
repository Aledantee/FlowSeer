# Tenant administration

The `flowseer.api.identity.v1` package holds `TenantService`, the Connect
service an operator calls to create, retrieve, and list tenants. The Tenant
entity itself, its ref pair, lifecycle, config, and record live in
[`model/identity/v1`](../../../model/identity/v1/README.md), which this
package imports and returns as `TenantRecord` from every call that hands
back a tenant.

## Boundaries

Imports: model/identity

Imported by: nothing

Deliberately absent:

- Tenant deletion. No RPC suspends a tenant yet.
- Operator management RPCs. Operators authenticate against their external
  identity provider and are authorized through relationship tuples rather
  than user records managed in this service.
