# Authorization rule schema

The `flowseer.authz.v1` package holds `Rule` and `RuleMode`, declaring how each
operator RPC under `flowseer.api.` is authorized. The
[operator authorization record](../../../../../docs/architecture/2026-09-30-operator-authorization-direction.md)
fixes the rule modes and relation checks. This package is the schema contract
alone.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/capture, api/device, api/edge, api/identity

Deliberately absent:

- Any engine-specific configuration: store ids, model definitions, tuple
  encodings. The rule declares what relation and object an RPC requires, not
  which engine evaluates it.
- Client-side token or claim structures. Authentication verifies identity
  before the authorization interceptor inspects the rule.
